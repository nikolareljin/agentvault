package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	dir := Dir()
	want := filepath.Join("/tmp/xdg-test", "agentvault")
	if dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}

func TestDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, _ := os.UserHomeDir()
	dir := Dir()
	if !strings.HasPrefix(dir, home) {
		t.Errorf("Dir() = %q, expected prefix %q", dir, home)
	}
}

func TestVaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	path := VaultPath()
	want := filepath.Join("/tmp/xdg-test", "agentvault", "vault.enc")
	if path != want {
		t.Errorf("VaultPath() = %q, want %q", path, want)
	}
}

// With HOME unset the path would be relative and land in the working directory.
func TestClaudeDesktopConfigPathRefusesRelative(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("home comes from HOME only on unix")
	}
	t.Setenv("HOME", "")
	if p, err := ClaudeDesktopConfigPath(); err == nil {
		t.Fatalf("got %q, want an error", p)
	}
	t.Setenv("HOME", filepath.FromSlash("/h"))
	if p, err := ClaudeDesktopConfigPath(); err != nil || !filepath.IsAbs(p) {
		t.Fatalf("got %q, %v", p, err)
	}
}

func TestClaudeDesktopConfigPathFor(t *testing.T) {
	home := filepath.FromSlash("/h")
	cases := []struct{ goos, appData, want string }{
		{"darwin", "", filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")},
		{"windows", filepath.FromSlash("/ad"), filepath.Join(filepath.FromSlash("/ad"), "Claude", "claude_desktop_config.json")},
		{"windows", "", filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")},
		{"linux", "", filepath.Join(home, ".config", "claude", "claude_desktop_config.json")},
	}
	for _, c := range cases {
		if got := claudeDesktopConfigPathFor(c.goos, home, c.appData); got != c.want {
			t.Errorf("%s/%q: got %s, want %s", c.goos, c.appData, got, c.want)
		}
	}
}
