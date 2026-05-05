package lsp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
	"github.com/winebarrel/pgls/internal/goast"
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
	if strings.HasSuffix(uri, ".go") {
		docsMu.Lock()
		text := docs[uri]
		docsMu.Unlock()
		if _, _, ok := goast.FindSQL([]byte(text), int(params.Position.Line), int(params.Position.Character)); !ok {
			return []protocol.CompletionItem{}, nil
		}
	}
	return schemaItems(loadedSchema), nil
}

func schemaItems(s *schema.Schema) []protocol.CompletionItem {
	tableKind := protocol.CompletionItemKindClass
	fieldKind := protocol.CompletionItemKindField
	var items []protocol.CompletionItem
	for _, t := range s.Tables {
		tableDetail := "table"
		items = append(items, protocol.CompletionItem{
			Label:  t.Name,
			Kind:   &tableKind,
			Detail: &tableDetail,
		})
		for _, c := range t.Columns {
			colDetail := fmt.Sprintf("%s.%s %s", t.Name, c.Name, c.Type)
			items = append(items, protocol.CompletionItem{
				Label:  c.Name,
				Kind:   &fieldKind,
				Detail: &colDetail,
			})
		}
	}
	return items
}
