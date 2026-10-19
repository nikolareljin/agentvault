package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestReadReferencedFileRefusesSymlinkOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The state after a swap: classification saw a regular file, the read
	// finds a symlink pointing out.
	if err := os.Symlink(secret, filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := readReferencedFile(dir, "notes.md"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("err = %v, want refusal", err)
	}
}

func TestReadReferencedFileRefusesOversize(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, agent.MaxReferenceBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readReferencedFile(dir, "big.md"); err == nil {
		t.Fatal("oversize file read")
	}
}

func TestReadReferencedFileReadsRegular(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "a.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readReferencedFile(dir, "docs/a.md")
	if err != nil || string(got) != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
}
