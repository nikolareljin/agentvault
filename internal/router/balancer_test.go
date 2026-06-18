package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func candidateWithURL(name, baseURL string) Candidate {
	return Candidate{
		Agent:  AgentView{Name: name},
		Target: agent.ExecutionTarget{BaseURL: baseURL, Supported: true},
		Score:  50,
	}
}

func candidateCLI(name string) Candidate {
	return Candidate{
		Agent:  AgentView{Name: name},
		Target: agent.ExecutionTarget{Runner: agent.RunnerClaudeCLI, Supported: true},
		Score:  70,
	}
}

func TestBalancerCheckHealth_HealthyHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewBalancer()
	c := candidateWithURL("http-agent", srv.URL)
	if !b.CheckHealth(context.Background(), c) {
		t.Error("expected healthy HTTP server to return true")
	}
}

func TestBalancerCheckHealth_Unreachable(t *testing.T) {
	b := NewBalancer()
	c := candidateWithURL("dead-agent", "http://localhost:19997")
	if b.CheckHealth(context.Background(), c) {
		t.Error("expected unreachable server to return false")
	}
}

func TestBalancerCheckHealth_CLIAgentAssumedHealthy(t *testing.T) {
	b := NewBalancer()
	c := candidateCLI("claude-cli")
	if !b.CheckHealth(context.Background(), c) {
		t.Error("CLI agents with no BaseURL should always be healthy")
	}
}

func TestBalancerCircuitBreaker_TripsAfterMaxFailures(t *testing.T) {
	b := NewBalancer()

	for i := 0; i < balancerMaxFailures; i++ {
		b.RecordFailure("broken-agent")
	}

	h := b.health["broken-agent"]
	if h.Available {
		t.Error("circuit breaker should have tripped: Available should be false after max failures")
	}
	if h.ConsecFailures != balancerMaxFailures {
		t.Errorf("ConsecFailures = %d, want %d", h.ConsecFailures, balancerMaxFailures)
	}
}

func TestBalancerRecordSuccess_ResetsCircuitBreaker(t *testing.T) {
	b := NewBalancer()
	for i := 0; i < balancerMaxFailures; i++ {
		b.RecordFailure("agent")
	}
	b.RecordSuccess("agent", 50.0)

	h := b.health["agent"]
	if !h.Available {
		t.Error("RecordSuccess should restore availability")
	}
	if h.ConsecFailures != 0 {
		t.Errorf("ConsecFailures = %d, want 0 after success", h.ConsecFailures)
	}
}

func TestBalancerEWMALatency(t *testing.T) {
	b := NewBalancer()
	b.RecordSuccess("agent", 100.0) // first sample seeds directly
	b.RecordSuccess("agent", 100.0)
	b.RecordSuccess("agent", 100.0)

	h := b.health["agent"]
	if h.AvgLatencyMs < 90 || h.AvgLatencyMs > 110 {
		t.Errorf("EWMA latency = %.1f, expected ~100", h.AvgLatencyMs)
	}
}

func TestBalancerCooldown_BlocksRecheckBeforeCooldown(t *testing.T) {
	b := NewBalancer()
	c := candidateWithURL("cool-agent", "http://localhost:19996")
	key := balancerHealthKey(c, c.Target.BaseURL)
	for i := 0; i < balancerMaxFailures; i++ {
		b.RecordFailure(key)
	}
	// Set LastCheck to just now — cooldown not expired.
	b.mu.Lock()
	b.health[key].LastCheck = time.Now()
	b.mu.Unlock()

	if b.CheckHealth(context.Background(), c) {
		t.Error("should not recheck during cooldown period")
	}
}

func TestBalancerHealthKeySeparatesSameAgentDifferentEndpoints(t *testing.T) {
	b := NewBalancer()
	oldEndpoint := candidateWithURL("shared-agent", "http://localhost:19996")
	oldKey := balancerHealthKey(oldEndpoint, oldEndpoint.Target.BaseURL)
	for i := 0; i < balancerMaxFailures; i++ {
		b.RecordFailure(oldKey)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	newEndpoint := candidateWithURL("shared-agent", srv.URL)
	if !b.CheckHealth(context.Background(), newEndpoint) {
		t.Fatal("same agent name with a different healthy endpoint should not inherit old cooldown state")
	}
}

func TestBalancerPickBest_SelectsLLMChoice(t *testing.T) {
	b := NewBalancer()
	candidates := []Candidate{
		{Agent: AgentView{Name: "codex"}, Target: agent.ExecutionTarget{Supported: true}, Score: 80},
		{Agent: AgentView{Name: "ollama"}, Target: agent.ExecutionTarget{Supported: true}, Score: 60},
	}
	decision := LLMRouterDecision{SelectedAgent: "ollama"}
	selected, err := b.PickBest(context.Background(), decision, candidates)
	if err != nil {
		t.Fatalf("PickBest() error = %v", err)
	}
	if selected.Agent.Name != "ollama" {
		t.Errorf("PickBest selected %q, want ollama (LLM choice)", selected.Agent.Name)
	}
}

func TestBalancerPickBest_FallsBackToHighestScore(t *testing.T) {
	b := NewBalancer()
	candidates := []Candidate{
		{Agent: AgentView{Name: "codex"}, Target: agent.ExecutionTarget{Supported: true}, Score: 80},
		{Agent: AgentView{Name: "ollama"}, Target: agent.ExecutionTarget{Supported: true}, Score: 60},
	}
	// LLM chose an agent that doesn't exist in candidates.
	decision := LLMRouterDecision{SelectedAgent: "nonexistent-agent"}
	selected, err := b.PickBest(context.Background(), decision, candidates)
	if err != nil {
		t.Fatalf("PickBest() error = %v", err)
	}
	// Should fall back to highest score (codex at 80).
	if selected.Agent.Name != "codex" {
		t.Errorf("PickBest fallback selected %q, want codex (highest score)", selected.Agent.Name)
	}
}

func TestBalancerPickBest_EmptyCandidatesReturnsError(t *testing.T) {
	b := NewBalancer()
	_, err := b.PickBest(context.Background(), LLMRouterDecision{}, nil)
	if err == nil {
		t.Error("expected error for empty candidates, got nil")
	}
}

func TestBalancerPickBest_UsesFallbackAgents(t *testing.T) {
	b := NewBalancer()

	// Primary unreachable, fallback present.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	candidates := []Candidate{
		{Agent: AgentView{Name: "primary"}, Target: agent.ExecutionTarget{BaseURL: "http://localhost:19995", Supported: true}, Score: 90},
		{Agent: AgentView{Name: "fallback"}, Target: agent.ExecutionTarget{BaseURL: srv.URL, Supported: true}, Score: 70},
	}
	decision := LLMRouterDecision{
		SelectedAgent:  "primary",
		FallbackAgents: []string{"fallback"},
	}
	selected, err := b.PickBest(context.Background(), decision, candidates)
	if err != nil {
		t.Fatalf("PickBest() error = %v", err)
	}
	if selected.Agent.Name != "fallback" {
		t.Errorf("expected fallback agent when primary unreachable, got %q", selected.Agent.Name)
	}
}
