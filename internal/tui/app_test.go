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
	if m2.mode != viewDetail {
		t.Errorf("after enter: mode = %d, want viewDetail", m2.mode)
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
	m.mode = viewDetail
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := newModel.(model)
	if m2.mode != viewList {
		t.Errorf("after esc: mode = %d, want viewList", m2.mode)
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
