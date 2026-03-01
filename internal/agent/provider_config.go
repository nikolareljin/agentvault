// Provider-specific configuration loading and saving.
//
// Each supported provider stores its configuration differently:
//   - Claude: ~/.claude/settings.json, ~/.claude/keybindings.json
//   - Codex:  ~/.codex/config.toml, ~/.codex/rules/*.md
//   - Ollama: OLLAMA_HOST environment variable
//
// These Load/Save functions bridge between the provider's native format
// and AgentVault's unified storage, enabling export/import of provider
// settings alongside agent configurations.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProviderConfig holds provider-specific configuration that can be synced
// across machines. Each provider's config is optional (nil if not configured).
type ProviderConfig struct {
	Claude *ClaudeConfig `json:"claude,omitempty"`
	Codex  *CodexConfig  `json:"codex,omitempty"`
	Ollama *OllamaConfig `json:"ollama,omitempty"`
}

// ClaudeConfig holds Claude Code specific settings.
type ClaudeConfig struct {
	EnabledPlugins map[string]bool   `json:"enabledPlugins,omitempty"`
	Keybindings    map[string]string `json:"keybindings,omitempty"`
	DefaultModel   string            `json:"defaultModel,omitempty"`
	AutoApprove    []string          `json:"autoApprove,omitempty"`
	MCPServers     map[string]any    `json:"mcpServers,omitempty"`
	CustomSettings map[string]any    `json:"customSettings,omitempty"`
}

// CodexConfig holds Codex CLI specific settings.
type CodexConfig struct {
	TrustedProjects map[string]string `json:"trustedProjects,omitempty"` // path -> trust_level
	DefaultModel    string            `json:"defaultModel,omitempty"`
	Rules           map[string]string `json:"rules,omitempty"` // name -> content
	CustomSettings  map[string]any    `json:"customSettings,omitempty"`
}

// OllamaConfig holds Ollama specific settings.
type OllamaConfig struct {
	BaseURL        string         `json:"baseUrl,omitempty"`
	DefaultModel   string         `json:"defaultModel,omitempty"`
	Models         []string       `json:"models,omitempty"`
	CustomSettings map[string]any `json:"customSettings,omitempty"`
}

// LoadClaudeConfig reads Claude configuration from ~/.claude/
func LoadClaudeConfig() (*ClaudeConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	config := &ClaudeConfig{
		EnabledPlugins: make(map[string]bool),
		Keybindings:    make(map[string]string),
		CustomSettings: make(map[string]any),
	}

	claudeDir := filepath.Join(home, ".claude")

	// Read settings.json
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var settings map[string]any
		if json.Unmarshal(data, &settings) == nil {
			if plugins, ok := settings["enabledPlugins"].(map[string]any); ok {
				for name, enabled := range plugins {
					if e, ok := enabled.(bool); ok {
						config.EnabledPlugins[name] = e
					}
				}
			}
			if model, ok := settings["defaultModel"].(string); ok {
				config.DefaultModel = model
			}
			if autoApprove, ok := anyToStringSlice(settings["autoApprove"]); ok {
				config.AutoApprove = autoApprove
			}
			if mcpServers, ok := settings["mcpServers"].(map[string]any); ok {
				config.MCPServers = make(map[string]any, len(mcpServers))
				for name, server := range mcpServers {
					config.MCPServers[name] = server
				}
			}
			// Store all other settings
			for k, v := range settings {
				if k != "enabledPlugins" && k != "defaultModel" && k != "autoApprove" && k != "mcpServers" {
					config.CustomSettings[k] = v
				}
			}
		}
	}

	// Read keybindings.json
	keybindingsPath := filepath.Join(claudeDir, "keybindings.json")
	if data, err := os.ReadFile(keybindingsPath); err == nil {
		json.Unmarshal(data, &config.Keybindings)
	}

	return config, nil
}

// SaveClaudeConfig writes Claude configuration to ~/.claude/
func SaveClaudeConfig(config *ClaudeConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		return err
	}

	// Build settings.json
	settings := make(map[string]any)
	for k, v := range config.CustomSettings {
		settings[k] = v
	}
	if len(config.EnabledPlugins) > 0 {
		settings["enabledPlugins"] = config.EnabledPlugins
	}
	if config.DefaultModel != "" {
		settings["defaultModel"] = config.DefaultModel
	}
	if len(config.AutoApprove) > 0 {
		settings["autoApprove"] = config.AutoApprove
	}
	if len(config.MCPServers) > 0 {
		settings["mcpServers"] = config.MCPServers
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		return err
	}

	// Write keybindings.json if present
	if len(config.Keybindings) > 0 {
		data, err := json.MarshalIndent(config.Keybindings, "", "  ")
		if err != nil {
			return err
		}
		keybindingsPath := filepath.Join(claudeDir, "keybindings.json")
		if err := os.WriteFile(keybindingsPath, data, 0600); err != nil {
			return err
		}
	} else {
		keybindingsPath := filepath.Join(claudeDir, "keybindings.json")
		if err := os.Remove(keybindingsPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

// LoadCodexConfig reads Codex configuration from ~/.codex/
func LoadCodexConfig() (*CodexConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	config := &CodexConfig{
		TrustedProjects: make(map[string]string),
		Rules:           make(map[string]string),
		CustomSettings:  make(map[string]any),
	}

	codexDir := filepath.Join(home, ".codex")

	// Read config.toml (simple parsing for trusted projects)
	configPath := filepath.Join(codexDir, "config.toml")
	if data, err := os.ReadFile(configPath); err == nil {
		lines := strings.Split(string(data), "\n")
		currentProject := ""
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[projects.") {
				// Extract path from [projects."/path/here"]
				start := strings.Index(line, `"`)
				end := strings.LastIndex(line, `"`)
				if start != -1 && end > start {
					currentProject = line[start+1 : end]
				}
			} else if strings.Contains(line, "trust_level") && currentProject != "" {
				// Extract trust level
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					level := strings.TrimSpace(parts[1])
					level = strings.Trim(level, `"`)
					config.TrustedProjects[currentProject] = level
				}
			}
		}
	}

	// Read rules from ~/.codex/rules/
	rulesDir := filepath.Join(codexDir, "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				rulePath := filepath.Join(rulesDir, e.Name())
				if data, err := os.ReadFile(rulePath); err == nil {
					name := strings.TrimSuffix(e.Name(), ".md")
					config.Rules[name] = string(data)
				}
			}
		}
	}

	return config, nil
}

// SaveCodexConfig writes Codex configuration to ~/.codex/
func SaveCodexConfig(config *CodexConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		return err
	}

	// Build config.toml (write even when empty so overwrite/clear is possible).
	var sb strings.Builder
	paths := make([]string, 0, len(config.TrustedProjects))
	for path := range config.TrustedProjects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		level := config.TrustedProjects[path]
		sb.WriteString(fmt.Sprintf("[projects.%q]\n", path))
		sb.WriteString(fmt.Sprintf("trust_level = %q\n\n", level))
	}
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(sb.String()), 0600); err != nil {
		return err
	}

	// Write rules and remove stale entries to support overwrite semantics.
	rulesDir := filepath.Join(codexDir, "rules")
	if err := os.MkdirAll(rulesDir, 0700); err != nil {
		return err
	}
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			if _, keep := config.Rules[name]; keep {
				continue
			}
			if err := os.Remove(filepath.Join(rulesDir, e.Name())); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	ruleNames := make([]string, 0, len(config.Rules))
	for name := range config.Rules {
		ruleNames = append(ruleNames, name)
	}
	sort.Strings(ruleNames)
	for _, name := range ruleNames {
		rulePath := filepath.Join(rulesDir, name+".md")
		if err := os.WriteFile(rulePath, []byte(config.Rules[name]), 0600); err != nil {
			return err
		}
	}

	return nil
}

// LoadOllamaConfig creates Ollama configuration from running instance
func LoadOllamaConfig() (*OllamaConfig, error) {
	config := &OllamaConfig{
		BaseURL:        "http://localhost:11434",
		CustomSettings: make(map[string]any),
	}

	// Check OLLAMA_HOST env
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		config.BaseURL = host
	}

	return config, nil
}

func anyToStringSlice(v any) ([]string, bool) {
	if v == nil {
		return nil, false
	}
	if values, ok := v.([]string); ok {
		return append([]string(nil), values...), true
	}
	values, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		s, ok := raw.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
