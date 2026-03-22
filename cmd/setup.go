package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
	statuspkg "github.com/nikolareljin/agentvault/internal/status"
	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

// SetupBundle represents a complete portable agent configuration bundle.
// This is the primary mechanism for replicating an entire agent setup
// across machines. It captures everything needed to recreate the environment:
// agents, sessions, rules, roles, instructions, provider configs, and an installation guide.
type SetupBundle struct {
	Version              string                   `json:"version"`
	CreatedAt            time.Time                `json:"created_at"`
	SourceMachine        string                   `json:"source_machine"`
	SourceOS             string                   `json:"source_os"`
	Agents               []agent.Agent            `json:"agents"`
	Sessions             agent.SessionConfig      `json:"sessions,omitempty"`
	SharedConfig         agent.SharedConfig       `json:"shared_config"`
	ProviderConfigs      agent.ProviderConfig     `json:"provider_configs"`
	Templates            workflowtemplates.Bundle `json:"workflow_templates"`
	ProviderFiles        []SetupAsset             `json:"provider_files"`
	ProjectFiles         []SetupAsset             `json:"project_files"`
	InstructionOverrides []SetupAsset             `json:"instruction_overrides"`
	SkillAssets          []SetupAsset             `json:"skill_assets"`
	StatusSnapshot       *statuspkg.Report        `json:"status_snapshot,omitempty"`
	DetectedAgents       []DetectedAgent          `json:"detected_agents,omitempty"`
	InstallGuide         InstallGuide             `json:"install_guide"`
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
	setupExportCmd.Flags().Bool("include-status", false, "include provider token/quota status snapshot in bundle")
	setupExportCmd.Flags().Bool("include-secrets", false, "include secret-bearing provider and asset files in bundle content")
	setupExportCmd.Flags().Bool("encrypted", false, "encrypt the bundle (prompted for password)")
	setupExportCmd.Flags().Bool("plain", false, "force plaintext JSON output")
	setupExportCmd.Flags().String("agent", "", "export only one named agent")
	setupExportCmd.Flags().String("project", "", "include project-local instruction, workflow, and skill assets from this directory")

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
	includeStatus, _ := cmd.Flags().GetBool("include-status")
	includeSecrets, _ := cmd.Flags().GetBool("include-secrets")
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
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: --include-secrets exports sensitive asset content without bundle encryption")
	}

	hostname, _ := os.Hostname()
	bundle := SetupBundle{
		Version:         "1.0",
		CreatedAt:       time.Now(),
		SourceMachine:   hostname,
		SourceOS:        runtime.GOOS + "/" + runtime.GOARCH,
		Agents:          v.List(),
		Sessions:        v.Sessions(),
		SharedConfig:    v.SharedConfig(),
		ProviderConfigs: v.ProviderConfigs(),
	}
	if strings.TrimSpace(agentName) != "" {
		selected, err := selectAgentsForExport(bundle.Agents, agentName)
		if err != nil {
			return err
		}
		bundle.Agents = selected
		bundle.Sessions = filterSessionsForAgents(bundle.Sessions, bundle.Agents)
	}
	templateBundle, templateWarnings, err := workflowtemplates.ExportBundle(resolveConfigDir())
	if err != nil {
		return fmt.Errorf("loading workflow templates for export: %w", err)
	}
	bundle.Templates = templateBundle
	for _, warn := range templateWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
	}
	collectedAssets, assetWarnings, err := collectSetupAssets(setupAssetOptions{
		ProjectDir:     projectDir,
		IncludeSecrets: includeSecrets,
	})
	if err != nil {
		return fmt.Errorf("collecting portable setup assets: %w", err)
	}
	bundle.ProviderFiles = collectedAssets.ProviderFiles
	bundle.ProjectFiles = collectedAssets.ProjectFiles
	bundle.InstructionOverrides = collectedAssets.InstructionOverrides
	bundle.SkillAssets = collectedAssets.SkillAssets
	for _, warn := range assetWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
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
	if includeStatus {
		home, err := os.UserHomeDir()
		if err == nil {
			report := statuspkg.BuildReport(v, home)
			bundle.StatusSnapshot = &report
		}
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
	fmt.Printf("  Sessions: %d\n", len(bundle.Sessions.Sessions))
	fmt.Printf("  Instructions: %d\n", len(bundle.SharedConfig.Instructions))
	fmt.Printf("  Workflow templates: %d\n", len(bundle.Templates.Assets))
	if includeKeys {
		fmt.Println("  Warning: API keys are included!")
	}
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
	if bundle.SharedConfig.SystemPrompt != "" && (sc.SystemPrompt == "" || merge) {
		sc.SystemPrompt = bundle.SharedConfig.SystemPrompt
		fmt.Println("  Imported: shared system prompt")
	}

	// Merge MCP servers
	mcpIndex := make(map[string]int)
	for i, s := range sc.MCPServers {
		mcpIndex[s.Name] = i
	}
	for _, s := range bundle.SharedConfig.MCPServers {
		if idx, ok := mcpIndex[s.Name]; !ok {
			sc.MCPServers = append(sc.MCPServers, s)
			mcpIndex[s.Name] = len(sc.MCPServers) - 1
			fmt.Printf("  Imported: MCP server %s\n", s.Name)
		} else if merge {
			sc.MCPServers[idx] = s
			fmt.Printf("  Updated: MCP server %s\n", s.Name)
		}
	}

	// Merge instructions
	instIndex := make(map[string]int)
	for i, inst := range sc.Instructions {
		instIndex[inst.Name] = i
	}
	for _, inst := range bundle.SharedConfig.Instructions {
		if idx, ok := instIndex[inst.Name]; !ok {
			sc.Instructions = append(sc.Instructions, inst)
			instIndex[inst.Name] = len(sc.Instructions) - 1
			fmt.Printf("  Imported: instruction %s\n", inst.Name)
		} else if merge {
			sc.Instructions[idx] = inst
			fmt.Printf("  Updated: instruction %s\n", inst.Name)
		}
	}
	for _, asset := range bundle.InstructionOverrides {
		name := instructionNameForAsset(asset)
		if name == "" || asset.Missing || !asset.ContentPresent {
			continue
		}
		filename := asset.ProjectRelativePath
		if filename == "" {
			filename = asset.LogicalPath
		}
		filename, err = sanitizeAssetRelativePath(filename)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skipping instruction override %q due to unsafe filename: %v\n", name, err)
			continue
		}
		inst := agent.InstructionFile{
			Name:      name,
			Filename:  filename,
			Content:   string(asset.Content),
			UpdatedAt: time.Now(),
		}
		if idx, ok := instIndex[inst.Name]; !ok {
			sc.Instructions = append(sc.Instructions, inst)
			instIndex[inst.Name] = len(sc.Instructions) - 1
			fmt.Printf("  Imported: instruction override %s\n", inst.Name)
		} else if merge || sc.Instructions[idx].Content == "" {
			sc.Instructions[idx] = inst
			fmt.Printf("  Updated: instruction override %s\n", inst.Name)
		}
	}

	// Merge rules
	ruleIndex := make(map[string]int)
	for i, r := range sc.Rules {
		ruleIndex[r.Name] = i
	}
	for _, r := range bundle.SharedConfig.Rules {
		if idx, ok := ruleIndex[r.Name]; !ok {
			sc.Rules = append(sc.Rules, r)
			ruleIndex[r.Name] = len(sc.Rules) - 1
			fmt.Printf("  Imported: rule %s\n", r.Name)
		} else if merge {
			sc.Rules[idx] = r
			fmt.Printf("  Updated: rule %s\n", r.Name)
		}
	}
	sort.Slice(sc.Rules, func(i, j int) bool {
		return sc.Rules[i].Priority < sc.Rules[j].Priority
	})

	// Merge roles
	roleIndex := make(map[string]int)
	for i, r := range sc.Roles {
		roleIndex[r.Name] = i
	}
	for _, r := range bundle.SharedConfig.Roles {
		if idx, ok := roleIndex[r.Name]; !ok {
			sc.Roles = append(sc.Roles, r)
			roleIndex[r.Name] = len(sc.Roles) - 1
			fmt.Printf("  Imported: role %s\n", r.Name)
		} else if merge {
			sc.Roles[idx] = r
			fmt.Printf("  Updated: role %s\n", r.Name)
		}
	}

	if err := v.SetSharedConfig(sc); err != nil {
		return fmt.Errorf("updating shared config: %w", err)
	}

	// Import provider configs
	pc := v.ProviderConfigs()
	if bundle.ProviderConfigs.Claude != nil && (pc.Claude == nil || merge) {
		pc.Claude = bundle.ProviderConfigs.Claude
		fmt.Println("  Imported: Claude config")
	}
	if bundle.ProviderConfigs.Codex != nil && (pc.Codex == nil || merge) {
		pc.Codex = bundle.ProviderConfigs.Codex
		fmt.Println("  Imported: Codex config")
	}
	if bundle.ProviderConfigs.Ollama != nil && (pc.Ollama == nil || merge) {
		pc.Ollama = bundle.ProviderConfigs.Ollama
		fmt.Println("  Imported: Ollama config")
	}
	if err := v.SetProviderConfigs(pc); err != nil {
		return fmt.Errorf("updating provider configs: %w", err)
	}
	// Backward compatibility: older bundles may not include workflow templates.
	if bundle.Templates.SchemaVersion != "" || len(bundle.Templates.Assets) > 0 {
		importedTemplates, templateWarnings, err := workflowtemplates.ImportBundle(resolveConfigDir(), bundle.Templates)
		if err != nil {
			return fmt.Errorf("importing workflow templates: %w", err)
		}
		if importedTemplates > 0 {
			fmt.Printf("  Imported: workflow templates (%d)\n", importedTemplates)
		}
		for _, warn := range templateWarnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
		}
	}
	filteredProjectFiles := filterProjectFilesForStaging(bundle.ProjectFiles, bundle.InstructionOverrides)
	stagedAssets, stageWarnings, err := stageImportedAssets(effectiveConfigDir(), append(append(append([]SetupAsset{}, bundle.ProviderFiles...), filteredProjectFiles...), bundle.SkillAssets...))
	if err != nil {
		return fmt.Errorf("staging imported portable assets: %w", err)
	}
	if stagedAssets > 0 {
		fmt.Printf("  Imported: portable assets (%d staged)\n", stagedAssets)
	}
	for _, warn := range stageWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
	}

	// Import sessions
	targetSessions := v.Sessions()
	sessionByName := make(map[string]int)
	sessionIDs := make(map[string]struct{})
	for i, s := range targetSessions.Sessions {
		sessionByName[s.Name] = i
		sessionIDs[s.ID] = struct{}{}
	}
	sessionAdded, sessionUpdated, sessionSkipped := 0, 0, 0
	for _, s := range bundle.Sessions.Sessions {
		// Session process info is machine-local and should not be imported.
		s.Status = agent.SessionStatusIdle
		s.UpdatedAt = time.Now()
		for i := range s.Agents {
			s.Agents[i].PID = 0
		}

		if idx, ok := sessionByName[s.Name]; ok {
			if merge {
				s.ID = targetSessions.Sessions[idx].ID
				targetSessions.Sessions[idx] = s
				fmt.Printf("  Updated: session %s\n", s.Name)
				sessionUpdated++
			} else {
				fmt.Printf("  Skipped: session %s (exists)\n", s.Name)
				sessionSkipped++
			}
			continue
		}

		if s.ID == "" {
			s.ID = agent.GenerateSessionID()
		}
		for {
			if _, exists := sessionIDs[s.ID]; !exists {
				break
			}
			s.ID = fmt.Sprintf("%s-%d", agent.GenerateSessionID(), time.Now().UnixNano()%1000)
		}
		targetSessions.Sessions = append(targetSessions.Sessions, s)
		sessionByName[s.Name] = len(targetSessions.Sessions) - 1
		sessionIDs[s.ID] = struct{}{}
		fmt.Printf("  Added: session %s\n", s.Name)
		sessionAdded++
	}
	if targetSessions.ActiveSession == "" && bundle.Sessions.ActiveSession != "" {
		targetSessions.ActiveSession = bundle.Sessions.ActiveSession
	}
	if !targetSessions.ParallelLimitSet && (bundle.Sessions.ParallelLimitSet || bundle.Sessions.ParallelLimit > 0) {
		targetSessions.ParallelLimit = bundle.Sessions.ParallelLimit
		targetSessions.ParallelLimitSet = true
	}
	if len(targetSessions.DefaultAgents) == 0 && len(bundle.Sessions.DefaultAgents) > 0 {
		targetSessions.DefaultAgents = append([]string(nil), bundle.Sessions.DefaultAgents...)
	}
	if err := v.SetSessions(targetSessions); err != nil {
		return fmt.Errorf("updating sessions: %w", err)
	}

	fmt.Printf("\nSummary: %d added, %d updated, %d skipped\n", added, updated, skipped)
	if sessionAdded+sessionUpdated+sessionSkipped > 0 {
		fmt.Printf("Sessions: %d added, %d updated, %d skipped\n", sessionAdded, sessionUpdated, sessionSkipped)
	}

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
		homeDir, homeErr := os.UserHomeDir()
		if homeErr != nil {
			fmt.Printf("  Warning: could not resolve home directory for provider asset apply: %v\n", homeErr)
		} else {
			appliedAssets, assetWarnings, err := applyProviderAssetsToSystem(homeDir, append(bundle.ProviderFiles, providerSkillAssets(bundle.SkillAssets)...))
			if err != nil {
				return fmt.Errorf("applying provider assets: %w", err)
			}
			if appliedAssets > 0 {
				fmt.Printf("  Applied: provider assets (%d)\n", appliedAssets)
			}
			for _, warn := range assetWarnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
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
