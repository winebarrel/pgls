package lsp

import (
	"bytes"
	"encoding/json"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/winebarrel/pgls/schema"
)

// resetState clears state shared via package-level vars so tests don't
// leak into one another.
func resetState(t *testing.T) {
	t.Helper()
	docsMu.Lock()
	docs = map[string]string{}
	docsMu.Unlock()
	schemaMu.Lock()
	loadedSchema = nil
	schemaMu.Unlock()
	publishSeqs.Range(func(k, _ any) bool {
		publishSeqs.Delete(k)
		return true
	})
	notify = nil
}

func capturingNotify(out *[][]byte) func(method string, params any) {
	return func(method string, params any) {
		b, _ := json.Marshal(params)
		*out = append(*out, b)
	}
}

func minimalSchema() *schema.Schema {
	return &schema.Schema{
		Tables: map[string]*schema.Table{
			"users": {
				Name: "users",
				Columns: []*schema.Column{
					{Name: "id", Type: "int8"},
					{Name: "email", Type: "text"},
				},
			},
		},
	}
}

// Regression: when there are no Lint issues we must publish an empty
// JSON array, not null. VSCode treats null as "no change" and stale
// errors stick to the document.
func TestPublishDiagnostics_EmptyIsJSONArray(t *testing.T) {
	resetState(t)

	schemaMu.Lock()
	loadedSchema = minimalSchema()
	schemaMu.Unlock()

	uri := "file:///tmp/x.go"
	docsMu.Lock()
	docs[uri] = "package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT id FROM users`\n\t_ = q\n}\n"
	docsMu.Unlock()

	var captured [][]byte
	notify = capturingNotify(&captured)

	publishDiagnostics(uri)

	if len(captured) != 1 {
		t.Fatalf("want 1 notification, got %d", len(captured))
	}
	if !bytes.Contains(captured[0], []byte(`"diagnostics":[]`)) {
		t.Errorf("diagnostics should be []; got %s", captured[0])
	}
	if bytes.Contains(captured[0], []byte(`"diagnostics":null`)) {
		t.Errorf("diagnostics must not serialize as null: %s", captured[0])
	}
}

func TestPublishDiagnostics_FlagsUnknownTable(t *testing.T) {
	resetState(t)

	schemaMu.Lock()
	loadedSchema = minimalSchema()
	schemaMu.Unlock()

	uri := "file:///tmp/x.go"
	docsMu.Lock()
	docs[uri] = "package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT * FROM nope`\n\t_ = q\n}\n"
	docsMu.Unlock()

	var captured [][]byte
	notify = capturingNotify(&captured)
	publishDiagnostics(uri)

	if len(captured) != 1 {
		t.Fatalf("want 1 notification, got %d", len(captured))
	}
	var params protocol.PublishDiagnosticsParams
	if err := json.Unmarshal(captured[0], &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(params.Diagnostics))
	}
	if !bytes.Contains([]byte(params.Diagnostics[0].Message), []byte("nope")) {
		t.Errorf("got %q", params.Diagnostics[0].Message)
	}
}

// Regression: rapid back-to-back publishes for the same URI must
// always end with a notification reflecting the freshest state.
// Earlier each publishDiagnostics ran in its own goroutine and notify
// could land out of order, leaving the editor showing diagnostics
// from an intermediate text state.
func TestPublishDiagnostics_LatestStateWins(t *testing.T) {
	resetState(t)

	schemaMu.Lock()
	loadedSchema = minimalSchema()
	schemaMu.Unlock()

	uri := "file:///tmp/x.go"
	good := "package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT id FROM users`\n\t_ = q\n}\n"
	bad := "package main\n\nfunc main() {\n\t// language=sql\n\tq := `SELECT id FROM nope`\n\t_ = q\n}\n"

	var captured [][]byte
	notify = capturingNotify(&captured)

	docsMu.Lock()
	docs[uri] = bad
	docsMu.Unlock()
	publishDiagnostics(uri)

	docsMu.Lock()
	docs[uri] = good
	docsMu.Unlock()
	publishDiagnostics(uri)

	if len(captured) < 1 {
		t.Fatal("no notifications captured")
	}
	last := captured[len(captured)-1]
	if !bytes.Contains(last, []byte(`"diagnostics":[]`)) {
		t.Errorf("final notification should be empty diagnostics; got %s", last)
	}
}
