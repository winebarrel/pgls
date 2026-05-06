package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func makeWorkspaceRoot(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return dir, "file://" + dir
}

func writeConfig(t *testing.T, dir, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".pgls.json"), []byte(contents), 0o644))
}

func paramsFor(uri string, init any) *protocol.InitializeParams {
	root := protocol.DocumentUri(uri)
	return &protocol.InitializeParams{
		RootURI:               &root,
		InitializationOptions: init,
	}
}

func TestSchemaDir_InitOptionsAbsolute(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	got := schemaDirFromOptions(paramsFor(uri, map[string]string{"schemaDir": "/abs/path"}))
	assert.Equal(t, "/abs/path", got)
	_ = root
}

func TestSchemaDir_InitOptionsRelative(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	got := schemaDirFromOptions(paramsFor(uri, map[string]string{"schemaDir": "db/schema"}))
	assert.Equal(t, filepath.Join(root, "db/schema"), got)
}

func TestSchemaDir_ConfigFileFallback(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "migrations"}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, filepath.Join(root, "migrations"), got)
}

func TestSchemaDir_ConfigFileRejectsAbsolutePath(t *testing.T) {
	// .pgls.json is committed to the repo; an absolute schemaDir would
	// let an unfamiliar workspace walk arbitrary .sql files elsewhere
	// on the user's disk.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "/etc/schemas"}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got)
}

func TestSchemaDir_ConfigFileRejectsParentEscape(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "../outside"}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got, "../outside should not escape the workspace")
}

func TestSchemaDir_ConfigFileAcceptsNormalisedRelative(t *testing.T) {
	// "db/../db/schema" cleans to "db/schema" — fine because it stays
	// inside the workspace.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "db/../db/schema"}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, filepath.Join(root, "db/schema"), got)
}

func TestSchemaDir_ConfigFileBeatsInitOptions(t *testing.T) {
	// .pgls.json is the project's authoritative schema location —
	// committed alongside the code — so it wins over per-session
	// initializationOptions. Editor settings can only set schemaDir
	// for projects that don't have a .pgls.json.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "winner"}`)
	got := schemaDirFromOptions(paramsFor(uri, map[string]string{"schemaDir": "loser"}))
	assert.Equal(t, filepath.Join(root, "winner"), got)
}

func TestSchemaDir_NoConfigNoOptions(t *testing.T) {
	_, uri := makeWorkspaceRoot(t)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got)
}

func TestSchemaDir_InvalidJSONIgnored(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `not json`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got, "invalid JSON should not crash, just yield empty")
}

func TestSchemaDir_EmptyInitOptionsFallsThroughToConfigFile(t *testing.T) {
	// Pinning the documented precedence: an empty schemaDir in
	// initializationOptions is treated as "not provided", not as
	// "explicitly disable", so .pgls.json still applies.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "fallback"}`)
	got := schemaDirFromOptions(paramsFor(uri, map[string]string{"schemaDir": ""}))
	assert.Equal(t, filepath.Join(root, "fallback"), got)
}

func TestSQLFunctions_NotConfiguredReturnsNil(t *testing.T) {
	_, uri := makeWorkspaceRoot(t)
	got := sqlFunctionsFromOptions(paramsFor(uri, nil))
	assert.Nil(t, got, "no config and no init options should return nil so caller uses the default set")
}

func TestSQLFunctions_FromInitOptions(t *testing.T) {
	_, uri := makeWorkspaceRoot(t)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{"sqlFunctions": []string{"Foo", "Bar"}}))
	assert.Equal(t, []string{"Foo", "Bar"}, got)
}

func TestSQLFunctions_EmptyArrayDisables(t *testing.T) {
	_, uri := makeWorkspaceRoot(t)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{"sqlFunctions": []string{}}))
	assert.NotNil(t, got, "explicit empty array must be returned, not nil — that opts out of function-call detection")
	assert.Empty(t, got)
}

func TestSQLFunctions_ConfigFileBeatsInitOptions(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"sqlFunctions": ["FromFile"]}`)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{"sqlFunctions": []string{"FromInit"}}))
	assert.Equal(t, []string{"FromFile"}, got)
}

func TestSchemaDir_EmptyConfigSchemaDir(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": ""}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got)
}
