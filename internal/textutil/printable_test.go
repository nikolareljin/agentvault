package textutil

import (
	"strings"
	"testing"
)

func TestPrintable(t *testing.T) {
	if got := Printable("a\x1b[2J\x07b\u009bcé"); got != "a?[2J?b?cé" {
		t.Errorf("got %q", got)
	}
}

func TestPrintableText(t *testing.T) {
	if got := PrintableText("a\r\n\tb\rc\x1b[2J\u009bd\n"); got != "a\r\n\tb?c?[2J?d\n" {
		t.Errorf("got %q", got)
	}
}

func TestEscapeC1JSON(t *testing.T) {
	in := []byte("{\"a\":\"x\u009b2J\u0085é\"}")
	got := string(EscapeC1JSON(in))
	if got != `{"a":"x\u009b2J\u0085é"}` {
		t.Errorf("got %q", got)
	}
	var b strings.Builder
	if _, err := (C1JSONWriter{&b}).Write(in); err != nil || b.String() != got {
		t.Errorf("writer got %q, %v", b.String(), err)
	}
}
