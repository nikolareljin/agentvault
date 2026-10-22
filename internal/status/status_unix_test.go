//go:build unix

package status

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A pipe named like a session log is not picked: reading it would block
// status until something writes to it.
func TestFindNewestJSONLSkipsPipes(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "a.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "b.jsonl"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	got, err := findNewestJSONL(root)
	if err != nil || got != old {
		t.Errorf("findNewestJSONL = %q, %v; want %q", got, err, old)
	}
}
