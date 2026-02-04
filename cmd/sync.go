package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronize unified rules across all agent instruction files",
	Long: `Generate and synchronize instruction files for all agents.

This command creates/updates instruction files (AGENTS.md, CLAUDE.md, codex.md,
MELDBOT.md, etc.) with unified rules, ensuring all agents follow the same guidelines.

The sync command:
  1. Reads unified rules from the vault
  2. Generates appropriate instruction files for each agent type
  3. Writes files to the target directory (or vault)

Examples:
  agentvault sync .              # Sync to current directory
  agentvault sync /path/to/proj  # Sync to specific project
  agentvault sync --vault        # Update vault's stored instructions
  agentvault sync . --agents     # Only generate AGENTS.md (shared)
  agentvault sync . --provider claude  # Only generate CLAUDE.md`,
}

var syncToCmd = &cobra.Command{
	Use:   "to [directory]",
	Short: "Sync rules to a project directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runSyncTo,
}

var syncVaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Update vault's stored instruction files with current rules",
	RunE:  runSyncVault,
}

var syncPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Preview what would be generated without writing files",
	RunE:  runSyncPreview,
}

func init() {
	rootCmd.AddCommand(syncCmd)
	syncCmd.AddCommand(syncToCmd)
	syncCmd.AddCommand(syncVaultCmd)
	syncCmd.AddCommand(syncPreviewCmd)

	syncToCmd.Flags().Bool("agents-only", false, "only generate AGENTS.md")
	syncToCmd.Flags().String("provider", "", "only generate for specific provider")
	syncToCmd.Flags().Bool("include-roles", true, "include role descriptions")
	syncToCmd.Flags().Bool("force", false, "overwrite existing files")

	syncVaultCmd.Flags().Bool("include-roles", true, "include role descriptions")

	syncPreviewCmd.Flags().String("provider", "", "preview specific provider")
}

func runSyncTo(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	dir := args[0]
	agentsOnly, _ := cmd.Flags().GetBool("agents-only")
	providerFilter, _ := cmd.Flags().GetString("provider")
	includeRoles, _ := cmd.Flags().GetBool("include-roles")
	force, _ := cmd.Flags().GetBool("force")

	shared := v.SharedConfig()

	if len(shared.Rules) == 0 {
		fmt.Println("No rules configured. Use 'agentvault rules init' to add default rules.")
		return nil
	}

	// Generate AGENTS.md (universal)
	if agentsOnly || providerFilter == "" {
		content := generateAgentsMD(shared, includeRoles)
		path := filepath.Join(dir, "AGENTS.md")
		if err := writeIfAllowed(path, content, force); err != nil {
			return err
		}
		fmt.Printf("  Wrote: AGENTS.md (%d bytes)\n", len(content))
	}

	if agentsOnly {
		return nil
	}

	// Generate provider-specific files
	providers := []agent.Provider{
		agent.ProviderClaude,
		agent.ProviderCodex,
		agent.ProviderMeldbot,
		agent.ProviderOpenclaw,
		agent.ProviderNanoclaw,
	}

	for _, p := range providers {
		if providerFilter != "" && string(p) != providerFilter {
			continue
		}

		content := generateProviderMD(p, shared, includeRoles)
		filename := agent.WellKnownInstructions[string(p)]
		if filename == "" {
			filename = strings.ToUpper(string(p)) + ".md"
		}

		path := filepath.Join(dir, filename)

		// Create parent directories if needed (for .github/copilot-instructions.md)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}

		if err := writeIfAllowed(path, content, force); err != nil {
			return err
		}
		fmt.Printf("  Wrote: %s (%d bytes)\n", filename, len(content))
	}

	fmt.Println("\nSync complete. All agents will now follow the same rules.")
	return nil
}

func runSyncVault(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	includeRoles, _ := cmd.Flags().GetBool("include-roles")
	shared := v.SharedConfig()

	if len(shared.Rules) == 0 {
		fmt.Println("No rules configured. Use 'agentvault rules init' to add default rules.")
		return nil
	}

	now := time.Now()
	updated := 0

	// Generate AGENTS.md
	agentsContent := generateAgentsMD(shared, includeRoles)
	agentsInst := agent.InstructionFile{
		Name:      "agents",
		Filename:  "AGENTS.md",
		Content:   agentsContent,
		UpdatedAt: now,
	}
	if err := v.SetInstruction(agentsInst); err != nil {
		return err
	}
	fmt.Printf("  Updated: agents (AGENTS.md, %d bytes)\n", len(agentsContent))
	updated++

	// Generate provider-specific files
	providers := []agent.Provider{
		agent.ProviderClaude,
		agent.ProviderCodex,
		agent.ProviderMeldbot,
		agent.ProviderOpenclaw,
		agent.ProviderNanoclaw,
	}

	for _, p := range providers {
		content := generateProviderMD(p, shared, includeRoles)
		name := string(p)
		filename := agent.WellKnownInstructions[name]
		if filename == "" {
			filename = strings.ToUpper(name) + ".md"
		}

		inst := agent.InstructionFile{
			Name:      name,
			Filename:  filename,
			Content:   content,
			UpdatedAt: now,
		}
		if err := v.SetInstruction(inst); err != nil {
			return err
		}
		fmt.Printf("  Updated: %s (%s, %d bytes)\n", name, filename, len(content))
		updated++
	}

	fmt.Printf("\nUpdated %d instruction file(s) in vault.\n", updated)
	fmt.Println("Use 'agentvault instructions push <dir>' to write to a project.")
	return nil
}

func runSyncPreview(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	providerFilter, _ := cmd.Flags().GetString("provider")
	shared := v.SharedConfig()

	if len(shared.Rules) == 0 {
		fmt.Println("No rules configured. Use 'agentvault rules init' to add default rules.")
		return nil
	}

	if providerFilter == "" {
		fmt.Println("=== AGENTS.md (Universal) ===")
		fmt.Println()
		fmt.Println(generateAgentsMD(shared, true))
		fmt.Println()
	}

	if providerFilter == "" || providerFilter == "claude" {
		fmt.Println("=== CLAUDE.md ===")
		fmt.Println()
		fmt.Println(generateProviderMD(agent.ProviderClaude, shared, true))
	}

	return nil
}

// generateAgentsMD produces the universal AGENTS.md content from unified rules.
// This file is the single source of truth that all agents reference.
// Rules are grouped by category (security, commit, coding, behavior, general)
// and sorted by priority within each group.
func generateAgentsMD(shared agent.SharedConfig, includeRoles bool) string {
	var sb strings.Builder

	sb.WriteString("# Agent Instructions\n\n")
	sb.WriteString("These rules apply to ALL AI agents working on this project.\n\n")

	// Sort rules by priority (lower number = higher priority = applied first)
	rules := make([]agent.UnifiedRule, 0, len(shared.Rules))
	for _, r := range shared.Rules {
		if r.Enabled {
			rules = append(rules, r)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority < rules[j].Priority
	})

	// Group by category
	categories := map[string][]agent.UnifiedRule{}
	categoryOrder := []string{"security", "commit", "coding", "behavior", "general"}
	for _, r := range rules {
		cat := r.Category
		if cat == "" {
			cat = "general"
		}
		categories[cat] = append(categories[cat], r)
	}

	for _, cat := range categoryOrder {
		catRules := categories[cat]
		if len(catRules) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("## %s\n\n", strings.Title(cat)))
		for _, r := range catRules {
			sb.WriteString(fmt.Sprintf("### %s\n", r.Description))
			sb.WriteString(fmt.Sprintf("%s\n\n", r.Content))
		}
	}

	// Add roles section if requested
	if includeRoles && len(shared.Roles) > 0 {
		sb.WriteString("## Available Roles\n\n")
		sb.WriteString("These roles can be applied to customize agent behavior:\n\n")
		for _, role := range shared.Roles {
			sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", role.Title, role.Name, role.Description))
		}
		sb.WriteString("\n")
	}

	// Add shared prompt if set
	if shared.SystemPrompt != "" {
		sb.WriteString("## Base Instructions\n\n")
		sb.WriteString(shared.SystemPrompt)
		sb.WriteString("\n\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("*Generated by AgentVault. These rules are synchronized across all agents.*\n")

	return sb.String()
}

// generateProviderMD produces a provider-specific instruction file (e.g., CLAUDE.md).
// Each file includes the same unified rules but formatted for the specific agent,
// with provider-specific notes about how the file is consumed.
func generateProviderMD(provider agent.Provider, shared agent.SharedConfig, includeRoles bool) string {
	var sb strings.Builder

	providerName := strings.Title(string(provider))

	sb.WriteString(fmt.Sprintf("# %s Instructions\n\n", providerName))
	sb.WriteString(fmt.Sprintf("These instructions are for %s when working on this project.\n\n", providerName))

	// Add provider-specific notes
	switch provider {
	case agent.ProviderClaude:
		sb.WriteString("> This file is read by Claude Code and Claude in supported IDEs.\n\n")
	case agent.ProviderCodex:
		sb.WriteString("> This file is read by Codex CLI.\n\n")
	case agent.ProviderMeldbot:
		sb.WriteString("> This file is read by Meldbot.\n\n")
	case agent.ProviderOpenclaw:
		sb.WriteString("> This file is read by Openclaw.\n\n")
	case agent.ProviderNanoclaw:
		sb.WriteString("> This file is read by Nanoclaw.\n\n")
	}

	// Include the universal rules
	sb.WriteString("## Rules\n\n")
	sb.WriteString("The following rules apply (see AGENTS.md for details):\n\n")

	rules := make([]agent.UnifiedRule, 0, len(shared.Rules))
	for _, r := range shared.Rules {
		if r.Enabled {
			rules = append(rules, r)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority < rules[j].Priority
	})

	for _, r := range rules {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", r.Description, r.Content))
	}
	sb.WriteString("\n")

	// Add shared prompt
	if shared.SystemPrompt != "" {
		sb.WriteString("## Base Instructions\n\n")
		sb.WriteString(shared.SystemPrompt)
		sb.WriteString("\n\n")
	}

	// Add roles section if requested
	if includeRoles && len(shared.Roles) > 0 {
		sb.WriteString("## Roles\n\n")
		sb.WriteString("Available roles (can be set per-agent):\n\n")
		for _, role := range shared.Roles {
			sb.WriteString(fmt.Sprintf("### %s\n", role.Title))
			sb.WriteString(fmt.Sprintf("%s\n\n", role.Prompt))
		}
	}

	sb.WriteString("---\n")
	sb.WriteString("*Generated by AgentVault. Keep in sync with AGENTS.md.*\n")

	return sb.String()
}

// writeIfAllowed writes content to path, refusing to overwrite existing files
// unless force is true. This prevents accidental loss of manually-edited files.
func writeIfAllowed(path, content string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("file %s already exists (use --force to overwrite)", path)
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}
