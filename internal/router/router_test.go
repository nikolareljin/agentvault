package router

import (
	"encoding/json"
	"strings"
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

func TestRouteLocalOnlyErrorsWithoutSupportedLocalTarget(t *testing.T) {
	_, err := Route(Request{
		Prompt: "Private local only code review.",
		Agents: []agent.Agent{{Name: "codex", Provider: agent.ProviderCodex, Model: "gpt-5-codex"}},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{LocalOnly: true},
	})
	if err == nil {
		t.Fatalf("expected local-only routing error")
	}
	if !strings.Contains(err.Error(), "no supported routing target satisfies the current policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteDecisionJSONDoesNotLeakSecrets(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Implement and test this Go refactor.",
		Agents: []agent.Agent{{
			Name:     "codex",
			Provider: agent.ProviderOpenAI,
			Model:    "gpt-5",
			APIKey:   "sk-secret-value",
			BaseURL:  "https://api.openai.com",
		}},
		Shared: agent.SharedConfig{},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("json.Marshal(decision) error = %v", err)
	}
	if strings.Contains(string(raw), "sk-secret-value") {
		t.Fatalf("routing decision leaked secret-bearing agent config: %s", string(raw))
	}
	if !strings.Contains(string(raw), "codex") {
		t.Fatalf("routing decision missing agent metadata: %s", string(raw))
	}
}

func TestClassifyPromptDoesNotTreatPleaseAsCoding(t *testing.T) {
	intent := classifyPrompt("Please summarize this design doc.")
	if intent.Coding {
		t.Fatalf("intent.Coding = true, want false for non-code prompt")
	}
}
