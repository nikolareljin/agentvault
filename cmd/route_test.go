package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

func TestReadPromptInputReturnsHelpfulNoPromptError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("text", "", "")
	cmd.Flags().String("file", "", "")

	_, err := readPromptInput(cmd)
	if err == nil {
		t.Fatalf("expected missing prompt error")
	}
	if !strings.Contains(err.Error(), "no prompt provided") {
		t.Fatalf("unexpected error: %v", err)
	}
}
