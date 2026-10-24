package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestStatusShowsCostAndBudgetAlert(t *testing.T) {
	v := testVault(t)
	shared := v.SharedConfig()
	shared.Pricing = append(agent.DefaultPricing(), agent.ProviderPricing{Provider: agent.ProviderOllama, ModelPattern: "budgeted", MonthlyBudgetUSD: 1})
	if err := v.SetSharedConfig(shared); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().UTC().AddDate(-1, 0, 0).Format(time.RFC3339)
	history := fmt.Sprintf(`{"provider":"ollama","estimated_cost_usd":0.9,"success":true,"timestamp":%q}
{"provider":"ollama","estimated_cost_usd":5,"success":true,"timestamp":%q}
{"provider":"claude","estimated_cost_usd":2,"success":false,"timestamp":%q}
`, now, old, now)
	if err := os.WriteFile(filepath.Join(filepath.Dir(v.Path()), "prompt-history.jsonl"), []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}

	m := initialModel(v)
	msg := statusRefreshCmd(v, true)()
	updated, _ := m.Update(msg)
	out := updated.(model).renderStatus()

	for _, want := range []string{
		"Total:        $5.9000 (2 prompts)",
		"ollama:       $5.9000 total, $0.9000 this month",
		"Budget: ollama spent $0.90 of $1.00 this month (90%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "claude:") {
		t.Errorf("failed prompt counted:\n%s", out)
	}
}

func TestStatusCostEmptyAndLoading(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	if out := m.renderStatus(); !strings.Contains(out, "Loading...") {
		t.Errorf("before refresh, want Loading:\n%s", out)
	}
	updated, _ := m.Update(statusRefreshCmd(v, true)())
	if out := updated.(model).renderStatus(); !strings.Contains(out, "No successful prompts recorded yet.") {
		t.Errorf("no history, want empty note:\n%s", out)
	}
}

func TestStatusCostReadOnlyOnStatusTab(t *testing.T) {
	v := testVault(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(v.Path()), "prompt-history.jsonl"),
		[]byte(`{"provider":"ollama","estimated_cost_usd":1,"success":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := initialModel(v)
	if msg := statusRefreshCmd(v, false)().(statusRefreshMsg); msg.report.Cost != nil || msg.costRead {
		t.Fatalf("history read off the Status tab: %+v", msg.report.Cost)
	}
	// Opening the tab reads the cost; a later refresh off the tab keeps it.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("7")})
	if cmd == nil {
		t.Fatal("opening the Status tab started no refresh")
	}
	next, _ = next.(model).Update(cmd())
	next, _ = next.(model).Update(statusRefreshCmd(v, false)())
	if out := next.(model).renderStatus(); !strings.Contains(out, "Total:        $1.0000 (1 prompts)") {
		t.Errorf("cost dropped by a refresh that did not read it:\n%s", out)
	}
}

func TestStatusCapabilityMatrix(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	if out := m.renderStatus(); !strings.Contains(out, "None recorded.") {
		t.Errorf("want empty note:\n%s", out)
	}
	if err := v.AddCapability(agent.ModelCapabilityEntry{
		EndpointURL:  "http://user:secret@gpu.example:11434",
		ModelName:    "qwen-coder\x1b[31m",
		ContextSize:  32768,
		Capabilities: []string{"coding", "Review", "tools"},
	}); err != nil {
		t.Fatal(err)
	}
	m.refresh()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("7")})
	out := next.(model).View()
	row := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "qwen-coder") {
			row = line
		}
	}
	if !strings.Contains(row, "32768 yes yes  -   -   -   -   -  tools") {
		t.Errorf("matrix row = %q", row)
	}
	if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "user:secret") {
		t.Errorf("escape or URL secret shown:\n%s", out)
	}
	if !strings.Contains(out, "COD=coding") {
		t.Errorf("legend missing:\n%s", out)
	}
}
