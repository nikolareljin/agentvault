//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// An env file that already exists with a looser mode is narrowed to 0600:
// it now holds keys.
func TestWriteEnvFileNarrowsExistingMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env.agents")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvFile("", p, "K=v\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}
