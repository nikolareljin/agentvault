package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import agents from an encrypted vault file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault import: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(importCmd)
}
