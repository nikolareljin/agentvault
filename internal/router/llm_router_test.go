package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func llmRouterTestCandidates() []Candidate {
	return []Candidate{
		{
			Agent:  AgentView{Name: "codex-agent", Provider: agent.ProviderCodex},
			Target: agent.ExecutionTarget{Runner: agent.RunnerCodexCLI, Supported: true},
			Route: agent.RouteConfig{
				Capabilities: []string{"coding"},
				LatencyTier:  "low",
				CostTier:     "low",
				PrivacyTier:  "remote",
				Priority:     80,
			},
			Score: 80,
		},
		{
			Agent:  AgentView{Name: "ollama-local", Provider: agent.ProviderOllama},
			Target: agent.ExecutionTarget{Runner: agent.RunnerOllamaHTTP, Local: true, Supported: true},
			Route: agent.RouteConfig{
				Capabilities: []string{"general"},
				LatencyTier:  "low",
				CostTier:     "low",
				PrivacyTier:  "local",
				Priority:     60,
			},
			Score: 60,
		},
	}
}

func llmDecisionJSON(selected string) string {
	d := LLMRouterDecision{
		SelectedAgent:  selected,
		FallbackAgents: []string{"ollama-local"},
		Reasoning:      "best for coding tasks",
		Confidence:     0.9,
		RoutingFactors: RoutingFactors{
			Complexity:       6,
			TaskType:         "coding",
			RequiresTools:    true,
			PrivacySensitive: false,
			TimeSensitive:    false,
		},
		EstInputTokens:  120,
		EstOutputTokens: 400,
	}
	b, _ := json.Marshal(d)
	return string(b)
}

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcd", 1},
		{"hello world", 2},
		{strings.Repeat("a", 40), 10},
		{"こんにちは", 1}, // unicode runes counted
	}
	for _, c := range cases {
		got := estimateTokens(c.in)
		if got != c.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAnalyzeWithLLMRouter_ParsesDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": llmDecisionJSON("codex-agent")}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	candidates := llmRouterTestCandidates()
	cfg := LLMRouterConfig{URL: srv.URL, TimeoutSecs: 5}
	decision, err := AnalyzeWithLLMRouter(context.Background(), "write a sort function", candidates, cfg)
	if err != nil {
		t.Fatalf("AnalyzeWithLLMRouter() error = %v", err)
	}
	if decision.SelectedAgent != "codex-agent" {
		t.Errorf("SelectedAgent = %q, want codex-agent", decision.SelectedAgent)
	}
	if decision.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", decision.Confidence)
	}
	if decision.RoutingFactors.TaskType != "coding" {
		t.Errorf("TaskType = %q, want coding", decision.RoutingFactors.TaskType)
	}
}

func TestAnalyzeWithLLMRouter_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "not valid json at all"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	candidates := llmRouterTestCandidates()
	cfg := LLMRouterConfig{URL: srv.URL, TimeoutSecs: 5}
	_, err := AnalyzeWithLLMRouter(context.Background(), "hello", candidates, cfg)
	if err == nil {
		t.Error("expected error for invalid JSON decision, got nil")
	}
}

func TestAnalyzeWithLLMRouter_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	candidates := llmRouterTestCandidates()
	cfg := LLMRouterConfig{URL: srv.URL, TimeoutSecs: 5}
	_, err := AnalyzeWithLLMRouter(context.Background(), "hello", candidates, cfg)
	if err == nil {
		t.Error("expected error for 5xx server response, got nil")
	}
}

func TestAnalyzeWithLLMRouter_Unreachable(t *testing.T) {
	candidates := llmRouterTestCandidates()
	cfg := LLMRouterConfig{URL: "http://localhost:19998", TimeoutSecs: 1}
	_, err := AnalyzeWithLLMRouter(context.Background(), "hello", candidates, cfg)
	if err == nil {
		t.Error("expected error for unreachable server, got nil")
	}
}

func TestAnalyzeWithLLMRouter_EmptyURL(t *testing.T) {
	candidates := llmRouterTestCandidates()
	cfg := LLMRouterConfig{URL: "", TimeoutSecs: 5}
	_, err := AnalyzeWithLLMRouter(context.Background(), "hello", candidates, cfg)
	if err == nil {
		t.Error("expected error for empty URL, got nil")
	}
}

func TestEnrichIntentFromLLMDecision_Coding(t *testing.T) {
	intent := Intent{}
	d := LLMRouterDecision{RoutingFactors: RoutingFactors{TaskType: "coding", Complexity: 5}}
	enrichIntentFromLLMDecision(&intent, d)
	if !intent.Coding || intent.Review || intent.Analysis || intent.TaskClass != "coding" {
		t.Errorf("unexpected intent after coding decision: %+v", intent)
	}
}

func TestEnrichIntentFromLLMDecision_Review(t *testing.T) {
	intent := Intent{}
	d := LLMRouterDecision{RoutingFactors: RoutingFactors{TaskType: "review", Complexity: 4}}
	enrichIntentFromLLMDecision(&intent, d)
	if intent.Coding || !intent.Review || intent.TaskClass != "review" {
		t.Errorf("unexpected intent after review decision: %+v", intent)
	}
}

func TestEnrichIntentFromLLMDecision_HighComplexityAddsAnalysis(t *testing.T) {
	intent := Intent{}
	d := LLMRouterDecision{RoutingFactors: RoutingFactors{TaskType: "coding", Complexity: 9}}
	enrichIntentFromLLMDecision(&intent, d)
	if !intent.Coding || !intent.Analysis {
		t.Errorf("expected Coding+Analysis for complexity 9, got %+v", intent)
	}
}

func TestEnrichIntentFromLLMDecision_Privacy(t *testing.T) {
	intent := Intent{}
	d := LLMRouterDecision{RoutingFactors: RoutingFactors{PrivacySensitive: true, TimeSensitive: true, TaskType: "general"}}
	enrichIntentFromLLMDecision(&intent, d)
	if !intent.PrivacySensitive {
		t.Error("expected PrivacySensitive=true")
	}
	if !intent.LatencySensitive {
		t.Error("expected LatencySensitive=true")
	}
}

func TestEnrichIntentFromLLMDecision_UnknownTaskType(t *testing.T) {
	intent := Intent{}
	d := LLMRouterDecision{RoutingFactors: RoutingFactors{TaskType: "something-exotic"}}
	enrichIntentFromLLMDecision(&intent, d)
	if intent.TaskClass != "general" {
		t.Errorf("unknown task type should default to general, got %q", intent.TaskClass)
	}
}

func TestBuildRoutingSystemPrompt_ContainsAgentNames(t *testing.T) {
	candidates := llmRouterTestCandidates()
	prompt := buildRoutingSystemPrompt(candidates)
	for _, c := range candidates {
		if !strings.Contains(prompt, c.Agent.Name) {
			t.Errorf("system prompt missing agent name %q", c.Agent.Name)
		}
	}
}

func TestNormalizeLLMRouterDecision(t *testing.T) {
	d := LLMRouterDecision{
		Confidence:     1.5,
		RoutingFactors: RoutingFactors{Complexity: 0, TaskType: "UNKNOWN"},
	}
	n := normalizeLLMRouterDecision(d)
	if n.Confidence != 1.0 {
		t.Errorf("confidence should be clamped to 1.0, got %v", n.Confidence)
	}
	if n.RoutingFactors.Complexity != 1 {
		t.Errorf("complexity 0 should normalize to 1, got %d", n.RoutingFactors.Complexity)
	}
	if n.RoutingFactors.TaskType != "general" {
		t.Errorf("unknown task type should normalize to general, got %q", n.RoutingFactors.TaskType)
	}
}
