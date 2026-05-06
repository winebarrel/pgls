package lsp

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setSchemaDirForTest(t *testing.T, dir string) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	schemaMu.Lock()
	loadedSchemaDir = filepath.Clean(abs)
	schemaMu.Unlock()
}

func TestPublishDiagnostics_SkipsFilesUnderSchemaDir(t *testing.T) {
	// A file inside schemaDir IS the schema — linting it against the
	// schema it itself defines is nonsensical. The previous behavior
	// flagged things like `public` in `CREATE TABLE public.users`.
	// Now publishDiagnostics short-circuits with an empty list (so
	// any prior squiggles get cleared).
	resetState(t)
	dir := t.TempDir()
	setSchemaForTest(t, schemaWithPositions())
	setSchemaDirForTest(t, dir)

	uri := "file://" + filepath.Join(dir, "users.sql")
	setDocForTest(t, uri, "CREATE TABLE public.users (id bigint PRIMARY KEY)")

	var captured [][]byte
	notify = capturingNotify(&captured)
	publishDiagnostics(uri)

	require.Len(t, captured, 1, "should still notify (with empty list) so stale diagnostics clear")
	assert.True(t, bytes.Contains(captured[0], []byte(`"diagnostics":[]`)),
		"expected empty diagnostics list; got %s", captured[0])
}

func TestPublishDiagnostics_StillLintsOutsideSchemaDir(t *testing.T) {
	// The skip is path-scoped — files outside the schema directory
	// must continue to be linted normally.
	resetState(t)
	schemaDir := t.TempDir()
	otherDir := t.TempDir()
	setSchemaForTest(t, schemaWithPositions())
	setSchemaDirForTest(t, schemaDir)

	uri := "file://" + filepath.Join(otherDir, "query.sql")
	setDocForTest(t, uri, "SELECT * FROM bogus")

	var captured [][]byte
	notify = capturingNotify(&captured)
	publishDiagnostics(uri)

	require.Len(t, captured, 1)
	assert.True(t, bytes.Contains(captured[0], []byte("bogus")),
		"expected `bogus` diagnostic for query outside schemaDir; got %s", captured[0])
}

func TestDocumentLink_SkipsFilesUnderSchemaDir(t *testing.T) {
	// Same logic for documentLink: schema files would otherwise emit
	// links pointing back at themselves, which is useless.
	resetState(t)
	dir := t.TempDir()
	setSchemaForTest(t, schemaWithPositions())
	setSchemaDirForTest(t, dir)

	uri := "file://" + filepath.Join(dir, "users.sql")
	setDocForTest(t, uri, "SELECT u.email FROM users u")

	links := runDocumentLink(t, uri)
	assert.Empty(t, links, "schema files should not emit document links")
}
