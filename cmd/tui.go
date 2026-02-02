package cmd

import (
	"github.com/nikolareljin/agentvault/internal/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch interactive TUI",
	Long:  `Launch an interactive terminal UI for browsing and inspecting agents.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return tui.Run()
		}
		return tui.RunWithVault(v)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
