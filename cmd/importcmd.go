package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nikolareljin/agentvault/internal/crypto"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import agents from an encrypted vault file",
	Long: `Import agents and shared config from an export file. Agents whose names
already exist in the vault are skipped. Use --plain for unencrypted JSON files.

Example:
  agentvault import backup.vault
  agentvault import agents.json --plain`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		inPath := args[0]
		raw, err := os.ReadFile(inPath)
		if err != nil {
			return fmt.Errorf("reading import file: %w", err)
		}

		plain, _ := cmd.Flags().GetBool("plain")
		var data []byte

		if plain {
			// verify it's valid JSON
			if !json.Valid(raw) {
				return fmt.Errorf("import file is not valid JSON")
			}
			data = raw
		} else {
			if len(raw) < crypto.SaltLen {
				return fmt.Errorf("import file is too short (corrupted?)")
			}
			salt := raw[:crypto.SaltLen]
			ciphertext := raw[crypto.SaltLen:]

			pw, err := readPassword("Import file password: ")
			if err != nil {
				return err
			}
			key, err := crypto.DeriveKey(pw, salt)
			if err != nil {
				return err
			}
			data, err = crypto.Decrypt(ciphertext, key)
			if err != nil {
				return fmt.Errorf("wrong password or corrupted import file")
			}
		}

		imported, skipped, err := v.ImportData(data)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d agents.\n", imported)
		if len(skipped) > 0 {
			fmt.Printf("Skipped (already exist): %s\n", strings.Join(skipped, ", "))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().Bool("plain", false, "import from unencrypted JSON")
}
