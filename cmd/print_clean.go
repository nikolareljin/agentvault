package cmd

import (
	"io"
	"os"

	"golang.org/x/term"

	"github.com/nikolareljin/agentvault/internal/textutil"
)

// printableText is printable for multi-line content such as an
// instruction file or a role prompt, which a bundle can set.
func printableText(s string) string {
	return textutil.PrintableText(s)
}

// c1Writer wraps a writer for JSON output so vault strings cannot carry C1
// control characters to the terminal.
func c1Writer(w io.Writer) io.Writer {
	return textutil.C1JSONWriter{W: w}
}

// escapeC1JSON escapes C1 control characters in JSON (or YAML, which
// already escapes them) printed to the terminal.
func escapeC1JSON(data []byte) []byte {
	return textutil.EscapeC1JSON(data)
}

// stdoutIsTerminal reports whether stdout is a terminal; tests replace it.
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) // #nosec G115 -- fd fits in int
}

// contentText is for content a user redirects into a file, such as
// `instructions show > CLAUDE.md` or `rules export > RULES.md`: it is
// byte-exact unless stdout is a terminal, where it is printableText.
func contentText(s string) string {
	if stdoutIsTerminal() {
		return printableText(s)
	}
	return s
}

// contentLine is contentText for a single line, such as a heading.
func contentLine(s string) string {
	if stdoutIsTerminal() {
		return printable(s)
	}
	return s
}
