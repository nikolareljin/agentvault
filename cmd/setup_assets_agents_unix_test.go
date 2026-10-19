//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
