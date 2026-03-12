package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

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

func TestTruncateForHistory_TruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("界", 700)
	got := truncateForHistory(long)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateForHistory returned invalid UTF-8")
	}
	if len([]rune(got)) != 500 {
		t.Fatalf("rune length = %d, want 500", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix")
	}
}

func TestShouldSkipOptimizationForWorkflow(t *testing.T) {
	tests := []struct {
		name        string
		workflowSet bool
		workflow    string
		optimizeSet bool
		optimize    bool
		profileSet  bool
		wantSkip    bool
	}{
		{name: "non workflow prompt", wantSkip: false},
		{name: "workflow disables default optimization", workflowSet: true, workflow: "implement_issue", wantSkip: true},
		{name: "workflow keeps explicit optimize true", workflowSet: true, workflow: "implement_issue", optimizeSet: true, optimize: true, wantSkip: false},
		{name: "workflow keeps explicit optimize false", workflowSet: true, workflow: "implement_issue", optimizeSet: true, optimize: false, wantSkip: false},
		{name: "workflow keeps explicit optimize profile", workflowSet: true, workflow: "implement_issue", profileSet: true, wantSkip: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newPromptOptimizationTestCommand()
			if tt.workflowSet {
				if err := cmd.Flags().Set("workflow", tt.workflow); err != nil {
					t.Fatalf("setting workflow flag: %v", err)
				}
			}
			if tt.optimizeSet {
				value := "false"
				if tt.optimize {
					value = "true"
				}
				if err := cmd.Flags().Set("optimize", value); err != nil {
					t.Fatalf("setting optimize flag: %v", err)
				}
			}
			if tt.profileSet {
				if err := cmd.Flags().Set("optimize-profile", "codex"); err != nil {
					t.Fatalf("setting optimize-profile flag: %v", err)
				}
			}
			if got := shouldSkipOptimizationForWorkflow(cmd); got != tt.wantSkip {
				t.Fatalf("shouldSkipOptimizationForWorkflow() = %v, want %v", got, tt.wantSkip)
			}
		})
	}
}

func newPromptOptimizationTestCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("workflow", "", "")
	cmd.Flags().Bool("optimize", true, "")
	cmd.Flags().String("optimize-profile", "auto", "")
	cmd.Flags().Duration("timeout", 5*time.Minute, "")
	return cmd
}
