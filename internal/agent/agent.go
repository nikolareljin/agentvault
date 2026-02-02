package agent

import (
	"errors"
	"time"
)

// Provider represents a supported AI provider.
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderGemini Provider = "gemini"
	ProviderCodex  Provider = "codex"
	ProviderOllama Provider = "ollama"
	ProviderOpenAI Provider = "openai"
	ProviderCustom Provider = "custom"
)

// MCPServer represents a Model Context Protocol server configuration.
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Agent represents a configured AI agent.
type Agent struct {
	Name         string      `json:"name"`
	Provider     Provider    `json:"provider"`
	Model        string      `json:"model"`
	APIKey       string      `json:"api_key,omitempty"`
	BaseURL      string      `json:"base_url,omitempty"`
	SystemPrompt string      `json:"system_prompt,omitempty"`
	TaskDesc     string      `json:"task_description,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	MCPServers   []MCPServer `json:"mcp_servers,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// InstructionFile represents a stored instruction file (e.g. AGENTS.md, CLAUDE.md).
type InstructionFile struct {
	Name      string    `json:"name"`     // key, e.g. "agents", "claude"
	Filename  string    `json:"filename"` // target filename, e.g. "AGENTS.md"
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WellKnownInstructions maps common names to their conventional filenames.
var WellKnownInstructions = map[string]string{
	"agents":  "AGENTS.md",
	"claude":  "CLAUDE.md",
	"codex":   "codex.md",
	"copilot": ".github/copilot-instructions.md",
}

// FilenameForInstruction returns the conventional filename for a name,
// or the name itself with .md appended if not well-known.
func FilenameForInstruction(name string) string {
	if fn, ok := WellKnownInstructions[name]; ok {
		return fn
	}
	return name + ".md"
}

// SharedConfig holds global settings that apply to all agents unless overridden.
type SharedConfig struct {
	SystemPrompt string            `json:"system_prompt,omitempty"`
	MCPServers   []MCPServer       `json:"mcp_servers,omitempty"`
	Instructions []InstructionFile `json:"instructions,omitempty"`
}

// ValidProviders returns all known provider values.
func ValidProviders() []Provider {
	return []Provider{
		ProviderClaude, ProviderGemini, ProviderCodex,
		ProviderOllama, ProviderOpenAI, ProviderCustom,
	}
}

// Validate checks that required fields are populated.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return errors.New("agent name is required")
	}
	if a.Provider == "" {
		return errors.New("agent provider is required")
	}
	valid := false
	for _, p := range ValidProviders() {
		if a.Provider == p {
			valid = true
			break
		}
	}
	if !valid {
		return errors.New("unknown provider: " + string(a.Provider))
	}
	return nil
}

// EffectiveSystemPrompt returns the agent's system prompt, falling back to the
// shared config prompt if the agent has none.
func (a *Agent) EffectiveSystemPrompt(shared SharedConfig) string {
	if a.SystemPrompt != "" {
		return a.SystemPrompt
	}
	return shared.SystemPrompt
}

// EffectiveMCPServers returns the agent's MCP servers merged with shared ones.
// Agent-specific servers with the same name override shared ones.
func (a *Agent) EffectiveMCPServers(shared SharedConfig) []MCPServer {
	seen := make(map[string]struct{})
	var result []MCPServer
	for _, s := range a.MCPServers {
		seen[s.Name] = struct{}{}
		result = append(result, s)
	}
	for _, s := range shared.MCPServers {
		if _, ok := seen[s.Name]; !ok {
			result = append(result, s)
		}
	}
	return result
}
