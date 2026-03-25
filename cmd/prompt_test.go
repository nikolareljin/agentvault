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

type stubPromptVault struct {
	agents []agent.Agent
	shared agent.SharedConfig
}

func (s stubPromptVault) Get(name string) (agent.Agent, bool) {
	for _, a := range s.agents {
		if a.Name == name {
			return a, true
		}
	}
	return agent.Agent{}, false
}

func (s stubPromptVault) List() []agent.Agent { return append([]agent.Agent(nil), s.agents...) }

func (s stubPromptVault) SharedConfig() agent.SharedConfig { return s.shared }

func TestResolvePromptAgentAutoUsesProvidedPromptText(t *testing.T) {
	cmd := newPromptOptimizationTestCommand()
	cmd.Flags().Bool("auto", false, "")
	if err := cmd.Flags().Set("auto", "true"); err != nil {
		t.Fatalf("setting auto flag: %v", err)
	}

	vault := stubPromptVault{agents: []agent.Agent{
		{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
		{Name: "codex", Provider: agent.ProviderCodex, Model: "gpt-5-codex"},
	}}

	got, decision, _, err := resolvePromptAgent(cmd, vault, nil, "Implement and test this Go refactor.")
	if err != nil {
		t.Fatalf("resolvePromptAgent() error = %v", err)
	}
	if got.Name != "codex" {
		t.Fatalf("selected agent = %q, want codex", got.Name)
	}
	if decision == nil || decision.Selected.Agent.Name != "codex" {
		t.Fatalf("routing decision = %#v, want codex selection", decision)
	}
}

func TestResolvePromptAgentAutoKeepsRuntimeValueSourcesFromOriginalAgent(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "https://remote.example")

	cmd := newPromptOptimizationTestCommand()
	cmd.Flags().Bool("auto", false, "")
	if err := cmd.Flags().Set("auto", "true"); err != nil {
		t.Fatalf("setting auto flag: %v", err)
	}

	vault := stubPromptVault{agents: []agent.Agent{{
		Name:     "local",
		Provider: agent.ProviderOllama,
		Model:    "llama3.2",
	}}}

	got, decision, runtimeCfg, err := resolvePromptAgent(cmd, vault, nil, "Summarize this issue.")
	if err != nil {
		t.Fatalf("resolvePromptAgent() error = %v", err)
	}
	if got.BaseURL != "" {
		t.Fatalf("returned agent base URL = %q, want original local config", got.BaseURL)
	}
	if runtimeCfg.BaseURL.Value != "https://remote.example" {
		t.Fatalf("runtime base URL = %q, want env override", runtimeCfg.BaseURL.Value)
	}
	if runtimeCfg.BaseURL.Source != agent.ValueSourceEnv {
		t.Fatalf("runtime base URL source = %q, want %q", runtimeCfg.BaseURL.Source, agent.ValueSourceEnv)
	}
	if decision == nil || decision.Selected.Target.AgentName != "local" {
		t.Fatalf("routing decision = %#v, want local selection", decision)
	}
}

func TestResolvePromptAgentAutoRejectsRemoteResolvedTargetForLocalOnly(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "https://remote.example")

	cmd := newPromptOptimizationTestCommand()
	cmd.Flags().Bool("auto", false, "")
	cmd.Flags().String("router", "", "")
	cmd.Flags().String("langgraph-cmd", "", "")
	cmd.Flags().Bool("prefer-local", false, "")
	cmd.Flags().Bool("prefer-fast", false, "")
	cmd.Flags().Bool("prefer-low-cost", false, "")
	cmd.Flags().Bool("local-only", false, "")
	if err := cmd.Flags().Set("auto", "true"); err != nil {
		t.Fatalf("setting auto flag: %v", err)
	}
	if err := cmd.Flags().Set("local-only", "true"); err != nil {
		t.Fatalf("setting local-only flag: %v", err)
	}

	vault := stubPromptVault{agents: []agent.Agent{{
		Name:     "local",
		Provider: agent.ProviderOllama,
		Model:    "llama3.2",
	}}}

	_, _, _, err := resolvePromptAgent(cmd, vault, nil, "Private local only code review.")
	if err == nil {
		t.Fatalf("expected local-only routing error for remotely resolved target")
	}
	if !strings.Contains(err.Error(), "current policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutePromptTargetUnsupportedRunnerMentionsRunner(t *testing.T) {
	_, err := executePromptTarget(agent.ExecutionTarget{Runner: agent.RunnerUnknown}, agent.Agent{Provider: agent.ProviderCustom}, "hello", time.Second)
	if err == nil {
		t.Fatalf("expected unsupported runner error")
	}
	if !strings.Contains(err.Error(), string(agent.RunnerUnknown)) {
		t.Fatalf("error = %q, want runner name", err.Error())
	}
}

func TestValidatePromptTargetUnsupportedRunnerMentionsRunner(t *testing.T) {
	err := validatePromptTarget(agent.ExecutionTarget{Runner: agent.RunnerUnknown}, agent.Agent{Provider: agent.ProviderCustom}, time.Second)
	if err == nil {
		t.Fatalf("expected unsupported runner validation error")
	}
	if !strings.Contains(err.Error(), string(agent.RunnerUnknown)) {
		t.Fatalf("error = %q, want runner name", err.Error())
	}
}
