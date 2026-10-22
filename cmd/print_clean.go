package cmd

import (
	"io"

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
