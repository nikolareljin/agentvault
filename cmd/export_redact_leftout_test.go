package cmd

import (
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestPlanProviderConfigsNamesUnfilledSecrets(t *testing.T) {
	local := agent.ProviderConfig{
		Claude: &agent.ClaudeConfig{CustomSettings: map[string]any{"env": map[string]any{
			"ANTHROPIC_API_KEY": "local-key", "ANTHROPIC_BASE_URL": "https://proxy.example"}}},
		Codex:  &agent.CodexConfig{CustomSettings: map[string]any{"tools": map[string]any{"api_token": "local-tok"}, "model": "m"}},
		Ollama: &agent.OllamaConfig{BaseURL: "http://gpu.example:11434", CustomSettings: map[string]any{"token": "local-ol"}},
	}
	b := SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	for _, s := range []importStrategy{strategyMirror, strategyMerge} {
		rep := &importReport{}
		planProviderConfigs(local, b.ProviderConfigs, s, rep)
		if r := mustJSON(t, rep); strings.Contains(r, "Left out") {
			t.Errorf("%v: same-machine round trip reports Left out: %s", s, r)
		}
	}

	b = SetupBundle{ProviderConfigs: local}
	redactVaultSecrets(&b)
	cs := b.ProviderConfigs.Claude.CustomSettings
	cs["env"].(map[string]any)["ANTHROPIC_BASE_URL"] = "https://evil.example"
	b.ProviderConfigs.Claude.RedactedFields = []string{"/env/ANTHROPIC_API_KEY"}
	b.ProviderConfigs.Codex.CustomSettings["model"] = "other"
	b.ProviderConfigs.Ollama.BaseURL = "http://evil.example:11434"
	rep := &importReport{}
	planProviderConfigs(local, b.ProviderConfigs, strategyMirror, rep)
	r := mustJSON(t, rep)
	for _, want := range []string{"Claude provider config /env/ANTHROPIC_API_KEY", "Codex provider config customSettings.tools", "Ollama provider config customSettings.token"} {
		if !strings.Contains(r, want) {
			t.Errorf("unfilled secret not named %q: %s", want, r)
		}
	}
	rep = &importReport{}
	planProviderConfigs(local, b.ProviderConfigs, strategyMerge, rep)
	if r := mustJSON(t, rep); strings.Contains(r, "Left out") {
		t.Errorf("merge keeps local configs but reports Left out: %s", r)
	}
}

func TestPlanProviderConfigsNamesNothingForBundleWithSecrets(t *testing.T) {
	local := agent.ProviderConfig{Codex: &agent.CodexConfig{CustomSettings: map[string]any{"tools": map[string]any{"api_token": "local-tok"}}}}
	incoming := agent.ProviderConfig{Codex: &agent.CodexConfig{CustomSettings: map[string]any{"tools": map[string]any{"api_token": "bundle-tok"}}}}
	rep := &importReport{}
	got, _ := planProviderConfigs(local, incoming, strategyMirror, rep)
	if r := mustJSON(t, rep); strings.Contains(r, "Left out") || !strings.Contains(mustJSON(t, got), "bundle-tok") {
		t.Errorf("bundle that carries its own secret: %s", r)
	}
}
