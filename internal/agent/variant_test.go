package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func claudeVariant(name, backend, group string, def bool) Agent {
	return Agent{Name: name, Provider: ProviderClaude, Backend: backend, Variant: &AgentVariant{Group: group, Default: def}}
}

func TestAgentVariantSerialization(t *testing.T) {
	a := claudeVariant("c-vertex", ClaudeBackendVertex, "claude", true)
	a.ProviderMeta = &AgentProviderMeta{VertexRegion: "us-east5", VertexProject: "p1", GatewayName: "gw"}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"variant":{"group":"claude","default":true}`, `"vertex_region":"us-east5"`, `"vertex_project":"p1"`, `"gateway_name":"gw"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json %s lacks %s", raw, want)
		}
	}
	var back Agent
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.VariantGroup() != "claude" || !back.IsVariantDefault() || back.ProviderMeta.VertexProject != "p1" {
		t.Fatalf("round trip = %#v", back)
	}
	y, err := yaml.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var backY Agent
	if err := yaml.Unmarshal(y, &backY); err != nil || !backY.IsVariantDefault() {
		t.Fatalf("yaml round trip = %#v, %v", backY, err)
	}
	// An agent with no variant writes no variant field: old bundles stay byte-equal.
	plain, _ := json.Marshal(Agent{Name: "x", Provider: ProviderClaude})
	if strings.Contains(string(plain), "variant") || strings.Contains(string(plain), "vertex") {
		t.Fatalf("plain agent json %s has variant fields", plain)
	}
	// A non-default member omits default.
	alt, _ := json.Marshal(claudeVariant("c", "", "claude", false))
	if !strings.Contains(string(alt), `"variant":{"group":"claude"}`) {
		t.Fatalf("alternate json %s", alt)
	}
}

func TestNormalizeVariantDefaults(t *testing.T) {
	agents := []Agent{
		claudeVariant("b", "", "g", true),
		claudeVariant("a", "", "g", true),
		claudeVariant("z", "", "h", false),
		claudeVariant("y", "", "h", false),
		{Name: "plain", Provider: ProviderClaude},
	}
	changed, notes := NormalizeVariantDefaults(agents, "")
	if !agents[1].IsVariantDefault() || agents[0].IsVariantDefault() {
		t.Fatalf("two defaults: want first by name (a) kept, got %#v", agents[:2])
	}
	if !agents[3].IsVariantDefault() || agents[2].IsVariantDefault() {
		t.Fatalf("no default: want first by name (y), got %#v", agents[2:4])
	}
	if len(changed) != 2 || len(notes) != 2 {
		t.Fatalf("changed %v notes %v", changed, notes)
	}
	// A preferred default (a new default set by add/edit) clears the old one.
	agents[0].Variant = &AgentVariant{Group: "g", Default: true}
	NormalizeVariantDefaults(agents, "b")
	if !agents[0].IsVariantDefault() || agents[1].IsVariantDefault() {
		t.Fatalf("preferred b should win, got %#v", agents[:2])
	}
	if d, _ := VariantDefault(agents, "g"); d.Name != "b" {
		t.Fatalf("VariantDefault = %s", d.Name)
	}
}

func TestSetVariantDefault(t *testing.T) {
	agents := []Agent{claudeVariant("a", "", "g", true), claudeVariant("b", "", "g", false)}
	if err := SetVariantDefault(agents, "g", "b"); err != nil {
		t.Fatal(err)
	}
	if agents[0].IsVariantDefault() || !agents[1].IsVariantDefault() {
		t.Fatalf("got %#v", agents)
	}
	if err := SetVariantDefault(agents, "g", "nope"); err == nil {
		t.Fatal("want an error for an agent outside the group")
	}
}

func TestClaudeBackendValidation(t *testing.T) {
	cases := []struct {
		name    string
		a       Agent
		wantErr string
	}{
		{"vertex missing project", Agent{Name: "v", Provider: ProviderClaude, Backend: "vertex", ProviderMeta: &AgentProviderMeta{VertexRegion: "us-east5"}}, "region and a project"},
		{"vertex no meta", Agent{Name: "v", Provider: ProviderClaude, Backend: "vertex"}, "region and a project"},
		{"vertex ok", Agent{Name: "v", Provider: ProviderClaude, Backend: "VERTEX", ProviderMeta: &AgentProviderMeta{VertexRegion: "r", VertexProject: "p"}}, ""},
		{"gateway missing url", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway"}, "--base-url or --gateway"},
		{"gateway url creds", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://u:p@gw.example.com"}, "must not carry credentials"},
		{"gateway bad scheme", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway", BaseURL: "ftp://gw.example.com"}, "http:// or https://"},
		{"gateway ok", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://gw.example.com"}, ""},
		{"gateway by name", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway", ProviderMeta: &AgentProviderMeta{GatewayName: "corp"}}, ""},
		{"gateway token auth", Agent{Name: "g", Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://gw.example.com", ProviderMeta: &AgentProviderMeta{AuthMode: AuthModeToken}}, ""},
		{"vertex api key auth", Agent{Name: "v", Provider: ProviderClaude, Backend: "vertex", ProviderMeta: &AgentProviderMeta{AuthMode: AuthModeAPIKey, VertexRegion: "r", VertexProject: "p"}}, "not supported"},
		{"unknown backend", Agent{Name: "u", Provider: ProviderClaude, Backend: "foundry"}, "unknown claude backend"},
		{"bad group", Agent{Name: "u", Provider: ProviderClaude, Variant: &AgentVariant{Group: "a b"}}, "whitespace"},
		{"empty group", Agent{Name: "u", Provider: ProviderClaude, Variant: &AgentVariant{}}, "group name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.a.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestClaudeBackendDiagnostic(t *testing.T) {
	if d := ClaudeBackendDiagnostic("foundry"); !strings.Contains(d, `unknown claude backend "foundry"`) || !strings.Contains(d, "runs as anthropic") {
		t.Fatalf("diagnostic = %q", d)
	}
	for _, b := range append([]string{"", " Gateway "}, ClaudeBackends...) {
		if d := ClaudeBackendDiagnostic(b); d != "" {
			t.Fatalf("diagnostic(%q) = %q, want none", b, d)
		}
	}
	if NormalizeClaudeBackend("foundry") != ClaudeBackendAnthropic {
		t.Fatal("unknown backend must still normalize to anthropic for compatibility")
	}
}

func TestClaudeRuntimeEnv(t *testing.T) {
	vertex := Agent{Provider: ProviderClaude, Backend: "vertex", ProviderMeta: &AgentProviderMeta{VertexRegion: "us-east5", VertexProject: "proj"}}
	env, err := ClaudeRuntimeEnv(vertex, nil)
	if err != nil || env["CLAUDE_CODE_USE_VERTEX"] != "1" || env["CLOUD_ML_REGION"] != "us-east5" || env["ANTHROPIC_VERTEX_PROJECT_ID"] != "proj" {
		t.Fatalf("vertex env = %v, %v", env, err)
	}
	if _, err := ClaudeRuntimeEnv(Agent{Provider: ProviderClaude, Backend: "vertex"}, nil); err == nil {
		t.Fatal("vertex without region/project: want an error")
	}

	gw := Agent{Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://gw.example.com", APIKey: "k1"}
	env, err = ClaudeRuntimeEnv(gw, nil)
	if err != nil || env["ANTHROPIC_BASE_URL"] != "https://gw.example.com" || env["ANTHROPIC_API_KEY"] != "k1" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("gateway env = %v, %v", env, err)
	}
	gw.ProviderMeta = &AgentProviderMeta{AuthMode: AuthModeToken}
	env, _ = ClaudeRuntimeEnv(gw, nil)
	if env["ANTHROPIC_AUTH_TOKEN"] != "k1" || env["ANTHROPIC_API_KEY"] != "" {
		t.Fatalf("gateway token env = %v", env)
	}

	ref := Agent{Provider: ProviderClaude, Backend: "gateway", ProviderMeta: &AgentProviderMeta{GatewayName: "corp"}}
	env, err = ClaudeRuntimeEnv(ref, &Gateway{Name: "corp", URL: "https://corp.example.com", Token: "t1"})
	if err != nil || env["ANTHROPIC_BASE_URL"] != "https://corp.example.com" || env["ANTHROPIC_AUTH_TOKEN"] != "t1" {
		t.Fatalf("stored gateway env = %v, %v", env, err)
	}
	if _, err := ClaudeRuntimeEnv(ref, nil); err == nil || !strings.Contains(err.Error(), `"corp" not found`) {
		t.Fatalf("missing stored gateway: err = %v", err)
	}
	if _, err := ClaudeRuntimeEnv(Agent{Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://u:p@x.example.com"}, nil); err == nil {
		t.Fatal("credentials in the base URL: want an error")
	}

	bedrock := Agent{Provider: ProviderClaude, Backend: "bedrock", ProviderMeta: &AgentProviderMeta{BedrockRegion: "us-west-2", AWSProfile: "dev"}}
	env, _ = ClaudeRuntimeEnv(bedrock, nil)
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["AWS_REGION"] != "us-west-2" || env["AWS_PROFILE"] != "dev" {
		t.Fatalf("bedrock env = %v", env)
	}
	env, _ = ClaudeRuntimeEnv(Agent{Provider: ProviderClaude}, nil)
	if len(env) != 0 {
		t.Fatalf("anthropic env = %v, want none", env)
	}
}
