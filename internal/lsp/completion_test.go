package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func runCompletion(t *testing.T, uri string, line, char int) []protocol.CompletionItem {
	t.Helper()
	res, err := completion(&glsp.Context{}, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     pos(line, char),
		},
	})
	require.NoError(t, err)
	items, ok := res.([]protocol.CompletionItem)
	require.True(t, ok, "result should be []CompletionItem; got %T", res)
	return items
}

func TestCompletion_NoSchema(t *testing.T) {
	resetState(t)
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM ")
	items := runCompletion(t, uri, 0, 14)
	assert.Empty(t, items)
}

func TestCompletion_AfterFROM_ReturnsTables(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM ")
	labels := labelsOf(runCompletion(t, uri, 0, 14))
	assert.Contains(t, labels, "users")
	assert.Contains(t, labels, "orders")
}

func TestCompletion_AfterSELECT_ScopesToFromTables(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT  FROM users")
	// Cursor right after "SELECT "
	labels := labelsOf(runCompletion(t, uri, 0, 7))
	assert.Contains(t, labels, "id")
	assert.Contains(t, labels, "email")
	assert.NotContains(t, labels, "user_id", "orders columns must not appear when only users is in scope")
}

func TestCompletion_QualifiedColumn(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u. FROM users u")
	labels := labelsOf(runCompletion(t, uri, 0, 9))
	assert.ElementsMatch(t, []string{"id", "email"}, labels)
}

func TestCompletion_KeywordsOfferedAfterFROM(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM ")
	items := runCompletion(t, uri, 0, 14)
	labels := labelsOf(items)
	assert.Contains(t, labels, "users")
	assert.Contains(t, labels, "WHERE")
	assert.Contains(t, labels, "JOIN")

	where := itemByLabel(items, "WHERE")
	require.NotNil(t, where)
	require.NotNil(t, where.SortText)
	assert.True(t, (*where.SortText)[0] == 'z',
		"keyword SortText should sort after tables/columns")
}

func TestCompletion_QualifiedColumn_NoKeywords(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u. FROM users u")
	labels := labelsOf(runCompletion(t, uri, 0, 9))
	assert.NotContains(t, labels, "WHERE", "keywords must not pollute dot-completion")
	assert.NotContains(t, labels, "SELECT")
}

func TestCompletion_DuplicateColumnsAreQualified(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	// Both users and orders have "id"; aliases "u" / "o" should be preferred.
	setDocForTest(t, uri, "SELECT  FROM users u JOIN orders o ON 1=1")
	items := runCompletion(t, uri, 0, 7)
	labels := labelsOf(items)
	assert.Contains(t, labels, "u.id")
	assert.Contains(t, labels, "o.id")
	assert.NotContains(t, labels, "id", "duplicate id must be qualified, not bare")

	uID := itemByLabel(items, "u.id")
	require.NotNil(t, uID)
	require.NotNil(t, uID.FilterText)
	assert.Equal(t, "id", *uID.FilterText, "FilterText should keep typing 'id' matching")
	require.NotNil(t, uID.InsertText)
	assert.Equal(t, "u.id", *uID.InsertText)

	// Unique columns stay bare.
	assert.Contains(t, labels, "email")
	assert.Contains(t, labels, "user_id")
}

func TestCompletion_GoFileWithoutMarker_ReturnsEmpty(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.go"
	// No "// language=sql" marker: pgls leaves the string alone.
	setDocForTest(t, uri, "package main\n\nfunc main() {\n\tq := `SELECT * FROM `\n\t_ = q\n}\n")
	items := runCompletion(t, uri, 3, 19)
	assert.Empty(t, items)
}

func TestCompletion_GoFileWithMarker(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.go"
	setDocForTest(t, uri, "package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT * FROM `\n\t_ = q\n}\n")
	// Cursor right after "FROM "
	labels := labelsOf(runCompletion(t, uri, 4, 20))
	assert.Contains(t, labels, "users")
	assert.Contains(t, labels, "orders")
}
