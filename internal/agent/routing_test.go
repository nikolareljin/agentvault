package agent

import "testing"

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

func TestResolveExecutionTarget(t *testing.T) {
	tests := []struct {
		name      string
		agent     Agent
		runner    RunnerKind
		local     bool
		supported bool
	}{
		{name: "ollama local", agent: Agent{Name: "ollama", Provider: ProviderOllama}, runner: RunnerOllamaHTTP, local: true, supported: true},
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
