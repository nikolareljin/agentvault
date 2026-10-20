package textutil

import "testing"

func TestPrintable(t *testing.T) {
	if got := Printable("a\x1b[2J\x07b\u009bcé"); got != "a?[2J?b?cé" {
		t.Errorf("got %q", got)
	}
}
