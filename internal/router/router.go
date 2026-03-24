package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// Request captures one routing decision request.
type Request struct {
	Prompt string
	Agents []agent.Agent
	Shared agent.SharedConfig
	Config agent.RouterConfig
}

// Intent is a normalized prompt classification used for routing.
type Intent struct {
	TaskClass        string `json:"task_class"`
	Coding           bool   `json:"coding"`
	Review           bool   `json:"review"`
	Analysis         bool   `json:"analysis"`
	LatencySensitive bool   `json:"latency_sensitive"`
	PrivacySensitive bool   `json:"privacy_sensitive"`
}

// Candidate captures one scored routing option.
type Candidate struct {
	Agent   agent.Agent           `json:"agent"`
	Target  agent.ExecutionTarget `json:"target"`
	Route   agent.RouteConfig     `json:"route"`
	Score   int                   `json:"score"`
	Reasons []string              `json:"reasons,omitempty"`
}

// Decision captures the selected route plus alternatives.
type Decision struct {
	Mode       string      `json:"mode"`
	Intent     Intent      `json:"intent"`
	Selected   Candidate   `json:"selected"`
	Fallbacks  []Candidate `json:"fallbacks,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Route chooses an execution target using either heuristic or LangGraph mode.
func Route(req Request) (Decision, error) {
	cfg := mergeRouterConfig(req.Shared.Router, req.Config).WithDefaults()
	mode := cfg.EffectiveMode()
	if mode == "langgraph" {
		decision, err := routeWithLangGraph(req, cfg)
		if err == nil {
			return decision, nil
		}
		if !cfg.AllowFallbacks {
			return Decision{}, err
		}
	}
	decision, err := routeHeuristic(req, cfg)
	if err != nil {
		return Decision{}, err
	}
	if mode == "langgraph" {
		decision.Mode = "heuristic-fallback"
	}
	return decision, nil
}

func mergeRouterConfig(base, override agent.RouterConfig) agent.RouterConfig {
	out := base
	if strings.TrimSpace(override.Mode) != "" {
		out.Mode = override.Mode
	}
	if strings.TrimSpace(override.LangGraphCmd) != "" {
		out.LangGraphCmd = override.LangGraphCmd
	}
	if override.PreferLocal {
		out.PreferLocal = true
	}
	if override.PreferFast {
		out.PreferFast = true
	}
	if override.PreferLowCost {
		out.PreferLowCost = true
	}
	if override.LocalOnly {
		out.LocalOnly = true
	}
	if override.RequireApproval {
		out.RequireApproval = true
	}
	if override.AllowFallbacks {
		out.AllowFallbacks = true
	}
	return out
}

func routeHeuristic(req Request, cfg agent.RouterConfig) (Decision, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Decision{}, errors.New("routing requires non-empty prompt")
	}
	intent := classifyPrompt(prompt)
	candidates := buildCandidates(req.Agents, intent, cfg, prompt)
	if len(candidates) == 0 {
		return Decision{}, errors.New("no routing candidates available")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].Target.Local != candidates[j].Target.Local {
				return candidates[i].Target.Local
			}
			return candidates[i].Agent.Name < candidates[j].Agent.Name
		}
		return candidates[i].Score > candidates[j].Score
	})

	selectedIdx := -1
	for i, candidate := range candidates {
		if candidate.Target.Supported && candidate.Score > -1000 {
			selectedIdx = i
			break
		}
	}
	if selectedIdx == -1 {
		return Decision{}, errors.New("no supported routing target satisfies the current policy")
	}

	selected := candidates[selectedIdx]
	fallbacks := make([]Candidate, 0, 3)
	if cfg.AllowFallbacks {
		for i, candidate := range candidates {
			if i == selectedIdx || !candidate.Target.Supported || candidate.Score <= -1000 {
				continue
			}
			fallbacks = append(fallbacks, candidate)
			if len(fallbacks) == 3 {
				break
			}
		}
	}

	return Decision{
		Mode:       "heuristic",
		Intent:     intent,
		Selected:   selected,
		Fallbacks:  fallbacks,
		Candidates: candidates,
	}, nil
}

func buildCandidates(agents []agent.Agent, intent Intent, cfg agent.RouterConfig, prompt string) []Candidate {
	candidates := make([]Candidate, 0, len(agents))
	for _, a := range agents {
		profile := a.EffectiveRouteConfig()
		if profile.Disabled {
			continue
		}
		target := agent.ResolveExecutionTarget(a)
		score, reasons := scoreCandidate(a, profile, target, intent, cfg, prompt)
		candidates = append(candidates, Candidate{
			Agent:   a,
			Target:  target,
			Route:   profile,
			Score:   score,
			Reasons: reasons,
		})
	}
	return candidates
}

func scoreCandidate(a agent.Agent, profile agent.RouteConfig, target agent.ExecutionTarget, intent Intent, cfg agent.RouterConfig, prompt string) (int, []string) {
	score := profile.Priority
	reasons := []string{fmt.Sprintf("base priority %d", profile.Priority)}
	caps := make(map[string]struct{}, len(profile.Capabilities))
	for _, capability := range profile.Capabilities {
		caps[capability] = struct{}{}
	}

	if !target.Supported {
		score -= 5000
		reasons = append(reasons, fmt.Sprintf("runner %s is not supported yet", target.Runner))
	}
	if cfg.LocalOnly && !target.Local {
		score -= 5000
		reasons = append(reasons, "rejected by local-only policy")
	}
	if cfg.PreferLocal && target.Local {
		bonus := 20
		if intent.Coding {
			bonus = 6
		}
		score += bonus
		reasons = append(reasons, "local target preferred")
	}
	if cfg.PreferFast {
		score += tierScore(profile.LatencyTier, 15, 5, -10)
		reasons = append(reasons, fmt.Sprintf("latency tier %s evaluated", profile.LatencyTier))
	}
	if cfg.PreferLowCost {
		score += tierScore(profile.CostTier, 15, 5, -10)
		reasons = append(reasons, fmt.Sprintf("cost tier %s evaluated", profile.CostTier))
	}
	if intent.LatencySensitive {
		score += tierScore(profile.LatencyTier, 20, 5, -10)
		reasons = append(reasons, "latency-sensitive prompt")
	}
	if intent.PrivacySensitive {
		score += privacyScore(profile.PrivacyTier)
		reasons = append(reasons, fmt.Sprintf("privacy tier %s evaluated", profile.PrivacyTier))
	}
	if target.Local && !intent.LatencySensitive && cfg.PreferLocal {
		score += 5
	}

	wanted := requiredCapability(intent)
	if wanted != "" {
		if _, ok := caps[wanted]; ok {
			score += 30
			reasons = append(reasons, fmt.Sprintf("supports %s tasks", wanted))
		} else {
			score -= 40
			reasons = append(reasons, fmt.Sprintf("missing %s capability", wanted))
		}
	}
	if _, ok := caps[agent.RouteCapabilityGeneral]; ok {
		score += 5
	}
	if strings.Contains(strings.ToLower(prompt), strings.ToLower(string(a.Provider))) {
		score += 8
		reasons = append(reasons, fmt.Sprintf("prompt explicitly references provider %s", a.Provider))
	}
	if a.Model != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(a.Model)) {
		score += 10
		reasons = append(reasons, fmt.Sprintf("prompt explicitly references model %s", a.Model))
	}
	if intent.Coding {
		switch target.Runner {
		case agent.RunnerCodexCLI, agent.RunnerClaudeCLI:
			score += 25
			reasons = append(reasons, fmt.Sprintf("runner %s is strong for coding", target.Runner))
		case agent.RunnerOllamaHTTP:
			score += 6
			reasons = append(reasons, "local ollama runner can handle coding prompts")
		}
	}
	if !intent.Coding && target.Runner == agent.RunnerOllamaHTTP && target.Local {
		score += 10
		reasons = append(reasons, "non-coding prompt prefers local Ollama by default")
	}
	return score, reasons
}

func tierScore(tier string, low, medium, high int) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "low":
		return low
	case "medium":
		return medium
	case "high":
		return high
	default:
		return 0
	}
}

func privacyScore(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "local":
		return 30
	case "restricted":
		return 10
	case "remote":
		return -20
	default:
		return 0
	}
}

func requiredCapability(intent Intent) string {
	switch {
	case intent.Review:
		return agent.RouteCapabilityReview
	case intent.Coding:
		return agent.RouteCapabilityCoding
	case intent.Analysis:
		return agent.RouteCapabilityAnalysis
	default:
		return agent.RouteCapabilityGeneral
	}
}

func classifyPrompt(prompt string) Intent {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	intent := Intent{TaskClass: "general"}
	codingTerms := []string{"code", "coding", "implement", "bug", "fix", "refactor", "test", "function", "compile", "build", "pr", "issue", "repository", "repo", "golang", "python", "javascript", "rust"}
	reviewTerms := []string{"review", "diff", "pull request", "regression", "risk", "bug", "edge case"}
	analysisTerms := []string{"analyze", "investigate", "compare", "architecture", "design", "tradeoff", "strategy"}
	latencyTerms := []string{"urgent", "asap", "quickly", "immediately", "fast", "time-sensitive"}
	privacyTerms := []string{"private", "confidential", "local only", "offline", "air-gapped", "airgapped", "no network"}

	intent.Coding = containsAny(lower, codingTerms)
	intent.Review = containsAny(lower, reviewTerms)
	intent.Analysis = containsAny(lower, analysisTerms)
	intent.LatencySensitive = containsAny(lower, latencyTerms)
	intent.PrivacySensitive = containsAny(lower, privacyTerms)

	switch {
	case intent.Review:
		intent.TaskClass = "review"
	case intent.Coding:
		intent.TaskClass = "coding"
	case intent.Analysis:
		intent.TaskClass = "analysis"
	default:
		intent.TaskClass = "general"
	}
	return intent
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

type langGraphInput struct {
	Prompt     string             `json:"prompt"`
	Config     agent.RouterConfig `json:"config"`
	Candidates []Candidate        `json:"candidates"`
}

type langGraphOutput struct {
	Mode          string   `json:"mode"`
	SelectedAgent string   `json:"selected_agent"`
	Fallbacks     []string `json:"fallbacks,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
}

func routeWithLangGraph(req Request, cfg agent.RouterConfig) (Decision, error) {
	cmdText := strings.TrimSpace(cfg.LangGraphCmd)
	if cmdText == "" {
		cmdText = strings.TrimSpace(os.Getenv("AGENTVAULT_LANGGRAPH_ROUTER_CMD"))
	}
	if cmdText == "" {
		return Decision{}, errors.New("langgraph mode requires router command or AGENTVAULT_LANGGRAPH_ROUTER_CMD")
	}
	candidates := buildCandidates(req.Agents, classifyPrompt(req.Prompt), cfg, req.Prompt)
	if len(candidates) == 0 {
		return Decision{}, errors.New("no routing candidates available")
	}
	parts := strings.Fields(cmdText)
	if len(parts) == 0 {
		return Decision{}, errors.New("langgraph router command is empty")
	}

	payload := langGraphInput{Prompt: req.Prompt, Config: cfg, Candidates: candidates}
	body, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Decision{}, fmt.Errorf("langgraph router failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var out langGraphOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Decision{}, fmt.Errorf("decoding langgraph router output: %w", err)
	}
	selected, ok := findCandidate(candidates, out.SelectedAgent)
	if !ok {
		return Decision{}, fmt.Errorf("langgraph router selected unknown agent %q", out.SelectedAgent)
	}
	fallbacks := make([]Candidate, 0, len(out.Fallbacks))
	for _, name := range out.Fallbacks {
		candidate, ok := findCandidate(candidates, name)
		if ok && candidate.Agent.Name != selected.Agent.Name {
			fallbacks = append(fallbacks, candidate)
		}
	}
	if len(out.Reasons) > 0 {
		selected.Reasons = append(selected.Reasons, out.Reasons...)
	}
	return Decision{
		Mode:       chooseNonEmpty(out.Mode, "langgraph"),
		Intent:     classifyPrompt(req.Prompt),
		Selected:   selected,
		Fallbacks:  fallbacks,
		Candidates: candidates,
	}, nil
}

func findCandidate(candidates []Candidate, name string) (Candidate, bool) {
	for _, candidate := range candidates {
		if candidate.Agent.Name == name {
			return candidate, true
		}
	}
	return Candidate{}, false
}

func chooseNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
