package posenc

import "testing"

func TestASCII(t *testing.T) {
	src := []byte("abc\ndef")
	cases := []struct {
		line, char, want int
	}{
		{0, 0, 0},
		{0, 1, 1},
		{0, 3, 3}, // end of line
		{1, 0, 4},
		{1, 2, 6},
	}
	for _, c := range cases {
		if got := LSPToByte(src, c.line, c.char); got != c.want {
			t.Errorf("(%d,%d): got %d, want %d", c.line, c.char, got, c.want)
		}
	}
}

func TestBMPMultibyte(t *testing.T) {
	// "あいう" — each rune is 3 bytes in UTF-8 and 1 UTF-16 code unit.
	src := []byte("あいう")
	cases := []struct {
		char, want int
	}{
		{0, 0},
		{1, 3},
		{2, 6},
		{3, 9},
	}
	for _, c := range cases {
		if got := LSPToByte(src, 0, c.char); got != c.want {
			t.Errorf("char=%d: got %d, want %d", c.char, got, c.want)
		}
	}
}

func TestSurrogatePair(t *testing.T) {
	// "🎉ab" — 🎉 (U+1F389) is 4 bytes UTF-8 and 2 UTF-16 code units.
	src := []byte("🎉ab")
	cases := []struct {
		char, want int
	}{
		{0, 0},
		{2, 4}, // after the emoji
		{3, 5}, // after 'a'
		{4, 6}, // after 'b'
	}
	for _, c := range cases {
		if got := LSPToByte(src, 0, c.char); got != c.want {
			t.Errorf("char=%d: got %d, want %d", c.char, got, c.want)
		}
	}
}

func TestMixed(t *testing.T) {
	// line 0: "abc"
	// line 1: "あい"  (each 3 bytes / 1 unit)
	// line 2: "🎉"    (4 bytes / 2 units)
	src := []byte("abc\nあい\n🎉")
	cases := []struct {
		line, char, want int
	}{
		{1, 0, 4},     // start of line 1
		{1, 1, 4 + 3}, // after あ
		{1, 2, 4 + 6}, // after い
		{2, 0, 11},    // start of line 2
		{2, 2, 15},    // after 🎉 (4 bytes)
	}
	for _, c := range cases {
		if got := LSPToByte(src, c.line, c.char); got != c.want {
			t.Errorf("(%d,%d): got %d, want %d", c.line, c.char, got, c.want)
		}
	}
}

func TestPastEnd(t *testing.T) {
	src := []byte("abc")
	if got := LSPToByte(src, 0, 100); got != 3 {
		t.Errorf("char past end: got %d, want 3", got)
	}
	if got := LSPToByte(src, 5, 0); got != 3 {
		t.Errorf("line past end: got %d, want 3", got)
	}
}

func TestByteToLSP(t *testing.T) {
	// Round-trip with ASCII and multibyte content.
	src := []byte("abc\nあい\n🎉ab")
	cases := []struct {
		offset, line, char int
	}{
		{0, 0, 0},
		{2, 0, 2},     // mid line 0
		{4, 1, 0},     // start of line 1
		{4 + 3, 1, 1}, // after あ
		{11, 2, 0},    // start of line 2
		{15, 2, 2},    // after 🎉
		{16, 2, 3},    // after a
	}
	for _, c := range cases {
		gotLine, gotChar := ByteToLSP(src, c.offset)
		if gotLine != c.line || gotChar != c.char {
			t.Errorf("offset=%d: got (%d,%d), want (%d,%d)", c.offset, gotLine, gotChar, c.line, c.char)
		}
	}
}

func TestStopsAtNewline(t *testing.T) {
	// Asking for char beyond the visible line should not cross the newline.
	src := []byte("ab\ncd")
	if got := LSPToByte(src, 0, 100); got != 2 {
		t.Errorf("got %d, want 2 (newline at index 2)", got)
	}
}
