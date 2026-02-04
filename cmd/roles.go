package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var rolesCmd = &cobra.Command{
	Use:     "roles",
	Aliases: []string{"role"},
	Short:   "Manage roles/personas that can be applied to agents",
	Long: `Manage roles that define how agents should behave.

Roles combine a persona prompt with specific rules. For example:
  - "Lead Engineer": Focus on architecture, best practices, mentoring
  - "Designer": Focus on UX, accessibility, visual consistency
  - "Security Auditor": Focus on vulnerabilities, secure coding

Assign a role to an agent with: agentvault edit <agent> --role <role-name>`,
}

var rolesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all roles",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		jsonOutput, _ := cmd.Flags().GetBool("json")
		shared := v.SharedConfig()

		if len(shared.Roles) == 0 {
			fmt.Println("No roles configured. Use 'agentvault roles add' or 'roles init' to get started.")
			return nil
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(shared.Roles, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Println("Roles:")
		fmt.Println(strings.Repeat("─", 60))
		for _, r := range shared.Roles {
			fmt.Printf("\n  %s (%s)\n", r.Title, r.Name)
			fmt.Printf("    %s\n", r.Description)
			if len(r.Rules) > 0 {
				fmt.Printf("    Rules: %s\n", strings.Join(r.Rules, ", "))
			}
			if len(r.Tags) > 0 {
				fmt.Printf("    Tags: %s\n", strings.Join(r.Tags, ", "))
			}
		}
		fmt.Println()
		return nil
	},
}

var rolesShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show details of a specific role",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		shared := v.SharedConfig()
		role, ok := agent.GetRole(shared.Roles, args[0])
		if !ok {
			return fmt.Errorf("role %q not found", args[0])
		}

		fmt.Printf("Role: %s\n", role.Name)
		fmt.Printf("Title: %s\n", role.Title)
		fmt.Printf("Description: %s\n", role.Description)
		if len(role.Rules) > 0 {
			fmt.Printf("Rules: %s\n", strings.Join(role.Rules, ", "))
		}
		if len(role.Tags) > 0 {
			fmt.Printf("Tags: %s\n", strings.Join(role.Tags, ", "))
		}
		fmt.Printf("\nPrompt:\n%s\n", role.Prompt)
		return nil
	},
}

var rolesAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new role",
	Long: `Add a new role that can be assigned to agents.

Example:
  agentvault roles add devops \
    --title "DevOps Engineer" \
    --description "Focus on infrastructure, CI/CD, and operations" \
    --prompt "You are a DevOps Engineer. Focus on infrastructure as code, CI/CD pipelines, monitoring, and operational excellence." \
    --rules "no-secrets-in-code,consistent-style" \
    --tags "ops,infrastructure"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		title, _ := cmd.Flags().GetString("title")
		description, _ := cmd.Flags().GetString("description")
		prompt, _ := cmd.Flags().GetString("prompt")
		rulesStr, _ := cmd.Flags().GetString("rules")
		tagsStr, _ := cmd.Flags().GetString("tags")

		if prompt == "" {
			return fmt.Errorf("--prompt is required")
		}
		if title == "" {
			title = name
		}

		shared := v.SharedConfig()

		// Check if role already exists
		for _, r := range shared.Roles {
			if r.Name == name {
				return fmt.Errorf("role %q already exists, use 'roles edit' to modify", name)
			}
		}

		var rules, tags []string
		if rulesStr != "" {
			rules = strings.Split(rulesStr, ",")
			for i := range rules {
				rules[i] = strings.TrimSpace(rules[i])
			}
		}
		if tagsStr != "" {
			tags = strings.Split(tagsStr, ",")
			for i := range tags {
				tags[i] = strings.TrimSpace(tags[i])
			}
		}

		role := agent.Role{
			Name:        name,
			Title:       title,
			Description: description,
			Prompt:      prompt,
			Rules:       rules,
			Tags:        tags,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		shared.Roles = append(shared.Roles, role)
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Role %q added.\n", name)
		return nil
	},
}

var rolesEditCmd = &cobra.Command{
	Use:   "edit [name]",
	Short: "Edit an existing role",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		idx := -1
		for i, r := range shared.Roles {
			if r.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("role %q not found", name)
		}

		role := &shared.Roles[idx]

		if cmd.Flags().Changed("title") {
			role.Title, _ = cmd.Flags().GetString("title")
		}
		if cmd.Flags().Changed("description") {
			role.Description, _ = cmd.Flags().GetString("description")
		}
		if cmd.Flags().Changed("prompt") {
			role.Prompt, _ = cmd.Flags().GetString("prompt")
		}
		if cmd.Flags().Changed("rules") {
			rulesStr, _ := cmd.Flags().GetString("rules")
			if rulesStr != "" {
				rules := strings.Split(rulesStr, ",")
				for i := range rules {
					rules[i] = strings.TrimSpace(rules[i])
				}
				role.Rules = rules
			} else {
				role.Rules = nil
			}
		}
		if cmd.Flags().Changed("tags") {
			tagsStr, _ := cmd.Flags().GetString("tags")
			if tagsStr != "" {
				tags := strings.Split(tagsStr, ",")
				for i := range tags {
					tags[i] = strings.TrimSpace(tags[i])
				}
				role.Tags = tags
			} else {
				role.Tags = nil
			}
		}

		role.UpdatedAt = time.Now()

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Role %q updated.\n", name)
		return nil
	},
}

var rolesRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a role",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		shared := v.SharedConfig()

		idx := -1
		for i, r := range shared.Roles {
			if r.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("role %q not found", name)
		}

		shared.Roles = append(shared.Roles[:idx], shared.Roles[idx+1:]...)

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("Role %q removed.\n", name)
		return nil
	},
}

var rolesInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize with default roles",
	Long: `Add the default set of roles to your vault.

Default roles include:
  - lead-engineer: Senior technical leader
  - designer: UI/UX Designer
  - security-auditor: Security focused reviewer
  - code-reviewer: Quality focused reviewer`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		force, _ := cmd.Flags().GetBool("force")
		shared := v.SharedConfig()

		if len(shared.Roles) > 0 && !force {
			return fmt.Errorf("roles already exist, use --force to add defaults anyway")
		}

		defaults := agent.DefaultRoles()
		now := time.Now()

		// Add defaults that don't already exist
		existingNames := make(map[string]bool)
		for _, r := range shared.Roles {
			existingNames[r.Name] = true
		}

		added := 0
		for _, def := range defaults {
			if !existingNames[def.Name] {
				def.CreatedAt = now
				def.UpdatedAt = now
				shared.Roles = append(shared.Roles, def)
				fmt.Printf("  Added: %s (%s)\n", def.Name, def.Title)
				added++
			}
		}

		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}

		fmt.Printf("\nAdded %d default role(s).\n", added)
		return nil
	},
}

var rolesApplyCmd = &cobra.Command{
	Use:   "apply [role-name] [agent-name]",
	Short: "Apply a role to an agent",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		roleName := args[0]
		agentName := args[1]

		shared := v.SharedConfig()

		// Verify role exists
		_, ok := agent.GetRole(shared.Roles, roleName)
		if !ok {
			return fmt.Errorf("role %q not found", roleName)
		}

		// Get agent
		a, ok := v.Get(agentName)
		if !ok {
			return fmt.Errorf("agent %q not found", agentName)
		}

		a.Role = roleName
		a.UpdatedAt = time.Now()

		if err := v.Update(a); err != nil {
			return err
		}

		fmt.Printf("Applied role %q to agent %q.\n", roleName, agentName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rolesCmd)
	rolesCmd.AddCommand(rolesListCmd)
	rolesCmd.AddCommand(rolesShowCmd)
	rolesCmd.AddCommand(rolesAddCmd)
	rolesCmd.AddCommand(rolesEditCmd)
	rolesCmd.AddCommand(rolesRemoveCmd)
	rolesCmd.AddCommand(rolesInitCmd)
	rolesCmd.AddCommand(rolesApplyCmd)

	rolesListCmd.Flags().Bool("json", false, "output as JSON")

	rolesAddCmd.Flags().String("title", "", "display title")
	rolesAddCmd.Flags().String("description", "", "description of the role")
	rolesAddCmd.Flags().String("prompt", "", "system prompt for this role (required)")
	rolesAddCmd.Flags().String("rules", "", "comma-separated rule names to apply")
	rolesAddCmd.Flags().String("tags", "", "comma-separated tags")

	rolesEditCmd.Flags().String("title", "", "display title")
	rolesEditCmd.Flags().String("description", "", "description")
	rolesEditCmd.Flags().String("prompt", "", "system prompt")
	rolesEditCmd.Flags().String("rules", "", "comma-separated rule names")
	rolesEditCmd.Flags().String("tags", "", "comma-separated tags")

	rolesInitCmd.Flags().Bool("force", false, "add defaults even if roles exist")
}
