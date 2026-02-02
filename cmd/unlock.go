package cmd

import (
	"fmt"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Verify the vault master password",
	Long: `Verify that the master password can decrypt the vault. Useful for
scripting and CI pipelines to validate credentials before running
other commands.

Exit code 0 means the password is correct; non-zero means failure.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := config.VaultPath()
		v := vault.New(vaultPath)
		if !v.Exists() {
			return fmt.Errorf("vault not found at %s (run 'agentvault init' first)", vaultPath)
		}
		pw, err := readPassword("Master password: ")
		if err != nil {
			return err
		}
		if err := v.Unlock(pw); err != nil {
			return err
		}
		fmt.Printf("Vault unlocked (%d agents).\n", len(v.List()))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(unlockCmd)
}
