package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestValidatePromptBackend_BedrockReturnsExplicitError(t *testing.T) {
	a := agent.Agent{
		Name:     "claude-bedrock",
		Provider: agent.ProviderClaude,
		Backend:  agent.ClaudeBackendBedrock,
	}

	err := validatePromptBackend(a, time.Second)
	if err == nil {
		t.Fatalf("expected error for bedrock validation")
	}
	if !strings.Contains(err.Error(), "bedrock backend validation is not supported yet") {
		t.Fatalf("unexpected bedrock validation error: %v", err)
	}
}

func TestExecutePrompt_BedrockReturnsExplicitError(t *testing.T) {
	a := agent.Agent{
		Name:     "claude-bedrock",
		Provider: agent.ProviderClaude,
		Backend:  agent.ClaudeBackendBedrock,
	}

	_, err := executePrompt(a, "hello", time.Second)
	if err == nil {
		t.Fatalf("expected error for bedrock execution")
	}
	if !strings.Contains(err.Error(), "bedrock backend execution is not supported yet") {
		t.Fatalf("unexpected bedrock execution error: %v", err)
	}
}

func TestValidateOllamaEndpoint(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	if err := validateOllamaEndpoint(okServer.URL, time.Second, "ollama validation"); err != nil {
		t.Fatalf("validateOllamaEndpoint() error = %v", err)
	}

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer failServer.Close()

	err := validateOllamaEndpoint(failServer.URL, time.Second, "ollama validation")
	if err == nil {
		t.Fatalf("expected status error")
	}
	if !strings.Contains(err.Error(), "ollama validation failed (400): bad request") {
		t.Fatalf("unexpected status error: %v", err)
	}
}

func TestEffectivePromptBackend(t *testing.T) {
	tests := []struct {
		name string
		a    agent.Agent
		want string
	}{
		{
			name: "claude defaults to anthropic",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  "",
			},
			want: agent.ClaudeBackendAnthropic,
		},
		{
			name: "claude explicit backend",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  agent.ClaudeBackendOllama,
			},
			want: agent.ClaudeBackendOllama,
		},
		{
			name: "non-claude returns provider",
			a: agent.Agent{
				Provider: agent.ProviderCodex,
			},
			want: string(agent.ProviderCodex),
		},
		{
			name: "invalid claude backend returns normalized raw value",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  "  CUSTOM  ",
			},
			want: "custom",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := effectivePromptBackend(tt.a)
			if got != tt.want {
				t.Fatalf("effectivePromptBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}
