// Package agent defines the core domain types for AgentVault.
//
// This package contains all data models shared across the application:
//   - Agent: individual AI agent configurations (Claude, Codex, etc.)
//   - UnifiedRule: cross-agent behavior rules (applied to ALL agents)
//   - Role: persona definitions that shape agent behavior
//   - Session: multi-agent workspace configurations
//   - SharedConfig: global settings inherited by all agents
//
// The key design principle is that rules and roles are defined once and
// applied uniformly across all agents, ensuring consistent behavior
// regardless of the underlying AI provider.
package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Provider represents a supported AI provider.
// Each provider maps to a CLI tool that can be auto-detected and launched.
type Provider string

const (
	ProviderClaude   Provider = "claude"
	ProviderGemini   Provider = "gemini"
	ProviderCodex    Provider = "codex"
	ProviderOllama   Provider = "ollama"
	ProviderOpenAI   Provider = "openai"
	ProviderMeldbot  Provider = "meldbot"
	ProviderOpenclaw Provider = "openclaw"
	ProviderNanoclaw Provider = "nanoclaw"
	ProviderAider    Provider = "aider"
	ProviderCustom   Provider = "custom"
	ProviderCopilot  Provider = "copilot"
	ProviderBedrock  Provider = "bedrock"
)

// Instruction scope constants for InstructionFile.Scope.
const (
	InstructionScopeGlobal    = "global"
	InstructionScopeDirectory = "directory"
	InstructionScopeLocal     = "local"
)

const (
	ClaudeBackendAnthropic = "anthropic"
	ClaudeBackendOllama    = "ollama"
	ClaudeBackendBedrock   = "bedrock"
)

// MCPServer represents a Model Context Protocol server configuration.
type MCPServer struct {
	Name    string            `json:"name"           yaml:"name"`
	Command string            `json:"command"        yaml:"command"`
	Args    []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"  yaml:"env,omitempty"`
}

// AgentProviderMeta captures provider-specific configuration for full round-trip portability.
// Only fields relevant to the agent's Provider are populated; all fields are optional.
type AgentProviderMeta struct {
	AuthMode        string            `json:"auth_mode,omitempty"        yaml:"auth_mode,omitempty"`
	BedrockRegion   string            `json:"bedrock_region,omitempty"   yaml:"bedrock_region,omitempty"`
	BedrockRoleARN  string            `json:"bedrock_role_arn,omitempty" yaml:"bedrock_role_arn,omitempty"`
	CopilotOrg      string            `json:"copilot_org,omitempty"      yaml:"copilot_org,omitempty"`
	GeminiProject   string            `json:"gemini_project,omitempty"   yaml:"gemini_project,omitempty"`
	GeminiLocation  string            `json:"gemini_location,omitempty"  yaml:"gemini_location,omitempty"`
	AWSProfile      string            `json:"aws_profile,omitempty"      yaml:"aws_profile,omitempty"`
	AWSRegion       string            `json:"aws_region,omitempty"       yaml:"aws_region,omitempty"`
	OllamaKeepAlive string            `json:"ollama_keep_alive,omitempty" yaml:"ollama_keep_alive,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"            yaml:"extra,omitempty"`
}

// Agent represents a configured AI agent.
type Agent struct {
	Name          string             `json:"name"                      yaml:"name"`
	Provider      Provider           `json:"provider"                  yaml:"provider"`
	Model         string             `json:"model"                     yaml:"model"`
	Backend       string             `json:"backend,omitempty"         yaml:"backend,omitempty"`
	APIKey        string             `json:"api_key,omitempty"         yaml:"api_key,omitempty"`
	BaseURL       string             `json:"base_url,omitempty"        yaml:"base_url,omitempty"`
	SystemPrompt  string             `json:"system_prompt,omitempty"   yaml:"system_prompt,omitempty"`
	TaskDesc      string             `json:"task_description,omitempty" yaml:"task_description,omitempty"`
	Tags          []string           `json:"tags,omitempty"            yaml:"tags,omitempty"`
	Route         RouteConfig        `json:"route,omitempty"           yaml:"route,omitempty"`
	MCPServers    []MCPServer        `json:"mcp_servers,omitempty"     yaml:"mcp_servers,omitempty"`
	Role          string             `json:"role,omitempty"            yaml:"role,omitempty"`
	DisabledRules []string           `json:"disabled_rules,omitempty"  yaml:"disabled_rules,omitempty"`
	ProviderMeta  *AgentProviderMeta `json:"provider_meta,omitempty"   yaml:"provider_meta,omitempty"`
	CreatedAt     time.Time          `json:"created_at"                yaml:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"                yaml:"updated_at"`
}

// UnifiedRule represents a rule that applies across all agents.
//
// Rules are the core mechanism for ensuring consistent behavior across
// Claude, Codex, Meldbot, Openclaw, Nanoclaw, and any other configured agent.
// They are stored in the encrypted vault and can be exported/imported to
// replicate behavior across machines.
//
// Rules are sorted by Priority (lower = higher priority, applied first)
// and can be individually enabled/disabled without removal.
// Individual agents can opt out of specific rules via Agent.DisabledRules.
type UnifiedRule struct {
	Name        string    `json:"name"`        // Unique identifier (e.g., "no-model-in-commit")
	Description string    `json:"description"` // Human-readable description
	Content     string    `json:"content"`     // The actual rule text
	Category    string    `json:"category"`    // Category: "commit", "coding", "behavior", "security"
	Priority    int       `json:"priority"`    // Lower = higher priority (applied first)
	Enabled     bool      `json:"enabled"`     // Can be disabled globally
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Role represents a persona/role that can be applied to agents.
//
// Roles combine a system prompt (persona) with a set of rule names,
// allowing agents to behave as specific team members:
//   - "Lead Engineer": architecture focus, best practices, mentoring
//   - "Security Auditor": vulnerability scanning, OWASP Top 10
//   - "Code Reviewer": quality, edge cases, error handling
//
// A role is assigned to an agent via Agent.Role and its prompt is
// prepended to the effective system prompt by BuildEffectivePrompt().
type Role struct {
	Name        string    `json:"name"`        // Unique identifier (e.g., "lead-engineer")
	Title       string    `json:"title"`       // Display title (e.g., "Lead Engineer")
	Description string    `json:"description"` // What this role does
	Prompt      string    `json:"prompt"`      // System prompt for this role
	Rules       []string  `json:"rules"`       // Additional rule names to apply
	Tags        []string  `json:"tags"`        // Tags for categorization
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// InstructionFile represents a stored instruction file (e.g. AGENTS.md, CLAUDE.md).
type InstructionFile struct {
	Name             string    `json:"name"                        yaml:"name"`     // key, e.g. "agents", "claude"
	Filename         string    `json:"filename"                    yaml:"filename"` // target filename, e.g. "AGENTS.md"
	Content          string    `json:"content"                     yaml:"content"`
	UpdatedAt        time.Time `json:"updated_at"                  yaml:"updated_at"`
	Scope            string    `json:"scope,omitempty"             yaml:"scope,omitempty"`             // "global" (default), "directory", or "local"
	DirectoryPattern string    `json:"directory_pattern,omitempty" yaml:"directory_pattern,omitempty"` // glob for "directory" scope
}

// PromptTokenUsage captures token usage metadata for one prompt execution.
type PromptTokenUsage struct {
	InputTokens           int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens          int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens           int64 `json:"total_tokens,omitempty"`
}

// PromptTranscriptEntry stores one prompt/response interaction from prompt mode.
type PromptTranscriptEntry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Prompt          string            `json:"prompt"`
	EffectivePrompt string            `json:"effective_prompt,omitempty"`
	ResponsePreview string            `json:"response_preview,omitempty"`
	TokenUsage      *PromptTokenUsage `json:"token_usage,omitempty"`
	Success         bool              `json:"success"`
	Error           string            `json:"error,omitempty"`
}

// PromptSession stores prompt-mode transcript/session metadata in vault state.
type PromptSession struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name,omitempty"`
	AgentName       string                  `json:"agent_name"`
	Provider        string                  `json:"provider,omitempty"`
	Model           string                  `json:"model,omitempty"`
	StartedAt       time.Time               `json:"started_at"`
	EndedAt         time.Time               `json:"ended_at"`
	Entries         []PromptTranscriptEntry `json:"entries,omitempty"`
	TotalTokenUsage *PromptTokenUsage       `json:"total_token_usage,omitempty"`
}

// PromptSessionRetentionLimit caps how many prompt sessions are retained in shared config.
const PromptSessionRetentionLimit = 20

// PromptSessionEntryLimit caps transcript entries stored per prompt session.
const PromptSessionEntryLimit = 200

// PromptTranscriptFieldMaxRunes caps stored transcript field size (runes).
const PromptTranscriptFieldMaxRunes = 4000

// PromptSessionIDMaxRunes caps imported/stored prompt session identifier size.
const PromptSessionIDMaxRunes = 128

// WellKnownInstructions maps common names to their conventional filenames.
// These are the instruction files that each AI agent reads from a project root.
// The sync command generates these files from unified rules, ensuring all
// agents receive the same instructions in their expected format.
var WellKnownInstructions = map[string]string{
	"agents":   "AGENTS.md",
	"claude":   "CLAUDE.md",
	"codex":    "codex.md",
	"copilot":  ".github/copilot-instructions.md",
	"meldbot":  "MELDBOT.md",
	"openclaw": "OPENCLAW.md",
	"nanoclaw": "NANOCLAW.md",
	"aider":    ".aider.conf.yml",
	"cursor":   ".cursorrules",
	"windsurf": ".windsurfrules",
}

// ProviderInstructionMap maps providers to their instruction file names.
// This allows pushing unified rules to the correct files for each provider.
var ProviderInstructionMap = map[Provider][]string{
	ProviderClaude:   {"claude", "agents"},
	ProviderCodex:    {"codex", "agents"},
	ProviderMeldbot:  {"meldbot", "agents"},
	ProviderOpenclaw: {"openclaw", "agents"},
	ProviderNanoclaw: {"nanoclaw", "agents"},
	ProviderAider:    {"aider", "agents"},
	ProviderGemini:   {"agents"},
	ProviderOllama:   {"agents"},
	ProviderOpenAI:   {"agents"},
	ProviderCustom:   {"agents"},
	ProviderCopilot:  {"copilot", "agents"},
	ProviderBedrock:  {"agents"},
}

// FilenameForInstruction returns the conventional filename for a name,
// or the name itself with .md appended if not well-known.
func FilenameForInstruction(name string) string {
	if fn, ok := WellKnownInstructions[name]; ok {
		return fn
	}
	return name + ".md"
}

// ProviderPricing holds per-token cost rates for one provider.
// Rates are in USD per 1,000 tokens.
type ProviderPricing struct {
	Provider          Provider `json:"provider"`
	ModelPattern      string   `json:"model_pattern,omitempty"` // substring match, empty = all models
	InputPer1KTokens  float64  `json:"input_per_1k_tokens"`
	OutputPer1KTokens float64  `json:"output_per_1k_tokens"`
	CachedPer1KTokens float64  `json:"cached_per_1k_tokens,omitempty"`
	MonthlyBudgetUSD  float64  `json:"monthly_budget_usd,omitempty"`
}

// DefaultPricing returns well-known public pricing for common providers.
// Values reflect publicly available pricing and should be updated as providers change rates.
func DefaultPricing() []ProviderPricing {
	return []ProviderPricing{
		{Provider: ProviderClaude, ModelPattern: "haiku", InputPer1KTokens: 0.00025, OutputPer1KTokens: 0.00125, CachedPer1KTokens: 0.000030},
		{Provider: ProviderClaude, ModelPattern: "sonnet", InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015, CachedPer1KTokens: 0.00030},
		{Provider: ProviderClaude, ModelPattern: "opus", InputPer1KTokens: 0.015, OutputPer1KTokens: 0.075, CachedPer1KTokens: 0.0015},
		{Provider: ProviderClaude, InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015},
		{Provider: ProviderOpenAI, ModelPattern: "gpt-4o-mini", InputPer1KTokens: 0.00015, OutputPer1KTokens: 0.000600},
		{Provider: ProviderOpenAI, ModelPattern: "gpt-4o", InputPer1KTokens: 0.005, OutputPer1KTokens: 0.015},
		{Provider: ProviderOpenAI, InputPer1KTokens: 0.005, OutputPer1KTokens: 0.015},
		{Provider: ProviderGemini, ModelPattern: "flash", InputPer1KTokens: 0.000075, OutputPer1KTokens: 0.0003},
		{Provider: ProviderGemini, InputPer1KTokens: 0.00125, OutputPer1KTokens: 0.005},
		{Provider: ProviderCodex, InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015},
		{Provider: ProviderBedrock, InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015},
		{Provider: ProviderOllama, InputPer1KTokens: 0, OutputPer1KTokens: 0},
	}
}

// ComputeCostUSD returns the estimated USD cost for the given token usage against a pricing slice.
// Returns 0 for providers not found in pricing (e.g. Ollama) and when usage is nil.
func ComputeCostUSD(usage *PromptTokenUsage, provider Provider, model string, pricing []ProviderPricing) float64 {
	if usage == nil || len(pricing) == 0 {
		return 0
	}
	// Find the most specific match: prefer ModelPattern hit over catch-all.
	var best *ProviderPricing
	for i := range pricing {
		p := &pricing[i]
		if p.Provider != provider {
			continue
		}
		if p.ModelPattern != "" && !containsFold(model, p.ModelPattern) {
			continue
		}
		if best == nil || (p.ModelPattern != "" && best.ModelPattern == "") {
			best = p
		}
	}
	if best == nil {
		return 0
	}
	inputCost := float64(usage.InputTokens) / 1000.0 * best.InputPer1KTokens
	cachedCost := float64(usage.CachedInputTokens) / 1000.0 * best.CachedPer1KTokens
	outputCost := float64(usage.OutputTokens) / 1000.0 * best.OutputPer1KTokens
	return inputCost + cachedCost + outputCost
}

func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	return len(s) >= len(substr) && containsRuneFold(s, substr)
}

func containsRuneFold(s, substr string) bool {
	ls, lsub := len(s), len(substr)
	for i := 0; i <= ls-lsub; i++ {
		if equalFold(s[i:i+lsub], substr) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// ModelCapabilityEntry records one endpoint/model capability tuple for the model registry.
type ModelCapabilityEntry struct {
	EndpointURL  string    `json:"endpoint_url"`
	ModelName    string    `json:"model_name"`
	ContextSize  int       `json:"context_size,omitempty"`
	Capabilities []string  `json:"capabilities"` // code, vision, embedding, reasoning, general
	Source       string    `json:"source"`       // manual | auto-discovered
	UpdatedAt    time.Time `json:"updated_at"`
}

// SharedConfig holds global settings that apply to all agents unless overridden.
type SharedConfig struct {
	SystemPrompt   string            `json:"system_prompt,omitempty"`
	MCPServers     []MCPServer       `json:"mcp_servers,omitempty"`
	Instructions   []InstructionFile `json:"instructions,omitempty"`
	Rules          []UnifiedRule     `json:"rules,omitempty"`
	Roles          []Role            `json:"roles,omitempty"`
	PromptSessions []PromptSession   `json:"prompt_sessions,omitempty"`
	Router         RouterConfig      `json:"router,omitempty"`
	Pricing        []ProviderPricing `json:"pricing,omitempty"`
}

// ValidProviders returns all known provider values.
func ValidProviders() []Provider {
	return []Provider{
		ProviderClaude, ProviderGemini, ProviderCodex,
		ProviderOllama, ProviderOpenAI, ProviderMeldbot,
		ProviderOpenclaw, ProviderNanoclaw, ProviderAider,
		ProviderCustom, ProviderCopilot, ProviderBedrock,
	}
}

// DefaultRules returns a set of commonly useful default rules.
func DefaultRules() []UnifiedRule {
	return []UnifiedRule{
		{
			Name:        "no-model-in-commit",
			Description: "Never include model names (claude, sonnet, gpt, etc.) in commit messages",
			Content:     "Never include AI model names like 'claude', 'sonnet', 'opus', 'gpt', 'codex', 'gemini' in commit messages. Write commits as if a human wrote them.",
			Category:    "commit",
			Priority:    10,
			Enabled:     true,
		},
		{
			Name:        "no-ai-attribution",
			Description: "Do not mention being an AI in code comments or documentation",
			Content:     "Do not add comments like 'Generated by AI' or 'Created by Claude'. Write code as a professional developer would.",
			Category:    "coding",
			Priority:    20,
			Enabled:     true,
		},
		{
			Name:        "consistent-style",
			Description: "Follow existing code style in the project",
			Content:     "Always match the existing code style, naming conventions, and patterns in the project. Do not introduce new styles without explicit request.",
			Category:    "coding",
			Priority:    30,
			Enabled:     true,
		},
		{
			Name:        "minimal-changes",
			Description: "Make minimal, focused changes",
			Content:     "Make the smallest possible changes to accomplish the task. Do not refactor unrelated code, add unnecessary features, or 'improve' code that wasn't requested to be changed.",
			Category:    "behavior",
			Priority:    40,
			Enabled:     true,
		},
		{
			Name:        "no-secrets-in-code",
			Description: "Never hardcode secrets or credentials",
			Content:     "Never hardcode API keys, passwords, tokens, or other secrets in code. Use environment variables or secure configuration management.",
			Category:    "security",
			Priority:    5,
			Enabled:     true,
		},
	}
}

// DefaultRoles returns commonly useful default roles.
func DefaultRoles() []Role {
	return []Role{
		{
			Name:        "lead-engineer",
			Title:       "Lead Engineer",
			Description: "Senior technical leader focused on architecture and best practices",
			Prompt:      "You are a Lead Engineer with 15+ years of experience. Focus on clean architecture, maintainability, security, and mentoring. Review code thoroughly, suggest improvements, and ensure best practices are followed. Consider long-term implications of technical decisions.",
			Rules:       []string{"consistent-style", "minimal-changes", "no-secrets-in-code"},
			Tags:        []string{"technical", "leadership"},
		},
		{
			Name:        "designer",
			Title:       "UI/UX Designer",
			Description: "Focus on user experience and interface design",
			Prompt:      "You are a UI/UX Designer. Focus on user experience, accessibility, visual consistency, and intuitive interactions. Consider responsive design, color contrast, and user flows. Suggest improvements that enhance usability.",
			Rules:       []string{"consistent-style"},
			Tags:        []string{"design", "frontend"},
		},
		{
			Name:        "security-auditor",
			Title:       "Security Auditor",
			Description: "Focus on security vulnerabilities and best practices",
			Prompt:      "You are a Security Auditor. Analyze code for vulnerabilities including injection attacks, authentication issues, data exposure, and OWASP Top 10. Suggest secure alternatives and flag potential risks. Never compromise on security for convenience.",
			Rules:       []string{"no-secrets-in-code"},
			Tags:        []string{"security", "audit"},
		},
		{
			Name:        "code-reviewer",
			Title:       "Code Reviewer",
			Description: "Thorough code review focused on quality",
			Prompt:      "You are a Code Reviewer. Examine code for bugs, logic errors, performance issues, and maintainability. Provide constructive feedback with specific suggestions. Check for edge cases and error handling.",
			Rules:       []string{"consistent-style", "minimal-changes"},
			Tags:        []string{"review", "quality"},
		},
	}
}

// GetRole returns a role by name from the shared config.
func GetRole(roles []Role, name string) (Role, bool) {
	for _, r := range roles {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

// GetRule returns a rule by name from the shared config.
func GetRule(rules []UnifiedRule, name string) (UnifiedRule, bool) {
	for _, r := range rules {
		if r.Name == name {
			return r, true
		}
	}
	return UnifiedRule{}, false
}

// BuildEffectivePrompt builds the complete system prompt for an agent,
// combining role, rules, and agent-specific settings.
//
// The prompt is assembled in priority order:
//  1. Role prompt (persona definition, e.g., "You are a Lead Engineer...")
//  2. Shared system prompt (global instructions for all agents)
//  3. Agent-specific system prompt (overrides/additions)
//  4. Enabled unified rules (filtered by agent's DisabledRules)
//
// This layered approach ensures consistent baseline behavior while
// allowing per-agent customization where needed.
func (a *Agent) BuildEffectivePrompt(shared SharedConfig) string {
	var parts []string
	roleRulePriority := make(map[string]int)

	// 1. Add role prompt if specified
	if a.Role != "" {
		if role, ok := GetRole(shared.Roles, a.Role); ok {
			parts = append(parts, role.Prompt)
			for idx, name := range role.Rules {
				if _, seen := roleRulePriority[name]; !seen {
					roleRulePriority[name] = idx
				}
			}
		}
	}

	// 2. Add shared system prompt
	if shared.SystemPrompt != "" {
		parts = append(parts, shared.SystemPrompt)
	}

	// 3. Add agent-specific prompt
	if a.SystemPrompt != "" {
		parts = append(parts, a.SystemPrompt)
	}

	// 4. Add enabled rules (not disabled for this agent)
	disabledSet := make(map[string]bool)
	for _, r := range a.DisabledRules {
		disabledSet[r] = true
	}

	var ruleTexts []string
	rules := make([]UnifiedRule, len(shared.Rules))
	copy(rules, shared.Rules)
	sort.SliceStable(rules, func(i, j int) bool {
		iRole, iInRole := roleRulePriority[rules[i].Name]
		jRole, jInRole := roleRulePriority[rules[j].Name]
		// Role-specific rules are surfaced first in the order defined by the role.
		if iInRole && jInRole {
			return iRole < jRole
		}
		if iInRole != jInRole {
			return iInRole
		}
		return rules[i].Priority < rules[j].Priority
	})
	for _, rule := range rules {
		if rule.Enabled && !disabledSet[rule.Name] {
			ruleTexts = append(ruleTexts, "- "+rule.Content)
		}
	}

	if len(ruleTexts) > 0 {
		parts = append(parts, "\n## Rules\n"+joinStrings(ruleTexts, "\n"))
	}

	return joinStrings(parts, "\n\n")
}

func joinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

// Validate checks that required fields are populated.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return errors.New("agent name is required")
	}
	if a.Provider == "" {
		return errors.New("agent provider is required")
	}
	valid := false
	for _, p := range ValidProviders() {
		if a.Provider == p {
			valid = true
			break
		}
	}
	if !valid {
		return errors.New("unknown provider: " + string(a.Provider))
	}
	backendRaw := strings.TrimSpace(a.Backend)
	if a.Provider == ProviderClaude {
		backend := strings.ToLower(backendRaw)
		if backend != "" && backend != ClaudeBackendAnthropic && backend != ClaudeBackendOllama && backend != ClaudeBackendBedrock {
			return errors.New("unknown claude backend: " + backendRaw)
		}
		a.Backend = backend // normalize in-place so ValidateProviderMeta sees canonical form
	} else if backendRaw != "" {
		return errors.New("backend is only supported for claude agents")
	}
	if err := a.Route.Validate(); err != nil {
		return err
	}
	if err := ValidateProviderMeta(a.Provider, a.Backend, a.ProviderMeta); err != nil {
		return err
	}
	return nil
}

// NormalizeClaudeBackend normalizes a raw Claude backend string to a canonical value.
// It trims whitespace, lowercases the input, and defaults to ClaudeBackendAnthropic
// for empty or unrecognized values.
func NormalizeClaudeBackend(raw string) string {
	backend := strings.TrimSpace(strings.ToLower(raw))
	if backend == "" {
		return ClaudeBackendAnthropic
	}
	switch backend {
	case ClaudeBackendAnthropic, ClaudeBackendOllama, ClaudeBackendBedrock:
		return backend
	default:
		return ClaudeBackendAnthropic
	}
}

// ParseClaudeBackend normalizes a raw Claude backend string and returns an
// error when the value is non-empty and unrecognized.
func ParseClaudeBackend(raw string) (string, error) {
	backend := strings.TrimSpace(strings.ToLower(raw))
	if backend == "" {
		return ClaudeBackendAnthropic, nil
	}
	switch backend {
	case ClaudeBackendAnthropic, ClaudeBackendOllama, ClaudeBackendBedrock:
		return backend, nil
	default:
		return "", fmt.Errorf("unknown claude backend: %s", raw)
	}
}

// EffectiveSystemPrompt returns the agent's system prompt, falling back to the
// shared config prompt if the agent has none.
func (a *Agent) EffectiveSystemPrompt(shared SharedConfig) string {
	if a.SystemPrompt != "" {
		return a.SystemPrompt
	}
	return shared.SystemPrompt
}

// EffectiveMCPServers returns the agent's MCP servers merged with shared ones.
// Agent-specific servers with the same name override shared ones.
func (a *Agent) EffectiveMCPServers(shared SharedConfig) []MCPServer {
	seen := make(map[string]struct{})
	var result []MCPServer
	for _, s := range a.MCPServers {
		seen[s.Name] = struct{}{}
		result = append(result, s)
	}
	for _, s := range shared.MCPServers {
		if _, ok := seen[s.Name]; !ok {
			result = append(result, s)
		}
	}
	return result
}
