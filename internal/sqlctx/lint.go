package sqlctx

import (
	"fmt"
	"strings"
)

// Schema is the minimal schema interface required by Lint.
type Schema interface {
	HasTable(name string) bool
	HasColumn(table, column string) bool
}

// Issue is a single linting problem at a byte range.
type Issue struct {
	Start, End int
	Message    string
}

// Lint walks sql and reports identifiers that look like table or
// column references but cannot be resolved against s.
//
// The current implementation only flags references that are
// unambiguous: table names following FROM/JOIN/INTO/UPDATE,
// qualifiers that resolve to neither a table nor a known alias,
// and qualified columns whose left-hand-side resolves to a real
// table but the right-hand-side is not a column of that table.
//
// Bare unqualified identifiers in column position are not flagged
// because they are too easily confused with SELECT-list aliases,
// CTE column names, function calls, or values from outer scopes.
func Lint(sql string, s Schema) []Issue {
	tokens := tokenize(sql)
	info := extractTables(tokens)
	aliases := info.aliases

	var issues []Issue
	for i, t := range tokens {
		if !isIdent(t.text) {
			continue
		}
		if stopWords[strings.ToUpper(t.text)] {
			continue
		}
		// Function call: identifier followed by "(".
		if i+1 < len(tokens) && tokens[i+1].text == "(" {
			continue
		}

		prev := tokenPtr(tokens, i-1)
		next := tokenPtr(tokens, i+1)

		// Identifier acting as a qualifier ("X." part).
		if next != nil && next.text == "." {
			// `FROM/JOIN/INTO/UPDATE schema.table` — schema name,
			// validate at the next ident.
			if prev != nil && isFromKeyword(prev.text) {
				continue
			}
			// `CREATE/ALTER/DROP TABLE schema.table` — DDL target,
			// not a reference. The schema name shouldn't be looked
			// up against the loaded schema (the table is being
			// defined / altered / dropped, the schema name is just
			// part of the fully-qualified identifier).
			if prev != nil && strings.EqualFold(prev.text, "TABLE") {
				continue
			}
			if _, ok := aliases[t.text]; ok {
				continue
			}
			if s.HasTable(t.text) {
				continue
			}
			issues = append(issues, Issue{
				Start:   t.start,
				End:     t.end,
				Message: fmt.Sprintf("unknown table or alias %q", t.text),
			})
			continue
		}

		// Identifier acting as a qualified column (".Y" part).
		if prev != nil && prev.text == "." {
			prev2 := tokenPtr(tokens, i-2)
			if prev2 != nil && isIdent(prev2.text) && !stopWords[strings.ToUpper(prev2.text)] {
				// `FROM/JOIN/INTO/UPDATE schema.table` — the right
				// side is a bare table reference, not a qualified
				// column. Validate it as a real schema table; do
				// NOT exempt virtual names (CTE / subquery
				// aliases) the way the bare-table branch does,
				// since PostgreSQL doesn't allow schema-qualifying
				// a CTE reference and silently accepting it would
				// hide a typo.
				prev3 := tokenPtr(tokens, i-3)
				if prev3 != nil && isFromKeyword(prev3.text) {
					if !s.HasTable(t.text) {
						issues = append(issues, Issue{
							Start:   t.start,
							End:     t.end,
							Message: fmt.Sprintf("unknown table %q", t.text),
						})
					}
					continue
				}
				qualifier := prev2.text
				realName, ok := aliases[qualifier]
				if !ok {
					realName = qualifier
				}
				if !s.HasTable(realName) {
					continue // qualifier already flagged above
				}
				if !s.HasColumn(realName, t.text) {
					issues = append(issues, Issue{
						Start:   t.start,
						End:     t.end,
						Message: fmt.Sprintf("column %q not in table %q", t.text, realName),
					})
				}
				continue
			}
		}

		// Bare table reference: identifier directly after FROM/JOIN/INTO/UPDATE.
		// Virtual tables (CTE / subquery aliases) are accepted silently;
		// only names that are neither real nor virtual get flagged.
		if prev != nil && isFromKeyword(prev.text) {
			if !s.HasTable(t.text) && !info.virtual[t.text] {
				issues = append(issues, Issue{
					Start:   t.start,
					End:     t.end,
					Message: fmt.Sprintf("unknown table %q", t.text),
				})
			}
		}
	}
	return issues
}

func isFromKeyword(s string) bool {
	switch strings.ToUpper(s) {
	case "FROM", "JOIN", "INTO", "UPDATE":
		return true
	}
	return false
}

func tokenPtr(tokens []token, i int) *token {
	if i < 0 || i >= len(tokens) {
		return nil
	}
	return &tokens[i]
}
