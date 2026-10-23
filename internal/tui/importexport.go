package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/textutil"
)

// The import and export forms only build an agentvault argument list and run
// the binary, like the ':' command bridge. All import/export logic stays in
// the CLI.

// agentvaultExecutable locates the binary the forms run. Tests replace it.
var agentvaultExecutable = os.Executable

// exportForm is the state of the export form.
type exportForm struct {
	Path          string // empty: the CLI's default export path
	IncludeKeys   bool
	Encrypt       bool
	IncludeMemory bool
}

func defaultExportForm() exportForm {
	// Matches the defaults of 'agentvault export -y'.
	return exportForm{IncludeKeys: true, Encrypt: true}
}

// exportArgs builds the argument list for 'agentvault export'. -y skips the
// wizard so the form's choices are what runs; a password prompt for an
// encrypted bundle still appears on the terminal.
func exportArgs(f exportForm) []string {
	args := []string{"export", "-y"}
	if !f.IncludeKeys {
		args = append(args, "--include-keys=false")
	}
	if !f.Encrypt {
		// The CLI refuses plain text with keys or secrets unless confirmed.
		args = append(args, "--plain", "--confirm")
	}
	if f.IncludeMemory {
		args = append(args, "--include-memory")
	}
	if p := strings.TrimSpace(f.Path); p != "" {
		// "--" keeps a path that starts with '-' from being read as a flag.
		args = append(args, "--", p)
	}
	return args
}

var (
	importStrategies  = []string{"merge", "replace", "mirror"}
	importOnConflicts = []string{"", "backup-replace", "keep"} // "" lets the CLI decide
)

// importForm is the state of the import form.
type importForm struct {
	Path        string // empty: the CLI picks the newest bundle
	Strategy    string // merge (CLI default), replace, mirror
	OnConflict  string // "", backup-replace, keep
	ProjectRoot string
	Maps        []string // OLD=NEW, in the order added
}

func (f importForm) mirror() bool { return f.Strategy == "mirror" }

// importArgs builds the argument list for 'agentvault import'.
func importArgs(f importForm, dryRun bool) []string {
	args := []string{"import"}
	if f.Strategy != "" && f.Strategy != "merge" {
		args = append(args, "--strategy", f.Strategy)
	}
	if f.OnConflict != "" {
		args = append(args, "--on-conflict", f.OnConflict)
	}
	if r := strings.TrimSpace(f.ProjectRoot); r != "" {
		// --project-root is only read with --apply-projects.
		args = append(args, "--apply-projects", "--project-root", r)
	}
	for _, m := range f.Maps {
		args = append(args, "--map", m)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if p := strings.TrimSpace(f.Path); p != "" {
		args = append(args, "--", p)
	}
	return args
}

// validateMapEntry checks the OLD=NEW shape. The CLI checks the paths.
func validateMapEntry(s string) (string, error) {
	s = strings.TrimSpace(s)
	from, to, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return "", fmt.Errorf("map %q: want OLD=NEW, both non-empty", s)
	}
	return s, nil
}

func (f importForm) validate() error {
	for _, m := range f.Maps {
		if _, err := validateMapEntry(m); err != nil {
			return err
		}
	}
	return nil
}

// displayCommand renders args as a shell-style line for the user to read.
// The command itself runs without a shell.
func displayCommand(args []string) string {
	parts := []string{"agentvault"}
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`;&|<>()*?[]#~!{}") {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// Form field indexes.
const (
	expFieldPath = iota
	expFieldKeys
	expFieldEncrypt
	expFieldMemory
	expFieldCount
)

const (
	impFieldPath = iota
	impFieldStrategy
	impFieldConflict
	impFieldRoot
	impFieldMap
	impFieldCount
)

// importPreviewMsg carries the output of the --dry-run preview.
type importPreviewMsg struct {
	seq    int // drops a result from a preview the user left
	output string
	err    error
}

func newFormInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = 1024
	ti.Width = 52
	ti.Cursor.SetMode(cursor.CursorStatic)
	return ti
}

func (m *model) openExport() tea.Cmd {
	m.mode = viewExport
	m.exp = defaultExportForm()
	m.expPath = newFormInput("default: <config dir>/exports/<host>-<timestamp>.avbundle")
	m.expFocus = expFieldPath
	return m.expPath.Focus()
}

func (m *model) openImport() tea.Cmd {
	m.mode = viewImport
	m.imp = importForm{Strategy: "merge"}
	m.impStrategy, m.impConflict = 0, 0
	m.impPath = newFormInput("default: newest bundle in <config dir>/exports")
	m.impRoot = newFormInput("optional: write the bundle's project files here")
	m.impMap = newFormInput("OLD=NEW, enter adds")
	m.impFocus = impFieldPath
	m.impOutput, m.impErr, m.impRunning = "", "", false
	return m.impPath.Focus()
}

// exportFormState reads the form inputs into the export state.
func (m model) exportFormState() exportForm {
	f := m.exp
	f.Path = expandTilde(m.expPath.Value())
	return f
}

// expandTilde does what a shell would do for a leading ~, since the forms
// run the CLI without one and the CLI takes these paths literally.
func expandTilde(p string) string {
	p = strings.TrimSpace(p)
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, p[1:])
}

func (m model) importFormState() importForm {
	f := m.imp
	f.Path = expandTilde(m.impPath.Value())
	f.ProjectRoot = expandTilde(m.impRoot.Value())
	f.Strategy = importStrategies[m.impStrategy]
	f.OnConflict = importOnConflicts[m.impConflict]
	return f
}

func (m *model) handleExportInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
		return m, nil
	case key.Matches(msg, keys.Submit):
		return m, m.runCLI(exportArgs(m.exportFormState()))
	case key.Matches(msg, keys.NextField):
		return m, m.focusExportField((m.expFocus + 1) % expFieldCount)
	case key.Matches(msg, keys.PrevField):
		return m, m.focusExportField((m.expFocus + expFieldCount - 1) % expFieldCount)
	}
	if m.expFocus == expFieldPath {
		var cmd tea.Cmd
		m.expPath, cmd = m.expPath.Update(msg)
		return m, cmd
	}
	if key.Matches(msg, keys.Toggle) {
		switch m.expFocus {
		case expFieldKeys:
			m.exp.IncludeKeys = !m.exp.IncludeKeys
		case expFieldEncrypt:
			m.exp.Encrypt = !m.exp.Encrypt
		case expFieldMemory:
			m.exp.IncludeMemory = !m.exp.IncludeMemory
		}
	}
	return m, nil
}

func (m *model) focusExportField(i int) tea.Cmd {
	m.expFocus = i
	if i == expFieldPath {
		return m.expPath.Focus()
	}
	m.expPath.Blur()
	return nil
}

func (m *model) handleImportInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
		return m, nil
	case key.Matches(msg, keys.NextField):
		return m, m.focusImportField((m.impFocus + 1) % impFieldCount)
	case key.Matches(msg, keys.PrevField):
		return m, m.focusImportField((m.impFocus + impFieldCount - 1) % impFieldCount)
	case key.Matches(msg, keys.Submit):
		if m.impFocus == impFieldMap && strings.TrimSpace(m.impMap.Value()) != "" {
			entry, err := validateMapEntry(m.impMap.Value())
			if err != nil {
				m.setStatus("Error: "+err.Error(), true)
				return m, nil
			}
			m.imp.Maps = append(m.imp.Maps, entry)
			m.impMap.SetValue("")
			return m, nil
		}
		return m, m.startImportPreview()
	}
	switch m.impFocus {
	case impFieldStrategy:
		m.impStrategy = cycle(msg, m.impStrategy, len(importStrategies))
		return m, nil
	case impFieldConflict:
		m.impConflict = cycle(msg, m.impConflict, len(importOnConflicts))
		return m, nil
	case impFieldMap:
		if key.Matches(msg, keys.RemoveMap) {
			if n := len(m.imp.Maps); n > 0 {
				m.imp.Maps = m.imp.Maps[:n-1]
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	switch m.impFocus {
	case impFieldPath:
		m.impPath, cmd = m.impPath.Update(msg)
	case impFieldRoot:
		m.impRoot, cmd = m.impRoot.Update(msg)
	case impFieldMap:
		m.impMap, cmd = m.impMap.Update(msg)
	}
	return m, cmd
}

func cycle(msg tea.KeyMsg, i, n int) int {
	switch {
	case key.Matches(msg, keys.CycleNext):
		return (i + 1) % n
	case key.Matches(msg, keys.CyclePrev):
		return (i + n - 1) % n
	}
	return i
}

func (m *model) focusImportField(i int) tea.Cmd {
	m.impPath.Blur()
	m.impRoot.Blur()
	m.impMap.Blur()
	m.impFocus = i
	switch i {
	case impFieldPath:
		return m.impPath.Focus()
	case impFieldRoot:
		return m.impRoot.Focus()
	case impFieldMap:
		return m.impMap.Focus()
	}
	return nil
}

// startImportPreview runs the import with --dry-run and captures its output,
// so it renders inside the TUI.
func (m *model) startImportPreview() tea.Cmd {
	if strings.TrimSpace(m.impMap.Value()) != "" {
		m.setStatus("Error: the map entry is not added yet; press enter on it, or clear it", true)
		return nil
	}
	f := m.importFormState()
	if err := f.validate(); err != nil {
		m.setStatus("Error: "+err.Error(), true)
		return nil
	}
	exe, err := agentvaultExecutable()
	if err != nil {
		m.setStatus(fmt.Sprintf("Unable to locate agentvault binary: %v", err), true)
		return nil
	}
	m.mode = viewImportPreview
	m.impRunning = true
	m.impOutput, m.impErr = "", ""
	m.impSeq++
	seq, args := m.impSeq, importArgs(f, true)
	// The CLI may ask for the master and bundle passwords, so the preview runs
	// on the terminal like the real import; its output is also copied into
	// buf and shown in the preview screen afterwards.
	var buf bytes.Buffer
	c := exec.Command(exe, args...)
	c.Stdout = io.MultiWriter(os.Stdout, &buf)
	c.Stderr = io.MultiWriter(os.Stderr, &buf)
	return runPreviewProcess(c, func(err error) tea.Msg {
		return importPreviewMsg{seq: seq, output: buf.String(), err: err}
	})
}

// runPreviewProcess hands the terminal to the preview command. Tests replace
// it to run the command without a terminal.
var runPreviewProcess = func(c *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
	return tea.ExecProcess(c, done)
}

func (m *model) handleImportPreviewMsg(msg importPreviewMsg) {
	if msg.seq != m.impSeq || m.mode != viewImportPreview {
		return
	}
	m.impRunning = false
	m.impOutput = msg.output
	m.impErr = ""
	if msg.err != nil {
		m.impErr = msg.err.Error()
	}
}

func (m *model) handleImportPreviewInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back), key.Matches(msg, keys.EditForm):
		m.mode = viewImport
		m.impRunning = false
		m.impSeq++ // a preview still running is ignored when it ends
		return m, nil
	}
	if m.impRunning {
		return m, nil
	}
	// The preview had the terminal for passwords, so a failure is real: fix
	// the form and preview again rather than run the import blind.
	if m.impErr != "" && (key.Matches(msg, keys.ConfirmImport) || key.Matches(msg, keys.ConfirmMirror)) {
		m.setStatus("The preview failed: press e to fix the form and preview again", true)
		return m, nil
	}
	f := m.importFormState()
	switch {
	case key.Matches(msg, keys.ConfirmMirror):
		if !f.mirror() {
			return m, nil
		}
		return m, m.runCLI(importArgs(f, false))
	case key.Matches(msg, keys.ConfirmImport):
		if f.mirror() {
			m.setStatus("Mirror deletes local items: press M to run it", true)
			return m, nil
		}
		return m, m.runCLI(importArgs(f, false))
	}
	return m, nil
}

// runCLI runs agentvault with args on the terminal, so it can prompt for a
// password or a confirmation.
func (m *model) runCLI(args []string) tea.Cmd {
	exe, err := agentvaultExecutable()
	if err != nil {
		m.setStatus(fmt.Sprintf("Unable to locate agentvault binary: %v", err), true)
		return nil
	}
	m.closeOverlay()
	m.commandRunning = true
	line := displayCommand(args)
	return tea.ExecProcess(exec.Command(exe, args...), func(err error) tea.Msg {
		return cliCommandFinishedMsg{command: line, err: err}
	})
}

func formMarker(focused bool) string {
	if focused {
		return "> "
	}
	return "  "
}

func onOff(b bool) string {
	if b {
		return "[x]"
	}
	return "[ ]"
}

func (m model) renderExport() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Export"))
	b.WriteString("\n\n")
	row := func(i int, label, value string) {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%s%-16s", formMarker(i == m.expFocus), label)))
		b.WriteString(" " + value + "\n")
	}
	row(expFieldPath, "Output file", m.expPath.View())
	row(expFieldKeys, "Include API keys", onOff(m.exp.IncludeKeys))
	row(expFieldEncrypt, "Encrypt", onOff(m.exp.Encrypt))
	row(expFieldMemory, "Include memory", onOff(m.exp.IncludeMemory))
	b.WriteString("\n")
	f := m.exportFormState()
	if f.IncludeKeys && !f.Encrypt {
		b.WriteString(errorStyle.Render("  Warning: API keys and secrets go into the file in plain text."))
		b.WriteString("\n")
	} else if f.IncludeKeys {
		b.WriteString(errorStyle.Render("  Warning: API keys go into the file (encrypted with the bundle password)."))
		b.WriteString("\n")
	} else if !f.Encrypt {
		b.WriteString(errorStyle.Render("  Warning: provider secrets go into the file in plain text."))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("  Runs: " + textutil.Printable(displayCommand(exportArgs(f)))))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderImport() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Import"))
	b.WriteString("\n\n")
	f := m.importFormState()
	row := func(i int, label, value string) {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%s%-13s", formMarker(i == m.impFocus), label)))
		b.WriteString(" " + value + "\n")
	}
	conflict := f.OnConflict
	if conflict == "" {
		conflict = "(CLI decides)"
	}
	row(impFieldPath, "Bundle", m.impPath.View())
	row(impFieldStrategy, "Strategy", "< "+f.Strategy+" >")
	row(impFieldConflict, "On conflict", "< "+conflict+" >")
	row(impFieldRoot, "Project root", m.impRoot.View())
	row(impFieldMap, "Add map", m.impMap.View())
	for i, mp := range f.Maps {
		b.WriteString(fmt.Sprintf("                 %d. --map %s\n", i+1, textutil.Printable(mp)))
	}
	b.WriteString("\n")
	if f.mirror() {
		b.WriteString(errorStyle.Render("  Warning: mirror deletes local items the bundle does not have."))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("  Preview runs: " + textutil.Printable(displayCommand(importArgs(f, true)))))
	b.WriteString("\n")
	return b.String()
}

// previewMaxLines caps how much dry-run output the preview shows.
const previewMaxLines = 40

func (m model) renderImportPreview() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Import preview (dry run)"))
	b.WriteString("\n\n")
	f := m.importFormState()
	if m.impRunning {
		b.WriteString(dimStyle.Render("  Running: " + textutil.Printable(displayCommand(importArgs(f, true)))))
		b.WriteString("\n")
		return b.String()
	}
	lines := strings.Split(strings.TrimRight(textutil.PrintableText(m.impOutput), "\n"), "\n")
	if len(lines) > previewMaxLines {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ... %d earlier lines not shown", len(lines)-previewMaxLines)))
		b.WriteString("\n")
		lines = lines[len(lines)-previewMaxLines:]
	}
	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n")
	if m.impErr != "" {
		b.WriteString(errorStyle.Render("  Preview failed: " + textutil.Printable(m.impErr)))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Press e to fix the form and preview again; the import does not run after a failed preview."))
		b.WriteString("\n")
	}
	if f.mirror() {
		b.WriteString(errorStyle.Render("  Warning: mirror deletes local items the bundle does not have. Press M to run it."))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("  Runs: " + textutil.Printable(displayCommand(importArgs(f, false)))))
	b.WriteString("\n")
	return b.String()
}
