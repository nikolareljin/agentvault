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
			left := 0
			fmt.Print(countLeftOut(&left, shellExportLine("AGENT_NAME", a.Name)))
			fmt.Print(countLeftOut(&left, shellExportLine("AGENT_PROVIDER", string(a.Provider))))
			fmt.Print(countLeftOut(&left, shellExportLine("AGENT_MODEL", a.Model)))
			if a.APIKey != "" {
				fmt.Print(countLeftOut(&left, shellExportLine("AGENT_API_KEY", a.APIKey)))
			}
			if a.BaseURL != "" {
				fmt.Print(countLeftOut(&left, shellExportLine("AGENT_BASE_URL", a.BaseURL)))
			}
			prompt := a.EffectiveSystemPrompt(shared)
			if prompt != "" {
				fmt.Print(countLeftOut(&left, shellExportLine("AGENT_SYSTEM_PROMPT", prompt)))
			}
			return leftOutError(cmd, left)
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
			BaseURL:      maskURLSecrets(a.BaseURL),
			SystemPrompt: a.EffectiveSystemPrompt(shared),
			TaskDesc:     a.TaskDesc,
		}
		enc := json.NewEncoder(c1Writer(os.Stdout))
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().Bool("env", false, "output as shell-exportable environment variables")
}
