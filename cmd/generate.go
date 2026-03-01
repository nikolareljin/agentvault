package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate configuration files for agents",
	Long: `Generate provider-specific configuration files from vault settings.

This command creates configuration files that agents can use directly:
  - Claude: ~/.claude/settings.json, keybindings
  - Codex: ~/.codex/config.toml, rules
  - Environment: .env files with API keys and settings
  - MCP: claude_desktop_config.json for MCP servers

Examples:
  agentvault generate claude    # Generate Claude config files
  agentvault generate codex     # Generate Codex config files
  agentvault generate env       # Generate .env file
  agentvault generate mcp       # Generate MCP server config
  agentvault generate all       # Generate all configurations`,
}

var generateClaudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Generate Claude Code configuration files",
	Long: `Generate Claude Code configuration files in ~/.claude/

Creates or updates:
  - settings.json (plugins, preferences)
  - keybindings.json (custom keybindings)

Use --dry-run to preview changes without writing.`,
	RunE: runGenerateClaude,
}

var generateCodexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Generate Codex CLI configuration files",
	Long: `Generate Codex CLI configuration files in ~/.codex/

Creates or updates:
  - config.toml (trusted projects)
  - rules/*.md (instruction rules)

Use --dry-run to preview changes without writing.`,
	RunE: runGenerateCodex,
}

var generateEnvCmd = &cobra.Command{
	Use:   "env [output-file]",
	Short: "Generate environment file with agent configurations",
	Long: `Generate a .env file containing agent configurations.

By default writes to .env in current directory.
Use --format to choose between dotenv and shell formats.

Examples:
  agentvault generate env                  # Write .env in current dir
  agentvault generate env .env.agents      # Write to specific file
  agentvault generate env --format shell   # Output as shell exports
  agentvault generate env --no-keys        # Exclude API keys`,
	RunE: runGenerateEnv,
}

var generateMCPCmd = &cobra.Command{
	Use:   "mcp [output-file]",
	Short: "Generate MCP server configuration",
	Long: `Generate a claude_desktop_config.json file for MCP servers.

This file can be used with Claude Desktop or other MCP-aware clients.
Includes all MCP servers from both shared config and individual agents.

Examples:
  agentvault generate mcp                         # Write to default location
  agentvault generate mcp ~/config/mcp.json       # Write to specific file
  agentvault generate mcp --agent my-agent        # Only include specific agent's MCPs`,
	RunE: runGenerateMCP,
}

var generateAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Generate all configuration files",
	RunE:  runGenerateAll,
}

func init() {
	rootCmd.AddCommand(generateCmd)
	generateCmd.AddCommand(generateClaudeCmd)
	generateCmd.AddCommand(generateCodexCmd)
	generateCmd.AddCommand(generateEnvCmd)
	generateCmd.AddCommand(generateMCPCmd)
	generateCmd.AddCommand(generateAllCmd)

	generateClaudeCmd.Flags().Bool("dry-run", false, "preview changes without writing")
	generateClaudeCmd.Flags().Bool("merge", true, "merge with existing config instead of overwriting")

	generateCodexCmd.Flags().Bool("dry-run", false, "preview changes without writing")
	generateCodexCmd.Flags().Bool("merge", true, "merge with existing config instead of overwriting")
	generateCodexCmd.Flags().String("project", "", "add current directory as trusted project")

	generateEnvCmd.Flags().String("format", "dotenv", "output format: dotenv, shell, json")
	generateEnvCmd.Flags().Bool("no-keys", false, "exclude API keys from output")
	generateEnvCmd.Flags().String("agent", "", "generate env for specific agent only")

	generateMCPCmd.Flags().String("agent", "", "include only specific agent's MCP servers")
	generateMCPCmd.Flags().Bool("shared-only", false, "include only shared MCP servers")
}

func runGenerateClaude(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	merge, _ := cmd.Flags().GetBool("merge")

	pc := v.ProviderConfigs()
	if pc.Claude == nil {
		fmt.Println("No Claude configuration stored in vault.")
		fmt.Println("Use 'agentvault setup pull --claude' to import your current config first.")
		return nil
	}

	home, _ := os.UserHomeDir()
	claudeDir := filepath.Join(home, ".claude")

	if dryRun {
		fmt.Println("Dry run - would generate:")
		fmt.Printf("  %s/settings.json\n", claudeDir)
		if len(pc.Claude.Keybindings) > 0 {
			fmt.Printf("  %s/keybindings.json\n", claudeDir)
		}
		data, _ := json.MarshalIndent(pc.Claude, "", "  ")
		fmt.Println("\nSettings content:")
		fmt.Println(string(data))
		return nil
	}

	// Load existing config if merging
	var existingConfig *agent.ClaudeConfig
	if merge {
		existingConfig, _ = agent.LoadClaudeConfig()
	}

	// Merge configurations
	finalConfig := cloneClaudeConfig(pc.Claude)
	if existingConfig != nil {
		// Merge plugins (vault overrides existing)
		for name, enabled := range existingConfig.EnabledPlugins {
			if _, ok := finalConfig.EnabledPlugins[name]; !ok {
				if finalConfig.EnabledPlugins == nil {
					finalConfig.EnabledPlugins = make(map[string]bool)
				}
				finalConfig.EnabledPlugins[name] = enabled
			}
		}
		// Merge custom settings
		for k, v := range existingConfig.CustomSettings {
			if _, ok := finalConfig.CustomSettings[k]; !ok {
				if finalConfig.CustomSettings == nil {
					finalConfig.CustomSettings = make(map[string]any)
				}
				finalConfig.CustomSettings[k] = v
			}
		}
	}

	if err := agent.SaveClaudeConfig(finalConfig); err != nil {
		return fmt.Errorf("saving Claude config: %w", err)
	}

	fmt.Printf("Generated Claude configuration in %s\n", claudeDir)
	if len(finalConfig.EnabledPlugins) > 0 {
		fmt.Printf("  Plugins: %d\n", len(finalConfig.EnabledPlugins))
	}
	return nil
}

func runGenerateCodex(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	merge, _ := cmd.Flags().GetBool("merge")
	addProject, _ := cmd.Flags().GetString("project")

	pc := v.ProviderConfigs()
	if pc.Codex == nil {
		pc.Codex = &agent.CodexConfig{
			TrustedProjects: make(map[string]string),
			Rules:           make(map[string]string),
		}
	}

	// Add current project if specified
	if addProject != "" {
		absPath, err := filepath.Abs(addProject)
		if err != nil {
			return fmt.Errorf("resolving project path: %w", err)
		}
		pc.Codex.TrustedProjects[absPath] = "trusted"
		// Save back to vault
		if err := v.SetCodexConfig(pc.Codex); err != nil {
			return fmt.Errorf("saving codex config: %w", err)
		}
	}

	home, _ := os.UserHomeDir()
	codexDir := filepath.Join(home, ".codex")

	if dryRun {
		fmt.Println("Dry run - would generate:")
		fmt.Printf("  %s/config.toml\n", codexDir)
		if len(pc.Codex.Rules) > 0 {
			for name := range pc.Codex.Rules {
				fmt.Printf("  %s/rules/%s.md\n", codexDir, name)
			}
		}
		return nil
	}

	// Load existing config if merging
	var existingConfig *agent.CodexConfig
	if merge {
		existingConfig, _ = agent.LoadCodexConfig()
	}

	// Merge configurations
	finalConfig := pc.Codex
	if existingConfig != nil {
		for path, level := range existingConfig.TrustedProjects {
			if _, ok := finalConfig.TrustedProjects[path]; !ok {
				finalConfig.TrustedProjects[path] = level
			}
		}
		for name, content := range existingConfig.Rules {
			if _, ok := finalConfig.Rules[name]; !ok {
				finalConfig.Rules[name] = content
			}
		}
	}

	if err := agent.SaveCodexConfig(finalConfig); err != nil {
		return fmt.Errorf("saving Codex config: %w", err)
	}

	fmt.Printf("Generated Codex configuration in %s\n", codexDir)
	if len(finalConfig.TrustedProjects) > 0 {
		fmt.Printf("  Trusted projects: %d\n", len(finalConfig.TrustedProjects))
	}
	if len(finalConfig.Rules) > 0 {
		fmt.Printf("  Rules: %d\n", len(finalConfig.Rules))
	}
	return nil
}

func runGenerateEnv(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	format, _ := cmd.Flags().GetString("format")
	noKeys, _ := cmd.Flags().GetBool("no-keys")
	agentFilter, _ := cmd.Flags().GetString("agent")

	outputFile := ".env"
	if len(args) > 0 {
		outputFile = args[0]
	}

	agents := v.List()
	if agentFilter != "" {
		filtered := []agent.Agent{}
		for _, a := range agents {
			if a.Name == agentFilter {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("agent %q not found", agentFilter)
		}
		agents = filtered
	}

	var output strings.Builder

	switch format {
	case "shell":
		output.WriteString("# Generated by agentvault\n")
		output.WriteString(fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		for _, a := range agents {
			prefix := envPrefixForAgent(a)
			output.WriteString(fmt.Sprintf("# Agent: %s (%s)\n", a.Name, a.Provider))
			if a.Model != "" {
				output.WriteString(fmt.Sprintf("export %s_MODEL=\"%s\"\n", prefix, a.Model))
			}
			if !noKeys && a.APIKey != "" {
				output.WriteString(fmt.Sprintf("export %s_API_KEY=\"%s\"\n", prefix, a.APIKey))
			}
			if a.BaseURL != "" {
				output.WriteString(fmt.Sprintf("export %s_BASE_URL=\"%s\"\n", prefix, a.BaseURL))
			}
			output.WriteString("\n")
		}

	case "json":
		envMap := make(map[string]map[string]string)
		for _, a := range agents {
			agentEnv := make(map[string]string)
			if a.Model != "" {
				agentEnv["model"] = a.Model
			}
			if !noKeys && a.APIKey != "" {
				agentEnv["api_key"] = a.APIKey
			}
			if a.BaseURL != "" {
				agentEnv["base_url"] = a.BaseURL
			}
			envMap[a.Name] = agentEnv
		}
		data, _ := json.MarshalIndent(envMap, "", "  ")
		output.Write(data)

	default: // dotenv
		output.WriteString("# Generated by agentvault\n")
		output.WriteString(fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		for _, a := range agents {
			output.WriteString(fmt.Sprintf("# Agent: %s (%s)\n", a.Name, a.Provider))
			prefix := envPrefixForAgent(a)
			if a.Model != "" {
				output.WriteString(fmt.Sprintf("%s_MODEL=%s\n", prefix, a.Model))
			}
			if !noKeys && a.APIKey != "" {
				output.WriteString(fmt.Sprintf("%s_API_KEY=%s\n", prefix, a.APIKey))
			}
			if a.BaseURL != "" {
				output.WriteString(fmt.Sprintf("%s_BASE_URL=%s\n", prefix, a.BaseURL))
			}
			output.WriteString("\n")
		}
	}

	if outputFile == "-" {
		fmt.Print(output.String())
		return nil
	}

	if err := os.WriteFile(outputFile, []byte(output.String()), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", outputFile, err)
	}

	fmt.Printf("Generated %s (%d agents)\n", outputFile, len(agents))
	if noKeys {
		fmt.Println("  Note: API keys excluded (use without --no-keys to include)")
	}
	return nil
}

func cloneClaudeConfig(in *agent.ClaudeConfig) *agent.ClaudeConfig {
	if in == nil {
		return &agent.ClaudeConfig{}
	}
	out := &agent.ClaudeConfig{
		DefaultModel: in.DefaultModel,
		AutoApprove:  append([]string(nil), in.AutoApprove...),
	}
	if len(in.EnabledPlugins) > 0 {
		out.EnabledPlugins = make(map[string]bool, len(in.EnabledPlugins))
		for k, v := range in.EnabledPlugins {
			out.EnabledPlugins[k] = v
		}
	}
	if len(in.Keybindings) > 0 {
		out.Keybindings = make(map[string]string, len(in.Keybindings))
		for k, v := range in.Keybindings {
			out.Keybindings[k] = v
		}
	}
	if len(in.MCPServers) > 0 {
		out.MCPServers = make(map[string]any, len(in.MCPServers))
		for k, v := range in.MCPServers {
			out.MCPServers[k] = v
		}
	}
	if len(in.CustomSettings) > 0 {
		out.CustomSettings = make(map[string]any, len(in.CustomSettings))
		for k, v := range in.CustomSettings {
			out.CustomSettings[k] = v
		}
	}
	return out
}

func envPrefixForAgent(a agent.Agent) string {
	provider := strings.ToUpper(strings.ReplaceAll(string(a.Provider), "-", "_"))
	name := strings.ToUpper(a.Name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	cleanName := strings.Trim(b.String(), "_")
	if cleanName == "" {
		cleanName = "AGENT"
	}
	return provider + "_" + cleanName
}

func runGenerateMCP(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	agentFilter, _ := cmd.Flags().GetString("agent")
	sharedOnly, _ := cmd.Flags().GetBool("shared-only")

	// Collect all MCP servers
	servers := make(map[string]agent.MCPServer)

	// Add shared servers first
	shared := v.SharedConfig()
	for _, s := range shared.MCPServers {
		servers[s.Name] = s
	}

	// Add agent-specific servers (override shared)
	if !sharedOnly {
		agents := v.List()
		for _, a := range agents {
			if agentFilter != "" && a.Name != agentFilter {
				continue
			}
			for _, s := range a.MCPServers {
				servers[s.Name] = s
			}
		}
	}

	if len(servers) == 0 {
		fmt.Println("No MCP servers configured.")
		return nil
	}

	// Build claude_desktop_config.json format
	type MCPServerConfig struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}

	config := struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}{
		MCPServers: make(map[string]MCPServerConfig),
	}

	for name, s := range servers {
		config.MCPServers[name] = MCPServerConfig{
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding MCP config: %w", err)
	}

	// Determine output location
	outputFile := ""
	if len(args) > 0 {
		outputFile = args[0]
	} else {
		home, _ := os.UserHomeDir()
		// Default to Claude Desktop config location
		outputFile = filepath.Join(home, ".config", "claude", "claude_desktop_config.json")
	}

	if outputFile == "-" {
		fmt.Println(string(data))
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(outputFile), 0700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := os.WriteFile(outputFile, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", outputFile, err)
	}

	fmt.Printf("Generated MCP config: %s\n", outputFile)
	fmt.Printf("  Servers: %d\n", len(servers))
	for name := range servers {
		fmt.Printf("    - %s\n", name)
	}
	return nil
}

func runGenerateAll(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	pc := v.ProviderConfigs()
	generated := 0

	// Generate Claude config
	if pc.Claude != nil {
		if err := agent.SaveClaudeConfig(pc.Claude); err != nil {
			fmt.Printf("Warning: could not generate Claude config: %v\n", err)
		} else {
			home, _ := os.UserHomeDir()
			fmt.Printf("  Generated: ~/.claude/ config\n")
			generated++
			_ = home
		}
	}

	// Generate Codex config
	if pc.Codex != nil {
		if err := agent.SaveCodexConfig(pc.Codex); err != nil {
			fmt.Printf("Warning: could not generate Codex config: %v\n", err)
		} else {
			fmt.Printf("  Generated: ~/.codex/ config\n")
			generated++
		}
	}

	// Generate .env
	agents := v.List()
	if len(agents) > 0 {
		var envContent strings.Builder
		envContent.WriteString("# Generated by agentvault\n")
		envContent.WriteString(fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		for _, a := range agents {
			prefix := strings.ToUpper(string(a.Provider))
			if a.Model != "" {
				envContent.WriteString(fmt.Sprintf("%s_MODEL=%s\n", prefix, a.Model))
			}
			if a.APIKey != "" {
				envContent.WriteString(fmt.Sprintf("%s_API_KEY=%s\n", prefix, a.APIKey))
			}
			if a.BaseURL != "" {
				envContent.WriteString(fmt.Sprintf("%s_BASE_URL=%s\n", prefix, a.BaseURL))
			}
			envContent.WriteString("\n")
		}
		if err := os.WriteFile(".env.agents", []byte(envContent.String()), 0600); err != nil {
			fmt.Printf("Warning: could not generate .env.agents: %v\n", err)
		} else {
			fmt.Println("  Generated: .env.agents")
			generated++
		}
	}

	// Generate MCP config to the default location
	shared := v.SharedConfig()
	if len(shared.MCPServers) > 0 {
		if err := runGenerateMCP(cmd, nil); err != nil {
			fmt.Printf("Warning: could not generate MCP config: %v\n", err)
		} else {
			generated++
		}
	}

	if generated == 0 {
		fmt.Println("No configurations to generate. Use 'agentvault setup pull' first.")
	} else {
		fmt.Printf("\nGenerated %d configuration(s).\n", generated)
	}

	return nil
}
