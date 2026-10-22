package cmd

import (
	"fmt"
	"os"

	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new AgentVault in the default config directory",
	Long: `Creates ~/.config/agentvault/ with an empty encrypted vault file.

The master password is read from AGENTVAULT_PASSWORD when set (at least
12 characters), so a new machine can be set up by script; otherwise init
asks for it twice.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := resolveVaultPath()
		v := vault.New(vaultPath)
		if v.Exists() {
			return fmt.Errorf("vault already exists at %s", vaultPath)
		}

		pw, err := resolveInitPassword()
		if err != nil {
			return err
		}

		if err := v.Init(pw); err != nil {
			return err
		}
		fmt.Printf("Vault initialized at %s\n", vaultPath)
		return nil
	},
}

// resolveInitPassword takes the new master password from AGENTVAULT_PASSWORD
// when set, so a new machine can be set up by script, else asks twice.
func resolveInitPassword() (string, error) {
	if pw := os.Getenv(VaultPasswordEnv); pw != "" {
		if len(pw) < minNewBundlePasswordLen {
			return "", fmt.Errorf("%s must be at least %d characters", VaultPasswordEnv, minNewBundlePasswordLen)
		}
		return pw, nil
	}
	if !term.IsTerminal(stdinFD()) {
		return "", fmt.Errorf("init needs a new master password but stdin is not a terminal: set %s", VaultPasswordEnv)
	}
	pw, err := readPassword("New master password: ")
	if err != nil {
		return "", err
	}
	if len(pw) < minNewBundlePasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", minNewBundlePasswordLen)
	}
	confirm, err := readPassword("Confirm master password: ")
	if err != nil {
		return "", err
	}
	if pw != confirm {
		return "", fmt.Errorf("passwords do not match")
	}
	return pw, nil
}

func init() {
	rootCmd.AddCommand(initCmd)
}
