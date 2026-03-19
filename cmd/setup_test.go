package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestSetupBundleMarshalIncludesWorkflowTemplatesField(t *testing.T) {
	data, err := json.Marshal(SetupBundle{})
	if err != nil {
		t.Fatalf("json.Marshal(SetupBundle{}) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(marshal output) error = %v", err)
	}
	if _, ok := payload["workflow_templates"]; !ok {
		t.Fatalf("marshal output keys = %v, want workflow_templates field present", payload)
	}
	for _, key := range []string{"provider_files", "project_files", "instruction_overrides", "skill_assets"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("marshal output keys = %v, want %s field present", payload, key)
		}
	}
}

func TestSelectAgentsForExport(t *testing.T) {
	agents := []agent.Agent{
		{Name: "alpha"},
		{Name: "beta"},
	}
	selected, err := selectAgentsForExport(agents, "beta")
	if err != nil {
		t.Fatalf("selectAgentsForExport() error = %v", err)
	}
	if len(selected) != 1 || selected[0].Name != "beta" {
		t.Fatalf("selectAgentsForExport() = %#v, want only beta", selected)
	}
}

func TestSelectAgentsForExportMissing(t *testing.T) {
	_, err := selectAgentsForExport([]agent.Agent{{Name: "alpha"}}, "beta")
	if err == nil || !strings.Contains(err.Error(), `agent "beta" not found`) {
		t.Fatalf("selectAgentsForExport() err = %v, want missing agent error", err)
	}
}

func TestFilterSessionsForAgents(t *testing.T) {
	input := agent.SessionConfig{
		Sessions: []agent.Session{
			{
				ID: "s1",
				Agents: []agent.SessionAgent{
					{Name: "alpha"},
					{Name: "beta"},
				},
			},
			{
				ID: "s2",
				Agents: []agent.SessionAgent{
					{Name: "gamma"},
				},
			},
		},
		ActiveSession: "s2",
		DefaultAgents: []string{"alpha", "beta"},
	}

	filtered := filterSessionsForAgents(input, []agent.Agent{{Name: "alpha"}})
	if len(filtered.Sessions) != 1 {
		t.Fatalf("filtered sessions = %#v, want one session", filtered.Sessions)
	}
	if len(filtered.Sessions[0].Agents) != 1 || filtered.Sessions[0].Agents[0].Name != "alpha" {
		t.Fatalf("filtered session agents = %#v, want only alpha", filtered.Sessions[0].Agents)
	}
	if filtered.ActiveSession != "" {
		t.Fatalf("filtered active session = %q, want cleared", filtered.ActiveSession)
	}
	if len(filtered.DefaultAgents) != 1 || filtered.DefaultAgents[0] != "alpha" {
		t.Fatalf("filtered default agents = %#v, want only alpha", filtered.DefaultAgents)
	}
}
