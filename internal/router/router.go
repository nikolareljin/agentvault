package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

var execLookPath = exec.LookPath

// llmBalancer is shared across calls so circuit-breaker state and EWMA latency persist.
var llmBalancer = NewBalancer()

var (
	codingTerms   = []string{"code", "coding", "implement", "bug", "fix", "refactor", "test", "function", "compile", "build", "issue", "repository", "repo", "golang", "python", "javascript", "rust"}
	reviewTerms   = []string{"review", "diff", "pull request", "regression", "risk", "edge case"}
	analysisTerms = []string{"analyze", "investigate", "compare", "architecture", "design", "tradeoff", "strategy"}
	latencyTerms  = []string{"urgent", "asap", "quickly", "immediately", "fast", "time-sensitive"}
	privacyTerms  = []string{"private", "confidential", "local only", "offline", "air-gapped", "airgapped", "no network"}
)

// Request captures one routing decision request.
type Request struct {
	Prompt            string
	Agents            []agent.Agent
	Shared            agent.SharedConfig
	Config            agent.RouterConfig
	ModelCapabilities []agent.ModelCapabilityEntry
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

// AgentView is the redacted agent metadata exposed by routing outputs.
type AgentView struct {
	Name     string         `json:"name"`
	Provider agent.Provider `json:"provider"`
	Model    string         `json:"model,omitempty"`
	Backend  string         `json:"backend,omitempty"`
	Role     string         `json:"role,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
}

// Candidate captures one scored routing option.
type Candidate struct {
	Agent   AgentView             `json:"agent"`
	Target  agent.ExecutionTarget `json:"target"`
	Route   agent.RouteConfig     `json:"route"`
	Score   int                   `json:"score"`
	Reasons []string              `json:"reasons,omitempty"`

	resolvedAgent agent.Agent `json:"-"`
}

func (c Candidate) AgentConfig() agent.Agent {
	return c.resolvedAgent
}

// Decision captures the selected route plus alternatives.
type Decision struct {
	Mode                string      `json:"mode"`
	Intent              Intent      `json:"intent"`
	Selected            Candidate   `json:"selected"`
	Fallbacks           []Candidate `json:"fallbacks,omitempty"`
	Candidates          []Candidate `json:"candidates,omitempty"`
	EffectiveImportance string      `json:"effective_importance,omitempty"`
	EffectiveDeadline   string      `json:"effective_deadline,omitempty"`
}

// Route chooses an execution target using either heuristic, LangGraph, or local-ai mode.
func Route(req Request) (Decision, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return Decision{}, errors.New("routing requires non-empty prompt")
	}
	cfg := mergeRouterConfig(req.Shared.Router, req.Config).WithDefaults()
	if err := cfg.Validate(); err != nil {
		return Decision{}, err
	}
	mode := cfg.EffectiveMode()
	switch mode {
	case "langgraph":
		decision, err := routeWithLangGraph(req, cfg)
		if err == nil {
			return decision, nil
		}
		if !cfg.AllowFallbacks {
			return Decision{}, err
		}
		decision, err = routeHeuristic(req, cfg)
		if err != nil {
			return Decision{}, err
		}
		decision.Mode = "heuristic-fallback"
		return decision, nil
	case "local-ai":
		return routeWithLocalAI(req, cfg)
	case "llm-router":
		return routeWithLLMRouter(req, cfg)
	default:
		return routeHeuristic(req, cfg)
	}
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
	if strings.TrimSpace(override.Importance) != "" {
		out.Importance = override.Importance
	}
	if strings.TrimSpace(override.Deadline) != "" {
		out.Deadline = override.Deadline
	}
	if strings.TrimSpace(override.LocalAIModel) != "" {
		out.LocalAIModel = override.LocalAIModel
	}
	if strings.TrimSpace(override.LocalAIOllamaURL) != "" {
		out.LocalAIOllamaURL = override.LocalAIOllamaURL
	}
	if strings.TrimSpace(override.LLMRouterURL) != "" {
		out.LLMRouterURL = override.LLMRouterURL
	}
	if strings.TrimSpace(override.LLMRouterModel) != "" {
		out.LLMRouterModel = override.LLMRouterModel
	}
	if override.LLMRouterTimeoutSecs > 0 {
		out.LLMRouterTimeoutSecs = override.LLMRouterTimeoutSecs
	}
	if override.LLMRouterEnableCostEst {
		out.LLMRouterEnableCostEst = true
	}
	if strings.TrimSpace(override.LLMRouterModelPath) != "" {
		out.LLMRouterModelPath = override.LLMRouterModelPath
	}
	if override.LLMRouterContextSize != 0 {
		out.LLMRouterContextSize = override.LLMRouterContextSize
	}
	if override.LLMRouterThreads != 0 {
		out.LLMRouterThreads = override.LLMRouterThreads
	}
	if override.LLMRouterGPULayers != 0 {
		out.LLMRouterGPULayers = override.LLMRouterGPULayers
	}
	return out
}

func routeHeuristic(req Request, cfg agent.RouterConfig) (Decision, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Decision{}, errors.New("routing requires non-empty prompt")
	}
	intent := classifyPrompt(prompt)
	candidates := buildCandidates(req.Agents, intent, cfg, prompt, req.ModelCapabilities)
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
		if candidateAllowed(candidate, cfg) {
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
			if i == selectedIdx || !candidateAllowed(candidate, cfg) {
				continue
			}
			fallbacks = append(fallbacks, candidate)
			if len(fallbacks) == 3 {
				break
			}
		}
	}

	return Decision{
		Mode:                "heuristic",
		Intent:              intent,
		Selected:            selected,
		Fallbacks:           fallbacks,
		Candidates:          candidates,
		EffectiveImportance: cfg.Importance,
		EffectiveDeadline:   cfg.Deadline,
	}, nil
}

func buildCandidates(agents []agent.Agent, intent Intent, cfg agent.RouterConfig, prompt string, capabilities ...[]agent.ModelCapabilityEntry) []Candidate {
	var caps []agent.ModelCapabilityEntry
	if len(capabilities) > 0 {
		caps = capabilities[0]
	}
	candidates := make([]Candidate, 0, len(agents))
	for _, a := range agents {
		profile := a.EffectiveRouteConfig()
		if profile.Disabled {
			continue
		}
		// Augment profile capabilities from the capability registry when the
		// agent's BaseURL matches a registered endpoint.
		if len(caps) > 0 && strings.TrimSpace(a.BaseURL) != "" {
			normalizedBase := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
			for _, cap := range caps {
				if strings.TrimRight(cap.EndpointURL, "/") == normalizedBase &&
					(cap.ModelName == "" || cap.ModelName == a.Model) {
					profile.Capabilities = mergeCapabilities(profile.Capabilities, cap.Capabilities)
					break
				}
			}
		}
		target := agent.ResolveExecutionTarget(a)
		score, reasons := scoreCandidate(a, profile, target, intent, cfg, prompt)
		candidates = append(candidates, Candidate{
			Agent: AgentView{
				Name:     a.Name,
				Provider: a.Provider,
				Model:    a.Model,
				Backend:  a.Backend,
				Role:     a.Role,
				Tags:     append([]string(nil), a.Tags...),
			},
			Target:        target,
			Route:         profile,
			Score:         score,
			Reasons:       reasons,
			resolvedAgent: a,
		})
	}
	return candidates
}

func candidateAllowed(candidate Candidate, cfg agent.RouterConfig) bool {
	if !candidate.Target.Supported {
		return false
	}
	if cfg.LocalOnly && !candidate.Target.Local {
		return false
	}
	return true
}

func scoreCandidate(a agent.Agent, profile agent.RouteConfig, target agent.ExecutionTarget, intent Intent, cfg agent.RouterConfig, prompt string) (int, []string) {
	score := profile.Priority
	reasons := []string{fmt.Sprintf("base priority %d", profile.Priority)}
	promptLower := strings.ToLower(prompt)
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
		reasons = append(reasons, "non-latency-sensitive local target (+5)")
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
	// High-complexity coding tasks (intent.Analysis set alongside intent.Coding) also benefit
	// from analysis capability; only apply the bonus when the agent supports the required coding
	// capability, so analysis-only agents are not unintentionally boosted for coding prompts.
	if intent.Analysis && wanted == agent.RouteCapabilityCoding {
		if _, hasWanted := caps[wanted]; hasWanted {
			if _, ok := caps[agent.RouteCapabilityAnalysis]; ok {
				score += 15
				reasons = append(reasons, "high-complexity coding task benefits from analysis capability")
			}
		}
	}
	if _, ok := caps[agent.RouteCapabilityGeneral]; ok {
		score += 5
	}
	providerLower := strings.ToLower(string(a.Provider))
	if providerLower != "" && strings.Contains(promptLower, providerLower) {
		score += 8
		reasons = append(reasons, fmt.Sprintf("prompt explicitly references provider %s", a.Provider))
	}
	if a.Model != "" {
		modelLower := strings.ToLower(a.Model)
		if strings.Contains(promptLower, modelLower) {
			score += 10
			reasons = append(reasons, fmt.Sprintf("prompt explicitly references model %s", a.Model))
		}
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

	importanceScore, importanceReasons := importanceDeadlineScore(cfg.Importance, cfg.Deadline, target, profile)
	score += importanceScore
	reasons = append(reasons, importanceReasons...)

	return score, reasons
}

// importanceDeadlineScore applies score adjustments based on the requested importance level and deadline.
func importanceDeadlineScore(importance, deadline string, target agent.ExecutionTarget, profile agent.RouteConfig) (int, []string) {
	imp := strings.ToLower(strings.TrimSpace(importance))
	dl := strings.ToLower(strings.TrimSpace(deadline))
	if imp == "" && dl == "" {
		return 0, nil
	}

	score := 0
	var reasons []string

	switch imp {
	case "critical":
		if target.Local {
			score -= 30
			reasons = append(reasons, "critical importance: penalizing local target for quality")
		} else {
			score += 50
			reasons = append(reasons, "critical importance: preferring cloud runner")
		}
	case "high":
		if !target.Local {
			score += 20
			reasons = append(reasons, "high importance: preferring cloud runner")
		}
	case "low":
		if target.Local {
			score += 25
			reasons = append(reasons, "low importance: preferring local target")
		}
		score += tierScore(profile.CostTier, 15, 5, -10)
		reasons = append(reasons, fmt.Sprintf("low importance: cost tier %s evaluated", profile.CostTier))
	}

	switch dl {
	case "immediate":
		bonus := tierScore(profile.LatencyTier, 30, 10, -20)
		score += bonus
		reasons = append(reasons, fmt.Sprintf("immediate deadline: latency tier %s evaluated", profile.LatencyTier))
		if target.Local {
			score += 15
			reasons = append(reasons, "immediate deadline: local target favored for low latency")
		}
	case "background":
		bonus := tierScore(profile.CostTier, 25, 5, -10)
		score += bonus
		reasons = append(reasons, fmt.Sprintf("background deadline: cost tier %s evaluated", profile.CostTier))
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
	normalized := normalizePromptText(lower)
	tokens := tokenizePrompt(normalized)
	intent := Intent{TaskClass: "general"}
	intent.Coding = containsAnyTerm(normalized, tokens, codingTerms)
	intent.Review = containsAnyTerm(normalized, tokens, reviewTerms)
	intent.Analysis = containsAnyTerm(normalized, tokens, analysisTerms)
	intent.LatencySensitive = containsAnyTerm(normalized, tokens, latencyTerms)
	intent.PrivacySensitive = containsAnyTerm(normalized, tokens, privacyTerms)

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

func containsAnyTerm(prompt string, tokens map[string]struct{}, terms []string) bool {
	if prompt == "" {
		return false
	}
	if len(tokens) == 0 {
		tokens = tokenizePrompt(prompt)
	}
	for _, term := range terms {
		normalizedTerm := normalizePromptText(strings.ToLower(term))
		if normalizedTerm == "" {
			continue
		}
		if strings.Contains(normalizedTerm, " ") {
			if strings.Contains(prompt, normalizedTerm) {
				return true
			}
			continue
		}
		if _, ok := tokens[normalizedTerm]; ok {
			return true
		}
	}
	return false
}

func normalizePromptText(prompt string) string {
	return strings.Join(strings.FieldsFunc(prompt, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}), " ")
}

func tokenizePrompt(prompt string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, token := range strings.Fields(prompt) {
		tokens[token] = struct{}{}
	}
	return tokens
}

func canonicalizeExecutablePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving absolute executable path: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return resolvedPath, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return absPath, nil
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func routeWithLocalAI(req Request, cfg agent.RouterConfig) (Decision, error) {
	trimmedPrompt := strings.TrimSpace(req.Prompt)

	ollamaURL := strings.TrimSpace(cfg.LocalAIOllamaURL)
	model := strings.TrimSpace(cfg.LocalAIModel)

	analysis, err := AnalyzeWithLocalAI(trimmedPrompt, ollamaURL, model, 10*time.Second)
	if err != nil {
		if !cfg.AllowFallbacks {
			return Decision{}, fmt.Errorf("local-ai analysis failed: %w", err)
		}
		decision, hErr := routeHeuristic(req, cfg)
		if hErr != nil {
			return Decision{}, hErr
		}
		decision.Mode = "local-ai-fallback"
		return decision, nil
	}

	intent := classifyPrompt(trimmedPrompt)
	enrichIntentFromLocalAI(&intent, analysis)

	// High urgency from local AI overrides deadline to immediate unless explicitly set
	overrideCfg := cfg
	if analysis.Urgency == "high" && strings.TrimSpace(cfg.Deadline) == "" {
		overrideCfg.Deadline = "immediate"
	}
	// Privacy sensitive: apply local-only if not already restricted
	if analysis.PrivacySensitive && !cfg.LocalOnly {
		overrideCfg.LocalOnly = true
	}

	candidates := buildCandidates(req.Agents, intent, overrideCfg, trimmedPrompt, req.ModelCapabilities)
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
		if candidateAllowed(candidate, overrideCfg) {
			selectedIdx = i
			break
		}
	}
	if selectedIdx == -1 {
		return Decision{}, errors.New("no supported routing target satisfies the current policy")
	}

	selected := candidates[selectedIdx]
	fallbacks := make([]Candidate, 0, 3)
	if overrideCfg.AllowFallbacks {
		for i, candidate := range candidates {
			if i == selectedIdx || !candidateAllowed(candidate, overrideCfg) {
				continue
			}
			fallbacks = append(fallbacks, candidate)
			if len(fallbacks) == 3 {
				break
			}
		}
	}

	aiReason := fmt.Sprintf("local-ai: complexity=%d/10 task=%s urgency=%s needs_tools=%v",
		analysis.Complexity, analysis.TaskType, analysis.Urgency, analysis.NeedsTools)
	candidates[selectedIdx].Reasons = append([]string{aiReason}, candidates[selectedIdx].Reasons...)
	selected = candidates[selectedIdx]

	return Decision{
		Mode:                "local-ai",
		Intent:              intent,
		Selected:            selected,
		Fallbacks:           fallbacks,
		Candidates:          candidates,
		EffectiveImportance: overrideCfg.Importance,
		EffectiveDeadline:   overrideCfg.Deadline,
	}, nil
}

func routeWithLLMRouter(req Request, cfg agent.RouterConfig) (Decision, error) {
	trimmedPrompt := strings.TrimSpace(req.Prompt)
	intent := classifyPrompt(trimmedPrompt)
	overrideCfg := cfg

	candidates := buildCandidates(req.Agents, intent, overrideCfg, trimmedPrompt, req.ModelCapabilities)
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

	llmCfg := LLMRouterConfig{
		URL:           cfg.LLMRouterURL,
		Model:         cfg.LLMRouterModel,
		TimeoutSecs:   cfg.LLMRouterTimeoutSecs,
		EnableCostEst: cfg.LLMRouterEnableCostEst,
		ModelPath:     cfg.LLMRouterModelPath,
		ContextSize:   cfg.LLMRouterContextSize,
		Threads:       cfg.LLMRouterThreads,
		GPULayers:     cfg.LLMRouterGPULayers,
	}

	decision, err := AnalyzeWithLLMRouter(context.Background(), trimmedPrompt, candidates, llmCfg)
	if err != nil {
		// Graceful degradation: fall through to heuristic, never surface the error to the caller.
		hDecision, hErr := routeHeuristic(req, overrideCfg)
		if hErr != nil {
			return Decision{}, hErr
		}
		hDecision.Mode = "heuristic-fallback"
		fallbackReason := fmt.Sprintf("llm-router unavailable (%s), used heuristic fallback", err.Error())
		hDecision.Selected.Reasons = append([]string{fallbackReason}, hDecision.Selected.Reasons...)
		return hDecision, nil
	}

	enrichIntentFromLLMDecision(&intent, decision)

	if decision.RoutingFactors.PrivacySensitive && !overrideCfg.LocalOnly {
		overrideCfg.LocalOnly = true
		// Re-filter candidates: the list was built before the LLM marked the prompt
		// privacy-sensitive, so remote targets must be excluded now.
		local := candidates[:0]
		for _, c := range candidates {
			if c.Target.Local {
				local = append(local, c)
			}
		}
		if len(local) > 0 {
			candidates = local
		}
	}
	if decision.RoutingFactors.TimeSensitive && strings.TrimSpace(overrideCfg.Deadline) == "" {
		overrideCfg.Deadline = "immediate"
	}

	selected, err := llmBalancer.PickBest(context.Background(), decision, candidates)
	if err != nil {
		return Decision{}, err
	}

	llmReason := fmt.Sprintf("llm-router: %s (confidence=%.2f complexity=%d/10 task=%s)",
		decision.Reasoning, decision.Confidence,
		decision.RoutingFactors.Complexity, decision.RoutingFactors.TaskType)
	if llmCfg.EnableCostEst && (decision.EstInputTokens > 0 || decision.EstOutputTokens > 0) {
		llmReason += fmt.Sprintf(" est_tokens=in:%d out:%d", decision.EstInputTokens, decision.EstOutputTokens)
	}
	selected.Reasons = append([]string{llmReason}, selected.Reasons...)

	fallbacks := make([]Candidate, 0, 3)
	if overrideCfg.AllowFallbacks {
		for _, name := range decision.FallbackAgents {
			for _, c := range candidates {
				if c.Agent.Name == name && c.Agent.Name != selected.Agent.Name {
					fallbacks = append(fallbacks, c)
					break
				}
			}
			if len(fallbacks) == 3 {
				break
			}
		}
	}

	return Decision{
		Mode:                "llm-router",
		Intent:              intent,
		Selected:            selected,
		Fallbacks:           fallbacks,
		Candidates:          candidates,
		EffectiveImportance: overrideCfg.Importance,
		EffectiveDeadline:   overrideCfg.Deadline,
	}, nil
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
	trimmedPrompt := strings.TrimSpace(req.Prompt)
	intent := classifyPrompt(trimmedPrompt)
	scriptPath := strings.TrimSpace(cfg.LangGraphCmd)
	if scriptPath == "" {
		scriptPath = strings.TrimSpace(os.Getenv("AGENTVAULT_LANGGRAPH_ROUTER_CMD"))
	}
	if scriptPath == "" {
		return Decision{}, errors.New("langgraph mode requires a router script path or AGENTVAULT_LANGGRAPH_ROUTER_CMD")
	}
	candidates := buildCandidates(req.Agents, intent, cfg, trimmedPrompt, req.ModelCapabilities)
	if len(candidates) == 0 {
		return Decision{}, errors.New("no routing candidates available")
	}
	resolvedScriptPath, err := resolveLangGraphScriptPath(scriptPath)
	if err != nil {
		return Decision{}, err
	}

	payload := langGraphInput{Prompt: trimmedPrompt, Config: cfg, Candidates: candidates}
	body, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pythonCmd, err := resolvePythonInterpreter()
	if err != nil {
		return Decision{}, err
	}
	// #nosec G204,G702 -- pythonCmd is the exact validated Python 3 interpreter path, and resolvedScriptPath is a canonicalized local .py file.
	cmd := exec.CommandContext(ctx, pythonCmd, resolvedScriptPath)
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
	trimmedSelected := strings.TrimSpace(out.SelectedAgent)
	if trimmedSelected == "" {
		return Decision{}, errors.New("decoding langgraph router output: selected_agent is empty or whitespace")
	}
	selectedIdx := findCandidateIndex(candidates, trimmedSelected)
	if selectedIdx == -1 {
		return Decision{}, fmt.Errorf("langgraph router selected unknown agent %q", trimmedSelected)
	}
	selected := candidates[selectedIdx]
	if !candidateAllowed(selected, cfg) {
		return Decision{}, fmt.Errorf("langgraph router selected disallowed agent %q", trimmedSelected)
	}
	fallbacks := make([]Candidate, 0, len(out.Fallbacks))
	for _, name := range out.Fallbacks {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		candidate, ok := findCandidate(candidates, trimmedName)
		if ok && candidate.Agent.Name != selected.Agent.Name && candidateAllowed(candidate, cfg) {
			fallbacks = append(fallbacks, candidate)
		}
	}
	if len(out.Reasons) > 0 {
		candidates[selectedIdx].Reasons = append(candidates[selectedIdx].Reasons, out.Reasons...)
		selected = candidates[selectedIdx]
	}
	return Decision{
		Mode:                chooseNonEmpty(out.Mode, "langgraph"),
		Intent:              intent,
		Selected:            selected,
		Fallbacks:           fallbacks,
		Candidates:          candidates,
		EffectiveImportance: cfg.Importance,
		EffectiveDeadline:   cfg.Deadline,
	}, nil
}

func resolvePythonInterpreter() (string, error) {
	candidates := []string{"python3", "python"}
	var lastErr error
	for _, name := range candidates {
		path, err := execLookPath(name)
		if err != nil {
			lastErr = err
			continue
		}

		path, err = canonicalizeExecutablePath(path)
		if err != nil {
			lastErr = fmt.Errorf("%s path could not be canonicalized: %w", name, err)
			continue
		}

		if err := validatePython3Interpreter(path); err != nil {
			lastErr = fmt.Errorf("%s is not a supported Python 3 interpreter: %w", name, err)
			continue
		}

		return path, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("langgraph mode requires a Python 3 interpreter on PATH (checked %s): %w", strings.Join(candidates, ", "), lastErr)
	}
	return "", errors.New("langgraph mode requires python3 or python (Python 3) on PATH")
}

func validatePython3Interpreter(path string) error {
	if strings.ContainsAny(path, "\n\r\t") {
		return errors.New("interpreter path contains invalid whitespace")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "-c", "import sys; raise SystemExit(0 if sys.version_info[0] >= 3 else 1)")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return errors.New("timeout while checking interpreter version")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func resolveLangGraphScriptPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("langgraph router script path is empty")
	}
	if strings.ContainsAny(trimmed, "\n\r\t") {
		return "", errors.New("langgraph router script path contains invalid whitespace")
	}
	absolute, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", fmt.Errorf("resolve langgraph router script path: %w", err)
	}
	resolved := absolute
	if symlinkTarget, err := filepath.EvalSymlinks(absolute); err == nil {
		resolved = symlinkTarget
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve langgraph router symlink: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat langgraph router script: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("langgraph router path must point to a Python file, not a directory")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("langgraph router path must point to a regular file")
	}
	if strings.ToLower(filepath.Ext(resolved)) != ".py" {
		return "", errors.New("langgraph router path must point to a .py file")
	}
	return resolved, nil
}

func findCandidate(candidates []Candidate, name string) (Candidate, bool) {
	idx := findCandidateIndex(candidates, name)
	if idx == -1 {
		return Candidate{}, false
	}
	return candidates[idx], true
}

func findCandidateIndex(candidates []Candidate, name string) int {
	for i, candidate := range candidates {
		if candidate.Agent.Name == name {
			return i
		}
	}
	return -1
}

func chooseNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func mergeCapabilities(existing, additional []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, c := range existing {
		seen[strings.ToLower(c)] = true
	}
	out := append([]string(nil), existing...)
	for _, c := range additional {
		lc := strings.ToLower(c)
		if !seen[lc] {
			out = append(out, lc)
			seen[lc] = true
		}
	}
	return out
}
