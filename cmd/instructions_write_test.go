package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
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

// diff reads the same names push writes; an escaping one must not be read.
func TestReadInstructionOnDiskRefusesEscape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readInstructionOnDisk(dir, agent.InstructionFile{Name: "x", Filename: "../secret.txt"})
	if !errors.Is(err, errUnsafeInstructionFilename) {
		t.Fatalf("err = %v, want errUnsafeInstructionFilename", err)
	}
	// An unplaced entry is "not on disk", not refused.
	if _, err := readInstructionOnDisk(dir, agent.InstructionFile{Name: "x"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty filename: err = %v, want os.ErrNotExist", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := readInstructionOnDisk(dir, agent.InstructionFile{Name: "x", Filename: "AGENTS.md"}); err != nil || string(b) != "ok" {
		t.Fatalf("got %q, %v", b, err)
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

// The push error tells the user to move a refused name with
// "instructions set <name> --filename <new>"; that must work without content.
func TestInstructionsSetFilenameAloneMovesStoredInstruction(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	cfg := filepath.Join(root, "agentvault")
	useConfigDir(t, cfg)
	v := vault.New(filepath.Join(cfg, "vault.enc"))
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.Init(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: "notes", Filename: "docs/notes.md", Content: "keep me\n"}); err != nil {
		t.Fatal(err)
	}
	withFlags(t, instSetCmd, map[string]string{"filename": "guide/notes.md"})
	if err := instSetCmd.RunE(instSetCmd, []string{"notes"}); err != nil {
		t.Fatalf("set --filename: %v", err)
	}
	v2 := vault.New(filepath.Join(cfg, "vault.enc"))
	if err := v2.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	got := v2.ListInstructions()
	if len(got) != 1 || got[0].Filename != "guide/notes.md" || got[0].Content != "keep me\n" {
		t.Fatalf("instructions = %+v, want notes moved to guide/notes.md with its content", got)
	}
	withFlags(t, instSetCmd, map[string]string{"filename": "x.md"})
	if err := instSetCmd.RunE(instSetCmd, []string{"missing"}); err == nil {
		t.Fatal("set --filename on a missing instruction succeeded; want an error")
	}
}
