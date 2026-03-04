package cmd

import (
	"errors"
	"fmt"
	"os"

	"path/filepath"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"golang.org/x/term"
)

// ErrVaultNotFound marks a missing vault file condition.
var ErrVaultNotFound = errors.New("vault not found")

// resolveVaultPath returns the vault file path, respecting the --config flag.
func resolveVaultPath() string {
	cfgDir := resolveConfigDir()
	if cfgDir != "" {
		return filepath.Join(cfgDir, config.VaultFile)
	}
	return config.VaultPath()
}

// resolvePromptHistoryPath returns the prompt history path, respecting --config.
func resolvePromptHistoryPath() string {
	cfgDir := resolveConfigDir()
	if cfgDir != "" {
		return filepath.Join(cfgDir, "prompt-history.jsonl")
	}
	return filepath.Join(config.Dir(), "prompt-history.jsonl")
}

func resolveConfigDir() string {
	cfgDir, err := rootCmd.PersistentFlags().GetString("config")
	if err == nil && cfgDir != "" {
		return cfgDir
	}
	return ""
}

// readPassword prompts for a password from the terminal without echo.
// Uses golang.org/x/term to suppress input display for security.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(stdinFD())
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(pw), nil
}

// openVault prompts for the master password and unlocks the vault.
// This is the common entry point for all commands that need vault access.
// It reads the vault path from config (respecting --config flag), checks
// existence, prompts for the password, and returns the unlocked vault.
func openVault() (*vault.Vault, error) {
	vaultPath := resolveVaultPath()
	v := vault.New(vaultPath)
	if !v.Exists() {
		return nil, fmt.Errorf("%w at %s (run 'agentvault init' first)", ErrVaultNotFound, vaultPath)
	}
	pw, err := readPassword("Master password: ")
	if err != nil {
		return nil, err
	}
	if err := v.Unlock(pw); err != nil {
		return nil, err
	}
	return v, nil
}
