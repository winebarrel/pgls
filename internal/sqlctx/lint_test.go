package sqlctx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestLint_SchemaQualifiedUnknownTable(t *testing.T) {
	// `FROM schema.table` — when the table doesn't exist, lint should
	// flag it. Previously the linear walker fell into the
	// qualified-column branch, found that "public" wasn't a table, and
	// silently skipped — a false negative.
	s := makeSchema()
	issues := Lint("SELECT * FROM public.bogus", s)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "bogus") {
		t.Fatalf("got %v", issueMessages(issues))
	}
}

func TestLint_DDLTableSchemaQualifier(t *testing.T) {
	// CREATE/ALTER/DROP TABLE schema.table — the schema name in the
	// DDL target must not be flagged. Without this exemption the
	// qualifier branch would say `unknown table or alias "public"`
	// because `public` is neither a table nor a registered alias.
	s := makeSchema()
	cases := []string{
		`CREATE TABLE public.users (id bigint PRIMARY KEY)`,
		`ALTER TABLE public.users ADD COLUMN x int`,
		`DROP TABLE public.users`,
	}
	for _, sql := range cases {
		assert.Empty(t, Lint(sql, s), "%q: should not flag schema name in DDL target", sql)
	}
}

func TestLint_SchemaQualifiedAcrossKeywords(t *testing.T) {
	// FROM/JOIN/UPDATE share isFromKeyword in the schema-qualified
	// branch, so they should behave identically. Pin examples for
	// keywords other than FROM (already covered above) so a future
	// per-keyword change can't silently regress.
	//
	// INSERT INTO is intentionally absent: pgls's linear walker
	// treats `ident(` as a function call before the table check
	// even runs, so `INSERT INTO public.bogus (id) VALUES ...` is a
	// pre-existing blind spot for bare and schema-qualified alike.
	// That's a separate scope.
	s := makeSchema()
	cases := []string{
		`SELECT * FROM users JOIN public.bogus ON 1=1`,
		`UPDATE public.bogus SET id = 1`,
	}
	for _, sql := range cases {
		got := Lint(sql, s)
		if len(got) != 1 || !strings.Contains(got[0].Message, "bogus") {
			t.Errorf("%q: got %v", sql, issueMessages(got))
		}
	}
}

func TestLint_SchemaQualifiedKnownTable(t *testing.T) {
	// `FROM public.users` where both "public" (a table) and "users" (a
	// table) exist in the schema must NOT produce
	//   `column "users" not in table "public"`
	// — that was a false positive from misinterpreting the schema
	// qualifier as a table-qualified column reference.
	s := fakeSchema{
		tables: map[string]bool{"users": true, "public": true},
		columns: map[string]map[string]bool{
			"users":  {"id": true},
			"public": {"id": true},
		},
	}
	if got := Lint("SELECT * FROM public.users", s); len(got) != 0 {
		t.Errorf("unexpected: %v", issueMessages(got))
	}
}

func TestLint_SchemaQualifiedCTENameStillFlagged(t *testing.T) {
	// PostgreSQL doesn't let you schema-qualify a CTE reference: the
	// CTE namespace is separate from the schema-qualified table
	// namespace. So `FROM public.active` where `active` is only a
	// CTE must still be flagged as unknown — even though the
	// bare-table branch would accept `FROM active` here.
	s := makeSchema()
	got := Lint(`WITH active AS (SELECT 1) SELECT * FROM public.active`, s)
	if len(got) != 1 || !strings.Contains(got[0].Message, "active") {
		t.Errorf("got %v", issueMessages(got))
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
