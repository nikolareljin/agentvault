package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/textutil"
	"github.com/nikolareljin/agentvault/internal/vault"
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
The default output is Claude Desktop's config for this OS:
  macOS    ~/Library/Application Support/Claude/claude_desktop_config.json
  Windows  %APPDATA%\Claude\claude_desktop_config.json
  Linux    ~/.config/claude/claude_desktop_config.json
Only the mcpServers key of an existing file is replaced.
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
		fmt.Println(string(escapeC1JSON(data)))
		return nil
	}

	// Load existing config if merging
	var existingConfig *agent.ClaudeConfig
	if merge {
		cfg, loadErr := agent.LoadClaudeConfig()
		if loadErr != nil {
			if !errors.Is(loadErr, os.ErrNotExist) {
				return fmt.Errorf("loading existing Claude config for merge: %w", loadErr)
			}
		} else {
			existingConfig = cfg
		}
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
		// Merge keybindings
		for key, action := range existingConfig.Keybindings {
			if _, ok := finalConfig.Keybindings[key]; !ok {
				if finalConfig.Keybindings == nil {
					finalConfig.Keybindings = make(map[string]string)
				}
				finalConfig.Keybindings[key] = action
			}
		}
		// Merge dedicated Claude fields to keep --merge non-destructive.
		if finalConfig.DefaultModel == "" {
			finalConfig.DefaultModel = existingConfig.DefaultModel
		}
		if len(finalConfig.AutoApprove) == 0 && len(existingConfig.AutoApprove) > 0 {
			finalConfig.AutoApprove = append([]string(nil), existingConfig.AutoApprove...)
		}
		for name, cfg := range existingConfig.MCPServers {
			if _, ok := finalConfig.MCPServers[name]; !ok {
				if finalConfig.MCPServers == nil {
					finalConfig.MCPServers = make(map[string]any)
				}
				finalConfig.MCPServers[name] = cfg
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
		if addProject == "" {
			fmt.Println("No Codex configuration stored in vault.")
			fmt.Println("Use 'agentvault setup pull --codex' to import your current config first, or pass --project to add one trusted project.")
			return nil
		}
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
				fmt.Printf("  %s/rules/%s.md\n", codexDir, printable(name))
			}
		}
		return nil
	}

	// Load existing config if merging
	var existingConfig *agent.CodexConfig
	if merge {
		cfg, loadErr := agent.LoadCodexConfig()
		if loadErr != nil {
			if !errors.Is(loadErr, os.ErrNotExist) {
				return fmt.Errorf("loading existing Codex config for merge: %w", loadErr)
			}
		} else {
			existingConfig = cfg
		}
	}

	// Merge configurations
	finalConfig := cloneCodexConfig(pc.Codex)
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
			output.WriteString(fmt.Sprintf("# Agent: %s (%s)\n", printable(a.Name), printable(string(a.Provider))))
			if a.Model != "" {
				output.WriteString(shellExportLine(prefix+"_MODEL", a.Model))
			}
			if !noKeys && a.APIKey != "" {
				output.WriteString(shellExportLine(prefix+"_API_KEY", a.APIKey))
			}
			if a.BaseURL != "" {
				output.WriteString(shellExportLine(prefix+"_BASE_URL", a.BaseURL))
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
		output.Write(escapeC1JSON(data))

	default: // dotenv
		output.WriteString("# Generated by agentvault\n")
		output.WriteString(fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
		for _, a := range agents {
			output.WriteString(fmt.Sprintf("# Agent: %s (%s)\n", printable(a.Name), printable(string(a.Provider))))
			prefix := envPrefixForAgent(a)
			if a.Model != "" {
				output.WriteString(dotenvLine(prefix+"_MODEL", a.Model))
			}
			if !noKeys && a.APIKey != "" {
				output.WriteString(dotenvLine(prefix+"_API_KEY", a.APIKey))
			}
			if a.BaseURL != "" {
				output.WriteString(dotenvLine(prefix+"_BASE_URL", a.BaseURL))
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

func cloneCodexConfig(in *agent.CodexConfig) *agent.CodexConfig {
	if in == nil {
		return &agent.CodexConfig{
			TrustedProjects: make(map[string]string),
			Rules:           make(map[string]string),
			CustomSettings:  make(map[string]any),
		}
	}
	out := &agent.CodexConfig{
		DefaultModel:    in.DefaultModel,
		TrustedProjects: make(map[string]string, len(in.TrustedProjects)),
		Rules:           make(map[string]string, len(in.Rules)),
		CustomSettings:  make(map[string]any, len(in.CustomSettings)),
	}
	for k, v := range in.TrustedProjects {
		out.TrustedProjects[k] = v
	}
	for k, v := range in.Rules {
		out.Rules[k] = v
	}
	for k, v := range in.CustomSettings {
		out.CustomSettings[k] = v
	}
	return out
}

// envPrefixForAgent is PROVIDER_NAME with every character other than A-Z
// and 0-9 replaced by '_'. Both parts can come from a bundle and the
// result is a variable name in output that is eval'd or sourced, so a
// provider such as "claude;cmd;" must not reach it as is.
func envPrefixForAgent(a agent.Agent) string {
	provider := envNamePart(string(a.Provider))
	if provider == "" || provider[0] <= '9' {
		provider = "PROVIDER" + provider
	}
	name := envNamePart(a.Name)
	if name == "" {
		name = "AGENT"
	}
	return provider + "_" + name
}

func envNamePart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// shellExportLine writes export NAME='value' for output that is eval'd or
// sourced. Inside single quotes bash, dash and zsh read every byte
// literally, so $, backticks, backslashes, ! and newlines round-trip and
// run nothing; a single quote ends the quotes, is escaped and reopens
// them. A value with a control character other than newline, tab or the
// CR of a CRLF would reach the terminal raw: it is left out with a warning.
func shellExportLine(name, value string) string {
	if textutil.PrintableText(value) != value {
		fmt.Fprintf(os.Stderr, "warning: %s left out: its value has a control character\n", name)
		return fmt.Sprintf("# %s left out: its value has a control character\n", name)
	}
	return "export " + name + "='" + strings.ReplaceAll(value, "'", `'\''`) + "'\n"
}

// dotenvLine writes NAME='value'. Single quotes keep the value literal for
// docker compose and a shell that sources the file, so a model such as
// "$(cmd)" from a bundle does not run. python-dotenv expands ${VAR} even in
// single quotes and has no form for a single quote that a shell also reads.
// A value with a single quote, "${" or a control character is left out
// with a warning.
func dotenvLine(name, value string) string {
	if strings.Contains(value, "'") || strings.Contains(value, "${") || textutil.Printable(value) != value {
		fmt.Fprintf(os.Stderr, "warning: %s left out: its value has a quote, ${ or control character\n", name)
		return fmt.Sprintf("# %s left out: its value has a quote, ${ or control character\n", name)
	}
	return name + "='" + value + "'\n"
}

func runGenerateMCP(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	agentFilter, _ := cmd.Flags().GetString("agent")
	sharedOnly, _ := cmd.Flags().GetBool("shared-only")

	outputFile := ""
	if len(args) > 0 {
		outputFile = args[0]
	}
	return generateMCPConfig(v, outputFile, agentFilter, sharedOnly)
}

func generateMCPConfig(v *vault.Vault, outputFile string, agentFilter string, sharedOnly bool) error {
	// Collect all MCP servers.
	servers := make(map[string]agent.MCPServer)

	shared := v.SharedConfig()
	for _, s := range shared.MCPServers {
		servers[s.Name] = s
	}

	if !sharedOnly {
		for _, a := range v.List() {
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

	type MCPServerConfig struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}

	desktop := struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}{
		MCPServers: make(map[string]MCPServerConfig),
	}

	for name, s := range servers {
		desktop.MCPServers[name] = MCPServerConfig{
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
		}
	}

	data, err := json.MarshalIndent(desktop, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding MCP config: %w", err)
	}

	if outputFile == "" {
		if outputFile, err = config.ClaudeDesktopConfigPath(); err != nil {
			return err
		}
	}

	if outputFile == "-" {
		fmt.Println(string(escapeC1JSON(data)))
		return nil
	}

	if err := writeMCPConfigFile(outputFile, data); err != nil {
		return err
	}

	fmt.Printf("Generated MCP config: %s\n", outputFile)
	fmt.Printf("  Servers: %d\n", len(servers))
	for name := range servers {
		fmt.Printf("    - %s\n", printable(name))
	}
	return nil
}

// writeMCPConfigFile merges data into outputFile and writes it. A device or
// FIFO such as /dev/stdout is written in place: a rename would replace it,
// and reading it first could block.
func writeMCPConfigFile(outputFile string, data []byte) error {
	if fi, err := os.Stat(outputFile); err == nil && !fi.Mode().IsRegular() && !fi.IsDir() {
		if err := os.WriteFile(outputFile, data, 0600); err != nil {
			return fmt.Errorf("writing %s: %w", outputFile, err)
		}
		return nil
	}
	merged, err := mergeMCPServersIntoFile(outputFile, data)
	if err != nil {
		return err
	}
	return writeFileAtomicKeepMode(outputFile, merged)
}

// mergeMCPServersIntoFile merges the generated servers into the mcpServers
// key of an existing config file and keeps every other key: a server only
// the file has stays, a generated one wins. The default output is Claude
// Desktop's own config, which holds the app's other settings too.
func mergeMCPServersIntoFile(path string, generated []byte) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return generated, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	// Notepad saves UTF-8 with a BOM, which json.Unmarshal rejects.
	existing = bytes.TrimPrefix(existing, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(existing)) == 0 {
		return generated, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(existing, &doc); err != nil || doc == nil {
		// A literal null decodes to a nil map with no error.
		if err == nil {
			err = errors.New("top-level value is null")
		}
		return nil, fmt.Errorf("%s is not a JSON object; not overwriting it (pass another output file): %w", path, err)
	}
	var gen map[string]json.RawMessage
	if err := json.Unmarshal(generated, &gen); err != nil {
		return nil, err
	}
	// Rebuild the object in the file's key order, replacing mcpServers in
	// place. Values are copied as written, only re-indented, so strings and
	// numbers keep their exact text.
	type member struct {
		key   string
		value json.RawMessage
	}
	var members []member
	dec := json.NewDecoder(bytes.NewReader(existing))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		if key == "mcpServers" {
			value = mergeServerObjects(value, gen["mcpServers"])
		}
		dup := false
		for i := range members {
			if members[i].key == key {
				// A repeated key: the last value wins, as in json.Unmarshal.
				members[i].value, dup = value, true
			}
		}
		if !dup {
			members = append(members, member{key, value})
		}
	}
	if _, ok := doc["mcpServers"]; !ok {
		members = append(members, member{"mcpServers", gen["mcpServers"]})
	}
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, m := range members {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		// Marshal would turn < > & in a key into \u003c \u003e \u0026.
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(m.key); err != nil {
			return nil, err
		}
		buf.Truncate(buf.Len() - 1)
		buf.WriteString(": ")
		if err := json.Indent(&buf, m.value, "  ", "  "); err != nil {
			return nil, err
		}
	}
	buf.WriteString("\n}")
	return buf.Bytes(), nil
}

// mergeServerObjects is the file's server object with the generated servers
// set in it, in the file's order, new ones after. A file value that is not
// an object is replaced.
func mergeServerObjects(existing, generated json.RawMessage) json.RawMessage {
	var gen map[string]json.RawMessage
	if err := json.Unmarshal(generated, &gen); err != nil || gen == nil {
		return generated
	}
	dec := json.NewDecoder(bytes.NewReader(existing))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return generated
	}
	var buf bytes.Buffer
	buf.WriteString("{")
	seen := map[string]bool{}
	n := 0
	write := func(key string, v json.RawMessage) {
		if n > 0 {
			buf.WriteString(",")
		}
		n++
		// Encode, not Marshal: Marshal turns < > & into \u003c and so on.
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(key)
		buf.Truncate(buf.Len() - 1)
		buf.WriteString(":")
		buf.Write(v)
	}
	var order []string
	vals := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return generated
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return generated
		}
		key, _ := tok.(string)
		if _, dup := vals[key]; !dup {
			order = append(order, key)
		}
		vals[key] = v
	}
	for _, key := range order {
		v := vals[key]
		if g, ok := gen[key]; ok {
			v = g
		}
		seen[key] = true
		write(key, v)
	}
	keys := make([]string, 0, len(gen))
	for k := range gen {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		write(k, gen[k])
	}
	buf.WriteString("}")
	return buf.Bytes()
}

// writeFileAtomicKeepMode replaces path through a temp file and a rename, so
// a crash never leaves Claude Desktop a half-written config. A symlinked path
// is written through to its target, and an existing file keeps its mode.
func writeFileAtomicKeepMode(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resolving %s: %w", path, err)
	} else {
		// A dangling link: follow every hop and create the final target
		// rather than replace a link. A loop fails in EvalSymlinks above.
		for hops := 0; hops < 255; hops++ {
			link, lerr := os.Readlink(target)
			if lerr != nil {
				break
			}
			if !filepath.IsAbs(link) {
				link = filepath.Dir(target) + string(filepath.Separator) + link
			}
			// Resolve the parent as the OS does: filepath.Join would clean
			// "dirlink/../x" lexically, but ".." applies after dirlink.
			if i := strings.LastIndexByte(link, filepath.Separator); i > 0 {
				if parent, perr := filepath.EvalSymlinks(link[:i]); perr == nil {
					link = filepath.Join(parent, link[i+1:])
				}
			}
			target = link
		}
	}
	mode := os.FileMode(0600)
	existing, statErr := os.Stat(target)
	if statErr == nil {
		mode = existing.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := refuseDanglingDirLink(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".claude_desktop_config-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if statErr == nil {
		if err := keepOwner(tmp, existing); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// Persist the rename itself. Windows cannot open a directory for Sync.
	if runtime.GOOS != "windows" {
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	return nil
}

// refuseDanglingDirLink fails when the nearest existing ancestor of dir is a
// symlink to nothing. MkdirAll would fail there with a bare "file exists".
func refuseDanglingDirLink(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				if _, serr := os.Stat(p); serr != nil {
					return fmt.Errorf("%s is a symlink to a missing directory; create its target or pass another output file", p)
				}
			}
			return nil
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
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
			fmt.Printf("  Generated: ~/.claude/ config\n")
			generated++
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
		envContent.WriteString("# API keys are intentionally omitted in generate all output.\n\n")
		for _, a := range agents {
			prefix := envPrefixForAgent(a)
			envContent.WriteString(fmt.Sprintf("# Agent: %s (%s)\n", printable(a.Name), printable(string(a.Provider))))
			if a.Model != "" {
				envContent.WriteString(dotenvLine(prefix+"_MODEL", a.Model))
			}
			if a.BaseURL != "" {
				envContent.WriteString(dotenvLine(prefix+"_BASE_URL", a.BaseURL))
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
		if err := generateMCPConfig(v, "", "", false); err != nil {
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
