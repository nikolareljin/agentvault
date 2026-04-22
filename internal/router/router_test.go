package router

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func requirePythonInterpreter(t *testing.T) {
	t.Helper()
	if _, err := resolvePythonInterpreter(); err != nil {
		t.Skipf("skipping LangGraph test: %v", err)
	}
}

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

func TestClassifyPromptTreatsRepoAsWholeWord(t *testing.T) {
	intent := classifyPrompt("Please send a report tomorrow.")
	if intent.Coding {
		t.Fatalf("intent.Coding = true, want false when repo appears only inside another word")
	}
}

func TestClassifyPromptPrefersCodingForBugFixRequest(t *testing.T) {
	intent := classifyPrompt("Please fix this bug in the router.")
	if !intent.Coding {
		t.Fatalf("intent.Coding = false, want true for bug-fix prompt")
	}
	if intent.TaskClass != "coding" {
		t.Fatalf("intent.TaskClass = %q, want coding", intent.TaskClass)
	}
}

func TestRouteWithLangGraphKeepsSelectedAndCandidatesReasonsInSync(t *testing.T) {
	requirePythonInterpreter(t)
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
	requirePythonInterpreter(t)
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
	requirePythonInterpreter(t)
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
	requirePythonInterpreter(t)
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
	requirePythonInterpreter(t)
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
	requirePythonInterpreter(t)
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

func TestResolvePythonInterpreterReturnsSupportedExecutable(t *testing.T) {
	tempDir := t.TempDir()
	python3Path := filepath.Join(tempDir, "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(python3Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(filepath.Dir(python3Path)) error = %v", err)
	}
	if err := os.WriteFile(python3Path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(python3Path) error = %v", err)
	}

	original := execLookPath
	execLookPath = func(file string) (string, error) {
		switch file {
		case "python3":
			return filepath.Join("bin", "python3"), nil
		default:
			return "", errors.New("missing")
		}
	}
	t.Cleanup(func() {
		execLookPath = original
	})

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("os.Chdir(tempDir) error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	path, err := resolvePythonInterpreter()
	if err != nil {
		t.Fatalf("resolvePythonInterpreter() error = %v", err)
	}
	if path == "" {
		t.Fatal("resolvePythonInterpreter() returned empty path")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("resolvePythonInterpreter() = %q, want absolute path", path)
	}
	if path != python3Path {
		t.Fatalf("resolvePythonInterpreter() = %q, want %q", path, python3Path)
	}
}

func TestResolveLangGraphScriptPathReturnsCanonicalAbsolutePath(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "router.py")
	if err := os.WriteFile(targetPath, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(targetPath) error = %v", err)
	}
	linkPath := filepath.Join(tempDir, "link.py")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("Symlink(linkPath) error = %v", err)
	}

	got, err := resolveLangGraphScriptPath(linkPath)
	if err != nil {
		t.Fatalf("resolveLangGraphScriptPath() error = %v", err)
	}
	if got != targetPath {
		t.Fatalf("resolveLangGraphScriptPath() = %q, want %q", got, targetPath)
	}
}

func TestResolvePythonInterpreterErrorsWithoutSupportedExecutable(t *testing.T) {
	original := execLookPath
	execLookPath = func(file string) (string, error) {
		return "", errors.New("missing")
	}
	t.Cleanup(func() {
		execLookPath = original
	})

	_, err := resolvePythonInterpreter()
	if err == nil {
		t.Fatal("expected missing interpreter error")
	}
	if !strings.Contains(err.Error(), "Python 3 interpreter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePythonInterpreterSkipsPython2Fallback(t *testing.T) {
	tempDir := t.TempDir()
	python2Path := filepath.Join(tempDir, "python")
	python3Path := filepath.Join(tempDir, "python3")

	if err := os.WriteFile(python2Path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(python2Path) error = %v", err)
	}
	if err := os.WriteFile(python3Path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(python3Path) error = %v", err)
	}

	original := execLookPath
	execLookPath = func(file string) (string, error) {
		switch file {
		case "python3":
			return python3Path, nil
		case "python":
			return python2Path, nil
		default:
			return "", errors.New("missing")
		}
	}
	t.Cleanup(func() {
		execLookPath = original
	})

	got, err := resolvePythonInterpreter()
	if err != nil {
		t.Fatalf("resolvePythonInterpreter() error = %v", err)
	}
	if got != python3Path {
		t.Fatalf("resolvePythonInterpreter() = %q, want %q", got, python3Path)
	}
}

func TestImportanceDeadlineScoreCriticalPenalizesLocal(t *testing.T) {
	localTarget := agent.ExecutionTarget{Local: true}
	score, reasons := importanceDeadlineScore("critical", "", localTarget, agent.RouteConfig{})
	if score >= 0 {
		t.Fatalf("importanceDeadlineScore(critical, local) = %d, want negative penalty", score)
	}
	if len(reasons) == 0 {
		t.Fatal("expected reasons for critical importance")
	}
}

func TestImportanceDeadlineScoreCriticalBonusCloud(t *testing.T) {
	cloudTarget := agent.ExecutionTarget{Local: false}
	score, _ := importanceDeadlineScore("critical", "", cloudTarget, agent.RouteConfig{})
	if score <= 0 {
		t.Fatalf("importanceDeadlineScore(critical, cloud) = %d, want positive bonus", score)
	}
}

func TestImportanceDeadlineScoreImmediateDeadlineBoostsLowLatency(t *testing.T) {
	localTarget := agent.ExecutionTarget{Local: true}
	profile := agent.RouteConfig{LatencyTier: "low"}
	score, reasons := importanceDeadlineScore("", "immediate", localTarget, profile)
	if score <= 0 {
		t.Fatalf("importanceDeadlineScore(immediate, low-latency-local) = %d, want positive bonus", score)
	}
	found := false
	for _, r := range reasons {
		if strings.Contains(r, "immediate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons missing 'immediate' mention: %v", reasons)
	}
}

func TestImportanceDeadlineScoreBackgroundFavorsLowCost(t *testing.T) {
	target := agent.ExecutionTarget{Local: false}
	profileLow := agent.RouteConfig{CostTier: "low"}
	profileHigh := agent.RouteConfig{CostTier: "high"}
	scoreLow, _ := importanceDeadlineScore("", "background", target, profileLow)
	scoreHigh, _ := importanceDeadlineScore("", "background", target, profileHigh)
	if scoreLow <= scoreHigh {
		t.Fatalf("background: low-cost score %d should exceed high-cost score %d", scoreLow, scoreHigh)
	}
}

func TestRouteCriticalImportancePrefersCloud(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
			{Name: "cloud", Provider: agent.ProviderClaude, Model: "claude-opus-4-7"},
		},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Importance: "critical"},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "cloud" {
		t.Fatalf("selected agent = %q, want cloud for critical importance", decision.Selected.Agent.Name)
	}
}

func TestRouteLowImportancePrefersLocal(t *testing.T) {
	decision, err := Route(Request{
		Prompt: "Summarize this design document.",
		Agents: []agent.Agent{
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
			{Name: "cloud", Provider: agent.ProviderClaude, Model: "claude-opus-4-7"},
		},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{Importance: "low"},
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Selected.Agent.Name != "local" {
		t.Fatalf("selected agent = %q, want local for low importance", decision.Selected.Agent.Name)
	}
}

func TestNormalizeLocalAIAnalysisClampsBounds(t *testing.T) {
	a := normalizeLocalAIAnalysis(LocalAIAnalysis{Complexity: 0, TaskType: "unknown", Urgency: "extreme"})
	if a.Complexity != 1 {
		t.Fatalf("Complexity = %d, want 1 (clamped from 0)", a.Complexity)
	}
	if a.TaskType != "general" {
		t.Fatalf("TaskType = %q, want general for unknown type", a.TaskType)
	}
	if a.Urgency != "medium" {
		t.Fatalf("Urgency = %q, want medium for unknown urgency", a.Urgency)
	}

	b := normalizeLocalAIAnalysis(LocalAIAnalysis{Complexity: 99})
	if b.Complexity != 10 {
		t.Fatalf("Complexity = %d, want 10 (clamped from 99)", b.Complexity)
	}
}

func TestEnrichIntentFromLocalAISetsPrivacy(t *testing.T) {
	intent := Intent{}
	enrichIntentFromLocalAI(&intent, LocalAIAnalysis{PrivacySensitive: true, Urgency: "high", TaskType: "coding"})
	if !intent.PrivacySensitive {
		t.Fatal("PrivacySensitive not set from local AI analysis")
	}
	if !intent.LatencySensitive {
		t.Fatal("LatencySensitive not set from high urgency")
	}
	if !intent.Coding {
		t.Fatal("Coding not set from coding task type")
	}
}

func TestStripJSONFencesRemovesMarkdown(t *testing.T) {
	input := "```json\n{\"complexity\": 5}\n```"
	got := stripJSONFences(input)
	if got != `{"complexity": 5}` {
		t.Fatalf("stripJSONFences() = %q, want bare JSON", got)
	}
}

func TestStripJSONFencesSingleLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"`" + "``json{\"x\":1}`" + "``", `{"x":1}`},
		{"`" + "``{\"x\":1}`" + "``", `{"x":1}`},
		{"`" + "``json[1,2,3]`" + "``", `[1,2,3]`},
	}
	for _, tc := range cases {
		got := stripJSONFences(tc.in)
		if got != tc.want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEnrichIntentFromLocalAISetsGeneralAndQuestion(t *testing.T) {
	for _, taskType := range []string{"general", "question"} {
		intent := Intent{Coding: true}
		enrichIntentFromLocalAI(&intent, LocalAIAnalysis{TaskType: taskType})
		if intent.Coding {
			t.Errorf("taskType=%q: Coding should be cleared", taskType)
		}
		if intent.TaskClass != taskType {
			t.Errorf("taskType=%q: TaskClass = %q, want %q", taskType, intent.TaskClass, taskType)
		}
	}
}

func TestEnrichIntentFromLocalAIClearsCodingOnReview(t *testing.T) {
	intent := Intent{Coding: true}
	enrichIntentFromLocalAI(&intent, LocalAIAnalysis{TaskType: "review"})
	if intent.Coding {
		t.Fatal("Coding should be cleared when TaskType=review")
	}
	if !intent.Review {
		t.Fatal("Review should be set when TaskType=review")
	}
}

func TestResolvePythonInterpreterErrorsWhenOnlyPython2IsAvailable(t *testing.T) {
	tempDir := t.TempDir()
	python2Path := filepath.Join(tempDir, "python")
	if err := os.WriteFile(python2Path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(python2Path) error = %v", err)
	}

	original := execLookPath
	execLookPath = func(file string) (string, error) {
		switch file {
		case "python":
			return python2Path, nil
		default:
			return "", errors.New("missing")
		}
	}
	t.Cleanup(func() {
		execLookPath = original
	})

	_, err := resolvePythonInterpreter()
	if err == nil {
		t.Fatal("expected unsupported Python 2 error")
	}
	if !strings.Contains(err.Error(), "Python 3 interpreter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteWithLocalAIReturnsErrorWhenOllamaUnreachableAndFallbacksDisabled(t *testing.T) {
	_, err := Route(Request{
		Prompt: "refactor the auth module",
		Agents: []agent.Agent{
			{Name: "local", Provider: agent.ProviderOllama, Model: "llama3.2"},
		},
		Shared: agent.SharedConfig{},
		Config: agent.RouterConfig{
			Mode:             "local-ai",
			LocalAIOllamaURL: "http://127.0.0.1:0",
			AllowFallbacks:   false,
		},
	})
	if err == nil {
		t.Fatal("expected error when Ollama unreachable and AllowFallbacks=false")
	}
	if !strings.Contains(err.Error(), "local-ai analysis failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
