package status

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCollectCodexStatus(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".codex", "sessions", "2026", "02", "09")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	logPath := filepath.Join(sessionDir, "rollout.jsonl")
	content := "{\"timestamp\":\"2026-02-09T10:00:00Z\",\"payload\":{\"type\":\"token_count\",\"info\":{\"total_token_usage\":{\"input_tokens\":100,\"cached_input_tokens\":20,\"output_tokens\":10,\"reasoning_output_tokens\":5,\"total_tokens\":110},\"model_context_window\":272000,\"rate_limits\":{\"primary\":{\"used_percent\":12.5,\"window_minutes\":300,\"resets_at\":1767435364},\"secondary\":{\"used_percent\":40,\"window_minutes\":10080,\"resets_at\":1767893520},\"credits\":{\"has_credits\":true,\"unlimited\":false,\"balance\":42.5},\"plan_type\":\"pro\"}}}}\n"
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	st := collectCodexStatus(home)
	if !st.Available {
		t.Fatalf("collectCodexStatus available = false, error = %s", st.Error)
	}
	if st.Tokens == nil || st.Tokens.TotalTokens != 110 {
		t.Fatalf("tokens = %#v, want total_tokens=110", st.Tokens)
	}
	if st.Quota == nil || st.Quota.Primary == nil || st.Quota.Secondary == nil {
		t.Fatalf("quota not populated: %#v", st.Quota)
	}
	if st.Quota.Primary.RemainingPercent != 87.5 {
		t.Fatalf("primary remaining = %.1f, want 87.5", st.Quota.Primary.RemainingPercent)
	}
	if st.Quota.PlanType != "pro" {
		t.Fatalf("plan type = %q, want pro", st.Quota.PlanType)
	}
}

func TestCollectCodexStatusNoSessions(t *testing.T) {
	home := t.TempDir()
	st := collectCodexStatus(home)
	if st.Available {
		t.Fatalf("expected unavailable status")
	}
	if st.Error == "" {
		t.Fatalf("expected error message")
	}
}

func writeHistoryLine(t *testing.T, path string, provider, model string, cost float64, tokens *agent.PromptTokenUsage, ts time.Time) {
	t.Helper()
	rec := map[string]any{
		"provider":           provider,
		"model":              model,
		"estimated_cost_usd": cost,
		"success":            true,
		"timestamp":          ts.Format(time.RFC3339),
	}
	if tokens != nil {
		rec["token_usage"] = tokens
	}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open history: %v", err)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", b)
}

func TestBuildCostReportFileNotFound(t *testing.T) {
	if got := BuildCostReport("/nonexistent/path.jsonl", nil); got != nil {
		t.Fatalf("expected nil for missing file, got %+v", got)
	}
}

func TestBuildCostReportEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := BuildCostReport(path, nil); got != nil {
		t.Fatalf("expected nil for empty file, got %+v", got)
	}
}

func TestBuildCostReportAggregation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	pricing := []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015},
	}

	writeHistoryLine(t, path, "claude", "claude-3-5-sonnet", 0.01, nil, now)
	writeHistoryLine(t, path, "claude", "claude-3-5-sonnet", 0.02, nil, now)
	writeHistoryLine(t, path, "openai", "gpt-4o", 0.05, nil, now)

	report := BuildCostReport(path, pricing)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.RecordCount != 3 {
		t.Errorf("RecordCount = %d, want 3", report.RecordCount)
	}
	want := 0.01 + 0.02 + 0.05
	if !approxEq(report.TotalUSD, want) {
		t.Errorf("TotalUSD = %v, want %v", report.TotalUSD, want)
	}
	if !approxEq(report.ByProvider["claude"], 0.03) {
		t.Errorf("ByProvider[claude] = %v, want 0.03", report.ByProvider["claude"])
	}
	if !approxEq(report.ByProvider["openai"], 0.05) {
		t.Errorf("ByProvider[openai] = %v, want 0.05", report.ByProvider["openai"])
	}
}

func TestBuildCostReportLegacyRecompute(t *testing.T) {
	// Records with estimated_cost_usd=0 but token_usage set should be recomputed.
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	pricing := []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, InputPer1KTokens: 1.0, OutputPer1KTokens: 2.0},
	}

	tokens := &agent.PromptTokenUsage{InputTokens: 1000, OutputTokens: 500}
	writeHistoryLine(t, path, "claude", "claude-3-5-sonnet", 0, tokens, now)

	report := BuildCostReport(path, pricing)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	// 1000/1000*1.0 + 500/1000*2.0 = 1.0 + 1.0 = 2.0
	if !approxEq(report.TotalUSD, 2.0) {
		t.Errorf("TotalUSD = %v, want 2.0 (recomputed from token_usage)", report.TotalUSD)
	}
}

func TestBuildCostReportIncludesZeroCostProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	writeHistoryLine(t, path, "ollama", "llama3.2", 0, nil, now)

	report := BuildCostReport(path, nil)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.RecordCount != 1 {
		t.Fatalf("RecordCount = %d, want 1", report.RecordCount)
	}
	cost, ok := report.ByProvider["ollama"]
	if !ok {
		t.Fatalf("ByProvider missing zero-cost ollama entry: %#v", report.ByProvider)
	}
	if cost != 0 {
		t.Fatalf("ByProvider[ollama] = %v, want 0", cost)
	}
	if report.TotalUSD != 0 {
		t.Fatalf("TotalUSD = %v, want 0", report.TotalUSD)
	}
}

func TestBuildCostReportSkipsBlankProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	writeHistoryLine(t, path, "  ", "unknown", 0.05, nil, now)

	report := BuildCostReport(path, nil)
	if report != nil {
		t.Fatalf("expected nil report when all successful records have blank providers, got %+v", report)
	}
}

func TestBuildCostReportSkipsFailedRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	// success=false record should be ignored
	rec := map[string]any{
		"provider":           "claude",
		"model":              "sonnet",
		"estimated_cost_usd": 99.0,
		"success":            false,
		"timestamp":          now.Format(time.RFC3339),
	}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	report := BuildCostReport(path, nil)
	if report != nil {
		t.Fatalf("expected nil report when all records are failures, got %+v", report)
	}
}

func TestBuildCostReportBudgetAlerts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()

	pricing := []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, InputPer1KTokens: 0.001, OutputPer1KTokens: 0.002, MonthlyBudgetUSD: 1.0},
	}

	// Spend 0.90 USD this month — should trigger >=80% alert.
	writeHistoryLine(t, path, "claude", "sonnet", 0.90, nil, now)

	report := BuildCostReport(path, pricing)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.BudgetAlerts) != 1 {
		t.Fatalf("BudgetAlerts len = %d, want 1", len(report.BudgetAlerts))
	}
	if report.BudgetAlerts[0].Provider != "claude" {
		t.Errorf("BudgetAlerts[0].Provider = %q, want claude", report.BudgetAlerts[0].Provider)
	}
}

func TestBuildCostReportBudgetAlertIgnoresPastMonths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	now := time.Now().UTC()
	lastMonth := now.AddDate(0, -1, 0)

	pricing := []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, InputPer1KTokens: 0.001, OutputPer1KTokens: 0.002, MonthlyBudgetUSD: 1.0},
	}

	// All spend in last month — should NOT trigger this-month budget alert.
	writeHistoryLine(t, path, "claude", "sonnet", 0.95, nil, lastMonth)

	report := BuildCostReport(path, pricing)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.BudgetAlerts) != 0 {
		t.Fatalf("BudgetAlerts should be empty for past-month spend, got %v", report.BudgetAlerts)
	}
}

func TestBuildWindowQuotaBounds(t *testing.T) {
	q := buildWindowQuota(120, 60, 100)
	if q.UsedPercent != 100 {
		t.Fatalf("used = %.1f, want 100", q.UsedPercent)
	}
	if q.RemainingPercent != 0 {
		t.Fatalf("remaining = %.1f, want 0", q.RemainingPercent)
	}
	quotaWithNegativeUsage := buildWindowQuota(-10, 60, 100)
	if quotaWithNegativeUsage.UsedPercent != 0 {
		t.Fatalf("used = %.1f, want 0", quotaWithNegativeUsage.UsedPercent)
	}
	if quotaWithNegativeUsage.RemainingPercent != 100 {
		t.Fatalf("remaining = %.1f, want 100", quotaWithNegativeUsage.RemainingPercent)
	}
	if !quotaWithNegativeUsage.ResetsAtTime.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("unexpected reset time: %v", quotaWithNegativeUsage.ResetsAtTime)
	}
}
