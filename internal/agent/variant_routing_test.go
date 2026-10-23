package agent

import (
	"strings"
	"testing"
)

func tiered(name, tier string, def bool) Agent {
	return Agent{Name: name, Provider: ProviderClaude, Variant: &AgentVariant{Group: "g", Default: def, Tier: tier}}
}

func names(as []Agent) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name)
	}
	return strings.Join(out, ",")
}

func TestParseVariantTierAndPolicy(t *testing.T) {
	for _, ok := range []string{"", "simple", "Standard", " deep "} {
		if _, err := ParseVariantTier(ok); err != nil {
			t.Errorf("ParseVariantTier(%q) = %v", ok, err)
		}
	}
	if _, err := ParseVariantTier("huge"); err == nil {
		t.Error("ParseVariantTier(huge): want error")
	}
	if p, err := ParseVariantPolicy(""); err != nil || p != VariantPolicyDefault {
		t.Errorf("ParseVariantPolicy(\"\") = %q, %v", p, err)
	}
	if _, err := ParseVariantPolicy("cheap"); err == nil {
		t.Error("ParseVariantPolicy(cheap): want error")
	}
}

func TestVariantTargetTier(t *testing.T) {
	for _, tc := range []struct{ policy, in, want string }{
		{VariantPolicyBalanced, "simple", "simple"},
		{VariantPolicyBalanced, "deep", "deep"},
		{VariantPolicyCostFirst, "deep", "standard"},
		{VariantPolicyCostFirst, "standard", "simple"},
		{VariantPolicyCostFirst, "simple", "simple"},
		{VariantPolicyQualityFirst, "simple", "standard"},
		{VariantPolicyQualityFirst, "standard", "deep"},
		{VariantPolicyQualityFirst, "deep", "deep"},
	} {
		if got := VariantTargetTier(tc.policy, tc.in); got != tc.want {
			t.Errorf("VariantTargetTier(%s, %s) = %s, want %s", tc.policy, tc.in, got, tc.want)
		}
	}
}

func TestVariantCandidatesTable(t *testing.T) {
	full := []Agent{tiered("s", "simple", false), tiered("m", "standard", true), tiered("d", "deep", false), tiered("u", "", false)}
	// Only simple and deep: standard prompts are a tie between them.
	gap := []Agent{tiered("s", "simple", false), tiered("d", "deep", false), tiered("u", "", true)}
	// Two members in one tier: the default first, then by name.
	twin := []Agent{tiered("b", "deep", false), tiered("c", "deep", true), tiered("a", "deep", false)}
	untiered := []Agent{tiered("y", "", false), tiered("x", "", false), tiered("z", "", true)}
	for _, tc := range []struct {
		name   string
		agents []Agent
		policy string
		tier   string
		want   string
	}{
		{"default policy ignores tiers", full, VariantPolicyDefault, "deep", "m"},
		{"unset policy is default", full, "", "simple", "m"},
		{"unknown policy is default", full, "fancy", "simple", "m"},
		{"balanced simple", full, VariantPolicyBalanced, "simple", "s,m,d,u"},
		{"balanced standard", full, VariantPolicyBalanced, "standard", "m,s,d,u"},
		{"balanced deep", full, VariantPolicyBalanced, "deep", "d,m,s,u"},
		{"cost-first deep", full, VariantPolicyCostFirst, "deep", "m,s,d,u"},
		{"cost-first standard", full, VariantPolicyCostFirst, "standard", "s,m,d,u"},
		{"cost-first simple", full, VariantPolicyCostFirst, "simple", "s,m,d,u"},
		{"quality-first simple", full, VariantPolicyQualityFirst, "simple", "m,d,s,u"},
		{"quality-first standard", full, VariantPolicyQualityFirst, "standard", "d,m,s,u"},
		{"balanced tie goes lower", gap, VariantPolicyBalanced, "standard", "s,d,u"},
		{"cost-first tie goes lower", gap, VariantPolicyCostFirst, "deep", "s,d,u"},
		{"quality-first tie goes higher", gap, VariantPolicyQualityFirst, "simple", "d,s,u"},
		{"same tier: default then name", twin, VariantPolicyBalanced, "deep", "c,a,b"},
		{"untiered only: default then name", untiered, VariantPolicyBalanced, "deep", "z,x,y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(VariantCandidates(tc.agents, "g", tc.policy, tc.tier)); got != tc.want {
				t.Fatalf("candidates = %s, want %s", got, tc.want)
			}
		})
	}
	if got := VariantCandidates(full, "nope", VariantPolicyBalanced, "deep"); got != nil {
		t.Fatalf("unknown group: %v", got)
	}
}

func TestSetVariantPolicyAndNormalizeInherit(t *testing.T) {
	agents := []Agent{tiered("a", "simple", true), tiered("b", "deep", false)}
	if err := SetVariantPolicy(agents, "g", VariantPolicyBalanced); err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		if a.Variant.Policy != VariantPolicyBalanced {
			t.Fatalf("%s policy = %q", a.Name, a.Variant.Policy)
		}
	}
	if err := SetVariantPolicy(agents, "missing", VariantPolicyBalanced); err == nil {
		t.Fatal("missing group: want error")
	}
	if err := SetVariantPolicy(agents, "g", "cheap"); err == nil {
		t.Fatal("bad policy: want error")
	}
	// A member that joins without a policy inherits the group's.
	agents = append(agents, tiered("c", "", false))
	NormalizeVariantDefaults(agents, "c")
	if agents[2].Variant.Policy != VariantPolicyBalanced || VariantPolicyOf(agents, "g") != VariantPolicyBalanced {
		t.Fatalf("joined member policy = %q", agents[2].Variant.Policy)
	}
	// default is stored as empty on every member.
	if err := SetVariantPolicy(agents, "g", VariantPolicyDefault); err != nil {
		t.Fatal(err)
	}
	if p := VariantPolicyOf(agents, "g"); p != "" {
		t.Fatalf("policy after default = %q", p)
	}
}

func TestVariantCredentialProblem(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	gw := func(name string) bool { return name == "corp" }
	for _, tc := range []struct {
		name string
		a    Agent
		env  map[string]string
		want string
	}{
		{"anthropic api_key mode no key", Agent{Provider: ProviderClaude, ProviderMeta: &AgentProviderMeta{AuthMode: AuthModeAPIKey}}, nil, "no anthropic API key"},
		{"anthropic no key, CLI login possible", Agent{Provider: ProviderClaude}, nil, ""},
		{"anthropic env key", Agent{Provider: ProviderClaude}, map[string]string{"ANTHROPIC_API_KEY": "k"}, ""},
		{"anthropic agent key", Agent{Provider: ProviderClaude, APIKey: "k"}, nil, ""},
		{"anthropic oauth", Agent{Provider: ProviderClaude, ProviderMeta: &AgentProviderMeta{AuthMode: AuthModeOAuth}}, nil, ""},
		{"bedrock no region", Agent{Provider: ProviderClaude, Backend: "bedrock"}, nil, "no AWS region"},
		{"bedrock env region", Agent{Provider: ProviderClaude, Backend: "bedrock"}, map[string]string{"AWS_REGION": "us-east-1"}, ""},
		{"vertex missing", Agent{Provider: ProviderClaude, Backend: "vertex"}, nil, "needs a region and a project"},
		{"vertex ok", Agent{Provider: ProviderClaude, Backend: "vertex", ProviderMeta: &AgentProviderMeta{VertexRegion: "us-east5", VertexProject: "my-project-1"}}, nil, ""},
		{"gateway missing stored", Agent{Provider: ProviderClaude, Backend: "gateway", ProviderMeta: &AgentProviderMeta{GatewayName: "gone"}}, nil, `stored gateway "gone" not found`},
		{"gateway no url", Agent{Provider: ProviderClaude, Backend: "gateway"}, nil, "no base URL"},
		{"gateway stored ok", Agent{Provider: ProviderClaude, Backend: "gateway", BaseURL: "https://gw.example.com", ProviderMeta: &AgentProviderMeta{GatewayName: "corp"}}, nil, ""},
		{"not claude", Agent{Provider: ProviderCodex}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env = tc.env
			got := VariantCredentialProblem(tc.a, getenv, gw)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("problem = %q, want %q", got, tc.want)
			}
		})
	}
}
