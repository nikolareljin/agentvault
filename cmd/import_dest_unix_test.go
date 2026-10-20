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

// runWithin fails the test if f does not return in time: a read of a pipe
// with no writer blocks forever.
func runWithin(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a pipe")
	}
}

// A pipe where a redacted provider file goes is not read to fill the
// blanked values, and not replaced.
func TestApplyProviderAssetPipeDestinationNotRead(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".copilot", "config.json")
	mustMkdirAll(t, filepath.Dir(dest))
	if err := syscall.Mkfifo(dest, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var warnings []string
	runWithin(t, func() {
		_, warnings, _ = applyProviderAssetsToSystem(home, []SetupAsset{{LogicalRoot: setupAssetRootProviderCopilotCLI,
			LogicalPath: "config.json", ContentPresent: true, Content: []byte(`{"model":"b","proxy":{"apiKey":""}}`),
			RedactedFields: []string{"/proxy/apiKey"}}})
	})
	if info, err := os.Lstat(dest); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("pipe replaced: %v %v", info, err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "not a regular file") {
		t.Errorf("warnings = %v", warnings)
	}
}

// --apply-projects with --strategy merge compares an existing file before
// writing; a pipe there is reported, not read.
func TestApplyProjectsPipeDestinationNotRead(t *testing.T) {
	cfg := t.TempDir()
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(cfg, setupImportedAssetsDirName, "project", "AGENTS.md"), "new\n")
	if err := syscall.Mkfifo(filepath.Join(project, "AGENTS.md"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var skipped []string
	var err error
	runWithin(t, func() { _, skipped, err = applyStagedProjectAssetsWith(cfg, project, false) })
	if err != nil || len(skipped) != 1 || !strings.Contains(skipped[0], "not a regular file") {
		t.Errorf("skipped = %v, err = %v", skipped, err)
	}
}
