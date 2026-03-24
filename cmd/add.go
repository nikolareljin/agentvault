package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new agent to the vault",
	Long: `Add a new agent configuration. Provide the name as an argument
and use flags for the remaining fields.

Example:
  agentvault add my-claude --provider claude --model claude-3-opus --api-key sk-...`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		apiKey, _ := cmd.Flags().GetString("api-key")
		baseURL, _ := cmd.Flags().GetString("base-url")
		backend, _ := cmd.Flags().GetString("backend")
		systemPrompt, _ := cmd.Flags().GetString("system-prompt")
		taskDesc, _ := cmd.Flags().GetString("task-desc")
		tagsStr, _ := cmd.Flags().GetString("tags")

		var tags []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}

		now := time.Now()
		normalizedBackend := strings.TrimSpace(backend)
		if agent.Provider(provider) == agent.ProviderClaude {
			normalizedBackend = strings.ToLower(normalizedBackend)
		}
		a := agent.Agent{
			Name:         args[0],
			Provider:     agent.Provider(provider),
			Model:        model,
			Backend:      normalizedBackend,
			APIKey:       apiKey,
			BaseURL:      baseURL,
			SystemPrompt: systemPrompt,
			TaskDesc:     taskDesc,
			Tags:         tags,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		applyRouteConfigFlags(cmd, &a.Route)
		if err := a.Validate(); err != nil {
			return err
		}
		if err := v.Add(a); err != nil {
			return err
		}
		fmt.Printf("Agent %q added.\n", a.Name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
	addCmd.Flags().StringP("provider", "p", "", "provider (claude, gemini, codex, ollama, openai, custom)")
	addCmd.Flags().StringP("model", "m", "", "model name")
	addCmd.Flags().String("backend", "", "backend (for claude: anthropic|ollama|bedrock)")
	addCmd.Flags().StringP("api-key", "k", "", "API key")
	addCmd.Flags().String("base-url", "", "custom base URL")
	addCmd.Flags().String("system-prompt", "", "system prompt")
	addCmd.Flags().String("task-desc", "", "task description")
	addCmd.Flags().String("tags", "", "comma-separated tags")
	registerRouteConfigFlags(addCmd.Flags())
	_ = addCmd.MarkFlagRequired("provider")
}
