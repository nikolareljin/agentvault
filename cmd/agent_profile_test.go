package cmd

import (
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func testAgent() agent.Agent {
	return agent.Agent{
		Name:     "test-claude",
		Provider: agent.ProviderClaude,
		Model:    "claude-sonnet-4-6",
		Backend:  agent.ClaudeBackendAnthropic,
		ProviderMeta: &agent.AgentProviderMeta{
			AuthMode: agent.AuthModeAPIKey,
		},
		CreatedAt: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
	}
}

func TestMarshalUnmarshalAgentProfile_JSON(t *testing.T) {
	a := testAgent()
	data, err := marshalAgentProfile(a, "json")
	if err != nil {
		t.Fatalf("marshalAgentProfile(json) error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshalAgentProfile(json) returned empty data")
	}

	got, ver, err := unmarshalAgentProfile(data, "json")
	if err != nil {
		t.Fatalf("unmarshalAgentProfile(json) error = %v", err)
	}
	if ver != agentProfileSchemaVersion {
		t.Errorf("schema version = %q, want %q", ver, agentProfileSchemaVersion)
	}
	if got.Name != a.Name || got.Provider != a.Provider || got.Model != a.Model {
		t.Errorf("round-trip agent mismatch: got %+v, want %+v", got, a)
	}
	if got.ProviderMeta == nil || got.ProviderMeta.AuthMode != agent.AuthModeAPIKey {
		t.Errorf("ProviderMeta not preserved in JSON round-trip")
	}
}

func TestMarshalUnmarshalAgentProfile_YAML(t *testing.T) {
	a := testAgent()
	data, err := marshalAgentProfile(a, "yaml")
	if err != nil {
		t.Fatalf("marshalAgentProfile(yaml) error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshalAgentProfile(yaml) returned empty data")
	}

	got, ver, err := unmarshalAgentProfile(data, "yaml")
	if err != nil {
		t.Fatalf("unmarshalAgentProfile(yaml) error = %v", err)
	}
	if ver != agentProfileSchemaVersion {
		t.Errorf("schema version = %q, want %q", ver, agentProfileSchemaVersion)
	}
	if got.Name != a.Name || got.Provider != a.Provider || got.Model != a.Model {
		t.Errorf("round-trip agent mismatch: got %+v, want %+v", got, a)
	}
}

func TestUnmarshalAgentProfile_AutodetectJSON(t *testing.T) {
	a := testAgent()
	data, err := marshalAgentProfile(a, "json")
	if err != nil {
		t.Fatalf("marshalAgentProfile error: %v", err)
	}
	// format="" tries JSON first, then falls back to YAML on parse failure.
	got, _, err := unmarshalAgentProfile(data, "")
	if err != nil {
		t.Fatalf("unmarshalAgentProfile autodetect error: %v", err)
	}
	if got.Name != a.Name {
		t.Errorf("autodetect JSON: got name %q, want %q", got.Name, a.Name)
	}
}

func TestUnmarshalAgentProfile_AutodetectYAML(t *testing.T) {
	a := testAgent()
	data, err := marshalAgentProfile(a, "yaml")
	if err != nil {
		t.Fatalf("marshalAgentProfile error: %v", err)
	}
	// format="" falls back to YAML when JSON parsing fails.
	got, _, err := unmarshalAgentProfile(data, "")
	if err != nil {
		t.Fatalf("unmarshalAgentProfile autodetect YAML error: %v", err)
	}
	if got.Name != a.Name {
		t.Errorf("autodetect YAML: got name %q, want %q", got.Name, a.Name)
	}
}

func TestMarshalUnmarshalInstructions_JSON(t *testing.T) {
	insts := []agent.InstructionFile{
		{Name: "agents", Filename: "AGENTS.md", Content: "global rules", Scope: agent.InstructionScopeGlobal},
		{Name: "claude", Filename: "CLAUDE.md", Content: "claude rules", Scope: agent.InstructionScopeDirectory, DirectoryPattern: "/repo/*"},
	}
	data, err := marshalInstructions(insts, "json", nil)
	if err != nil {
		t.Fatalf("marshalInstructions(json) error = %v", err)
	}
	got, err := unmarshalInstructions(data, "json", "instructions.json")
	if err != nil {
		t.Fatalf("unmarshalInstructions(json) error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instructions, want 2", len(got))
	}
	if got[0].Scope != agent.InstructionScopeGlobal {
		t.Errorf("scope not preserved: got %q, want %q", got[0].Scope, agent.InstructionScopeGlobal)
	}
	if got[1].DirectoryPattern != "/repo/*" {
		t.Errorf("directory pattern not preserved: got %q", got[1].DirectoryPattern)
	}
}

func TestMarshalUnmarshalInstructions_YAML(t *testing.T) {
	insts := []agent.InstructionFile{
		{Name: "agents", Filename: "AGENTS.md", Content: "global rules"},
	}
	data, err := marshalInstructions(insts, "yaml", nil)
	if err != nil {
		t.Fatalf("marshalInstructions(yaml) error = %v", err)
	}
	got, err := unmarshalInstructions(data, "", "instructions.yaml")
	if err != nil {
		t.Fatalf("unmarshalInstructions yaml autodetect error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "agents" {
		t.Errorf("YAML round-trip failed: got %+v", got)
	}
}

func TestPrepareAgentProfileMergePreservesCreatedAtAndBumpsUpdatedAt(t *testing.T) {
	existing := testAgent()
	existing.APIKey = "existing-key"
	imported := testAgent()
	imported.APIKey = ""
	imported.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	imported.UpdatedAt = time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 9, 15, 30, 0, 0, time.UTC)

	got := prepareAgentProfileMerge(existing, imported, now)

	if !got.CreatedAt.Equal(existing.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want existing %v", got.CreatedAt, existing.CreatedAt)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}
	if got.APIKey != "existing-key" {
		t.Fatalf("APIKey = %q, want existing key", got.APIKey)
	}
}

func TestPrepareAgentProfileMergeKeepsImportedAPIKey(t *testing.T) {
	existing := testAgent()
	existing.APIKey = "existing-key"
	imported := testAgent()
	imported.APIKey = "imported-key"

	got := prepareAgentProfileMerge(existing, imported, time.Now())

	if got.APIKey != "imported-key" {
		t.Fatalf("APIKey = %q, want imported key", got.APIKey)
	}
}
