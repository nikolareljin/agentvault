package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/workspace"
)

// actionItem is one entry of the actions menu. Its shortcut runs it
// directly from the menu, so a task is two keys away from any screen.
type actionItem struct {
	shortcut key.Binding
	label    string
	desc     string
	run      func(m *model) tea.Cmd
}

func actionItems() []actionItem {
	return []actionItem{
		{keys.ActAdd, "Add agent", "Register an agent by name, provider and model", (*model).openAddAgent},
		{keys.ActSwitch, "Switch agent", "Pick the current agent for this session", (*model).openSwitchAgent},
		{keys.ActPrompt, "Prompt", "Send a prompt to the current agent", (*model).openQuickPrompt},
		{keys.ActUsage, "Usage", "Provider status and token usage (Status tab)", (*model).openUsage},
		{keys.ActImport, "Import", "Preview, then import a bundle (agentvault import)", (*model).openImport},
		{keys.ActExport, "Export", "Export settings to a bundle (agentvault export)", (*model).openExport},
		{keys.ActCommand, "Run command", "Type any agentvault CLI command", (*model).openCommand},
	}
}

// Add-agent form fields. The provider field is a selector, not a text input.
const (
	addFieldName = iota
	addFieldProvider
	addFieldModel
	addFieldBaseURL
	addFieldCount
)

func newAddInputs() []textinput.Model {
	mk := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = 256
		ti.Width = 48
		// Update does not forward blink messages, so keep the cursor steady.
		ti.Cursor.SetMode(cursor.CursorStatic)
		return ti
	}
	// Index matches the addField constants; the provider slot stays unused.
	return []textinput.Model{
		mk("my-agent"),
		{},
		mk("optional, e.g. claude-sonnet-4"),
		mk("optional, e.g. http://localhost:11434"),
	}
}

func (m *model) openActions() {
	if m.mode != viewActions {
		m.actionsReturn = m.mode
	}
	m.mode = viewActions
	m.actionCursor = 0
}

func (m *model) closeOverlay() {
	m.mode = m.actionsReturn
	if m.mode == viewActions || m.mode == viewAddAgent || m.mode == viewSwitchAgent ||
		m.mode == viewExport || m.mode == viewImport || m.mode == viewImportPreview {
		m.mode = m.getModeForTab()
	}
}

func (m *model) handleActionsInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := actionItems()
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
		return m, nil
	case key.Matches(msg, keys.Up):
		if m.actionCursor > 0 {
			m.actionCursor--
		}
		return m, nil
	case key.Matches(msg, keys.Down):
		if m.actionCursor < len(items)-1 {
			m.actionCursor++
		}
		return m, nil
	case key.Matches(msg, keys.Select):
		return m, items[m.actionCursor].run(m)
	case key.Matches(msg, keys.Quit):
		m.quitting = true
		return m, tea.Quit
	}
	for _, it := range items {
		if key.Matches(msg, it.shortcut) {
			return m, it.run(m)
		}
	}
	return m, nil
}

func (m *model) openAddAgent() tea.Cmd {
	m.mode = viewAddAgent
	m.addInputs = newAddInputs()
	m.addProviders = agent.ValidProviders()
	m.addProvider = 0
	m.addFocus = addFieldName
	return m.addInputs[addFieldName].Focus()
}

func (m *model) openSwitchAgent() tea.Cmd {
	if len(m.agents) == 0 {
		m.closeOverlay()
		m.setStatus("No agents in vault; add one first", true)
		return nil
	}
	m.mode = viewSwitchAgent
	m.switchCursor = 0
	if i := m.currentAgentIndex(); i >= 0 {
		m.switchCursor = i
	}
	return nil
}

func (m *model) openUsage() tea.Cmd {
	m.activeTab = tabStatus
	m.mode = viewStatus
	return statusRefreshCmd(m.vault)
}

func (m *model) openCommand() tea.Cmd {
	m.closeOverlay()
	m.commandMode = true
	m.commandQuery = ""
	return nil
}

// openQuickPrompt opens the Prompt Gateway on the prompt step when a current
// agent is set, so enter runs it in the default workspace. Without one it
// starts at agent selection, and the agent picked becomes current.
func (m *model) openQuickPrompt() tea.Cmd {
	if len(m.agents) == 0 {
		m.closeOverlay()
		m.setStatus("No agents in vault; add one first", true)
		return nil
	}
	m.activeTab = tabCommands
	m.mode = viewCommands
	m.resetGateway()
	m.gatewayQuick = true
	idx := m.currentAgentIndex()
	if idx < 0 && len(m.agents) == 1 {
		idx = 0
		m.currentAgent = m.agents[0].Name
	}
	if idx >= 0 {
		m.gatewayAgentCursor = idx
		m.gatewayStage = gatewayInputPrompt
	} else {
		m.gatewayStage = gatewaySelectAgent
	}
	return nil
}

func (m *model) resetGateway() {
	m.gatewayStage = gatewaySelectAgent
	m.gatewayQuick = false
	m.gatewayAgentCursor = 0
	if i := m.currentAgentIndex(); i >= 0 {
		m.gatewayAgentCursor = i
	}
	m.gatewayPrompt = ""
	m.gatewayWorkspace = ""
	m.gatewayWorkSource = ""
	m.gatewayWorkGitRepo = false
	m.gatewayEffective = ""
	m.gatewayProfile = ""
	m.gatewayOptimized = false
	m.gatewayResponse = ""
	m.gatewayErr = ""
	m.gatewayUsage = gatewayUsage{}
}

// quickRunGateway rewrites the prompt and runs it in the default workspace,
// skipping the workspace and preview steps.
func (m *model) quickRunGateway() tea.Cmd {
	a := m.agents[m.gatewayAgentCursor]
	m.gatewayEffective, m.gatewayProfile = optimizePromptForGateway(m.gatewayPrompt, a, m.shared)
	m.gatewayOptimized = true
	resolved, err := workspace.ResolveCurrentDirDefault(m.cwd)
	if err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	m.gatewayWorkspace = resolved.Path
	m.gatewayWorkSource = string(resolved.Source)
	m.gatewayWorkGitRepo = resolved.IsGitRepo
	return m.startGatewayRun()
}

func (m *model) startGatewayRun() tea.Cmd {
	m.gatewayRunning = true
	m.gatewayResponse = ""
	m.gatewayErr = ""
	m.gatewayUsage = gatewayUsage{}
	m.gatewayStartedAt = time.Now()
	m.gatewayTick = 0
	m.gatewayStage = gatewayRunning
	a := m.agents[m.gatewayAgentCursor]
	return tea.Batch(runGatewayPromptCmd(a, m.gatewayEffective, m.gatewayWorkspace, 5*time.Minute), gatewayPulseCmd())
}

// currentAgentIndex is the index of the session's current agent in m.agents,
// or -1 when none is set or it was removed from the vault.
func (m model) currentAgentIndex() int {
	if m.currentAgent == "" {
		return -1
	}
	for i, a := range m.agents {
		if a.Name == m.currentAgent {
			return i
		}
	}
	return -1
}

func (m *model) handleSwitchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
	case key.Matches(msg, keys.Up):
		if m.switchCursor > 0 {
			m.switchCursor--
		}
	case key.Matches(msg, keys.Down):
		if m.switchCursor < len(m.agents)-1 {
			m.switchCursor++
		}
	case key.Matches(msg, keys.Select):
		if m.switchCursor < len(m.agents) {
			m.currentAgent = m.agents[m.switchCursor].Name
			m.setStatus(fmt.Sprintf("Current agent: %s", m.currentAgent), false)
		}
		m.closeOverlay()
	case key.Matches(msg, keys.ClearAgent):
		m.currentAgent = ""
		m.setStatus("Current agent cleared", false)
		m.closeOverlay()
	}
	return m, nil
}

func (m *model) handleAddAgentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
		return m, nil
	case key.Matches(msg, keys.Submit):
		return m, m.submitAddAgent()
	case key.Matches(msg, keys.NextField):
		return m, m.focusAddField((m.addFocus + 1) % addFieldCount)
	case key.Matches(msg, keys.PrevField):
		return m, m.focusAddField((m.addFocus + addFieldCount - 1) % addFieldCount)
	}
	if m.addFocus == addFieldProvider {
		n := len(m.addProviders)
		if n == 0 {
			return m, nil
		}
		switch {
		case key.Matches(msg, keys.CycleNext):
			m.addProvider = (m.addProvider + 1) % n
		case key.Matches(msg, keys.CyclePrev):
			m.addProvider = (m.addProvider + n - 1) % n
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.addInputs[m.addFocus], cmd = m.addInputs[m.addFocus].Update(msg)
	return m, cmd
}

func (m *model) focusAddField(i int) tea.Cmd {
	for j := range m.addInputs {
		if j != addFieldProvider {
			m.addInputs[j].Blur()
		}
	}
	m.addFocus = i
	if i == addFieldProvider {
		return nil
	}
	return m.addInputs[i].Focus()
}

// addFormProvider is the provider the selector shows.
func (m model) addFormProvider() agent.Provider {
	if m.addProvider < 0 || m.addProvider >= len(m.addProviders) {
		return ""
	}
	return m.addProviders[m.addProvider]
}

// submitAddAgent applies the same checks as the CLI add command
// (agent.Validate, then vault.Add, which refuses duplicate names).
func (m *model) submitAddAgent() tea.Cmd {
	now := time.Now()
	a := agent.Agent{
		Name:      strings.TrimSpace(m.addInputs[addFieldName].Value()),
		Provider:  m.addFormProvider(),
		Model:     strings.TrimSpace(m.addInputs[addFieldModel].Value()),
		BaseURL:   strings.TrimSpace(m.addInputs[addFieldBaseURL].Value()),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.Validate(); err != nil {
		m.setStatus("Error: "+err.Error(), true)
		return nil
	}
	if err := m.vault.Add(a); err != nil {
		m.setStatus(fmt.Sprintf("Error: %v", err), true)
		return nil
	}
	m.refresh()
	m.closeOverlay()
	m.setStatus(fmt.Sprintf("Added %s. If it needs an API key: agentvault edit %s --api-key KEY", a.Name, a.Name), false)
	return nil
}

func (m model) renderActions() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Actions"))
	b.WriteString("\n\n")
	for i, it := range actionItems() {
		cursor, style := "  ", normalStyle
		if i == m.actionCursor {
			cursor, style = "> ", selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s  %-14s", cursor, it.shortcut.Help().Key, it.label)))
		b.WriteString(dimStyle.Render("  " + it.desc))
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderAddAgent() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Add agent"))
	b.WriteString("\n\n")
	labels := []string{"Name", "Provider", "Model", "Base URL"}
	for i, label := range labels {
		marker := "  "
		if i == m.addFocus {
			marker = "> "
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("%s%-10s", marker, label)))
		b.WriteString(" ")
		if i == addFieldProvider {
			b.WriteString(fmt.Sprintf("< %s >", m.addFormProvider()))
		} else {
			b.WriteString(m.addInputs[i].View())
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  No API key here: if one is needed, use agentvault edit NAME --api-key KEY"))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderSwitchAgent() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Switch agent"))
	b.WriteString("\n\n")
	for i, a := range m.agents {
		cursor, style := "  ", normalStyle
		if i == m.switchCursor {
			cursor, style = "> ", selectedStyle
		}
		mark := " "
		if a.Name == m.currentAgent {
			mark = "*"
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s %-20s  %-10s  %s", cursor, mark, truncate(a.Name, 20), a.Provider, truncate(a.Model, 30))))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  The current agent lasts for this session only; Prompt uses it."))
	b.WriteString("\n")
	return b.String()
}
