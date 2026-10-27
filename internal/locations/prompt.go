package locations

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nikolareljin/agentvault/internal/textutil"
)

// ErrAborted is returned when the user quits a dialog or input ends.
var ErrAborted = errors.New("aborted")

// Row is one line of the confirmation dialog: a bundle path and where it
// goes here. To is empty when the row is skipped or not yet chosen.
type Row struct {
	From    string
	To      string
	Note    string
	Choices []string
}

// Confirm shows rows as a numbered list and lets the user change each
// row's target. A row number edits that row; an empty line accepts the list
// as shown; q aborts. home expands a leading ~ in typed paths. A typed path
// must be an existing absolute directory. Rows left without a target are
// returned with To empty and mean "do not match".
func Confirm(rows []Row, home string, in *bufio.Reader, out io.Writer) ([]Row, error) {
	rows = append([]Row(nil), rows...)
	for {
		fmt.Fprintln(out, "Projects in the bundle and where they go here:")
		for i, r := range rows {
			to := r.To
			if to == "" {
				to = "(skipped)"
				if len(r.Choices) > 0 {
					to = "(choose one)"
				}
			}
			fmt.Fprintf(out, "  %d. %s -> %s\n", i+1, textutil.Printable(r.From), textutil.Printable(to))
			if r.Note != "" {
				fmt.Fprintf(out, "       %s\n", textutil.Printable(r.Note))
			}
			for j, c := range r.Choices {
				fmt.Fprintf(out, "       %c) %s\n", 'a'+j, textutil.Printable(c))
			}
		}
		fmt.Fprint(out, "Enter a row number to change its path, empty line to accept all, q to abort: ")
		line, err := readLine(in)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(line) {
		case "":
			return rows, nil
		case "q", "quit":
			return nil, ErrAborted
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(rows) {
			fmt.Fprintf(out, "No row %q.\n", line)
			continue
		}
		r := &rows[n-1]
		hint := "a path"
		if len(r.Choices) > 0 {
			hint = fmt.Sprintf("a letter a-%c or a path", 'a'+len(r.Choices)-1)
		}
		fmt.Fprintf(out, "New path for row %d (%s, - to skip, empty keeps it): ", n, hint)
		answer, err := readLine(in)
		if err != nil {
			return nil, err
		}
		switch {
		case answer == "":
		case answer == "-":
			r.To = ""
		case len(answer) == 1 && answer[0] >= 'a' && int(answer[0]-'a') < len(r.Choices):
			r.To = r.Choices[answer[0]-'a']
		default:
			p, err := typedDir(answer, home)
			if err != nil {
				fmt.Fprintf(out, "Not changed: %v\n", err)
				continue
			}
			r.To = p
		}
	}
}

// typedDir checks a path the user typed: ~ expanded, absolute, an existing
// directory.
func typedDir(s, home string) (string, error) {
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		if home == "" {
			return "", errors.New("no home directory to expand ~")
		}
		s = filepath.Join(home, s[1:])
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("%q is not an absolute path", s)
	}
	s = filepath.Clean(s)
	if !isDir(s) {
		return "", fmt.Errorf("%s is not a directory", s)
	}
	return s, nil
}

// readLine reads one trimmed line. End of input before any text aborts, so
// a closed stdin never accepts a list nobody read.
func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", ErrAborted
	}
	return strings.TrimSpace(line), nil
}

// AskWorkDir asks where an unmatched work dir is on this machine, offering
// suggestions. It returns "" when the user skips it.
func AskWorkDir(w WorkDir, suggestions []string, home string, in *bufio.Reader, out io.Writer) (string, error) {
	for {
		if w.Name != "" {
			fmt.Fprintf(out, "Work dir %s (%q, named %s) was not found here.\n", textutil.Printable(w.Abs), w.Label, textutil.Printable(w.Name))
		} else {
			fmt.Fprintf(out, "Work dir %s (%q) was not found here.\n", textutil.Printable(w.Abs), w.Label)
		}
		for i, s := range suggestions {
			fmt.Fprintf(out, "  %d. %s\n", i+1, textutil.Printable(s))
		}
		fmt.Fprint(out, "Enter a number, a path, empty line to skip, q to abort: ")
		line, err := readLine(in)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(line) {
		case "":
			return "", nil
		case "q", "quit":
			return "", ErrAborted
		}
		if n, err := strconv.Atoi(line); err == nil {
			if n >= 1 && n <= len(suggestions) {
				return suggestions[n-1], nil
			}
			fmt.Fprintf(out, "No suggestion %d.\n", n)
			continue
		}
		p, err := typedDir(line, home)
		if err != nil {
			fmt.Fprintf(out, "%v\n", err)
			continue
		}
		return p, nil
	}
}

// ConfirmHidden asks about one target inside a hidden folder: y keeps it,
// a typed path replaces it, an empty line skips it. It returns "" for skip.
func ConfirmHidden(from, to, home string, in *bufio.Reader, out io.Writer) (string, error) {
	for {
		fmt.Fprintf(out, "%s -> %s is inside a hidden folder.\n", textutil.Printable(from), textutil.Printable(to))
		fmt.Fprint(out, "Enter y to use it, a different path, empty line to skip, q to abort: ")
		line, err := readLine(in)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(line) {
		case "":
			return "", nil
		case "y", "yes":
			return to, nil
		case "q", "quit":
			return "", ErrAborted
		}
		p, err := typedDir(line, home)
		if err != nil {
			fmt.Fprintf(out, "%v\n", err)
			continue
		}
		return p, nil
	}
}

// HomeDir is os.UserHomeDir cleaned, or "".
func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(h)
}
