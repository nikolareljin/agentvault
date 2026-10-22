package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Vault fields can come from another machine's bundle; the TUI must not
// pass their control characters to the terminal.
func TestViewStripsControlCharacters(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.agents[0].Name = "n\x1b]0;title\a\r"
	m.agents[0].Model = "m\x1b[2J\u009b1;1H"
	for _, view := range []string{m.View(), func() string {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(model).View()
	}()} {
		if strings.ContainsAny(view, "\a\r\u009b") || strings.Contains(view, "\x1b]") || strings.Contains(view, "\x1b[2J") {
			t.Errorf("view has control characters: %q", view)
		}
		if !strings.Contains(view, "n?]0;title??") {
			t.Errorf("name missing: %q", view)
		}
	}
}

func TestCleanViewKeepsStyles(t *testing.T) {
	in := "\x1b[1;38;5;12mA\x1b[0m\tb\n\x1b[2Jc\x1b]8;;u\a"
	if got := cleanView(in); got != "\x1b[1;38;5;12mA\x1b[0m\tb\n?[2Jc?]8;;u?" {
		t.Errorf("got %q", got)
	}
}
