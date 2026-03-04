package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestOptimizePromptForOllama(t *testing.T) {
	a := agent.Agent{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.1", Role: "lead-engineer"}
	shared := agent.SharedConfig{
		Roles: []agent.Role{{Name: "lead-engineer", Title: "Lead Engineer"}},
		Rules: []agent.UnifiedRule{
			{Name: "keep-minimal", Content: "Keep changes minimal", Priority: 50, Enabled: true},
			{Name: "do-tests", Content: "Add tests", Priority: 10, Enabled: true},
		},
	}

	out, profile := optimizePromptForAgent("Build a parser for CSV", a, shared, "auto")
	if profile != "ollama" {
		t.Fatalf("profile = %q, want ollama", profile)
	}
	if !strings.Contains(out, "## Task") {
		t.Fatalf("optimized prompt missing task section")
	}
	if !strings.Contains(out, "Build a parser for CSV") {
		t.Fatalf("optimized prompt missing original content")
	}
	if !strings.Contains(out, "Keep changes minimal") {
		t.Fatalf("optimized prompt missing enabled rule")
	}
	if !strings.Contains(out, "Lead Engineer") {
		t.Fatalf("optimized prompt missing role title")
	}
	if strings.Index(out, "Add tests") > strings.Index(out, "Keep changes minimal") {
		t.Fatalf("rules are not sorted by priority in optimized prompt")
	}
}

func TestOptimizePromptSkipsAgentDisabledRules(t *testing.T) {
	a := agent.Agent{
		Name:          "local",
		Provider:      agent.ProviderOllama,
		DisabledRules: []string{"skip-this"},
	}
	shared := agent.SharedConfig{
		Rules: []agent.UnifiedRule{
			{Name: "skip-this", Content: "Should not appear", Priority: 1, Enabled: true},
			{Name: "keep-this", Content: "Keep this rule", Priority: 2, Enabled: true},
		},
	}

	out, _ := optimizePromptForAgent("Build a parser for CSV", a, shared, "auto")
	if strings.Contains(out, "Should not appear") {
		t.Fatalf("optimized prompt contains disabled rule: %q", out)
	}
	if !strings.Contains(out, "Keep this rule") {
		t.Fatalf("optimized prompt missing enabled rule")
	}
}

func TestChooseOptimizationProfileCopilotHeuristic(t *testing.T) {
	a := agent.Agent{Name: "my-copilot", Provider: agent.ProviderCustom, Model: "copilot-chat"}
	profile := chooseOptimizationProfile(a, "auto")
	if profile != "copilot" {
		t.Fatalf("profile = %q, want copilot", profile)
	}
}

func TestParseCodexUsage(t *testing.T) {
	raw := "{\"payload\":{\"type\":\"token_count\",\"info\":{\"total_token_usage\":{\"input_tokens\":10,\"cached_input_tokens\":2,\"output_tokens\":3,\"reasoning_output_tokens\":1,\"total_tokens\":13}}}}\n"
	u := parseCodexUsage(raw)
	if u.InputTokens != 10 || u.OutputTokens != 3 || u.TotalTokens != 13 || u.CachedInputTokens != 2 {
		t.Fatalf("unexpected usage parsed: %#v", u)
	}
}

func TestPromptRecordJSON_OmitsEmptyTokenUsage(t *testing.T) {
	record := PromptRecord{
		ID:              "prompt-1",
		AgentName:       "codex",
		Provider:        "codex",
		OriginalPrompt:  "hello",
		EffectivePrompt: "hello",
		Success:         true,
	}

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(record) error = %v", err)
	}
	if strings.Contains(string(raw), "token_usage") {
		t.Fatalf("expected token_usage to be omitted, got: %s", string(raw))
	}
}

func TestPromptRecordJSON_IncludesNonEmptyTokenUsage(t *testing.T) {
	record := PromptRecord{
		ID:              "prompt-2",
		AgentName:       "codex",
		Provider:        "codex",
		OriginalPrompt:  "hello",
		EffectivePrompt: "hello",
		Success:         true,
		TokenUsage: &agent.PromptTokenUsage{
			InputTokens: 1,
		},
	}

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal(record) error = %v", err)
	}
	if !strings.Contains(string(raw), "token_usage") {
		t.Fatalf("expected token_usage to be present, got: %s", string(raw))
	}
}
