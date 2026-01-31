package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock the vault (cache master password)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault unlock: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(unlockCmd)
}
