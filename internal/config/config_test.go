package config

import (
	"os"
	"path/filepath"
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
