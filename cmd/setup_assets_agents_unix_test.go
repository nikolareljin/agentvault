//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A pipe or an unreadable file in a directory a note names is left behind;
// the export goes on and does not block on the pipe.
func TestReferencedDirPipesAndUnreadableFilesDoNotAbort(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs mkfifo and file permissions")
	}
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "See `notes/` and `secret.md`.\n")
	mustWriteFile(t, filepath.Join(project, "notes", "ok.md"), "fine\n")
	mustWriteFile(t, filepath.Join(project, "notes", "locked.md"), "x\n")
	mustWriteFile(t, filepath.Join(project, "secret.md"), "x\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "See `~/kb/`.\n")
	mustWriteFile(t, filepath.Join(home, "kb", "ok.md"), "fine\n")
	for _, p := range []string{filepath.Join(project, "notes", "fifo.md"), filepath.Join(home, "kb", "fifo.md")} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
	}
	for _, p := range []string{filepath.Join(project, "notes", "locked.md"), filepath.Join(project, "secret.md")} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project})
	if err != nil {
		t.Fatalf("export aborted: %v", err)
	}
	if a := findAsset(assets.ProjectFiles, setupAssetRootProject, "notes/ok.md"); a == nil || !a.ContentPresent {
		t.Errorf("notes/ok.md not carried: %+v", a)
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, "kb/ok.md"); a == nil || !a.ContentPresent {
		t.Errorf("kb/ok.md not carried: %+v", a)
	}
	if !declinedHas(assets.Declined, "secret.md", DeclinedByPolicy) || !declinedHas(assets.Declined, "notes/locked.md", DeclinedByPolicy) {
		t.Errorf("unreadable file not reported: %v", assets.Declined)
	}
}

// A pipe inside a carried tool directory is skipped with a warning; the
// export goes on. backup~1.md and aux.md are carried, aux.md with a warning
// that Windows cannot write it.
func TestToolDirOddEntriesDoNotAbortExport(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	agents := filepath.Join(home, ".claude", "agents")
	mustWriteFile(t, filepath.Join(agents, "ok.md"), "fine\n")
	mustWriteFile(t, filepath.Join(agents, "backup~1.md"), "old\n")
	mustWriteFile(t, filepath.Join(agents, "aux.md"), "x\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "projects", "p~1", "memory", "m.md"), "m\n")
	if err := syscall.Mkfifo(filepath.Join(agents, "p.md"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	assets, warnings, err := collectSetupAssets(setupAssetOptions{IncludeMemory: true})
	if err != nil {
		t.Fatalf("export aborted: %v", err)
	}
	for _, name := range []string{"agents/ok.md", "agents/backup~1.md", "agents/aux.md"} {
		if a := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, name); a == nil || !a.ContentPresent {
			t.Errorf("%s not carried: %+v", name, a)
		}
	}
	warned := false
	for _, w := range warnings {
		if strings.Contains(w, "agents/aux.md") && strings.Contains(w, "Windows device name") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no Windows warning for aux.md in %q", warnings)
	}
}

// A pipe swapped in after the checks does not block the export.
func TestPipeSwappedInAfterCheckDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.md")
	mustWriteFile(t, p, "plain\n")
	afterAssetStat = func(string) {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterAssetStat = nil })
	done := make(chan SetupAsset, 1)
	go func() {
		a, _, _ := loadSetupAsset(p, setupAssetKindProviderFile, setupAssetOriginProviderHome,
			setupAssetRootProviderHome, "notes.md", "", false, true, false)
		done <- a
	}()
	select {
	case a := <-done:
		if a.ContentPresent {
			t.Errorf("pipe read as content: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a pipe swapped in after the checks")
	}
}
