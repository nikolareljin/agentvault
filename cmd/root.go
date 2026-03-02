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
	"errors"
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

var tuiTargetAliases = map[string]string{
	"":             "agents",
	"default":      "agents",
	"home":         "agents",
	"agent":        "agents",
	"agents":       "agents",
	"add":          "agents",
	"list":         "agents",
	"edit":         "agents",
	"remove":       "agents",
	"run":          "agents",
	"init":         "agents",
	"unlock":       "agents",
	"inst":         "instructions",
	"instruction":  "instructions",
	"instructions": "instructions",
	"rule":         "rules",
	"rules":        "rules",
	"sess":         "sessions",
	"session":      "sessions",
	"sessions":     "sessions",
	"workspace":    "sessions",
	"workspaces":   "sessions",
	"detect":       "detected",
	"detected":     "detected",
	"command":      "commands",
	"commands":     "commands",
	"prompt":       "commands",
	"sync":         "commands",
	"generate":     "commands",
	"status":       "status",
	"config":       "status",
	"setup":        "status",
	"serve":        "status",
	"version":      "status",
}

var canonicalTUITargets = []string{
	"agents",
	"instructions",
	"rules",
	"sessions",
	"detected",
	"commands",
	"status",
}

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
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func applyEarlyPersistentFlags(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
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
		if !isVaultNotFoundError(err) {
			return err
		}
		// Fall back to placeholder TUI only when the vault does not exist yet.
		return tui.RunWithTarget(target)
	}
	return tui.RunWithVaultTarget(v, target)
}

func isVaultNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "vault not found")
}

func parseTUIInvocation(args []string) (bool, string, error) {
	if len(args) == 0 {
		return true, "agents", nil
	}

	tuiFlagIdx := -1
	tuiFlagValue := ""
	consumeNextAsTarget := false
	for i, arg := range args {
		if arg == "--" {
			break
		}
		switch {
		case arg == "--tui" || arg == "-t":
			tuiFlagIdx = i
			tuiFlagValue = ""
			consumeNextAsTarget = true
		case strings.HasPrefix(arg, "--tui="):
			tuiFlagIdx = i
			tuiFlagValue = strings.TrimSpace(strings.TrimPrefix(arg, "--tui="))
			consumeNextAsTarget = false
		case strings.HasPrefix(arg, "-t="):
			tuiFlagIdx = i
			tuiFlagValue = strings.TrimSpace(strings.TrimPrefix(arg, "-t="))
			consumeNextAsTarget = false
		case strings.HasPrefix(arg, "-t") && len(arg) > 2 && arg[2] != '=':
			tuiFlagIdx = i
			tuiFlagValue = strings.TrimSpace(arg[2:])
			consumeNextAsTarget = false
		default:
			continue
		}
	}
	if tuiFlagIdx == -1 {
		return false, "", nil
	}

	if consumeNextAsTarget && tuiFlagValue == "" && tuiFlagIdx+1 < len(args) && !strings.HasPrefix(args[tuiFlagIdx+1], "-") {
		tuiFlagValue = strings.TrimSpace(args[tuiFlagIdx+1])
	}
	if tuiFlagValue != "" {
		target, ok := normalizeTUITarget(tuiFlagValue)
		if !ok {
			return false, "", fmt.Errorf("invalid --tui target %q (valid: %s)", tuiFlagValue, strings.Join(canonicalTUITargets, ", "))
		}
		return true, target, nil
	}

	command, hasCommand := firstCommandToken(args)
	if hasCommand {
		if target, ok := normalizeTUITarget(command); ok {
			return true, target, nil
		}
	}
	return true, "agents", nil
}

func firstCommandToken(args []string) (string, bool) {
	skipNext := false
	afterDoubleDash := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--" {
			afterDoubleDash = true
			continue
		}
		if afterDoubleDash {
			return arg, true
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--config" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
			// Skip consumed values for any bare --tui/-t occurrence so command inference
			// always starts from the first real command token.
			if (arg == "--tui" || arg == "-t") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
			continue
		}
		return arg, true
	}
	return "", false
}

func normalizeTUITarget(raw string) (string, bool) {
	target, ok := tuiTargetAliases[strings.ToLower(strings.TrimSpace(raw))]
	return target, ok
}

func init() {
	rootCmd.PersistentFlags().String("config", "", "config directory (default: ~/.config/agentvault)")
	rootCmd.PersistentFlags().StringP("tui", "t", "", fmt.Sprintf("launch interactive terminal UI; optional target: %s", strings.Join(canonicalTUITargets, "|")))
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
