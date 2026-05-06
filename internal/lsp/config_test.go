package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
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

// argIdx is a tiny helper for building *int values inline in test
// fixtures, since sqlFunctionEntry.ArgIndex is now a pointer.
func argIdx(i int) *int { return &i }

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

func TestLoadConfigFile_MalformedJSONShowsError(t *testing.T) {
	// pgls is strict on malformed .pgls.json — if we silently fell back
	// to defaults the user would see pgls "do nothing" with no obvious
	// reason. window/showMessage surfaces the parse error to the editor
	// so the typo is fixable instead of invisible.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "db", "sqlFunctions": "not-an-array"}`)

	captured := captureNotify(t)
	require.Nil(t, loadConfigFile(paramsFor(uri, nil)),
		"malformed .pgls.json must yield nil")

	require.Len(t, *captured, 1)
	assert.Equal(t, protocol.ServerWindowShowMessage, (*captured)[0].method)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, ".pgls.json")
}

func TestLoadConfigFile_UnknownFieldShowsError(t *testing.T) {
	// Strict decode: a typo'd key (`sqlFunktions` instead of
	// `sqlFunctions`) must error rather than silently dropping the
	// user's intended config. window/showMessage surfaces the typo
	// so they can fix it.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": "db", "sqlFunktions": []}`)

	captured := captureNotify(t)
	require.Nil(t, loadConfigFile(paramsFor(uri, nil)))

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "sqlFunktions",
		"the rejected field name should appear in the error so the user can spot the typo")
}

func TestInitOptionsConfig_UnknownFieldShowsError(t *testing.T) {
	// Same strictness for initializationOptions — a misspelled key in
	// the editor's settings shouldn't be silently accepted.
	_, uri := makeWorkspaceRoot(t)

	captured := captureNotify(t)
	require.Nil(t, initOptionsConfig(paramsFor(uri, map[string]any{
		"schemaDir":  "db",
		"sqlFunktns": []any{},
	})))

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "sqlFunktns")
}

func TestLoadConfigFile_ReadErrorShowsError(t *testing.T) {
	// `.pgls.json` exists but pgls can't read it (here: no read
	// permission). Without a window/showMessage the editor user sees
	// pgls quietly do nothing and has no idea config was skipped.
	root, uri := makeWorkspaceRoot(t)
	path := filepath.Join(root, ".pgls.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o644))
	require.NoError(t, os.Chmod(path, 0))
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	// Some environments don't honour `chmod 0` for the current
	// process — root on Unix, Windows in general, and a few exotic
	// filesystems. Probe the same way pgls will and skip if reads
	// still succeed; otherwise the test asserts behavior that
	// can't actually trigger here.
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("chmod 0 didn't block reads on this platform; can't simulate a read failure")
	}

	captured := captureNotify(t)
	require.Nil(t, loadConfigFile(paramsFor(uri, nil)))

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "failed to read")
}

func TestLoadConfigFile_TrailingDataShowsError(t *testing.T) {
	// `{...}\n{...}` is not valid pgls config — the JSON decoder will
	// happily decode the first object and ignore the rest, so without
	// an explicit trailing-data check the second config silently wins
	// or is silently lost. decodeConfig requires io.EOF after the
	// first value, surfacing the issue via window/showMessage.
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, "{\"schemaDir\": \"db\"}\n{\"schemaDir\": \"other\"}")

	captured := captureNotify(t)
	require.Nil(t, loadConfigFile(paramsFor(uri, nil)))

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "unexpected data")
}

func TestInitOptionsConfig_MalformedShowsError(t *testing.T) {
	// Same idea for editor-supplied initializationOptions — a malformed
	// payload (e.g. wrong type for sqlFunctions) is reported rather than
	// silently dropped.
	_, uri := makeWorkspaceRoot(t)

	captured := captureNotify(t)
	require.Nil(t, initOptionsConfig(paramsFor(uri, map[string]any{
		"sqlFunctions": "not-an-array",
	})))

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "initializationOptions")
}

type capturedNotify struct {
	method string
	params any
}

// captureNotify swaps the package-level notify for one that records
// every call into the returned slice. The original notify is restored
// when the test cleans up.
func captureNotify(t *testing.T) *[]capturedNotify {
	t.Helper()
	prev := notify
	t.Cleanup(func() { notify = prev })
	out := &[]capturedNotify{}
	notify = glsp.NotifyFunc(func(method string, params any) {
		*out = append(*out, capturedNotify{method: method, params: params})
	})
	return out
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
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{
		"sqlFunctions": []map[string]any{
			{"name": "Foo", "argIndex": 0},
			{"name": "Bar", "argIndex": 1},
		},
	}))
	assert.Equal(t, []sqlFunctionEntry{{Name: "Foo", ArgIndex: argIdx(0)}, {Name: "Bar", ArgIndex: argIdx(1)}}, got)
}

func TestSQLFunctions_EmptyArrayDisables(t *testing.T) {
	_, uri := makeWorkspaceRoot(t)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{"sqlFunctions": []any{}}))
	assert.NotNil(t, got, "explicit empty array must be returned, not nil — that opts out of function-call detection")
	assert.Empty(t, got)
}

func TestSQLFunctions_ConfigFileBeatsInitOptions(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"sqlFunctions": [{"name": "FromFile", "argIndex": 0}]}`)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{
		"sqlFunctions": []map[string]any{{"name": "FromInit", "argIndex": 0}},
	}))
	assert.Equal(t, []sqlFunctionEntry{{Name: "FromFile", ArgIndex: argIdx(0)}}, got)
}

func TestSetSQLFunctions_AllInvalidFallsBackToDefaults(t *testing.T) {
	// A config like `[{"name": "", "argIndex": -1}]` has zero usable
	// entries — silently honouring it would disable function-call
	// detection (indistinguishable from `[]`). pgls instead falls
	// back to the default set and surfaces the failure via
	// window/showMessage so the user notices.
	prev := loadedSQLFuncs
	t.Cleanup(func() {
		sqlFuncsMu.Lock()
		loadedSQLFuncs = prev
		sqlFuncsMu.Unlock()
	})

	captured := captureNotify(t)
	setSQLFunctions([]sqlFunctionEntry{
		{Name: "", ArgIndex: argIdx(0)},
		{Name: "Bad", ArgIndex: argIdx(-1)},
		{Name: "MissingIdx"}, // ArgIndex omitted (nil) — also invalid
	})

	got := currentSQLFuncs()
	assert.NotEmpty(t, got, "should fall back to defaults, not be empty")
	assert.Contains(t, got, "Query", "default set must be in effect")

	require.Len(t, *captured, 1)
	p, ok := (*captured)[0].params.(*protocol.ShowMessageParams)
	require.True(t, ok)
	assert.Equal(t, protocol.MessageTypeError, p.Type)
	assert.Contains(t, p.Message, "sqlFunctions")
}

func TestSetSQLFunctions_OmittedArgIndexIsInvalid(t *testing.T) {
	// `[{"name": "QueryContext"}]` — argIndex omitted. Previously this
	// silently decoded as 0, which is wrong for *Context methods (their
	// query lives at arg 1). With ArgIndex as *int we can tell "not set"
	// from "explicitly 0", so this entry is rejected at validation.
	prev := loadedSQLFuncs
	t.Cleanup(func() {
		sqlFuncsMu.Lock()
		loadedSQLFuncs = prev
		sqlFuncsMu.Unlock()
	})

	captured := captureNotify(t)
	setSQLFunctions([]sqlFunctionEntry{
		{Name: "QueryContext"}, // ArgIndex nil
	})

	got := currentSQLFuncs()
	assert.NotEmpty(t, got, "all-invalid set should fall back to defaults")
	assert.Equal(t, 1, got["QueryContext"], "default's arg index for *Context is 1")
	require.Len(t, *captured, 1)
}

func TestSQLFunctions_OmittedArgIndexParsedAsNil(t *testing.T) {
	// Sanity check that the JSON decoder does what we think — a
	// missing argIndex must surface as a nil pointer, not as a 0,
	// otherwise the validation above can't tell the cases apart.
	_, uri := makeWorkspaceRoot(t)
	got := sqlFunctionsFromOptions(paramsFor(uri, map[string]any{
		"sqlFunctions": []map[string]any{{"name": "Foo"}}, // no argIndex
	}))
	require.Len(t, got, 1)
	assert.Nil(t, got[0].ArgIndex,
		"omitted argIndex must decode as nil so validation can flag it")
}

func TestSetSQLFunctions_PartialInvalidUsesValid(t *testing.T) {
	// Mixed list: keep the valid entries, log the invalid ones, no
	// fallback to defaults — the user gave a partial valid config so
	// honour what they meant.
	prev := loadedSQLFuncs
	t.Cleanup(func() {
		sqlFuncsMu.Lock()
		loadedSQLFuncs = prev
		sqlFuncsMu.Unlock()
	})

	captured := captureNotify(t)
	setSQLFunctions([]sqlFunctionEntry{
		{Name: "Good", ArgIndex: argIdx(0)},
		{Name: "", ArgIndex: argIdx(0)}, // invalid — silently dropped
	})

	got := currentSQLFuncs()
	assert.Equal(t, 1, len(got))
	assert.Equal(t, 0, got["Good"])
	assert.Empty(t, *captured, "partial-valid config should not fire showMessage")
}

func TestSetSQLFunctions_EmptySliceStaysDisabled(t *testing.T) {
	// Confirm `[]` (explicit disable) is preserved and doesn't
	// trip the all-invalid fallback.
	prev := loadedSQLFuncs
	t.Cleanup(func() {
		sqlFuncsMu.Lock()
		loadedSQLFuncs = prev
		sqlFuncsMu.Unlock()
	})

	captured := captureNotify(t)
	setSQLFunctions([]sqlFunctionEntry{})
	assert.Empty(t, currentSQLFuncs(), "explicit empty slice must disable detection")
	assert.Empty(t, *captured)
}

func TestSchemaDir_EmptyConfigSchemaDir(t *testing.T) {
	root, uri := makeWorkspaceRoot(t)
	writeConfig(t, root, `{"schemaDir": ""}`)
	got := schemaDirFromOptions(paramsFor(uri, nil))
	assert.Equal(t, "", got)
}
