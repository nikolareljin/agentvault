package textutil

import (
	"fmt"
	"io"
	"strings"
)

// Printable replaces C0 and C1 control characters and DEL with '?', so text
// from a bundle cannot move the cursor or rewrite what a terminal shows.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

// PrintableText is Printable for multi-line text such as an instruction
// file: it keeps newlines, tabs and the CR of a CRLF.
func PrintableText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r' && i+1 < len(s) && s[i+1] == '\n':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EscapeC1JSON escapes C1 control characters in JSON text as \u00XX.
// encoding/json escapes C0 but writes C1 raw, and some terminals act on
// it. Raw C1 can only occur inside JSON strings, so the value is unchanged.
func EscapeC1JSON(data []byte) []byte {
	if !hasC1(data) {
		return data
	}
	out := make([]byte, 0, len(data)+16)
	for i := 0; i < len(data); i++ {
		if data[i] == 0xc2 && i+1 < len(data) && data[i+1] >= 0x80 && data[i+1] < 0xa0 {
			out = append(out, fmt.Sprintf(`\u%04x`, data[i+1])...)
			i++
			continue
		}
		out = append(out, data[i])
	}
	return out
}

func hasC1(data []byte) bool {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == 0xc2 && data[i+1] >= 0x80 && data[i+1] < 0xa0 {
			return true
		}
	}
	return false
}

// C1JSONWriter applies EscapeC1JSON to each write. json.Encoder writes a
// whole value per call, so no character is split across writes.
type C1JSONWriter struct{ W io.Writer }

func (w C1JSONWriter) Write(p []byte) (int, error) {
	if _, err := w.W.Write(EscapeC1JSON(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
