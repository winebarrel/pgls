package schema

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"

	"github.com/winebarrel/pgls/internal/posenc"
)

type Schema struct {
	Tables map[string]*Table
}

func (s *Schema) HasTable(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Tables[name]
	return ok
}

func (s *Schema) HasColumn(table, column string) bool {
	if s == nil {
		return false
	}
	t, ok := s.Tables[table]
	if !ok {
		return false
	}
	for _, c := range t.Columns {
		if c.Name == column {
			return true
		}
	}
	return false
}

// Position is the source location of a Table or Column declaration.
// Path is the absolute file path on disk; Line and Character are
// 0-indexed LSP positions (Character in UTF-16 code units).
type Position struct {
	Path      string
	Line      int
	Character int
}

type Table struct {
	Schema   string
	Name     string
	Columns  []*Column
	Position Position
}

type Column struct {
	Name     string
	Type     string
	Position Position
}

func Parse(sql string) (*Schema, error) {
	result, err := pgquery.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("parse sql: %w", err)
	}

	src := []byte(sql)
	s := &Schema{Tables: map[string]*Table{}}
	for _, raw := range result.Stmts {
		create := raw.Stmt.GetCreateStmt()
		if create == nil {
			continue
		}
		t := tableFromCreate(create, src)
		s.Tables[t.Name] = t
	}
	return s, nil
}

func tableFromCreate(c *pg_query.CreateStmt, src []byte) *Table {
	t := &Table{
		Schema:   c.Relation.Schemaname,
		Name:     c.Relation.Relname,
		Position: positionAt(src, int(c.Relation.Location)),
	}
	for _, elt := range c.TableElts {
		col := elt.GetColumnDef()
		if col == nil {
			continue
		}
		t.Columns = append(t.Columns, &Column{
			Name:     col.Colname,
			Type:     typeName(col.TypeName),
			Position: positionAt(src, int(col.Location)),
		})
	}
	return t
}

// positionAt converts a byte offset within src into a Position. Negative
// offsets — which pg_query uses for nodes whose source location is
// unknown — collapse to (0, 0).
func positionAt(src []byte, offset int) Position {
	if offset < 0 {
		return Position{}
	}
	line, char := posenc.ByteToLSP(src, offset)
	return Position{Line: line, Character: char}
}

func typeName(t *pg_query.TypeName) string {
	if t == nil {
		return ""
	}
	var parts []string
	for _, n := range t.Names {
		if s := n.GetString_(); s != nil {
			parts = append(parts, s.Sval)
		}
	}
	return strings.TrimPrefix(strings.Join(parts, "."), "pg_catalog.")
}
