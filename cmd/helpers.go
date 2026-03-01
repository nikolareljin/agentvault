package cmd

import (
	"fmt"
	"os"

	"path/filepath"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"golang.org/x/term"
)

// resolveVaultPath returns the vault file path, respecting the --config flag.
func resolveVaultPath() string {
	cfgDir, err := rootCmd.PersistentFlags().GetString("config")
	if err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, config.VaultFile)
	}
	return config.VaultPath()
}

// readPassword prompts for a password from the terminal without echo.
// Uses golang.org/x/term to suppress input display for security.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
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
		return nil, fmt.Errorf("vault not found at %s (run 'agentvault init' first)", vaultPath)
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
