//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// Detection and setup pull read Claude's settings.json and Codex's
// config.toml. A pipe in their place is skipped; before, it blocked
// detect, setup pull and export.
func TestDetectAndLoadSkipPipes(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(root, ".claude", "settings.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, ".codex", "config.toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		detectClaude()
		detectCodex()
		if _, err := agent.LoadClaudeConfig(); err != nil {
			t.Error(err)
		}
		if _, err := agent.LoadCodexConfig(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("reading a pipe blocked")
	}
}
