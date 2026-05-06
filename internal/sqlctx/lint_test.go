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

func TestLint_NonQueryStatementsAreSkipped(t *testing.T) {
	// pgls's lint allow-lists query verbs (SELECT/INSERT/UPDATE/
	// DELETE/MERGE/WITH/VALUES/EXPLAIN). Everything else — DDL,
	// admin commands, etc. — falls through unchecked, which both
	// avoids spurious diagnostics on schema-defining SQL and
	// keeps pgls future-proof against new DDL keywords.
	s := makeSchema()
	cases := []string{
		`CREATE TABLE public.users (id bigint PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS public.users (id int)`,
		`ALTER TABLE public.users ADD COLUMN x int`,
		`ALTER TABLE ONLY public.users ADD COLUMN x int`,
		`DROP TABLE public.users`,
		`DROP TABLE IF EXISTS public.users`,
		`TRUNCATE public.users`,
		`COMMENT ON TABLE public.users IS 'hello'`,
		`GRANT SELECT ON public.users TO some_role`,
		// Inner unknown-table inside a DDL body (e.g. CREATE VIEW
		// AS SELECT ... FROM bogus) is NOT flagged either — accepted
		// trade-off; pgls focuses on query lint, not DDL lint.
		`CREATE VIEW v AS SELECT * FROM bogus`,
		// Vendor-specific / admin verbs not in the allow-list also
		// get skipped, even though they're not strictly DDL.
		`SET search_path TO public`,
		`COPY users FROM '/tmp/file.csv'`,
	}
	for _, sql := range cases {
		if got := Lint(sql, s); len(got) != 0 {
			t.Errorf("%q: should not flag, got %v", sql, issueMessages(got))
		}
	}
}

func TestLint_QueryAfterNonQueryStillFlagged(t *testing.T) {
	// In a multi-statement file the allow-list applies per
	// statement, so a SELECT after a CREATE still gets linted and
	// surfaces real typos.
	s := makeSchema()
	got := Lint(`CREATE TABLE public.foo (id int); SELECT * FROM bogus`, s)
	if len(got) != 1 || !strings.Contains(got[0].Message, "bogus") {
		t.Errorf("expected exactly one diagnostic for SELECT after DDL, got %v", issueMessages(got))
	}
}

func TestLint_AliasesFromNonQueryDontLeak(t *testing.T) {
	// CREATE VIEW (or any non-query statement) wraps a SELECT that
	// can introduce aliases. Those aliases live inside the DDL's
	// scope only — a later, separate query statement must not see
	// them. Without statement-aware extractTables, the alias `u`
	// from the CREATE leaked across the `;` and silently masked the
	// `unknown table or alias "u"` diagnostic that the second
	// statement actually deserves.
	s := makeSchema()
	sql := `CREATE VIEW v AS SELECT * FROM users u; SELECT u.id FROM bogus`
	got := issueMessages(Lint(sql, s))

	hasU := false
	hasBogus := false
	for _, m := range got {
		if strings.Contains(m, `"u"`) {
			hasU = true
		}
		if strings.Contains(m, "bogus") {
			hasBogus = true
		}
	}
	if !hasU {
		t.Errorf("expected unknown-alias diagnostic for `u` (DDL alias must not leak); got %v", got)
	}
	if !hasBogus {
		t.Errorf("expected unknown-table diagnostic for bogus; got %v", got)
	}
}

func TestLint_ExplainPrefixedStatementsAreSkipped(t *testing.T) {
	// EXPLAIN is intentionally NOT in the allow-list. PostgreSQL
	// accepts EXPLAIN on non-query preparable statements too —
	// `EXPLAIN CREATE TABLE foo AS SELECT ...` is valid, and
	// linting it would re-introduce the schema-name false
	// positive this PR is trying to suppress. The trade-off is
	// that ad-hoc `EXPLAIN SELECT ...` files won't get pgls
	// diagnostics either; we accept that.
	s := makeSchema()
	cases := []string{
		`EXPLAIN SELECT * FROM bogus`,
		`EXPLAIN ANALYZE SELECT * FROM bogus`,
		`EXPLAIN CREATE TABLE public.foo AS SELECT * FROM users`,
	}
	for _, sql := range cases {
		if got := Lint(sql, s); len(got) != 0 {
			t.Errorf("%q: EXPLAIN-prefixed statement should be skipped, got %v", sql, issueMessages(got))
		}
	}
}
