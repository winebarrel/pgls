package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/winebarrel/pgls/schema"
)

// schemaWithPositions returns a schema covering two tables that share
// an "id" column (so completion duplicate-handling can be exercised),
// each annotated with a Position pointing at a fake schema file.
func schemaWithPositions() *schema.Schema {
	return &schema.Schema{
		Tables: map[string]*schema.Table{
			"users": {
				Name:     "users",
				Position: schema.Position{Path: "/test/users.sql", Line: 0, Character: 13},
				Columns: []*schema.Column{
					{Name: "id", Type: "int8", Position: schema.Position{Path: "/test/users.sql", Line: 1, Character: 4}},
					{Name: "email", Type: "text", Position: schema.Position{Path: "/test/users.sql", Line: 2, Character: 4}},
				},
			},
			"orders": {
				Name:     "orders",
				Position: schema.Position{Path: "/test/orders.sql", Line: 0, Character: 13},
				Columns: []*schema.Column{
					{Name: "id", Type: "int8", Position: schema.Position{Path: "/test/orders.sql", Line: 1, Character: 4}},
					{Name: "user_id", Type: "int8", Position: schema.Position{Path: "/test/orders.sql", Line: 2, Character: 4}},
				},
			},
		},
	}
}

func setSchemaForTest(t *testing.T, s *schema.Schema) {
	t.Helper()
	schemaMu.Lock()
	loadedSchema = s
	schemaMu.Unlock()
}

func setDocForTest(t *testing.T, uri, text string) {
	t.Helper()
	docsMu.Lock()
	docs[uri] = text
	docsMu.Unlock()
}

func pos(line, char int) protocol.Position {
	return protocol.Position{Line: uint32(line), Character: uint32(char)}
}

func labelsOf(items []protocol.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func itemByLabel(items []protocol.CompletionItem, label string) *protocol.CompletionItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}
