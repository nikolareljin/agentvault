package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export vault to an encrypted file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault export: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
}
