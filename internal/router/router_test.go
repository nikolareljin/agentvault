package router

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRouteDoesNotRejectSupportedCandidatesForVeryLowPriority(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{
			{
				Name:     "local",
				Provider: agent.ProviderOllama,
				Model:    "llama3.2",
				Route:    agent.RouteConfig{Priority: -5000},
			},
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

func TestRouteWithLangGraphKeepsSelectedAndCandidatesReasonsInSync(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "router.py")
	script := `import json, sys
json.load(sys.stdin)
json.dump({"mode": "langgraph", "selected_agent": "local", "reasons": ["langgraph picked local"]}, sys.stdout)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(scriptPath) error = %v", err)
	}

	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if got := decision.Selected.Reasons; len(got) == 0 || got[len(got)-1] != "langgraph picked local" {
		t.Fatalf("selected reasons = %#v, want appended langgraph reason", got)
	}
	if got := decision.Candidates[0].Reasons; len(got) == 0 || got[len(got)-1] != "langgraph picked local" {
		t.Fatalf("candidate reasons = %#v, want appended langgraph reason", got)
	}
}

func TestRouteRejectsEmptyPromptBeforeLangGraph(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "router.py")
	script := `import json, sys
json.load(sys.stdin)
json.dump({"mode": "langgraph", "selected_agent": "local"}, sys.stdout)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(scriptPath) error = %v", err)
	}

	_, err := Route(Request{
		Prompt: "   ",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath},
	})
	if err == nil {
		t.Fatalf("expected empty-prompt routing error")
	}
	if !strings.Contains(err.Error(), "non-empty prompt") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteRejectsUnknownRouterMode(t *testing.T) {
	_, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Config: agent.RouterConfig{Mode: "langgrpah"},
	})
	if err == nil {
		t.Fatalf("expected router mode validation error")
	}
	if !strings.Contains(err.Error(), "unknown router mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteWithLangGraphRejectsEmptySelectedAgent(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "router.py")
	script := `import json, sys
json.load(sys.stdin)
json.dump({"mode": "langgraph", "selected_agent": "   "}, sys.stdout)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(scriptPath) error = %v", err)
	}

	_, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath},
	})
	if err == nil {
		t.Fatalf("expected empty selected_agent error")
	}
	if !strings.Contains(err.Error(), "selected_agent is empty or whitespace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteWithLangGraphSupportsDashPrefixedScriptPath(t *testing.T) {
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "-router.py")
	script := `import json, sys
json.load(sys.stdin)
json.dump({"mode": "langgraph", "selected_agent": "local"}, sys.stdout)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(scriptPath) error = %v", err)
	}

	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local", decision.Selected.Agent.Name)
	}
}

func TestRouteWithLangGraphPythonRouterSkipsUnsupportedAndLocalOnlyViolations(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "python", "langgraph_router.py")

	decision, err := Route(Request{
		Prompt: "Private local only code review.",
		Agents: []agent.Agent{
			{Name: "remote", Provider: agent.ProviderCodex, Model: "gpt-5-codex"},
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2", Route: agent.RouteConfig{Priority: -50}},
		},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath, LocalOnly: true},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local", decision.Selected.Agent.Name)
	}
}

func TestRouteWithLangGraphFallsBackWhenLangGraphRuntimeFails(t *testing.T) {
	tempDir := t.TempDir()
	langGraphDir := filepath.Join(tempDir, "langgraph")
	if err := os.MkdirAll(langGraphDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(langGraphDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(langGraphDir, "__init__.py"), []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(__init__.py) error = %v", err)
	}
	graphPy := `START = "start"
END = "end"
class StateGraph:
    def __init__(self, _state):
        pass
    def add_node(self, *_args, **_kwargs):
        pass
    def add_edge(self, *_args, **_kwargs):
        pass
    def compile(self):
        raise RuntimeError("boom")
`
	if err := os.WriteFile(filepath.Join(langGraphDir, "graph.py"), []byte(graphPy), 0o600); err != nil {
		t.Fatalf("WriteFile(graph.py) error = %v", err)
	}
	t.Setenv("PYTHONPATH", tempDir)

	scriptPath := filepath.Join("..", "..", "python", "langgraph_router.py")
	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"}},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: scriptPath, AllowFallbacks: true},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Mode != "langgraph" && decision.Mode != "python-fallback" {
		t.Fatalf("decision.Mode = %q, want python-fallback-compatible mode", decision.Mode)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local", decision.Selected.Agent.Name)
	}
}
