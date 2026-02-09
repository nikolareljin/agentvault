// Package cmd implements the CLI commands for AgentVault.
//
// Command hierarchy:
//
//	agentvault
//	├── init                   Initialize encrypted vault
//	├── detect [add]           Auto-detect installed AI agents
//	├── add/list/edit/remove   Agent CRUD operations
//	├── run                    Show effective agent configuration
//	├── rules                  Manage unified cross-agent rules
//	│   ├── init/list/add/edit/remove/enable/disable/export
//	├── roles                  Manage agent personas
//	│   ├── init/list/add/edit/remove/apply
//	├── session                Multi-agent workspace management
//	│   ├── create/start/stop/list/show/export/import/activate
//	├── sync                   Generate instruction files from rules
//	│   ├── to/vault/preview
//	├── setup                  Full configuration export/import
//	│   ├── export/import/show/apply/pull
//	├── instructions           Manage stored instruction files
//	├── status                 Show token usage and quota status
//	├── --tui, -t              Launch interactive terminal UI
//	└── version                Show version info
package cmd

import (
	"github.com/nikolareljin/agentvault/internal/tui"
	"github.com/spf13/cobra"
)

// Build-time variables, set by the linker via -ldflags.
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
Secrets are stored in an AES-256 encrypted local vault.

Key features:
  - Unified rules that apply across ALL agents (Claude, Codex, Meldbot, etc.)
  - Roles/personas for consistent agent behavior
  - Multi-agent sessions with parallel execution
  - Encrypted vault with AES-256-GCM
  - Export/import complete setups across machines
  - Interactive TUI for browsing and managing agents

Get started:
  agentvault init              # Create vault
  agentvault detect add        # Auto-detect and add agents
  agentvault rules init        # Set up default rules
  agentvault --tui             # Launch interactive UI`,
}

// Execute runs the root command. Called from main().
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().String("config", "", "config directory (default: ~/.config/agentvault)")
	rootCmd.Flags().BoolP("tui", "t", false, "launch interactive terminal UI")
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		launchTUI, _ := cmd.Flags().GetBool("tui")
		if launchTUI {
			v, err := openVault()
			if err != nil {
				return tui.Run()
			}
			return tui.RunWithVault(v)
		}
		return cmd.Help()
	}
}
