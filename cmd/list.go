package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault list: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
