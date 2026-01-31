package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new AgentVault in the default config directory",
	Long:  `Creates ~/.config/agentvault/ with an empty encrypted vault file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault init: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
