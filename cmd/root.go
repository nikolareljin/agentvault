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

type tuiTargetSpec struct {
	canonical string
	aliases   []string
}

// Keep canonical targets and aliases together to avoid drift across maps/slices.
var tuiTargetSpecs = []tuiTargetSpec{
	{
		canonical: "agents",
		aliases: []string{
			"", "default", "home", "agent", "agents", "add", "list", "edit", "remove", "run", "init", "unlock",
		},
	},
	{
		canonical: "instructions",
		aliases:   []string{"inst", "instruction", "instructions"},
	},
	{
		canonical: "rules",
		aliases:   []string{"rule", "rules"},
	},
	{
		canonical: "sessions",
		aliases:   []string{"sess", "session", "sessions", "workspace", "workspaces"},
	},
	{
		canonical: "detected",
		aliases:   []string{"detect", "detected"},
	},
	{
		canonical: "commands",
		aliases:   []string{"command", "commands", "prompt", "sync", "generate"},
	},
	{
		canonical: "status",
		aliases:   []string{"status", "config", "setup", "serve", "version"},
	},
}

var (
	canonicalTUITargets = buildCanonicalTUITargets()
	canonicalTUISet     = buildCanonicalTUISet()
	tuiTargetAliases    = buildTUITargetAliases()
)

func buildCanonicalTUITargets() []string {
	targets := make([]string, 0, len(tuiTargetSpecs))
	for _, spec := range tuiTargetSpecs {
		targets = append(targets, spec.canonical)
	}
	return targets
}

func buildTUITargetAliases() map[string]string {
	aliases := make(map[string]string)
	for _, spec := range tuiTargetSpecs {
		aliases[strings.ToLower(spec.canonical)] = spec.canonical
		for _, alias := range spec.aliases {
			normalized := strings.ToLower(strings.TrimSpace(alias))
			if normalized == "" {
				aliases[""] = spec.canonical
				continue
			}
			aliases[normalized] = spec.canonical
		}
	}
	return aliases
}

func buildCanonicalTUISet() map[string]struct{} {
	set := make(map[string]struct{}, len(canonicalTUITargets))
	for _, target := range canonicalTUITargets {
		set[strings.ToLower(target)] = struct{}{}
	}
	return set
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
	filteredArgs := stripPromptModeFlags(args)

	// Preserve Cobra help semantics even when TUI interception is enabled.
	if containsHelpFlag(args) {
		rootCmd.SetArgs(filteredArgs)
		return rootCmd.Execute()
	}

	if launch, err := parsePromptModeInvocation(args); err != nil {
		return err
	} else if launch {
		return runPromptMode()
	}

	if launch, target, err := parseTUIInvocation(filteredArgs); err != nil {
		return err
	} else if launch {
		return launchTUI(target)
	}
	rootCmd.SetArgs(filteredArgs)
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
	return errors.Is(err, ErrVaultNotFound) || errors.Is(err, os.ErrNotExist)
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
		// Only consume the next token as an explicit target when it is canonical.
		candidate := strings.TrimSpace(args[tuiFlagIdx+1])
		if canonical, ok := normalizeExplicitTUITarget(candidate); ok {
			tuiFlagValue = canonical
		} else if tuiFlagIdx+2 >= len(args) {
			// For trailing single-token values after -t/--tui, treat as explicit target input.
			return false, "", fmt.Errorf("invalid TUI target %q (valid: %s)", candidate, strings.Join(canonicalTUITargets, ", "))
		}
	}
	if tuiFlagValue != "" {
		target, ok := normalizeExplicitTUITarget(tuiFlagValue)
		if !ok {
			return false, "", fmt.Errorf("invalid TUI target %q (valid: %s)", tuiFlagValue, strings.Join(canonicalTUITargets, ", "))
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

func parsePromptModeInvocation(args []string) (bool, error) {
	flagSeen := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-p" || arg == "--prompt-mode" {
			flagSeen = true
			break
		}
	}
	if !flagSeen {
		return false, nil
	}
	if _, hasCommand := firstCommandToken(args); hasCommand {
		return false, nil
	}
	if err := validatePromptModeArgs(args); err != nil {
		return false, err
	}
	return true, nil
}

func validatePromptModeArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		switch {
		case arg == "-p" || arg == "--prompt-mode":
			continue
		case arg == "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return fmt.Errorf("flag needs an argument: --config")
			}
			i++
			continue
		case strings.HasPrefix(arg, "--config="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
			if value == "" {
				return fmt.Errorf("flag needs an argument: --config")
			}
			continue
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag for prompt mode: %s", arg)
		default:
			// Non-flag positional args are treated as command args elsewhere.
			return nil
		}
	}
	return nil
}

func stripPromptModeFlags(args []string) []string {
	out := make([]string, 0, len(args))
	afterDoubleDash := false
	for _, arg := range args {
		if arg == "--" {
			afterDoubleDash = true
			out = append(out, arg)
			continue
		}
		if afterDoubleDash {
			out = append(out, arg)
			continue
		}
		if arg == "-p" || arg == "--prompt-mode" {
			continue
		}
		out = append(out, arg)
	}
	return out
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
			// Skip the next token only when bare --tui/-t actually consumed a canonical target.
			if (arg == "--tui" || arg == "-t") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				if _, ok := normalizeExplicitTUITarget(args[i+1]); ok {
					skipNext = true
				}
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

func normalizeExplicitTUITarget(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := canonicalTUISet[normalized]; !ok {
		return "", false
	}
	return normalized, true
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
