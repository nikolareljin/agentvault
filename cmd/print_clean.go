package cmd

import (
	"io"

	"github.com/spf13/cobra"

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

// rawFlagUsage is the help text of --raw on commands that print content.
const rawFlagUsage = "print content byte-exact, control characters and terminal escapes included (for redirecting to a file; unsafe on a terminal)"

// contentText prints content through printableText unless the command was
// given --raw, which a user picks to write the content byte-exact to a file.
// Whether stdout is a terminal is not a signal: a pipe to less -R or a
// tmux pane still reaches one.
func contentText(cmd *cobra.Command, s string) string {
	if raw, _ := cmd.Flags().GetBool("raw"); raw {
		return s
	}
	return printableText(s)
}
