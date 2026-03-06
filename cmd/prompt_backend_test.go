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
