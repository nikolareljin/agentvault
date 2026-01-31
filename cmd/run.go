package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [name]",
	Short: "Invoke an agent with a prompt",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("agentvault run: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
