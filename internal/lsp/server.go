package lsp

import (
	"fmt"
	"strings"
	"sync"

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

	loadedSchema *schema.Schema
)

func Run(s *schema.Schema) error {
	loadedSchema = s
	handler = protocol.Handler{
		Initialize:             initialize,
		Initialized:            func(*glsp.Context, *protocol.InitializedParams) error { return nil },
		Shutdown:               func(*glsp.Context) error { return nil },
		SetTrace:               func(*glsp.Context, *protocol.SetTraceParams) error { return nil },
		TextDocumentDidOpen:    didOpen,
		TextDocumentDidChange:  didChange,
		TextDocumentDidClose:   didClose,
		TextDocumentCompletion: completion,
	}
	srv := server.NewServer(&handler, name, false)
	return srv.RunStdio()
}

func initialize(_ *glsp.Context, _ *protocol.InitializeParams) (any, error) {
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

func didOpen(_ *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	docsMu.Lock()
	defer docsMu.Unlock()
	docs[params.TextDocument.URI] = params.TextDocument.Text
	return nil
}

func didChange(_ *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	docsMu.Lock()
	defer docsMu.Unlock()
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
	return nil
}

func didClose(_ *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	docsMu.Lock()
	defer docsMu.Unlock()
	delete(docs, params.TextDocument.URI)
	return nil
}

func completion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	if loadedSchema == nil {
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
	return contextItems(loadedSchema, ctx), nil
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
			var items []protocol.CompletionItem
			for _, n := range ctx.FromTables {
				if t, ok := s.Tables[n]; ok {
					items = append(items, columnItems(t)...)
				}
			}
			return items
		}
		return allColumnItems(s)
	default:
		return allItems(s)
	}
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

func allColumnItems(s *schema.Schema) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, t := range s.Tables {
		items = append(items, columnItems(t)...)
	}
	return items
}

func allItems(s *schema.Schema) []protocol.CompletionItem {
	items := tableItems(s)
	items = append(items, allColumnItems(s)...)
	return items
}
