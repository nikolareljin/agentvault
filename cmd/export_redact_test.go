package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func claudeConfigFrom(t *testing.T, settings string) *agent.ClaudeConfig {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(settings), &m); err != nil {
		t.Fatal(err)
	}
	c := &agent.ClaudeConfig{EnabledPlugins: map[string]bool{}, CustomSettings: map[string]any{}}
	agent.ApplyClaudeSettings(c, m)
	return c
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRedactVaultSecretsBlanksVaultCopies(t *testing.T) {
	claude := claudeConfigFrom(t, `{"model":"opus","env":{"K":"canary-env"},
		"apiKeyHelper":"echo canary-helper","permissions":{"allow":["Bash(ls)"]},
		"mcpServers":{"s":{"command":"srv","headers":{"X-Id":"canary-hdr"}}}}`)
	b := SetupBundle{
		Agents: []agent.Agent{{
			Name:         "a",
			BaseURL:      "https://u:canary-pass@h.example",
			ProviderMeta: &agent.AgentProviderMeta{Extra: map[string]string{"api_token": "canary-extra", "region": "eu"}},
			MCPServers:   []agent.MCPServer{{Name: "m", Command: "x", Env: map[string]string{"T": "canary-agent-env"}}},
		}},
		SharedConfig: agent.SharedConfig{MCPServers: []agent.MCPServer{{
			Name: "gh", Command: "srv", Args: []string{"--token", "canary-arg", "--verbose"},
			Env: map[string]string{"GITHUB_TOKEN": "canary-shared-env"},
		}}},
		ProviderConfigs: agent.ProviderConfig{
			Claude: claude,
			Ollama: &agent.OllamaConfig{BaseURL: "http://localhost:11434", CustomSettings: map[string]any{"token": "canary-ollama"}},
		},
	}
	before := mustJSON(t, b)
	orig := b
	redactVaultSecrets(&b)
	got := mustJSON(t, b)
	if !strings.Contains(before, "canary-") {
		t.Fatal("fixture has no canaries")
	}
	if strings.Contains(got, "canary-") {
		t.Errorf("bundle carries a vault secret: %s", got)
	}
	for _, keep := range []string{`"opus"`, "Bash(ls)", `"--verbose"`, `"region":"eu"`, "localhost:11434"} {
		if !strings.Contains(got, keep) {
			t.Errorf("bundle lost %s: %s", keep, got)
		}
	}
	if len(b.ProviderConfigs.Claude.RedactedFields) == 0 {
		t.Error("no redacted pointers recorded for the Claude config")
	}
	// The vault's own values are untouched.
	if orig.SharedConfig.MCPServers[0].Args[1] != "canary-arg" || claude.CustomSettings["apiKeyHelper"] != "echo canary-helper" ||
		orig.Agents[0].ProviderMeta.Extra["api_token"] != "canary-extra" {
		t.Error("redaction changed the vault's values")
	}
}

func TestRedactClaudeConfigWithUnplaceableSecretDropsSettings(t *testing.T) {
	c := claudeConfigFrom(t, `{"model":"opus","statusLine":{"command":"run --token=canary-x"}}`)
	c.Keybindings = map[string]string{"ctrl+k": "x"}
	out := redactClaudeConfig(c)
	if !out.SettingsRedacted || strings.Contains(mustJSON(t, out), "canary-") {
		t.Fatalf("want settings left out: %s", mustJSON(t, out))
	}
	if out.Keybindings["ctrl+k"] != "x" {
		t.Error("keybindings lost")
	}
	if assets := claudeConfigAssets(out); len(assets) != 1 || assets[0].LogicalPath != "keybindings.json" {
		t.Errorf("a config without its settings must not write settings.json: %+v", assets)
	}
	// Import keeps this machine's settings.
	local := claudeConfigFrom(t, `{"model":"sonnet","env":{"K":"v"}}`)
	got, _ := planProviderConfigs(agent.ProviderConfig{Claude: local}, agent.ProviderConfig{Claude: out}, strategyMirror, &importReport{})
	if got.Claude.CustomSettings["model"] != "sonnet" || got.Claude.SettingsRedacted {
		t.Errorf("mirror import replaced local settings with none: %s", mustJSON(t, got.Claude))
	}
}

func TestRestoreClaudeConfigFillsOnlyUnchangedDefinition(t *testing.T) {
	local := claudeConfigFrom(t, `{"model":"opus","env":{"K":"secret-v"},
		"mcpServers":{"s":{"command":"srv","headers":{"Authorization":"Bearer local"}}}}`)
	exported := redactClaudeConfig(local)
	if assets := claudeConfigAssets(exported); len(assets) == 0 || len(assets[0].RedactedFields) == 0 {
		t.Fatalf("settings.json asset lost its redacted pointers: %+v", assets)
	}
	got := restoreClaudeConfig(exported, local)
	if s := mustJSON(t, got); !strings.Contains(s, "secret-v") || !strings.Contains(s, "Bearer local") || got.RedactedFields != nil {
		t.Errorf("unchanged config not restored: %s", s)
	}
	// The bundle points the server elsewhere: the local header stays here.
	moved := redactClaudeConfig(claudeConfigFrom(t, `{"model":"opus","env":{"K":"x"},
		"mcpServers":{"s":{"command":"other","headers":{"Authorization":"Bearer x"}}}}`))
	if s := mustJSON(t, restoreClaudeConfig(moved, local)); strings.Contains(s, "Bearer local") {
		t.Errorf("local header restored into a changed server: %s", s)
	}
}

func TestRestoreMCPServerSecrets(t *testing.T) {
	local := []agent.MCPServer{{Name: "gh", Command: "srv", Args: []string{"--token", "tok", "-v"}, Env: map[string]string{"T": "env-v"}}}
	in := blankMCPServerSecrets(local)
	if in[0].Args[1] != "" || in[0].Env["T"] != "" || local[0].Args[1] != "tok" {
		t.Fatalf("blanking: %+v (local %+v)", in, local)
	}
	got := restoreMCPServerSecrets(in, local)
	if got[0].Args[1] != "tok" || got[0].Env["T"] != "env-v" {
		t.Errorf("unchanged server not restored: %+v", got)
	}
	changed := blankMCPServerSecrets([]agent.MCPServer{{Name: "gh", Command: "evil", Args: []string{"--token", "x", "-v"}, Env: map[string]string{"T": "y"}}})
	got = restoreMCPServerSecrets(changed, local)
	if got[0].Args[1] == "tok" || got[0].Env["T"] == "env-v" {
		t.Errorf("local secret restored into a changed server: %+v", got)
	}
	if _, has := got[0].Env["T"]; has {
		t.Errorf("blank env value kept: %+v", got[0].Env)
	}
}

func TestPlanAgentOpsRestoresBlankedAgentSecrets(t *testing.T) {
	local := agent.Agent{Name: "a", BaseURL: "https://u:p@h.example",
		MCPServers: []agent.MCPServer{{Name: "m", Command: "x", Env: map[string]string{"T": "v"}}}}
	b := SetupBundle{Agents: []agent.Agent{local}}
	redactVaultSecrets(&b)
	ops := planAgentOps([]agent.Agent{local}, b.Agents, strategyMirror, local.CreatedAt, &importReport{})
	if len(ops) != 0 {
		t.Errorf("re-importing an unchanged agent rewrote it: %+v", ops)
	}
}

func TestPlanProfileImportRestoresSharedMCPSecrets(t *testing.T) {
	v := newTestVault(t)
	local := agent.SharedConfig{MCPServers: []agent.MCPServer{{
		Name: "gh", Command: "srv", Args: []string{"--token", "tok"}, Env: map[string]string{"T": "env-v"},
	}}}
	if err := v.SetSharedConfig(local); err != nil {
		t.Fatal(err)
	}
	b := SetupBundle{SharedConfig: local}
	redactVaultSecrets(&b)
	plan := planProfileImport(v, b, strategyMirror, time.Now())
	if plan.SharedChanged {
		t.Errorf("mirror import of an unchanged server blanked it: %+v", plan.Shared.MCPServers)
	}
}

func TestRestoreClaudeConfigOnFreshMachineKeepsSettings(t *testing.T) {
	exported := redactClaudeConfig(claudeConfigFrom(t, `{"model":"opus","env":{"K":"v"}}`))
	got := restoreClaudeConfig(exported, nil)
	s := mustJSON(t, got)
	if got.CustomSettings["model"] != "opus" || strings.Contains(s, `"K"`) {
		t.Errorf("want model kept and the blank env value left out: %s", s)
	}
}

func TestPlanProviderConfigsRestoresOllamaBaseURL(t *testing.T) {
	local := agent.ProviderConfig{Ollama: &agent.OllamaConfig{BaseURL: "https://u:p@h.example"}}
	b := SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	if b.ProviderConfigs.Ollama.BaseURL != "" {
		t.Fatalf("base URL with credentials exported: %q", b.ProviderConfigs.Ollama.BaseURL)
	}
	got, _ := planProviderConfigs(local, b.ProviderConfigs, strategyMirror, &importReport{})
	if got.Ollama.BaseURL != "https://u:p@h.example" {
		t.Errorf("local base URL lost: %q", got.Ollama.BaseURL)
	}
}

func TestRemapProviderConfigsRewritesRedactedPointers(t *testing.T) {
	exported := redactClaudeConfig(claudeConfigFrom(t, `{"mcpServers":{"/old/home/p":{"command":"srv","env":{"T":"v"}}}}`))
	pc, n, err := remapProviderConfigs(agent.ProviderConfig{Claude: exported}, []pathRemap{{From: "/old/home", To: "/new/home"}})
	if err != nil || n == 0 {
		t.Fatalf("remap: n=%d err=%v", n, err)
	}
	if got := pc.Claude.RedactedFields; len(got) != 1 || got[0] != "/mcpServers/~1new~1home~1p/env/T" {
		t.Errorf("pointers not remapped with their keys: %v", got)
	}
}
