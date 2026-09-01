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

	got := planSessions(agent.SessionConfig{}, incoming, strategyMerge, now, &rep)
	if len(got.Sessions) != 1 {
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

	got := planSessions(existing, incoming, strategyReplace, time.Now(), &rep)
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

	got := planSessions(existing, incoming, strategyMirror, time.Now(), &rep)
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

func TestPlanProfileImportMergesPricing(t *testing.T) {
	v := newTestVault(t)
	if err := v.SetSharedConfig(agent.SharedConfig{
		Pricing: []agent.ProviderPricing{{Provider: agent.ProviderClaude, InputPer1KTokens: 1}},
	}); err != nil {
		t.Fatalf("SetSharedConfig() error = %v", err)
	}

	setup := SetupBundle{SharedConfig: agent.SharedConfig{Pricing: []agent.ProviderPricing{
		{Provider: agent.ProviderClaude, InputPer1KTokens: 2},
		{Provider: agent.ProviderOllama, InputPer1KTokens: 0},
	}}}

	plan := planProfileImport(v, setup, strategyReplace, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}
	pricing := v.SharedConfig().Pricing
	if len(pricing) != 2 {
		t.Fatalf("pricing = %#v, want both rows after a replace import", pricing)
	}
	if pricing[0].InputPer1KTokens != 2 {
		t.Fatalf("pricing[0].InputPer1KTokens = %v, want the bundle rate", pricing[0].InputPer1KTokens)
	}
}

func TestPricingKeySeparatesModelPatterns(t *testing.T) {
	a := pricingKey(agent.ProviderPricing{Provider: agent.ProviderClaude, ModelPattern: "opus"})
	b := pricingKey(agent.ProviderPricing{Provider: agent.ProviderClaude, ModelPattern: "sonnet"})
	if a == b {
		t.Fatal("pricingKey() collapsed two model patterns into one key")
	}
}

func TestPlanProfileImportKeepsLocalPromptSessions(t *testing.T) {
	v := newTestVault(t)
	if err := v.SetSharedConfig(agent.SharedConfig{
		PromptSessions: []agent.PromptSession{{ID: "local-history", AgentName: "alpha"}},
	}); err != nil {
		t.Fatalf("SetSharedConfig() error = %v", err)
	}

	// A bundle never carries prompt sessions, so even mirror must leave them alone.
	plan := planProfileImport(v, SetupBundle{}, strategyMirror, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}
	sessions := v.SharedConfig().PromptSessions
	if len(sessions) != 1 || sessions[0].ID != "local-history" {
		t.Fatalf("prompt sessions = %#v, want local run history untouched", sessions)
	}
}

func TestWithoutPromptSessionsStripsTranscripts(t *testing.T) {
	sc := agent.SharedConfig{
		SystemPrompt: "keep me",
		PromptSessions: []agent.PromptSession{{
			ID:      "s1",
			Entries: []agent.PromptTranscriptEntry{{Prompt: "secret question"}},
		}},
	}
	got := withoutPromptSessions(sc)
	if got.PromptSessions != nil {
		t.Fatalf("PromptSessions = %#v, want nil so transcripts never reach a bundle", got.PromptSessions)
	}
	if got.SystemPrompt != "keep me" {
		t.Fatalf("SystemPrompt = %q, want the rest of the config preserved", got.SystemPrompt)
	}
}

func TestPlanProfileImportSkipsWritesWhenNothingChanges(t *testing.T) {
	v := newTestVault(t)
	if err := v.Add(agent.Agent{Name: "alpha", Provider: agent.ProviderClaude}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// A bundle carrying exactly what the vault already holds must plan no writes.
	setup := SetupBundle{Agents: []agent.Agent{{Name: "alpha", Provider: agent.ProviderClaude}}}
	plan := planProfileImport(v, setup, strategyMerge, time.Now())

	if plan.SharedChanged {
		t.Error("SharedChanged = true, want no shared-config write for an unchanged import")
	}
	if plan.SessionsChanged {
		t.Error("SessionsChanged = true, want no session write for an unchanged import")
	}
	if plan.CapsChanged {
		t.Error("CapsChanged = true, want no capability write for an unchanged import")
	}
	if plan.ProviderChanged {
		t.Error("ProviderChanged = true, want no provider-config write for an unchanged import")
	}
}

func TestCommitProfileImportLeavesParallelLimitUnset(t *testing.T) {
	v := newTestVault(t)
	if v.Sessions().ParallelLimitSet {
		t.Fatal("fresh vault reports ParallelLimitSet, cannot test the regression")
	}

	// Importing a bundle with no session data must not mark the parallel limit as
	// explicitly configured, which Vault.SetSessions would do on any write.
	plan := planProfileImport(v, SetupBundle{}, strategyMerge, time.Now())
	if err := commitProfileImport(v, plan); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}
	if v.Sessions().ParallelLimitSet {
		t.Fatal("ParallelLimitSet = true after an import that carried no session config")
	}
}

func TestMergeKeyedMirrorReturnsNilWhenEverythingRemoved(t *testing.T) {
	type item struct{ Name string }
	rep := importReport{}
	got := mergeKeyed([]item{{"gone"}}, nil, func(i item) string { return i.Name }, strategyMirror, "rule", &rep)
	if got != nil {
		t.Fatalf("mergeKeyed() = %#v, want nil so an emptied collection stays deep-equal to an unset one", got)
	}
}

func TestPlanAgentOpsReplaceSkipsIdenticalAgent(t *testing.T) {
	rep := importReport{}
	existing := agent.Agent{Name: "alpha", Provider: agent.ProviderClaude, Model: "opus"}
	ops := planAgentOps([]agent.Agent{existing}, []agent.Agent{existing}, strategyReplace, time.Now(), &rep)
	if len(ops) != 0 {
		t.Fatalf("planAgentOps() = %#v, want no write for an agent that already matches", ops)
	}
	if rep.Updated != 0 {
		t.Fatalf("report.Updated = %d, want 0 for an unchanged agent", rep.Updated)
	}
}

func TestMergeKeyedReplaceSkipsIdenticalItems(t *testing.T) {
	type item struct {
		Name  string
		Value string
	}
	rep := importReport{}
	same := []item{{"a", "same"}}
	got := mergeKeyed(same, same, func(i item) string { return i.Name }, strategyReplace, "item", &rep)
	if len(got) != 1 {
		t.Fatalf("mergeKeyed() = %#v, want the single item retained", got)
	}
	if rep.Updated != 0 || rep.Skipped != 1 {
		t.Fatalf("report = %+v, want the identical item reported as skipped, not updated", rep)
	}
}

func TestInstructionStampPrefersBundleCreationTime(t *testing.T) {
	created := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if got := instructionStamp(SetupBundle{CreatedAt: created}, time.Now()); !got.Equal(created) {
		t.Fatalf("instructionStamp() = %v, want the bundle creation time", got)
	}
	now := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	if got := instructionStamp(SetupBundle{}, now); !got.Equal(now) {
		t.Fatalf("instructionStamp() = %v, want the fallback clock for a bundle with no creation time", got)
	}
}

func TestPlanProfileImportIsIdempotentForInstructionOverrides(t *testing.T) {
	v := newTestVault(t)
	setup := SetupBundle{
		CreatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		InstructionOverrides: []SetupAsset{{
			Kind:                setupAssetKindInstruction,
			LogicalPath:         "AGENTS.md",
			ProjectRelativePath: "AGENTS.md",
			ContentPresent:      true,
			Content:             []byte("# rules"),
		}},
	}

	first := planProfileImport(v, setup, strategyMirror, time.Now())
	if err := commitProfileImport(v, first); err != nil {
		t.Fatalf("commitProfileImport() error = %v", err)
	}
	if len(v.SharedConfig().Instructions) != 1 {
		t.Fatalf("instructions = %#v, want the override imported", v.SharedConfig().Instructions)
	}

	// Re-importing the same bundle later must not rewrite the instruction.
	second := planProfileImport(v, setup, strategyMirror, time.Now().Add(time.Hour))
	if second.SharedChanged {
		t.Fatal("SharedChanged = true on a repeat import, want override-derived instructions to be stable")
	}
	if second.Report.Updated != 0 {
		t.Fatalf("report.Updated = %d on a repeat import, want 0", second.Report.Updated)
	}
}
