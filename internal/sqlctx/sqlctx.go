// Package sqlctx classifies the cursor location inside a SQL statement
// to determine what kind of identifier should be offered as completion.
package sqlctx

import (
	"regexp"
	"strings"

	pgquery "github.com/wasilibs/go-pgquery"
)

type State int

const (
	StateUnknown State = iota
	StateTable
	StateColumn
	StateQualifiedColumn
)

type Context struct {
	State      State
	Qualifier  string            // for StateQualifiedColumn (alias or table name)
	FromTables []string          // real tables in scope (deduped)
	Aliases    map[string]string // alias -> table name (real or virtual)
	Virtual    map[string]bool   // names introduced by WITH or "(...) alias"
}

// tablesInfo accumulates table-and-alias information while walking SQL.
type tablesInfo struct {
	aliases    map[string]string
	realTables []string
	realSeen   map[string]bool
	virtual    map[string]bool
}

func newTablesInfo() *tablesInfo {
	return &tablesInfo{
		aliases:  map[string]string{},
		realSeen: map[string]bool{},
		virtual:  map[string]bool{},
	}
}

func (i *tablesInfo) addReal(name string) {
	if i.realSeen[name] {
		return
	}
	i.realSeen[name] = true
	i.realTables = append(i.realTables, name)
}

// Identifier describes a single identifier under the cursor for hover/lookup.
type Identifier struct {
	Name      string
	Qualifier string // empty if unqualified
	Start     int    // byte offset within the SQL string
	End       int
}

// IdentifierAt returns the identifier covering the byte offset cursor,
// recognizing a leading "qualifier." prefix when present.
func IdentifierAt(sql string, cursor int) (Identifier, bool) {
	tokens := tokenize(sql)
	for i, t := range tokens {
		if !isIdent(t.text) {
			continue
		}
		if cursor < t.start || cursor > t.end {
			continue
		}
		id := Identifier{Name: t.text, Start: t.start, End: t.end}
		if i >= 2 && tokens[i-1].text == "." && isIdent(tokens[i-2].text) {
			id.Qualifier = tokens[i-2].text
		}
		return id, true
	}
	return Identifier{}, false
}

type token struct {
	text  string
	start int
	end   int
}

// tokenize splits sql into tokens. It uses libpg_query's lexer (which
// handles quoted identifiers, casts, dollar-quoted strings, and the
// full PostgreSQL keyword set), and falls back to a regex tokenizer
// when Scan fails on input the lexer can't recover from.
func tokenize(s string) []token {
	result, err := pgquery.Scan(s)
	if err != nil || result == nil {
		return tokenizeRegex(s)
	}
	out := make([]token, 0, len(result.Tokens))
	for _, t := range result.Tokens {
		if int(t.End) > len(s) || t.Start < 0 || t.Start > t.End {
			continue
		}
		text := s[t.Start:t.End]
		if strings.HasPrefix(text, "--") || strings.HasPrefix(text, "/*") {
			continue
		}
		// Strip surrounding quotes from quoted identifiers ("foo" → foo)
		// so downstream lookups match the unquoted schema names. The
		// original byte range is preserved for diagnostic ranges.
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			text = text[1 : len(text)-1]
		}
		out = append(out, token{text: text, start: int(t.Start), end: int(t.End)})
	}
	return out
}

var tokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|[0-9]+(?:\.[0-9]+)?|'[^']*'|"[^"]*"|--[^\n]*|[.,;()=<>!*+\-/]`)

func tokenizeRegex(s string) []token {
	matches := tokenRe.FindAllStringIndex(s, -1)
	out := make([]token, 0, len(matches))
	for _, m := range matches {
		t := s[m[0]:m[1]]
		if strings.HasPrefix(t, "--") {
			continue
		}
		out = append(out, token{text: t, start: m[0], end: m[1]})
	}
	return out
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// Words that should not be treated as user-defined identifiers
// (table names, aliases, column references). Used both to terminate
// FROM-clause table lists and to skip keyword tokens during linting.
var stopWords = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true,
	"LIMIT": true, "OFFSET": true, "ON": true, "USING": true,
	"SET": true, "VALUES": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true,
	"FROM": true, "AS": true, "AND": true, "OR": true, "NOT": true,
	"INTO": true, "UPDATE": true, "RETURNING": true, "SELECT": true,
	"WITH": true, "UNION": true, "EXCEPT": true, "INTERSECT": true,
	"INSERT": true, "DELETE": true, "TRUNCATE": true, "TABLE": true,
	"ALL": true, "DISTINCT": true, "BY": true, "ASC": true, "DESC": true,
	"NULLS": true, "FIRST": true, "LAST": true,
	"IS": true, "NULL": true, "TRUE": true, "FALSE": true,
	"IN": true, "BETWEEN": true, "LIKE": true, "ILIKE": true,
	"SIMILAR": true, "TO": true, "EXISTS": true, "ANY": true, "SOME": true,
	"CASE": true, "WHEN": true, "THEN": true, "ELSE": true, "END": true,
	"INTEGER": true, "INT": true, "BIGINT": true, "SMALLINT": true,
	"BOOLEAN": true, "BOOL": true, "TEXT": true, "VARCHAR": true, "CHAR": true,
	"DATE": true, "TIME": true, "TIMESTAMP": true, "TIMESTAMPTZ": true,
	"NUMERIC": true, "DECIMAL": true, "REAL": true,
	"DOUBLE": true, "PRECISION": true,
	"JSON": true, "JSONB": true, "UUID": true, "BYTEA": true,
	"IF": true,
}

func Analyze(sql string, cursor int) Context {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(sql) {
		cursor = len(sql)
	}
	all := tokenize(sql)
	info := extractTables(all)
	prefix := tokensBefore(all, cursor)
	state, qualifier := determineState(prefix)
	return Context{
		State:      state,
		Qualifier:  qualifier,
		FromTables: info.realTables,
		Aliases:    info.aliases,
		Virtual:    info.virtual,
	}
}

func tokensBefore(tokens []token, cursor int) []token {
	for i, t := range tokens {
		if t.start >= cursor {
			return tokens[:i]
		}
	}
	return tokens
}

func extractTables(tokens []token) *tablesInfo {
	info := newTablesInfo()
	extractTablesRange(tokens, 0, len(tokens), info)
	return info
}

// extractQueryStatementTables is the lint-scoped version of
// extractTables: it walks the token stream a statement at a time
// (split on `;`) and only collects aliases / real tables / CTE
// names from statements whose leading keyword is a query verb.
// That keeps DDL-only aliases (e.g. `CREATE VIEW v AS SELECT *
// FROM users u`) from leaking into the lint scope of an adjacent
// query statement and silently suppressing real diagnostics.
//
// The non-statement-aware extractTables stays in use for Analyze
// (cursor-position completion), where seeing tables across the
// whole document is the desired behavior.
func extractQueryStatementTables(tokens []token) *tablesInfo {
	info := newTablesInfo()
	start := 0
	for i := 0; i <= len(tokens); i++ {
		if i < len(tokens) && tokens[i].text != ";" {
			continue
		}
		if i > start && isQueryLeadingKeyword(tokens[start].text) {
			extractTablesRange(tokens, start, i, info)
		}
		start = i + 1
	}
	return info
}

// isQueryLeadingKeyword reports whether the leading keyword of a SQL
// statement marks it as a query that pgls's lint should examine.
// The set is intentionally narrow:
//   - SELECT / INSERT / UPDATE / DELETE / MERGE — core DML.
//   - WITH — always feeds one of the above.
//   - VALUES — top-level value list, harmless to lint.
//   - EXPLAIN — wraps another statement; lint sees through to it.
//
// Allow-listing rather than blocklisting DDL keeps pgls future-proof:
// query verbs are small and stable, while DDL gets new verbs across
// PostgreSQL versions. A missed DDL keyword would re-introduce false
// positives; an unrecognised DML form simply isn't validated.
func isQueryLeadingKeyword(s string) bool {
	switch strings.ToUpper(s) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE",
		"WITH", "VALUES", "EXPLAIN":
		return true
	}
	return false
}

func extractTablesRange(tokens []token, start, end int, info *tablesInfo) {
	i := start
	for i < end {
		upper := strings.ToUpper(tokens[i].text)
		switch upper {
		case "FROM", "JOIN", "INTO", "UPDATE":
			i = readTableList(tokens, i+1, end, info)
			continue
		case "WITH":
			j := i + 1
			if j < end && strings.ToUpper(tokens[j].text) == "RECURSIVE" {
				j++
			}
			i = readCTEDefinitions(tokens, j, end, info)
			continue
		}
		i++
	}
}

func readTableList(tokens []token, start, end int, info *tablesInfo) int {
	i := start
	for i < end {
		t := tokens[i]

		// Subquery in table position: ( ... ) [AS] alias
		if t.text == "(" {
			close := skipBalancedParens(tokens, i, end)
			// Walk the subquery body so its FROM/JOIN/CTE aliases are
			// collected too. Scope leakage (inner aliases visible to
			// outer query) is accepted as a v1 limitation — yields
			// false negatives, never false positives.
			extractTablesRange(tokens, i+1, close-1, info)

			j := close
			if j < end && strings.ToUpper(tokens[j].text) == "AS" {
				j++
			}
			if j < end && isIdent(tokens[j].text) && !stopWords[strings.ToUpper(tokens[j].text)] {
				name := tokens[j].text
				info.aliases[name] = name
				info.virtual[name] = true
				j++
			}
			if j < end && tokens[j].text == "," {
				i = j + 1
				continue
			}
			return j
		}

		if !isIdent(t.text) || stopWords[strings.ToUpper(t.text)] {
			return i
		}
		tableName := t.text
		i++
		// schema.table
		if i+1 < end && tokens[i].text == "." && isIdent(tokens[i+1].text) && !stopWords[strings.ToUpper(tokens[i+1].text)] {
			tableName = tokens[i+1].text
			i += 2
		}
		alias := tableName
		if i < end && strings.ToUpper(tokens[i].text) == "AS" {
			i++
		}
		if i < end && isIdent(tokens[i].text) && !stopWords[strings.ToUpper(tokens[i].text)] {
			alias = tokens[i].text
			i++
		}
		info.addReal(tableName)
		// Register only an *explicit* alias. The bare table is looked up
		// through schema.HasTable; registering it here would shadow
		// unknown-table diagnostics for typos.
		if alias != tableName {
			info.aliases[alias] = tableName
		}
		if i < end && tokens[i].text == "," {
			i++
			continue
		}
		return i
	}
	return i
}

// readCTEDefinitions parses the CTE list of a WITH clause: a comma-separated
// sequence of "name [(col, ...)] AS [NOT] [MATERIALIZED] (body)" entries.
// Each CTE name is registered as a virtual table so it resolves like a
// real table in FROM/JOIN/qualifier checks; column-level validation
// against the CTE body is not attempted.
func readCTEDefinitions(tokens []token, start, end int, info *tablesInfo) int {
	i := start
	for i < end {
		if !isIdent(tokens[i].text) || stopWords[strings.ToUpper(tokens[i].text)] {
			return i
		}
		name := tokens[i].text
		i++
		// optional column list: (col1, col2, ...)
		if i < end && tokens[i].text == "(" {
			i = skipBalancedParens(tokens, i, end)
		}
		if i < end && strings.ToUpper(tokens[i].text) == "AS" {
			i++
		}
		// optional [NOT] MATERIALIZED
		if i < end && strings.ToUpper(tokens[i].text) == "NOT" {
			i++
		}
		if i < end && strings.ToUpper(tokens[i].text) == "MATERIALIZED" {
			i++
		}
		// CTE body
		if i < end && tokens[i].text == "(" {
			close := skipBalancedParens(tokens, i, end)
			extractTablesRange(tokens, i+1, close-1, info)
			i = close
		}
		info.aliases[name] = name
		info.virtual[name] = true
		if i < end && tokens[i].text == "," {
			i++
			continue
		}
		return i
	}
	return i
}

func skipBalancedParens(tokens []token, i, end int) int {
	if i >= end || tokens[i].text != "(" {
		return i
	}
	depth := 1
	i++
	for i < end && depth > 0 {
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
		}
		i++
	}
	return i
}

func determineState(tokens []token) (State, string) {
	n := len(tokens)
	if n == 0 {
		return StateUnknown, ""
	}

	// Tail-based qualified column: "ident." or "ident.ident"
	if tokens[n-1].text == "." && n >= 2 && isIdent(tokens[n-2].text) {
		return StateQualifiedColumn, tokens[n-2].text
	}
	if isIdent(tokens[n-1].text) && n >= 3 && tokens[n-2].text == "." && isIdent(tokens[n-3].text) {
		return StateQualifiedColumn, tokens[n-3].text
	}

	clause := ""
	for _, t := range tokens {
		switch strings.ToUpper(t.text) {
		case "SELECT", "WHERE", "HAVING", "ON", "USING", "SET", "RETURNING",
			"GROUP", "ORDER", "BY":
			clause = "column"
		case "FROM", "JOIN", "TRUNCATE":
			clause = "table"
		case "INTO", "UPDATE":
			clause = "table"
		case "(":
			// "INSERT INTO t (..." switches into a column list
			if clause == "table" {
				clause = "column"
			}
		case "LIMIT", "OFFSET", "VALUES":
			clause = ""
		}
	}

	switch clause {
	case "table":
		return StateTable, ""
	case "column":
		return StateColumn, ""
	}
	return StateUnknown, ""
}
