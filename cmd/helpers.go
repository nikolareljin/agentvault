package cmd

import (
	"fmt"
	"os"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"golang.org/x/term"
)

// readPassword prompts for a password from the terminal without echo.
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
func openVault() (*vault.Vault, error) {
	vaultPath := config.VaultPath()
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
