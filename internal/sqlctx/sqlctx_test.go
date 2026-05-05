package sqlctx

import (
	"strings"
	"testing"
)

func at(t *testing.T, marked string) (string, int) {
	t.Helper()
	const m = "<|>"
	idx := strings.Index(marked, m)
	if idx < 0 {
		t.Fatalf("marker not found")
	}
	return marked[:idx] + marked[idx+len(m):], idx
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

func TestEmpty(t *testing.T) {
	c := Analyze("", 0)
	if c.State != StateUnknown {
		t.Errorf("state=%v", c.State)
	}
}

func TestAfterFrom(t *testing.T) {
	sql, cur := at(t, "SELECT * FROM <|>")
	c := Analyze(sql, cur)
	if c.State != StateTable {
		t.Errorf("state=%v", c.State)
	}
}

func TestAfterSelect(t *testing.T) {
	sql, cur := at(t, "SELECT <|> FROM users")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}

func TestInWhere(t *testing.T) {
	sql, cur := at(t, "SELECT id FROM users WHERE <|>")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}

func TestQualifiedColumn(t *testing.T) {
	sql, cur := at(t, "SELECT u.<|> FROM users u")
	c := Analyze(sql, cur)
	if c.State != StateQualifiedColumn {
		t.Errorf("state=%v", c.State)
	}
	if c.Qualifier != "u" {
		t.Errorf("qualifier=%q", c.Qualifier)
	}
	if c.Aliases["u"] != "users" {
		t.Errorf("aliases=%v", c.Aliases)
	}
}

func TestQualifiedColumnInProgress(t *testing.T) {
	sql, cur := at(t, "SELECT u.id<|> FROM users u")
	c := Analyze(sql, cur)
	if c.State != StateQualifiedColumn {
		t.Errorf("state=%v", c.State)
	}
	if c.Qualifier != "u" {
		t.Errorf("qualifier=%q", c.Qualifier)
	}
}

func TestJoin(t *testing.T) {
	sql, cur := at(t, "SELECT * FROM users JOIN orders ON <|>")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") || !contains(c.FromTables, "orders") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}

func TestInsertColumnList(t *testing.T) {
	sql, cur := at(t, "INSERT INTO users (<|>)")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}

func TestUpdateSet(t *testing.T) {
	sql, cur := at(t, "UPDATE users SET <|>")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}

func TestIdentifierAt(t *testing.T) {
	sql, cur := at(t, "SELECT em<|>ail FROM users")
	id, ok := IdentifierAt(sql, cur)
	if !ok {
		t.Fatal("want ok")
	}
	if id.Name != "email" || id.Qualifier != "" {
		t.Errorf("got %+v", id)
	}
}

func TestIdentifierAtQualified(t *testing.T) {
	sql, cur := at(t, "SELECT u.em<|>ail FROM users u")
	id, ok := IdentifierAt(sql, cur)
	if !ok {
		t.Fatal("want ok")
	}
	if id.Name != "email" || id.Qualifier != "u" {
		t.Errorf("got %+v", id)
	}
}

func TestIdentifierAtTableName(t *testing.T) {
	sql, cur := at(t, "SELECT * FROM us<|>ers")
	id, ok := IdentifierAt(sql, cur)
	if !ok {
		t.Fatal("want ok")
	}
	if id.Name != "users" || id.Qualifier != "" {
		t.Errorf("got %+v", id)
	}
}

func TestIdentifierAtNotIdent(t *testing.T) {
	sql, cur := at(t, "SELECT *<|> FROM users")
	if _, ok := IdentifierAt(sql, cur); ok {
		t.Error("want not ok on '*'")
	}
}

func TestSchemaQualifiedTable(t *testing.T) {
	sql, cur := at(t, "SELECT * FROM public.users WHERE <|>")
	c := Analyze(sql, cur)
	if c.State != StateColumn {
		t.Errorf("state=%v", c.State)
	}
	if !contains(c.FromTables, "users") {
		t.Errorf("fromTables=%v", c.FromTables)
	}
}
