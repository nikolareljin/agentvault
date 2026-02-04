package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var editCmd = &cobra.Command{
	Use:   "edit [name]",
	Short: "Edit an existing agent",
	Long: `Update fields of an existing agent. Only provided flags are changed;
omitted flags leave the current value intact.

Example:
  agentvault edit my-claude --model claude-4-opus --api-key sk-new-key`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		a, ok := v.Get(args[0])
		if !ok {
			return fmt.Errorf("agent %q not found", args[0])
		}

		if cmd.Flags().Changed("provider") {
			p, _ := cmd.Flags().GetString("provider")
			a.Provider = agent.Provider(p)
		}
		if cmd.Flags().Changed("model") {
			a.Model, _ = cmd.Flags().GetString("model")
		}
		if cmd.Flags().Changed("api-key") {
			a.APIKey, _ = cmd.Flags().GetString("api-key")
		}
		if cmd.Flags().Changed("base-url") {
			a.BaseURL, _ = cmd.Flags().GetString("base-url")
		}
		if cmd.Flags().Changed("system-prompt") {
			a.SystemPrompt, _ = cmd.Flags().GetString("system-prompt")
		}
		if cmd.Flags().Changed("task-desc") {
			a.TaskDesc, _ = cmd.Flags().GetString("task-desc")
		}
		if cmd.Flags().Changed("tags") {
			tagsStr, _ := cmd.Flags().GetString("tags")
			var tags []string
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
			a.Tags = tags
		}
		if cmd.Flags().Changed("role") {
			a.Role, _ = cmd.Flags().GetString("role")
		}
		if cmd.Flags().Changed("disable-rules") {
			rulesStr, _ := cmd.Flags().GetString("disable-rules")
			var rules []string
			for _, r := range strings.Split(rulesStr, ",") {
				r = strings.TrimSpace(r)
				if r != "" {
					rules = append(rules, r)
				}
			}
			a.DisabledRules = rules
		}

		if err := a.Validate(); err != nil {
			return err
		}
		a.UpdatedAt = time.Now()
		if err := v.Update(a); err != nil {
			return err
		}
		fmt.Printf("Agent %q updated.\n", a.Name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(editCmd)
	editCmd.Flags().StringP("provider", "p", "", "provider")
	editCmd.Flags().StringP("model", "m", "", "model name")
	editCmd.Flags().StringP("api-key", "k", "", "API key")
	editCmd.Flags().String("base-url", "", "custom base URL")
	editCmd.Flags().String("system-prompt", "", "system prompt")
	editCmd.Flags().String("task-desc", "", "task description")
	editCmd.Flags().String("tags", "", "comma-separated tags")
	editCmd.Flags().String("role", "", "role to apply (e.g., lead-engineer)")
	editCmd.Flags().String("disable-rules", "", "comma-separated rules to disable for this agent")
}
