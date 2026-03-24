package router

import (
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestRoutePrefersLocalOllamaForGeneralPrompt(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{
			{Name: "codex", Provider: agent.ProviderCodex},
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
		},
		Shared: agent.SharedConfig{},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local", decision.Selected.Agent.Name)
	}
}

func TestRoutePrefersCodingTargetForCodePrompt(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Implement and test this Go refactor.",
		Agents: []agent.Agent{
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
			{Name: "codex", Provider: agent.ProviderCodex, Model: "gpt-5-codex"},
		},
		Shared: agent.SharedConfig{},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "codex" {
		t.Fatalf("selected agent = %q, want codex", decision.Selected.Agent.Name)
	}
}

func TestRouteLocalOnlyRejectsRemoteTargets(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Private local only code review.",
		Agents: []agent.Agent{
			{Name: "codex", Provider: agent.ProviderCodex, Model: "gpt-5-codex"},
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
		},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{LocalOnly: true},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local", decision.Selected.Agent.Name)
	}
}
