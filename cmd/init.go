package cmd

import (
	"fmt"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new AgentVault in the default config directory",
	Long:  `Creates ~/.config/agentvault/ with an empty encrypted vault file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := config.VaultPath()
		v := vault.New(vaultPath)
		if v.Exists() {
			return fmt.Errorf("vault already exists at %s", vaultPath)
		}

		pw, err := readPassword("New master password: ")
		if err != nil {
			return err
		}
		if len(pw) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}
		confirm, err := readPassword("Confirm master password: ")
		if err != nil {
			return err
		}
		if pw != confirm {
			return fmt.Errorf("passwords do not match")
		}

		if err := v.Init(pw); err != nil {
			return err
		}
		fmt.Printf("Vault initialized at %s\n", vaultPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
