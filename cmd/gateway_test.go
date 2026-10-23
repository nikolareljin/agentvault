package cmd

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func storedGateway(t *testing.T, cfgDir, name string) agent.Gateway {
	t.Helper()
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	g, ok := v.GetGateway(name)
	if !ok {
		t.Fatalf("gateway %s not stored", name)
	}
	return g
}

func addTestGateway(t *testing.T, name string, flags map[string]string) {
	t.Helper()
	if _, err := runPricing(t, gatewayAddCmd, []string{name}, flags); err != nil {
		t.Fatalf("add: %v", err)
	}
}

func TestGatewayAddReadsSecretsFromEnv(t *testing.T) {
	cfgDir := scopeTestVault(t)
	t.Setenv(gatewayTokenInputEnv, "tok-from-env")
	t.Setenv(gatewayPasswordInputEnv, "pw-from-env")
	addTestGateway(t, "lab", map[string]string{"url": "https://llm.example.com", "basic-auth-user": "me"})
	if g := storedGateway(t, cfgDir, "lab"); g.Token != "tok-from-env" || g.BasicAuth() != "me:pw-from-env" {
		t.Errorf("stored = %+v", g)
	}
	// A flag still wins over the environment.
	addTestGateway(t, "lab2", map[string]string{"url": "https://llm.example.com", "token": "tok-flag"})
	if g := storedGateway(t, cfgDir, "lab2"); g.Token != "tok-flag" {
		t.Errorf("token = %q, want the flag's", g.Token)
	}
}

func TestGatewayAddListNoSecrets(t *testing.T) {
	cfgDir := scopeTestVault(t)
	t.Setenv(gatewayTokenInputEnv, "")
	t.Setenv(gatewayPasswordInputEnv, "")
	addTestGateway(t, "lab", map[string]string{"url": "https://llm.example.com", "token": "tok-secret-1",
		"basic-auth-user": "me", "basic-auth-password": "pw-secret-1"})
	if _, err := runPricing(t, gatewayAddCmd, []string{"lab"}, map[string]string{"url": "https://other.example", "generate": "true"}); err == nil {
		t.Error("duplicate add succeeded")
	}
	for _, bad := range []map[string]string{
		{"url": "ftp://x.example", "token": "t"},
		{"url": "https://u:p@x.example", "token": "t"},
		{"url": "https://x.example"},
		{"url": "https://x.example", "basic-auth-user": "me"},
		{"url": "https://x.example", "token": "t", "generate": "true"},
	} {
		if _, err := runPricing(t, gatewayAddCmd, []string{"bad"}, bad); err == nil {
			t.Errorf("add %v succeeded", bad)
		}
	}
	if _, err := runPricing(t, gatewayAddCmd, []string{"x\x1b[31my"}, map[string]string{"url": "https://x.example", "token": "t"}); err == nil {
		t.Error("add with an escape in the name succeeded")
	}
	out, err := runPricing(t, gatewayListCmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("list printed a secret:\n%s", out)
	}
	if !strings.Contains(out, "lab") || !strings.Contains(out, "https://llm.example.com") || !strings.Contains(out, "me") {
		t.Errorf("list output:\n%s", out)
	}
	if g := storedGateway(t, cfgDir, "lab"); g.Token != "tok-secret-1" || g.BasicAuth() != "me:pw-secret-1" {
		t.Errorf("stored = %+v", g)
	}
	// Gateways are not agents.
	out, err = runPricing(t, listCmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "lab") {
		t.Errorf("agentvault list shows the gateway:\n%s", out)
	}
	if _, err := runPricing(t, gatewayRemoveCmd, []string{"lab"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, gatewayRemoveCmd, []string{"lab"}, nil); err == nil {
		t.Error("second remove succeeded")
	}
}

func TestGatewayRotateKeepsOnePrevious(t *testing.T) {
	cfgDir := scopeTestVault(t)
	addTestGateway(t, "lab", map[string]string{"url": "https://llm.example.com", "token": "first"})
	out, err := runPricing(t, gatewayRotateCmd, []string{"lab"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := storedGateway(t, cfgDir, "lab")
	if g.Token == "first" || len(g.Token) != 43 || g.PreviousToken != "first" || g.RotatedAt.IsZero() {
		t.Errorf("after rotate: %+v", g)
	}
	if strings.Contains(out, g.Token) || !strings.Contains(out, "gateway env lab") {
		t.Errorf("rotate output:\n%s", out)
	}
	second := g.Token
	if _, err := runPricing(t, gatewayRotateCmd, []string{"lab"}, nil); err != nil {
		t.Fatal(err)
	}
	g = storedGateway(t, cfgDir, "lab")
	if g.PreviousToken != second || g.Token == second || g.Token == "first" {
		t.Errorf("second rotate: %+v", g)
	}
	if _, err := runPricing(t, gatewayRotateCmd, []string{"nope"}, nil); err == nil {
		t.Error("rotate of a missing gateway succeeded")
	}
}

func TestGatewayEnv(t *testing.T) {
	scopeTestVault(t)
	addTestGateway(t, "lab", map[string]string{"url": "https://llm.example.com", "token": "it's",
		"basic-auth-user": "me", "basic-auth-password": "p w"})
	out, err := runPricing(t, gatewayEnvCmd, []string{"lab"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "export OLLAMA_TOKEN='it'\\''s'\nexport BASIC_AUTH='me:p w'\n"
	if out != want {
		t.Errorf("env = %q, want %q", out, want)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		got, err := exec.Command(sh, "-c", out+`printf '%s|%s' "$OLLAMA_TOKEN" "$BASIC_AUTH"`).Output()
		if err != nil || string(got) != "it's|me:p w" {
			t.Errorf("sh read %q, %v", got, err)
		}
	}
	if _, err := runPricing(t, gatewayEnvCmd, []string{"lab"}, map[string]string{"previous": "true"}); err == nil {
		t.Error("--previous without a previous token succeeded")
	}
	if _, err := runPricing(t, gatewayRotateCmd, []string{"lab"}, nil); err != nil {
		t.Fatal(err)
	}
	// dotenv has no form for a single quote: env fails and writes nothing,
	// so `env --format dotenv > .env` cannot leave a .env without the token.
	out, err = runPricing(t, gatewayEnvCmd, []string{"lab"}, map[string]string{"previous": "true", "format": "dotenv"})
	if err == nil || !strings.Contains(err.Error(), "OLLAMA_TOKEN cannot be written as dotenv") || strings.Contains(out, "BASIC_AUTH") {
		t.Errorf("dotenv --previous = %q, %v", out, err)
	}
	out, err = runPricing(t, gatewayEnvCmd, []string{"lab"}, map[string]string{"format": "dotenv"})
	if err != nil || !strings.HasPrefix(out, "OLLAMA_TOKEN='") || !strings.Contains(out, "BASIC_AUTH='me:p w'\n") {
		t.Errorf("dotenv = %q, %v", out, err)
	}
	if _, err := runPricing(t, gatewayEnvCmd, []string{"nope"}, nil); err == nil {
		t.Error("env of a missing gateway succeeded")
	}
	// A user without a password, as an export without keys leaves it, is not basic auth.
	if g := (agent.Gateway{BasicAuthUser: "me"}); g.BasicAuth() != "" {
		t.Errorf("BasicAuth() = %q for a user without a password", g.BasicAuth())
	}
	if _, err := runPricing(t, gatewayEnvCmd, []string{"lab"}, map[string]string{"format": "json"}); err == nil {
		t.Error("unknown format accepted")
	}
}

func TestGatewayVaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := vault.New(path)
	if err := v.Init("test-password"); err != nil {
		t.Fatal(err)
	}
	g := agent.Gateway{Name: "lab", URL: "https://llm.example.com", Token: "t", PreviousToken: "p",
		BasicAuthUser: "u", BasicAuthPassword: "pw", RotatedAt: time.Unix(1700000000, 0).UTC()}
	if err := v.AddGateway(g); err != nil {
		t.Fatal(err)
	}
	r := vault.New(path)
	if err := r.Unlock("test-password"); err != nil {
		t.Fatal(err)
	}
	got, ok := r.GetGateway("lab")
	if !ok || got != g {
		t.Errorf("round trip = %+v, want %+v", got, g)
	}
	// An older reader without the field still decodes the vault data.
	data, _ := r.ExportData()
	var old struct {
		Agents []agent.Agent `json:"agents"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		t.Errorf("older layout decode: %v", err)
	}
}

func TestGatewayExportFollowsIncludeKeys(t *testing.T) {
	v, root := sessionTestVault(t, nil)
	if err := v.AddGateway(agent.Gateway{Name: "lab", URL: "https://llm.example.com", Token: "tok-x",
		PreviousToken: "prev-x", BasicAuthUser: "me", BasicAuthPassword: "pw-x"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, config.AppName)
	res, err := collectSetupBundle(exportCmd, v, dir, setupCollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s := mustJSON(t, res.Bundle.Gateways); strings.Contains(s, "-x") || !strings.Contains(s, `"me"`) {
		t.Errorf("export without keys: %s", s)
	}
	if g, _ := v.GetGateway("lab"); g.Token != "tok-x" {
		t.Error("export changed the vault's gateway")
	}
	res, err = collectSetupBundle(exportCmd, v, dir, setupCollectOptions{IncludeKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	if g := res.Bundle.Gateways; len(g) != 1 || g[0].Token != "tok-x" || g[0].BasicAuthPassword != "pw-x" {
		t.Errorf("export with keys: %+v", g)
	}
}

func TestGatewayImportStrategies(t *testing.T) {
	local := agent.Gateway{Name: "lab", URL: "https://llm.example.com", Token: "local-tok", BasicAuthUser: "me", BasicAuthPassword: "local-pw"}
	blanked := local
	blankGatewaySecrets(&blanked)
	newGw := agent.Gateway{Name: "new", URL: "https://n.example", Token: "n-tok"}
	for _, tc := range []struct {
		name     string
		strategy importStrategy
		in       []agent.Gateway
		want     map[string]string // name -> token
		leftOut  bool
	}{
		{"merge adds, keeps local", strategyMerge, []agent.Gateway{{Name: "lab", URL: "https://evil.example", Token: "b"}, newGw}, map[string]string{"lab": "local-tok", "new": "n-tok"}, false},
		{"replace blank same URL keeps local", strategyReplace, []agent.Gateway{blanked}, map[string]string{"lab": "local-tok"}, false},
		{"replace blank moved URL drops", strategyReplace, []agent.Gateway{{Name: "lab", URL: "https://evil.example"}}, map[string]string{"lab": ""}, true},
		{"mirror removes local-only", strategyMirror, []agent.Gateway{newGw}, map[string]string{"new": "n-tok"}, false},
		{"mirror keeps local on a bad entry", strategyMirror, []agent.Gateway{{Name: "lab", URL: "ftp://x.example", Token: "b"}}, map[string]string{"lab": "local-tok"}, false},
		{"escape in name not added", strategyMerge, []agent.Gateway{{Name: "e\x1b]0;x\a", URL: "https://x.example", Token: "b"}}, map[string]string{"lab": "local-tok"}, false},
		{"bad entry not added", strategyMerge, []agent.Gateway{{Name: "bad", URL: "https://u:p@x.example", Token: "b"}}, map[string]string{"lab": "local-tok"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVault(t)
			if err := v.AddGateway(local); err != nil {
				t.Fatal(err)
			}
			plan := planProfileImport(v, SetupBundle{Gateways: tc.in}, tc.strategy, time.Now(), importConsent{})
			if err := commitProfileImport(v, plan); err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, g := range v.Gateways() {
				got[g.Name] = g.Token
			}
			if mustJSON(t, got) != mustJSON(t, tc.want) {
				t.Errorf("gateways = %v, want %v", got, tc.want)
			}
			if lo := strings.Contains(mustJSON(t, plan.Report), "gateway lab token"); lo != tc.leftOut {
				t.Errorf("Left out = %v: %s", lo, mustJSON(t, plan.Report))
			}
		})
	}
	// Re-importing the local gateway, blanked, changes nothing.
	v := newTestVault(t)
	if err := v.AddGateway(local); err != nil {
		t.Fatal(err)
	}
	if plan := planProfileImport(v, SetupBundle{Gateways: []agent.Gateway{blanked}}, strategyMirror, time.Now(), importConsent{}); plan.GatewaysChanged {
		t.Errorf("unchanged gateway rewritten: %+v", plan.Gateways)
	}
}
