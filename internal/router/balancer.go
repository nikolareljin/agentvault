package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

const (
	balancerHealthTimeout = 2 * time.Second
	balancerCooldown      = 60 * time.Second
	balancerMaxFailures   = 3
	balancerEWMADecay     = 0.8
)

// ProviderHealth tracks liveness and latency for one agent endpoint.
type ProviderHealth struct {
	Available      bool
	ConsecFailures int
	LastCheck      time.Time
	AvgLatencyMs   float64 // EWMA: avg = avg*0.8 + sample*0.2
}

// Balancer tracks health and latency for routing candidates, applying a circuit breaker
// after repeated failures and exponential weighted moving average latency tracking.
type Balancer struct {
	mu     sync.RWMutex
	health map[string]*ProviderHealth
}

// NewBalancer returns a ready-to-use Balancer.
func NewBalancer() *Balancer {
	return &Balancer{health: make(map[string]*ProviderHealth)}
}

func (b *Balancer) getOrCreate(name string) *ProviderHealth {
	h, ok := b.health[name]
	if !ok {
		h = &ProviderHealth{Available: true}
		b.health[name] = h
	}
	return h
}

// CheckHealth pings the candidate's HTTP endpoint. CLI runners (Claude, Codex, Gemini)
// have no HTTP endpoint to probe and are always considered healthy. HTTP runners (Ollama,
// OpenAI-compat) are probed using their effective BaseURL, which is resolved from the
// stored agent value → OLLAMA_HOST env → provider default, so agents that omit BaseURL
// (relying on the env or localhost default) are still subject to circuit-breaker health checks.
// Updates the circuit breaker state: marks unavailable after balancerMaxFailures consecutive
// failures; allows re-check after balancerCooldown.
func (b *Balancer) CheckHealth(ctx context.Context, c Candidate) bool {
	// CLI-launched subprocess agents have no HTTP endpoint; always healthy.
	switch c.Target.Runner {
	case agent.RunnerCodexCLI, agent.RunnerClaudeCLI, agent.RunnerGeminiCLI, agent.RunnerBedrockAPI:
		return true
	}

	// Determine the effective endpoint to probe.
	// Use the stored Target.BaseURL when present; fall back to the resolved runtime
	// config URL (handles OLLAMA_HOST env / provider default) for HTTP agents that
	// omit BaseURL in the vault.
	baseURL := c.Target.BaseURL
	if baseURL == "" {
		baseURL = strings.TrimSpace(agent.ResolvePromptRuntimeConfig(c.AgentConfig()).BaseURL.Value)
	}
	if baseURL == "" {
		// No URL resolvable — no HTTP endpoint to probe; assume healthy.
		return true
	}

	b.mu.Lock()
	h := b.getOrCreate(c.Agent.Name)
	if !h.Available && time.Since(h.LastCheck) < balancerCooldown {
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()

	start := time.Now()
	hctx, cancel := context.WithTimeout(ctx, balancerHealthTimeout)
	defer cancel()

	pingURL := strings.TrimRight(baseURL, "/") + "/"
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, pingURL, nil)
	if err != nil {
		b.RecordFailure(c.Agent.Name)
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	latencyMs := float64(time.Since(start).Milliseconds())
	if err != nil {
		b.RecordFailure(c.Agent.Name)
		return false
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		b.RecordFailure(c.Agent.Name)
		return false
	}

	b.RecordSuccess(c.Agent.Name, latencyMs)
	return true
}

// RecordSuccess marks the agent healthy and updates the EWMA latency.
func (b *Balancer) RecordSuccess(name string, latencyMs float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.getOrCreate(name)
	h.Available = true
	h.ConsecFailures = 0
	h.LastCheck = time.Now()
	if h.AvgLatencyMs == 0 {
		h.AvgLatencyMs = latencyMs
	} else {
		h.AvgLatencyMs = h.AvgLatencyMs*balancerEWMADecay + latencyMs*(1-balancerEWMADecay)
	}
}

// RecordFailure increments the failure counter and trips the circuit breaker after balancerMaxFailures.
func (b *Balancer) RecordFailure(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.getOrCreate(name)
	h.ConsecFailures++
	h.LastCheck = time.Now()
	if h.ConsecFailures >= balancerMaxFailures {
		h.Available = false
	}
}

// PickBest selects the LLM-chosen agent from candidates if it is healthy,
// otherwise falls back to the highest-scored candidate.
// Returns an error only when candidates is empty.
func (b *Balancer) PickBest(ctx context.Context, decision LLMRouterDecision, candidates []Candidate) (Candidate, error) {
	if len(candidates) == 0 {
		return Candidate{}, fmt.Errorf("balancer: no candidates available")
	}

	// Try the LLM-selected agent first.
	if decision.SelectedAgent != "" {
		for _, c := range candidates {
			if c.Agent.Name == decision.SelectedAgent && b.CheckHealth(ctx, c) {
				return c, nil
			}
		}
	}

	// Try fallback agents in order.
	for _, name := range decision.FallbackAgents {
		for _, c := range candidates {
			if c.Agent.Name == name && b.CheckHealth(ctx, c) {
				return c, nil
			}
		}
	}

	// Fall through to the highest-scored candidate (candidates are pre-sorted by score descending).
	for _, c := range candidates {
		if b.CheckHealth(ctx, c) {
			return c, nil
		}
	}

	// All candidates unreachable — return the top-scored anyway (graceful degradation).
	return candidates[0], nil
}
