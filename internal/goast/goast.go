package goast

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/winebarrel/pgls/internal/posenc"
)

// SQLString is an SQL-marked string literal found in Go source.
type SQLString struct {
	SQL       string
	StartByte int // byte offset of the inner SQL within the source
}

// SQLFunctions maps a Go method name to the 0-indexed positional
// argument that holds the SQL string. Method names are matched
// without a receiver, so "Query" matches `db.Query(...)`,
// `tx.Query(...)`, `*sql.DB.Query(...)` alike. Bare-identifier calls
// (a same-package `Query("...")` helper) are deliberately not matched
// — see callFuncName.
type SQLFunctions = map[string]int

// DefaultSQLFunctions returns database/sql's DB / Tx methods that take
// a query string, each pointing at the right positional index. Callers
// can use this verbatim, extend it, or supply their own set; an empty
// set disables function-call detection so only the language=sql marker
// fires.
func DefaultSQLFunctions() SQLFunctions {
	return SQLFunctions{
		"Query":           0,
		"QueryRow":        0,
		"Exec":            0,
		"Prepare":         0,
		"QueryContext":    1,
		"QueryRowContext": 1,
		"ExecContext":     1,
		"PrepareContext":  1,
	}
}

// FindAllSQL returns every string literal in src that should be
// treated as SQL, recognized by either:
//   - a JetBrains-style `language=sql` / `language=postgresql` marker
//     comment on the line directly above, or
//   - being passed as an argument to a function/method whose name
//     appears in funcs.
//
// Callers usually pass DefaultSQLFunctions(); empty or nil funcs
// disables the function-call path so only marker comments are honoured.
func FindAllSQL(src []byte, funcs SQLFunctions) []SQLString {
	fset, file, marked := parseWithMarkers(src)
	if file == nil {
		return nil
	}
	inFunc := callSQLPositions(file, funcs)

	var out []SQLString
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if !marked[lit.Pos()] && !inFunc[lit.Pos()] {
			return true
		}
		inner, ok := stripQuotes(lit.Value)
		if !ok {
			return true
		}
		out = append(out, SQLString{
			SQL:       inner,
			StartByte: fset.Position(lit.Pos()).Offset + 1,
		})
		return true
	})
	return out
}

// FindSQL returns the SQL text and the byte offset of the cursor
// within it when the cursor sits inside a string literal that pgls
// recognises as SQL — either via a marker comment or because the
// literal is passed to a function in funcs.
//
// line and character are 0-indexed LSP positions; character is in
// UTF-16 code units (the LSP default).
func FindSQL(src []byte, line, character int, funcs SQLFunctions) (sql string, offset int, ok bool) {
	cursor := posenc.LSPToByte(src, line, character)

	fset, file, marked := parseWithMarkers(src)
	if file == nil {
		return "", 0, false
	}
	inFunc := callSQLPositions(file, funcs)

	var found *ast.BasicLit
	ast.Inspect(file, func(n ast.Node) bool {
		lit, isLit := n.(*ast.BasicLit)
		if !isLit || lit.Kind != token.STRING {
			return true
		}
		start := fset.Position(lit.Pos()).Offset
		end := fset.Position(lit.End()).Offset
		if start <= cursor && cursor <= end {
			if marked[lit.Pos()] || inFunc[lit.Pos()] {
				found = lit
			}
			return false
		}
		return true
	})
	if found == nil {
		return "", 0, false
	}

	inner, ok := stripQuotes(found.Value)
	if !ok {
		return "", 0, false
	}

	innerStart := fset.Position(found.Pos()).Offset + 1
	off := cursor - innerStart
	if off < 0 {
		off = 0
	}
	if off > len(inner) {
		off = len(inner)
	}
	return inner, off, true
}

func stripQuotes(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	q := raw[0]
	if q != '"' && q != '`' {
		return "", false
	}
	return raw[1 : len(raw)-1], true
}

// parseWithMarkers parses src with comments preserved and returns the
// set of BasicLit positions that carry a `language=sql` /
// `language=postgresql` marker on the line directly above.
func parseWithMarkers(src []byte) (*token.FileSet, *ast.File, map[token.Pos]bool) {
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, "", src, parser.AllErrors|parser.ParseComments)
	if file == nil {
		return fset, nil, nil
	}

	commentEndLine := map[int]*ast.CommentGroup{}
	for _, cg := range file.Comments {
		commentEndLine[fset.Position(cg.End()).Line] = cg
	}

	marked := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		litLine := fset.Position(lit.Pos()).Line
		if cg, ok := commentEndLine[litLine-1]; ok && hasSQLMarker(cg.Text()) {
			marked[lit.Pos()] = true
		}
		return true
	})
	return fset, file, marked
}

// callSQLPositions returns the set of string-literal positions that
// occupy the configured query slot of a call to a recognized SQL
// function. Only the slot named in funcs is examined — a parameter
// literal in a different position (e.g. `db.Exec(query, "value")` or
// `db.QueryContext(ctx, q, "value")`) is never misread as SQL.
func callSQLPositions(file *ast.File, funcs SQLFunctions) map[token.Pos]bool {
	if len(funcs) == 0 {
		return nil
	}
	out := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callFuncName(call.Fun)
		if name == "" {
			return true
		}
		idx, ok := funcs[name]
		if !ok || idx < 0 || idx >= len(call.Args) {
			return true
		}
		if lit, ok := unwrapParens(call.Args[idx]).(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out[lit.Pos()] = true
		}
		return true
	})
	return out
}

// unwrapParens peels off any number of *ast.ParenExpr wrappers so a
// literal written as `db.Query((`SELECT ...`))` is still recognised as
// a string literal in the query slot.
func unwrapParens(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// callFuncName returns the trailing identifier of a selector-style
// call expression (e.g. `db.Query(...)` → "Query", `pkg.Query(...)` →
// "Query"), or "" otherwise. Without type information pgls can't
// distinguish a method call from a package-qualified function call —
// both parse as *ast.SelectorExpr — so it matches both forms by name.
// In practice this is fine: configured names are matched without a
// receiver, so a configured "Query" lawfully covers both `db.Query`
// and `somepkg.Query`.
//
// Bare-identifier calls (a same-package `Query("...")` helper) are
// deliberately excluded; matching them would let an unrelated `Query`
// defined elsewhere in the same package accidentally trigger SQL
// handling. Generic instantiations (`x.Query[T](...)` → *ast.IndexExpr,
// `x.Query[T,U](...)` → *ast.IndexListExpr) are unwrapped so they
// still match.
func callFuncName(fun ast.Expr) string {
	for {
		switch fn := fun.(type) {
		case *ast.IndexExpr:
			fun = fn.X
		case *ast.IndexListExpr:
			fun = fn.X
		case *ast.SelectorExpr:
			return fn.Sel.Name
		default:
			return ""
		}
	}
}

// hasSQLMarker reports whether s (a comment group's joined text)
// contains a JetBrains-style language marker for SQL or PostgreSQL.
// The match is case-insensitive and tolerates surrounding whitespace,
// so it accepts "// language=sql", "//language=PostgreSQL",
// "/* language=SQL */", etc.
func hasSQLMarker(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "language=sql") ||
		strings.Contains(lower, "language=postgresql")
}
