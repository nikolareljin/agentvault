package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// tierAgent is a claude anthropic member of group "claude" with a key.
func tierAgent(name, tier, policy string, def bool) agent.Agent {
	return agent.Agent{Name: name, Provider: agent.ProviderClaude, Model: "m-" + name, APIKey: "k-" + name,
		Variant: &agent.AgentVariant{Group: "claude", Default: def, Tier: tier, Policy: policy}}
}

func newVariantPromptCmd(variant, tier string) *cobra.Command {
	cmd := newPromptOptimizationTestCommand()
	cmd.Flags().Bool("auto", false, "")
	cmd.Flags().String("variant", variant, "")
	cmd.Flags().String("tier", tier, "")
	cmd.SetErr(&bytes.Buffer{})
	return cmd
}

// stubLookPath makes every provider CLI present, except the names in missing.
func stubLookPath(t *testing.T, missing ...string) {
	t.Helper()
	prev := promptLookPath
	promptLookPath = func(name string) (string, error) {
		for _, m := range missing {
			if m == name {
				return "", errors.New("not found")
			}
		}
		return "/bin/" + name, nil
	}
	t.Cleanup(func() { promptLookPath = prev })
}

const deepPrompt = "Refactor the parser module.\n1. read the code\n2. list the problems\n3. write tests\n4. fix the bugs\n5. then run the suite\n6. finally commit the fix"

func TestVariantChoiceByPolicyAndTier(t *testing.T) {
	stubLookPath(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	members := func(policy string) stubPromptVault {
		return stubPromptVault{agents: []agent.Agent{
			tierAgent("c-simple", "simple", policy, false),
			tierAgent("c-standard", "standard", policy, true),
			tierAgent("c-deep", "deep", policy, false),
		}}
	}
	for _, tc := range []struct {
		policy, prompt, tierFlag, want, tier string
	}{
		{"", "hi", "", "c-standard", "simple"},
		{"", deepPrompt, "", "c-standard", "deep"},
		{"balanced", "hi", "", "c-simple", "simple"},
		{"balanced", deepPrompt, "", "c-deep", "deep"},
		{"cost-first", deepPrompt, "", "c-standard", "deep"},
		{"quality-first", "hi", "", "c-standard", "simple"},
		{"balanced", "hi", "deep", "c-deep", "deep"},
	} {
		got, choice, err := resolveVariantChoice(newVariantPromptCmd("", tc.tierFlag), members(tc.policy), "claude", tc.prompt)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != tc.want || choice.Variant != tc.want || choice.Tier != tc.tier {
			t.Fatalf("policy %q prompt %.10q: got %s tier %s, want %s tier %s", tc.policy, tc.prompt, got.Name, choice.Tier, tc.want, tc.tier)
		}
	}
}

// Under the default policy the group default runs even when it cannot
// start: no pre-flight, no fallback, no stderr line, as in 0.17.0.
func TestVariantDefaultPolicyUnchanged(t *testing.T) {
	stubLookPath(t, "claude")
	v := stubPromptVault{agents: []agent.Agent{tierAgent("c-a", "simple", "", false), tierAgent("c-b", "deep", "", true)}}
	cmd := newVariantPromptCmd("", "")
	a, _, _, choice, err := resolvePromptAgentChoice(cmd, v, []string{"claude"}, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "c-b" || len(choice.Fallbacks) != 0 || showVariantChoice(choice) {
		t.Fatalf("default policy: got %s, fallbacks %v, show %v", a.Name, choice.Fallbacks, showVariantChoice(choice))
	}
	// resolvePromptAgent (the pre-policy entry point) agrees.
	if a2, _, _, err := resolvePromptAgent(cmd, v, []string{"claude"}, deepPrompt); err != nil || a2.Name != "c-b" {
		t.Fatalf("resolvePromptAgent = %s, %v", a2.Name, err)
	}
}

func TestVariantOverrides(t *testing.T) {
	stubLookPath(t)
	v := stubPromptVault{agents: []agent.Agent{
		tierAgent("c-simple", "simple", "balanced", true),
		tierAgent("c-deep", "deep", "balanced", false),
		{Name: "solo", Provider: agent.ProviderClaude, APIKey: "k"},
	}}
	got, choice, err := resolveVariantChoice(newVariantPromptCmd("c-deep", ""), v, "claude", "hi")
	if err != nil || got.Name != "c-deep" || !choice.Overridden {
		t.Fatalf("--variant c-deep: %s %v", got.Name, err)
	}
	if _, _, err := resolveVariantChoice(newVariantPromptCmd("solo", ""), v, "claude", "hi"); err == nil || !strings.Contains(err.Error(), `agent "solo" is not in variant group "claude"`) {
		t.Fatalf("--variant outside group: err = %v", err)
	}
	if _, _, err := resolveVariantChoice(newVariantPromptCmd("", "huge"), v, "claude", "hi"); err == nil || !strings.Contains(err.Error(), "unknown variant tier") {
		t.Fatalf("--tier huge: err = %v", err)
	}
	if _, _, _, _, err := resolvePromptAgentChoice(newVariantPromptCmd("c-deep", ""), v, []string{"solo"}, "hi"); !errors.Is(err, errVariantFlagsNeedGroup) {
		t.Fatalf("--variant with an agent name: err = %v", err)
	}
	auto := newVariantPromptCmd("", "deep")
	_ = auto.Flags().Set("auto", "true")
	if _, _, _, _, err := resolvePromptAgentChoice(auto, v, nil, "hi"); err == nil || !strings.Contains(err.Error(), "--auto does not take") {
		t.Fatalf("--auto --tier: err = %v", err)
	}
}

func TestVariantFailover(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	stubLookPath(t)
	noKey := tierAgent("c-deep-a", "deep", "balanced", true)
	noKey.APIKey = ""
	gwMissing := agent.Agent{Name: "c-deep-b", Provider: agent.ProviderClaude, Backend: "gateway",
		ProviderMeta: &agent.AgentProviderMeta{GatewayName: "gone"},
		Variant:      &agent.AgentVariant{Group: "claude", Tier: "deep", Policy: "balanced"}}
	v := gatewayPromptVault{stubPromptVault: stubPromptVault{agents: []agent.Agent{
		noKey, gwMissing, tierAgent("c-standard", "standard", "balanced", false),
	}}, gw: agent.Gateway{Name: "corp", URL: "https://corp.example.com"}}

	got, choice, err := resolveVariantChoice(newVariantPromptCmd("", "deep"), v, "claude", "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "c-standard" {
		t.Fatalf("ran %s, want c-standard", got.Name)
	}
	want := `c-deep-a: no anthropic API key (agent key or ANTHROPIC_API_KEY); c-deep-b: stored gateway "gone" not found`
	if choice.FallbackReason() != want {
		t.Fatalf("fallback reason = %q, want %q", choice.FallbackReason(), want)
	}
	var buf bytes.Buffer
	printVariantChoice(&buf, choice)
	if wantOut := "variant: c-standard (group claude, policy balanced, tier deep)\nfallback from c-deep-a: no anthropic API key (agent key or ANTHROPIC_API_KEY)\nfallback from c-deep-b: stored gateway \"gone\" not found\n"; buf.String() != wantOut {
		t.Fatalf("stderr line:\n%s", buf.String())
	}

	// No CLI on PATH: every member fails, and the error names each one.
	stubLookPath(t, "claude")
	_, _, err = resolveVariantChoice(newVariantPromptCmd("", "deep"), v, "claude", "x")
	if err == nil {
		t.Fatal("all members fail: want error")
	}
	for _, part := range []string{`no variant of group "claude" can run`, "c-deep-a: no anthropic API key", `c-deep-b: stored gateway "gone" not found`, "c-standard: claude binary not found in PATH"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q lacks %q", err, part)
		}
	}
}

func TestVariantStoreRoundTripAndImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := vault.New(path)
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []agent.Agent{tierAgent("c-a", "simple", "", true), tierAgent("c-b", "deep", "", false)} {
		if err := v.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.SetVariantPolicy("claude", "quality-first"); err != nil {
		t.Fatal(err)
	}
	re := vault.New(path)
	if err := re.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	if p := agent.VariantPolicyOf(re.List(), "claude"); p != "quality-first" {
		t.Fatalf("policy after reload = %q", p)
	}
	if b, _ := re.Get("c-b"); b.Variant.Tier != "deep" || b.Variant.Policy != "quality-first" {
		t.Fatalf("c-b after reload = %#v", b.Variant)
	}

	bundle := SetupBundle{Agents: []agent.Agent{tierAgent("c-a", "standard", "cost-first", true), tierAgent("c-n", "deep", "cost-first", false)}}
	for _, tc := range []struct {
		strategy importStrategy
		policy   string
		tierA    string
	}{
		{strategyMerge, "quality-first", "simple"},
		{strategyReplace, "cost-first", "standard"},
		{strategyMirror, "cost-first", "standard"},
	} {
		t.Run(string(tc.strategy), func(t *testing.T) {
			v := newTestVault(t)
			for _, a := range []agent.Agent{tierAgent("c-a", "simple", "", true), tierAgent("c-b", "deep", "", false)} {
				if err := v.Add(a); err != nil {
					t.Fatal(err)
				}
			}
			if err := v.SetVariantPolicy("claude", "quality-first"); err != nil {
				t.Fatal(err)
			}
			plan := planProfileImport(v, bundle, tc.strategy, time.Now(), importConsent{})
			if err := commitProfileImport(v, plan); err != nil {
				t.Fatal(err)
			}
			for _, m := range agent.VariantMembers(v.List(), "claude") {
				if m.Variant.Policy != tc.policy {
					t.Fatalf("%s policy = %q, want %q", m.Name, m.Variant.Policy, tc.policy)
				}
			}
			if a, _ := v.Get("c-a"); a.Variant.Tier != tc.tierA {
				t.Fatalf("c-a tier = %q, want %q", a.Variant.Tier, tc.tierA)
			}
			if n, ok := v.Get("c-n"); !ok || n.Variant.Tier != "deep" {
				t.Fatalf("c-n = %#v", n)
			}
			lines := strings.Join(plan.Report.Lines, "\n")
			if changed := tc.policy != "quality-first"; changed != strings.Contains(lines, "group claude policy cost-first (bundle)") {
				t.Fatalf("report:\n%s", lines)
			}
		})
	}
	// A local group without a policy takes the bundle's even under merge.
	v2 := newTestVault(t)
	if err := v2.Add(tierAgent("c-a", "simple", "", true)); err != nil {
		t.Fatal(err)
	}
	plan := planProfileImport(v2, bundle, strategyMerge, time.Now(), importConsent{})
	if err := commitProfileImport(v2, plan); err != nil {
		t.Fatal(err)
	}
	if p := agent.VariantPolicyOf(v2.List(), "claude"); p != "cost-first" {
		t.Fatalf("merge into unset policy = %q", p)
	}
}

// TestVariantPromptEndToEnd drives add/edit --variant-tier, variant policy,
// variant list, list, prompt --dry-run --json and a real run whose history
// record carries the variant fields.
func TestVariantPromptEndToEnd(t *testing.T) {
	cfgDir := scopeTestVault(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	stubLookPath(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "pong", "prompt_eval_count": 3, "eval_count": 1})
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	promptCmd.SetErr(&stderr)
	t.Cleanup(func() { promptCmd.SetErr(nil) })
	resetFlags := func() {
		for _, c := range []string{"add", "edit", "prompt"} {
			cmd, _, _ := rootCmd.Find([]string{c})
			cmd.Flags().VisitAll(func(fl *pflag.Flag) {
				_ = fl.Value.Set(fl.DefValue)
				fl.Changed = false
			})
		}
	}
	t.Cleanup(resetFlags)
	run := func(args ...string) (string, error) {
		resetFlags()
		return captureStdoutForTest(t, func() error {
			rootCmd.SetArgs(args)
			return rootCmd.Execute()
		})
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	must("add", "a-claude", "--provider", "claude", "--model", "opus", "--variant-group", "grp", "--variant-default", "--variant-tier", "simple")
	must("add", "b-local", "--provider", "ollama", "--model", "llama3", "--base-url", srv.URL, "--variant-group", "grp")
	must("edit", "b-local", "--variant-tier", "simple")
	must("add", "c-deep", "--provider", "claude", "--model", "opus", "--api-key", "k", "--variant-group", "grp", "--variant-tier", "deep")
	if _, err := run("edit", "c-deep", "--variant-tier", "huge"); err == nil {
		t.Fatal("--variant-tier huge: want error")
	}
	if _, err := run("add", "loner", "--provider", "claude", "--variant-tier", "deep"); err == nil {
		t.Fatal("--variant-tier without a group: want error")
	}
	if out := must("variant", "policy", "grp"); strings.TrimSpace(out) != "default" {
		t.Fatalf("policy before set = %q", out)
	}
	if _, err := run("variant", "policy", "grp", "cheap"); err == nil {
		t.Fatal("bad policy: want error")
	}
	must("variant", "policy", "grp", "balanced")
	if out := must("variant", "policy", "grp"); strings.TrimSpace(out) != "balanced" {
		t.Fatalf("policy = %q", out)
	}
	out := must("variant", "list")
	if !strings.Contains(out, "TIER") || !strings.Contains(out, "balanced") || !strings.Contains(out, "deep") {
		t.Fatalf("variant list:\n%s", out)
	}
	if out := must("list"); !strings.Contains(out, "grp*:simple") || !strings.Contains(out, "grp:deep") {
		t.Fatalf("list:\n%s", out)
	}

	// Dry run: a-claude (simple, default) has no key, so b-local runs.
	out = must("prompt", "grp", "--text", "hi", "--dry-run", "--json", "--no-log")
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if payload["variant"] != "b-local" || payload["group"] != "grp" || payload["policy"] != "balanced" || payload["tier"] != "simple" ||
		!strings.Contains(payload["fallback_reason"].(string), "a-claude: no anthropic API key") {
		t.Fatalf("dry-run json = %v", payload)
	}
	stderr.Reset()
	must("prompt", "grp", "--text", deepPrompt, "--dry-run", "--no-log")
	if !strings.Contains(stderr.String(), "variant: c-deep (group grp, policy balanced, tier deep)") {
		t.Fatalf("dry-run stderr:\n%s", stderr.String())
	}

	// A real run writes the choice into history.
	history := filepath.Join(cfgDir, "h.jsonl")
	stderr.Reset()
	must("prompt", "grp", "--text", "hi", "--history-file", history, "--no-stream")
	if !strings.Contains(stderr.String(), "variant: b-local (group grp, policy balanced, tier simple)") ||
		!strings.Contains(stderr.String(), "fallback from a-claude: no anthropic API key") {
		t.Fatalf("run stderr:\n%s", stderr.String())
	}
	raw, err := os.ReadFile(history)
	if err != nil {
		t.Fatal(err)
	}
	var rec PromptRecord
	if err := json.Unmarshal(bytes.TrimSpace(raw), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.AgentName != "b-local" || rec.Variant != "b-local" || rec.VariantGroup != "grp" || rec.VariantPolicy != "balanced" ||
		rec.VariantTier != "simple" || !strings.HasPrefix(rec.FallbackReason, "a-claude: ") || !rec.Success {
		t.Fatalf("history record = %#v", rec)
	}

	// The vault on disk keeps tier and policy.
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if c, _ := v.Get("c-deep"); c.Variant == nil || c.Variant.Tier != "deep" || c.Variant.Policy != "balanced" {
		t.Fatalf("stored c-deep = %#v", c.Variant)
	}
}
