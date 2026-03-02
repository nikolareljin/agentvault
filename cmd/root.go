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
//	├── prompt                 Gateway prompt execution with usage tracking
//	├── serve                  Start HTTP API server for vault integration
//	├── status                 Show token usage and quota status
//	├── --tui, -t              Launch interactive terminal UI
//	└── version                Show version info
package cmd

import (
	"fmt"
	"os"
	"strings"

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
  agentvault                   # Launch interactive UI (default)`,
}

// Execute runs the root command. Called from main().
func Execute() error {
	args := os.Args[1:]
	if err := applyEarlyPersistentFlags(args); err != nil {
		return err
	}

	// Preserve Cobra help semantics even when TUI interception is enabled.
	if containsHelpFlag(args) {
		rootCmd.SetArgs(args)
		return rootCmd.Execute()
	}

	if launch, target, err := parseTUIInvocation(args); err != nil {
		return err
	} else if launch {
		return launchTUI(target)
	}
	return rootCmd.Execute()
}

func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func applyEarlyPersistentFlags(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return fmt.Errorf("flag needs an argument: --config")
			}
			if err := rootCmd.PersistentFlags().Set("config", args[i+1]); err != nil {
				return err
			}
			i++
		case strings.HasPrefix(arg, "--config="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
			if value == "" {
				return fmt.Errorf("flag needs an argument: --config")
			}
			if err := rootCmd.PersistentFlags().Set("config", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func launchTUI(target string) error {
	v, err := openVault()
	if err != nil {
		return tui.RunWithTarget(target)
	}
	return tui.RunWithVaultTarget(v, target)
}

func parseTUIInvocation(args []string) (bool, string, error) {
	if len(args) == 0 {
		return true, "agents", nil
	}

	tuiFlagIdx := -1
	tuiFlagValue := ""
	for i, arg := range args {
		switch {
		case arg == "--tui" || arg == "-t":
			tuiFlagIdx = i
			tuiFlagValue = ""
		case strings.HasPrefix(arg, "--tui="):
			tuiFlagIdx = i
			tuiFlagValue = strings.TrimSpace(strings.TrimPrefix(arg, "--tui="))
		case strings.HasPrefix(arg, "-t="):
			tuiFlagIdx = i
			tuiFlagValue = strings.TrimSpace(strings.TrimPrefix(arg, "-t="))
		default:
			continue
		}
	}
	if tuiFlagIdx == -1 {
		return false, "", nil
	}

	if tuiFlagValue == "" && tuiFlagIdx+1 < len(args) && !strings.HasPrefix(args[tuiFlagIdx+1], "-") {
		tuiFlagValue = strings.TrimSpace(args[tuiFlagIdx+1])
	}
	if tuiFlagValue != "" {
		target, ok := normalizeTUITarget(tuiFlagValue)
		if !ok {
			return false, "", fmt.Errorf("invalid --tui target %q (valid: agents, instructions, rules, sessions, detected, commands, status)", tuiFlagValue)
		}
		return true, target, nil
	}

	command, hasCommand := firstCommandToken(args, tuiFlagIdx)
	if hasCommand {
		if target, ok := normalizeTUITarget(command); ok {
			return true, target, nil
		}
	}
	return true, "agents", nil
}

func firstCommandToken(args []string, tuiFlagIdx int) (string, bool) {
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--config" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
			if (arg == "--tui" || arg == "-t") && i == tuiFlagIdx && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
			continue
		}
		return arg, true
	}
	return "", false
}

func normalizeTUITarget(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "default", "home", "agent", "agents", "add", "list", "edit", "remove", "run", "init", "unlock":
		return "agents", true
	case "instruction", "instructions":
		return "instructions", true
	case "rule", "rules":
		return "rules", true
	case "session", "sessions":
		return "sessions", true
	case "detect", "detected":
		return "detected", true
	case "command", "commands", "prompt", "sync", "generate":
		return "commands", true
	case "status", "config", "setup", "serve", "version":
		return "status", true
	default:
		return "", false
	}
}

func init() {
	rootCmd.PersistentFlags().String("config", "", "config directory (default: ~/.config/agentvault)")
	rootCmd.PersistentFlags().StringP("tui", "t", "", "launch interactive terminal UI; optional target: agents|instructions|rules|sessions|detected|commands|status")
	if tuiFlag := rootCmd.PersistentFlags().Lookup("tui"); tuiFlag != nil {
		tuiFlag.NoOptDefVal = "agents"
	}
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return launchTUI("agents")
		}
		return cmd.Help()
	}
}
