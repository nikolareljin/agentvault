package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// scopeTestVault makes an empty vault in a throwaway HOME and returns its
// config dir.
func scopeTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	cfgDir := filepath.Join(root, config.AppName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Init(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, cfgDir)
	return cfgDir
}

// twoProjects is work/svc, whose AGENTS.md names notes/x.md, and an empty
// work/mono.
func twoProjects(t *testing.T) (svc, mono string) {
	t.Helper()
	work := t.TempDir()
	svc = filepath.Join(work, "svc")
	mono = filepath.Join(work, "mono")
	for _, d := range []string{filepath.Join(svc, "notes"), mono} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(svc, "AGENTS.md"), "Read `notes/x.md` first.\n")
	writeFile(t, filepath.Join(svc, "notes", "x.md"), "svc only\n")
	return svc, mono
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// Issue 80: a file pulled from one project's references is not written
// into another project by push. The instruction itself still is.
func TestPushDoesNotWriteAnotherProjectsReferencedFiles(t *testing.T) {
	scopeTestVault(t)
	svc, mono := twoProjects(t)
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out, err := captureStdoutForTest(t, func() error { return instPushCmd.RunE(instPushCmd, []string{mono}) })
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if exists(filepath.Join(mono, "notes", "x.md")) {
		t.Fatal("push wrote svc's notes/x.md into mono")
	}
	if !exists(filepath.Join(mono, "AGENTS.md")) {
		t.Error("push did not write AGENTS.md")
	}
	if !strings.Contains(out, "notes/x.md") || !strings.Contains(out, svc) {
		t.Errorf("push did not name the skipped file and its project:\n%s", out)
	}
}

func TestPushFromAnyProjectWritesThem(t *testing.T) {
	scopeTestVault(t)
	svc, mono := twoProjects(t)
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	withFlags(t, instPushCmd, map[string]string{"from-any-project": "true"})
	if err := instPushCmd.RunE(instPushCmd, []string{mono}); err != nil {
		t.Fatalf("push: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mono, "notes", "x.md"))
	if err != nil || string(got) != "svc only\n" {
		t.Errorf("--from-any-project did not write notes/x.md: %q %v", got, err)
	}
}

func TestPushRestoresReferencedFilesInTheSameProject(t *testing.T) {
	scopeTestVault(t)
	svc, _ := twoProjects(t)
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := os.Remove(filepath.Join(svc, "notes", "x.md")); err != nil {
		t.Fatal(err)
	}
	if err := instPushCmd.RunE(instPushCmd, []string{svc}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(svc, "notes", "x.md")); err != nil || string(got) != "svc only\n" {
		t.Errorf("push to the source project did not restore notes/x.md: %q %v", got, err)
	}
}

// A referenced file stored before source projects were recorded has no
// project. It is skipped and named, with a way forward, never guessed.
func TestPushSkipsLegacyReferencedFiles(t *testing.T) {
	cfgDir := scopeTestVault(t)
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	for _, inst := range []agent.InstructionFile{
		{Name: "agents", Filename: "AGENTS.md", Content: "Read `notes/x.md`.\n", UpdatedAt: time.Now()},
		{Name: "notes/x.md", Filename: "notes/x.md", Content: "old\n", UpdatedAt: time.Now()},
	} {
		if err := v.SetInstruction(inst); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	out, err := captureStdoutForTest(t, func() error { return instPushCmd.RunE(instPushCmd, []string{dst}) })
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if exists(filepath.Join(dst, "notes", "x.md")) {
		t.Error("push wrote a referenced file with no source project")
	}
	if !exists(filepath.Join(dst, "AGENTS.md")) {
		t.Error("push did not write AGENTS.md")
	}
	if !strings.Contains(out, "notes/x.md") || !strings.Contains(out, "unknown") || !strings.Contains(out, "--from-any-project") {
		t.Errorf("legacy entry not reported with a hint:\n%s", out)
	}
}

// setup apply writes stored instructions too, so it follows the same rule.
func TestSetupApplyDoesNotWriteAnotherProjectsReferencedFiles(t *testing.T) {
	scopeTestVault(t)
	svc, mono := twoProjects(t)
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out, err := captureStdoutForTest(t, func() error { return setupApplyCmd.RunE(setupApplyCmd, []string{mono}) })
	if err != nil {
		t.Fatalf("setup apply: %v", err)
	}
	if exists(filepath.Join(mono, "notes", "x.md")) {
		t.Fatal("setup apply wrote svc's notes/x.md into mono")
	}
	if !strings.Contains(out, "notes/x.md") {
		t.Errorf("setup apply did not name the skipped file:\n%s", out)
	}
}

// Pulling a second project whose own notes/x.md has the same name takes
// that project's copy, not the stored one from the first project.
func TestPullTakesThisProjectsCopyOfASharedName(t *testing.T) {
	scopeTestVault(t)
	svc, mono := twoProjects(t)
	if err := os.MkdirAll(filepath.Join(mono, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mono, "AGENTS.md"), "Read `notes/x.md` first.\n")
	writeFile(t, filepath.Join(mono, "notes", "x.md"), "mono only\n")
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull svc: %v", err)
	}
	if err := instPullCmd.RunE(instPullCmd, []string{mono}); err != nil {
		t.Fatalf("pull mono: %v", err)
	}
	if err := os.Remove(filepath.Join(mono, "notes", "x.md")); err != nil {
		t.Fatal(err)
	}
	if err := instPushCmd.RunE(instPushCmd, []string{mono}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(mono, "notes", "x.md")); err != nil || string(got) != "mono only\n" {
		t.Errorf("mono's notes/x.md after pull and push: %q %v", got, err)
	}
}

// A project with no git identity is keyed by its path. Import rewrites that
// path with the same rules as every other path in the bundle, so the files
// still go back to the project on the new machine.
func TestRemapInstructionProjectsRewritesPathKeys(t *testing.T) {
	bundle := []agent.InstructionFile{
		{Name: "notes/x.md", Filename: "notes/x.md", SourceProject: "path:/home/old/work/svc"},
		{Name: "docs/y.md", Filename: "docs/y.md", SourceProject: "remote:github.com/o/r"},
	}
	local := agent.InstructionFile{Name: "z.md", Filename: "z.md", SourceProject: "path:/home/old/work/svc/sub"}
	newRoot := t.TempDir()
	remaps, err := buildPathRemaps("", "", "/home/old/work/svc", newRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, changed := remapInstructionProjects(append(append([]agent.InstructionFile(nil), bundle...), local), bundle, remaps)
	if !changed {
		t.Fatal("nothing rewritten")
	}
	if want := instructionProjectKey(newRoot, nil); got[0].SourceProject != want {
		t.Errorf("path key after import = %q, want %q", got[0].SourceProject, want)
	}
	if got[1].SourceProject != "remote:github.com/o/r" {
		t.Errorf("remote key changed to %q", got[1].SourceProject)
	}
	if got[2].SourceProject != local.SourceProject {
		t.Errorf("a key the bundle did not bring was rewritten to %q", got[2].SourceProject)
	}
}

// The source project rides in the bundle as an optional field: a new bundle
// carries it, and a bundle written before it existed still reads, with the
// field empty (unknown).
func TestBundleCarriesSourceProject(t *testing.T) {
	b := PortableBundle{SchemaVersion: PortableBundleSchemaVersion, Profiles: []PortableProfile{{Name: "default"}}}
	b.Profiles[0].Setup.SharedConfig.Instructions = []agent.InstructionFile{
		{Name: "notes/x.md", Filename: "notes/x.md", Content: "x\n", SourceProject: "remote:github.com/o/svc"},
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := decodePortableBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Profiles[0].Setup.SharedConfig.Instructions[0].SourceProject; p != "remote:github.com/o/svc" {
		t.Errorf("source project after a round trip = %q", p)
	}

	old := strings.Replace(string(raw), `,"source_project":"remote:github.com/o/svc"`, "", 1)
	if old == string(raw) {
		t.Fatal("test did not remove the field")
	}
	got, _, err = decodePortableBundle([]byte(old))
	if err != nil {
		t.Fatalf("a bundle without source_project: %v", err)
	}
	if p := got.Profiles[0].Setup.SharedConfig.Instructions[0].SourceProject; p != "" {
		t.Errorf("source project of an old bundle = %q, want empty", p)
	}
}

// diff compares what push would write: another project's referenced file
// is not a difference in this one.
func TestDiffLeavesOutAnotherProjectsReferencedFiles(t *testing.T) {
	scopeTestVault(t)
	svc, mono := twoProjects(t)
	if err := instPullCmd.RunE(instPullCmd, []string{svc}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	writeFile(t, filepath.Join(mono, "AGENTS.md"), "Read `notes/x.md` first.\n")
	out, err := captureStdoutForTest(t, func() error { return instDiffCmd.RunE(instDiffCmd, []string{mono}) })
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.Contains(out, "not on disk") || !strings.Contains(out, "in sync") {
		t.Errorf("diff counted another project's file:\n%s", out)
	}
}
