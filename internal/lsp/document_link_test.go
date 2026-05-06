package lsp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func runDocumentLink(t *testing.T, uri string) []protocol.DocumentLink {
	t.Helper()
	res, err := documentLink(&glsp.Context{}, &protocol.DocumentLinkParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	require.NoError(t, err)
	return res
}

func linkAt(links []protocol.DocumentLink, line, startChar int) *protocol.DocumentLink {
	for i := range links {
		r := links[i].Range
		if int(r.Start.Line) == line && int(r.Start.Character) == startChar {
			return &links[i]
		}
	}
	return nil
}

func TestDocumentLink_NoSchema(t *testing.T) {
	resetState(t)
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	assert.Empty(t, runDocumentLink(t, uri))
}

func TestDocumentLink_SQLFile_TableLink(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT * FROM users")
	links := runDocumentLink(t, uri)
	require.Len(t, links, 1)
	link := links[0]
	require.NotNil(t, link.Target)
	// Fragment carries the 1-indexed CREATE TABLE line.
	assert.True(t, strings.HasSuffix(*link.Target, "/users.sql#L1"),
		"target should end with users.sql#L1; got %q", *link.Target)
	assert.Equal(t, uint32(14), link.Range.Start.Character)
	assert.Equal(t, uint32(19), link.Range.End.Character)
}

func TestDocumentLink_QualifiedReferenceMergesIntoOneLink(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.sql"
	setDocForTest(t, uri, "SELECT u.email FROM users u")
	links := runDocumentLink(t, uri)
	// Expect: one merged link for "u.email" + one for "users".
	require.Len(t, links, 2)
	merged := linkAt(links, 0, 7) // start of "u"
	require.NotNil(t, merged, "expected a link starting at the qualifier position")
	assert.Equal(t, uint32(14), merged.Range.End.Character,
		"merged range should cover the whole 'u.email' (no gap on the dot)")
	require.NotNil(t, merged.Target)
	assert.True(t, strings.HasSuffix(*merged.Target, "/users.sql#L3"),
		"qualified column should target the column line; got %q", *merged.Target)
}

func TestDocumentLink_GoFileOnlyMarkedSQL(t *testing.T) {
	resetState(t)
	setSchemaForTest(t, schemaWithPositions())
	uri := "file:///tmp/x.go"
	src := "package main\n\nfunc main() {\n\t// language=sql\n\tq1 := `SELECT * FROM users`\n\tq2 := `SELECT * FROM orders`\n\t_, _ = q1, q2\n}\n"
	setDocForTest(t, uri, src)
	links := runDocumentLink(t, uri)
	// Only q1 has the marker; q2's "orders" should produce no link.
	require.Len(t, links, 1)
	require.NotNil(t, links[0].Target)
	assert.True(t, strings.HasSuffix(*links[0].Target, "/users.sql#L1"),
		"only marked SQL should yield links; got %q", *links[0].Target)
}
