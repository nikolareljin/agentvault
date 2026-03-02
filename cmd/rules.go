package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var rulesCmd = &cobra.Command{
	Use:     "rules",
	Aliases: []string{"rule"},
	Short:   "Manage unified rules that apply across all agents",
	Long: `Manage rules that apply consistently across all AI agents.

Rules ensure all agents (Claude, Codex, Meldbot, Openclaw, Nanoclaw, etc.)
follow the same guidelines. For example:
  - Never include model names in commit messages
  - Follow existing code style
  - Never hardcode secrets

Rules are exported/imported with your setup, ensuring consistency across machines.`,
}

var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all unified rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		showDisabled, _ := cmd.Flags().GetBool("all")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		shared := v.SharedConfig()
		rules := shared.Rules

		if len(rules) == 0 {
			fmt.Println("No rules configured. Use 'agentvault rules add' or 'rules init' to get started.")
			return nil
		}

		// Sort by priority
		sort.Slice(rules, func(i, j int) bool {
			return rules[i].Priority < rules[j].Priority
		})

		if jsonOutput {
			data, _ := json.MarshalIndent(rules, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Println("Unified Rules:")
		fmt.Println(strings.Repeat("─", 70))
		for _, r := range rules {
			if !showDisabled && !r.Enabled {
				continue
			}
			status := "✓"
			if !r.Enabled {
				status = "○"
			}
			fmt.Printf("\n  %s [%s] %s (priority: %d)\n", status, r.Category, r.Name, r.Priority)
			fmt.Printf("    %s\n", r.Description)
		}
		fmt.Println()
		return nil
	},
}

var rulesShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show details of a specific rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		shared := v.SharedConfig()
		rule, ok := agent.GetRule(shared.Rules, args[0])
		if !ok {
			return fmt.Errorf("rule %q not found", args[0])
		}

		fmt.Printf("Rule: %s\n", rule.Name)
		fmt.Printf("Description: %s\n", rule.Description)
		fmt.Printf("Category: %s\n", rule.Category)
		fmt.Printf("Priority: %d\n", rule.Priority)
		fmt.Printf("Enabled: %v\n", rule.Enabled)
		fmt.Printf("\nContent:\n%s\n", rule.Content)
		return nil
	},
}

var rulesAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new unified rule",
	Long: `Add a new rule that applies across all agents.

Example:
  agentvault rules add no-todos \
    --description "Don't leave TODO comments" \
    --content "Never leave TODO, FIXME, or HACK comments. Complete the work or create an issue." \
    --category coding \
    --priority 50`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		description, _ := cmd.Flags().GetString("description")
		content, _ := cmd.Flags().GetString("content")
		category, _ := cmd.Flags().GetString("category")
		priority, _ := cmd.Flags().GetInt("priority")

		if content == "" {
			return fmt.Errorf("--content is required")
		}
		if description == "" {
			description = name
		}

		shared := v.SharedConfig()

		// Check if rule already exists
		for _, r := range shared.Rules {
			if r.Name == name {
				return fmt.Errorf("rule %q already exists, use 'rules edit' to modify", name)
			}
		}

		rule := agent.UnifiedRule{
			Name:        name,
			Description: description,
			Content:     content,
			Category:    category,
			Priority:    priority,
			Enabled:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		shared.Rules = append(shared.Rules, rule)
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Rule %q added.\n", name)
		return nil
	},
}

var rulesEditCmd = &cobra.Command{
	Use:   "edit [name]",
	Short: "Edit an existing rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		idx := -1
		for i, r := range shared.Rules {
			if r.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("rule %q not found", name)
		}

		rule := &shared.Rules[idx]

		if cmd.Flags().Changed("description") {
			rule.Description, _ = cmd.Flags().GetString("description")
		}
		if cmd.Flags().Changed("content") {
			rule.Content, _ = cmd.Flags().GetString("content")
		}
		if cmd.Flags().Changed("category") {
			rule.Category, _ = cmd.Flags().GetString("category")
		}
		if cmd.Flags().Changed("priority") {
			rule.Priority, _ = cmd.Flags().GetInt("priority")
		}
		if cmd.Flags().Changed("enabled") {
			rule.Enabled, _ = cmd.Flags().GetBool("enabled")
		}

		rule.UpdatedAt = time.Now()

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Rule %q updated.\n", name)
		return nil
	},
}

var rulesRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		idx := -1
		for i, r := range shared.Rules {
			if r.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("rule %q not found", name)
		}

		shared.Rules = append(shared.Rules[:idx], shared.Rules[idx+1:]...)

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Rule %q removed.\n", name)
		return nil
	},
}

var rulesEnableCmd = &cobra.Command{
	Use:   "enable [name]",
	Short: "Enable a rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		for i, r := range shared.Rules {
			if r.Name == name {
				shared.Rules[i].Enabled = true
				shared.Rules[i].UpdatedAt = time.Now()
				if err := v.SetSharedConfig(shared); err != nil {
					return err
				}
				fmt.Printf("Rule %q enabled.\n", name)
				return nil
			}
		}
		return fmt.Errorf("rule %q not found", name)
	},
}

var rulesDisableCmd = &cobra.Command{
	Use:   "disable [name]",
	Short: "Disable a rule (keeps it but doesn't apply)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		for i, r := range shared.Rules {
			if r.Name == name {
				shared.Rules[i].Enabled = false
				shared.Rules[i].UpdatedAt = time.Now()
				if err := v.SetSharedConfig(shared); err != nil {
					return err
				}
				fmt.Printf("Rule %q disabled.\n", name)
				return nil
			}
		}
		return fmt.Errorf("rule %q not found", name)
	},
}

var rulesInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize with default rules",
	Long: `Add the default set of unified rules to your vault.

Default rules include:
  - no-model-in-commit: Don't mention AI models in commits
  - no-ai-attribution: Don't add "generated by AI" comments
  - consistent-style: Follow existing code style
  - minimal-changes: Make focused, minimal changes
  - no-secrets-in-code: Never hardcode secrets`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		force, _ := cmd.Flags().GetBool("force")
		shared := v.SharedConfig()

		if len(shared.Rules) > 0 && !force {
			return fmt.Errorf("rules already exist, use --force to add defaults anyway")
		}

		defaults := agent.DefaultRules()
		now := time.Now()

		// Add defaults that don't already exist
		existingNames := make(map[string]bool)
		for _, r := range shared.Rules {
			existingNames[r.Name] = true
		}

		added := 0
		for _, def := range defaults {
			if !existingNames[def.Name] {
				def.CreatedAt = now
				def.UpdatedAt = now
				shared.Rules = append(shared.Rules, def)
				fmt.Printf("  Added: %s\n", def.Name)
				added++
			}
		}

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("\nAdded %d default rule(s).\n", added)
		return nil
	},
}

var rulesExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export rules as markdown for use in instruction files",
	Long: `Export all enabled rules as formatted markdown.
This can be included in AGENTS.md, CLAUDE.md, or other instruction files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		shared := v.SharedConfig()

		// Sort by priority
		rules := make([]agent.UnifiedRule, len(shared.Rules))
		copy(rules, shared.Rules)
		sort.Slice(rules, func(i, j int) bool {
			return rules[i].Priority < rules[j].Priority
		})

		fmt.Println("## Rules")
		fmt.Println()
		fmt.Println("The following rules apply to all AI agents working on this project:")
		fmt.Println()

		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			fmt.Printf("### %s\n", r.Description)
			fmt.Printf("%s\n\n", r.Content)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(rulesCmd)
	rulesCmd.AddCommand(rulesListCmd)
	rulesCmd.AddCommand(rulesShowCmd)
	rulesCmd.AddCommand(rulesAddCmd)
	rulesCmd.AddCommand(rulesEditCmd)
	rulesCmd.AddCommand(rulesRemoveCmd)
	rulesCmd.AddCommand(rulesEnableCmd)
	rulesCmd.AddCommand(rulesDisableCmd)
	rulesCmd.AddCommand(rulesInitCmd)
	rulesCmd.AddCommand(rulesExportCmd)

	rulesListCmd.Flags().Bool("all", false, "show disabled rules too")
	rulesListCmd.Flags().Bool("json", false, "output as JSON")

	rulesAddCmd.Flags().String("description", "", "human-readable description")
	rulesAddCmd.Flags().String("content", "", "the rule text (required)")
	rulesAddCmd.Flags().String("category", "general", "category: commit, coding, behavior, security")
	rulesAddCmd.Flags().Int("priority", 50, "priority (lower = applied first)")

	rulesEditCmd.Flags().String("description", "", "human-readable description")
	rulesEditCmd.Flags().String("content", "", "the rule text")
	rulesEditCmd.Flags().String("category", "", "category")
	rulesEditCmd.Flags().Int("priority", 0, "priority")
	rulesEditCmd.Flags().Bool("enabled", true, "enable/disable the rule")

	rulesInitCmd.Flags().Bool("force", false, "add defaults even if rules exist")
}
