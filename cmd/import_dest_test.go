package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// aux.md and NOTES~1.MD are ordinary names off Windows, so --apply-projects
// writes them; Windows import refuses device names and short-name aliases.
func TestApplyProjectsWritesWindowsOnlyNamesElsewhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot create these names on Windows")
	}
	cfg := t.TempDir()
	project := t.TempDir()
	stage := filepath.Join(cfg, setupImportedAssetsDirName, "project")
	for _, n := range []string{"aux.md", "NOTES~1.MD", "ok.md"} {
		mustWriteFile(t, filepath.Join(stage, n), "x\n")
	}
	applied, skipped, err := applyStagedProjectAssetsWith(cfg, project, true)
	if err != nil || applied != 3 || len(skipped) != 0 {
		t.Errorf("applied %d, skipped %v, err %v", applied, skipped, err)
	}
	for _, n := range []string{"aux.md", "NOTES~1.MD"} {
		if _, err := os.Lstat(filepath.Join(project, n)); err != nil {
			t.Errorf("%s not written: %v", n, err)
		}
	}
}

func TestApplyProjectsMergeKeepsUnreadableDestination(t *testing.T) {
	cfg := t.TempDir()
	project := t.TempDir()
	stage := filepath.Join(cfg, setupImportedAssetsDirName, "project")
	mustWriteFile(t, filepath.Join(stage, "big.md"), "new\n")
	big := strings.Repeat("x", maxSetupAssetBytes+1)
	mustWriteFile(t, filepath.Join(project, "big.md"), big)
	applied, skipped, err := applyStagedProjectAssetsWith(cfg, project, false)
	if err != nil || applied != 0 || len(skipped) != 1 {
		t.Errorf("applied %d, skipped %v, err %v", applied, skipped, err)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "big.md")); string(got) != big {
		t.Error("merge overwrote a destination it could not read")
	}
}
