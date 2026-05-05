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
	FromTables []string          // tables visible in scope (deduped, real names)
	Aliases    map[string]string // alias -> real table name
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
	aliases := extractTables(all)
	prefix := tokensBefore(all, cursor)
	state, qualifier := determineState(prefix)
	return Context{
		State:      state,
		Qualifier:  qualifier,
		FromTables: uniqueValues(aliases),
		Aliases:    aliases,
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

func uniqueValues(m map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range m {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func extractTables(tokens []token) map[string]string {
	aliases := map[string]string{}
	i := 0
	for i < len(tokens) {
		upper := strings.ToUpper(tokens[i].text)
		if upper == "FROM" || upper == "JOIN" || upper == "INTO" || upper == "UPDATE" {
			i = readTableList(tokens, i+1, aliases)
			continue
		}
		i++
	}
	return aliases
}

func readTableList(tokens []token, i int, aliases map[string]string) int {
	for i < len(tokens) {
		t := tokens[i]
		if !isIdent(t.text) || stopWords[strings.ToUpper(t.text)] {
			return i
		}
		tableName := t.text
		i++
		// schema.table
		if i+1 < len(tokens) && tokens[i].text == "." && isIdent(tokens[i+1].text) && !stopWords[strings.ToUpper(tokens[i+1].text)] {
			tableName = tokens[i+1].text
			i += 2
		}
		alias := tableName
		// optional AS
		if i < len(tokens) && strings.ToUpper(tokens[i].text) == "AS" {
			i++
		}
		// optional alias identifier (must not be a stop word)
		if i < len(tokens) && isIdent(tokens[i].text) && !stopWords[strings.ToUpper(tokens[i].text)] {
			alias = tokens[i].text
			i++
		}
		aliases[alias] = tableName
		if alias != tableName {
			aliases[tableName] = tableName
		}
		// chain comma-separated tables
		if i < len(tokens) && tokens[i].text == "," {
			i++
			continue
		}
		return i
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
