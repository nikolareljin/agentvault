package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nikolareljin/agentvault/internal/localllm"
)

// localEngMu serialises engine access: llama.cpp contexts are not thread-safe,
// and the mutex doubles as a cache guard for the long-lived engine instance.
var (
	localEngMu     sync.Mutex
	localEng       localllm.Engine
	localEngCfgKey string
)

// LLMRouterConfig holds settings for the llm-router routing mode.
// Either URL (HTTP server) or ModelPath (embedded inference) must be set.
type LLMRouterConfig struct {
	URL           string
	Model         string
	TimeoutSecs   int
	EnableCostEst bool

	// Embedded inference fields (requires -tags localllm build).
	ModelPath   string
	ContextSize int
	Threads     int
	GPULayers   int
}

// RoutingFactors captures the analysis dimensions returned by the routing model.
type RoutingFactors struct {
	Complexity       int    `json:"complexity"` // 1–10
	TaskType         string `json:"task_type"`  // one of: coding, documentation, review, analysis, general
	RequiresTools    bool   `json:"requires_tools"`
	PrivacySensitive bool   `json:"privacy_sensitive"`
	TimeSensitive    bool   `json:"time_sensitive"`
}

// LLMRouterDecision is the structured routing decision from the local inference server.
type LLMRouterDecision struct {
	SelectedAgent   string         `json:"selected_agent"`
	FallbackAgents  []string       `json:"fallback_agents"`
	Reasoning       string         `json:"reasoning"`
	Confidence      float64        `json:"confidence"`
	RoutingFactors  RoutingFactors `json:"routing_factors"`
	EstInputTokens  int            `json:"estimated_input_tokens"`
	EstOutputTokens int            `json:"estimated_output_tokens"`
}

// estimateTokens returns a rough token count using the 4-chars-per-token heuristic.
// Counts runes via range to avoid allocating a []rune.
func estimateTokens(text string) int {
	var n int
	for range text {
		n++
	}
	if n == 0 {
		return 0
	}
	est := n / 4
	if est == 0 {
		return 1
	}
	return est
}

// buildRoutingSystemPrompt builds the system prompt listing all candidate agents with their tiers.
func buildRoutingSystemPrompt(candidates []Candidate) string {
	var sb strings.Builder
	sb.WriteString("You are an intelligent AI routing agent. Select the best agent for the task.\n\nAvailable agents:\n")
	for _, c := range candidates {
		caps := strings.Join(c.Route.Capabilities, ",")
		if caps == "" {
			caps = "general"
		}
		sb.WriteString(fmt.Sprintf("- name=%q capabilities=[%s] latency=%s cost=%s privacy=%s priority=%d\n",
			c.Agent.Name, caps,
			defaultTier(c.Route.LatencyTier, "medium"),
			defaultTier(c.Route.CostTier, "medium"),
			defaultTier(c.Route.PrivacyTier, "remote"),
			c.Route.Priority,
		))
	}
	sb.WriteString(`
Consider: task complexity, privacy requirements, cost (prefer local for simple tasks), tool availability, latency sensitivity.

task_type must be exactly one of: coding, documentation, review, analysis, general
  coding       - write or modify source code
  documentation - write or update docs, README, docstrings, specs, changelogs
  review       - review code, diffs, or pull requests
  analysis     - investigate, compare, or design architecture/strategy
  general      - anything that does not fit the above

Respond ONLY with valid JSON (no markdown, no extra text):
{
  "selected_agent": "<name from list above>",
  "fallback_agents": ["<name>"],
  "reasoning": "<one sentence>",
  "confidence": 0.85,
  "routing_factors": {
    "complexity": 5,
    "task_type": "coding",
    "requires_tools": false,
    "privacy_sensitive": false,
    "time_sensitive": false
  },
  "estimated_input_tokens": 0,
  "estimated_output_tokens": 0
}`)
	return sb.String()
}

func buildRoutingUserMessage(prompt string, inputEst int) string {
	return fmt.Sprintf("Task (estimated %d input tokens):\n%s", inputEst, strings.TrimSpace(prompt))
}

func defaultTier(val, fallback string) string {
	v := strings.TrimSpace(val)
	if v == "" {
		return fallback
	}
	return v
}

// callLLMServer POSTs to an OpenAI-compatible /v1/chat/completions endpoint and returns the parsed decision.
func callLLMServer(ctx context.Context, baseURL, systemPrompt, userMsg, model string) (LLMRouterDecision, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := map[string]any{
		"messages": []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		"temperature": 0.1,
		"stream":      false,
		"max_tokens":  128,
	}
	if strings.TrimSpace(model) != "" {
		payload["model"] = model
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: marshal request: %w", err)
	}

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: call server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		const maxErrBody = 4096
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		return LLMRouterDecision{}, fmt.Errorf("llm-router: server error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	const maxRespBody = 1 << 20 // 1 MiB — routing decisions are small JSON
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBody)).Decode(&out); err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: empty choices in response")
	}

	rawContent := strings.TrimSpace(out.Choices[0].Message.Content)
	return parseLLMDecision(rawContent)
}

// parseLLMDecision parses a raw model output string into a LLMRouterDecision.
// It strips markdown code fences and normalises the decision before returning.
func parseLLMDecision(raw string) (LLMRouterDecision, error) {
	cleaned := stripJSONFences(strings.TrimSpace(raw))
	var decision LLMRouterDecision
	if err := json.Unmarshal([]byte(cleaned), &decision); err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: parse decision JSON: %w", err)
	}
	return normalizeLLMRouterDecision(decision), nil
}

// analyzeLocal runs inference using the embedded llama.cpp engine (requires -tags localllm).
// The engine is loaded once per unique model path and reused across routing calls; the mutex
// serialises access because llama.cpp contexts are not thread-safe.
func analyzeLocal(ctx context.Context, sysPrompt, usrMsg string, cfg LLMRouterConfig) (LLMRouterDecision, error) {
	localEngMu.Lock()
	defer localEngMu.Unlock()

	cfgKey := fmt.Sprintf("%s:%d:%d:%d", cfg.ModelPath, cfg.ContextSize, cfg.Threads, cfg.GPULayers)
	if localEng == nil || localEngCfgKey != cfgKey {
		if localEng != nil {
			localEng.Close()
			localEng = nil
		}
		eng, err := localllm.New(cfg.ModelPath, cfg.ContextSize, cfg.Threads, cfg.GPULayers)
		if err != nil {
			return LLMRouterDecision{}, fmt.Errorf("llm-router local: %w", err)
		}
		localEng = eng
		localEngCfgKey = cfgKey
	}

	raw, err := localEng.Route(ctx, sysPrompt, usrMsg)
	if err != nil {
		return LLMRouterDecision{}, fmt.Errorf("llm-router local: %w", err)
	}
	return parseLLMDecision(raw)
}

// AnalyzeWithLLMRouter returns a structured routing decision for the given prompt.
// When cfg.ModelPath is non-empty, inference runs in-process via the embedded llama.cpp
// engine (requires -tags localllm build). Otherwise, cfg.URL must point to an
// OpenAI-compatible HTTP server. Returns an error on failure; the caller falls back
// to heuristic routing.
func AnalyzeWithLLMRouter(ctx context.Context, prompt string, candidates []Candidate, cfg LLMRouterConfig) (LLMRouterDecision, error) {
	inputEst := estimateTokens(prompt)
	sysPrompt := buildRoutingSystemPrompt(candidates)
	usrMsg := buildRoutingUserMessage(prompt, inputEst)

	// Apply timeout to both embedded and HTTP paths so --llm-router-timeout is honoured uniformly.
	timeout := time.Duration(cfg.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if strings.TrimSpace(cfg.ModelPath) != "" {
		return analyzeLocal(ctx, sysPrompt, usrMsg, cfg)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if baseURL == "" {
		return LLMRouterDecision{}, fmt.Errorf("llm-router: set --llm-router-url or --llm-router-model-path")
	}
	return callLLMServer(ctx, baseURL, sysPrompt, usrMsg, cfg.Model)
}

// enrichIntentFromLLMDecision updates a heuristic Intent with analysis from the LLM router.
func enrichIntentFromLLMDecision(intent *Intent, d LLMRouterDecision) {
	f := d.RoutingFactors
	if f.PrivacySensitive {
		intent.PrivacySensitive = true
	}
	if f.TimeSensitive {
		intent.LatencySensitive = true
	}
	switch strings.ToLower(strings.TrimSpace(f.TaskType)) {
	case "coding":
		intent.Coding = true
		intent.Review = false
		intent.Analysis = false
		intent.Documentation = false
		intent.TaskClass = "coding"
	case "documentation":
		intent.Coding = false
		intent.Review = false
		intent.Analysis = false
		intent.Documentation = true
		intent.TaskClass = "documentation"
	case "review":
		intent.Coding = false
		intent.Review = true
		intent.Analysis = false
		intent.Documentation = false
		intent.TaskClass = "review"
	case "analysis":
		intent.Coding = false
		intent.Review = false
		intent.Analysis = true
		intent.Documentation = false
		intent.TaskClass = "analysis"
	default:
		intent.Coding = false
		intent.Review = false
		intent.Analysis = false
		intent.Documentation = false
		intent.TaskClass = "general"
	}
	// High complexity tasks benefit from analysis capability even within coding tasks.
	if f.Complexity >= 8 && !intent.Analysis {
		intent.Analysis = true
	}
}

func normalizeLLMRouterDecision(d LLMRouterDecision) LLMRouterDecision {
	if d.Confidence < 0 {
		d.Confidence = 0
	}
	if d.Confidence > 1 {
		d.Confidence = 1
	}
	f := &d.RoutingFactors
	if f.Complexity < 1 {
		f.Complexity = 1
	}
	if f.Complexity > 10 {
		f.Complexity = 10
	}
	switch strings.ToLower(strings.TrimSpace(f.TaskType)) {
	case "coding", "documentation", "review", "analysis", "general":
		f.TaskType = strings.ToLower(strings.TrimSpace(f.TaskType))
	default:
		f.TaskType = "general"
	}
	return d
}
