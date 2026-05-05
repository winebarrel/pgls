package sqlctx

import (
	"strings"
	"testing"
)

type fakeSchema struct {
	tables  map[string]bool
	columns map[string]map[string]bool
}

func (f fakeSchema) HasTable(name string) bool { return f.tables[name] }
func (f fakeSchema) HasColumn(table, column string) bool {
	cols, ok := f.columns[table]
	return ok && cols[column]
}

func makeSchema() fakeSchema {
	return fakeSchema{
		tables: map[string]bool{"users": true, "orders": true},
		columns: map[string]map[string]bool{
			"users":  {"id": true, "email": true},
			"orders": {"id": true, "user_id": true},
		},
	}
}

func issueMessages(issues []Issue) []string {
	var out []string
	for _, iss := range issues {
		out = append(out, iss.Message)
	}
	return out
}

func TestLint_OK(t *testing.T) {
	s := makeSchema()
	cases := []string{
		"SELECT * FROM users",
		"SELECT u.email FROM users u WHERE u.id = 1",
		"SELECT * FROM users JOIN orders ON users.id = orders.user_id",
		"INSERT INTO users (id, email) VALUES (1, 'a')",
		"UPDATE users SET email = 'a' WHERE id = 1",
		"SELECT count(*) FROM users",
		"SELECT * FROM public.users WHERE users.id IS NOT NULL",
	}
	for _, sql := range cases {
		if got := Lint(sql, s); len(got) != 0 {
			t.Errorf("%q: unexpected issues %v", sql, issueMessages(got))
		}
	}
}

func TestLint_UnknownTable(t *testing.T) {
	s := makeSchema()
	issues := Lint("SELECT * FROM bogus", s)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "bogus") {
		t.Fatalf("got %v", issueMessages(issues))
	}
}

func TestLint_UnknownColumn(t *testing.T) {
	s := makeSchema()
	issues := Lint("SELECT u.bogus FROM users u", s)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "bogus") {
		t.Fatalf("got %v", issueMessages(issues))
	}
}

func TestLint_UnknownQualifier(t *testing.T) {
	s := makeSchema()
	issues := Lint("SELECT bogus.id FROM users", s)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "bogus") {
		t.Fatalf("got %v", issueMessages(issues))
	}
}

func TestLint_QualifierViaTableName(t *testing.T) {
	// users.id where 'users' is the table directly (no alias) should resolve.
	s := makeSchema()
	if got := Lint("SELECT users.id FROM users", s); len(got) != 0 {
		t.Errorf("unexpected: %v", issueMessages(got))
	}
}

func TestLint_JoinUnknown(t *testing.T) {
	s := makeSchema()
	issues := Lint("SELECT * FROM users JOIN nope ON 1=1", s)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "nope") {
		t.Fatalf("got %v", issueMessages(issues))
	}
}

func TestLint_QuotedIdent(t *testing.T) {
	s := makeSchema()
	// Quoted identifier matches the unquoted schema name → no issue.
	if got := Lint(`SELECT * FROM "users"`, s); len(got) != 0 {
		t.Errorf("unexpected: %v", issueMessages(got))
	}
	// Quoted identifier with wrong case must still be flagged: PostgreSQL
	// quoted idents are case-preserving and "Users" ≠ "users".
	got := Lint(`SELECT * FROM "Users"`, s)
	if len(got) != 1 || !strings.Contains(got[0].Message, "Users") {
		t.Errorf("got %v", issueMessages(got))
	}
}

func TestLint_CastOperator(t *testing.T) {
	s := makeSchema()
	if got := Lint(`SELECT u.id::text FROM users u`, s); len(got) != 0 {
		t.Errorf("unexpected: %v", issueMessages(got))
	}
}

func TestLint_CTE(t *testing.T) {
	s := makeSchema()
	cases := []string{
		`WITH active AS (SELECT * FROM users WHERE 1=1) SELECT * FROM active`,
		`WITH RECURSIVE rec AS (SELECT 1) SELECT * FROM rec`,
		`WITH a AS (SELECT * FROM users), b AS (SELECT * FROM orders) SELECT * FROM a JOIN b ON 1=1`,
		`WITH active AS (SELECT id FROM users) SELECT a.id FROM active a WHERE a.id > 0`,
	}
	for _, sql := range cases {
		if got := Lint(sql, s); len(got) != 0 {
			t.Errorf("%q: unexpected %v", sql, issueMessages(got))
		}
	}
}

func TestLint_Subquery(t *testing.T) {
	s := makeSchema()
	cases := []string{
		`SELECT * FROM (SELECT id FROM users) sub`,
		`SELECT sub.id FROM (SELECT id FROM users) sub`,
		`SELECT * FROM users JOIN (SELECT id FROM orders) o ON users.id = o.id`,
	}
	for _, sql := range cases {
		if got := Lint(sql, s); len(got) != 0 {
			t.Errorf("%q: unexpected %v", sql, issueMessages(got))
		}
	}
}

func TestLint_CTEStillFlagsRealTypos(t *testing.T) {
	s := makeSchema()
	// Inner subquery references an unknown table — should still flag.
	got := Lint(`WITH a AS (SELECT * FROM nope) SELECT * FROM a`, s)
	if len(got) != 1 || !strings.Contains(got[0].Message, "nope") {
		t.Errorf("got %v", issueMessages(got))
	}
}

func TestLint_FunctionNotFlagged(t *testing.T) {
	s := makeSchema()
	if got := Lint("SELECT now() FROM users", s); len(got) != 0 {
		t.Errorf("unexpected: %v", issueMessages(got))
	}
}
