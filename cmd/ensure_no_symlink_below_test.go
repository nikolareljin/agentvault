package cmd

import (
	"path/filepath"
	"testing"
)

// A path outside root has no components "below" it to check; it must be
// refused rather than passed.
func TestEnsureNoSymlinkBelowRefusesPathOutsideRoot(t *testing.T) {
	r := t.TempDir()
	if err := ensureNoSymlinkBelow(filepath.Join(r, "root"), filepath.Join(r, "other", "x")); err == nil {
		t.Fatal("path outside root accepted")
	}
	if err := ensureNoSymlinkBelow(filepath.Join(r, "root"), filepath.Join(r, "root")); err != nil {
		t.Fatalf("root itself refused: %v", err)
	}
}
