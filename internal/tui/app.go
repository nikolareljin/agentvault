// Package tui implements the interactive terminal UI for AgentVault.
//
// The TUI uses the Bubble Tea framework with four main tabs:
//  1. Agents:       List/detail view of configured agents
//  2. Instructions: Stored instruction files (AGENTS.md, CLAUDE.md, etc.)
//  3. Detected:     Auto-detected AI CLI tools on the system
//  4. Status:       Vault info, provider configs, and system overview
//
// Navigation follows vim-style keybindings (h/j/k/l) with Tab cycling
// between tabs and / for search/filter in the Agents tab.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// Lipgloss styles for consistent TUI rendering.
// Colors use ANSI 256-color codes for broad terminal compatibility.
var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	subtitleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	selectedStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	normalStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	labelStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	helpStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	successStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	tabActiveStyle   = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("62")).Foreground(lipgloss.Color("255")).Padding(0, 2)
	tabInactiveStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Padding(0, 2)
	boxStyle         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1, 2)
)

type viewMode int

const (
	viewAgentList viewMode = iota
	viewAgentDetail
	viewInstructions
	viewInstructionDetail
	viewDetected
	viewStatus
	viewHelp
)

type tab int

const (
	tabAgents tab = iota
	tabInstructions
	tabDetected
	tabStatus
)

// DetectedAgentInfo represents a detected agent for display.
type DetectedAgentInfo struct {
	Name      string
	Provider  string
	Version   string
	Path      string
	Status    string
	InVault   bool
	ConfigDir string
}

// model is the Bubble Tea application state.
// It holds all data loaded from the vault plus UI state (cursor, tabs, search).
type model struct {
	// Data from vault (refreshed on 'r' key)
	vault        *vault.Vault
	agents       []agent.Agent
	shared       agent.SharedConfig
	instructions []agent.InstructionFile
	detected     []DetectedAgentInfo
	providerCfgs agent.ProviderConfig

	// UI navigation state
	activeTab      tab
	mode           viewMode
	cursor         int // Agents tab cursor
	instCursor     int // Instructions tab cursor
	detectedCursor int // Detected tab cursor

	// Terminal dimensions (updated on resize)
	width    int
	height   int
	quitting bool

	// Search/filter (Agents tab only, activated with '/')
	searchMode     bool
	searchQuery    string
	filteredAgents []int // indices into agents slice

	// Temporary status messages (auto-clear after 3 seconds)
	statusMsg     string
	statusIsError bool
	statusTime    time.Time
}

func initialModel(v *vault.Vault) model {
	m := model{
		vault:        v,
		agents:       v.List(),
		shared:       v.SharedConfig(),
		instructions: v.ListInstructions(),
		providerCfgs: v.ProviderConfigs(),
		activeTab:    tabAgents,
		mode:         viewAgentList,
	}
	m.filteredAgents = make([]int, len(m.agents))
	for i := range m.agents {
		m.filteredAgents[i] = i
	}
	m.detected = detectAgentsForTUI()
	return m
}

func detectAgentsForTUI() []DetectedAgentInfo {
	// Simplified detection for TUI display
	var detected []DetectedAgentInfo

	// Check for Claude
	if path := findExecutable("claude"); path != "" {
		version := getVersion("claude", "--version")
		detected = append(detected, DetectedAgentInfo{
			Name:     "claude",
			Provider: "claude",
			Version:  version,
			Path:     path,
			Status:   "installed",
		})
	}

	// Check for Codex
	if path := findExecutable("codex"); path != "" {
		version := getVersion("codex", "--version")
		detected = append(detected, DetectedAgentInfo{
			Name:     "codex",
			Provider: "codex",
			Version:  version,
			Path:     path,
			Status:   "installed",
		})
	}

	// Check for Ollama
	if path := findExecutable("ollama"); path != "" {
		version := getVersion("ollama", "--version")
		detected = append(detected, DetectedAgentInfo{
			Name:     "ollama",
			Provider: "ollama",
			Version:  version,
			Path:     path,
			Status:   "installed",
		})
	}

	return detected
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Clear old status messages
		if time.Since(m.statusTime) > 3*time.Second {
			m.statusMsg = ""
		}

		// Handle search mode
		if m.searchMode {
			return m.handleSearchInput(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.mode == viewAgentDetail || m.mode == viewInstructionDetail {
				m.mode = m.getModeForTab()
				return m, nil
			}
			if m.mode == viewHelp {
				m.mode = m.getModeForTab()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "esc":
			if m.mode == viewAgentDetail || m.mode == viewInstructionDetail || m.mode == viewHelp {
				m.mode = m.getModeForTab()
				return m, nil
			}

		case "tab", "l":
			m.activeTab = (m.activeTab + 1) % 4
			m.mode = m.getModeForTab()
			m.cursor = 0
			return m, nil

		case "shift+tab", "h":
			if m.activeTab == 0 {
				m.activeTab = 3
			} else {
				m.activeTab--
			}
			m.mode = m.getModeForTab()
			m.cursor = 0
			return m, nil

		case "1":
			m.activeTab = tabAgents
			m.mode = viewAgentList
			return m, nil
		case "2":
			m.activeTab = tabInstructions
			m.mode = viewInstructions
			return m, nil
		case "3":
			m.activeTab = tabDetected
			m.mode = viewDetected
			return m, nil
		case "4":
			m.activeTab = tabStatus
			m.mode = viewStatus
			return m, nil

		case "?":
			m.mode = viewHelp
			return m, nil

		case "/":
			if m.activeTab == tabAgents {
				m.searchMode = true
				m.searchQuery = ""
			}
			return m, nil

		case "up", "k":
			return m.handleNavUp(), nil

		case "down", "j":
			return m.handleNavDown(), nil

		case "enter":
			return m.handleEnter(), nil

		case "r":
			// Refresh
			m.agents = m.vault.List()
			m.shared = m.vault.SharedConfig()
			m.instructions = m.vault.ListInstructions()
			m.detected = detectAgentsForTUI()
			m.providerCfgs = m.vault.ProviderConfigs()
			m.updateFilteredAgents()
			m.setStatus("Refreshed", false)
			return m, nil
		}
	}
	return m, nil
}

func (m *model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.updateFilteredAgents()
	case "enter":
		m.searchMode = false
		m.updateFilteredAgents()
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.updateFilteredAgents()
		}
	default:
		if len(msg.String()) == 1 {
			m.searchQuery += msg.String()
			m.updateFilteredAgents()
		}
	}
	return m, nil
}

func (m *model) updateFilteredAgents() {
	if m.searchQuery == "" {
		m.filteredAgents = make([]int, len(m.agents))
		for i := range m.agents {
			m.filteredAgents[i] = i
		}
		return
	}

	query := strings.ToLower(m.searchQuery)
	m.filteredAgents = nil
	for i, a := range m.agents {
		if strings.Contains(strings.ToLower(a.Name), query) ||
			strings.Contains(strings.ToLower(string(a.Provider)), query) ||
			strings.Contains(strings.ToLower(a.Model), query) {
			m.filteredAgents = append(m.filteredAgents, i)
		}
	}
	if m.cursor >= len(m.filteredAgents) {
		m.cursor = max(0, len(m.filteredAgents)-1)
	}
}

func (m *model) handleNavUp() model {
	switch m.activeTab {
	case tabAgents:
		if m.mode == viewAgentList && m.cursor > 0 {
			m.cursor--
		}
	case tabInstructions:
		if m.mode == viewInstructions && m.instCursor > 0 {
			m.instCursor--
		}
	case tabDetected:
		if m.mode == viewDetected && m.detectedCursor > 0 {
			m.detectedCursor--
		}
	}
	return *m
}

func (m *model) handleNavDown() model {
	switch m.activeTab {
	case tabAgents:
		if m.mode == viewAgentList && m.cursor < len(m.filteredAgents)-1 {
			m.cursor++
		}
	case tabInstructions:
		if m.mode == viewInstructions && m.instCursor < len(m.instructions)-1 {
			m.instCursor++
		}
	case tabDetected:
		if m.mode == viewDetected && m.detectedCursor < len(m.detected)-1 {
			m.detectedCursor++
		}
	}
	return *m
}

func (m *model) handleEnter() model {
	switch m.activeTab {
	case tabAgents:
		if m.mode == viewAgentList && len(m.filteredAgents) > 0 {
			m.mode = viewAgentDetail
		}
	case tabInstructions:
		if m.mode == viewInstructions && len(m.instructions) > 0 {
			m.mode = viewInstructionDetail
		}
	}
	return *m
}

func (m *model) getModeForTab() viewMode {
	switch m.activeTab {
	case tabAgents:
		return viewAgentList
	case tabInstructions:
		return viewInstructions
	case tabDetected:
		return viewDetected
	case tabStatus:
		return viewStatus
	}
	return viewAgentList
}

func (m *model) setStatus(msg string, isError bool) {
	m.statusMsg = msg
	m.statusIsError = isError
	m.statusTime = time.Now()
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header with tabs
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")

	// Content based on view mode
	switch m.mode {
	case viewAgentList:
		b.WriteString(m.renderAgentList())
	case viewAgentDetail:
		b.WriteString(m.renderAgentDetail())
	case viewInstructions:
		b.WriteString(m.renderInstructions())
	case viewInstructionDetail:
		b.WriteString(m.renderInstructionDetail())
	case viewDetected:
		b.WriteString(m.renderDetected())
	case viewStatus:
		b.WriteString(m.renderStatus())
	case viewHelp:
		b.WriteString(m.renderHelp())
	}

	// Status bar
	if m.statusMsg != "" && time.Since(m.statusTime) < 3*time.Second {
		b.WriteString("\n")
		if m.statusIsError {
			b.WriteString(errorStyle.Render(m.statusMsg))
		} else {
			b.WriteString(successStyle.Render(m.statusMsg))
		}
	}

	// Help
	b.WriteString("\n\n")
	b.WriteString(m.renderHelpBar())

	return b.String()
}

func (m model) renderHeader() string {
	title := titleStyle.Render("AgentVault")

	tabs := []string{"Agents", "Instructions", "Detected", "Status"}
	var tabBar strings.Builder
	for i, t := range tabs {
		if tab(i) == m.activeTab {
			tabBar.WriteString(tabActiveStyle.Render(fmt.Sprintf("%d:%s", i+1, t)))
		} else {
			tabBar.WriteString(tabInactiveStyle.Render(fmt.Sprintf("%d:%s", i+1, t)))
		}
		tabBar.WriteString(" ")
	}

	return fmt.Sprintf("%s    %s", title, tabBar.String())
}

func (m model) renderAgentList() string {
	var b strings.Builder

	// Search bar
	if m.searchMode {
		b.WriteString(subtitleStyle.Render("Search: "))
		b.WriteString(m.searchQuery)
		b.WriteString("█")
		b.WriteString("\n\n")
	} else if m.searchQuery != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf("Filter: %s", m.searchQuery)))
		b.WriteString("\n\n")
	}

	if len(m.filteredAgents) == 0 {
		if m.searchQuery != "" {
			b.WriteString(dimStyle.Render("  No agents match the filter."))
		} else {
			b.WriteString(dimStyle.Render("  No agents configured. Use 'agentvault add' to get started."))
		}
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	header := fmt.Sprintf("  %-20s  %-10s  %-20s  %s", "NAME", "PROVIDER", "MODEL", "TAGS")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 70)))
	b.WriteString("\n")

	for i, idx := range m.filteredAgents {
		a := m.agents[idx]
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		tags := ""
		if len(a.Tags) > 0 {
			tags = strings.Join(a.Tags, ",")
			if len(tags) > 15 {
				tags = tags[:12] + "..."
			}
		}
		line := fmt.Sprintf("%s%-20s  %-10s  %-20s  %s", cursor, truncate(a.Name, 20), a.Provider, truncate(a.Model, 20), tags)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	// Shared config info
	b.WriteString("\n")
	if m.shared.SystemPrompt != "" {
		prompt := truncate(m.shared.SystemPrompt, 60)
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

	return b.String()
}

func (m model) renderAgentDetail() string {
	if m.cursor >= len(m.filteredAgents) {
		return ""
	}
	a := m.agents[m.filteredAgents[m.cursor]]
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
		masked := a.APIKey[:min(4, len(a.APIKey))] + strings.Repeat("*", max(0, len(a.APIKey)-4))
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
		b.WriteString(fmt.Sprintf("  %s  %s%s\n", labelStyle.Render(fmt.Sprintf("%-16s", "System Prompt:")), truncate(effectivePrompt, 50), src))
	} else {
		field("System Prompt", "")
	}

	field("Task Desc", truncate(a.TaskDesc, 50))

	if len(a.Tags) > 0 {
		field("Tags", strings.Join(a.Tags, ", "))
	} else {
		field("Tags", "")
	}

	// MCP servers
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
			b.WriteString(fmt.Sprintf("    • %s: %s %s%s\n", s.Name, s.Command, strings.Join(s.Args, " "), origin))
		}
	}

	if !a.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("\n  %s  %s\n", dimStyle.Render("Created:"), a.CreatedAt.Format("2006-01-02 15:04")))
	}
	if !a.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  %s  %s\n", dimStyle.Render("Updated:"), a.UpdatedAt.Format("2006-01-02 15:04")))
	}

	return b.String()
}

func (m model) renderInstructions() string {
	var b strings.Builder

	if len(m.instructions) == 0 {
		b.WriteString(dimStyle.Render("  No instruction files stored."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Use 'agentvault instructions pull' to import from a project."))
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	header := fmt.Sprintf("  %-15s  %-35s  %10s  %s", "NAME", "FILENAME", "SIZE", "UPDATED")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 75)))
	b.WriteString("\n")

	for i, inst := range m.instructions {
		cursor := "  "
		style := normalStyle
		if i == m.instCursor {
			cursor = "> "
			style = selectedStyle
		}
		updated := ""
		if !inst.UpdatedAt.IsZero() {
			updated = inst.UpdatedAt.Format("2006-01-02")
		}
		line := fmt.Sprintf("%s%-15s  %-35s  %10d  %s", cursor, truncate(inst.Name, 15), truncate(inst.Filename, 35), len(inst.Content), updated)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderInstructionDetail() string {
	if m.instCursor >= len(m.instructions) {
		return ""
	}
	inst := m.instructions[m.instCursor]
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("Instruction: %s", inst.Name)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("Target: %s  |  Size: %d bytes", inst.Filename, len(inst.Content))))
	b.WriteString("\n\n")

	// Show content preview (first ~20 lines)
	lines := strings.Split(inst.Content, "\n")
	maxLines := min(20, len(lines))
	for i := 0; i < maxLines; i++ {
		line := lines[i]
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		b.WriteString(fmt.Sprintf("  %s\n", line))
	}
	if len(lines) > maxLines {
		b.WriteString(dimStyle.Render(fmt.Sprintf("\n  ... and %d more lines", len(lines)-maxLines)))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderDetected() string {
	var b strings.Builder

	if len(m.detected) == 0 {
		b.WriteString(dimStyle.Render("  No AI agents detected on this system."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Install Claude Code, Codex CLI, or Ollama to get started."))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(subtitleStyle.Render("Installed AI Agents"))
	b.WriteString("\n\n")

	for i, d := range m.detected {
		cursor := "  "
		style := normalStyle
		if i == m.detectedCursor {
			cursor = "> "
			style = selectedStyle
		}

		vaultStatus := dimStyle.Render("○")
		if d.InVault {
			vaultStatus = successStyle.Render("●")
		}

		line := fmt.Sprintf("%s%s %-12s  %-30s  %s", cursor, vaultStatus, d.Name, d.Version, d.Path)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  ● = in vault    ○ = not in vault"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Use 'agentvault detect add' to add detected agents to vault."))
	b.WriteString("\n")

	return b.String()
}

func (m model) renderStatus() string {
	var b strings.Builder

	b.WriteString(subtitleStyle.Render("System Status"))
	b.WriteString("\n\n")

	// Vault info
	b.WriteString(labelStyle.Render("  Vault:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("    Path:         %s\n", m.vault.Path()))
	b.WriteString(fmt.Sprintf("    Agents:       %d\n", len(m.agents)))
	b.WriteString(fmt.Sprintf("    Instructions: %d\n", len(m.instructions)))
	b.WriteString(fmt.Sprintf("    MCP Servers:  %d (shared)\n", len(m.shared.MCPServers)))

	// Provider configs
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Provider Configs:"))
	b.WriteString("\n")

	claudeStatus := dimStyle.Render("not configured")
	if m.providerCfgs.Claude != nil {
		plugins := len(m.providerCfgs.Claude.EnabledPlugins)
		claudeStatus = successStyle.Render(fmt.Sprintf("configured (%d plugins)", plugins))
	}
	b.WriteString(fmt.Sprintf("    Claude: %s\n", claudeStatus))

	codexStatus := dimStyle.Render("not configured")
	if m.providerCfgs.Codex != nil {
		projects := len(m.providerCfgs.Codex.TrustedProjects)
		rules := len(m.providerCfgs.Codex.Rules)
		codexStatus = successStyle.Render(fmt.Sprintf("configured (%d projects, %d rules)", projects, rules))
	}
	b.WriteString(fmt.Sprintf("    Codex:  %s\n", codexStatus))

	ollamaStatus := dimStyle.Render("not configured")
	if m.providerCfgs.Ollama != nil {
		ollamaStatus = successStyle.Render(fmt.Sprintf("configured (base: %s)", m.providerCfgs.Ollama.BaseURL))
	}
	b.WriteString(fmt.Sprintf("    Ollama: %s\n", ollamaStatus))

	// Detected agents
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Detected Agents:"))
	b.WriteString("\n")
	for _, d := range m.detected {
		status := successStyle.Render("✓")
		vaultNote := ""
		if !d.InVault {
			vaultNote = dimStyle.Render(" (not in vault)")
		}
		b.WriteString(fmt.Sprintf("    %s %s %s%s\n", status, d.Name, d.Version, vaultNote))
	}
	if len(m.detected) == 0 {
		b.WriteString(dimStyle.Render("    No agents detected"))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderHelp() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Keyboard Shortcuts"))
	b.WriteString("\n\n")

	sections := []struct {
		title string
		keys  [][]string
	}{
		{
			title: "Navigation",
			keys: [][]string{
				{"Tab / l", "Next tab"},
				{"Shift+Tab / h", "Previous tab"},
				{"1-4", "Jump to tab"},
				{"j/k or ↓/↑", "Move cursor"},
				{"Enter", "View details"},
				{"Esc", "Back / Close"},
				{"q", "Quit"},
			},
		},
		{
			title: "Agents Tab",
			keys: [][]string{
				{"/", "Search/filter agents"},
				{"Enter", "View agent details"},
			},
		},
		{
			title: "General",
			keys: [][]string{
				{"r", "Refresh data"},
				{"?", "Show this help"},
			},
		},
	}

	for _, section := range sections {
		b.WriteString(subtitleStyle.Render("  " + section.title))
		b.WriteString("\n")
		for _, kv := range section.keys {
			b.WriteString(fmt.Sprintf("    %-20s %s\n", kv[0], kv[1]))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderHelpBar() string {
	var help string
	switch m.mode {
	case viewAgentDetail, viewInstructionDetail:
		help = "esc: back  q: quit"
	case viewHelp:
		help = "esc: back  q: quit"
	default:
		if m.searchMode {
			help = "enter: apply filter  esc: cancel"
		} else {
			help = "tab: switch tabs  /: search  ?: help  q: quit"
		}
	}
	return helpStyle.Render("  " + help)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Run starts the TUI application with an unlocked vault.
func Run() error {
	return RunWithVault(nil)
}

// RunWithVault starts the TUI with a pre-unlocked vault.
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
