package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage shared vault configuration",
	Long: `View and update the shared configuration that applies to all agents.
The shared system prompt is used by any agent that does not define its own.
Shared MCP servers are merged with agent-specific ones.`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the shared configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(shared)
	},
}

var configSetPromptCmd = &cobra.Command{
	Use:   "set-prompt [prompt]",
	Short: "Set the shared system prompt for all agents",
	Long: `Set a default system prompt that applies to every agent unless the agent
defines its own. This makes all agents behave consistently.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()
		shared.SystemPrompt = args[0]
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}
		fmt.Println("Shared system prompt updated.")
		return nil
	},
}

var configAddMCPCmd = &cobra.Command{
	Use:   "add-mcp [name]",
	Short: "Add a shared MCP server configuration",
	Long: `Add an MCP server that will be available to all agents.
Agent-specific MCP servers with the same name override shared ones.

Example:
  agentvault config add-mcp filesystem --command npx --args "-y,@anthropic/mcp-server-filesystem,/home"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()

		mcpCmd, _ := cmd.Flags().GetString("command")
		mcpArgs, _ := cmd.Flags().GetStringSlice("args")

		for _, s := range shared.MCPServers {
			if s.Name == args[0] {
				return fmt.Errorf("shared MCP server %q already exists", args[0])
			}
		}

		shared.MCPServers = append(shared.MCPServers, agent.MCPServer{
			Name:    args[0],
			Command: mcpCmd,
			Args:    mcpArgs,
		})
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}
		fmt.Printf("Shared MCP server %q added.\n", args[0])
		return nil
	},
}

var configRemoveMCPCmd = &cobra.Command{
	Use:   "remove-mcp [name]",
	Short: "Remove a shared MCP server configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()
		idx := -1
		for i, s := range shared.MCPServers {
			if s.Name == args[0] {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("shared MCP server %q not found", args[0])
		}
		shared.MCPServers = append(shared.MCPServers[:idx], shared.MCPServers[idx+1:]...)
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}
		fmt.Printf("Shared MCP server %q removed.\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetPromptCmd)
	configCmd.AddCommand(configAddMCPCmd)
	configCmd.AddCommand(configRemoveMCPCmd)

	configAddMCPCmd.Flags().String("command", "", "MCP server command")
	configAddMCPCmd.Flags().StringSlice("args", nil, "MCP server arguments")
	_ = configAddMCPCmd.MarkFlagRequired("command")
}
