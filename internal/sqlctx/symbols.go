package sqlctx

import "strings"

type SymbolKind int

const (
	// SymbolTable identifies a token in FROM/JOIN/INTO/UPDATE position.
	SymbolTable SymbolKind = iota
	// SymbolQualifier identifies a token followed by "."  (e.g. the
	// "u" in "u.email").
	SymbolQualifier
	// SymbolQualifiedColumn identifies a token preceded by "ident." —
	// the column part of a qualified reference.
	SymbolQualifiedColumn
)

type Symbol struct {
	Kind      SymbolKind
	Name      string
	Qualifier string // populated for SymbolQualifiedColumn
	Start     int    // byte offset within the SQL string
	End       int
}

// WalkSymbols returns every identifier in sql that plausibly resolves
// to a schema table or column, alongside the aliases and virtual
// names (CTE / subquery) collected from the same SQL. Function calls,
// keywords, and identifiers in column-only context are skipped.
func WalkSymbols(sql string) (symbols []Symbol, aliases map[string]string, virtual map[string]bool) {
	tokens := tokenize(sql)
	info := extractTables(tokens)

	for i, t := range tokens {
		if !isIdent(t.text) {
			continue
		}
		if stopWords[strings.ToUpper(t.text)] {
			continue
		}
		// Skip function calls.
		if i+1 < len(tokens) && tokens[i+1].text == "(" {
			continue
		}
		prev := tokenPtr(tokens, i-1)
		next := tokenPtr(tokens, i+1)

		// Identifier acting as a qualifier ("X." part).
		if next != nil && next.text == "." {
			// Schema-qualified table after FROM/JOIN/INTO/UPDATE: the
			// schema name itself isn't a table reference, the next
			// token (the table) is.
			if prev != nil && isFromKeyword(prev.text) {
				continue
			}
			symbols = append(symbols, Symbol{
				Kind:  SymbolQualifier,
				Name:  t.text,
				Start: t.start,
				End:   t.end,
			})
			continue
		}

		// Qualified column part (".Y").
		if prev != nil && prev.text == "." {
			prev2 := tokenPtr(tokens, i-2)
			if prev2 != nil && isIdent(prev2.text) && !stopWords[strings.ToUpper(prev2.text)] {
				symbols = append(symbols, Symbol{
					Kind:      SymbolQualifiedColumn,
					Name:      t.text,
					Qualifier: prev2.text,
					Start:     t.start,
					End:       t.end,
				})
				continue
			}
		}

		// Bare table reference in FROM/JOIN/INTO/UPDATE.
		if prev != nil && isFromKeyword(prev.text) {
			symbols = append(symbols, Symbol{
				Kind:  SymbolTable,
				Name:  t.text,
				Start: t.start,
				End:   t.end,
			})
		}
	}
	return symbols, info.aliases, info.virtual
}
