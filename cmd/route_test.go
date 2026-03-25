package cmd

import (
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestRouteCommandRejectsUnexpectedArgs(t *testing.T) {
	if err := routeCmd.Args(routeCmd, []string{"extra"}); err == nil {
		t.Fatalf("expected route command to reject extra positional args")
	}
}

func TestResolvedRoutingAgentsAppliesRuntimeBaseURL(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "https://remote.example")

	resolved := resolvedRoutingAgents([]agent.Agent{{
		Name:     "local",
		Provider: agent.ProviderOllama,
		Model:    "llama3.2",
	}})
	if len(resolved) != 1 {
		t.Fatalf("len(resolved) = %d, want 1", len(resolved))
	}
	if resolved[0].BaseURL != "https://remote.example" {
		t.Fatalf("resolved base URL = %q, want env override", resolved[0].BaseURL)
	}
}
