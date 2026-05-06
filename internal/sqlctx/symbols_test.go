package sqlctx

import "testing"

func findSymbol(syms []Symbol, name string, kind SymbolKind) *Symbol {
	for i := range syms {
		if syms[i].Name == name && syms[i].Kind == kind {
			return &syms[i]
		}
	}
	return nil
}

func TestWalkSymbols_TableReference(t *testing.T) {
	syms, _, _ := WalkSymbols("SELECT * FROM users")
	if findSymbol(syms, "users", SymbolTable) == nil {
		t.Errorf("missing SymbolTable for users; got %+v", syms)
	}
}

func TestWalkSymbols_QualifierAndColumn(t *testing.T) {
	syms, aliases, _ := WalkSymbols("SELECT u.email FROM users u")
	if findSymbol(syms, "u", SymbolQualifier) == nil {
		t.Error("missing qualifier u")
	}
	col := findSymbol(syms, "email", SymbolQualifiedColumn)
	if col == nil {
		t.Fatal("missing qualified column email")
	}
	if col.Qualifier != "u" {
		t.Errorf("col.Qualifier=%q, want u", col.Qualifier)
	}
	if aliases["u"] != "users" {
		t.Errorf("aliases[u]=%q, want users", aliases["u"])
	}
}

func TestWalkSymbols_SkipsFunctionCalls(t *testing.T) {
	// "now()" — "now" is followed by "(" so it's a function call, not a column.
	syms, _, _ := WalkSymbols("SELECT now() FROM users")
	if findSymbol(syms, "now", SymbolQualifier) != nil ||
		findSymbol(syms, "now", SymbolTable) != nil ||
		findSymbol(syms, "now", SymbolQualifiedColumn) != nil {
		t.Errorf("now() leaked into symbols: %+v", syms)
	}
}

func TestWalkSymbols_SkipsSchemaQualifier(t *testing.T) {
	// "FROM public.users" — "public" is a schema name, not a table reference.
	syms, _, _ := WalkSymbols("SELECT * FROM public.users")
	if findSymbol(syms, "public", SymbolQualifier) != nil {
		t.Error("public should not be emitted as a qualifier")
	}
	if findSymbol(syms, "users", SymbolQualifiedColumn) == nil {
		t.Errorf("expected users as qualified column part; got %+v", syms)
	}
}

func TestWalkSymbols_SkipsKeywords(t *testing.T) {
	// "IS NULL", "AND" etc. should not appear as symbols.
	syms, _, _ := WalkSymbols("SELECT id FROM users WHERE id IS NOT NULL AND email LIKE 'a%'")
	for _, s := range syms {
		switch s.Name {
		case "IS", "NOT", "NULL", "AND", "LIKE", "WHERE", "FROM", "SELECT":
			t.Errorf("keyword %q leaked as symbol kind=%d", s.Name, s.Kind)
		}
	}
}

func TestWalkSymbols_CTE(t *testing.T) {
	syms, aliases, virtual := WalkSymbols(
		"WITH active AS (SELECT id FROM users) SELECT * FROM active")
	if !virtual["active"] {
		t.Errorf("active should be virtual; got %v", virtual)
	}
	if aliases["active"] != "active" {
		t.Errorf("alias chain wrong: %v", aliases)
	}
	if findSymbol(syms, "active", SymbolTable) == nil {
		t.Errorf("FROM active should emit SymbolTable; got %+v", syms)
	}
}
