package goast

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/winebarrel/pgls/internal/posenc"
)

var sqlPrefixes = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "WITH",
	"CREATE", "ALTER", "DROP", "TRUNCATE",
}

// FindSQL returns the SQL text and the byte offset of the cursor within
// it when the cursor sits inside a string literal that looks like SQL.
//
// line and character are 0-indexed LSP positions; character is in
// UTF-16 code units (the LSP default).
func FindSQL(src []byte, line, character int) (sql string, offset int, ok bool) {
	cursor := posenc.LSPToByte(src, line, character)

	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, "", src, parser.AllErrors)
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
			found = lit
			return false
		}
		return true
	})
	if found == nil {
		return "", 0, false
	}

	raw := found.Value
	if len(raw) < 2 {
		return "", 0, false
	}
	q := raw[0]
	if q != '"' && q != '`' {
		return "", 0, false
	}
	inner := raw[1 : len(raw)-1]
	if !looksLikeSQL(inner) {
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

func looksLikeSQL(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	if t == "" {
		return false
	}
	upper := strings.ToUpper(t)
	for _, p := range sqlPrefixes {
		if len(upper) > len(p) && strings.HasPrefix(upper, p) {
			next := upper[len(p)]
			if next == ' ' || next == '\t' || next == '\n' || next == '\r' {
				return true
			}
		}
	}
	return false
}

