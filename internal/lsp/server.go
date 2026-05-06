package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
	"github.com/winebarrel/pgls/internal/goast"
	"github.com/winebarrel/pgls/internal/posenc"
	"github.com/winebarrel/pgls/internal/sqlctx"
	"github.com/winebarrel/pgls/schema"
)

const name = "pgls"

// version is overridden via -ldflags at build time. The source default
// is what `go install` and a plain `go build` produce; goreleaser
// stamps it with the tag (e.g. "1.0.0") on releases.
var version = "dev"

var (
	handler protocol.Handler

	docsMu sync.Mutex
	docs   = map[string]string{}

	schemaMu     sync.RWMutex
	loadedSchema *schema.Schema

	notify glsp.NotifyFunc

	cliDir       string
	watcherOnce  sync.Once
	watcherStart sync.Mutex // serializes attempts to (re)configure the watcher

	// publishMu serializes publishDiagnostics so that the notification
	// stream stays in sequence even when didChange fans out across
	// goroutines (jsonrpc2.HandlerWithError dispatches each request in
	// its own goroutine).
	publishMu sync.Mutex

	// publishSeqs maps each document URI to a monotonic counter that's
	// bumped on every publish request. A handler holding publishMu
	// drops its work if the counter has advanced past its captured seq,
	// so a backlog of stale diagnostics can never overwrite a newer one.
	publishSeqs sync.Map // uri -> *atomic.Uint64

	// sqlFuncsMu guards loadedSQLFuncs.
	sqlFuncsMu      sync.RWMutex
	loadedSQLFuncs  goast.SQLFunctions = goast.DefaultSQLFunctions()
)

func Run(cliSchemaDir string) error {
	cliDir = cliSchemaDir
	handler = protocol.Handler{
		Initialize:             initialize,
		Initialized:            func(*glsp.Context, *protocol.InitializedParams) error { return nil },
		Shutdown:               func(*glsp.Context) error { return nil },
		SetTrace:               func(*glsp.Context, *protocol.SetTraceParams) error { return nil },
		TextDocumentDidOpen:      didOpen,
		TextDocumentDidChange:    didChange,
		TextDocumentDidClose:     didClose,
		TextDocumentCompletion:   completion,
		TextDocumentHover:        hover,
		TextDocumentDefinition:   definition,
		TextDocumentDocumentLink: documentLink,
	}
	srv := server.NewServer(&handler, name, false)
	return srv.RunStdio()
}

// pglsConfig is the JSON shape of pgls's user-supplied configuration.
// It carries every recognized field, and is used as-is for both the
// `.pgls.json` workspace file and the LSP `initializationOptions`
// payload — same shape, same validation rules. SQLFunctions is a
// pointer so callers can distinguish "explicitly empty" (opt out of
// function-call detection) from "field omitted" (use defaults).
type pglsConfig struct {
	SchemaDir    string              `json:"schemaDir"`
	SQLFunctions *[]sqlFunctionEntry `json:"sqlFunctions"`
}

// sqlFunctionEntry is the JSON shape of one entry in the sqlFunctions
// list. Name is matched against call expressions by selector, ArgIndex
// is the 0-indexed positional argument that holds the SQL string.
//
// ArgIndex is a pointer so we can distinguish "explicitly 0" from
// "field omitted" — a missing argIndex was previously decoded as 0
// silently, which is wrong for *Context methods (their query lives at
// arg 1) and produced confusing "pgls says nothing about my SQL"
// behavior. A nil ArgIndex is now flagged at validation time.
type sqlFunctionEntry struct {
	Name     string `json:"name"`
	ArgIndex *int   `json:"argIndex"`
}

func initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	// Set notify before loading config so parse errors can surface via
	// window/showMessage during this initialize handler.
	notify = ctx.Notify

	fileCfg := loadConfigFile(params)
	initCfg := initOptionsConfig(params)

	dir := schemaDirFromOptionsWith(params, fileCfg, initCfg)
	if dir == "" {
		dir = cliDir
	}
	if dir != "" {
		loadAndSetSchema(dir)
		startSchemaWatcher(dir)
	}
	setSQLFunctions(sqlFunctionsFromOptionsWith(fileCfg, initCfg))

	caps := handler.CreateServerCapabilities()
	caps.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{".", " "},
	}
	caps.TextDocumentSync = protocol.TextDocumentSyncOptions{
		OpenClose: boolPtr(true),
		Change:    syncKindPtr(protocol.TextDocumentSyncKindIncremental),
	}
	v := version
	return protocol.InitializeResult{
		Capabilities: caps,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    name,
			Version: &v,
		},
	}, nil
}

// decodeConfig strictly decodes a JSON payload into a pglsConfig.
// "Strict" means:
//   - unknown fields are rejected (a typo like `sqlFunktions` errors
//     instead of silently dropping the user's intended config), and
//   - trailing data after the first JSON value is rejected (a stray
//     second object or accidental concatenation doesn't get silently
//     ignored).
func decodeConfig(b []byte) (*pglsConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var cfg pglsConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return &cfg, nil
}

// loadConfigFile reads `.pgls.json` from the workspace root and
// returns its parsed contents, or nil if the workspace root is unknown
// or the file is missing. Decoding is strict — invalid JSON, wrong
// types, AND unknown fields all yield nil plus a `window/showMessage`
// error toast, so the user can see and fix the typo rather than
// silently losing config to a misspelled key.
func loadConfigFile(params *protocol.InitializeParams) *pglsConfig {
	root := workspaceRoot(params)
	if root == "" {
		return nil
	}
	path := filepath.Join(root, ".pgls.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("read %s: %v", path, err)
		}
		return nil
	}
	cfg, err := decodeConfig(b)
	if err != nil {
		msg := fmt.Sprintf("pgls: failed to parse %s — %v", path, err)
		log.Print(msg)
		showError(msg)
		return nil
	}
	return cfg
}

// initOptionsConfig parses params.InitializationOptions into a
// pglsConfig. Returns nil when init options are absent, JSON-encoding
// fails, or strict decoding fails (unknown field, wrong type, etc.).
// Decode failures surface via window/showMessage so a typo in editor
// settings is visible rather than producing an inert pgls.
func initOptionsConfig(params *protocol.InitializeParams) *pglsConfig {
	if params.InitializationOptions == nil {
		return nil
	}
	b, err := json.Marshal(params.InitializationOptions)
	if err != nil {
		return nil
	}
	cfg, err := decodeConfig(b)
	if err != nil {
		msg := fmt.Sprintf("pgls: failed to parse initializationOptions — %v", err)
		log.Print(msg)
		showError(msg)
		return nil
	}
	return cfg
}

// showError sends an error-level window/showMessage to the editor.
// It's a no-op when notify is unset (e.g. inside tests), so callers
// can fire it unconditionally.
func showError(msg string) {
	if notify == nil {
		return
	}
	notify(protocol.ServerWindowShowMessage, &protocol.ShowMessageParams{
		Type:    protocol.MessageTypeError,
		Message: msg,
	})
}

// schemaDirFromOptions resolves the schema directory. A project-local
// .pgls.json at the workspace root wins over LSP initializationOptions
// — the config file is committed alongside the code, so it's the
// authoritative schema location for the project; init options are
// useful for editor-specific overrides only when no .pgls.json is
// present. Returning "" leaves the caller to fall back to the CLI
// flag (or to leave the schema unloaded).
func schemaDirFromOptions(params *protocol.InitializeParams) string {
	return schemaDirFromOptionsWith(params, loadConfigFile(params), initOptionsConfig(params))
}

func schemaDirFromOptionsWith(params *protocol.InitializeParams, fileCfg, initCfg *pglsConfig) string {
	if dir := schemaDirFromConfigFile(params, fileCfg); dir != "" {
		return dir
	}
	if initCfg == nil {
		return ""
	}
	return resolveSchemaDir(initCfg.SchemaDir, workspaceRoot(params))
}

// schemaDirFromConfigFile validates and resolves the schemaDir from a
// pre-loaded `.pgls.json`. Returns "" when cfg is nil or the field is
// unset. Because the config file is checked into the repository, the
// schemaDir it carries is constrained to a path inside the workspace
// — absolute paths and ".." escapes are rejected so an unfamiliar
// repo can't make pgls walk and surface arbitrary `.sql` files
// elsewhere on disk. The CLI flag and initializationOptions paths
// stay unrestricted because the user (or their editor config) supplies
// them explicitly.
func schemaDirFromConfigFile(params *protocol.InitializeParams, cfg *pglsConfig) string {
	if cfg == nil || cfg.SchemaDir == "" {
		return ""
	}
	root := workspaceRoot(params)
	path := filepath.Join(root, ".pgls.json")
	if filepath.IsAbs(cfg.SchemaDir) {
		log.Printf("%s: absolute schemaDir %q rejected (must be inside workspace)", path, cfg.SchemaDir)
		return ""
	}
	resolved := filepath.Clean(filepath.Join(root, cfg.SchemaDir))
	cleanRoot := filepath.Clean(root)
	if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+string(filepath.Separator)) {
		log.Printf("%s: schemaDir %q escapes workspace, rejected", path, cfg.SchemaDir)
		return ""
	}
	return resolved
}

func resolveSchemaDir(dir, root string) string {
	if dir == "" {
		return ""
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	if root != "" {
		return filepath.Join(root, dir)
	}
	return dir
}

// sqlFunctionsFromOptions returns the user-configured SQL-function
// list, or nil if no configuration is present (caller falls back to
// goast.DefaultSQLFunctions). .pgls.json wins over initializationOptions
// — same precedence as schemaDir.
//
// The returned slice may be empty; an explicit empty array opts out of
// function-call detection so only the language=sql marker fires.
func sqlFunctionsFromOptions(params *protocol.InitializeParams) []sqlFunctionEntry {
	return sqlFunctionsFromOptionsWith(loadConfigFile(params), initOptionsConfig(params))
}

func sqlFunctionsFromOptionsWith(fileCfg, initCfg *pglsConfig) []sqlFunctionEntry {
	if fileCfg != nil && fileCfg.SQLFunctions != nil {
		return *fileCfg.SQLFunctions
	}
	if initCfg != nil && initCfg.SQLFunctions != nil {
		return *initCfg.SQLFunctions
	}
	return nil
}


// setSQLFunctions installs the active SQL-function set.
//   - nil funcs reverts to DefaultSQLFunctions.
//   - An empty slice (`[]`) is the explicit "disable function-call
//     detection" signal — only the language=sql marker fires.
//   - Otherwise each entry is validated (non-empty Name, non-negative
//     ArgIndex). Invalid entries are logged. If every entry is invalid
//     we fall back to defaults and surface a window/showMessage so the
//     user knows their config is wrong, rather than silently ending up
//     with detection disabled.
func setSQLFunctions(funcs []sqlFunctionEntry) {
	sqlFuncsMu.Lock()
	defer sqlFuncsMu.Unlock()
	if funcs == nil {
		loadedSQLFuncs = goast.DefaultSQLFunctions()
		return
	}
	set := make(goast.SQLFunctions, len(funcs))
	invalid := 0
	for _, e := range funcs {
		if e.Name == "" || e.ArgIndex == nil || *e.ArgIndex < 0 {
			invalid++
			log.Printf("sqlFunctions: ignoring invalid entry %+v", e)
			continue
		}
		set[e.Name] = *e.ArgIndex
	}
	if invalid > 0 && len(set) == 0 {
		// Every entry was rejected — distinguish this from `[]` (which
		// is the explicit-disable signal) by falling back to defaults
		// and telling the user.
		msg := fmt.Sprintf("pgls: every sqlFunctions entry was invalid (%d ignored); falling back to defaults", invalid)
		log.Print(msg)
		showError(msg)
		loadedSQLFuncs = goast.DefaultSQLFunctions()
		return
	}
	loadedSQLFuncs = set
}

func currentSQLFuncs() goast.SQLFunctions {
	sqlFuncsMu.RLock()
	defer sqlFuncsMu.RUnlock()
	// goast.SQLFunctions is a map (reference type); returning the
	// underlying map would let any caller mutate shared state outside
	// of sqlFuncsMu. Hand back a fresh copy so each request gets an
	// independent snapshot.
	cp := make(goast.SQLFunctions, len(loadedSQLFuncs))
	for k, v := range loadedSQLFuncs {
		cp[k] = v
	}
	return cp
}

func workspaceRoot(params *protocol.InitializeParams) string {
	if len(params.WorkspaceFolders) > 0 {
		if p := uriToPath(params.WorkspaceFolders[0].URI); p != "" {
			return p
		}
	}
	if params.RootURI != nil {
		if p := uriToPath(*params.RootURI); p != "" {
			return p
		}
	}
	if params.RootPath != nil {
		return *params.RootPath
	}
	return ""
}

func boolPtr(b bool) *bool                                                    { return &b }
func syncKindPtr(k protocol.TextDocumentSyncKind) *protocol.TextDocumentSyncKind { return &k }

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

func currentSchema() *schema.Schema {
	schemaMu.RLock()
	defer schemaMu.RUnlock()
	return loadedSchema
}

func loadAndSetSchema(dir string) {
	s, err := schema.Load(dir)
	if err != nil {
		log.Printf("schema load %q: %v", dir, err)
		return
	}
	schemaMu.Lock()
	loadedSchema = s
	schemaMu.Unlock()
	log.Printf("loaded schema from %s (%d tables)", dir, len(s.Tables))
}

// startSchemaWatcher launches a single fsnotify-based watcher on dir
// that reloads the schema and republishes diagnostics when any .sql
// file changes. It is a no-op on subsequent calls — the watcher is
// established only once per process.
func startSchemaWatcher(dir string) {
	watcherOnce.Do(func() {
		watcherStart.Lock()
		defer watcherStart.Unlock()

		w, err := fsnotify.NewWatcher()
		if err != nil {
			log.Printf("schema watcher: %v", err)
			return
		}
		if err := addDirsRecursively(w, dir); err != nil {
			log.Printf("schema watcher add %q: %v", dir, err)
			w.Close() //nolint:errcheck
			return
		}

		go runWatcher(w, dir)
	})
}

func addDirsRecursively(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.Add(p)
		}
		return nil
	})
}

func runWatcher(w *fsnotify.Watcher, dir string) {
	defer w.Close() //nolint:errcheck

	var (
		timer   *time.Timer
		timerMu sync.Mutex
	)
	schedule := func() {
		timerMu.Lock()
		defer timerMu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(200*time.Millisecond, func() {
			loadAndSetSchema(dir)
			republishAllDiagnostics()
		})
	}

	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// Watch new subdirectories as they appear.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			if strings.EqualFold(filepath.Ext(ev.Name), ".sql") {
				schedule()
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("schema watcher: %v", err)
		}
	}
}

func republishAllDiagnostics() {
	if notify == nil {
		return
	}
	docsMu.Lock()
	uris := make([]string, 0, len(docs))
	for u := range docs {
		uris = append(uris, u)
	}
	docsMu.Unlock()
	for _, u := range uris {
		publishDiagnostics(u)
	}
}

func didOpen(_ *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	docsMu.Lock()
	docs[params.TextDocument.URI] = params.TextDocument.Text
	docsMu.Unlock()
	publishDiagnostics(params.TextDocument.URI)
	return nil
}

func didChange(_ *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	docsMu.Lock()
	text := docs[params.TextDocument.URI]
	for _, change := range params.ContentChanges {
		switch c := change.(type) {
		case protocol.TextDocumentContentChangeEventWhole:
			text = c.Text
		case protocol.TextDocumentContentChangeEvent:
			text = applyContentChange(text, c)
		}
	}
	docs[params.TextDocument.URI] = text
	docsMu.Unlock()
	publishDiagnostics(params.TextDocument.URI)
	return nil
}

// applyContentChange applies a single LSP content change to text.
// A nil Range means the change replaces the entire document.
// Per the LSP spec, when multiple changes arrive in one didChange,
// each change's positions refer to the document state *after* the
// preceding changes in the batch have been applied — so callers
// fold this function over the slice in order.
func applyContentChange(text string, c protocol.TextDocumentContentChangeEvent) string {
	if c.Range == nil {
		return c.Text
	}
	src := []byte(text)
	start := posenc.LSPToByte(src, int(c.Range.Start.Line), int(c.Range.Start.Character))
	end := posenc.LSPToByte(src, int(c.Range.End.Line), int(c.Range.End.Character))
	if end < start {
		end = start
	}
	return text[:start] + c.Text + text[end:]
}

func didClose(_ *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	docsMu.Lock()
	delete(docs, params.TextDocument.URI)
	docsMu.Unlock()
	// Clear any prior diagnostics so stale errors don't linger.
	if notify != nil {
		notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []protocol.Diagnostic{},
		})
	}
	return nil
}

func publishDiagnostics(uri string) {
	seqPtr, _ := publishSeqs.LoadOrStore(uri, new(atomic.Uint64))
	seq := seqPtr.(*atomic.Uint64)
	mySeq := seq.Add(1)

	publishMu.Lock()
	defer publishMu.Unlock()

	// While we waited for the mutex another publish for the same URI may
	// have been queued. Skip if so — it will run with fresher state.
	if seq.Load() != mySeq {
		return
	}
	if notify == nil {
		return
	}
	s := currentSchema()
	if s == nil {
		return
	}
	docsMu.Lock()
	text, open := docs[uri]
	docsMu.Unlock()
	if !open {
		return
	}

	src := []byte(text)
	// Initialise as an empty slice — Go marshals a nil []T to JSON
	// `null`, which VSCode treats as "no change" instead of "clear
	// the diagnostics for this document". An explicit `[]` is what
	// actually wipes stale errors.
	diags := []protocol.Diagnostic{}

	if strings.HasSuffix(uri, ".go") {
		for _, blk := range goast.FindAllSQL(src, currentSQLFuncs()) {
			for _, iss := range sqlctx.Lint(blk.SQL, s) {
				diags = append(diags, makeDiagnostic(src, blk.StartByte+iss.Start, blk.StartByte+iss.End, iss.Message))
			}
		}
	} else {
		for _, iss := range sqlctx.Lint(text, s) {
			diags = append(diags, makeDiagnostic(src, iss.Start, iss.End, iss.Message))
		}
	}

	severity := protocol.DiagnosticSeverityError
	source := name
	for i := range diags {
		diags[i].Severity = &severity
		diags[i].Source = &source
	}

	notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

func makeDiagnostic(src []byte, start, end int, msg string) protocol.Diagnostic {
	sLine, sChar := posenc.ByteToLSP(src, start)
	eLine, eChar := posenc.ByteToLSP(src, end)
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: uint32(sLine), Character: uint32(sChar)},
			End:   protocol.Position{Line: uint32(eLine), Character: uint32(eChar)},
		},
		Message: msg,
	}
}

func completion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	sch := currentSchema()
	if sch == nil {
		return []protocol.CompletionItem{}, nil
	}
	uri := params.TextDocument.URI
	docsMu.Lock()
	text := docs[uri]
	docsMu.Unlock()

	var sql string
	var off int
	line, char := int(params.Position.Line), int(params.Position.Character)

	if strings.HasSuffix(uri, ".go") {
		s, o, ok := goast.FindSQL([]byte(text), line, char, currentSQLFuncs())
		if !ok {
			return []protocol.CompletionItem{}, nil
		}
		sql, off = s, o
	} else {
		sql = text
		off = posenc.LSPToByte([]byte(text), line, char)
	}

	ctx := sqlctx.Analyze(sql, off)
	return contextItems(sch, ctx), nil
}

func contextItems(s *schema.Schema, ctx sqlctx.Context) []protocol.CompletionItem {
	switch ctx.State {
	case sqlctx.StateTable:
		return tableItems(s)
	case sqlctx.StateQualifiedColumn:
		realName, ok := ctx.Aliases[ctx.Qualifier]
		if !ok {
			realName = ctx.Qualifier
		}
		if t, ok := s.Tables[realName]; ok {
			return columnItems(t)
		}
		return nil
	case sqlctx.StateColumn:
		if len(ctx.FromTables) > 0 {
			return scopedColumnItems(s, ctx.FromTables, ctx.Aliases)
		}
		return scopedColumnItems(s, allTableNames(s), nil)
	default:
		items := tableItems(s)
		items = append(items, scopedColumnItems(s, allTableNames(s), nil)...)
		return items
	}
}

func allTableNames(s *schema.Schema) []string {
	names := make([]string, 0, len(s.Tables))
	for n := range s.Tables {
		names = append(names, n)
	}
	return names
}

// scopedColumnItems builds column completions for a set of visible tables.
// When the same column name appears in multiple tables, the duplicate
// entries are qualified ("u.id" / "orders.id") so the editor popup can
// distinguish them. FilterText keeps the bare column name as the typing
// match, and InsertText writes the qualified form. Aliases are preferred
// over real table names when an explicit alias is in scope.
func scopedColumnItems(s *schema.Schema, tableNames []string, aliases map[string]string) []protocol.CompletionItem {
	realToAlias := map[string]string{}
	for alias, real := range aliases {
		if alias != real {
			realToAlias[real] = alias
		}
	}

	counts := map[string]int{}
	for _, n := range tableNames {
		if t, ok := s.Tables[n]; ok {
			for _, c := range t.Columns {
				counts[c.Name]++
			}
		}
	}

	fieldKind := protocol.CompletionItemKindField
	var items []protocol.CompletionItem
	for _, n := range tableNames {
		t, ok := s.Tables[n]
		if !ok {
			continue
		}
		prefix := t.Name
		if a, ok := realToAlias[t.Name]; ok {
			prefix = a
		}
		for _, c := range t.Columns {
			detail := fmt.Sprintf("%s.%s %s", t.Name, c.Name, c.Type)
			label := c.Name
			var insertText, filterText *string
			if counts[c.Name] > 1 {
				qualified := prefix + "." + c.Name
				label = qualified
				ins := qualified
				flt := c.Name
				insertText = &ins
				filterText = &flt
			}
			items = append(items, protocol.CompletionItem{
				Label:      label,
				Kind:       &fieldKind,
				Detail:     &detail,
				InsertText: insertText,
				FilterText: filterText,
			})
		}
	}
	return items
}

// documentLink overrides VSCode's built-in URL detection for SQL
// identifiers that happen to look like domain names — `u.email`,
// `o.id`, etc. — by pointing them at file:// URLs of the schema
// definition. VSCode prefers LSP-supplied document links over its
// own pattern-matched ones, so Cmd-click no longer opens a browser.
func documentLink(_ *glsp.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	sch := currentSchema()
	if sch == nil {
		return []protocol.DocumentLink{}, nil
	}
	uri := params.TextDocument.URI
	docsMu.Lock()
	text := docs[uri]
	docsMu.Unlock()
	if text == "" {
		return []protocol.DocumentLink{}, nil
	}
	src := []byte(text)

	links := []protocol.DocumentLink{}
	addLink := func(startB, endB int, target string) {
		sLine, sChar := posenc.ByteToLSP(src, startB)
		eLine, eChar := posenc.ByteToLSP(src, endB)
		t := target
		links = append(links, protocol.DocumentLink{
			Range: protocol.Range{
				Start: protocol.Position{Line: uint32(sLine), Character: uint32(sChar)},
				End:   protocol.Position{Line: uint32(eLine), Character: uint32(eChar)},
			},
			Target: &t,
		})
	}
	appendBlock := func(sql string, baseByte int) {
		symbols, aliases, _ := sqlctx.WalkSymbols(sql)
		for i := 0; i < len(symbols); i++ {
			sym := symbols[i]
			// Pair "X" (qualifier) with the immediately following
			// QualifiedColumn so the dot between them is covered by a
			// single contiguous link — VSCode draws one underline per
			// link, so two adjacent links render as a visual gap on
			// the "." character.
			if sym.Kind == sqlctx.SymbolQualifier && i+1 < len(symbols) {
				next := symbols[i+1]
				if next.Kind == sqlctx.SymbolQualifiedColumn && next.Qualifier == sym.Name {
					qt := symbolTarget(sch, sym, aliases)
					ct := symbolTarget(sch, next, aliases)
					switch {
					case ct != "":
						// Prefer the column position when it resolves
						// — more useful as a destination.
						addLink(baseByte+sym.Start, baseByte+next.End, ct)
					case qt != "":
						addLink(baseByte+sym.Start, baseByte+sym.End, qt)
					}
					i++ // skip the column we just consumed
					continue
				}
			}
			if target := symbolTarget(sch, sym, aliases); target != "" {
				addLink(baseByte+sym.Start, baseByte+sym.End, target)
			}
		}
	}
	if strings.HasSuffix(uri, ".go") {
		for _, blk := range goast.FindAllSQL(src, currentSQLFuncs()) {
			appendBlock(blk.SQL, blk.StartByte)
		}
	} else {
		appendBlock(text, 0)
	}
	return links, nil
}

func symbolTarget(s *schema.Schema, sym sqlctx.Symbol, aliases map[string]string) string {
	var pos *schema.Position
	switch sym.Kind {
	case sqlctx.SymbolTable, sqlctx.SymbolQualifier:
		if t, ok := s.Tables[sym.Name]; ok {
			pos = &t.Position
		} else if real, ok := aliases[sym.Name]; ok {
			if t, ok := s.Tables[real]; ok {
				pos = &t.Position
			}
		}
	case sqlctx.SymbolQualifiedColumn:
		realName, ok := aliases[sym.Qualifier]
		if !ok {
			realName = sym.Qualifier
		}
		if t, ok := s.Tables[realName]; ok {
			if c := findColumn(t, sym.Name); c != nil {
				pos = &c.Position
			}
		}
	}
	if pos == nil || pos.Path == "" {
		return ""
	}
	// VSCode honours a `#L<line>` fragment on file:// URIs to open at
	// a specific line. Line numbers in the fragment are 1-indexed,
	// while LSP positions are 0-indexed.
	return fmt.Sprintf("file://%s#L%d", pos.Path, pos.Line+1)
}

func definition(_ *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	sch := currentSchema()
	if sch == nil {
		return nil, nil
	}
	uri := params.TextDocument.URI
	docsMu.Lock()
	text := docs[uri]
	docsMu.Unlock()

	var sql string
	var off int
	line, char := int(params.Position.Line), int(params.Position.Character)

	if strings.HasSuffix(uri, ".go") {
		s, o, ok := goast.FindSQL([]byte(text), line, char, currentSQLFuncs())
		if !ok {
			return nil, nil
		}
		sql, off = s, o
	} else {
		sql = text
		off = posenc.LSPToByte([]byte(text), line, char)
	}

	id, ok := sqlctx.IdentifierAt(sql, off)
	if !ok {
		return nil, nil
	}
	ctx := sqlctx.Analyze(sql, off)
	pos := definitionPosition(sch, id, ctx)
	if pos == nil || pos.Path == "" {
		return nil, nil
	}
	target := protocol.Position{Line: uint32(pos.Line), Character: uint32(pos.Character)}
	return protocol.Location{
		URI: "file://" + pos.Path,
		Range: protocol.Range{
			Start: target,
			End:   target,
		},
	}, nil
}

func definitionPosition(s *schema.Schema, id sqlctx.Identifier, ctx sqlctx.Context) *schema.Position {
	if id.Qualifier != "" {
		realName, ok := ctx.Aliases[id.Qualifier]
		if !ok {
			realName = id.Qualifier
		}
		if t, ok := s.Tables[realName]; ok {
			if c := findColumn(t, id.Name); c != nil {
				return &c.Position
			}
		}
		return nil
	}

	if t, ok := s.Tables[id.Name]; ok {
		return &t.Position
	}
	if real, ok := ctx.Aliases[id.Name]; ok {
		if t, ok := s.Tables[real]; ok {
			return &t.Position
		}
	}

	candidates := ctx.FromTables
	if len(candidates) == 0 {
		for n := range s.Tables {
			candidates = append(candidates, n)
		}
	}
	for _, name := range candidates {
		t, ok := s.Tables[name]
		if !ok {
			continue
		}
		if c := findColumn(t, id.Name); c != nil {
			return &c.Position
		}
	}
	return nil
}

func hover(_ *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	sch := currentSchema()
	if sch == nil {
		return nil, nil
	}
	uri := params.TextDocument.URI
	docsMu.Lock()
	text := docs[uri]
	docsMu.Unlock()

	var sql string
	var off int
	line, char := int(params.Position.Line), int(params.Position.Character)

	if strings.HasSuffix(uri, ".go") {
		s, o, ok := goast.FindSQL([]byte(text), line, char, currentSQLFuncs())
		if !ok {
			return nil, nil
		}
		sql, off = s, o
	} else {
		sql = text
		off = posenc.LSPToByte([]byte(text), line, char)
	}

	id, ok := sqlctx.IdentifierAt(sql, off)
	if !ok {
		return nil, nil
	}
	ctx := sqlctx.Analyze(sql, off)
	md := hoverMarkdown(sch, id, ctx)
	if md == "" {
		return nil, nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: md,
		},
	}, nil
}

func hoverMarkdown(s *schema.Schema, id sqlctx.Identifier, ctx sqlctx.Context) string {
	if id.Qualifier != "" {
		realName, ok := ctx.Aliases[id.Qualifier]
		if !ok {
			realName = id.Qualifier
		}
		if t, ok := s.Tables[realName]; ok {
			if c := findColumn(t, id.Name); c != nil {
				return fmt.Sprintf("**%s.%s** `%s`", t.Name, c.Name, c.Type)
			}
		}
		return ""
	}

	if t, ok := s.Tables[id.Name]; ok {
		return formatTable(t)
	}
	if real, ok := ctx.Aliases[id.Name]; ok && real != id.Name {
		if t, ok := s.Tables[real]; ok {
			return formatTable(t) + fmt.Sprintf("\n_alias: `%s`_", id.Name)
		}
	}

	candidates := ctx.FromTables
	if len(candidates) == 0 {
		for n := range s.Tables {
			candidates = append(candidates, n)
		}
	}
	for _, name := range candidates {
		t, ok := s.Tables[name]
		if !ok {
			continue
		}
		if c := findColumn(t, id.Name); c != nil {
			return fmt.Sprintf("**%s.%s** `%s`", t.Name, c.Name, c.Type)
		}
	}
	return ""
}

func findColumn(t *schema.Table, name string) *schema.Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func formatTable(t *schema.Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (table)\n\n", t.Name)
	b.WriteString("| Column | Type |\n| --- | --- |\n")
	for _, c := range t.Columns {
		fmt.Fprintf(&b, "| %s | `%s` |\n", c.Name, c.Type)
	}
	return b.String()
}

func tableItems(s *schema.Schema) []protocol.CompletionItem {
	kind := protocol.CompletionItemKindClass
	detail := "table"
	var items []protocol.CompletionItem
	for _, t := range s.Tables {
		items = append(items, protocol.CompletionItem{
			Label:  t.Name,
			Kind:   &kind,
			Detail: &detail,
		})
	}
	return items
}

func columnItems(t *schema.Table) []protocol.CompletionItem {
	kind := protocol.CompletionItemKindField
	var items []protocol.CompletionItem
	for _, c := range t.Columns {
		detail := fmt.Sprintf("%s.%s %s", t.Name, c.Name, c.Type)
		items = append(items, protocol.CompletionItem{
			Label:  c.Name,
			Kind:   &kind,
			Detail: &detail,
		})
	}
	return items
}

