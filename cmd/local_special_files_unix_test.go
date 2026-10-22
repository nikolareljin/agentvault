//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// specialProject is a project whose AGENTS.md is a plain file, CLAUDE.md a
// symlink to it, and .cursorrules and GEMINI.md pipes nobody writes to.
func specialProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "rules\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, ".cursorrules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "GEMINI.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// instructions pull reports a pipe and pulls the rest; before, it blocked
// on the pipe.
func TestInstructionsPullSkipsPipes(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	cfgDir := filepath.Join(root, config.AppName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.New(filepath.Join(cfgDir, config.VaultFile)).Init(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, cfgDir)
	dir := specialProject(t)
	done := make(chan error, 1)
	go func() { done <- instPullCmd.RunE(instPullCmd, []string{dir}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("instructions pull did not finish")
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, inst := range v.ListInstructions() {
		got[inst.Filename] = inst.Content
	}
	if got["AGENTS.md"] != "rules\n" || got["CLAUDE.md"] != "rules\n" {
		t.Errorf("stored = %v", got)
	}
	if _, ok := got[".cursorrules"]; ok {
		t.Errorf("pipe stored: %v", got)
	}
	if _, ok := got["GEMINI.md"]; ok {
		t.Errorf("pipe stored: %v", got)
	}
}

// export --project skips an instruction file that is a symlink or a pipe
// with a warning; before, one such file ended the whole export.
func TestProjectExportSkipsSymlinksAndPipes(t *testing.T) {
	dir := specialProject(t)
	files, _, warnings, _, err := collectProjectAssets(dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.LogicalPath)
	}
	if strings.Join(paths, ",") != "AGENTS.md" {
		t.Errorf("files = %v", paths)
	}
	all := strings.Join(warnings, "\n")
	for _, name := range []string{"CLAUDE.md", ".cursorrules", "GEMINI.md"} {
		if !strings.Contains(all, name) {
			t.Errorf("no warning for %s in %q", name, all)
		}
	}
}
