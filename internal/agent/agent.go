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

// Agent represents a configured AI agent.
type Agent struct {
	Name         string    `json:"name"`
	Provider     Provider  `json:"provider"`
	Model        string    `json:"model"`
	APIKey       string    `json:"api_key,omitempty"`
	BaseURL      string    `json:"base_url,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	TaskDesc     string    `json:"task_description,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
