package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func runHover(t *testing.T, uri string, line, char int) *protocol.Hover {
	t.Helper()
	res, err := hover(&glsp.Context{}, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     pos(line, char),
		},
	})
	require.NoError(t, err)
	return res
}

func hoverText(h *protocol.Hover) string {
	if h == nil {
		return ""
	}
	if mc, ok := h.Contents.(protocol.MarkupContent); ok {
		return mc.Value
	}
	return ""
}

func TestHover_NoSchema(t *testing.T) {
	resetState(t)
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	assert.Nil(t, runHover(t, uri, 0, 16))
}

func TestHover_TableShowsColumnList(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	// Cursor inside "users" (chars 14..19)
	text := hoverText(runHover(t, uri, 0, 16))
	assert.Contains(t, text, "**users** (table)")
	assert.Contains(t, text, "| id | `int8` |")
	assert.Contains(t, text, "| email | `text` |")
}

func TestHover_QualifiedColumn(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u.email FROM users u")
	// Cursor inside "email" (chars 9..14)
	text := hoverText(runHover(t, uri, 0, 11))
	assert.Equal(t, "**users.email** `text`", text)
}

func TestHover_AliasResolvesToTable(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u.email FROM users u WHERE u.id = 1")
	// Cursor on the second "u" — bare alias usage in WHERE
	text := hoverText(runHover(t, uri, 0, 34))
	assert.Contains(t, text, "**users** (table)")
	assert.Contains(t, text, "_alias: `u`_")
}

func TestHover_OperatorReturnsNil(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	// Cursor on "*" (char 7)
	assert.Nil(t, runHover(t, uri, 0, 7))
}
