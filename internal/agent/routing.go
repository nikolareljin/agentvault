package agent

import (
	"fmt"
	"net/url"
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
	Capabilities []string `json:"capabilities,omitempty"`
	LatencyTier  string   `json:"latency_tier,omitempty"`
	CostTier     string   `json:"cost_tier,omitempty"`
	PrivacyTier  string   `json:"privacy_tier,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}

// RouterConfig stores shared prompt-router settings.
type RouterConfig struct {
	Mode            string `json:"mode,omitempty"`
	LangGraphCmd    string `json:"langgraph_command,omitempty"`
	PreferLocal     bool   `json:"prefer_local,omitempty"`
	PreferFast      bool   `json:"prefer_fast,omitempty"`
	PreferLowCost   bool   `json:"prefer_low_cost,omitempty"`
	LocalOnly       bool   `json:"local_only,omitempty"`
	AllowFallbacks  bool   `json:"allow_fallbacks,omitempty"`
	RequireApproval bool   `json:"require_approval,omitempty"`
}

// ExecutionTarget is a normalized execution descriptor derived from an agent.
type ExecutionTarget struct {
	AgentName string     `json:"agent_name"`
	Provider  Provider   `json:"provider"`
	Runner    RunnerKind `json:"runner"`
	Model     string     `json:"model,omitempty"`
	Backend   string     `json:"backend,omitempty"`
	BaseURL   string     `json:"base_url,omitempty"`
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

func (cfg RouterConfig) WithDefaults() RouterConfig {
	out := cfg
	if !out.PreferLocal && !out.PreferFast && !out.PreferLowCost && !out.LocalOnly {
		out.PreferLocal = true
	}
	if !out.AllowFallbacks {
		out.AllowFallbacks = true
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
	case ProviderCodex, ProviderClaude, ProviderOpenAI:
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
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
