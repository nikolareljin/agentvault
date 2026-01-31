package cmd

import (
	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "agentvault",
	Short: "Manage AI agents, keys, and instructions",
	Long: `AgentVault is a CLI/TUI tool for managing multiple AI agents,
their API keys, model configurations, and custom instructions.
Secrets are stored in an AES-256 encrypted local vault.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().String("config", "", "config directory (default: ~/.config/agentvault)")
}
