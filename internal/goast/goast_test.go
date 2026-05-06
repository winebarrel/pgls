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

func TestFindSQL_BacktickWithMarker(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	// language=sql
	q := `+"`SELECT id<|> FROM users`"+`
	_ = q
}
`)
	sql, off, ok := FindSQL(src, line, char, DefaultSQLFunctions())
	if !ok {
		t.Fatal("want ok=true")
	}
	if got := sql[:off]; got != "SELECT id" {
		t.Errorf("sql[:off]=%q, want %q", got, "SELECT id")
	}
}

func TestFindSQL_DoubleQuotedWithMarker(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	// language=postgresql
	q := "INSERT INTO users<|> (id) VALUES (1)"
	_ = q
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); !ok {
		t.Fatal("want ok=true")
	}
}

func TestFindSQL_NoMarker(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	q := `+"`SELECT id<|> FROM users`"+`
	_ = q
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); ok {
		t.Error("want ok=false without marker")
	}
}

func TestFindSQL_MarkerNotImmediatelyAbove(t *testing.T) {
	// Marker comment with a blank line between it and the literal —
	// the marker doesn't apply (must be on the line directly above).
	src, line, char := cursorAt(t, `package main

func main() {
	// language=sql

	q := `+"`SELECT id<|> FROM users`"+`
	_ = q
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); ok {
		t.Error("want ok=false when marker is not immediately above")
	}
}

func TestFindSQL_BlockCommentMarker(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	/* language=SQL */
	q := `+"`SELECT id<|> FROM users`"+`
	_ = q
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); !ok {
		t.Error("want ok=true with /* language=SQL */ marker")
	}
}

func TestFindSQL_OutsideString(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	x := 1<|>
	_ = x
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); ok {
		t.Error("want ok=false outside string")
	}
}

func TestFindSQL_MultibyteOnSameLine(t *testing.T) {
	// Marker required; verify UTF-16 position translation still works.
	src := []byte("package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT 🎉 FROM users`\n}\n")
	// Line 4 (0-indexed), UTF-16 char 15 = right after 🎉
	sql, off, ok := FindSQL(src, 4, 15, DefaultSQLFunctions())
	if !ok {
		t.Fatal("want ok=true")
	}
	if got := sql[:off]; got != "SELECT 🎉" {
		t.Errorf("sql[:off]=%q, want %q", got, "SELECT 🎉")
	}
}

func TestFindAllSQL_OnlyMarked(t *testing.T) {
	src := []byte("package main\n\nfunc main() {\n\t// language=sql\n\tq1 := `SELECT * FROM users`\n\tq2 := `SELECT * FROM orders`\n\t_, _ = q1, q2\n}\n")
	blocks := FindAllSQL(src, DefaultSQLFunctions())
	if len(blocks) != 1 {
		t.Fatalf("want 1 marked block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].SQL, "users") {
		t.Errorf("got %q", blocks[0].SQL)
	}
}

func TestFindSQL_FunctionCallArg(t *testing.T) {
	src, line, char := cursorAt(t, `package main

import "database/sql"

func main(db *sql.DB) {
	_, _ = db.Query(`+"`SELECT id<|> FROM users`"+`)
}
`)
	sql, off, ok := FindSQL(src, line, char, DefaultSQLFunctions())
	if !ok {
		t.Fatal("want ok=true; db.Query arg should be recognised")
	}
	if got := sql[:off]; got != "SELECT id" {
		t.Errorf("sql[:off]=%q, want %q", got, "SELECT id")
	}
}

func TestFindSQL_EmptyFuncsDisablesCallDetection(t *testing.T) {
	src, line, char := cursorAt(t, `package main

import "database/sql"

func main(db *sql.DB) {
	_, _ = db.Query(`+"`SELECT id<|> FROM users`"+`)
}
`)
	if _, _, ok := FindSQL(src, line, char, SQLFunctions{}); ok {
		t.Error("want ok=false: empty function set must disable function-call detection")
	}
}

func TestFindSQL_UnknownFunctionIgnored(t *testing.T) {
	src, line, char := cursorAt(t, `package main

func main() {
	_ = somethingElse(`+"`SELECT id<|> FROM users`"+`)
}

func somethingElse(s string) string { return s }
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); ok {
		t.Error("want ok=false: somethingElse isn't in the SQL function list")
	}
}

func TestFindSQL_OnlyFirstStringLiteralArgIsSQL(t *testing.T) {
	// Per-Copilot review on PR #12: the second string literal argument
	// (a parameter value, not SQL) must NOT be flagged as SQL. Only
	// the first string literal among the call's args counts.
	src, line, char := cursorAt(t, `package main

import "database/sql"

func main(db *sql.DB) {
	_, _ = db.Exec(`+"`INSERT INTO users (email) VALUES ($1)`"+`, "literal<|>_value")
}
`)
	if _, _, ok := FindSQL(src, line, char, DefaultSQLFunctions()); ok {
		t.Error("want ok=false: the second string literal is a value, not SQL")
	}
}

func TestFindAllSQL_FunctionCalls(t *testing.T) {
	src := []byte(`package main

import "database/sql"

func main(db *sql.DB) {
	_, _ = db.Query(` + "`SELECT * FROM users`" + `)
	_, _ = db.Exec(` + "`INSERT INTO orders VALUES (1)`" + `)
}
`)
	blocks := FindAllSQL(src, DefaultSQLFunctions())
	if len(blocks) != 2 {
		t.Fatalf("want 2 SQL strings, got %d: %+v", len(blocks), blocks)
	}
}
