package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

//
// Copied from stdlib strings.TrimSpace, modified to only trim leading spaces and to return
// the number of trimmed characters
//

var asciiSpace = [256]uint8{'\t': 1, '\n': 1, '\v': 1, '\f': 1, '\r': 1, ' ': 1}

func trimLeadingSpace(s string) (string, int) {
	// Fast path for ASCII: look for the first ASCII non-space byte
	start := 0
	for ; start < len(s); start++ {
		c := s[start]
		if c >= utf8.RuneSelf {
			// If we run into a non-ASCII byte, fall back to the
			// slower unicode-aware method on the remaining bytes
			trimmed := strings.TrimFunc(s[start:], unicode.IsSpace)
			return trimmed, len([]rune(s)) - len([]rune(trimmed))
		}
		if asciiSpace[c] == 0 {
			break
		}
	}

	return s[start:len(s)], start
}
