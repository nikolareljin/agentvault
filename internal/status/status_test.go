package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestBuildWindowQuotaBounds(t *testing.T) {
	q := buildWindowQuota(120, 60, 100)
	if q.RemainingPercent != 0 {
		t.Fatalf("remaining = %.1f, want 0", q.RemainingPercent)
	}
	q2 := buildWindowQuota(-10, 60, 100)
	if q2.RemainingPercent != 100 {
		t.Fatalf("remaining = %.1f, want 100", q2.RemainingPercent)
	}
	if !q2.ResetsAtTime.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("unexpected reset time: %v", q2.ResetsAtTime)
	}
}
