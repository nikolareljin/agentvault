package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	normalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	labelStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

type viewMode int

const (
	viewList viewMode = iota
	viewDetail
)

type model struct {
	vault    *vault.Vault
	agents   []agent.Agent
	shared   agent.SharedConfig
	cursor   int
	mode     viewMode
	width    int
	height   int
	quitting bool
}

func initialModel(v *vault.Vault) model {
	return model{
		vault:  v,
		agents: v.List(),
		shared: v.SharedConfig(),
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.mode == viewDetail {
				m.mode = viewList
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "esc":
			if m.mode == viewDetail {
				m.mode = viewList
				return m, nil
			}

		case "up", "k":
			if m.mode == viewList && m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.mode == viewList && m.cursor < len(m.agents)-1 {
				m.cursor++
			}

		case "enter":
			if m.mode == viewList && len(m.agents) > 0 {
				m.mode = viewDetail
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	switch m.mode {
	case viewDetail:
		return m.detailView()
	default:
		return m.listView()
	}
}

func (m model) listView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("AgentVault"))
	b.WriteString("\n\n")

	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("  No agents configured. Use 'agentvault add' to get started."))
		b.WriteString("\n")
	} else {
		for i, a := range m.agents {
			cursor := "  "
			style := normalStyle
			if i == m.cursor {
				cursor = "> "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%-20s  %-10s  %s", cursor, a.Name, a.Provider, a.Model)
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.shared.SystemPrompt != "" {
		prompt := m.shared.SystemPrompt
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Shared prompt: %s", prompt)))
		b.WriteString("\n")
	}
	if len(m.shared.MCPServers) > 0 {
		names := make([]string, len(m.shared.MCPServers))
		for i, s := range m.shared.MCPServers {
			names[i] = s.Name
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Shared MCPs: %s", strings.Join(names, ", "))))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  j/k: navigate  enter: view details  q: quit"))
	b.WriteString("\n")

	return b.String()
}

func (m model) detailView() string {
	if m.cursor >= len(m.agents) {
		return ""
	}
	a := m.agents[m.cursor]
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("Agent: %s", a.Name)))
	b.WriteString("\n\n")

	field := func(label, value string) {
		if value == "" {
			value = dimStyle.Render("(not set)")
		}
		b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render(fmt.Sprintf("%-16s", label+":")), value))
	}

	field("Provider", string(a.Provider))
	field("Model", a.Model)
	if a.APIKey != "" {
		masked := a.APIKey[:4] + strings.Repeat("*", len(a.APIKey)-4)
		field("API Key", masked)
	} else {
		field("API Key", "")
	}
	field("Base URL", a.BaseURL)

	effectivePrompt := a.EffectiveSystemPrompt(m.shared)
	if effectivePrompt != "" {
		src := ""
		if a.SystemPrompt == "" {
			src = dimStyle.Render(" (from shared)")
		}
		b.WriteString(fmt.Sprintf("  %s  %s%s\n", labelStyle.Render(fmt.Sprintf("%-16s", "System Prompt:")), truncate(effectivePrompt, 60), src))
	} else {
		field("System Prompt", "")
	}

	field("Task Desc", a.TaskDesc)

	if len(a.Tags) > 0 {
		field("Tags", strings.Join(a.Tags, ", "))
	} else {
		field("Tags", "")
	}

	mcpServers := a.EffectiveMCPServers(m.shared)
	if len(mcpServers) > 0 {
		b.WriteString(fmt.Sprintf("\n  %s\n", labelStyle.Render("MCP Servers:")))
		for _, s := range mcpServers {
			origin := ""
			isShared := true
			for _, as := range a.MCPServers {
				if as.Name == s.Name {
					isShared = false
					break
				}
			}
			if isShared {
				origin = dimStyle.Render(" (shared)")
			}
			b.WriteString(fmt.Sprintf("    - %s: %s %s%s\n", s.Name, s.Command, strings.Join(s.Args, " "), origin))
		}
	}

	if !a.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("\n  %s  %s\n", dimStyle.Render("Created:"), a.CreatedAt.Format("2006-01-02 15:04")))
	}
	if !a.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  %s  %s\n", dimStyle.Render("Updated:"), a.UpdatedAt.Format("2006-01-02 15:04")))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  esc/q: back to list"))
	b.WriteString("\n")

	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// Run starts the TUI application with an unlocked vault.
func Run() error {
	return RunWithVault(nil)
}

// RunWithVault starts the TUI with a pre-unlocked vault.
// If v is nil, the TUI shows a message to use the CLI instead.
func RunWithVault(v *vault.Vault) error {
	var m tea.Model
	if v != nil {
		m = initialModel(v)
	} else {
		m = placeholderModel{}
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// placeholderModel is shown when no vault is provided.
type placeholderModel struct{}

func (placeholderModel) Init() tea.Cmd { return nil }
func (placeholderModel) View() string {
	return titleStyle.Render("AgentVault") + "\n\n" +
		"  Run 'agentvault init' first, then 'agentvault tui' to use the TUI.\n\n" +
		helpStyle.Render("  q: quit") + "\n"
}
func (p placeholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return p, tea.Quit
		}
	}
	return p, nil
}
