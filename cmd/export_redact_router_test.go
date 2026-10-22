package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestSecretStringQueryAndHeaderAssign(t *testing.T) {
	for _, s := range []string{
		"https://h.example/mcp?client_secret=c1",
		"https://h.example/mcp?api-key=c2",
		"https://h.example/mcp?x=1&private_token=c3",
		"https://h.example/cb#access_token=c6",
		"https://h.blob.example/c?sv=1&sig=c4",
		"X-Api-Key=c5",
	} {
		if !secretString(s) {
			t.Errorf("secretString(%q) = false", s)
		}
	}
	for _, s := range []string{"https://h.example/mcp?page=2&keyword=x", "max-tokens=5", "--verbose"} {
		if secretString(s) {
			t.Errorf("secretString(%q) = true", s)
		}
	}
}

func TestRedactSensitiveMapReadsTypedLists(t *testing.T) {
	got := mustJSON(t, redactSensitiveMap(map[string]any{
		"tables": []map[string]any{{"token": "canary-t", "name": "keep-me"}},
		"query":  map[string]any{"query_params": map[string]any{"sig": "canary-sig"}},
	}))
	if strings.Contains(got, "canary") || !strings.Contains(got, "keep-me") {
		t.Errorf("typed list not redacted field by field: %s", got)
	}
}

func TestRedactVaultSecretsBlanksRouterAndExtraHeaders(t *testing.T) {
	b := SetupBundle{
		Agents: []agent.Agent{{Name: "a", ProviderMeta: &agent.AgentProviderMeta{Extra: map[string]string{"headers": "canary-h"}}}},
		SharedConfig: agent.SharedConfig{Router: agent.RouterConfig{Mode: "llm-router",
			LangGraphCmd: "lg --token canary-lg", LLMRouterURL: "https://u:canary-u@h.example",
			LocalAIOllamaURL: "http://h.example:11434/?api-key=canary-q"}},
	}
	redactVaultSecrets(&b)
	got := mustJSON(t, b)
	if strings.Contains(got, "canary") || !strings.Contains(got, "llm-router") {
		t.Errorf("bundle carries a router or extra secret: %s", got)
	}
}

func TestPlanProfileImportRestoresRouterSecrets(t *testing.T) {
	local := agent.SharedConfig{Router: agent.RouterConfig{Mode: "llm-router",
		LLMRouterURL: "https://u:local-p@h.example", LLMRouterModel: "m"}}
	for _, s := range []importStrategy{strategyMirror, strategyMerge, strategyReplace} {
		v := newTestVault(t)
		if err := v.SetSharedConfig(local); err != nil {
			t.Fatal(err)
		}
		b := SetupBundle{SharedConfig: local}
		redactVaultSecrets(&b)
		plan := planProfileImport(v, b, s, time.Now(), importConsent{})
		if plan.Shared.Router != local.Router || strings.Contains(mustJSON(t, plan.Report), "Left out") {
			t.Errorf("%v: same-machine round trip lost the router secret: %+v %v", s, plan.Shared.Router, plan.Report.Lines)
		}
	}

	// A bundle that changes the model keeps its blank: the URL is not filled.
	v := newTestVault(t)
	if err := v.SetSharedConfig(local); err != nil {
		t.Fatal(err)
	}
	b := SetupBundle{SharedConfig: local}
	redactVaultSecrets(&b)
	b.SharedConfig.Router.LLMRouterModel = "other"
	plan := planProfileImport(v, b, strategyMirror, time.Now(), importConsent{})
	if strings.Contains(mustJSON(t, plan.Shared), "local-p") || !strings.Contains(mustJSON(t, plan.Report), "llm_router_url") {
		t.Errorf("changed router got the local secret or no Left out line: %+v %v", plan.Shared.Router, plan.Report.Lines)
	}
}

func TestPlanProfileImportNamesBlanksOnFreshMachine(t *testing.T) {
	src := SetupBundle{
		Agents: []agent.Agent{{Name: "a", Provider: agent.ProviderCustom, BaseURL: "https://gw.example/v1?api-key=k",
			ProviderMeta: &agent.AgentProviderMeta{Extra: map[string]string{"endpoint": "https://h/?sig=s", "region": "eu"}}}},
		SharedConfig: agent.SharedConfig{Router: agent.RouterConfig{Mode: "llm-router", LLMRouterURL: "https://u:p@r.example"}},
		ProviderConfigs: agent.ProviderConfig{
			Codex:  &agent.CodexConfig{CustomSettings: map[string]any{"model_providers": map[string]any{"az": map[string]any{"base_url": "https://az.example", "http_headers": map[string]any{"X": "v"}}}}},
			Ollama: &agent.OllamaConfig{BaseURL: "http://u:p@gpu.example:11434", CustomSettings: map[string]any{"token": "t"}},
		},
	}
	redactVaultSecrets(&src)
	plan := planProfileImport(newTestVault(t), src, strategyMirror, time.Now(), importConsent{})
	r := strings.Join(plan.Report.Lines, "\n")
	for _, want := range []string{
		"Left out: agent a base_url:", "Left out: agent a provider_meta.extra.endpoint:",
		"Left out: shared router config llm_router_url:", "Left out: Ollama provider config base_url:",
		"Left out: Codex provider config customSettings.model_providers:", "Left out: Ollama provider config customSettings.token:",
	} {
		if strings.Count(r, want) != 1 {
			t.Errorf("want one %q line:\n%s", want, r)
		}
	}
	for _, op := range plan.AgentOps {
		if _, ok := op.Agent.ProviderMeta.Extra["endpoint"]; ok {
			t.Errorf("blank extra written: %+v", op.Agent.ProviderMeta.Extra)
		}
	}

	// A local agent with a plain base URL differs: its blank is named too.
	v := newTestVault(t)
	if err := v.Add(agent.Agent{Name: "a", Provider: agent.ProviderCustom, BaseURL: "https://plain.example"}); err != nil {
		t.Fatal(err)
	}
	plan = planProfileImport(v, src, strategyMirror, time.Now(), importConsent{})
	if r := strings.Join(plan.Report.Lines, "\n"); strings.Count(r, "Left out: agent a base_url:") != 1 {
		t.Errorf("changed agent's blank base URL not named once:\n%s", r)
	}
}

// A DSN with a password and no scheme is a credential; an scp remote, an
// image digest, a mailto address and a Windows path are not.
func TestSecretStringSchemelessUserinfo(t *testing.T) {
	for s, want := range map[string]bool{
		"root:pw@tcp(db:3306)/app":                         true,
		"user:pw@db.example:5432/app":                      true,
		"--dsn=user:pw@host/db":                            true,
		":pw@redis:6379":                                   true,
		"git@github.com:o/r.git":                           false,
		"node:20@sha256:abc":                               false,
		"mailto:me@example.org":                            false,
		"npx -y @scope/pkg@1.2.3":                          false,
		"Bash(git status:*)":                               false,
		"c:pw@db":                                          true,
		`C:\Users\me\npm\node_modules\@scope\pkg\index.js`: false,
		`C:\a\@x;root:pw@tcp(db:3306)/app`:                 true,
		`C:\a\@x root:pw@db`:                               true,
		"mailto:a@b.example,root:pw@db":                    true,
		"node:20@sha256:abc;root:pw@db":                    true,
		`C:\a:pw@db`:                                       true,
		`--root=D:\work\@app`:                              false,
	} {
		if got := secretString(s); got != want {
			t.Errorf("secretString(%q) = %v, want %v", s, got, want)
		}
	}
}
