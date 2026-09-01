package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func TestParseImportStrategy(t *testing.T) {
	for _, in := range []string{"merge", "MERGE", " replace ", "mirror"} {
		if _, err := parseImportStrategy(in); err != nil {
			t.Errorf("parseImportStrategy(%q) error = %v, want nil", in, err)
		}
	}
	if _, err := parseImportStrategy("overwrite"); err == nil {
		t.Fatal("parseImportStrategy(\"overwrite\") error = nil, want an error")
	}
}

func TestImportStrategyBehaviorFlags(t *testing.T) {
	cases := []struct {
		strategy importStrategy
		wins     bool
		deletes  bool
	}{
		{strategyMerge, false, false},
		{strategyReplace, true, false},
		{strategyMirror, true, true},
	}
	for _, tc := range cases {
		if got := tc.strategy.incomingWins(); got != tc.wins {
			t.Errorf("%s.incomingWins() = %v, want %v", tc.strategy, got, tc.wins)
		}
		if got := tc.strategy.deletesLocalOnly(); got != tc.deletes {
			t.Errorf("%s.deletesLocalOnly() = %v, want %v", tc.strategy, got, tc.deletes)
		}
	}
}

func TestMergeKeyedStrategies(t *testing.T) {
	type item struct {
		Name  string
		Value string
	}
	keyOf := func(i item) string { return i.Name }
	existing := []item{{"a", "local"}, {"onlyLocal", "local"}}
	incoming := []item{{"a", "bundle"}, {"b", "bundle"}}

	cases := []struct {
		strategy importStrategy
		want     []item
	}{
		{strategyMerge, []item{{"a", "local"}, {"onlyLocal", "local"}, {"b", "bundle"}}},
		{strategyReplace, []item{{"a", "bundle"}, {"onlyLocal", "local"}, {"b", "bundle"}}},
		{strategyMirror, []item{{"a", "bundle"}, {"b", "bundle"}}},
	}
	for _, tc := range cases {
		rep := importReport{}
		got := mergeKeyed(existing, incoming, keyOf, tc.strategy, "item", &rep)
		if len(got) != len(tc.want) {
			t.Fatalf("mergeKeyed(%s) = %#v, want %#v", tc.strategy, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("mergeKeyed(%s)[%d] = %#v, want %#v", tc.strategy, i, got[i], tc.want[i])
			}
		}
	}
}

func TestMergeKeyedMirrorReportsRemovals(t *testing.T) {
	type item struct{ Name string }
	rep := importReport{}
	mergeKeyed([]item{{"gone"}}, []item{{"kept"}}, func(i item) string { return i.Name }, strategyMirror, "rule", &rep)
	if rep.Removed != 1 {
		t.Fatalf("report.Removed = %d, want 1", rep.Removed)
	}
	joined := strings.Join(rep.Lines, "\n")
	if !strings.Contains(joined, "Removed: rule gone") {
		t.Fatalf("report lines = %q, want a removal line for rule gone", joined)
	}
}

func TestDisplayKeyJoinsCompositeParts(t *testing.T) {
	if got := displayKey("AGENTS.md\x00global\x00"); got != "AGENTS.md / global" {
		t.Fatalf("displayKey() = %q, want %q", got, "AGENTS.md / global")
	}
}

func TestPlanAgentOpsMergeKeepsLocal(t *testing.T) {
	now := time.Now()
	rep := importReport{}
	ops := planAgentOps(
		[]agent.Agent{{Name: "alpha", Model: "local-model"}},
		[]agent.Agent{{Name: "alpha", Model: "bundle-model"}, {Name: "beta"}},
		strategyMerge, now, &rep)

	if len(ops) != 1 || ops[0].Kind != "add" || ops[0].Name != "beta" {
		t.Fatalf("planAgentOps(merge) = %#v, want only an add for beta", ops)
	}
	if rep.Skipped != 1 {
		t.Fatalf("report.Skipped = %d, want 1", rep.Skipped)
	}
}

func TestPlanAgentOpsReplaceKeepsLocalKeyWhenBundleHasNone(t *testing.T) {
	now := time.Now()
	rep := importReport{}
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ops := planAgentOps(
		[]agent.Agent{{Name: "alpha", APIKey: "local-secret", CreatedAt: created}},
		[]agent.Agent{{Name: "alpha", Model: "bundle-model"}},
		strategyReplace, now, &rep)

	if len(ops) != 1 || ops[0].Kind != "update" {
		t.Fatalf("planAgentOps(replace) = %#v, want a single update", ops)
	}
	if ops[0].Agent.APIKey != "local-secret" {
		t.Fatalf("APIKey = %q, want the local key preserved when the bundle carries none", ops[0].Agent.APIKey)
	}
	if ops[0].Agent.Model != "bundle-model" {
		t.Fatalf("Model = %q, want the bundle value", ops[0].Agent.Model)
	}
	if !ops[0].Agent.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want the original creation time preserved", ops[0].Agent.CreatedAt)
	}
}

func TestPlanAgentOpsMirrorRemovesLocalOnly(t *testing.T) {
	rep := importReport{}
	ops := planAgentOps(
		[]agent.Agent{{Name: "alpha"}, {Name: "stale"}},
		[]agent.Agent{{Name: "alpha"}},
		strategyMirror, time.Now(), &rep)

	var removed []string
	for _, op := range ops {
		if op.Kind == "remove" {
			removed = append(removed, op.Name)
		}
	}
	if len(removed) != 1 || removed[0] != "stale" {
		t.Fatalf("removals = %v, want [stale]", removed)
	}
}

func TestPlanSessionsStripsMachineLocalState(t *testing.T) {
	now := time.Now()
	rep := importReport{}
	incoming := agent.SessionConfig{Sessions: []agent.Session{{
		Name:   "review",
		ID:     "sess-1",
		Status: agent.SessionStatusRunning,
		Agents: []agent.SessionAgent{{Name: "alpha", PID: 4242}},
	}}}

	got, changed := planSessions(agent.SessionConfig{}, incoming, strategyMerge, now, &rep)
	if !changed || len(got.Sessions) != 1 {
		t.Fatalf("planSessions() = %#v, want one imported session", got)
	}
	s := got.Sessions[0]
	if s.Status != agent.SessionStatusIdle {
		t.Fatalf("Status = %q, want idle", s.Status)
	}
	if s.Agents[0].PID != 0 {
		t.Fatalf("PID = %d, want 0", s.Agents[0].PID)
	}
}

func TestPlanSessionsKeepsLocalIDOnNameCollision(t *testing.T) {
	rep := importReport{}
	existing := agent.SessionConfig{Sessions: []agent.Session{{Name: "review", ID: "local-id"}}}
	incoming := agent.SessionConfig{Sessions: []agent.Session{{Name: "review", ID: "bundle-id"}}}

	got, _ := planSessions(existing, incoming, strategyReplace, time.Now(), &rep)
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "local-id" {
		t.Fatalf("sessions = %#v, want the local session ID preserved", got.Sessions)
	}
}

func TestPlanSessionsClearsDanglingActiveSession(t *testing.T) {
	rep := importReport{}
	existing := agent.SessionConfig{
		Sessions:      []agent.Session{{Name: "stale", ID: "stale-id"}},
		ActiveSession: "stale-id",
	}
	incoming := agent.SessionConfig{Sessions: []agent.Session{{Name: "review", ID: "review-id"}}}

	got, _ := planSessions(existing, incoming, strategyMirror, time.Now(), &rep)
	if got.ActiveSession != "" {
		t.Fatalf("ActiveSession = %q, want empty once the referenced session is gone", got.ActiveSession)
	}
}

func TestPlanProviderConfigsRespectsStrategy(t *testing.T) {
	existing := agent.ProviderConfig{Claude: &agent.ClaudeConfig{DefaultModel: "local"}}
	incoming := agent.ProviderConfig{Claude: &agent.ClaudeConfig{DefaultModel: "bundle"}}

	rep := importReport{}
	merged, _ := planProviderConfigs(existing, incoming, strategyMerge, &rep)
	if merged.Claude.DefaultModel != "local" {
		t.Fatalf("merge kept %q, want the local Claude config", merged.Claude.DefaultModel)
	}

	rep = importReport{}
	replaced, changed := planProviderConfigs(existing, incoming, strategyReplace, &rep)
	if !changed || replaced.Claude.DefaultModel != "bundle" {
		t.Fatalf("replace kept %q, want the bundle Claude config", replaced.Claude.DefaultModel)
	}
}

func TestPlanProviderConfigsMirrorClearsMissing(t *testing.T) {
	existing := agent.ProviderConfig{Codex: &agent.CodexConfig{DefaultModel: "local"}}
	rep := importReport{}
	got, changed := planProviderConfigs(existing, agent.ProviderConfig{}, strategyMirror, &rep)
	if !changed || got.Codex != nil {
		t.Fatalf("mirror = %#v, want the Codex config dropped", got)
	}
	if rep.Removed != 1 {
		t.Fatalf("report.Removed = %d, want 1", rep.Removed)
	}
}

func TestMergeInstructionSourcesFoldsOverrides(t *testing.T) {
	now := time.Now()
	overrides := []SetupAsset{{
		Kind:                setupAssetKindInstruction,
		LogicalPath:         "AGENTS.md",
		ProjectRelativePath: "AGENTS.md",
		ContentPresent:      true,
		Content:             []byte("# rules"),
	}}
	got := mergeInstructionSources(nil, overrides, now)
	if len(got) != 1 || got[0].Content != "# rules" {
		t.Fatalf("mergeInstructionSources() = %#v, want the override folded in", got)
	}
}

func TestCapabilityKeyNormalizesEndpoint(t *testing.T) {
	a := capabilityKey(agent.ModelCapabilityEntry{EndpointURL: " http://host:11434/ ", ModelName: " llama "})
	b := capabilityKey(agent.ModelCapabilityEntry{EndpointURL: "http://host:11434", ModelName: "llama"})
	if a != b {
		t.Fatalf("capabilityKey mismatch: %q vs %q", a, b)
	}
}

// newTestVault creates an unlocked vault in a temp directory.
func newTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("test-password"); err != nil {
		t.Fatalf("vault.Init() error = %v", err)
	}
	return v
}

func TestPlanAndCommitProfileImportMirrorConverges(t *testing.T) {
	v := newTestVault(t)
	if err := v.Add(agent.Agent{Name: "stale", Provider: agent.ProviderClaude}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := v.SetSharedConfig(agent.SharedConfig{
		SystemPrompt: "old prompt",
		Rules:        []agent.UnifiedRule{{Name: "local-only"}},
	}); err != nil {
		t.Fatalf("SetSharedConfig() error = %v", err)
	}

	setup := SetupBundle{
		Agents: []agent.Agent{{Name: "alpha", Provider: agent.ProviderClaude}},
		SharedConfig: agent.SharedConfig{
			SystemPrompt: "new prompt",
			Rules:        []agent.UnifiedRule{{Name: "shared-rule"}},
		},
	}

	plan := planProfileImport(v, setup, strategyMirror, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}

	agents := v.List()
	if len(agents) != 1 || agents[0].Name != "alpha" {
		t.Fatalf("agents = %#v, want only alpha after a mirror import", agents)
	}
	shared := v.SharedConfig()
	if shared.SystemPrompt != "new prompt" {
		t.Fatalf("SystemPrompt = %q, want the bundle value", shared.SystemPrompt)
	}
	if len(shared.Rules) != 1 || shared.Rules[0].Name != "shared-rule" {
		t.Fatalf("rules = %#v, want only shared-rule", shared.Rules)
	}
}

func TestPlanAndCommitProfileImportMergeIsNonDestructive(t *testing.T) {
	v := newTestVault(t)
	if err := v.Add(agent.Agent{Name: "local", Provider: agent.ProviderClaude, Model: "local-model"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	setup := SetupBundle{Agents: []agent.Agent{
		{Name: "local", Provider: agent.ProviderClaude, Model: "bundle-model"},
		{Name: "incoming", Provider: agent.ProviderClaude},
	}}

	plan := planProfileImport(v, setup, strategyMerge, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}

	got, ok := v.Get("local")
	if !ok || got.Model != "local-model" {
		t.Fatalf("agent local = %#v, want the local model preserved", got)
	}
	if _, ok := v.Get("incoming"); !ok {
		t.Fatal("agent incoming missing, want it added by the merge")
	}
}

func TestCommitProfileImportReplacesCapabilities(t *testing.T) {
	v := newTestVault(t)
	if err := v.AddCapability(agent.ModelCapabilityEntry{
		EndpointURL: "http://local:11434", ModelName: "old", Capabilities: []string{"general"},
	}); err != nil {
		t.Fatalf("AddCapability() error = %v", err)
	}

	setup := SetupBundle{ModelCapabilities: []agent.ModelCapabilityEntry{{
		EndpointURL: "http://local:11434", ModelName: "new", Capabilities: []string{"coding"},
	}}}

	plan := planProfileImport(v, setup, strategyMirror, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}
	caps := v.ListCapabilities()
	if len(caps) != 1 || caps[0].ModelName != "new" {
		t.Fatalf("capabilities = %#v, want only the bundle entry after a mirror import", caps)
	}
}
