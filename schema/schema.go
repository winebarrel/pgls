package schema

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

type Schema struct {
	Tables map[string]*Table
}

type Table struct {
	Schema  string
	Name    string
	Columns []*Column
}

type Column struct {
	Name string
	Type string
}

func Parse(sql string) (*Schema, error) {
	result, err := pgquery.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("parse sql: %w", err)
	}

	s := &Schema{Tables: map[string]*Table{}}
	for _, raw := range result.Stmts {
		create := raw.Stmt.GetCreateStmt()
		if create == nil {
			continue
		}
		t := tableFromCreate(create)
		s.Tables[t.Name] = t
	}
	return s, nil
}

func tableFromCreate(c *pg_query.CreateStmt) *Table {
	t := &Table{
		Schema: c.Relation.Schemaname,
		Name:   c.Relation.Relname,
	}
	for _, elt := range c.TableElts {
		col := elt.GetColumnDef()
		if col == nil {
			continue
		}
		t.Columns = append(t.Columns, &Column{
			Name: col.Colname,
			Type: typeName(col.TypeName),
		})
	}
	return t
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
