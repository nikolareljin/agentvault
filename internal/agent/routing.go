package agent

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunnerKind identifies the concrete execution path for an agent target.
type RunnerKind string

const (
	RunnerUnknown    RunnerKind = "unknown"
	RunnerOllamaHTTP RunnerKind = "ollama_http"
	RunnerCodexCLI   RunnerKind = "codex_cli"
	RunnerClaudeCLI  RunnerKind = "claude_cli"
	RunnerGeminiCLI  RunnerKind = "gemini_cli"
	RunnerOpenAIHTTP RunnerKind = "openai_http"
	RunnerBedrockAPI RunnerKind = "bedrock_api"
)

// Routing capability labels used by heuristic and LangGraph routing.
const (
	RouteCapabilityGeneral  = "general"
	RouteCapabilityCoding   = "coding"
	RouteCapabilityReview   = "review"
	RouteCapabilityAnalysis = "analysis"
)

var validRouteCapabilities = map[string]struct{}{
	RouteCapabilityGeneral:  {},
	RouteCapabilityCoding:   {},
	RouteCapabilityReview:   {},
	RouteCapabilityAnalysis: {},
}

var validRouteTiers = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

var validRoutePrivacyTiers = map[string]struct{}{
	"local":      {},
	"restricted": {},
	"remote":     {},
}

// RouteConfig stores per-agent routing metadata.
type RouteConfig struct {
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	LatencyTier  string   `json:"latency_tier,omitempty" yaml:"latency_tier,omitempty"`
	CostTier     string   `json:"cost_tier,omitempty"    yaml:"cost_tier,omitempty"`
	PrivacyTier  string   `json:"privacy_tier,omitempty" yaml:"privacy_tier,omitempty"`
	Priority     int      `json:"priority,omitempty"     yaml:"priority,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"     yaml:"disabled,omitempty"`
}

// RouterConfig stores shared prompt-router settings.
type RouterConfig struct {
	Mode             string `json:"mode,omitempty"`
	LangGraphCmd     string `json:"langgraph_command,omitempty"`
	PreferLocal      bool   `json:"prefer_local,omitempty"`
	PreferFast       bool   `json:"prefer_fast,omitempty"`
	PreferLowCost    bool   `json:"prefer_low_cost,omitempty"`
	LocalOnly        bool   `json:"local_only,omitempty"`
	AllowFallbacks   bool   `json:"allow_fallbacks,omitempty"`
	RequireApproval  bool   `json:"require_approval,omitempty"`
	Importance       string `json:"importance,omitempty"`          // low|medium|high|critical
	Deadline         string `json:"deadline,omitempty"`            // immediate|normal|background
	LocalAIModel     string `json:"local_ai_model,omitempty"`      // model used for local-ai routing classification
	LocalAIOllamaURL string `json:"local_ai_ollama_url,omitempty"` // ollama base URL override for local-ai routing

	// llm-router mode: calls a local llama.cpp or bitnet.cpp server
	LLMRouterURL           string `json:"llm_router_url,omitempty"             yaml:"llm_router_url"`
	LLMRouterModel         string `json:"llm_router_model,omitempty"           yaml:"llm_router_model"`
	LLMRouterTimeoutSecs   int    `json:"llm_router_timeout_secs,omitempty"    yaml:"llm_router_timeout_secs"`
	LLMRouterEnableCostEst bool   `json:"llm_router_enable_cost_est,omitempty" yaml:"llm_router_enable_cost_est"`

	// llm-router embedded mode: path to a local GGUF model file for in-process inference.
	// When set, inference runs inside the binary (no HTTP server required).
	// Requires agentvault built with `make build-bitnet` (-tags localllm).
	LLMRouterModelPath   string `json:"llm_router_model_path,omitempty"    yaml:"llm_router_model_path"`
	LLMRouterContextSize int    `json:"llm_router_context_size,omitempty"  yaml:"llm_router_context_size"`
	LLMRouterThreads     int    `json:"llm_router_threads,omitempty"       yaml:"llm_router_threads"`
	LLMRouterGPULayers   int    `json:"llm_router_gpu_layers,omitempty"    yaml:"llm_router_gpu_layers"`
}

func (cfg RouterConfig) IsZero() bool {
	return cfg == RouterConfig{}
}

func (cfg RouterConfig) Validate() error {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "" {
		switch mode {
		case "heuristic", "langgraph", "local-ai", "llm-router":
		default:
			return fmt.Errorf("unknown router mode: %s", cfg.Mode)
		}
	}
	if mode == "llm-router" &&
		strings.TrimSpace(cfg.LLMRouterURL) == "" &&
		strings.TrimSpace(cfg.LLMRouterModelPath) == "" {
		return fmt.Errorf("llm-router mode requires --llm-router-url (HTTP server) or --llm-router-model-path (embedded inference)")
	}
	if imp := strings.ToLower(strings.TrimSpace(cfg.Importance)); imp != "" {
		switch imp {
		case "low", "medium", "high", "critical":
		default:
			return fmt.Errorf("unknown importance level: %s", cfg.Importance)
		}
	}
	if dl := strings.ToLower(strings.TrimSpace(cfg.Deadline)); dl != "" {
		switch dl {
		case "immediate", "normal", "background":
		default:
			return fmt.Errorf("unknown deadline: %s", cfg.Deadline)
		}
	}
	return nil
}

// ExecutionTarget is a normalized execution descriptor derived from an agent.
type ExecutionTarget struct {
	AgentName string     `json:"agent_name"`
	Provider  Provider   `json:"provider"`
	Runner    RunnerKind `json:"runner"`
	Model     string     `json:"model,omitempty"`
	Backend   string     `json:"backend,omitempty"`
	BaseURL   string     `json:"-"`
	Local     bool       `json:"local"`
	Supported bool       `json:"supported"`
}

func (cfg RouteConfig) Validate() error {
	for _, capability := range cfg.Capabilities {
		normalized := strings.ToLower(strings.TrimSpace(capability))
		if normalized == "" {
			continue
		}
		if _, ok := validRouteCapabilities[normalized]; !ok {
			return fmt.Errorf("unknown route capability: %s", capability)
		}
	}
	if err := validateRouteTier("latency tier", cfg.LatencyTier, validRouteTiers); err != nil {
		return err
	}
	if err := validateRouteTier("cost tier", cfg.CostTier, validRouteTiers); err != nil {
		return err
	}
	if err := validateRouteTier("privacy tier", cfg.PrivacyTier, validRoutePrivacyTiers); err != nil {
		return err
	}
	return nil
}

func (cfg RouterConfig) EffectiveMode() string {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		return "heuristic"
	}
	return mode
}

// ValidImportanceLevels are the accepted values for RouterConfig.Importance.
var ValidImportanceLevels = []string{"low", "medium", "high", "critical"}

// ValidDeadlines are the accepted values for RouterConfig.Deadline.
var ValidDeadlines = []string{"immediate", "normal", "background"}

func (cfg RouterConfig) WithDefaults() RouterConfig {
	out := cfg
	// Suppress the default local preference when importance/deadline already provide explicit routing intent.
	imp := strings.ToLower(strings.TrimSpace(out.Importance))
	dl := strings.ToLower(strings.TrimSpace(out.Deadline))
	hasImportanceIntent := imp != "" && imp != "medium"
	hasDeadlineIntent := dl != "" && dl != "normal"
	if !out.PreferLocal && !out.PreferFast && !out.PreferLowCost && !out.LocalOnly && !hasImportanceIntent && !hasDeadlineIntent {
		out.PreferLocal = true
	}
	if out.LLMRouterTimeoutSecs == 0 {
		out.LLMRouterTimeoutSecs = 30
	}
	if out.LLMRouterContextSize == 0 {
		out.LLMRouterContextSize = 512
	}
	return out
}

func validateRouteTier(label, raw string, allowed map[string]struct{}) error {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return nil
	}
	if _, ok := allowed[normalized]; !ok {
		return fmt.Errorf("unknown %s: %s", label, raw)
	}
	return nil
}

// EffectiveRouteConfig merges explicit per-agent routing metadata with inferred defaults.
func (a Agent) EffectiveRouteConfig() RouteConfig {
	cfg := a.Route
	if len(cfg.Capabilities) == 0 {
		cfg.Capabilities = inferredRouteCapabilities(a)
	} else {
		cfg.Capabilities = normalizeRouteCapabilities(cfg.Capabilities)
	}
	if strings.TrimSpace(cfg.LatencyTier) == "" {
		cfg.LatencyTier = inferredLatencyTier(a)
	} else {
		cfg.LatencyTier = strings.ToLower(strings.TrimSpace(cfg.LatencyTier))
	}
	if strings.TrimSpace(cfg.CostTier) == "" {
		cfg.CostTier = inferredCostTier(a)
	} else {
		cfg.CostTier = strings.ToLower(strings.TrimSpace(cfg.CostTier))
	}
	if strings.TrimSpace(cfg.PrivacyTier) == "" {
		cfg.PrivacyTier = inferredPrivacyTier(a)
	} else {
		cfg.PrivacyTier = strings.ToLower(strings.TrimSpace(cfg.PrivacyTier))
	}
	return cfg
}

// ResolveExecutionTarget normalizes how an agent will actually be executed.
func ResolveExecutionTarget(a Agent) ExecutionTarget {
	target := ExecutionTarget{
		AgentName: a.Name,
		Provider:  a.Provider,
		Model:     strings.TrimSpace(a.Model),
		Backend:   strings.TrimSpace(a.Backend),
		BaseURL:   strings.TrimSpace(a.BaseURL),
		Runner:    RunnerUnknown,
		Supported: false,
	}

	switch a.Provider {
	case ProviderOllama:
		target.Runner = RunnerOllamaHTTP
		target.Local = isLocalEndpoint(target.BaseURL)
		target.Supported = true
	case ProviderCodex:
		target.Runner = RunnerCodexCLI
		target.Local = false
		target.Supported = true
	case ProviderGemini:
		target.Runner = RunnerGeminiCLI
		target.Local = false
		target.Supported = true
	case ProviderOpenAI:
		target.Runner = RunnerOpenAIHTTP
		target.Local = isExplicitLocalEndpoint(target.BaseURL)
		target.Supported = true
	case ProviderClaude:
		switch NormalizeClaudeBackend(a.Backend) {
		case ClaudeBackendOllama:
			target.Runner = RunnerOllamaHTTP
			target.Local = isLocalEndpoint(target.BaseURL)
			target.Supported = true
		case ClaudeBackendBedrock:
			target.Runner = RunnerBedrockAPI
			target.Local = false
			target.Supported = false
		default:
			target.Runner = RunnerClaudeCLI
			target.Local = false
			target.Supported = true
		}
	default:
		target.Local = false
		target.Supported = false
	}

	return target
}

// IsGitWorktree reports whether dir is inside a Git worktree by walking parent
// directories and checking for a .git directory or file.
func IsGitWorktree(dir string) bool {
	current := strings.TrimSpace(dir)
	if current == "" {
		return false
	}

	abs, err := filepath.Abs(current)
	if err != nil {
		return false
	}

	for {
		gitPath := filepath.Join(abs, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}

// BuildCodexExecArgs returns the standard non-interactive Codex invocation used
// by AgentVault. Prompt gateway flows are intended to be agentic, so Codex is
// launched in low-friction workspace-write mode.
func BuildCodexExecArgs(model, outputPath, cwd, prompt string) []string {
	args := []string{"exec", "--json", "--output-last-message", outputPath, "--full-auto"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	if strings.TrimSpace(cwd) != "" && !IsGitWorktree(cwd) {
		args = append(args, "--skip-git-repo-check")
	}
	args = append(args, prompt)
	return args
}

// BuildCodexStreamArgs returns the Codex invocation for live streaming agentic execution.
// Omits --json and --output-last-message so output flows as human-readable text to the terminal
// instead of JSON event lines.
func BuildCodexStreamArgs(model, cwd, prompt string) []string {
	args := []string{"exec", "--full-auto"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	if strings.TrimSpace(cwd) != "" && !IsGitWorktree(cwd) {
		args = append(args, "--skip-git-repo-check")
	}
	args = append(args, prompt)
	return args
}

// BuildClaudeExecArgs returns the standard non-interactive Claude invocation
// used by AgentVault. It prefers the built-in auto permission mode so Claude
// can act on the workspace instead of only describing changes.
func BuildClaudeExecArgs(model, prompt string) []string {
	args := []string{"-p", "--output-format", "json", "--permission-mode", "auto"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	args = append(args, prompt)
	return args
}

// BuildGeminiExecArgs returns the standard non-interactive Gemini invocation
// used by AgentVault. auto_edit enables editing tools without dropping into a
// read-only planning/chat-only flow.
func BuildGeminiExecArgs(model, prompt string) []string {
	args := []string{"--prompt", prompt, "--output-format", "json", "--approval-mode", "auto_edit"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	return args
}

// BuildClaudeStreamArgs returns the Claude invocation for live streaming agentic execution.
// Omits --output-format json so output flows progressively to the terminal.
func BuildClaudeStreamArgs(model, prompt string) []string {
	args := []string{"-p", "--permission-mode", "auto"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	args = append(args, prompt)
	return args
}

// BuildGeminiStreamArgs returns the Gemini invocation for live streaming agentic execution.
// Omits --output-format json so output flows progressively to the terminal.
func BuildGeminiStreamArgs(model, prompt string) []string {
	args := []string{"--prompt", prompt, "--approval-mode", "auto_edit"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	return args
}

func normalizeRouteCapabilities(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := validRouteCapabilities[normalized]; !ok {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func inferredRouteCapabilities(a Agent) []string {
	capabilities := []string{RouteCapabilityGeneral}
	target := ResolveExecutionTarget(a)
	switch a.Provider {
	case ProviderCodex, ProviderClaude, ProviderOpenAI, ProviderGemini:
		capabilities = append(capabilities, RouteCapabilityCoding, RouteCapabilityReview, RouteCapabilityAnalysis)
	case ProviderOllama:
		capabilities = append(capabilities, RouteCapabilityCoding, RouteCapabilityAnalysis)
	}
	if target.Runner == RunnerClaudeCLI {
		capabilities = append(capabilities, RouteCapabilityCoding, RouteCapabilityReview)
	}
	return normalizeRouteCapabilities(capabilities)
}

func inferredLatencyTier(a Agent) string {
	target := ResolveExecutionTarget(a)
	if target.Runner == RunnerOllamaHTTP && target.Local {
		return "low"
	}
	switch a.Provider {
	case ProviderCodex, ProviderClaude:
		return "medium"
	default:
		return "medium"
	}
}

func inferredCostTier(a Agent) string {
	target := ResolveExecutionTarget(a)
	if target.Runner == RunnerOllamaHTTP && target.Local {
		return "low"
	}
	switch a.Provider {
	case ProviderOpenAI, ProviderClaude:
		return "high"
	case ProviderCodex:
		return "medium"
	default:
		return "medium"
	}
}

func inferredPrivacyTier(a Agent) string {
	target := ResolveExecutionTarget(a)
	if target.Local {
		return "local"
	}
	switch target.Runner {
	case RunnerBedrockAPI:
		return "restricted"
	default:
		return "remote"
	}
}

func isLocalEndpoint(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	return isExplicitLocalEndpoint(trimmed)
}

func isExplicitLocalEndpoint(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
