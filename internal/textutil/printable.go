package textutil

import "strings"

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
