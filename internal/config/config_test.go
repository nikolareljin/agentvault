package config

import (
	"os"
	"strings"
	"testing"
)

func TestDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	dir := Dir()
	if dir != "/tmp/xdg-test/agentvault" {
		t.Errorf("Dir() = %q, want %q", dir, "/tmp/xdg-test/agentvault")
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
	if path != "/tmp/xdg-test/agentvault/vault.enc" {
		t.Errorf("VaultPath() = %q, want %q", path, "/tmp/xdg-test/agentvault/vault.enc")
	}
}
