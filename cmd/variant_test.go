package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func variantAgent(name, backend string, def bool) agent.Agent {
	a := agent.Agent{Name: name, Provider: agent.ProviderClaude, Model: "m-" + name, Backend: backend,
		Variant: &agent.AgentVariant{Group: "claude", Default: def}}
	switch backend {
	case agent.ClaudeBackendGateway:
		a.BaseURL = "https://gw.example.com"
		a.APIKey = "gw-secret"
	case agent.ClaudeBackendVertex:
		a.ProviderMeta = &agent.AgentProviderMeta{VertexRegion: "us-east5", VertexProject: "proj"}
	}
	return a
}

func defaultOf(t *testing.T, v *vault.Vault, group string) string {
	t.Helper()
	n := 0
	name := ""
	for _, a := range v.List() {
		if a.VariantGroup() == group && a.IsVariantDefault() {
			n++
			name = a.Name
		}
	}
	if n != 1 {
		t.Fatalf("group %s has %d defaults, want exactly 1: %#v", group, n, v.List())
	}
	return name
}

func TestVaultKeepsOneVariantDefault(t *testing.T) {
	v := newTestVault(t)
	for _, a := range []agent.Agent{variantAgent("c-direct", "", true), variantAgent("c-bedrock", "bedrock", false)} {
		if err := v.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	// Adding a new default clears the old one.
	if err := v.Add(variantAgent("c-gw", "gateway", true)); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(t, v, "claude"); got != "c-gw" {
		t.Fatalf("default = %s, want c-gw", got)
	}
	// Removing the default falls back to the first member by name, with a note.
	if err := v.Remove("c-gw"); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(t, v, "claude"); got != "c-bedrock" {
		t.Fatalf("default after remove = %s, want c-bedrock", got)
	}
	if notes := v.VariantNotes(); len(notes) != 1 || !strings.Contains(notes[0], "using c-bedrock") {
		t.Fatalf("notes = %v", notes)
	}
	if err := v.SetVariantDefault("claude", "c-direct"); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(t, v, "claude"); got != "c-direct" {
		t.Fatalf("default = %s, want c-direct", got)
	}
}

func TestImportVariantDefaultMergeVsReplace(t *testing.T) {
	bundle := SetupBundle{Agents: []agent.Agent{
		variantAgent("c-direct", "", false),
		variantAgent("c-bedrock", "bedrock", true),
		{Name: "odd", Provider: agent.ProviderClaude, Backend: "foundry"},
	}}
	for _, tc := range []struct {
		strategy importStrategy
		want     string
		source   string
	}{
		{strategyMerge, "c-local", "local"},
		{strategyReplace, "c-bedrock", "bundle"},
		{strategyMirror, "c-bedrock", "bundle"},
	} {
		t.Run(string(tc.strategy), func(t *testing.T) {
			v := newTestVault(t)
			if err := v.Add(variantAgent("c-local", "", true)); err != nil {
				t.Fatal(err)
			}
			if err := v.Add(variantAgent("c-direct", "", false)); err != nil {
				t.Fatal(err)
			}
			plan := planProfileImport(v, bundle, tc.strategy, time.Now(), importConsent{})
			if err := commitProfileImport(v, plan); err != nil {
				t.Fatal(err)
			}
			if got := defaultOf(t, v, "claude"); got != tc.want {
				t.Fatalf("default = %s, want %s", got, tc.want)
			}
			lines := strings.Join(plan.Report.Lines, "\n")
			if !strings.Contains(lines, "Variant: group claude default "+tc.want+" ("+tc.source+")") {
				t.Fatalf("report lacks the variant line:\n%s", lines)
			}
			if !strings.Contains(lines, `Note: agent odd: unknown claude backend "foundry"`) {
				t.Fatalf("report lacks the unknown backend note:\n%s", lines)
			}
		})
	}
}

func TestImportVariantDefaultFallsBackWhenWinnerRemoved(t *testing.T) {
	v := newTestVault(t)
	if err := v.Add(variantAgent("c-local", "", true)); err != nil {
		t.Fatal(err)
	}
	// Mirror drops c-local; the bundle has members but no marked default.
	bundle := SetupBundle{Agents: []agent.Agent{variantAgent("c-z", "", false), variantAgent("c-b", "bedrock", false)}}
	plan := planProfileImport(v, bundle, strategyMirror, time.Now(), importConsent{})
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(t, v, "claude"); got != "c-b" {
		t.Fatalf("default = %s, want c-b (first by name)", got)
	}
	if lines := strings.Join(plan.Report.Lines, "\n"); !strings.Contains(lines, "default c-b (first by name)") {
		t.Fatalf("report:\n%s", lines)
	}
}

// TestVariantExportImportRoundTrip exports a variant group with and without
// keys and imports it into a fresh vault.
func TestVariantExportImportRoundTrip(t *testing.T) {
	for _, keys := range []string{"true", "false"} {
		t.Run("include-keys="+keys, func(t *testing.T) {
			root := t.TempDir()
			setTestHomeDir(t, root)
			t.Setenv("XDG_CONFIG_HOME", root)
			t.Setenv(ConfigDirsEnv, "")
			t.Setenv(VaultPasswordEnv, e2eMasterPassword)
			t.Setenv(ExportPasswordEnv, e2eBundlePassword)
			t.Setenv(ImportPasswordEnv, e2eBundlePassword)
			t.Setenv("ANTHROPIC_API_KEY", "")

			seedProfileVault(t, filepath.Join(root, config.AppName), []agent.Agent{
				variantAgent("c-direct", "", false),
				variantAgent("c-bedrock", "bedrock", false),
				variantAgent("c-gw", "gateway", false),
				variantAgent("c-vertex", "vertex", true),
			}, nil)

			bundlePath := filepath.Join(root, "b"+DefaultExportExtension)
			var out bytes.Buffer
			exportCmd.SetOut(&out)
			exportCmd.SetErr(&out)
			t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
			withFlags(t, exportCmd, map[string]string{
				"yes": "true", "include-provider-files": "false", "include-skills": "false",
				"detect": "false", "include-keys": keys,
			})
			if err := runExport(exportCmd, []string{bundlePath}); err != nil {
				t.Fatalf("runExport() error = %v\n%s", err, out.String())
			}

			targetDir := filepath.Join(root, "machine-b")
			seedProfileVault(t, targetDir, nil, nil)
			useConfigDir(t, targetDir)
			importCmd.SetOut(&out)
			importCmd.SetErr(&out)
			t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
			withFlags(t, importCmd, map[string]string{"strategy": string(strategyMerge)})
			if err := runImport(importCmd, []string{bundlePath}); err != nil {
				t.Fatalf("runImport() error = %v\n%s", err, out.String())
			}

			target := vault.New(filepath.Join(targetDir, config.VaultFile))
			if err := target.Unlock(e2eMasterPassword); err != nil {
				t.Fatal(err)
			}
			if got := defaultOf(t, target, "claude"); got != "c-vertex" {
				t.Fatalf("default = %s, want c-vertex", got)
			}
			members := agent.VariantMembers(target.List(), "claude")
			if len(members) != 4 {
				t.Fatalf("members = %#v, want 4", members)
			}
			gw, _ := target.Get("c-gw")
			vx, _ := target.Get("c-vertex")
			if gw.Backend != "gateway" || gw.BaseURL != "https://gw.example.com" || vx.ProviderMeta == nil || vx.ProviderMeta.VertexProject != "proj" {
				t.Fatalf("backend settings lost: gw=%#v vertex=%#v", gw, vx)
			}
			wantKey := ""
			if keys == "true" {
				wantKey = "gw-secret"
			}
			if gw.APIKey != wantKey {
				t.Fatalf("gateway key = %q, want %q", gw.APIKey, wantKey)
			}
		})
	}
}

func TestPrintVariantList(t *testing.T) {
	agents := []agent.Agent{variantAgent("c-b", "bedrock", false), variantAgent("c-a", "", true),
		{Name: "odd", Provider: agent.ProviderClaude, Backend: "foundry", Variant: &agent.AgentVariant{Group: "claude"}}}
	var b bytes.Buffer
	if err := printVariantList(&b, agents, ""); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"c-a", "yes", "claude/bedrock", "claude/foundry (unknown)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output lacks %q:\n%s", want, got)
		}
	}
	if err := printVariantList(&b, agents, "nope"); err == nil {
		t.Fatal("unknown group: want an error")
	}
}

func TestVariantCommandsEndToEnd(t *testing.T) {
	scopeTestVault(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	run := func(args ...string) (string, error) {
		return captureStdoutForTest(t, func() error {
			rootCmd.SetArgs(args)
			return rootCmd.Execute()
		})
	}
	resetFlags := func() {
		for _, c := range []string{"add", "edit"} {
			cmd, _, _ := rootCmd.Find([]string{c})
			for _, f := range []string{"variant-group", "variant-default", "backend", "base-url", "vertex-region", "vertex-project", "gateway", "model", "api-key", "auth-mode"} {
				if fl := cmd.Flags().Lookup(f); fl != nil {
					_ = fl.Value.Set(fl.DefValue)
					fl.Changed = false
				}
			}
		}
	}
	t.Cleanup(resetFlags)
	steps := [][]string{
		{"add", "c-direct", "--provider", "claude", "--model", "opus", "--variant-group", "claude", "--variant-default"},
		{"add", "c-gw", "--provider", "claude", "--backend", "gateway", "--base-url", "https://gw.example.com", "--variant-group", "claude"},
		{"edit", "c-gw", "--variant-default"},
	}
	for _, s := range steps {
		resetFlags()
		if out, err := run(s...); err != nil {
			t.Fatalf("%v: %v\n%s", s, err, out)
		}
	}
	resetFlags()
	if _, err := run("add", "bad", "--provider", "claude", "--backend", "gateway", "--base-url", "https://u:p@gw.example.com"); err == nil {
		t.Fatal("gateway URL with credentials: want an error")
	}
	resetFlags()
	if _, err := run("add", "bad2", "--provider", "claude", "--backend", "gateway", "--gateway", "missing"); err == nil || !strings.Contains(err.Error(), `gateway "missing" not found`) {
		t.Fatalf("missing stored gateway: err = %v", err)
	}
	resetFlags()
	out, err := run("variant", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c-gw") || !strings.Contains(out, "claude/gateway") {
		t.Fatalf("variant list:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "c-direct") && strings.Contains(line, "yes") {
			t.Fatalf("c-direct still default after edit:\n%s", out)
		}
	}
	if _, err := run("variant", "default", "claude", "c-direct"); err != nil {
		t.Fatal(err)
	}
	out, _ = run("list")
	if !strings.Contains(out, "claude*") {
		t.Fatalf("list lacks the default marker:\n%s", out)
	}
}

type gatewayPromptVault struct {
	stubPromptVault
	gw agent.Gateway
}

func (g gatewayPromptVault) GetGateway(name string) (agent.Gateway, bool) {
	return g.gw, name == g.gw.Name
}

func TestResolvePromptAgentGroupAndStoredGateway(t *testing.T) {
	cmd := newPromptOptimizationTestCommand()
	cmd.Flags().Bool("auto", false, "")
	gwAgent := agent.Agent{Name: "c-gw", Provider: agent.ProviderClaude, Backend: "gateway",
		ProviderMeta: &agent.AgentProviderMeta{GatewayName: "corp"},
		Variant:      &agent.AgentVariant{Group: "claude", Default: true}}
	v := gatewayPromptVault{
		stubPromptVault: stubPromptVault{agents: []agent.Agent{variantAgent("c-direct", "", false), gwAgent}},
		gw:              agent.Gateway{Name: "corp", URL: "https://corp.example.com", Token: "t1"},
	}
	got, _, _, err := resolvePromptAgent(cmd, v, []string{"claude"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "c-gw" || got.BaseURL != "https://corp.example.com" || got.APIKey != "t1" {
		t.Fatalf("resolved = %#v", got)
	}
	env, err := claudePromptEnv(got)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL=https://corp.example.com") || !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=t1") {
		t.Fatal("prompt env lacks the stored gateway URL and token")
	}
	// The stored agent is not changed.
	if a, _ := v.Get("c-gw"); a.BaseURL != "" || a.APIKey != "" || a.ProviderMeta.AuthMode != "" {
		t.Fatalf("stored agent changed: %#v", a)
	}
}
