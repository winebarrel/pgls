package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func makeRange(sl, sc, el, ec uint32) *protocol.Range {
	return &protocol.Range{
		Start: protocol.Position{Line: sl, Character: sc},
		End:   protocol.Position{Line: el, Character: ec},
	}
}

func TestApplyContentChange_FullReplacement(t *testing.T) {
	got := applyContentChange("abc", protocol.TextDocumentContentChangeEvent{Text: "xyz"})
	if got != "xyz" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_Insert(t *testing.T) {
	// Insert "BC" between "a" and "d" → "aBCd"
	got := applyContentChange("ad", protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 0, 1),
		Text:  "BC",
	})
	if got != "aBCd" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_Replace(t *testing.T) {
	// Replace "bc" with "XY" in "abcd" → "aXYd"
	got := applyContentChange("abcd", protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 0, 3),
		Text:  "XY",
	})
	if got != "aXYd" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_Delete(t *testing.T) {
	got := applyContentChange("abcd", protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 0, 3),
		Text:  "",
	})
	if got != "ad" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_AcrossLines(t *testing.T) {
	// Replace "bc\nde" with "X" → "aXf"
	text := "abc\ndef"
	got := applyContentChange(text, protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 1, 2),
		Text:  "X",
	})
	if got != "aXf" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_Multibyte(t *testing.T) {
	// "あい" — each rune is 3 UTF-8 bytes / 1 UTF-16 unit.
	// Delete "い" (UTF-16 char 1..2)  → "あ"
	got := applyContentChange("あい", protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 0, 2),
		Text:  "",
	})
	if got != "あ" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_SurrogatePair(t *testing.T) {
	// 🎉 occupies 2 UTF-16 units / 4 UTF-8 bytes.
	// Replace it with "X" → "Xab"
	got := applyContentChange("🎉ab", protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 0, 0, 2),
		Text:  "X",
	})
	if got != "Xab" {
		t.Errorf("got %q", got)
	}
}

func TestApplyContentChange_Sequence(t *testing.T) {
	// Two edits in one batch: insert ", " after "a", then append "!" at the end.
	// The second change's positions refer to the doc *after* the first applies.
	text := "ab"
	text = applyContentChange(text, protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 1, 0, 1),
		Text:  ", ",
	})
	// after first: "a, b"
	text = applyContentChange(text, protocol.TextDocumentContentChangeEvent{
		Range: makeRange(0, 4, 0, 4),
		Text:  "!",
	})
	if text != "a, b!" {
		t.Errorf("got %q", text)
	}
}
