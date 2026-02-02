package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [name]",
	Short: "Show the effective configuration for an agent",
	Long: `Display the full resolved configuration for an agent, including
shared settings (system prompt, MCP servers). Use --env to output as
shell-exportable environment variables.

Example:
  agentvault run my-claude
  eval $(agentvault run my-claude --env)`,
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

		shared := v.SharedConfig()
		asEnv, _ := cmd.Flags().GetBool("env")

		if asEnv {
			fmt.Printf("export AGENT_NAME=%q\n", a.Name)
			fmt.Printf("export AGENT_PROVIDER=%q\n", a.Provider)
			fmt.Printf("export AGENT_MODEL=%q\n", a.Model)
			if a.APIKey != "" {
				fmt.Printf("export AGENT_API_KEY=%q\n", a.APIKey)
			}
			if a.BaseURL != "" {
				fmt.Printf("export AGENT_BASE_URL=%q\n", a.BaseURL)
			}
			prompt := a.EffectiveSystemPrompt(shared)
			if prompt != "" {
				fmt.Printf("export AGENT_SYSTEM_PROMPT=%q\n", prompt)
			}
			return nil
		}

		type output struct {
			Name         string `json:"name"`
			Provider     string `json:"provider"`
			Model        string `json:"model"`
			BaseURL      string `json:"base_url,omitempty"`
			SystemPrompt string `json:"system_prompt,omitempty"`
			TaskDesc     string `json:"task_description,omitempty"`
		}
		out := output{
			Name:         a.Name,
			Provider:     string(a.Provider),
			Model:        a.Model,
			BaseURL:      a.BaseURL,
			SystemPrompt: a.EffectiveSystemPrompt(shared),
			TaskDesc:     a.TaskDesc,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().Bool("env", false, "output as shell-exportable environment variables")
}
