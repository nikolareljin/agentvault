package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// initTestDir points the config at an empty directory in a throwaway HOME.
func initTestDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	cfgDir := filepath.Join(root, config.AppName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, cfgDir)
	return cfgDir
}

// A new machine is set up by script: init takes AGENTVAULT_PASSWORD, as
// every command that opens the vault does.
func TestInitTakesPasswordFromEnv(t *testing.T) {
	cfgDir := initTestDir(t)
	t.Setenv(VaultPasswordEnv, "correct horse battery")
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := vault.New(filepath.Join(cfgDir, config.VaultFile)).Unlock("correct horse battery"); err != nil {
		t.Fatalf("vault does not open with the env password: %v", err)
	}
}

func TestInitRefusesShortEnvPassword(t *testing.T) {
	cfgDir := initTestDir(t)
	t.Setenv(VaultPasswordEnv, "short")
	err := initCmd.RunE(initCmd, nil)
	if err == nil || !strings.Contains(err.Error(), VaultPasswordEnv) {
		t.Fatalf("init with a short env password: %v", err)
	}
	if vault.New(filepath.Join(cfgDir, config.VaultFile)).Exists() {
		t.Error("init created a vault with a short password")
	}
}

// Without the variable and without a terminal, init names the remedy
// instead of failing on the terminal read.
func TestInitWithoutTerminalNamesTheEnv(t *testing.T) {
	initTestDir(t)
	t.Setenv(VaultPasswordEnv, "")
	err := initCmd.RunE(initCmd, nil)
	if err == nil || !strings.Contains(err.Error(), VaultPasswordEnv) {
		t.Fatalf("init without a terminal: %v", err)
	}
}
