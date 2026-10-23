package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	statuspkg "github.com/nikolareljin/agentvault/internal/status"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

// runPricing runs one pricing subcommand with flags, then puts every flag
// back to its default and unchanged, since cobra keeps them between runs.
func runPricing(t *testing.T, c *cobra.Command, args []string, flags map[string]string) (string, error) {
	t.Helper()
	for k, v := range flags {
		if err := c.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for k := range flags {
			f := c.Flags().Lookup(k)
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	}()
	return captureStdoutForTest(t, func() error { return c.RunE(c, args) })
}

func storedPricing(t *testing.T, cfgDir string) []agent.ProviderPricing {
	t.Helper()
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	return v.SharedConfig().Pricing
}

func findPricing(rows []agent.ProviderPricing, p agent.Provider, model string) (agent.ProviderPricing, bool) {
	for _, r := range rows {
		if r.Provider == p && r.ModelPattern == model {
			return r, true
		}
	}
	return agent.ProviderPricing{}, false
}

// The first set stores the defaults with the change: otherwise every other
// provider would drop to no pricing and cost $0.
func TestPricingSetSeedsDefaults(t *testing.T) {
	cfgDir := scopeTestVault(t)
	if _, err := runPricing(t, configPricingSetCmd, []string{"ollama"}, map[string]string{"input": "0.001", "output": "0.002", "monthly-budget": "5"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	rows := storedPricing(t, cfgDir)
	want := len(agent.DefaultPricing())
	if _, ok := findPricing(agent.DefaultPricing(), agent.ProviderOllama, ""); !ok {
		want++
	}
	if len(rows) != want {
		t.Errorf("stored %d rows, want %d (the defaults, ollama set)", len(rows), want)
	}
	if r, ok := findPricing(rows, agent.ProviderOllama, ""); !ok || r.InputPer1KTokens != 0.001 || r.MonthlyBudgetUSD != 5 {
		t.Errorf("ollama row = %+v %v", r, ok)
	}
	if _, ok := findPricing(rows, agent.ProviderClaude, "sonnet"); !ok {
		t.Error("the default claude sonnet row was dropped")
	}
}

// Changing one rate of a stored row keeps the others.
func TestPricingSetKeepsUnchangedRates(t *testing.T) {
	cfgDir := scopeTestVault(t)
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"model": "Sonnet", "input": "0.01"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	r, ok := findPricing(storedPricing(t, cfgDir), agent.ProviderClaude, "sonnet")
	if !ok || r.InputPer1KTokens != 0.01 || r.OutputPer1KTokens != 0.015 {
		t.Errorf("claude sonnet after set --input = %+v %v", r, ok)
	}
}

func TestPricingSetRefusesBadInput(t *testing.T) {
	scopeTestVault(t)
	for _, tc := range []struct {
		args  []string
		flags map[string]string
		want  string
	}{
		{[]string{"nosuch"}, map[string]string{"input": "1", "output": "1"}, "unknown provider"},
		{[]string{"ollama"}, map[string]string{"input": "-1", "output": "1"}, ">= 0"},
		{[]string{"ollama"}, map[string]string{"model": "x", "input": "NaN", "output": "1"}, "--input must be"},
		{[]string{"ollama"}, map[string]string{"model": "x", "input": "1", "output": "Inf"}, "--output must be"},
		{[]string{"ollama"}, map[string]string{"model": "x", "input": "1"}, "needs --input and --output"},
	} {
		if _, err := runPricing(t, configPricingSetCmd, tc.args, tc.flags); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("set %v %v: %v, want %q", tc.args, tc.flags, err, tc.want)
		}
	}
}

func TestPricingOneBudgetPerProvider(t *testing.T) {
	cfgDir := scopeTestVault(t)
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"model": "sonnet", "monthly-budget": "10"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"monthly-budget": "50"}); err != nil {
		t.Fatal(err)
	}
	rows := storedPricing(t, cfgDir)
	if p, _ := findPricing(rows, agent.ProviderClaude, "sonnet"); p.MonthlyBudgetUSD != 0 {
		t.Errorf("sonnet budget %v, want 0 after the provider row took the budget", p.MonthlyBudgetUSD)
	}
	if p, _ := findPricing(rows, agent.ProviderClaude, ""); p.MonthlyBudgetUSD != 50 {
		t.Errorf("claude budget %v, want 50", p.MonthlyBudgetUSD)
	}
	// 0 on another row removes nothing but that row's own budget.
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"model": "opus", "monthly-budget": "0"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := findPricing(storedPricing(t, cfgDir), agent.ProviderClaude, ""); p.MonthlyBudgetUSD != 50 {
		t.Errorf("claude budget %v after opus set to 0, want 50", p.MonthlyBudgetUSD)
	}
}

func TestPricingRemove(t *testing.T) {
	cfgDir := scopeTestVault(t)
	if _, err := runPricing(t, configPricingRemoveCmd, []string{"claude"}, map[string]string{"model": "sonnet"}); err == nil {
		t.Error("remove with no pricing stored succeeded")
	}
	if _, err := runPricing(t, configPricingSetCmd, []string{"ollama"}, map[string]string{"input": "0", "output": "0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, configPricingRemoveCmd, []string{"claude"}, map[string]string{"model": "sonnet"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := findPricing(storedPricing(t, cfgDir), agent.ProviderClaude, "sonnet"); ok {
		t.Error("claude sonnet still stored")
	}
	if _, err := runPricing(t, configPricingRemoveCmd, []string{"claude"}, map[string]string{"model": "sonnet"}); err == nil {
		t.Error("removing a missing row succeeded")
	}
}

func TestWriteCostCSV(t *testing.T) {
	var b bytes.Buffer
	report := &statuspkg.CostReport{
		ByProvider: map[string]float64{"claude": 1.5, "=cmd": 0.25},
		ThisMonth:  map[string]float64{"claude": 0.5},
	}
	pricing := []agent.ProviderPricing{{Provider: agent.ProviderClaude, MonthlyBudgetUSD: 20}}
	if err := writeCostCSV(&b, report, pricing); err != nil {
		t.Fatal(err)
	}
	want := "provider,total_usd,this_month_usd,monthly_budget_usd\n'=cmd,0.250000,0.000000,\nclaude,1.500000,0.500000,20\n"
	if b.String() != want {
		t.Errorf("csv =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestPricingMatchesStoredRowIgnoringCase(t *testing.T) {
	cfgDir := scopeTestVault(t)
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	// A bundle or an older vault can hold a pattern that is not lower case.
	if err := v.SetSharedConfig(agent.SharedConfig{Pricing: []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, ModelPattern: "Sonnet", InputPer1KTokens: 1, OutputPer1KTokens: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"model": "sonnet", "input": "3"}); err != nil {
		t.Fatal(err)
	}
	rows := storedPricing(t, cfgDir)
	if len(rows) != 1 || rows[0].InputPer1KTokens != 3 || rows[0].OutputPer1KTokens != 2 {
		t.Fatalf("rows = %+v, want the Sonnet row updated in place", rows)
	}
	if _, err := runPricing(t, configPricingRemoveCmd, []string{"claude"}, map[string]string{"model": "SONNET"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestPricingSetNeedsAChange(t *testing.T) {
	cfgDir := scopeTestVault(t)
	if _, err := runPricing(t, configPricingSetCmd, []string{"claude"}, map[string]string{"model": "sonnet"}); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("set with no values: %v", err)
	}
	if rows := storedPricing(t, cfgDir); len(rows) != 0 {
		t.Errorf("stored %d rows, want none", len(rows))
	}
}
