package status

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// Report is a machine-readable system status payload.
type Report struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Vault       *VaultSummary             `json:"vault,omitempty"`
	Providers   map[string]ProviderStatus `json:"providers"`
	Agents      []AgentStatus             `json:"agents,omitempty"`
	Cost        *CostReport               `json:"cost,omitempty"`
}

// CostReport aggregates estimated spend from the prompt history file.
type CostReport struct {
	TotalUSD     float64            `json:"total_usd"`
	ByProvider   map[string]float64 `json:"by_provider"`
	RecordCount  int                `json:"record_count"`
	BudgetAlerts []BudgetAlert      `json:"budget_alerts,omitempty"`
}

// BudgetAlert is emitted when a provider's estimated spend exceeds its monthly budget.
type BudgetAlert struct {
	Provider  string  `json:"provider"`
	SpentUSD  float64 `json:"spent_usd"`
	BudgetUSD float64 `json:"budget_usd"`
}

// VaultSummary captures high-level vault metadata counts.
type VaultSummary struct {
	Path          string `json:"path"`
	Agents        int    `json:"agents"`
	Instructions  int    `json:"instructions"`
	Rules         int    `json:"rules"`
	Roles         int    `json:"roles"`
	SharedMCP     int    `json:"shared_mcp_servers"`
	Sessions      int    `json:"sessions"`
	ProviderConfs int    `json:"provider_configs"`
}

// AgentStatus maps an agent to the provider status key used in Providers.
type AgentStatus struct {
	Name     string         `json:"name"`
	Provider agent.Provider `json:"provider"`
	Model    string         `json:"model,omitempty"`
	Status   string         `json:"status"`
}

// ProviderStatus describes token/quota usage availability per provider.
type ProviderStatus struct {
	Provider  string      `json:"provider"`
	Available bool        `json:"available"`
	Source    string      `json:"source,omitempty"`
	UpdatedAt *time.Time  `json:"updated_at,omitempty"`
	Error     string      `json:"error,omitempty"`
	Tokens    *TokenUsage `json:"tokens,omitempty"`
	Quota     *QuotaUsage `json:"quota,omitempty"`
}

// TokenUsage contains aggregate token counters from the provider status source.
type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens          int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens,omitempty"`
	TotalTokens           int64 `json:"total_tokens,omitempty"`
	ContextWindow         int64 `json:"context_window,omitempty"`
}

// QuotaUsage contains provider rate-limit windows and credit state.
type QuotaUsage struct {
	Primary   *WindowQuota `json:"primary,omitempty"`
	Secondary *WindowQuota `json:"secondary,omitempty"`
	Credits   *CreditQuota `json:"credits,omitempty"`
	PlanType  string       `json:"plan_type,omitempty"`
}

// WindowQuota represents one time-window quota with usage and reset time.
type WindowQuota struct {
	UsedPercent      float64   `json:"used_percent"`
	RemainingPercent float64   `json:"remaining_percent"`
	WindowMinutes    int       `json:"window_minutes"`
	ResetsAt         int64     `json:"resets_at"`
	ResetsAtTime     time.Time `json:"resets_at_time"`
}

// CreditQuota represents credit availability when reported by the provider.
type CreditQuota struct {
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    float64 `json:"balance,omitempty"`
}

// BuildReport builds a complete status report. If v is nil, vault/agent data is omitted.
func BuildReport(v *vault.Vault, homeDir string) Report {
	report := Report{
		GeneratedAt: time.Now().UTC(),
		Providers:   make(map[string]ProviderStatus),
	}

	providers := providersFromVault(v)
	if len(providers) == 0 {
		providers = []agent.Provider{agent.ProviderCodex, agent.ProviderClaude, agent.ProviderOllama, agent.ProviderAider}
	}

	for _, p := range providers {
		report.Providers[string(p)] = collectProviderStatus(p, homeDir)
	}

	if v == nil {
		return report
	}

	shared := v.SharedConfig()
	sessions := v.Sessions()
	providerConfigs := v.ProviderConfigs()
	providerCfgCount := 0
	if providerConfigs.Claude != nil {
		providerCfgCount++
	}
	if providerConfigs.Codex != nil {
		providerCfgCount++
	}
	if providerConfigs.Ollama != nil {
		providerCfgCount++
	}

	agents := append([]agent.Agent(nil), v.List()...)
	report.Vault = &VaultSummary{
		Path:          v.Path(),
		Agents:        len(agents),
		Instructions:  len(v.ListInstructions()),
		Rules:         len(shared.Rules),
		Roles:         len(shared.Roles),
		SharedMCP:     len(shared.MCPServers),
		Sessions:      len(sessions.Sessions),
		ProviderConfs: providerCfgCount,
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})
	for _, a := range agents {
		report.Agents = append(report.Agents, AgentStatus{
			Name:     a.Name,
			Provider: a.Provider,
			Model:    a.Model,
			Status:   statusForAgent(a, report.Providers),
		})
	}

	return report
}

// CostReportForVault builds a cost report from the vault's prompt-history file.
// Returns nil when the vault is nil or no history exists.
func CostReportForVault(v *vault.Vault) *CostReport {
	if v == nil {
		return nil
	}
	historyPath := filepath.Join(filepath.Dir(v.Path()), "prompt-history.jsonl")
	shared := v.SharedConfig()
	return BuildCostReport(historyPath, shared.Pricing)
}

func providersFromVault(v *vault.Vault) []agent.Provider {
	if v == nil {
		return nil
	}
	seen := make(map[agent.Provider]bool)
	for _, a := range v.List() {
		seen[a.Provider] = true
	}
	providers := make([]agent.Provider, 0, len(seen))
	for p := range seen {
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i] < providers[j]
	})
	return providers
}

func statusForAgent(a agent.Agent, providers map[string]ProviderStatus) string {
	ps, ok := providers[string(a.Provider)]
	if !ok {
		return "unknown"
	}
	if ps.Available {
		return "ok"
	}
	return "unavailable"
}

func collectProviderStatus(p agent.Provider, homeDir string) ProviderStatus {
	switch p {
	case agent.ProviderCodex:
		return collectCodexStatus(homeDir)
	default:
		return ProviderStatus{
			Provider:  string(p),
			Available: false,
			Error:     "usage integration not implemented for this provider",
		}
	}
}

type codexSessionEvent struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type string `json:"type"`
		Info struct {
			TotalTokenUsage struct {
				InputTokens           int64 `json:"input_tokens"`
				CachedInputTokens     int64 `json:"cached_input_tokens"`
				OutputTokens          int64 `json:"output_tokens"`
				ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
				TotalTokens           int64 `json:"total_tokens"`
			} `json:"total_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
			RateLimits         struct {
				Primary struct {
					UsedPercent   float64 `json:"used_percent"`
					WindowMinutes int     `json:"window_minutes"`
					ResetsAt      int64   `json:"resets_at"`
				} `json:"primary"`
				Secondary struct {
					UsedPercent   float64 `json:"used_percent"`
					WindowMinutes int     `json:"window_minutes"`
					ResetsAt      int64   `json:"resets_at"`
				} `json:"secondary"`
				Credits struct {
					HasCredits bool     `json:"has_credits"`
					Unlimited  bool     `json:"unlimited"`
					Balance    *float64 `json:"balance"`
				} `json:"credits"`
				PlanType *string `json:"plan_type"`
			} `json:"rate_limits"`
		} `json:"info"`
	} `json:"payload"`
}

func collectCodexStatus(homeDir string) ProviderStatus {
	status := ProviderStatus{Provider: string(agent.ProviderCodex)}
	if strings.TrimSpace(homeDir) == "" {
		status.Available = false
		status.Error = "home directory is not available"
		return status
	}

	latestFile, err := findNewestJSONL(filepath.Join(homeDir, ".codex", "sessions"))
	if err != nil {
		status.Available = false
		status.Error = err.Error()
		return status
	}

	f, err := os.Open(latestFile)
	if err != nil {
		status.Available = false
		status.Error = fmt.Sprintf("opening %s: %v", latestFile, err)
		return status
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var last *codexSessionEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt codexSessionEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Payload.Type != "token_count" {
			continue
		}
		copyEvt := evt
		last = &copyEvt
	}
	if err := scanner.Err(); err != nil {
		status.Available = false
		status.Error = fmt.Sprintf("reading %s: %v", latestFile, err)
		return status
	}
	if last == nil {
		status.Available = false
		status.Error = "no token_count events found in latest codex session"
		status.Source = latestFile
		return status
	}

	status.Available = true
	status.Source = latestFile
	if ts, err := time.Parse(time.RFC3339Nano, last.Timestamp); err == nil {
		ts = ts.UTC()
		status.UpdatedAt = &ts
	}

	status.Tokens = &TokenUsage{
		InputTokens:           last.Payload.Info.TotalTokenUsage.InputTokens,
		CachedInputTokens:     last.Payload.Info.TotalTokenUsage.CachedInputTokens,
		OutputTokens:          last.Payload.Info.TotalTokenUsage.OutputTokens,
		ReasoningOutputTokens: last.Payload.Info.TotalTokenUsage.ReasoningOutputTokens,
		TotalTokens:           last.Payload.Info.TotalTokenUsage.TotalTokens,
		ContextWindow:         last.Payload.Info.ModelContextWindow,
	}

	quota := &QuotaUsage{}
	if last.Payload.Info.RateLimits.Primary.WindowMinutes > 0 {
		quota.Primary = buildWindowQuota(
			last.Payload.Info.RateLimits.Primary.UsedPercent,
			last.Payload.Info.RateLimits.Primary.WindowMinutes,
			last.Payload.Info.RateLimits.Primary.ResetsAt,
		)
	}
	if last.Payload.Info.RateLimits.Secondary.WindowMinutes > 0 {
		quota.Secondary = buildWindowQuota(
			last.Payload.Info.RateLimits.Secondary.UsedPercent,
			last.Payload.Info.RateLimits.Secondary.WindowMinutes,
			last.Payload.Info.RateLimits.Secondary.ResetsAt,
		)
	}
	if last.Payload.Info.RateLimits.Credits.HasCredits || last.Payload.Info.RateLimits.Credits.Unlimited || last.Payload.Info.RateLimits.Credits.Balance != nil {
		quota.Credits = &CreditQuota{
			HasCredits: last.Payload.Info.RateLimits.Credits.HasCredits,
			Unlimited:  last.Payload.Info.RateLimits.Credits.Unlimited,
		}
		if last.Payload.Info.RateLimits.Credits.Balance != nil {
			quota.Credits.Balance = *last.Payload.Info.RateLimits.Credits.Balance
		}
	}
	if last.Payload.Info.RateLimits.PlanType != nil {
		quota.PlanType = *last.Payload.Info.RateLimits.PlanType
	}
	if quota.Primary != nil || quota.Secondary != nil || quota.Credits != nil || quota.PlanType != "" {
		status.Quota = quota
	}

	return status
}

func buildWindowQuota(used float64, windowMinutes int, resetsAt int64) *WindowQuota {
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	remaining := 100.0 - used
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	resetsAtTime := time.Unix(resetsAt, 0).UTC()
	return &WindowQuota{
		UsedPercent:      used,
		RemainingPercent: remaining,
		WindowMinutes:    windowMinutes,
		ResetsAt:         resetsAt,
		ResetsAtTime:     resetsAtTime,
	}
}

func findNewestJSONL(root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("codex sessions directory not found: %s", root)
	}

	var newest string
	var newestMod time.Time

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if newest == "" || fi.ModTime().After(newestMod) {
			newest = path
			newestMod = fi.ModTime()
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if newest == "" {
		return "", fmt.Errorf("no codex session logs found")
	}
	return newest, nil
}

// BuildCostReport reads the prompt history JSONL file and aggregates estimated cost by provider.
// Returns nil if the history file cannot be opened (e.g. not found, permission error) or is empty.
func BuildCostReport(historyPath string, pricing []agent.ProviderPricing) *CostReport {
	f, err := os.Open(historyPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	if len(pricing) == 0 {
		pricing = agent.DefaultPricing()
	}

	type historyRecord struct {
		Provider         string                  `json:"provider"`
		Model            string                  `json:"model"`
		TokenUsage       *agent.PromptTokenUsage `json:"token_usage"`
		EstimatedCostUSD *float64                `json:"estimated_cost_usd"`
		Success          bool                    `json:"success"`
		Timestamp        time.Time               `json:"timestamp"`
	}

	now := time.Now().UTC()
	thisYear, thisMonth := now.Year(), now.Month()

	byProvider := make(map[string]float64)
	byProviderThisMonth := make(map[string]float64)
	var total float64
	var count int

	// bufio.Reader.ReadString accumulates across internal refills so there is no
	// fixed line-length cap (unlike bufio.Scanner). Break on any read error,
	// preserving partial results rather than silently truncating.
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		rawLine, err := reader.ReadString('\n')
		line := strings.TrimSpace(rawLine)
		if line != "" {
			var rec historyRecord
			if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr == nil && rec.Success {
				provider := strings.TrimSpace(rec.Provider)
				if provider == "" {
					continue
				}
				if _, ok := byProvider[provider]; !ok {
					byProvider[provider] = 0
				}
				var cost float64
				if rec.EstimatedCostUSD != nil {
					cost = *rec.EstimatedCostUSD
				} else if rec.TokenUsage != nil {
					// Re-compute for records written before cost tracking landed.
					p := agent.Provider(provider)
					cost = agent.ComputeCostUSD(rec.TokenUsage, p, rec.Model, pricing)
				}
				if cost > 0 {
					byProvider[provider] += cost
					total += cost
					// Track this-month spend separately for budget alert evaluation.
					if !rec.Timestamp.IsZero() &&
						rec.Timestamp.UTC().Year() == thisYear &&
						rec.Timestamp.UTC().Month() == thisMonth {
						byProviderThisMonth[provider] += cost
					}
				}
				count++
			}
		}
		if err != nil {
			break
		}
	}

	if count == 0 {
		return nil
	}

	report := &CostReport{
		TotalUSD:    total,
		ByProvider:  byProvider,
		RecordCount: count,
	}

	// Check budget alerts using current-month spend only (budget is monthly).
	for prov, spent := range byProviderThisMonth {
		for _, p := range pricing {
			if string(p.Provider) == prov && p.MonthlyBudgetUSD > 0 && spent >= p.MonthlyBudgetUSD*0.8 {
				report.BudgetAlerts = append(report.BudgetAlerts, BudgetAlert{
					Provider:  prov,
					SpentUSD:  spent,
					BudgetUSD: p.MonthlyBudgetUSD,
				})
				break
			}
		}
	}
	sort.Slice(report.BudgetAlerts, func(i, j int) bool {
		return report.BudgetAlerts[i].Provider < report.BudgetAlerts[j].Provider
	})

	return report
}
