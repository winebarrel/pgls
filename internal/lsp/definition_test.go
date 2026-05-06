package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func runDefinition(t *testing.T, uri string, line, char int) (*protocol.Location, bool) {
	t.Helper()
	res, err := definition(&glsp.Context{}, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     pos(line, char),
		},
	})
	require.NoError(t, err)
	if res == nil {
		return nil, false
	}
	loc, ok := res.(protocol.Location)
	require.True(t, ok, "result should be Location; got %T", res)
	return &loc, true
}

func TestDefinition_TableJumpsToCreateTable(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	loc, found := runDefinition(t, uri, 0, 16)
	require.True(t, found)
	assert.Equal(t, "file:///test/users.sql", loc.URI)
	assert.Equal(t, uint32(0), loc.Range.Start.Line)
	assert.Equal(t, uint32(13), loc.Range.Start.Character)
}

func TestDefinition_QualifiedColumnJumpsToColumnLine(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u.email FROM users u")
	loc, found := runDefinition(t, uri, 0, 11) // inside "email"
	require.True(t, found)
	assert.Equal(t, "file:///test/users.sql", loc.URI)
	assert.Equal(t, uint32(2), loc.Range.Start.Line, "column 'email' is on line 2 in the test schema")
}

func TestDefinition_AliasResolvesToTable(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u.email FROM users u WHERE u.id = 1")
	// Cursor on the second "u" — alias usage
	loc, found := runDefinition(t, uri, 0, 34)
	require.True(t, found)
	assert.Equal(t, "file:///test/users.sql", loc.URI)
	assert.Equal(t, uint32(0), loc.Range.Start.Line)
}

func TestDefinition_UnknownReturnsNil(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM bogus")
	_, found := runDefinition(t, uri, 0, 16)
	assert.False(t, found)
}
