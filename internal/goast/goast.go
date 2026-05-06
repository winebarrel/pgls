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

// SQLFunctions is the set of Go function/method names whose string
// literal arguments are interpreted as SQL. Method names are matched
// without a receiver, so "Query" matches both `db.Query(...)` and
// `tx.Query(...)`.
type SQLFunctions = map[string]bool

// DefaultSQLFunctions returns the database/sql DB / Tx methods that
// take a query string. Callers can use this verbatim, extend it, or
// supply their own set entirely (an empty set disables function-call
// detection so only the language=sql marker comment fires).
func DefaultSQLFunctions() SQLFunctions {
	return SQLFunctions{
		"Query":            true,
		"QueryRow":         true,
		"QueryContext":     true,
		"QueryRowContext":  true,
		"Exec":             true,
		"ExecContext":      true,
		"Prepare":          true,
		"PrepareContext":   true,
	}
}

// FindAllSQL returns every string literal in src that should be
// treated as SQL, recognised by either:
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

// callSQLPositions returns the set of string-literal positions that are
// passed as the query argument to a call expression whose function name
// matches funcs. Methods are matched by selector name only ("Query"
// covers `db.Query(...)`, `tx.Query(...)`, `*sql.DB.Query(...)`).
//
// Only the *first* string literal among the call's positional args is
// flagged. That handles the standard database/sql shape — Query / Exec
// take the SQL as their first arg, while *Context variants put the
// context first (a non-literal) and the SQL as the next arg, which is
// still the first literal seen. The single-literal rule also avoids
// false positives like `db.Exec("INSERT ...", "literal_value")`, where
// the second string literal is a parameter value, not SQL.
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
		if name == "" || !funcs[name] {
			return true
		}
		for _, arg := range call.Args {
			if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out[lit.Pos()] = true
				break
			}
		}
		return true
	})
	return out
}

func callFuncName(fun ast.Expr) string {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
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
