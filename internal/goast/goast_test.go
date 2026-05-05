package goast

import (
	"strings"
	"testing"
)

// cursorAt returns the source with the marker removed and the
// (line, character) of where the marker was, both 0-indexed.
func cursorAt(t *testing.T, marked string) ([]byte, int, int) {
	t.Helper()
	const marker = "<|>"
	idx := strings.Index(marked, marker)
	if idx < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	before := marked[:idx]
	src := before + marked[idx+len(marker):]
	line := strings.Count(before, "\n")
	last := strings.LastIndex(before, "\n")
	char := len(before) - (last + 1)
	return []byte(src), line, char
}

func TestFindSQL_Backtick(t *testing.T) {
	src, line, char := cursorAt(t, "package main\n\nfunc main() {\n\tdb.Query(`SELECT id<|> FROM users`)\n}\n")
	sql, off, ok := FindSQL(src, line, char)
	if !ok {
		t.Fatal("want ok=true")
	}
	if !strings.HasPrefix(sql, "SELECT id") {
		t.Errorf("sql=%q", sql)
	}
	if got := sql[:off]; got != "SELECT id" {
		t.Errorf("sql[:off]=%q, want %q", got, "SELECT id")
	}
}

func TestFindSQL_DoubleQuoted(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	q := "INSERT INTO users<|> (id) VALUES (1)"
	_ = q
}
`)
	sql, _, ok := FindSQL(src, line, char)
	if !ok {
		t.Fatal("want ok=true")
	}
	if !strings.HasPrefix(sql, "INSERT INTO") {
		t.Errorf("sql=%q", sql)
	}
}

func TestFindSQL_NotSQLString(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	q := "hello <|>world"
	_ = q
}
`)
	if _, _, ok := FindSQL(src, line, char); ok {
		t.Error("want ok=false for non-SQL string")
	}
}

func TestFindSQL_OutsideString(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	x := 1<|>
	_ = x
}
`)
	if _, _, ok := FindSQL(src, line, char); ok {
		t.Error("want ok=false outside string")
	}
}

func TestFindSQL_MultibyteOnSameLine(t *testing.T) {
	// The string contains 🎉 (surrogate pair: 4 UTF-8 bytes / 2 UTF-16 units)
	// before the cursor on the same line, verifying that LSP UTF-16
	// positions are translated to byte offsets correctly.
	src := []byte("package main\n\nfunc main() {\n\tq := `SELECT 🎉 FROM users`\n}\n")
	// Line 3, UTF-16 char 15 = right after 🎉 (\t=1, q :=⎵=4 [actually 5: q + space + : + = + space], `SELECT⎵=8, 🎉=2 → 1+5+8+1=15)
	sql, off, ok := FindSQL(src, 3, 15)
	if !ok {
		t.Fatal("want ok=true")
	}
	if got := sql[:off]; got != "SELECT 🎉" {
		t.Errorf("sql[:off]=%q, want %q", got, "SELECT 🎉")
	}
}

func TestFindSQL_MultilineBacktick(t *testing.T) {
	src, line, char := cursorAt(t, "package main\n\nfunc main() {\n\tq := `SELECT *\nFROM users\nWHERE id = <|>1`\n\t_ = q\n}\n")
	sql, off, ok := FindSQL(src, line, char)
	if !ok {
		t.Fatal("want ok=true")
	}
	if !strings.Contains(sql[:off], "WHERE id = ") {
		t.Errorf("sql[:off]=%q", sql[:off])
	}
}
