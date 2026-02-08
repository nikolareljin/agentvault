package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
	"github.com/spf13/cobra"
)

// SetupBundle represents a complete portable agent configuration bundle.
// This is the primary mechanism for replicating an entire agent setup
// across machines. It captures everything needed to recreate the environment:
// agents, rules, roles, instructions, provider configs, and an installation guide.
type SetupBundle struct {
	Version         string               `json:"version"`
	CreatedAt       time.Time            `json:"created_at"`
	SourceMachine   string               `json:"source_machine"`
	SourceOS        string               `json:"source_os"`
	Agents          []agent.Agent        `json:"agents"`
	SharedConfig    agent.SharedConfig   `json:"shared_config"`
	ProviderConfigs agent.ProviderConfig `json:"provider_configs"`
	DetectedAgents  []DetectedAgent      `json:"detected_agents,omitempty"`
	InstallGuide    InstallGuide         `json:"install_guide"`
}

// InstallGuide contains instructions for setting up agents on a new machine.
type InstallGuide struct {
	Requirements []Requirement `json:"requirements"`
	Steps        []SetupStep   `json:"steps"`
	PostSetup    []string      `json:"post_setup"`
}

// Requirement represents a software requirement.
type Requirement struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InstallCmd  string `json:"install_cmd"`
	Required    bool   `json:"required"`
}

// SetupStep represents a setup instruction.
type SetupStep struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Commands    []string `json:"commands,omitempty"`
	Manual      string   `json:"manual,omitempty"`
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Unified agent setup management",
	Long: `Export and import complete agent configurations including:
  - All agents with their settings
  - Instruction files (AGENTS.md, CLAUDE.md, etc.)
  - Provider-specific configurations (plugins, rules, trusted projects)
  - Installation guides for replicating the setup

This is the primary way to synchronize your agent configuration across machines.`,
}

var setupExportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export complete agent setup to a portable bundle",
	Long: `Create a portable bundle containing all agent configurations and instructions.

The bundle includes:
  - All agents (names, providers, models, API keys if --include-keys)
  - Instruction files stored in vault
  - Claude settings (plugins, keybindings)
  - Codex settings (trusted projects, rules)
  - Ollama configuration
  - Installation guide for the target machine

Examples:
  agentvault setup export my-setup.json           # Export to JSON
  agentvault setup export my-setup.bundle         # Export encrypted
  agentvault setup export setup.json --include-keys  # Include API keys
  agentvault setup export setup.json --detect     # Include detected agent info`,
	Args: cobra.ExactArgs(1),
	RunE: runSetupExport,
}

var setupImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import agent setup from a bundle",
	Long: `Import agent configurations from a setup bundle.

By default, existing agents are skipped. Use --merge to update settings.

Examples:
  agentvault setup import my-setup.json
  agentvault setup import my-setup.bundle    # Encrypted bundle
  agentvault setup import setup.json --merge # Update existing agents`,
	Args: cobra.ExactArgs(1),
	RunE: runSetupImport,
}

var setupShowCmd = &cobra.Command{
	Use:   "show [file]",
	Short: "Show contents of a setup bundle without importing",
	Args:  cobra.ExactArgs(1),
	RunE:  runSetupShow,
}

var setupApplyCmd = &cobra.Command{
	Use:   "apply [directory]",
	Short: "Apply stored instructions to a project directory",
	Long: `Push all instruction files from the vault to a project directory
and optionally generate provider-specific configuration files.

This is a convenience command combining:
  - agentvault instructions push
  - agentvault generate (if --generate flag is set)

Examples:
  agentvault setup apply .                 # Push instructions to current dir
  agentvault setup apply /path/to/project  # Push to specific directory
  agentvault setup apply . --generate      # Also generate .env and configs`,
	Args: cobra.ExactArgs(1),
	RunE: runSetupApply,
}

var setupPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull provider configurations from system into vault",
	Long: `Read current Claude, Codex, and Ollama configurations from the system
and store them in the vault for export.

This captures:
  - Claude: settings.json, keybindings, plugins
  - Codex: config.toml, trusted projects, rules
  - Ollama: models list, base URL

Examples:
  agentvault setup pull           # Pull all provider configs
  agentvault setup pull --claude  # Pull only Claude config
  agentvault setup pull --codex   # Pull only Codex config`,
	RunE: runSetupPull,
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.AddCommand(setupExportCmd)
	setupCmd.AddCommand(setupImportCmd)
	setupCmd.AddCommand(setupShowCmd)
	setupCmd.AddCommand(setupApplyCmd)
	setupCmd.AddCommand(setupPullCmd)

	setupExportCmd.Flags().Bool("include-keys", false, "include API keys in export (use with caution)")
	setupExportCmd.Flags().Bool("detect", false, "include detected agent information")
	setupExportCmd.Flags().Bool("encrypted", false, "encrypt the bundle (prompted for password)")
	setupExportCmd.Flags().Bool("plain", false, "force plaintext JSON output")

	setupImportCmd.Flags().Bool("merge", false, "merge with existing agents instead of skipping")
	setupImportCmd.Flags().Bool("apply-provider-configs", false, "apply provider configs to system after import")

	setupApplyCmd.Flags().Bool("generate", false, "also generate .env and provider config files")
	setupApplyCmd.Flags().StringSlice("only", nil, "apply only specific instructions (e.g., --only agents,claude)")

	setupPullCmd.Flags().Bool("claude", false, "pull only Claude config")
	setupPullCmd.Flags().Bool("codex", false, "pull only Codex config")
	setupPullCmd.Flags().Bool("ollama", false, "pull only Ollama config")
}

func runSetupExport(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	includeKeys, _ := cmd.Flags().GetBool("include-keys")
	detect, _ := cmd.Flags().GetBool("detect")
	encrypted, _ := cmd.Flags().GetBool("encrypted")
	plain, _ := cmd.Flags().GetBool("plain")

	// Determine output format from extension
	outputFile := args[0]
	if strings.HasSuffix(outputFile, ".bundle") && !plain {
		encrypted = true
	}

	hostname, _ := os.Hostname()
	bundle := SetupBundle{
		Version:         "1.0",
		CreatedAt:       time.Now(),
		SourceMachine:   hostname,
		SourceOS:        runtime.GOOS + "/" + runtime.GOARCH,
		Agents:          v.List(),
		SharedConfig:    v.SharedConfig(),
		ProviderConfigs: v.ProviderConfigs(),
	}

	// Optionally strip API keys
	if !includeKeys {
		for i := range bundle.Agents {
			bundle.Agents[i].APIKey = ""
		}
	}

	// Detect installed agents if requested
	if detect {
		bundle.DetectedAgents = detectAllAgents()
	}

	// Generate installation guide
	bundle.InstallGuide = generateInstallGuide(bundle)

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding bundle: %w", err)
	}

	if encrypted {
		password, err := readPassword("Bundle password: ")
		if err != nil {
			return err
		}
		confirm, err := readPassword("Confirm password: ")
		if err != nil {
			return err
		}
		if password != confirm {
			return fmt.Errorf("passwords do not match")
		}
		if len(password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}

		salt, err := crypto.GenerateSalt()
		if err != nil {
			return err
		}
		key, err := crypto.DeriveKey(password, salt)
		if err != nil {
			return err
		}
		ciphertext, err := crypto.Encrypt(data, key)
		if err != nil {
			return err
		}
		data = append(salt, ciphertext...)
	}

	if err := os.WriteFile(outputFile, data, 0600); err != nil {
		return fmt.Errorf("writing bundle: %w", err)
	}

	fmt.Printf("Setup bundle exported to %s\n", outputFile)
	fmt.Printf("  Agents: %d\n", len(bundle.Agents))
	fmt.Printf("  Instructions: %d\n", len(bundle.SharedConfig.Instructions))
	if includeKeys {
		fmt.Println("  Warning: API keys are included!")
	}
	if encrypted {
		fmt.Println("  Encrypted: yes")
	}
	return nil
}

func runSetupImport(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	merge, _ := cmd.Flags().GetBool("merge")
	applyConfigs, _ := cmd.Flags().GetBool("apply-provider-configs")

	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}

	// Try to detect if encrypted
	var bundle SetupBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		// Might be encrypted
		if len(data) < crypto.SaltLen {
			return fmt.Errorf("invalid bundle format")
		}
		password, err := readPassword("Bundle password: ")
		if err != nil {
			return err
		}
		salt := data[:crypto.SaltLen]
		ciphertext := data[crypto.SaltLen:]
		key, err := crypto.DeriveKey(password, salt)
		if err != nil {
			return err
		}
		plaintext, err := crypto.Decrypt(ciphertext, key)
		if err != nil {
			return fmt.Errorf("decryption failed (wrong password?)")
		}
		if err := json.Unmarshal(plaintext, &bundle); err != nil {
			return fmt.Errorf("decoding bundle: %w", err)
		}
	}

	fmt.Printf("Importing setup from %s (created %s)\n",
		bundle.SourceMachine, bundle.CreatedAt.Format("2006-01-02 15:04"))

	// Import agents
	added, updated, skipped := 0, 0, 0
	for _, a := range bundle.Agents {
		existing, exists := v.Get(a.Name)
		if exists {
			if merge {
				// Preserve API key if not in bundle
				if a.APIKey == "" {
					a.APIKey = existing.APIKey
				}
				a.UpdatedAt = time.Now()
				if err := v.Update(a); err != nil {
					return fmt.Errorf("updating agent %s: %w", a.Name, err)
				}
				fmt.Printf("  Updated: %s\n", a.Name)
				updated++
			} else {
				fmt.Printf("  Skipped: %s (exists)\n", a.Name)
				skipped++
			}
		} else {
			a.CreatedAt = time.Now()
			a.UpdatedAt = time.Now()
			if err := v.Add(a); err != nil {
				return fmt.Errorf("adding agent %s: %w", a.Name, err)
			}
			fmt.Printf("  Added: %s\n", a.Name)
			added++
		}
	}

	// Import shared config
	sc := v.SharedConfig()
	if bundle.SharedConfig.SystemPrompt != "" && sc.SystemPrompt == "" {
		sc.SystemPrompt = bundle.SharedConfig.SystemPrompt
		fmt.Println("  Imported: shared system prompt")
	}

	// Merge MCP servers
	seenMCP := make(map[string]struct{})
	for _, s := range sc.MCPServers {
		seenMCP[s.Name] = struct{}{}
	}
	for _, s := range bundle.SharedConfig.MCPServers {
		if _, ok := seenMCP[s.Name]; !ok {
			sc.MCPServers = append(sc.MCPServers, s)
			fmt.Printf("  Imported: MCP server %s\n", s.Name)
		}
	}

	// Merge instructions
	seenInst := make(map[string]struct{})
	for _, inst := range sc.Instructions {
		seenInst[inst.Name] = struct{}{}
	}
	for _, inst := range bundle.SharedConfig.Instructions {
		if _, ok := seenInst[inst.Name]; !ok {
			sc.Instructions = append(sc.Instructions, inst)
			fmt.Printf("  Imported: instruction %s\n", inst.Name)
		}
	}
	v.SetSharedConfig(sc)

	// Import provider configs
	pc := v.ProviderConfigs()
	if bundle.ProviderConfigs.Claude != nil && pc.Claude == nil {
		pc.Claude = bundle.ProviderConfigs.Claude
		fmt.Println("  Imported: Claude config")
	}
	if bundle.ProviderConfigs.Codex != nil && pc.Codex == nil {
		pc.Codex = bundle.ProviderConfigs.Codex
		fmt.Println("  Imported: Codex config")
	}
	if bundle.ProviderConfigs.Ollama != nil && pc.Ollama == nil {
		pc.Ollama = bundle.ProviderConfigs.Ollama
		fmt.Println("  Imported: Ollama config")
	}
	v.SetProviderConfigs(pc)

	fmt.Printf("\nSummary: %d added, %d updated, %d skipped\n", added, updated, skipped)

	// Apply provider configs to system if requested
	if applyConfigs {
		fmt.Println("\nApplying provider configs to system...")
		if pc.Claude != nil {
			if err := agent.SaveClaudeConfig(pc.Claude); err != nil {
				fmt.Printf("  Warning: could not apply Claude config: %v\n", err)
			} else {
				fmt.Println("  Applied: Claude config to ~/.claude/")
			}
		}
		if pc.Codex != nil {
			if err := agent.SaveCodexConfig(pc.Codex); err != nil {
				fmt.Printf("  Warning: could not apply Codex config: %v\n", err)
			} else {
				fmt.Println("  Applied: Codex config to ~/.codex/")
			}
		}
	}

	// Show installation guide
	if len(bundle.InstallGuide.Requirements) > 0 {
		fmt.Println("\n--- Installation Guide ---")
		fmt.Println("Requirements:")
		for _, req := range bundle.InstallGuide.Requirements {
			status := "optional"
			if req.Required {
				status = "required"
			}
			fmt.Printf("  • %s (%s)\n", req.Name, status)
			if req.InstallCmd != "" {
				fmt.Printf("    Install: %s\n", req.InstallCmd)
			}
		}
	}

	return nil
}

func runSetupShow(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}

	var bundle SetupBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		// Might be encrypted
		if len(data) < crypto.SaltLen {
			return fmt.Errorf("invalid bundle format")
		}
		password, err := readPassword("Bundle password: ")
		if err != nil {
			return err
		}
		salt := data[:crypto.SaltLen]
		ciphertext := data[crypto.SaltLen:]
		key, err := crypto.DeriveKey(password, salt)
		if err != nil {
			return err
		}
		plaintext, err := crypto.Decrypt(ciphertext, key)
		if err != nil {
			return fmt.Errorf("decryption failed (wrong password?)")
		}
		if err := json.Unmarshal(plaintext, &bundle); err != nil {
			return fmt.Errorf("decoding bundle: %w", err)
		}
	}

	fmt.Println("Setup Bundle Contents")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Version:     %s\n", bundle.Version)
	fmt.Printf("Created:     %s\n", bundle.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Source:      %s (%s)\n", bundle.SourceMachine, bundle.SourceOS)

	fmt.Printf("\nAgents (%d):\n", len(bundle.Agents))
	for _, a := range bundle.Agents {
		hasKey := "no key"
		if a.APIKey != "" {
			hasKey = "has key"
		}
		fmt.Printf("  • %s (%s, %s) [%s]\n", a.Name, a.Provider, a.Model, hasKey)
	}

	fmt.Printf("\nInstructions (%d):\n", len(bundle.SharedConfig.Instructions))
	for _, inst := range bundle.SharedConfig.Instructions {
		fmt.Printf("  • %s -> %s (%d bytes)\n", inst.Name, inst.Filename, len(inst.Content))
	}

	if bundle.SharedConfig.SystemPrompt != "" {
		prompt := bundle.SharedConfig.SystemPrompt
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		fmt.Printf("\nShared Prompt: %s\n", prompt)
	}

	fmt.Printf("\nMCP Servers (%d):\n", len(bundle.SharedConfig.MCPServers))
	for _, s := range bundle.SharedConfig.MCPServers {
		fmt.Printf("  • %s: %s\n", s.Name, s.Command)
	}

	if len(bundle.DetectedAgents) > 0 {
		fmt.Printf("\nDetected Agents on Source Machine:\n")
		for _, a := range bundle.DetectedAgents {
			if a.Status != "not_found" {
				fmt.Printf("  • %s v%s (%s)\n", a.Name, a.Version, a.Status)
			}
		}
	}

	return nil
}

func runSetupApply(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	dir := args[0]
	generate, _ := cmd.Flags().GetBool("generate")
	only, _ := cmd.Flags().GetStringSlice("only")

	// Filter instructions if --only specified
	onlySet := make(map[string]struct{})
	for _, name := range only {
		onlySet[name] = struct{}{}
	}

	instructions := v.ListInstructions()
	if len(instructions) == 0 {
		fmt.Println("No instruction files stored. Use 'agentvault instructions pull' or 'set' first.")
		return nil
	}

	pushed := 0
	for _, inst := range instructions {
		if len(onlySet) > 0 {
			if _, ok := onlySet[inst.Name]; !ok {
				continue
			}
		}
		p := filepath.Join(dir, inst.Filename)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", inst.Filename, err)
		}
		if err := os.WriteFile(p, []byte(inst.Content), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
		fmt.Printf("  Pushed %s -> %s\n", inst.Name, inst.Filename)
		pushed++
	}
	fmt.Printf("Applied %d instruction file(s) to %s\n", pushed, dir)

	if generate {
		fmt.Println("\nGenerating configuration files...")
		// Generate .env file with agent configs
		envPath := filepath.Join(dir, ".env.agents")
		var envContent strings.Builder
		envContent.WriteString("# Generated by agentvault\n")
		envContent.WriteString(fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

		agents := v.List()
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

		if err := os.WriteFile(envPath, []byte(envContent.String()), 0600); err != nil {
			return fmt.Errorf("writing .env.agents: %w", err)
		}
		fmt.Printf("  Generated %s\n", envPath)
	}

	return nil
}

func runSetupPull(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	claudeOnly, _ := cmd.Flags().GetBool("claude")
	codexOnly, _ := cmd.Flags().GetBool("codex")
	ollamaOnly, _ := cmd.Flags().GetBool("ollama")
	allProviders := !claudeOnly && !codexOnly && !ollamaOnly

	pc := v.ProviderConfigs()
	pulled := 0

	if claudeOnly || allProviders {
		claudeConfig, err := agent.LoadClaudeConfig()
		if err == nil && (len(claudeConfig.EnabledPlugins) > 0 || len(claudeConfig.CustomSettings) > 0) {
			pc.Claude = claudeConfig
			fmt.Println("  Pulled: Claude config from ~/.claude/")
			if len(claudeConfig.EnabledPlugins) > 0 {
				fmt.Printf("    Plugins: %d enabled\n", len(claudeConfig.EnabledPlugins))
			}
			pulled++
		} else {
			fmt.Println("  Skipped: Claude (no config found)")
		}
	}

	if codexOnly || allProviders {
		codexConfig, err := agent.LoadCodexConfig()
		if err == nil && (len(codexConfig.TrustedProjects) > 0 || len(codexConfig.Rules) > 0) {
			pc.Codex = codexConfig
			fmt.Println("  Pulled: Codex config from ~/.codex/")
			if len(codexConfig.TrustedProjects) > 0 {
				fmt.Printf("    Trusted projects: %d\n", len(codexConfig.TrustedProjects))
			}
			if len(codexConfig.Rules) > 0 {
				fmt.Printf("    Rules: %d\n", len(codexConfig.Rules))
			}
			pulled++
		} else {
			fmt.Println("  Skipped: Codex (no config found)")
		}
	}

	if ollamaOnly || allProviders {
		ollamaConfig, err := agent.LoadOllamaConfig()
		if err == nil {
			pc.Ollama = ollamaConfig
			fmt.Println("  Pulled: Ollama config")
			pulled++
		} else {
			fmt.Println("  Skipped: Ollama (not configured)")
		}
	}

	if pulled > 0 {
		if err := v.SetProviderConfigs(pc); err != nil {
			return fmt.Errorf("saving provider configs: %w", err)
		}
		fmt.Printf("\nPulled %d provider config(s) into vault.\n", pulled)
	} else {
		fmt.Println("\nNo provider configs to pull.")
	}

	return nil
}

// generateInstallGuide creates step-by-step instructions for setting up the
// same agent environment on a new machine. Requirements are derived from the
// providers used in the bundle, and steps walk through init, import, API key
// configuration, and provider config application.
func generateInstallGuide(bundle SetupBundle) InstallGuide {
	guide := InstallGuide{}

	// Add requirements based on detected agents
	providers := make(map[agent.Provider]bool)
	for _, a := range bundle.Agents {
		providers[a.Provider] = true
	}

	if providers[agent.ProviderClaude] {
		guide.Requirements = append(guide.Requirements, Requirement{
			Name:        "Claude Code",
			Description: "Anthropic's Claude CLI tool",
			InstallCmd:  "npm install -g @anthropic/claude-code",
			Required:    true,
		})
	}

	if providers[agent.ProviderCodex] {
		guide.Requirements = append(guide.Requirements, Requirement{
			Name:        "Codex CLI",
			Description: "OpenAI's Codex-based CLI tool",
			InstallCmd:  "npm install -g @openai/codex",
			Required:    true,
		})
	}

	if providers[agent.ProviderOllama] {
		guide.Requirements = append(guide.Requirements, Requirement{
			Name:        "Ollama",
			Description: "Local LLM server",
			InstallCmd:  "curl -fsSL https://ollama.com/install.sh | sh",
			Required:    true,
		})
	}

	// Add setup steps
	guide.Steps = []SetupStep{
		{
			Name:        "Initialize AgentVault",
			Description: "Create a new vault on this machine",
			Commands:    []string{"agentvault init"},
		},
		{
			Name:        "Import this bundle",
			Description: "Import the agent configurations",
			Commands:    []string{"agentvault setup import <this-file>"},
		},
		{
			Name:        "Configure API keys",
			Description: "Add API keys for each agent",
			Commands: []string{
				"agentvault edit <agent-name> --api-key <your-key>",
			},
		},
	}

	if len(bundle.SharedConfig.Instructions) > 0 {
		guide.Steps = append(guide.Steps, SetupStep{
			Name:        "Apply instructions to project",
			Description: "Push instruction files to your project directory",
			Commands:    []string{"agentvault setup apply /path/to/project"},
		})
	}

	if bundle.ProviderConfigs.Claude != nil || bundle.ProviderConfigs.Codex != nil {
		guide.Steps = append(guide.Steps, SetupStep{
			Name:        "Apply provider configs",
			Description: "Apply Claude/Codex settings to system",
			Commands:    []string{"agentvault setup import <file> --apply-provider-configs"},
		})
	}

	guide.PostSetup = []string{
		"Verify agents work: agentvault detect",
		"View configuration: agentvault --tui",
		"Test an agent: agentvault run <agent-name>",
	}

	return guide
}
