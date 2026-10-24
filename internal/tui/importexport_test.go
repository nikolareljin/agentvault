package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeCLIEnv makes the test binary act as a fake agentvault: it prints its
// arguments one per line, and exits 3 when the value is "fail". The preview
// tests point agentvaultExecutable at the test binary, so no shell, script
// or extra build is needed on any OS.
const fakeCLIEnv = "AGENTVAULT_TUI_FAKE_CLI"

func runFakeCLI() int {
	for _, a := range os.Args[1:] {
		fmt.Println("arg:" + a)
	}
	if os.Getenv(fakeCLIEnv) == "fail" {
		fmt.Fprintln(os.Stderr, "Error: fake failure")
		return 3
	}
	return 0
}

func useFakeCLI(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(fakeCLIEnv, mode)
	prev := agentvaultExecutable
	agentvaultExecutable = os.Executable
	prevRun := runPreviewProcess
	runPreviewProcess = func(c *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		previewCmds = append(previewCmds, c)
		return func() tea.Msg { return done(c.Run()) }
	}
	t.Cleanup(func() { agentvaultExecutable, runPreviewProcess, previewCmds = prev, prevRun, nil })
}

// previewCmds records the preview commands the fake runner was given.
var previewCmds []*exec.Cmd

// The preview may prompt for passwords, so it gets the terminal: stdin is
// left for ExecProcess to attach, and output is copied, not captured alone.
func TestImportPreviewGetsTerminal(t *testing.T) {
	useFakeCLI(t, "ok")
	runPreview(t, openImportForm(t))
	if len(previewCmds) != 1 {
		t.Fatalf("preview did not go through runPreviewProcess (%d calls)", len(previewCmds))
	}
	if c := previewCmds[0]; c.Stdin != nil || c.Stdout == nil || c.Stderr == nil {
		t.Fatalf("stdin=%v stdout=%v stderr=%v, want terminal stdin and copied output", c.Stdin, c.Stdout, c.Stderr)
	}
}

func TestExportArgs(t *testing.T) {
	d := defaultExportForm()
	with := func(f func(*exportForm)) exportForm { e := d; f(&e); return e }
	tests := []struct {
		name string
		form exportForm
		want []string
	}{
		{"defaults are minimal", d, []string{"export", "-y"}},
		{"path with spaces stays one arg", with(func(e *exportForm) { e.Path = "/tmp/my dir/out.avbundle" }),
			[]string{"export", "-y", "--", "/tmp/my dir/out.avbundle"}},
		{"blank path omitted", with(func(e *exportForm) { e.Path = "   " }), []string{"export", "-y"}},
		{"no keys", with(func(e *exportForm) { e.IncludeKeys = false }), []string{"export", "-y", "--include-keys=false"}},
		{"plain", with(func(e *exportForm) { e.Encrypt = false }), []string{"export", "-y", "--plain", "--confirm"}},
		{"memory", with(func(e *exportForm) { e.IncludeMemory = true }), []string{"export", "-y", "--include-memory"}},
		{"dash path", with(func(e *exportForm) { e.Path = "-x" }), []string{"export", "-y", "--", "-x"}},
	}
	for _, tc := range tests {
		if got := exportArgs(tc.form); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestImportArgs(t *testing.T) {
	tests := []struct {
		name   string
		form   importForm
		dryRun bool
		want   []string
	}{
		{"defaults are minimal", importForm{Strategy: "merge"}, false, []string{"import"}},
		{"dry run", importForm{Strategy: "merge"}, true, []string{"import", "--dry-run"}},
		{"replace", importForm{Strategy: "replace"}, false, []string{"import", "--strategy", "replace"}},
		{"mirror flagged", importForm{Strategy: "mirror"}, false, []string{"import", "--strategy", "mirror"}},
		{"on conflict", importForm{Strategy: "merge", OnConflict: "keep"}, false, []string{"import", "--on-conflict", "keep"}},
		{"project root", importForm{Strategy: "merge", ProjectRoot: "/src/my app"}, false,
			[]string{"import", "--apply-projects", "--project-root", "/src/my app"}},
		{"blank optional fields omitted", importForm{Strategy: "merge", Path: " ", ProjectRoot: "  "}, false, []string{"import"}},
		{"maps repeated in order", importForm{Strategy: "merge", Maps: []string{"/b=/y", "/a=/x"}}, false,
			[]string{"import", "--map", "/b=/y", "--map", "/a=/x"}},
		{"path with spaces", importForm{Strategy: "merge", Path: "/tmp/a b.avbundle"}, true,
			[]string{"import", "--dry-run", "--", "/tmp/a b.avbundle"}},
	}
	for _, tc := range tests {
		if got := importArgs(tc.form, tc.dryRun); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if !(importForm{Strategy: "mirror"}).mirror() || (importForm{Strategy: "replace"}).mirror() {
		t.Error("mirror() wrong")
	}
}

func TestDisplayCommandQuotes(t *testing.T) {
	got := displayCommand([]string{"import", "--", "/tmp/a b", "it's", ""})
	want := `agentvault import -- '/tmp/a b' 'it'\''s' ''`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func openImportForm(t *testing.T) model {
	t.Helper()
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('i'))))
	if m.mode != viewImport {
		t.Fatalf("mode = %v, want import form", m.mode)
	}
	return m
}

func focusImport(t *testing.T, m model, field int) model {
	t.Helper()
	for i := 0; i < impFieldCount && m.impFocus != field; i++ {
		m = asModel(t, must(m.Update(keyTab)))
	}
	if m.impFocus != field {
		t.Fatalf("focus = %d, want %d", m.impFocus, field)
	}
	return m
}

func TestImportFormMapEntries(t *testing.T) {
	m := focusImport(t, openImportForm(t), impFieldMap)
	for _, bad := range []string{"noequals", "=/new", "/old=", " = "} {
		m.impMap.SetValue(bad)
		m = asModel(t, must(m.Update(keyEnter)))
		if len(m.imp.Maps) != 0 || !m.statusIsError || m.mode != viewImport {
			t.Fatalf("%q: maps=%q status=%q mode=%v, want refused", bad, m.imp.Maps, m.statusMsg, m.mode)
		}
	}
	m.impMap.SetValue("")
	m = typeText(t, m, "/old home=/new home")
	m = asModel(t, must(m.Update(keyEnter)))
	m = typeText(t, m, "/a=/b")
	m = asModel(t, must(m.Update(keyEnter)))
	if want := []string{"/old home=/new home", "/a=/b"}; !reflect.DeepEqual(m.imp.Maps, want) {
		t.Fatalf("maps = %q, want %q", m.imp.Maps, want)
	}
	if !strings.Contains(m.View(), "'/old home=/new home'") {
		t.Fatal("command line with quoted map not shown")
	}
	m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})))
	if want := []string{"/old home=/new home"}; !reflect.DeepEqual(m.imp.Maps, want) {
		t.Fatalf("after remove maps = %q, want %q", m.imp.Maps, want)
	}
	// A typed but not added entry blocks the preview instead of being dropped.
	m.impMap.SetValue("bad")
	m = focusImport(t, m, impFieldPath)
	if m = asModel(t, must(m.Update(keyEnter))); m.mode != viewImport || !m.statusIsError {
		t.Fatalf("mode=%v status=%q, want preview refused", m.mode, m.statusMsg)
	}
}

func TestImportFormRejectsBadStoredMap(t *testing.T) {
	m := openImportForm(t)
	m.imp.Maps = []string{"broken"}
	tm, cmd := m.Update(keyEnter)
	m = asModel(t, tm)
	if cmd != nil || m.mode != viewImport || !m.statusIsError {
		t.Fatalf("cmd=%v mode=%v status=%q, want refused", cmd != nil, m.mode, m.statusMsg)
	}
}

// runPreview presses enter on the form and feeds the preview command's result
// back into the model.
func runPreview(t *testing.T, m model) model {
	t.Helper()
	tm, cmd := m.Update(keyEnter)
	m = asModel(t, tm)
	if m.mode != viewImportPreview || cmd == nil {
		t.Fatalf("mode=%v cmd=%v status=%q, want preview running", m.mode, cmd != nil, m.statusMsg)
	}
	msg := cmd()
	if _, ok := msg.(importPreviewMsg); !ok {
		t.Fatalf("preview returned %T, want importPreviewMsg", msg)
	}
	return asModel(t, must(m.Update(msg)))
}

func TestImportPreviewRunsDryRun(t *testing.T) {
	useFakeCLI(t, "ok")
	m := openImportForm(t)
	m.impPath.SetValue("/tmp/my bundle.avbundle")
	m.imp.Maps = []string{"/a=/b"}
	m = runPreview(t, m)
	if m.impErr != "" {
		t.Fatalf("preview error %q, output %q", m.impErr, m.impOutput)
	}
	for _, want := range []string{"arg:import", "arg:--map", "arg:/a=/b", "arg:--dry-run", "arg:/tmp/my bundle.avbundle"} {
		if !strings.Contains(m.impOutput, want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, m.impOutput)
		}
	}
	view := m.View()
	if !strings.Contains(view, "arg:--dry-run") {
		t.Fatalf("preview output not rendered:\n%s", view)
	}
	if !strings.Contains(view, "agentvault import --map /a=/b -- '/tmp/my bundle.avbundle'") {
		t.Fatalf("real command line not shown:\n%s", view)
	}
	// e goes back to the form with its state kept.
	back := asModel(t, must(m.Update(runeKey('e'))))
	if back.mode != viewImport || back.impPath.Value() != "/tmp/my bundle.avbundle" {
		t.Fatalf("e: mode=%v path=%q", back.mode, back.impPath.Value())
	}
	// y runs the real import on the terminal and closes the flow.
	tm, cmd := m.Update(runeKey('y'))
	if got := asModel(t, tm); cmd == nil || got.mode == viewImportPreview {
		t.Fatalf("y: cmd=%v mode=%v, want import started", cmd != nil, got.mode)
	}
}

func TestImportPreviewShowsCLIError(t *testing.T) {
	useFakeCLI(t, "fail")
	m := runPreview(t, openImportForm(t))
	if m.impErr == "" {
		t.Fatal("preview error not recorded")
	}
	view := m.View()
	if !strings.Contains(view, "Error: fake failure") || !strings.Contains(view, "Preview failed") {
		t.Fatalf("CLI error not shown:\n%s", view)
	}
	// A failed preview does not lead to the real import.
	for _, k := range []tea.KeyMsg{runeKey('y'), keyEnter, runeKey('M')} {
		tm, cmd := m.Update(k)
		if got := asModel(t, tm); cmd != nil || got.mode != viewImportPreview {
			t.Fatalf("%q after failed preview: cmd=%v mode=%v", k.String(), cmd != nil, got.mode)
		}
	}
}

func TestImportPreviewScrubsControlChars(t *testing.T) {
	m := openImportForm(t)
	m.mode = viewImportPreview
	m.handleImportPreviewMsg(importPreviewMsg{seq: m.impSeq, output: "ok\x1b]0;evil\x07 \x9b2J"})
	view := m.View()
	if strings.Contains(view, "\x07") || strings.Contains(view, "]0;evil\x07") || strings.Contains(view, "\x9b") {
		t.Fatalf("control characters reached the view: %q", view)
	}
}

func TestImportMirrorNeedsExplicitConfirm(t *testing.T) {
	useFakeCLI(t, "ok")
	m := focusImport(t, openImportForm(t), impFieldStrategy)
	m = asModel(t, must(m.Update(runeKey('l'))))
	m = asModel(t, must(m.Update(runeKey('l'))))
	if got := m.importFormState().Strategy; got != "mirror" {
		t.Fatalf("strategy = %q, want mirror", got)
	}
	if !strings.Contains(m.View(), "mirror deletes local items") {
		t.Fatal("mirror warning not shown on the form")
	}
	m = runPreview(t, m)
	for _, k := range []tea.KeyMsg{runeKey('y'), keyEnter} {
		tm, cmd := m.Update(k)
		if got := asModel(t, tm); cmd != nil || got.mode != viewImportPreview {
			t.Fatalf("%q ran a mirror import without M", k.String())
		}
	}
	tm, cmd := m.Update(runeKey('M'))
	if cmd == nil || asModel(t, tm).mode == viewImportPreview {
		t.Fatal("M did not run the mirror import")
	}
}

func TestImportExportEscFromEveryStage(t *testing.T) {
	m := openImportForm(t)
	if got := asModel(t, must(m.Update(keyEsc))); got.mode != viewAgentList {
		t.Fatalf("esc on import form: mode %v", got.mode)
	}
	m.mode = viewImportPreview
	m.impRunning = true
	got := asModel(t, must(m.Update(keyEsc)))
	if got.mode != viewImport {
		t.Fatalf("esc on preview: mode %v, want form", got.mode)
	}
	got.handleImportPreviewMsg(importPreviewMsg{seq: m.impSeq, output: "stale"})
	if got.impOutput != "" {
		t.Fatal("result of a preview the user left was shown")
	}
	if got = asModel(t, must(got.Update(keyEsc))); got.mode != viewAgentList {
		t.Fatalf("esc after preview: mode %v", got.mode)
	}
	e := asModel(t, must(actionsTestModel(t).Update(keySpace)))
	e = asModel(t, must(e.Update(runeKey('x'))))
	if e.mode != viewExport {
		t.Fatalf("x: mode %v, want export", e.mode)
	}
	if got := asModel(t, must(e.Update(keyEsc))); got.mode != viewAgentList {
		t.Fatalf("esc on export: mode %v", got.mode)
	}
}

func TestExportFormTogglesAndWarns(t *testing.T) {
	m := asModel(t, must(actionsTestModel(t).Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('x'))))
	if !strings.Contains(m.View(), "API keys go into the file") {
		t.Fatal("keys warning not shown by default")
	}
	m = typeText(t, m, "/tmp/out dir/b.avbundle")
	m = asModel(t, must(m.Update(keyTab))) // keys
	m = asModel(t, must(m.Update(keyTab))) // encrypt
	m = asModel(t, must(m.Update(keySpace)))
	if !strings.Contains(m.View(), "plain text") {
		t.Fatal("plain text warning not shown")
	}
	want := []string{"export", "-y", "--plain", "--confirm", "--", "/tmp/out dir/b.avbundle"}
	if got := exportArgs(m.exportFormState()); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
	if !strings.Contains(m.View(), "agentvault export -y --plain --confirm -- '/tmp/out dir/b.avbundle'") {
		t.Fatal("command line not shown")
	}
}

// The forms run without a shell, so a leading ~ is expanded here.
func TestFormPathsExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := openImportForm(t)
	m.impPath.SetValue("~/b.avbundle")
	m.impRoot.SetValue("~")
	f := m.importFormState()
	if f.Path != filepath.Join(home, "b.avbundle") || f.ProjectRoot != home {
		t.Fatalf("path=%q root=%q, want under %s", f.Path, f.ProjectRoot, home)
	}
	m.expPath.SetValue("~/out.avbundle")
	if got := m.exportFormState().Path; got != filepath.Join(home, "out.avbundle") {
		t.Fatalf("export path=%q", got)
	}
	// NEW is a path on this machine; OLD is the exporting machine's.
	if got, err := validateMapEntry("~/old=~/new"); err != nil || got != "~/old="+filepath.Join(home, "new") {
		t.Fatalf("map entry=%q err=%v", got, err)
	}
	if got := expandTilde("~user/x"); got != "~user/x" {
		t.Fatalf("~user expanded: %q", got)
	}
}
