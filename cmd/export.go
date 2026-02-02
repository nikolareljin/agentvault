package cmd

import (
	"fmt"
	"os"

	"github.com/nikolareljin/agentvault/internal/crypto"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export vault to an encrypted file",
	Long: `Export all agents and shared config to an encrypted file that can be
imported on another machine. You will be prompted for an export password
(can differ from your vault master password).

Use --plain to export as unencrypted JSON (API keys will be visible).

Example:
  agentvault export backup.vault
  agentvault export agents.json --plain`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		plaintext, err := v.ExportData()
		if err != nil {
			return err
		}

		plain, _ := cmd.Flags().GetBool("plain")
		outPath := args[0]

		if plain {
			if err := os.WriteFile(outPath, plaintext, 0600); err != nil {
				return fmt.Errorf("writing export: %w", err)
			}
			fmt.Printf("Exported %d agents to %s (unencrypted)\n", len(v.List()), outPath)
			return nil
		}

		pw, err := readPassword("Export password: ")
		if err != nil {
			return err
		}
		if len(pw) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}
		confirm, err := readPassword("Confirm export password: ")
		if err != nil {
			return err
		}
		if pw != confirm {
			return fmt.Errorf("passwords do not match")
		}

		salt, err := crypto.GenerateSalt()
		if err != nil {
			return err
		}
		key, err := crypto.DeriveKey(pw, salt)
		if err != nil {
			return err
		}
		ciphertext, err := crypto.Encrypt(plaintext, key)
		if err != nil {
			return err
		}
		data := append(salt, ciphertext...)
		if err := os.WriteFile(outPath, data, 0600); err != nil {
			return fmt.Errorf("writing export: %w", err)
		}
		fmt.Printf("Exported %d agents to %s (encrypted)\n", len(v.List()), outPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().Bool("plain", false, "export as unencrypted JSON")
}
