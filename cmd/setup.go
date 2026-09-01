package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
	statuspkg "github.com/nikolareljin/agentvault/internal/status"
	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// SetupBundle represents a complete portable agent configuration bundle.
// This is the primary mechanism for replicating an entire agent setup
// across machines. It captures everything needed to recreate the environment:
// agents, sessions, rules, roles, instructions, provider configs, and an installation guide.
type SetupBundle struct {
	Version              string                       `json:"version"`
	CreatedAt            time.Time                    `json:"created_at"`
	SourceMachine        string                       `json:"source_machine"`
	SourceOS             string                       `json:"source_os"`
	Agents               []agent.Agent                `json:"agents"`
	Sessions             agent.SessionConfig          `json:"sessions,omitempty"`
	SharedConfig         agent.SharedConfig           `json:"shared_config"`
	ProviderConfigs      agent.ProviderConfig         `json:"provider_configs"`
	Templates            workflowtemplates.Bundle     `json:"workflow_templates"`
	ProviderFiles        []SetupAsset                 `json:"provider_files"`
	ProjectFiles         []SetupAsset                 `json:"project_files"`
	InstructionOverrides []SetupAsset                 `json:"instruction_overrides"`
	SkillAssets          []SetupAsset                 `json:"skill_assets"`
	ModelCapabilities    []agent.ModelCapabilityEntry `json:"model_capabilities,omitempty"`
	StatusSnapshot       *statuspkg.Report            `json:"status_snapshot,omitempty"`
	DetectedAgents       []DetectedAgent              `json:"detected_agents,omitempty"`
	InstallGuide         InstallGuide                 `json:"install_guide"`
}

// MarshalJSON normalizes empty asset and guide slices to [] for stable bundle output.
func (s SetupBundle) MarshalJSON() ([]byte, error) {
	type alias SetupBundle
	copy := s
	if copy.ProviderFiles == nil {
		copy.ProviderFiles = []SetupAsset{}
	}
	if copy.ProjectFiles == nil {
		copy.ProjectFiles = []SetupAsset{}
	}
	if copy.InstructionOverrides == nil {
		copy.InstructionOverrides = []SetupAsset{}
	}
	if copy.SkillAssets == nil {
		copy.SkillAssets = []SetupAsset{}
	}
	if copy.InstallGuide.Requirements == nil {
		copy.InstallGuide.Requirements = []Requirement{}
	}
	if copy.InstallGuide.Steps == nil {
		copy.InstallGuide.Steps = []SetupStep{}
	}
	if copy.InstallGuide.PostSetup == nil {
		copy.InstallGuide.PostSetup = []string{}
	}
	return json.Marshal(alias(copy))
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
  - Session definitions for multi-agent orchestration across machines
  - Instruction files stored in vault
  - Shared rules and roles
  - Claude settings (plugins, keybindings)
  - Codex settings (trusted projects, rules)
  - Ollama configuration
  - Optional status snapshot for orchestration-aware scheduling
  - Installation guide for the target machine

Examples:
  agentvault setup export my-setup.json           # Export to JSON
  agentvault setup export my-setup.bundle         # Export encrypted
  agentvault setup export setup.json --include-keys  # Include API keys
  agentvault setup export setup.json --include-status # Include token/quota snapshot
  agentvault setup export setup.json --detect     # Include detected agent info
  agentvault setup export setup.json --agent my-codex --project .`,
	Args:       cobra.ExactArgs(1),
	RunE:       runSetupExport,
	Deprecated: "use 'agentvault export' instead; it covers every config profile on this machine",
}

var setupImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import agent setup from a bundle",
	Long: `Import agent configurations from a setup bundle.

By default, existing agents are skipped. Use --merge to update settings.

Examples:
  agentvault setup import my-setup.json
  agentvault setup import my-setup.bundle    # Encrypted bundle
  agentvault setup import setup.json --merge # Update existing agents
  agentvault setup import setup.json --apply-provider-configs # Apply provider configs and assets`,
	Args:       cobra.ExactArgs(1),
	RunE:       runSetupImport,
	Deprecated: "use 'agentvault import' instead; it adds --strategy merge|replace|mirror",
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

	setupExportCmd.Flags().Bool("include-keys", false, "include API keys in export, also resolving keys from environment variables (use with caution)")
	setupExportCmd.Flags().Bool("detect", false, "include detected agent information")
	setupExportCmd.Flags().Bool("include-status", false, "include provider token/quota status snapshot in bundle")
	setupExportCmd.Flags().Bool("include-secrets", false, "include secret-bearing provider and asset files in bundle content; plaintext exports require interactive confirmation unless --confirm is set or --encrypted is used")
	setupExportCmd.Flags().Bool("confirm", false, "skip interactive confirmation for sensitive export options (for scripted/CI use)")
	setupExportCmd.Flags().Bool("encrypted", false, "encrypt the bundle (prompted for password)")
	setupExportCmd.Flags().Bool("plain", false, "force plaintext JSON output")
	setupExportCmd.Flags().String("agent", "", "export only one named agent")
	setupExportCmd.Flags().String("project", "", "include project-local instruction, workflow, and skill assets from this directory")

	setupImportCmd.Flags().Bool("merge", false, "merge with existing agents instead of skipping")
	setupImportCmd.Flags().Bool("apply-provider-configs", false, "apply provider configs and provider asset files to system after import")

	setupApplyCmd.Flags().Bool("generate", false, "also generate .env and provider config files")
	setupApplyCmd.Flags().StringSlice("only", nil, "apply only specific instructions (e.g., --only agents,claude)")

	setupPullCmd.Flags().Bool("claude", false, "pull only Claude config")
	setupPullCmd.Flags().Bool("codex", false, "pull only Codex config")
	setupPullCmd.Flags().Bool("ollama", false, "pull only Ollama config")
}

// runSetupExport implements the deprecated `setup export`. It keeps writing the
// v1 bundle layout for compatibility with older readers, but shares its
// collection path with `agentvault export`.
func runSetupExport(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	includeKeys, _ := cmd.Flags().GetBool("include-keys")
	detect, _ := cmd.Flags().GetBool("detect")
	includeStatus, _ := cmd.Flags().GetBool("include-status")
	includeSecrets, _ := cmd.Flags().GetBool("include-secrets")
	confirmFlag, _ := cmd.Flags().GetBool("confirm")
	encrypted, _ := cmd.Flags().GetBool("encrypted")
	plain, _ := cmd.Flags().GetBool("plain")
	agentName, _ := cmd.Flags().GetString("agent")
	projectDir, _ := cmd.Flags().GetString("project")

	// Determine output format from extension
	outputFile := args[0]
	if strings.HasSuffix(outputFile, ".bundle") && !plain {
		encrypted = true
	}
	if includeSecrets && !encrypted {
		if err := confirmPlaintextExport(confirmFlag, term.IsTerminal(stdinFD()), os.Stdin, cmd.ErrOrStderr(), "--encrypted"); err != nil {
			return err
		}
	}

	collected, err := collectSetupBundle(cmd, v, resolveConfigDir(), setupCollectOptions{
		IncludeKeys:          includeKeys,
		IncludeSecrets:       includeSecrets,
		IncludeSessions:      true,
		IncludeTemplates:     true,
		IncludeProviderFiles: true,
		IncludeSkills:        true,
		IncludeStatus:        includeStatus,
		IncludeDetected:      detect,
		ProjectDir:           projectDir,
		AgentName:            agentName,
	})
	if err != nil {
		return err
	}
	bundle := collected.Bundle

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
		if data, err = encryptBundlePayload(data, password); err != nil {
			return err
		}
	}

	if err := os.WriteFile(outputFile, data, 0600); err != nil {
		return fmt.Errorf("writing bundle: %w", err)
	}

	fmt.Printf("Setup bundle exported to %s\n", outputFile)
	fmt.Printf("  Agents: %d\n", len(bundle.Agents))
	fmt.Printf("  Sessions: %d\n", len(bundle.Sessions.Sessions))
	fmt.Printf("  Instructions: %d\n", len(bundle.SharedConfig.Instructions))
	fmt.Printf("  Workflow templates: %d\n", len(bundle.Templates.Assets))
	if includeStatus {
		fmt.Println("  Includes status snapshot: yes")
	}
	if encrypted {
		fmt.Println("  Encrypted: yes")
	}
	if strings.TrimSpace(agentName) != "" {
		fmt.Printf("  Export mode: single-agent (%s)\n", agentName)
	} else {
		fmt.Println("  Export mode: full bundle")
	}
	fmt.Printf("  Provider files: %d\n", len(bundle.ProviderFiles))
	fmt.Printf("  Project files: %d\n", len(bundle.ProjectFiles))
	fmt.Printf("  Instruction overrides: %d\n", len(bundle.InstructionOverrides))
	fmt.Printf("  Skill assets: %d\n", len(bundle.SkillAssets))
	if includeSecrets {
		fmt.Println("  Includes sensitive asset content: yes")
	}
	if len(bundle.Agents) > 0 {
		fmt.Println("  Agent keys:")
		for i, a := range bundle.Agents {
			keyStatus := agentKeyStatus(a, includeKeys, collected.EnvKeyFilled[i])
			fmt.Printf("    %-20s %s\n", a.Name+":", keyStatus)
		}
	}
	return nil
}

// runSetupImport implements the deprecated `setup import`. The historical
// --merge flag maps onto the replace strategy, which is what it always meant.
func runSetupImport(cmd *cobra.Command, args []string) error {
	merge, _ := cmd.Flags().GetBool("merge")
	applyConfigs, _ := cmd.Flags().GetBool("apply-provider-configs")

	strategy := strategyMerge
	if merge {
		strategy = strategyReplace
	}

	raw, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}
	payload, err := decodeImportPayload(raw)
	if err != nil {
		return err
	}
	bundle, format, err := decodePortableBundle(payload)
	if err != nil {
		return err
	}
	// This legacy path has no profile selection, so folding several source-machine
	// profiles into one vault would combine configurations the user never asked to
	// merge. A single-profile bundle is unambiguous and still imports here.
	if format == bundleFormatPortable && len(bundle.Profiles) > 1 {
		return fmt.Errorf("bundle holds %d profiles (%s); 'setup import' cannot choose between them, use 'agentvault import %s --profile NAME'",
			len(bundle.Profiles), strings.Join(bundle.ProfileNames(), ", "), args[0])
	}

	v, err := openVault()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Importing setup from %s (created %s)\n",
		bundleOrigin(bundle), bundle.CreatedAt.Format("2006-01-02 15:04"))

	now := time.Now()
	total := importReport{}
	for _, profile := range bundle.Profiles {
		if len(bundle.Profiles) > 1 {
			fmt.Fprintf(out, "\nProfile %q:\n", profile.Name)
		}
		plan := planProfileImport(v, profile.Setup, strategy, now)
		for _, line := range plan.Report.Lines {
			fmt.Fprintln(out, line)
		}
		total.Added += plan.Report.Added
		total.Updated += plan.Report.Updated
		total.Skipped += plan.Report.Skipped
		total.Removed += plan.Report.Removed
		if err := commitProfileImport(v, plan); err != nil {
			return err
		}
		if err := importProfileAssets(cmd, profile.Setup, applyConfigs); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "\nSummary: %d added, %d updated, %d skipped\n", total.Added, total.Updated, total.Skipped)
	printInstallGuide(cmd, bundle.Profiles)
	return nil
}

func mergeSharedRouterConfig(dst *agent.SharedConfig, src agent.SharedConfig, merge bool) string {
	if dst == nil || src.Router.IsZero() {
		return ""
	}
	if dst.Router.IsZero() {
		dst.Router = src.Router
		return "Imported"
	}
	if merge {
		dst.Router = src.Router
		return "Updated"
	}
	return ""
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
	if len(bundle.Templates.Assets) > 0 {
		fmt.Printf("\nWorkflow Templates (%d):\n", len(bundle.Templates.Assets))
		for _, tpl := range bundle.Templates.Assets {
			fmt.Printf("  • %s (%s, version=%s)\n", tpl.Filename, tpl.Key, tpl.Version)
		}
	}
	if len(bundle.ProviderFiles) > 0 || len(bundle.ProjectFiles) > 0 || len(bundle.InstructionOverrides) > 0 || len(bundle.SkillAssets) > 0 {
		fmt.Printf("\nPortable Assets:\n")
		printSetupAssetSummary("Provider Files", bundle.ProviderFiles)
		printSetupAssetSummary("Project Files", bundle.ProjectFiles)
		printSetupAssetSummary("Instruction Overrides", bundle.InstructionOverrides)
		printSetupAssetSummary("Skill Assets", bundle.SkillAssets)
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
	fmt.Printf("\nSessions (%d):\n", len(bundle.Sessions.Sessions))
	for _, s := range bundle.Sessions.Sessions {
		fmt.Printf("  • %s (%d agents, %s)\n", s.Name, len(s.Agents), s.ProjectDir)
	}
	if bundle.StatusSnapshot != nil {
		fmt.Printf("\nStatus Snapshot: %s\n", bundle.StatusSnapshot.GeneratedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Providers: %d\n", len(bundle.StatusSnapshot.Providers))
	}

	return nil
}

func selectAgentsForExport(all []agent.Agent, name string) ([]agent.Agent, error) {
	for _, item := range all {
		if item.Name == name {
			return []agent.Agent{item}, nil
		}
	}
	return nil, fmt.Errorf("agent %q not found", name)
}

func filterProjectFilesForStaging(projectFiles []SetupAsset, instructionOverrides []SetupAsset) []SetupAsset {
	if len(projectFiles) == 0 || len(instructionOverrides) == 0 {
		return append([]SetupAsset{}, projectFiles...)
	}
	overridePaths := make(map[string]struct{}, len(instructionOverrides))
	for _, asset := range instructionOverrides {
		path := asset.ProjectRelativePath
		if path == "" {
			path = asset.LogicalPath
		}
		if path == "" {
			continue
		}
		overridePaths[filepath.ToSlash(path)] = struct{}{}
	}
	filtered := make([]SetupAsset, 0, len(projectFiles))
	for _, asset := range projectFiles {
		path := asset.ProjectRelativePath
		if path == "" {
			path = asset.LogicalPath
		}
		if _, exists := overridePaths[filepath.ToSlash(path)]; exists {
			continue
		}
		filtered = append(filtered, asset)
	}
	return filtered
}

func printSetupAssetSummary(label string, assets []SetupAsset) {
	if len(assets) == 0 {
		return
	}
	sensitive := 0
	redacted := 0
	for _, asset := range assets {
		if asset.Sensitive {
			sensitive++
		}
		if asset.Redacted {
			redacted++
		}
	}
	fmt.Printf("  %s: %d", label, len(assets))
	if sensitive > 0 || redacted > 0 {
		fmt.Printf(" (sensitive=%d, redacted=%d)", sensitive, redacted)
	}
	fmt.Println()
}

func filterSessionsForAgents(config agent.SessionConfig, selected []agent.Agent) agent.SessionConfig {
	if len(selected) == 0 {
		return agent.SessionConfig{}
	}
	allowed := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		allowed[item.Name] = struct{}{}
	}

	filtered := config
	filtered.Sessions = nil
	activeSessionAllowed := false
	for _, session := range config.Sessions {
		keptAgents := make([]agent.SessionAgent, 0, len(session.Agents))
		for _, sessionAgent := range session.Agents {
			if _, ok := allowed[sessionAgent.Name]; ok {
				keptAgents = append(keptAgents, sessionAgent)
			}
		}
		if len(keptAgents) == 0 {
			continue
		}
		session.Agents = keptAgents
		filtered.Sessions = append(filtered.Sessions, session)
		if session.ID == config.ActiveSession {
			activeSessionAllowed = true
		}
	}
	filtered.DefaultAgents = filterStringSet(config.DefaultAgents, allowed)
	if !activeSessionAllowed {
		filtered.ActiveSession = ""
	}
	return filtered
}

func filterStringSet(items []string, allowed map[string]struct{}) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func providerSkillAssets(items []SetupAsset) []SetupAsset {
	filtered := make([]SetupAsset, 0, len(items))
	for _, item := range items {
		if item.LogicalRoot == setupAssetRootProviderClaudeSkill || item.LogicalRoot == setupAssetRootProviderCodexSkill {
			filtered = append(filtered, item)
		}
	}
	return filtered
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
	if len(onlySet) == 0 {
		stagedApplied, err := applyStagedProjectAssets(effectiveConfigDir(), dir)
		if err != nil {
			return err
		}
		if stagedApplied > 0 {
			fmt.Printf("Applied %d staged project asset(s) to %s\n", stagedApplied, dir)
		}
	}

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
			Description: "Apply Claude/Codex settings and provider assets to system",
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

// confirmPlaintextExport gates a plaintext --include-secrets export.
// encryptFlag names the flag that would encrypt the output, because `export` and
// the deprecated `setup export` spell it differently.
// It returns nil when export should proceed (confirmFlag set or user typed y/yes).
// isTerminal and r are injected so the function is testable without a real TTY.
func confirmPlaintextExport(confirmFlag bool, isTerminal bool, r io.Reader, w io.Writer, encryptFlag string) error {
	if confirmFlag {
		return nil
	}
	if !isTerminal {
		return fmt.Errorf("--include-secrets without %s requires interactive confirmation; use --confirm to bypass in non-interactive environments", encryptFlag)
	}
	fmt.Fprintln(w, "warning: --include-secrets will embed sensitive asset content in plaintext")
	fmt.Fprint(w, "Confirm export with sensitive content? [y/N]: ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("export cancelled: add %s to protect sensitive content, or use --confirm to bypass this check", encryptFlag)
	}
	return nil
}

// resolveAgentEnvAPIKey returns the API key for the agent from standard environment
// variables when the vault-stored key is empty. This allows --include-keys to also
// capture credentials that were configured via environment variables rather than
// stored directly in the vault.
func resolveAgentEnvAPIKey(a agent.Agent) string {
	if a.APIKey != "" {
		return a.APIKey
	}
	switch a.Provider {
	case agent.ProviderClaude:
		switch agent.NormalizeClaudeBackend(a.Backend) {
		case agent.ClaudeBackendOllama, agent.ClaudeBackendBedrock:
			return ""
		default:
			return os.Getenv("ANTHROPIC_API_KEY")
		}
	case agent.ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case agent.ProviderGemini:
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			return key
		}
		return os.Getenv("GOOGLE_API_KEY")
	case agent.ProviderCodex:
		return os.Getenv("OPENAI_API_KEY")
	}
	return ""
}

// agentRequiresAPIKey reports whether the agent's provider/backend needs an API key.
// Ollama and Claude agents using the Ollama or Bedrock backend do not require one.
func agentRequiresAPIKey(a agent.Agent) bool {
	if a.Provider == agent.ProviderOllama {
		return false
	}
	if a.Provider == agent.ProviderClaude {
		switch agent.NormalizeClaudeBackend(a.Backend) {
		case agent.ClaudeBackendOllama, agent.ClaudeBackendBedrock:
			return false
		}
	}
	return true
}

// agentKeyStatus returns a human-readable label describing the key state for
// an agent in the export summary. envFilled indicates the key was sourced from
// an environment variable during export (vault key was empty).
func agentKeyStatus(a agent.Agent, includeKeys bool, envFilled bool) string {
	if !includeKeys {
		if !agentRequiresAPIKey(a) {
			return "[no key needed]"
		}
		return "[redacted]"
	}
	if a.APIKey == "" {
		if agentRequiresAPIKey(a) {
			return "[no key found]"
		}
		return "[no key needed]"
	}
	if envFilled {
		return "[env key included]"
	}
	return "[vault key included]"
}
