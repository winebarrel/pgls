// Package posenc converts LSP positions (UTF-16 code units, the LSP
// default) into byte offsets within a UTF-8 source buffer.
package posenc

import (
	"bytes"
	"unicode/utf8"
)

// LSPToByte converts a 0-indexed (line, character) LSP position to a
// byte offset within src. character is interpreted as UTF-16 code
// units, matching the LSP default position encoding.
//
// If the position lies past the end of its line or past the end of
// src, the returned offset is clamped to the end of the line or src.
func LSPToByte(src []byte, line, character int) int {
	if line < 0 {
		line = 0
	}
	if character < 0 {
		character = 0
	}

	pos := 0
	for l := 0; l < line; l++ {
		nl := bytes.IndexByte(src[pos:], '\n')
		if nl < 0 {
			return len(src)
		}
		pos += nl + 1
	}

	units := 0
	for pos < len(src) {
		if units >= character {
			return pos
		}
		r, size := utf8.DecodeRune(src[pos:])
		if r == '\n' {
			return pos
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		pos += size
	}
	return pos
}
