package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// A vault written before filenames were validated can still hold an escaping
// entry. Push must refuse it at the write site, not trust the vault.
func TestWriteInstructionFileRefusesEscape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escaped.txt", "a/../../escaped.txt", "/tmp/escaped.txt"} {
		err := writeInstructionFile(dir, agent.InstructionFile{Name: "x", Filename: name, Content: "pwned"})
		if err == nil {
			t.Errorf("%q: written, want error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("file escaped the target dir: %v", err)
	}
}

func TestWriteInstructionFileRefusesSymlinkBelowRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{dir, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "docs")); err != nil {
		t.Fatal(err)
	}
	err := writeInstructionFile(dir, agent.InstructionFile{Name: "x", Filename: "docs/a.md", Content: "x"})
	if err == nil {
		t.Fatal("wrote through a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "a.md")); !os.IsNotExist(err) {
		t.Fatal("file landed outside via symlink")
	}
}

// The target dir itself may sit under a symlink (macOS /var -> /private/var,
// a symlinked ~/Projects). That must keep working.
func TestWriteInstructionFileAllowsSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := writeInstructionFile(link, agent.InstructionFile{Name: "x", Filename: "docs/a.md", Content: "ok"}); err != nil {
		t.Fatalf("symlinked root refused: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(real, "docs", "a.md")); err != nil || string(b) != "ok" {
		t.Fatalf("got %q, %v", b, err)
	}
}
