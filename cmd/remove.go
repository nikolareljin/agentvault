package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove an agent from the vault",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault remove: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
