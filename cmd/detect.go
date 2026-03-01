package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

// DetectedAgent represents an AI agent CLI tool found on the system.
// Detection checks for the binary in PATH, reads its version, and inspects
// known config directories for provider-specific settings.
type DetectedAgent struct {
	Name        string         `json:"name"`
	Provider    agent.Provider `json:"provider"`
	Version     string         `json:"version"`
	Path        string         `json:"path"`
	ConfigDir   string         `json:"config_dir,omitempty"`
	ConfigFiles []string       `json:"config_files,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
	Status      string         `json:"status"` // "found", "configured", "not_found"
	InVault     bool           `json:"in_vault"`
}

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect installed AI agents on the system",
	Long: `Scan the system for installed AI agent CLI tools and their configurations.

Detects:
  - Claude Code (claude CLI)
  - Codex CLI (codex)
  - Ollama (ollama)
  - Aider (aider)
  - OpenAI CLI tools

Use 'agentvault detect add' to automatically add detected agents to the vault.`,
	RunE: runDetect,
}

var detectAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Detect agents and add them to the vault",
	Long:  `Detect installed agents and automatically add them to the vault with their configurations.`,
	RunE:  runDetectAdd,
}

func init() {
	rootCmd.AddCommand(detectCmd)
	detectCmd.AddCommand(detectAddCmd)
	detectCmd.Flags().Bool("json", false, "output as JSON")
	detectCmd.Flags().Bool("verbose", false, "show detailed configuration info")
	detectAddCmd.Flags().Bool("force", false, "overwrite existing agents in vault")
}

func runDetect(cmd *cobra.Command, args []string) error {
	agents := detectAllAgents()
	jsonOutput, _ := cmd.Flags().GetBool("json")
	verbose, _ := cmd.Flags().GetBool("verbose")

	// Avoid interactive password prompts for plain "detect".
	// Only check vault membership when password is provided via env.
	if password := os.Getenv("AGENTVAULT_PASSWORD"); password != "" {
		v := vault.New(resolveVaultPath())
		if v.Exists() && v.Unlock(password) == nil {
			for i := range agents {
				if _, found := v.Get(agents[i].Name); found {
					agents[i].InVault = true
				}
			}
		}
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(agents, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println("Detected AI Agents:")
	fmt.Println(strings.Repeat("─", 60))

	found := 0
	for _, a := range agents {
		if a.Status == "not_found" {
			continue
		}
		found++
		vaultStatus := ""
		if a.InVault {
			vaultStatus = " ✓ in vault"
		}
		fmt.Printf("\n  %s (%s)%s\n", a.Name, a.Provider, vaultStatus)
		fmt.Printf("    Version:    %s\n", a.Version)
		fmt.Printf("    Path:       %s\n", a.Path)
		if a.ConfigDir != "" {
			fmt.Printf("    Config:     %s\n", a.ConfigDir)
		}
		if verbose && len(a.Settings) > 0 {
			fmt.Println("    Settings:")
			for k, v := range a.Settings {
				fmt.Printf("      %s: %v\n", k, v)
			}
		}
		if verbose && len(a.ConfigFiles) > 0 {
			fmt.Println("    Config files:")
			for _, f := range a.ConfigFiles {
				fmt.Printf("      - %s\n", f)
			}
		}
	}

	if found == 0 {
		fmt.Println("\n  No AI agents detected on this system.")
	} else {
		fmt.Printf("\n%s\n", strings.Repeat("─", 60))
		fmt.Printf("Found %d agent(s). Use 'agentvault detect add' to add them to your vault.\n", found)
	}

	return nil
}

func runDetectAdd(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	force, _ := cmd.Flags().GetBool("force")
	agents := detectAllAgents()

	added := 0
	skipped := 0
	updated := 0

	for _, detected := range agents {
		if detected.Status == "not_found" {
			continue
		}

		existing, exists := v.Get(detected.Name)
		if exists && !force {
			fmt.Printf("  Skipped %s (already in vault, use --force to update)\n", detected.Name)
			skipped++
			continue
		}

		newAgent := agent.Agent{
			Name:      detected.Name,
			Provider:  detected.Provider,
			Tags:      []string{"auto-detected"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Apply detected settings
		if model, ok := detected.Settings["model"].(string); ok {
			newAgent.Model = model
		}
		if baseURL, ok := detected.Settings["base_url"].(string); ok {
			newAgent.BaseURL = baseURL
		}

		if exists {
			// Preserve existing API key and other user settings
			newAgent.APIKey = existing.APIKey
			newAgent.SystemPrompt = existing.SystemPrompt
			newAgent.TaskDesc = existing.TaskDesc
			newAgent.Tags = mergeUniqueTags(existing.Tags, newAgent.Tags)
			if err := v.Update(newAgent); err != nil {
				return fmt.Errorf("updating agent %s: %w", newAgent.Name, err)
			}
			fmt.Printf("  Updated %s (%s)\n", newAgent.Name, newAgent.Provider)
			updated++
		} else {
			if err := v.Add(newAgent); err != nil {
				return fmt.Errorf("adding agent %s: %w", newAgent.Name, err)
			}
			fmt.Printf("  Added %s (%s)\n", newAgent.Name, newAgent.Provider)
			added++
		}
	}

	fmt.Printf("\nSummary: %d added, %d updated, %d skipped\n", added, updated, skipped)
	return nil
}

// detectAllAgents scans the system for all supported AI agent CLI tools.
// Each detect function checks: binary in PATH, version, config directory,
// and provider-specific settings (plugins, models, trusted projects, etc.).
func detectAllAgents() []DetectedAgent {
	var agents []DetectedAgent
	agents = append(agents, detectClaude())
	agents = append(agents, detectCodex())
	agents = append(agents, detectOllama())
	agents = append(agents, detectAider())
	agents = append(agents, detectMeldbot())
	agents = append(agents, detectOpenclaw())
	agents = append(agents, detectNanoclaw())
	agents = append(agents, detectGemini())
	agents = append(agents, detectOpenAI())
	agents = append(agents, detectCopilotCLI())
	return agents
}

func detectClaude() DetectedAgent {
	a := DetectedAgent{
		Name:     "claude",
		Provider: agent.ProviderClaude,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find claude binary
	path, err := exec.LookPath("claude")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("claude", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config directory
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(configDir); err == nil {
		a.ConfigDir = configDir
		a.Status = "configured"

		// List config files
		entries, _ := os.ReadDir(configDir)
		for _, e := range entries {
			if !e.IsDir() {
				a.ConfigFiles = append(a.ConfigFiles, e.Name())
			}
		}

		// Read settings.json
		settingsPath := filepath.Join(configDir, "settings.json")
		if data, err := os.ReadFile(settingsPath); err == nil {
			var settings map[string]any
			if json.Unmarshal(data, &settings) == nil {
				a.Settings["settings"] = settings
				if plugins, ok := settings["enabledPlugins"].(map[string]any); ok {
					enabledPlugins := []string{}
					for name, enabled := range plugins {
						if e, ok := enabled.(bool); ok && e {
							enabledPlugins = append(enabledPlugins, name)
						}
					}
					a.Settings["plugins"] = enabledPlugins
				}
			}
		}
	}

	return a
}

func detectCodex() DetectedAgent {
	a := DetectedAgent{
		Name:     "codex",
		Provider: agent.ProviderCodex,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find codex binary
	path, err := exec.LookPath("codex")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("codex", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config directory
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".codex")
	if _, err := os.Stat(configDir); err == nil {
		a.ConfigDir = configDir
		a.Status = "configured"

		// List config files
		entries, _ := os.ReadDir(configDir)
		for _, e := range entries {
			if !e.IsDir() {
				a.ConfigFiles = append(a.ConfigFiles, e.Name())
			}
		}

		// Read config.toml (simple parsing)
		configPath := filepath.Join(configDir, "config.toml")
		if data, err := os.ReadFile(configPath); err == nil {
			// Parse trusted projects; only include sections explicitly marked as trusted.
			lines := strings.Split(string(data), "\n")
			trustedProjects := []string{}
			currentProjectPath := ""
			currentProjectTrusted := false
			for _, rawLine := range lines {
				line := strings.TrimSpace(rawLine)
				if strings.HasPrefix(line, "[projects.") {
					start := strings.Index(line, `"`)
					end := strings.LastIndex(line, `"`)
					if start != -1 && end > start {
						currentProjectPath = line[start+1 : end]
						currentProjectTrusted = false
					} else {
						currentProjectPath = ""
						currentProjectTrusted = false
					}
					continue
				}
				if currentProjectPath != "" &&
					strings.Contains(line, "trust_level") &&
					strings.Contains(line, "trusted") &&
					!currentProjectTrusted {
					trustedProjects = append(trustedProjects, currentProjectPath)
					currentProjectTrusted = true
				}
			}
			if len(trustedProjects) > 0 {
				a.Settings["trusted_projects"] = trustedProjects
			}
		}

		// Check for rules directory
		rulesDir := filepath.Join(configDir, "rules")
		if entries, err := os.ReadDir(rulesDir); err == nil {
			rules := []string{}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					rules = append(rules, e.Name())
				}
			}
			if len(rules) > 0 {
				a.Settings["rules"] = rules
			}
		}
	}

	return a
}

func detectOllama() DetectedAgent {
	a := DetectedAgent{
		Name:     "ollama",
		Provider: agent.ProviderOllama,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find ollama binary
	path, err := exec.LookPath("ollama")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("ollama", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check if ollama server is running and get models
	out, err = exec.Command("ollama", "list").Output()
	if err == nil {
		a.Status = "configured"
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		models := []string{}
		for i, line := range lines {
			if i == 0 {
				continue // Skip header
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				models = append(models, fields[0])
			}
		}
		if len(models) > 0 {
			a.Settings["models"] = models
			a.Settings["model"] = models[0] // Default to first model
		}
	}

	// Default base URL
	a.Settings["base_url"] = "http://localhost:11434"

	return a
}

func detectAider() DetectedAgent {
	a := DetectedAgent{
		Name:     "aider",
		Provider: agent.ProviderAider,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find aider binary
	path, err := exec.LookPath("aider")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("aider", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".aider.conf.yml")
	if _, err := os.Stat(configPath); err == nil {
		a.ConfigDir = filepath.Dir(configPath)
		a.ConfigFiles = []string{".aider.conf.yml"}
		a.Status = "configured"
	}

	return a
}

func detectMeldbot() DetectedAgent {
	a := DetectedAgent{
		Name:     "meldbot",
		Provider: agent.ProviderMeldbot,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find meldbot binary
	path, err := exec.LookPath("meldbot")
	if err != nil {
		// Also try "meld"
		path, err = exec.LookPath("meld")
		if err != nil {
			return a
		}
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command(path, "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".meldbot", "config.json")
	if _, err := os.Stat(configPath); err == nil {
		a.ConfigDir = filepath.Dir(configPath)
		a.ConfigFiles = []string{"config.json"}
		a.Status = "configured"
	}

	return a
}

func detectOpenclaw() DetectedAgent {
	a := DetectedAgent{
		Name:     "openclaw",
		Provider: agent.ProviderOpenclaw,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find openclaw binary
	path, err := exec.LookPath("openclaw")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("openclaw", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".openclaw", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		a.ConfigDir = filepath.Dir(configPath)
		a.ConfigFiles = []string{"config.yaml"}
		a.Status = "configured"
	}

	return a
}

func detectNanoclaw() DetectedAgent {
	a := DetectedAgent{
		Name:     "nanoclaw",
		Provider: agent.ProviderNanoclaw,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	// Find nanoclaw binary
	path, err := exec.LookPath("nanoclaw")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"

	// Get version
	out, err := exec.Command("nanoclaw", "--version").Output()
	if err == nil {
		a.Version = strings.TrimSpace(string(out))
	}

	// Check config
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".nanoclaw", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		a.ConfigDir = filepath.Dir(configPath)
		a.ConfigFiles = []string{"config.yaml"}
		a.Status = "configured"
	}

	return a
}

func detectGemini() DetectedAgent {
	a := DetectedAgent{
		Name:     "gemini",
		Provider: agent.ProviderGemini,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	path, err := exec.LookPath("gemini")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"
	if out, err := exec.Command("gemini", "--version").Output(); err == nil {
		a.Version = strings.TrimSpace(string(out))
	}
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".gemini")
	if _, err := os.Stat(configDir); err == nil {
		a.ConfigDir = configDir
		a.Status = "configured"
	}
	return a
}

func detectOpenAI() DetectedAgent {
	a := DetectedAgent{
		Name:     "openai",
		Provider: agent.ProviderOpenAI,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	path, err := exec.LookPath("openai")
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"
	if out, err := exec.Command("openai", "--version").Output(); err == nil {
		a.Version = strings.TrimSpace(string(out))
	}
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".openai")
	if _, err := os.Stat(configDir); err == nil {
		a.ConfigDir = configDir
		a.Status = "configured"
	}
	return a
}

func detectCopilotCLI() DetectedAgent {
	a := DetectedAgent{
		Name:     "copilot",
		Provider: agent.ProviderCustom,
		Status:   "not_found",
		Settings: make(map[string]any),
	}

	var path string
	var err error
	for _, candidate := range []string{"copilot", "github-copilot-cli"} {
		path, err = exec.LookPath(candidate)
		if err == nil {
			break
		}
	}
	if err != nil {
		return a
	}
	a.Path = path
	a.Status = "found"
	if out, err := exec.Command(path, "--version").Output(); err == nil {
		a.Version = strings.TrimSpace(string(out))
	}
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "github-copilot")
	if _, err := os.Stat(configDir); err == nil {
		a.ConfigDir = configDir
		a.Status = "configured"
	}
	return a
}

// mergeUniqueTags combines two tag slices, deduplicating by value.
// Used when updating an existing agent to preserve user-defined tags.
func mergeUniqueTags(existing, new []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(existing)+len(new))
	for _, t := range existing {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			result = append(result, t)
		}
	}
	for _, t := range new {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			result = append(result, t)
		}
	}
	return result
}
