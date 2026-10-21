package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestSameAgentEndpointRefusesBlankedPlainEndpoint(t *testing.T) {
	local := agent.Agent{Name: "a", Provider: agent.ProviderOpenAI, BaseURL: "https://corp.example/v1", APIKey: "local-key",
		ProviderMeta: &agent.AgentProviderMeta{Extra: map[string]string{"endpoint": "https://corp.example", "api_token": "tok"}}}
	for name, move := range map[string]func(*agent.Agent){
		"blank base_url":    func(a *agent.Agent) { a.BaseURL = "" },
		"blank plain extra": func(a *agent.Agent) { a.ProviderMeta.Extra["endpoint"] = "" },
		"missing extra":     func(a *agent.Agent) { delete(a.ProviderMeta.Extra, "endpoint") },
	} {
		b := SetupBundle{Agents: []agent.Agent{local}}
		b.Agents[0].ProviderMeta = &agent.AgentProviderMeta{Extra: map[string]string{"endpoint": "https://corp.example", "api_token": "tok"}}
		redactVaultSecrets(&b)
		b.Agents[0].APIKey = ""
		move(&b.Agents[0])
		ops := planAgentOps([]agent.Agent{local}, b.Agents, strategyMirror, local.CreatedAt, &importReport{})
		if s := mustJSON(t, ops); strings.Contains(s, "local-key") || strings.Contains(s, `"tok"`) {
			t.Errorf("%s: local secret restored into a changed endpoint: %s", name, s)
		}
	}
}

func TestRestoreMCPServerSecretsRefusesDroppedEnv(t *testing.T) {
	local := []agent.MCPServer{{Name: "gh", Command: "srv", Env: map[string]string{"API_URL": "https://corp.example", "T": "env-v"}}}
	in := blankMCPServerSecrets(local)
	delete(in[0].Env, "API_URL")
	got, _ := restoreMCPServerSecrets(in, local)
	if got[0].Env["T"] == "env-v" {
		t.Errorf("secret restored into a server missing an env value: %+v", got)
	}
}

func TestPlanAgentOpsNamesBlankSecretOfNewAgent(t *testing.T) {
	b := SetupBundle{Agents: []agent.Agent{{Name: "n", ProviderMeta: &agent.AgentProviderMeta{Extra: map[string]string{"api_token": "x"}}}}}
	redactVaultSecrets(&b)
	rep := &importReport{}
	planAgentOps(nil, b.Agents, strategyMirror, b.Agents[0].CreatedAt, rep)
	s := mustJSON(t, rep)
	if !strings.Contains(s, "no such agent") || strings.Contains(s, "differs") {
		t.Errorf("new agent's blank secret reported as a moved agent: %s", s)
	}
}

func TestDropBundleCommandsKeepsBlankedEnvQuietly(t *testing.T) {
	local := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://corp.example"}}`)
	content := []byte(`{"env":{"ANTHROPIC_BASE_URL":""}}`)
	out, dropped := dropBundleCommandsExcept(content, local, nil)
	if len(dropped) != 0 {
		t.Errorf("export's blank reported as a change: %v", dropped)
	}
	if !strings.Contains(string(out), "https://corp.example") {
		t.Errorf("local value not kept: %s", out)
	}
	_, dropped = dropBundleCommandsExcept([]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://evil.example"}}`), local, nil)
	if len(dropped) != 1 {
		t.Errorf("changed value not reported: %v", dropped)
	}
}

func TestPlanProviderConfigsKeepsLocalCustomSecrets(t *testing.T) {
	local := agent.ProviderConfig{
		Codex:  &agent.CodexConfig{CustomSettings: map[string]any{"tools": map[string]any{"api_token": "local-tok", "n": float64(2)}}},
		Ollama: &agent.OllamaConfig{CustomSettings: map[string]any{"token": "local-ol", "keep": "x"}},
	}
	b := SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	got, _ := planProviderConfigs(local, b.ProviderConfigs, strategyMirror, &importReport{})
	if s := mustJSON(t, got); !strings.Contains(s, "local-tok") || !strings.Contains(s, "local-ol") {
		t.Errorf("unchanged settings lost this machine's secrets: %s", s)
	}
	b = SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	b.ProviderConfigs.Codex.CustomSettings["base_url"] = "https://evil.example"
	got, _ = planProviderConfigs(local, b.ProviderConfigs, strategyMirror, &importReport{})
	if s := mustJSON(t, got.Codex); strings.Contains(s, "local-tok") {
		t.Errorf("local secret kept in changed settings: %s", s)
	}
}

func TestLeftOutReportedOnlyWhenBundleWins(t *testing.T) {
	local := agent.SharedConfig{MCPServers: []agent.MCPServer{{Name: "gh", Command: "srv", Env: map[string]string{"T": "v"}}}}
	for _, tc := range []struct {
		strategy importStrategy
		want     bool
	}{{strategyMerge, false}, {strategyMirror, true}} {
		v := newTestVault(t)
		if err := v.SetSharedConfig(local); err != nil {
			t.Fatal(err)
		}
		b := SetupBundle{SharedConfig: agent.SharedConfig{MCPServers: []agent.MCPServer{{Name: "gh", Command: "evil", Env: map[string]string{"T": "v"}}}}}
		redactVaultSecrets(&b)
		plan := planProfileImport(v, b, tc.strategy, time.Now())
		if got := strings.Contains(mustJSON(t, plan.Report), "gh env.T"); got != tc.want {
			t.Errorf("%v: Left out reported=%v, want %v: %s", tc.strategy, got, tc.want, mustJSON(t, plan.Report))
		}
		la := agent.Agent{Name: "a", BaseURL: "https://u:p@h.example"}
		ba := SetupBundle{Agents: []agent.Agent{la}}
		redactVaultSecrets(&ba)
		ba.Agents[0].Provider = agent.ProviderCustom
		rep := &importReport{}
		planAgentOps([]agent.Agent{la}, ba.Agents, tc.strategy, la.CreatedAt, rep)
		if got := strings.Contains(mustJSON(t, rep), "base_url"); got != tc.want {
			t.Errorf("%v: agent Left out reported=%v, want %v: %s", tc.strategy, got, tc.want, mustJSON(t, rep))
		}
	}
}

func TestPlanProviderConfigsKeepsNoOllamaSecretForMovedBaseURL(t *testing.T) {
	local := agent.ProviderConfig{Ollama: &agent.OllamaConfig{BaseURL: "http://gpu.example:11434", CustomSettings: map[string]any{"token": "local-ol"}}}
	b := SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	b.ProviderConfigs.Ollama.BaseURL = "http://evil.example:11434"
	got, _ := planProviderConfigs(local, b.ProviderConfigs, strategyMirror, &importReport{})
	if s := mustJSON(t, got.Ollama); strings.Contains(s, "local-ol") {
		t.Errorf("local Ollama secret kept for a moved base URL: %s", s)
	}
}
