package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type model struct{}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	return "AgentVault TUI - Press q to quit\n(Not yet implemented)\n"
}

// Run starts the TUI application.
func Run() error {
	p := tea.NewProgram(model{})
	_, err := p.Run()
	return err
}
