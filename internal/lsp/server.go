package lsp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

const (
	name    = "pgls"
	version = "0.0.1"
)

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
)

func Run(cliSchemaDir string) error {
	cliDir = cliSchemaDir
	handler = protocol.Handler{
		Initialize:             initialize,
		Initialized:            func(*glsp.Context, *protocol.InitializedParams) error { return nil },
		Shutdown:               func(*glsp.Context) error { return nil },
		SetTrace:               func(*glsp.Context, *protocol.SetTraceParams) error { return nil },
		TextDocumentDidOpen:    didOpen,
		TextDocumentDidChange:  didChange,
		TextDocumentDidClose:   didClose,
		TextDocumentCompletion: completion,
		TextDocumentHover:      hover,
	}
	srv := server.NewServer(&handler, name, false)
	return srv.RunStdio()
}

type initOptions struct {
	SchemaDir string `json:"schemaDir"`
}

func initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	notify = ctx.Notify

	dir := schemaDirFromOptions(params)
	if dir == "" {
		dir = cliDir
	}
	if dir != "" {
		loadAndSetSchema(dir)
		startSchemaWatcher(dir)
	}

	caps := handler.CreateServerCapabilities()
	caps.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{".", " "},
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

func schemaDirFromOptions(params *protocol.InitializeParams) string {
	if params.InitializationOptions == nil {
		return ""
	}
	b, err := json.Marshal(params.InitializationOptions)
	if err != nil {
		return ""
	}
	var opts initOptions
	if err := json.Unmarshal(b, &opts); err != nil {
		return ""
	}
	if opts.SchemaDir == "" {
		return ""
	}
	if filepath.IsAbs(opts.SchemaDir) {
		return opts.SchemaDir
	}
	if root := workspaceRoot(params); root != "" {
		return filepath.Join(root, opts.SchemaDir)
	}
	return opts.SchemaDir
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
			w.Close()
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
	defer w.Close()

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
			if c.Range == nil {
				text = c.Text
			}
		}
	}
	docs[params.TextDocument.URI] = text
	docsMu.Unlock()
	publishDiagnostics(params.TextDocument.URI)
	return nil
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
	if notify == nil {
		return
	}
	s := currentSchema()
	if s == nil {
		return
	}
	docsMu.Lock()
	text := docs[uri]
	docsMu.Unlock()
	if text == "" {
		return
	}

	src := []byte(text)
	var diags []protocol.Diagnostic

	if strings.HasSuffix(uri, ".go") {
		for _, blk := range goast.FindAllSQL(src) {
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
		s, o, ok := goast.FindSQL([]byte(text), line, char)
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
		s, o, ok := goast.FindSQL([]byte(text), line, char)
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

