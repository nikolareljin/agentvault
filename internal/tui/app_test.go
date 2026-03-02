package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func testVault(t *testing.T) *vault.Vault {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(dir + "/test.enc")
	if err := v.Init("testpass"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	_ = v.Add(agent.Agent{Name: "claude-main", Provider: agent.ProviderClaude, Model: "claude-3-opus", APIKey: "sk-1234abcd"})
	_ = v.Add(agent.Agent{Name: "gemini-fast", Provider: agent.ProviderGemini, Model: "gemini-pro"})
	return v
}

func TestPlaceholderModelView(t *testing.T) {
	m := placeholderModel{}
	view := m.View()
	if view == "" {
		t.Error("placeholderModel.View() returned empty string")
	}
	if !strings.Contains(view, "AgentVault") {
		t.Error("placeholderModel.View() missing title")
	}
}

func TestListView(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	view := m.View()

	if !strings.Contains(view, "AgentVault") {
		t.Error("listView missing title")
	}
	if !strings.Contains(view, "claude-main") {
		t.Error("listView missing agent name")
	}
	if !strings.Contains(view, "gemini-fast") {
		t.Error("listView missing second agent")
	}
}

func TestNavigateDown(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m2 := newModel.(model)
	if m2.cursor != 1 {
		t.Errorf("after j: cursor = %d, want 1", m2.cursor)
	}
	// can't go past end
	newModel, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m3 := newModel.(model)
	if m3.cursor != 1 {
		t.Errorf("after second j: cursor = %d, want 1", m3.cursor)
	}
}

func TestNavigateUp(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.cursor = 1
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m2 := newModel.(model)
	if m2.cursor != 0 {
		t.Errorf("after k: cursor = %d, want 0", m2.cursor)
	}
	// can't go past start
	newModel, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m3 := newModel.(model)
	if m3.cursor != 0 {
		t.Errorf("after second k: cursor = %d, want 0", m3.cursor)
	}
}

func TestEnterDetailView(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := newModel.(model)
	if m2.mode != viewAgentDetail {
		t.Errorf("after enter: mode = %d, want viewAgentDetail", m2.mode)
	}
	view := m2.View()
	if !strings.Contains(view, "Agent: claude-main") {
		t.Error("detailView missing agent title")
	}
	if !strings.Contains(view, "sk-1") {
		t.Error("detailView missing masked API key prefix")
	}
}

func TestEscBackToList(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.mode = viewAgentDetail
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := newModel.(model)
	if m2.mode != viewAgentList {
		t.Errorf("after esc: mode = %d, want viewAgentList", m2.mode)
	}
}

func TestEmptyVaultView(t *testing.T) {
	dir := t.TempDir()
	v := vault.New(dir + "/empty.enc")
	_ = v.Init("testpass")

	m := initialModel(v)
	view := m.View()
	if !strings.Contains(view, "No agents configured") {
		t.Error("empty vault should show no-agents message")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"exact", 5, "exact"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestMarkDetectedInVaultUsesNameNotProvider(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.detected = []DetectedAgentInfo{
		{Name: "different-name", Provider: string(agent.ProviderClaude)},
		{Name: "claude-main", Provider: "custom"},
	}

	m.markDetectedInVault()

	if m.detected[0].InVault {
		t.Fatalf("expected provider-only match to remain out of vault")
	}
	if !m.detected[1].InVault {
		t.Fatalf("expected exact name match to be in vault")
	}
}

func TestAutoAddDetectedAgentsSkipsDuplicatePathAndExistingName(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.detected = []DetectedAgentInfo{
		{Name: "copilot", Provider: "custom", Path: "/usr/bin/copilot"},
		{Name: "github-copilot-cli", Provider: "custom", Path: "/usr/bin/copilot"},
		{Name: "claude-main", Provider: "claude", Path: "/usr/bin/claude"},
	}

	m.autoAddDetectedAgents()

	var (
		copilotCount      int
		githubCopilotSeen bool
		claudeMainCount   int
	)
	for _, a := range m.vault.List() {
		switch a.Name {
		case "copilot":
			copilotCount++
		case "github-copilot-cli":
			githubCopilotSeen = true
		case "claude-main":
			claudeMainCount++
		}
	}
	if copilotCount != 1 {
		t.Fatalf("copilotCount = %d, want 1", copilotCount)
	}
	if githubCopilotSeen {
		t.Fatalf("github-copilot-cli should be skipped as duplicate path")
	}
	if claudeMainCount != 1 {
		t.Fatalf("claude-main count = %d, want 1 (existing agent must not be duplicated)", claudeMainCount)
	}
}

func TestApplyStartTarget(t *testing.T) {
	v := testVault(t)
	cases := []struct {
		target string
		tab    tab
		mode   viewMode
	}{
		{target: "", tab: tabAgents, mode: viewAgentList},
		{target: "instructions", tab: tabInstructions, mode: viewInstructions},
		{target: "rules", tab: tabRules, mode: viewRules},
		{target: "sessions", tab: tabSessions, mode: viewSessions},
		{target: "detected", tab: tabDetected, mode: viewDetected},
		{target: "commands", tab: tabCommands, mode: viewCommands},
		{target: "status", tab: tabStatus, mode: viewStatus},
	}

	for _, tc := range cases {
		m := initialModel(v)
		applyStartTarget(&m, tc.target)
		if m.activeTab != tc.tab {
			t.Fatalf("target %q activeTab = %v, want %v", tc.target, m.activeTab, tc.tab)
		}
		if m.mode != tc.mode {
			t.Fatalf("target %q mode = %v, want %v", tc.target, m.mode, tc.mode)
		}
	}
}
