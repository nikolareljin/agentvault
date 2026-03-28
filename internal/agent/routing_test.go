package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteConfigValidateRejectsUnknownCapability(t *testing.T) {
	cfg := RouteConfig{Capabilities: []string{"coding", "unknown"}}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for unknown capability")
	}
}

func TestRouterConfigWithDefaultsDoesNotForceAllowFallbacks(t *testing.T) {
	cfg := (RouterConfig{}).WithDefaults()
	if !cfg.PreferLocal {
		t.Fatalf("PreferLocal = false, want true default")
	}
	if cfg.AllowFallbacks {
		t.Fatalf("AllowFallbacks = true, want false when unset")
	}
}

func TestRouterConfigValidateRejectsUnknownMode(t *testing.T) {
	cfg := RouterConfig{Mode: "langgrpah"}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected validation error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown router mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveExecutionTarget(t *testing.T) {
	tests := []struct {
		name      string
		agent     Agent
		runner    RunnerKind
		local     bool
		supported bool
	}{
		{name: "ollama implicit local default", agent: Agent{Name: "ollama", Provider: ProviderOllama}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama explicit localhost", agent: Agent{Name: "ollama-local", Provider: ProviderOllama, BaseURL: "http://localhost:11434"}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama explicit loopback", agent: Agent{Name: "ollama-loopback", Provider: ProviderOllama, BaseURL: "http://127.0.0.1:11434"}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama remote https", agent: Agent{Name: "ollama-remote", Provider: ProviderOllama, BaseURL: "https://remote.example"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "ollama hostname without scheme is not local", agent: Agent{Name: "ollama-noscheme-remote", Provider: ProviderOllama, BaseURL: "remote.example:443"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "ollama localhost without scheme is not local", agent: Agent{Name: "ollama-noscheme-localhost", Provider: ProviderOllama, BaseURL: "localhost:11434"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "codex cli", agent: Agent{Name: "codex", Provider: ProviderCodex}, runner: RunnerCodexCLI, local: false, supported: true},
		{name: "openai http", agent: Agent{Name: "openai", Provider: ProviderOpenAI}, runner: RunnerOpenAIHTTP, local: false, supported: true},
		{name: "claude cli", agent: Agent{Name: "claude", Provider: ProviderClaude}, runner: RunnerClaudeCLI, local: false, supported: true},
		{name: "claude ollama", agent: Agent{Name: "claude-local", Provider: ProviderClaude, Backend: ClaudeBackendOllama}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "claude bedrock unsupported", agent: Agent{Name: "claude-bedrock", Provider: ProviderClaude, Backend: ClaudeBackendBedrock}, runner: RunnerBedrockAPI, local: false, supported: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveExecutionTarget(tt.agent)
			if got.Runner != tt.runner || got.Local != tt.local || got.Supported != tt.supported {
				t.Fatalf("ResolveExecutionTarget() = %#v", got)
			}
		})
	}
}

func TestExecutionTargetJSONOmitsBaseURL(t *testing.T) {
	target := ExecutionTarget{
		AgentName: "openai",
		Provider:  ProviderOpenAI,
		Runner:    RunnerOpenAIHTTP,
		BaseURL:   "https://user:secret@example.com/v1?token=secret",
		Local:     false,
		Supported: true,
	}
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("json.Marshal(target) error = %v", err)
	}
	if strings.Contains(string(raw), "base_url") || strings.Contains(string(raw), "secret") {
		t.Fatalf("expected marshaled target to omit base_url secrets, got: %s", string(raw))
	}
}
