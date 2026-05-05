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

// FindAllSQL returns every string literal in src whose preceding line
// carries a JetBrains-style `language=sql` (or `language=postgresql`)
// marker comment. Used to drive whole-file analyses such as
// diagnostics.
func FindAllSQL(src []byte) []SQLString {
	fset, file, marked := parseWithMarkers(src)
	if file == nil {
		return nil
	}
	var out []SQLString
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if !marked[lit.Pos()] {
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
// within it when the cursor sits inside an SQL-marked string literal.
//
// line and character are 0-indexed LSP positions; character is in
// UTF-16 code units (the LSP default).
func FindSQL(src []byte, line, character int) (sql string, offset int, ok bool) {
	cursor := posenc.LSPToByte(src, line, character)

	fset, file, marked := parseWithMarkers(src)
	if file == nil {
		return "", 0, false
	}

	var found *ast.BasicLit
	ast.Inspect(file, func(n ast.Node) bool {
		lit, isLit := n.(*ast.BasicLit)
		if !isLit || lit.Kind != token.STRING {
			return true
		}
		start := fset.Position(lit.Pos()).Offset
		end := fset.Position(lit.End()).Offset
		if start <= cursor && cursor <= end {
			if marked[lit.Pos()] {
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
