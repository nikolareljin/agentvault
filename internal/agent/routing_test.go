package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteConfigValidateRejectsUnknownCapability(t *testing.T) {
	cfg := RouteConfig{Capabilities: []string{"coding", "unknown"}}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for unknown capability")
	}
}

func TestRouterConfigWithDefaultsDoesNotForceAllowFallbacks(t *testing.T) {
	cfg := (RouterConfig{}).WithDefaults()
	if !cfg.PreferLocal {
		t.Fatalf("PreferLocal = false, want true default")
	}
	if cfg.AllowFallbacks {
		t.Fatalf("AllowFallbacks = true, want false when unset")
	}
}

func TestRouterConfigValidateRejectsUnknownMode(t *testing.T) {
	cfg := RouterConfig{Mode: "langgrpah"}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected validation error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown router mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouterConfigValidateAcceptsLocalAIMode(t *testing.T) {
	cfg := RouterConfig{Mode: "local-ai"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for local-ai mode", err)
	}
}

func TestRouterConfigValidateRejectsUnknownImportance(t *testing.T) {
	cfg := RouterConfig{Importance: "extreme"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unknown importance level")
	}
}

func TestRouterConfigValidateAcceptsValidImportance(t *testing.T) {
	for _, imp := range []string{"low", "medium", "high", "critical"} {
		cfg := RouterConfig{Importance: imp}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for importance %q", err, imp)
		}
	}
}

func TestRouterConfigValidateRejectsUnknownDeadline(t *testing.T) {
	cfg := RouterConfig{Deadline: "asap"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unknown deadline")
	}
}

func TestRouterConfigValidateAcceptsValidDeadlines(t *testing.T) {
	for _, dl := range []string{"immediate", "normal", "background"} {
		cfg := RouterConfig{Deadline: dl}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for deadline %q", err, dl)
		}
	}
}

func TestBuildClaudeStreamArgsOmitsOutputFormat(t *testing.T) {
	args := BuildClaudeStreamArgs("claude-opus-4-7", "hello")
	for _, arg := range args {
		if arg == "--output-format" {
			t.Fatal("BuildClaudeStreamArgs should not include --output-format flag")
		}
	}
	found := false
	for _, arg := range args {
		if arg == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatal("BuildClaudeStreamArgs missing prompt in args")
	}
}

func TestBuildGeminiStreamArgsOmitsOutputFormat(t *testing.T) {
	args := BuildGeminiStreamArgs("gemini-2.5-flash", "hello")
	for _, arg := range args {
		if arg == "--output-format" {
			t.Fatal("BuildGeminiStreamArgs should not include --output-format flag")
		}
	}
}

func TestResolveExecutionTarget(t *testing.T) {
	tests := []struct {
		name      string
		agent     Agent
		runner    RunnerKind
		local     bool
		supported bool
	}{
		{name: "ollama implicit local default", agent: Agent{Name: "ollama", Provider: ProviderOllama}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama explicit localhost", agent: Agent{Name: "ollama-local", Provider: ProviderOllama, BaseURL: "http://localhost:11434"}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama explicit loopback", agent: Agent{Name: "ollama-loopback", Provider: ProviderOllama, BaseURL: "http://127.0.0.1:11434"}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "ollama remote https", agent: Agent{Name: "ollama-remote", Provider: ProviderOllama, BaseURL: "https://remote.example"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "ollama hostname without scheme is not local", agent: Agent{Name: "ollama-noscheme-remote", Provider: ProviderOllama, BaseURL: "remote.example:443"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "ollama localhost without scheme is not local", agent: Agent{Name: "ollama-noscheme-localhost", Provider: ProviderOllama, BaseURL: "localhost:11434"}, runner: RunnerOllamaHTTP, local: false, supported: true},
		{name: "codex cli", agent: Agent{Name: "codex", Provider: ProviderCodex}, runner: RunnerCodexCLI, local: false, supported: true},
		{name: "gemini cli", agent: Agent{Name: "gemini", Provider: ProviderGemini}, runner: RunnerGeminiCLI, local: false, supported: true},
		{name: "openai http", agent: Agent{Name: "openai", Provider: ProviderOpenAI}, runner: RunnerOpenAIHTTP, local: false, supported: true},
		{name: "claude cli", agent: Agent{Name: "claude", Provider: ProviderClaude}, runner: RunnerClaudeCLI, local: false, supported: true},
		{name: "claude ollama", agent: Agent{Name: "claude-local", Provider: ProviderClaude, Backend: ClaudeBackendOllama}, runner: RunnerOllamaHTTP, local: true, supported: true},
		{name: "claude bedrock unsupported", agent: Agent{Name: "claude-bedrock", Provider: ProviderClaude, Backend: ClaudeBackendBedrock}, runner: RunnerBedrockAPI, local: false, supported: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveExecutionTarget(tt.agent)
			if got.Runner != tt.runner || got.Local != tt.local || got.Supported != tt.supported {
				t.Fatalf("ResolveExecutionTarget() = %#v", got)
			}
		})
	}
}

func TestExecutionTargetJSONOmitsBaseURL(t *testing.T) {
	target := ExecutionTarget{
		AgentName: "openai",
		Provider:  ProviderOpenAI,
		Runner:    RunnerOpenAIHTTP,
		BaseURL:   "https://user:secret@example.com/v1?token=secret",
		Local:     false,
		Supported: true,
	}
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("json.Marshal(target) error = %v", err)
	}
	if strings.Contains(string(raw), "base_url") || strings.Contains(string(raw), "secret") {
		t.Fatalf("expected marshaled target to omit base_url secrets, got: %s", string(raw))
	}
}

func TestIsGitWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	nested := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git dir: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested repo dir: %v", err)
	}

	if !IsGitWorktree(repo) {
		t.Fatalf("IsGitWorktree(%q) = false, want true", repo)
	}
	if !IsGitWorktree(nested) {
		t.Fatalf("IsGitWorktree(%q) = false, want true", nested)
	}

	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("creating plain dir: %v", err)
	}
	if IsGitWorktree(plain) {
		t.Fatalf("IsGitWorktree(%q) = true, want false", plain)
	}

	worktreeRoot := filepath.Join(root, "worktree")
	worktreeNested := filepath.Join(worktreeRoot, "child")
	if err := os.MkdirAll(worktreeNested, 0o755); err != nil {
		t.Fatalf("creating worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, ".git"), []byte("gitdir: /tmp/example"), 0o644); err != nil {
		t.Fatalf("creating .git file: %v", err)
	}
	if !IsGitWorktree(worktreeNested) {
		t.Fatalf("IsGitWorktree(%q) = false, want true for .git file", worktreeNested)
	}
}

func TestBuildCodexExecArgs(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git dir: %v", err)
	}

	args := BuildCodexExecArgs("gpt-5", "/tmp/out.txt", repo, "fix the bug")
	want := []string{"exec", "--json", "--output-last-message", "/tmp/out.txt", "--full-auto", "--model", "gpt-5", "fix the bug"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("BuildCodexExecArgs() = %#v, want %#v", args, want)
	}
}

func TestBuildCodexExecArgsAddsSkipGitCheckOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	args := BuildCodexExecArgs("", "/tmp/out.txt", dir, "fix the bug")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--full-auto") {
		t.Fatalf("expected --full-auto in args: %#v", args)
	}
	if !strings.Contains(joined, "--skip-git-repo-check") {
		t.Fatalf("expected --skip-git-repo-check in args: %#v", args)
	}
}

func TestBuildCodexStreamArgsOmitsJSON(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git dir: %v", err)
	}
	args := BuildCodexStreamArgs("gpt-5", repo, "fix the bug")
	joined := strings.Join(args, " ")
	for _, banned := range []string{"--json", "--output-last-message"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("BuildCodexStreamArgs should not include %q, got: %#v", banned, args)
		}
	}
	if !strings.Contains(joined, "--full-auto") {
		t.Fatalf("expected --full-auto in args: %#v", args)
	}
	if !strings.Contains(joined, "fix the bug") {
		t.Fatalf("expected prompt in args: %#v", args)
	}
}

func TestBuildClaudeExecArgs(t *testing.T) {
	args := BuildClaudeExecArgs("sonnet", "fix the bug")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--output-format json", "--permission-mode auto", "--model sonnet", "fix the bug"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in args: %#v", want, args)
		}
	}
}

func TestBuildGeminiExecArgs(t *testing.T) {
	args := BuildGeminiExecArgs("gemini-2.5-pro", "fix the bug")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--prompt fix the bug", "--output-format json", "--approval-mode auto_edit", "--model gemini-2.5-pro"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in args: %#v", want, args)
		}
	}
}

func TestInferredRouteCapabilitiesIncludesGeminiCodingReviewAndAnalysis(t *testing.T) {
	got := inferredRouteCapabilities(Agent{Name: "gemini", Provider: ProviderGemini})
	for _, want := range []string{
		RouteCapabilityGeneral,
		RouteCapabilityCoding,
		RouteCapabilityReview,
		RouteCapabilityAnalysis,
	} {
		if !containsString(got, want) {
			t.Fatalf("inferredRouteCapabilities() missing %q in %#v", want, got)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
