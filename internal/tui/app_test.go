package tui

import "testing"

func TestModelView(t *testing.T) {
	m := model{}
	view := m.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}
