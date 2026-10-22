package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// sharedNameProjects is twoProjects with mono also naming notes/x.md, each
// with its own content, both pulled.
func sharedNameProjects(t *testing.T) (svc, mono string) {
	t.Helper()
	svc, mono = twoProjects(t)
	if err := os.MkdirAll(filepath.Join(mono, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mono, "AGENTS.md"), "Read `notes/x.md` first.\n")
	writeFile(t, filepath.Join(mono, "notes", "x.md"), "mono only\n")
	for _, d := range []string{svc, mono} {
		if _, err := captureStdoutForTest(t, func() error { return instPullCmd.RunE(instPullCmd, []string{d}) }); err != nil {
			t.Fatalf("pull %s: %v", d, err)
		}
	}
	return svc, mono
}

// Issue 87: each project keeps its own copy of a shared referenced name, so
// the first project's push still restores its file after the second pulls.
func TestEachProjectKeepsItsCopyOfASharedName(t *testing.T) {
	scopeTestVault(t)
	svc, mono := sharedNameProjects(t)
	for d, want := range map[string]string{svc: "svc only\n", mono: "mono only\n"} {
		if err := os.Remove(filepath.Join(d, "notes", "x.md")); err != nil {
			t.Fatal(err)
		}
		out, err := captureStdoutForTest(t, func() error { return instPushCmd.RunE(instPushCmd, []string{d}) })
		if err != nil {
			t.Fatalf("push %s: %v", d, err)
		}
		if got, err := os.ReadFile(filepath.Join(d, "notes", "x.md")); err != nil || string(got) != want {
			t.Errorf("%s notes/x.md after push = %q %v, want %q:\n%s", d, got, err, want, out)
		}
		if strings.Contains(out, "Skipped") {
			t.Errorf("push to %s skipped another project's copy of its own file:\n%s", d, out)
		}
	}
}

// show picks the copy of the project it runs in, and refuses to guess
// outside both.
func TestShowPicksTheCurrentProjectsCopy(t *testing.T) {
	scopeTestVault(t)
	_, mono := sharedNameProjects(t)
	t.Chdir(mono)
	out, err := captureStdoutForTest(t, func() error { return instShowCmd.RunE(instShowCmd, []string{"notes/x.md"}) })
	if err != nil || out != "mono only\n" {
		t.Errorf("show in mono = %q %v", out, err)
	}
	t.Chdir(t.TempDir())
	if _, err := captureStdoutForTest(t, func() error { return instShowCmd.RunE(instShowCmd, []string{"notes/x.md"}) }); err == nil || !strings.Contains(err.Error(), "2 projects") {
		t.Errorf("show outside both projects: %v", err)
	}
}

// A copy stored before projects were recorded is replaced by the re-pull,
// not kept beside it as a second, unknown copy.
func TestRepullReplacesLegacyCopy(t *testing.T) {
	cfgDir := scopeTestVault(t)
	svc, _ := twoProjects(t)
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	for _, inst := range []agent.InstructionFile{
		{Name: "agents", Filename: "AGENTS.md", Content: "Read `notes/x.md` first.\n"},
		{Name: "notes/x.md", Filename: "notes/x.md", Content: "old\n"},
	} {
		if err := v.SetInstruction(inst); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureStdoutForTest(t, func() error { return instPullCmd.RunE(instPullCmd, []string{svc}) }); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	var copies []string
	for _, inst := range v.ListInstructions() {
		if inst.Name == "notes/x.md" {
			copies = append(copies, inst.SourceProject)
		}
	}
	if len(copies) != 1 || copies[0] != instructionProjectKey(svc, nil) {
		t.Errorf("notes/x.md copies after re-pull: %q", copies)
	}
}

// --from-any-project in a third project writes one notes/x.md, not two
// colliding ones.
func TestFromAnyProjectWritesOneCopyPerFilename(t *testing.T) {
	scopeTestVault(t)
	sharedNameProjects(t)
	third := t.TempDir()
	if err := instPushCmd.Flags().Set("from-any-project", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instPushCmd.Flags().Set("from-any-project", "false") })
	out, err := captureStdoutForTest(t, func() error { return instPushCmd.RunE(instPushCmd, []string{third}) })
	if err != nil {
		t.Fatalf("push --from-any-project: %v\n%s", err, out)
	}
	if !exists(filepath.Join(third, "notes", "x.md")) {
		t.Errorf("notes/x.md not written:\n%s", out)
	}
}

// A path key rewritten on import that lands on a copy this vault has for
// the same project leaves one entry: this vault's under merge.
func TestRemapInstructionProjectsDedupes(t *testing.T) {
	newRoot := t.TempDir()
	key := instructionProjectKey(newRoot, nil)
	bundle := []agent.InstructionFile{{Name: "notes/x.md", Filename: "notes/x.md", Content: "bundle\n", SourceProject: "path:/home/old/work/svc"}}
	local := agent.InstructionFile{Name: "notes/x.md", Filename: "notes/x.md", Content: "local\n", SourceProject: key}
	remaps, err := buildPathRemaps("", "", "/home/old/work/svc", newRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundleWins := range []bool{false, true} {
		got, _ := remapInstructionProjects([]agent.InstructionFile{local, bundle[0]}, bundle, remaps, bundleWins)
		want := "local\n"
		if bundleWins {
			want = "bundle\n"
		}
		if len(got) != 1 || got[0].Content != want {
			t.Errorf("bundleWins=%v: %d entries, %+v", bundleWins, len(got), got)
		}
	}
}
