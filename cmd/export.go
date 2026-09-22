package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	statuspkg "github.com/nikolareljin/agentvault/internal/status"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// exportOptions is the resolved answer set for one export run. Every field is
// settable by flag and askable by the wizard, so scripted and interactive runs
// take the same code path.
type exportOptions struct {
	Output         string
	Profiles       []string
	IncludeKeys    bool
	IncludeSecrets bool
	// Project-relative `.local.` files to carry anyway, named one at a time.
	IncludeLocalFiles   []string
	IncludeSessions     bool
	IncludeTemplates    bool
	IncludeProviderFile bool
	IncludeSkills       bool
	IncludeStatus       bool
	IncludeDetected     bool
	ProjectDir          string
	Encrypt             bool
	VaultOnly           bool
	Confirm             bool
}

// defaultExportOptions returns the "export everything" defaults used when the
// wizard is skipped or every prompt is answered with the default.
func defaultExportOptions() exportOptions {
	return exportOptions{
		IncludeKeys:         true,
		IncludeSecrets:      true,
		IncludeSessions:     true,
		IncludeTemplates:    true,
		IncludeProviderFile: true,
		IncludeSkills:       true,
		IncludeDetected:     true,
		// A quota snapshot is a point-in-time reading rather than a setting,
		// so it stays opt-in even in a full export.
		IncludeStatus: false,
		Encrypt:       true,
	}
}

var exportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export every agentvault setting to one portable bundle",
	Long: `Export agents, shared config, rules, roles, instructions, provider configs,
workflow templates, provider home files and skill assets to a single portable
bundle, then import it on another machine so both run the same setup.

Run with no arguments to get an interactive wizard. Every prompt has a default,
so pressing Enter through all of them exports everything, encrypted, to the
default export directory:

  ` + "`<config dir>/exports/<host>-<timestamp>.avbundle`" + `

Every agentvault config directory on this machine that holds a vault becomes a
named profile in the bundle. Discovery covers the active config dir, the XDG and
home fallbacks, sibling ` + "`agentvault*`" + ` directories (including hidden
` + "`~/.agentvault*`" + ` ones), and any directory listed in
AGENTVAULT_CONFIG_DIRS. Each profile's vault is unlocked separately; the value of
AGENTVAULT_PASSWORD is tried first, so one shared master password needs no typing.

When stdin is not a terminal, the wizard is skipped and the defaults apply.
Encrypted non-interactive exports read the bundle password from
AGENTVAULT_EXPORT_PASSWORD.

Examples:
  agentvault export                          # wizard, or full defaults when piped
  agentvault export -y                       # full export, no questions
  agentvault export team.avbundle            # wizard, explicit output path
  agentvault export out.json --plain --confirm
  agentvault export --profile work --profile default
  agentvault export legacy.vault --vault-only`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)

	defaults := defaultExportOptions()
	exportCmd.Flags().BoolP("yes", "y", false, "accept all defaults and skip the wizard")
	exportCmd.Flags().StringSlice("profile", nil, "profile names to export (repeatable; default: every discovered profile)")
	exportCmd.Flags().Bool("include-keys", defaults.IncludeKeys, "include API keys, resolving empty vault keys from the environment")
	exportCmd.Flags().Bool("include-secrets", defaults.IncludeSecrets, "include secret-bearing provider and asset file content")
	exportCmd.Flags().Bool("include-sessions", defaults.IncludeSessions, "include multi-agent session definitions")
	exportCmd.Flags().Bool("include-templates", defaults.IncludeTemplates, "include workflow templates")
	exportCmd.Flags().Bool("include-provider-files", defaults.IncludeProviderFile, "include provider home files (~/.claude, ~/.codex, ~/.copilot)")
	exportCmd.Flags().Bool("include-skills", defaults.IncludeSkills, "include skill assets")
	exportCmd.Flags().StringArray("include-local", nil,
		"carry one project-relative .local. file anyway, e.g. .claude/settings.local.json (repeatable; they are left behind by default because they are per-machine and may hold credentials)")
	exportCmd.Flags().Bool("include-status", defaults.IncludeStatus, "include a provider token/quota status snapshot")
	exportCmd.Flags().Bool("detect", defaults.IncludeDetected, "include detected agent information")
	exportCmd.Flags().String("project", "", "also capture project-local instruction, workflow and skill assets from this directory")
	exportCmd.Flags().Bool("encrypt", defaults.Encrypt, "encrypt the bundle with a password")
	exportCmd.Flags().Bool("plain", false, "write plaintext JSON (turns off encryption)")
	exportCmd.Flags().Bool("vault-only", false, "write the legacy single-vault export instead of a portable bundle")
	exportCmd.Flags().Bool("confirm", false, "skip interactive confirmations (for scripted use)")
}

func runExport(cmd *cobra.Command, args []string) error {
	opts := defaultExportOptions()
	opts.Output = ""
	if len(args) == 1 {
		opts.Output = args[0]
	}
	applyExportFlags(cmd, &opts)

	skipWizard, _ := cmd.Flags().GetBool("yes")
	isTerminal := term.IsTerminal(stdinFD())
	interactive := !skipWizard && isTerminal

	explicitConfigDir := resolveConfigDir()
	discovered := discoverProfiles(explicitConfigDir)
	if len(discovered) == 0 {
		return fmt.Errorf("no agentvault vault found (run 'agentvault init' first)")
	}

	if interactive {
		if err := runExportWizard(cmd, &opts, discovered); err != nil {
			return err
		}
	}

	selected, err := selectProfilesForExport(discovered, opts.Profiles)
	if err != nil {
		return err
	}

	if strings.TrimSpace(opts.Output) == "" {
		opts.Output = defaultExportPath(nowFunc())
	}
	if opts.VaultOnly && len(selected) > 1 {
		return fmt.Errorf("--vault-only exports a single vault; narrow the selection with --profile")
	}
	// Encryption is decided by --encrypt/--plain or the wizard, never by the file
	// extension, so the same command always produces the same kind of file.
	if !opts.Encrypt && opts.IncludeSecrets {
		if err := confirmPlaintextExport(opts.Confirm, isTerminal, os.Stdin, cmd.ErrOrStderr(), "--encrypt"); err != nil {
			return err
		}
	}

	password := ""
	if opts.Encrypt {
		password, err = resolveExportPassword(isTerminal)
		if err != nil {
			return err
		}
	}

	if opts.VaultOnly {
		return writeVaultOnlyExport(cmd, selected[0], opts, password)
	}

	bundle := newPortableBundle(nowFunc())
	for _, profile := range selected {
		built, err := buildProfileBundle(cmd, profile, opts)
		if err != nil {
			return err
		}
		bundle.Profiles = append(bundle.Profiles, built)
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding bundle: %w", err)
	}
	if opts.Encrypt {
		if data, err = encryptBundlePayload(data, password); err != nil {
			return err
		}
	}
	if err := writeExportFile(opts.Output, data); err != nil {
		return err
	}

	printExportSummary(cmd, bundle, opts)
	return nil
}

// applyExportFlags copies explicitly set flags onto opts, leaving unset flags at
// their defaults so the wizard can still ask about them.
func applyExportFlags(cmd *cobra.Command, opts *exportOptions) {
	flags := cmd.Flags()
	boolTargets := map[string]*bool{
		"include-keys":           &opts.IncludeKeys,
		"include-secrets":        &opts.IncludeSecrets,
		"include-sessions":       &opts.IncludeSessions,
		"include-templates":      &opts.IncludeTemplates,
		"include-provider-files": &opts.IncludeProviderFile,
		"include-skills":         &opts.IncludeSkills,
		"include-status":         &opts.IncludeStatus,
		"detect":                 &opts.IncludeDetected,
		"encrypt":                &opts.Encrypt,
		"vault-only":             &opts.VaultOnly,
		"confirm":                &opts.Confirm,
	}
	for name, target := range boolTargets {
		if value, err := flags.GetBool(name); err == nil {
			*target = value
		}
	}
	if values, err := flags.GetStringArray("include-local"); err == nil && len(values) > 0 {
		opts.IncludeLocalFiles = values
	}
	if plain, err := flags.GetBool("plain"); err == nil && plain {
		opts.Encrypt = false
	}
	if profiles, err := flags.GetStringSlice("profile"); err == nil {
		opts.Profiles = profiles
	}
	if project, err := flags.GetString("project"); err == nil {
		opts.ProjectDir = project
	}
}

// selectProfilesForExport narrows discovered profiles to the requested names.
// An empty selection means every discovered profile.
func selectProfilesForExport(discovered []discoveredProfile, wanted []string) ([]discoveredProfile, error) {
	if len(wanted) == 0 {
		return discovered, nil
	}
	byName := make(map[string]discoveredProfile, len(discovered))
	for _, p := range discovered {
		byName[strings.ToLower(p.Name)] = p
	}
	var selected []discoveredProfile
	seen := make(map[string]struct{}, len(wanted))
	for _, name := range wanted {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || key == "all" {
			return discovered, nil
		}
		profile, ok := byName[key]
		if !ok {
			return nil, fmt.Errorf("profile %q not found; available: %s", name, strings.Join(discoveredProfileNames(discovered), ", "))
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, profile)
	}
	return selected, nil
}

func discoveredProfileNames(profiles []discoveredProfile) []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}

// buildProfileBundle unlocks one profile's vault and captures its full setup.
func buildProfileBundle(cmd *cobra.Command, profile discoveredProfile, opts exportOptions) (PortableProfile, error) {
	v, err := unlockProfileVault(profile)
	if err != nil {
		return PortableProfile{}, err
	}

	host, _ := os.Hostname()
	collected, err := collectSetupBundle(cmd, v, profile.ConfigDir, setupCollectOptions{
		IncludeKeys:          opts.IncludeKeys,
		IncludeSecrets:       opts.IncludeSecrets,
		IncludeSessions:      opts.IncludeSessions,
		IncludeTemplates:     opts.IncludeTemplates,
		IncludeProviderFiles: opts.IncludeProviderFile,
		IncludeSkills:        opts.IncludeSkills,
		IncludeStatus:        opts.IncludeStatus,
		IncludeDetected:      opts.IncludeDetected,
		ProjectDir:           opts.ProjectDir,
		IncludeLocalFiles:    opts.IncludeLocalFiles,
	})
	if err != nil {
		return PortableProfile{}, fmt.Errorf("profile %q: %w", profile.Name, err)
	}
	return PortableProfile{
		Name:      profile.Name,
		ConfigDir: profile.ConfigDir,
		Machine:   host,
		OS:        collected.Bundle.SourceOS,
		Setup:     collected.Bundle,
	}, nil
}

// unlockProfileVault opens one profile's vault, trying AGENTVAULT_PASSWORD before
// prompting so a shared master password across profiles needs typing once at most.
func unlockProfileVault(profile discoveredProfile) (*vault.Vault, error) {
	vaultPath := filepath.Join(profile.ConfigDir, config.VaultFile)
	v := vault.New(vaultPath)
	if !v.Exists() {
		return nil, fmt.Errorf("%w at %s", ErrVaultNotFound, vaultPath)
	}
	if envPassword := os.Getenv(VaultPasswordEnv); envPassword != "" {
		if err := v.Unlock(envPassword); err == nil {
			return v, nil
		}
	}
	if err := requireInteractivePassword(VaultPasswordEnv); err != nil {
		return nil, fmt.Errorf("profile %q: %w", profile.Name, err)
	}
	pw, err := readPassword(fmt.Sprintf("Master password for profile %q: ", profile.Name))
	if err != nil {
		return nil, err
	}
	if err := v.Unlock(pw); err != nil {
		return nil, fmt.Errorf("profile %q: %w", profile.Name, err)
	}
	return v, nil
}

// writeVaultOnlyExport writes the legacy single-vault payload, kept so existing
// scripts and older agentvault builds can still consume an export.
func writeVaultOnlyExport(cmd *cobra.Command, profile discoveredProfile, opts exportOptions, password string) error {
	v, err := unlockProfileVault(profile)
	if err != nil {
		return err
	}
	plaintext, err := v.ExportData()
	if err != nil {
		return err
	}
	data := plaintext
	if opts.Encrypt {
		if data, err = encryptBundlePayload(plaintext, password); err != nil {
			return err
		}
	}
	if err := writeExportFile(opts.Output, data); err != nil {
		return err
	}
	encLabel := "unencrypted"
	if opts.Encrypt {
		encLabel = "encrypted"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Exported %d agents from profile %q to %s (%s, vault-only format)\n",
		len(v.List()), profile.Name, opts.Output, encLabel)
	return nil
}

// writeExportFile creates the parent directory then writes the bundle at 0600.
func writeExportFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating export directory: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing export: %w", err)
	}
	return nil
}

// resolveExportPassword reads the bundle password, preferring the environment so
// non-interactive runs can still produce encrypted output.
func resolveExportPassword(isTerminal bool) (string, error) {
	if envPassword := os.Getenv(ExportPasswordEnv); envPassword != "" {
		if len(envPassword) < 8 {
			return "", fmt.Errorf("%s must be at least 8 characters", ExportPasswordEnv)
		}
		return envPassword, nil
	}
	if !isTerminal {
		return "", fmt.Errorf("encrypted export needs a password: set %s, or pass --plain --confirm for an unencrypted bundle", ExportPasswordEnv)
	}
	pw, err := readPassword("Bundle password: ")
	if err != nil {
		return "", err
	}
	if len(pw) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	confirm, err := readPassword("Confirm bundle password: ")
	if err != nil {
		return "", err
	}
	if pw != confirm {
		return "", fmt.Errorf("passwords do not match")
	}
	return pw, nil
}

// printExportSummary reports what actually landed in the bundle.
func printExportSummary(cmd *cobra.Command, bundle PortableBundle, opts exportOptions) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Bundle written to %s\n", opts.Output)
	fmt.Fprintf(out, "  Schema: %s\n", bundle.SchemaVersion)
	fmt.Fprintf(out, "  Profiles: %d (%s)\n", len(bundle.Profiles), strings.Join(bundle.ProfileNames(), ", "))
	for _, p := range bundle.Profiles {
		fmt.Fprintf(out, "  Profile %q (%s)\n", p.Name, p.ConfigDir)
		fmt.Fprintf(out, "    Agents: %d\n", len(p.Setup.Agents))
		fmt.Fprintf(out, "    Sessions: %d\n", len(p.Setup.Sessions.Sessions))
		fmt.Fprintf(out, "    Rules: %d, Roles: %d, Instructions: %d, MCP servers: %d\n",
			len(p.Setup.SharedConfig.Rules), len(p.Setup.SharedConfig.Roles),
			len(p.Setup.SharedConfig.Instructions), len(p.Setup.SharedConfig.MCPServers))
		fmt.Fprintf(out, "    Workflow templates: %d\n", len(p.Setup.Templates.Assets))
		fmt.Fprintf(out, "    Provider files: %d, Skill assets: %d, Project files: %d\n",
			len(p.Setup.ProviderFiles), len(p.Setup.SkillAssets), len(p.Setup.ProjectFiles))
	}
	fmt.Fprintf(out, "  API keys included: %s\n", yesNo(opts.IncludeKeys))
	fmt.Fprintf(out, "  Secret file content included: %s\n", yesNo(opts.IncludeSecrets))
	fmt.Fprintf(out, "  Encrypted: %s\n", yesNo(opts.Encrypt))
	fmt.Fprintf(out, "\nImport on another machine with:\n  agentvault import %s --strategy mirror\n", opts.Output)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// setupCollectOptions selects which parts of a profile end up in its bundle.
type setupCollectOptions struct {
	IncludeKeys          bool
	IncludeSecrets       bool
	IncludeSessions      bool
	IncludeTemplates     bool
	IncludeProviderFiles bool
	IncludeSkills        bool
	IncludeStatus        bool
	IncludeDetected      bool
	ProjectDir           string
	AgentName            string
	// Project-relative `.local.` files to carry anyway, named one at a time.
	IncludeLocalFiles []string
}

// withoutPromptSessions strips stored prompt transcripts from a shared config.
// Prompt sessions are local run history, and their entries hold prompt and
// response text, so they never belong in a bundle meant to be shared.
func withoutPromptSessions(sc agent.SharedConfig) agent.SharedConfig {
	sc.PromptSessions = nil
	return sc
}

// setupCollectResult is a collected bundle plus the provenance of its API keys.
type setupCollectResult struct {
	Bundle SetupBundle
	// EnvKeyFilled is parallel to Bundle.Agents and marks agents whose key came
	// from the environment rather than the vault.
	EnvKeyFilled []bool
}

// collectSetupBundle captures one unlocked vault plus its on-disk assets into a
// SetupBundle. This is the single collection path shared by `export` and the
// deprecated `setup export`.
func collectSetupBundle(cmd *cobra.Command, v *vault.Vault, configDir string, opts setupCollectOptions) (setupCollectResult, error) {
	host, _ := os.Hostname()
	bundle := SetupBundle{
		Version:         "1.0",
		CreatedAt:       nowFunc(),
		SourceMachine:   host,
		SourceOS:        goOSArch(),
		Agents:          v.List(),
		SharedConfig:    withoutPromptSessions(v.SharedConfig()),
		ProviderConfigs: v.ProviderConfigs(),
	}
	if opts.IncludeSessions {
		bundle.Sessions = v.Sessions()
	}
	if strings.TrimSpace(opts.AgentName) != "" {
		selected, err := selectAgentsForExport(bundle.Agents, opts.AgentName)
		if err != nil {
			return setupCollectResult{}, err
		}
		bundle.Agents = selected
		bundle.Sessions = filterSessionsForAgents(bundle.Sessions, bundle.Agents)
	}

	if opts.IncludeTemplates {
		templateBundle, templateWarnings, err := workflowtemplates.ExportBundle(configDir)
		if err != nil {
			return setupCollectResult{}, fmt.Errorf("loading workflow templates: %w", err)
		}
		bundle.Templates = templateBundle
		for _, warn := range templateWarnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
		}
	}

	if opts.IncludeProviderFiles || opts.IncludeSkills || strings.TrimSpace(opts.ProjectDir) != "" {
		collected, assetWarnings, err := collectSetupAssets(setupAssetOptions{
			ProjectDir:        opts.ProjectDir,
			IncludeSecrets:    opts.IncludeSecrets,
			IncludeLocalFiles: opts.IncludeLocalFiles,
		})
		if err != nil {
			return setupCollectResult{}, fmt.Errorf("collecting portable setup assets: %w", err)
		}
		if opts.IncludeProviderFiles {
			bundle.ProviderFiles = collected.ProviderFiles
		}
		if opts.IncludeSkills {
			bundle.SkillAssets = collected.SkillAssets
		}
		bundle.ProjectFiles = collected.ProjectFiles
		bundle.InstructionOverrides = collected.InstructionOverrides
		for _, warn := range assetWarnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
		}
	}

	envKeyFilled := make([]bool, len(bundle.Agents))
	if opts.IncludeKeys {
		for i := range bundle.Agents {
			if bundle.Agents[i].APIKey == "" {
				if k := resolveAgentEnvAPIKey(bundle.Agents[i]); k != "" {
					bundle.Agents[i].APIKey = k
					envKeyFilled[i] = true
				}
			}
		}
	} else {
		for i := range bundle.Agents {
			bundle.Agents[i].APIKey = ""
		}
	}

	bundle.ModelCapabilities = v.ListCapabilities()

	if opts.IncludeDetected {
		bundle.DetectedAgents = detectAllAgents()
	}
	if opts.IncludeStatus {
		if home, err := os.UserHomeDir(); err == nil {
			report := statuspkg.BuildReport(v, home)
			bundle.StatusSnapshot = &report
		}
	}
	bundle.InstallGuide = generateInstallGuide(bundle)
	return setupCollectResult{Bundle: bundle, EnvKeyFilled: envKeyFilled}, nil
}
