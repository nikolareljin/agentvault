// Package tui implements the interactive terminal UI for AgentVault.
//
// The TUI uses the Bubble Tea framework with seven main tabs:
//  1. Agents:       List/detail view of configured agents
//  2. Instructions: Stored instruction files (AGENTS.md, CLAUDE.md, etc.)
//  3. Rules:        Unified rules that apply across all agents
//  4. Sessions:     Multi-agent work sessions
//  5. Detected:     Auto-detected AI CLI tools on the system
//  6. Commands:     Run any AgentVault CLI command from inside the TUI
//  7. Status:       Vault info, provider configs, and system overview
//
// Navigation follows vim-style keybindings (h/j/k/l) with Tab cycling
// between tabs and / for search/filter in the Agents tab.
//
// Interactive actions:
//   - d: Delete selected agent or rule (with confirmation)
//   - e: Edit selected instruction in external editor ($EDITOR, nano, vi)
//   - a: Add detected agent to vault (Detected tab)
//   - :: Run any CLI command (all CLI features available in TUI)
package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nikolareljin/agentvault/internal/agent"
	statuspkg "github.com/nikolareljin/agentvault/internal/status"
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
	warnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
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
	viewRules
	viewRuleDetail
	viewSessions
	viewSessionDetail
	viewDetected
	viewDetectedDetail
	viewCommands
	viewStatus
	viewHelp
	viewConfirmDelete
)

type tab int

const (
	tabAgents tab = iota
	tabInstructions
	tabRules
	tabSessions
	tabDetected
	tabCommands
	tabStatus
)

const numTabs = 7

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

type detectedInstructionInfo struct {
	Name     string
	Filename string
	Path     string
	Updated  time.Time
	Size     int
}

type quickCommand struct {
	Label   string
	Command string
}

type statusRefreshMsg struct {
	report statuspkg.Report
	err    error
}

// editorFinishedMsg is sent when the external editor exits.
type editorFinishedMsg struct {
	err     error
	tmpPath string
	name    string
}

// cliCommandFinishedMsg is sent after a CLI command exits.
type cliCommandFinishedMsg struct {
	command string
	err     error
}

type gatewayStage int

const (
	gatewayOff gatewayStage = iota
	gatewaySelectAgent
	gatewayInputPrompt
	gatewayPreview
	gatewayRunning
	gatewayResult
)

type gatewayUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

type gatewayFinishedMsg struct {
	response string
	usage    gatewayUsage
	err      error
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
	statusReport *statuspkg.Report
	statusErr    string

	// UI navigation state
	activeTab      tab
	mode           viewMode
	prevMode       viewMode // for returning from confirm/help
	cursor         int      // Agents tab cursor
	instCursor     int      // Instructions tab cursor
	ruleCursor     int      // Rules tab cursor
	sessionCursor  int      // Sessions tab cursor
	detectedCursor int      // Detected tab cursor

	// Terminal dimensions (updated on resize)
	width    int
	height   int
	quitting bool

	// Search/filter (Agents tab only, activated with '/')
	searchMode     bool
	searchQuery    string
	filteredAgents []int // indices into agents slice

	// CLI command mode (activated with ':')
	commandMode    bool
	commandQuery   string
	lastCommand    string
	commandRunning bool
	commandCursor  int
	quickCommands  []quickCommand

	// Prompt gateway mode (Commands tab, activated with 'g')
	gatewayStage       gatewayStage
	gatewayAgentCursor int
	gatewayPrompt      string
	gatewayEffective   string
	gatewayProfile     string
	gatewayOptimized   bool
	gatewayRunning     bool
	gatewayResponse    string
	gatewayErr         string
	gatewayUsage       gatewayUsage

	// Delete confirmation state
	deleteTarget string // name of item to delete
	deleteType   string // "agent", "rule", "instruction"

	// Editor state
	editingInst string // name of instruction being edited
	editTmpPath string // temp file path for editor

	// Temporary status messages (auto-clear after 3 seconds)
	statusMsg     string
	statusIsError bool
	statusTime    time.Time
	cwd           string
	localInst     []detectedInstructionInfo
}

func initialModel(v *vault.Vault) model {
	cwd, _ := os.Getwd()
	m := model{
		vault:        v,
		agents:       v.List(),
		shared:       v.SharedConfig(),
		instructions: v.ListInstructions(),
		providerCfgs: v.ProviderConfigs(),
		activeTab:    tabAgents,
		mode:         viewAgentList,
		cwd:          cwd,
		quickCommands: []quickCommand{
			{Label: "List agents", Command: "list"},
			{Label: "Auto-detect and add agents", Command: "detect add"},
			{Label: "Initialize default rules", Command: "rules init"},
			{Label: "Initialize default roles", Command: "roles init"},
			{Label: "Pull instructions from current project", Command: "instructions pull ."},
			{Label: "List sessions", Command: "session list"},
			{Label: "Provider status (JSON)", Command: "status --json"},
			{Label: "Create session in current dir", Command: "session create current --dir ."},
		},
	}
	m.filteredAgents = make([]int, len(m.agents))
	for i := range m.agents {
		m.filteredAgents[i] = i
	}
	m.detected = detectAgentsForTUI()
	m.markDetectedInVault()
	m.autoAddDetectedAgents()
	m.refreshLocalInstructions()
	return m
}

func applyStartTarget(m *model, target string) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "agents":
		m.activeTab = tabAgents
		m.mode = viewAgentList
	case "instructions":
		m.activeTab = tabInstructions
		m.mode = viewInstructions
	case "rules":
		m.activeTab = tabRules
		m.mode = viewRules
	case "sessions":
		m.activeTab = tabSessions
		m.mode = viewSessions
	case "detected":
		m.activeTab = tabDetected
		m.mode = viewDetected
	case "commands":
		m.activeTab = tabCommands
		m.mode = viewCommands
	case "status":
		m.activeTab = tabStatus
		m.mode = viewStatus
	}
}

func (m *model) markDetectedInVault() {
	for i, d := range m.detected {
		m.detected[i].InVault = m.vaultHasAgentNamed(d.Name)
	}
}

func (m *model) refresh() {
	m.agents = m.vault.List()
	m.shared = m.vault.SharedConfig()
	m.instructions = m.vault.ListInstructions()
	m.detected = detectAgentsForTUI()
	m.providerCfgs = m.vault.ProviderConfigs()
	m.markDetectedInVault()
	m.autoAddDetectedAgents()
	m.refreshLocalInstructions()
	m.updateFilteredAgents()
}

func (m *model) autoAddDetectedAgents() {
	seenByPath := make(map[string]struct{}, len(m.detected))
	seenByName := make(map[string]struct{}, len(m.detected))
	for _, d := range m.detected {
		nameKey := strings.ToLower(strings.TrimSpace(d.Name))
		if nameKey == "" {
			continue
		}
		pathKey := strings.TrimSpace(d.Path)
		if pathKey != "" {
			if _, ok := seenByPath[pathKey]; ok {
				continue
			}
			seenByPath[pathKey] = struct{}{}
		}
		if _, ok := seenByName[nameKey]; ok {
			continue
		}
		seenByName[nameKey] = struct{}{}
		if m.vaultHasAgentNamed(d.Name) {
			continue
		}

		newAgent := agent.Agent{
			Name:      d.Name,
			Provider:  agent.Provider(d.Provider),
			Tags:      []string{"auto-detected"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := m.vault.Add(newAgent); err != nil {
			continue
		}
	}
	// Re-sync in-memory lists after auto-add.
	m.agents = m.vault.List()
	m.markDetectedInVault()
}

func (m *model) vaultHasAgentNamed(name string) bool {
	needle := strings.TrimSpace(name)
	if needle == "" {
		return false
	}
	for _, a := range m.vault.List() {
		if strings.EqualFold(a.Name, needle) {
			return true
		}
	}
	return false
}

func (m *model) refreshLocalInstructions() {
	var local []detectedInstructionInfo
	seen := map[string]bool{}
	reverse := map[string]string{}
	for name, filename := range agent.WellKnownInstructions {
		reverse[filepath.Clean(filename)] = name
	}

	roots := []string{m.cwd}
	sessions := m.vault.Sessions()
	for _, s := range sessions.Sessions {
		if s.ProjectDir != "" {
			roots = append(roots, s.ProjectDir)
		}
	}

	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true

		for relPath, name := range reverse {
			fullPath := filepath.Join(root, relPath)
			info, err := os.Stat(fullPath)
			if err != nil || info.IsDir() {
				continue
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			local = append(local, detectedInstructionInfo{
				Name:     name,
				Filename: relPath,
				Path:     fullPath,
				Updated:  info.ModTime(),
				Size:     len(data),
			})

			// Current working directory is canonical source for auto-sync.
			if root == m.cwd {
				inst := agent.InstructionFile{
					Name:      name,
					Filename:  relPath,
					Content:   string(data),
					UpdatedAt: info.ModTime(),
				}
				_ = m.vault.SetInstruction(inst)
			}
		}
	}

	m.localInst = local
	m.instructions = m.vault.ListInstructions()
}

func detectAgentsForTUI() []DetectedAgentInfo {
	var detected []DetectedAgentInfo

	agents := []struct {
		name     string
		provider string
	}{
		{"claude", "claude"},
		{"codex", "codex"},
		{"ollama", "ollama"},
		{"aider", "aider"},
		{"meldbot", "meldbot"},
		{"openclaw", "openclaw"},
		{"nanoclaw", "nanoclaw"},
		{"gemini", "gemini"},
		{"openai", "openai"},
		{"copilot", "custom"},
		{"github-copilot-cli", "custom"},
	}

	for _, ag := range agents {
		if path := findExecutable(ag.name); path != "" {
			version := getVersion(ag.name, "--version")
			detected = append(detected, DetectedAgentInfo{
				Name:     ag.name,
				Provider: ag.provider,
				Version:  version,
				Path:     path,
				Status:   "installed",
			})
		}
	}

	return detected
}

func (m model) Init() tea.Cmd {
	return tea.Batch(statusRefreshCmd(m.vault), tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return t }))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case editorFinishedMsg:
		return m.handleEditorFinished(msg)

	case statusRefreshMsg:
		if msg.err != nil {
			m.statusErr = msg.err.Error()
		} else {
			report := msg.report
			m.statusReport = &report
			m.statusErr = ""
		}
		return m, nil

	case cliCommandFinishedMsg:
		m.commandRunning = false
		m.commandMode = false
		m.lastCommand = msg.command
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("Command failed: %v", msg.err), true)
		} else {
			m.setStatus("Command completed", false)
		}
		m.refresh()
		return m, nil

	case gatewayFinishedMsg:
		m.gatewayRunning = false
		m.gatewayStage = gatewayResult
		if msg.err != nil {
			m.gatewayErr = msg.err.Error()
			m.gatewayResponse = ""
			m.gatewayUsage = gatewayUsage{}
			m.setStatus("Gateway execution failed", true)
		} else {
			m.gatewayErr = ""
			m.gatewayResponse = msg.response
			m.gatewayUsage = msg.usage
			m.setStatus("Gateway execution completed", false)
		}
		return m, nil

	case tea.KeyMsg:
		// Clear old status messages
		if time.Since(m.statusTime) > 3*time.Second {
			m.statusMsg = ""
		}

		// Handle delete confirmation mode
		if m.mode == viewConfirmDelete {
			return m.handleConfirmDelete(msg)
		}

		// Handle search mode
		if m.searchMode {
			return m.handleSearchInput(msg)
		}

		// Handle command mode
		if m.commandMode {
			return m.handleCommandInput(msg)
		}
		if m.activeTab == tabCommands && m.gatewayStage != gatewayOff {
			return m.handleGatewayInput(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.mode == viewAgentDetail || m.mode == viewInstructionDetail ||
				m.mode == viewRuleDetail || m.mode == viewSessionDetail || m.mode == viewDetectedDetail {
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
			if m.mode == viewAgentDetail || m.mode == viewInstructionDetail ||
				m.mode == viewRuleDetail || m.mode == viewSessionDetail || m.mode == viewDetectedDetail ||
				m.mode == viewHelp {
				m.mode = m.getModeForTab()
				return m, nil
			}

		case "tab":
			m.activeTab = (m.activeTab + 1) % numTabs
			m.mode = m.getModeForTab()
			return m, nil

		case "shift+tab":
			if m.activeTab == 0 {
				m.activeTab = tab(numTabs - 1)
			} else {
				m.activeTab--
			}
			m.mode = m.getModeForTab()
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
			m.activeTab = tabRules
			m.mode = viewRules
			return m, nil
		case "4":
			m.activeTab = tabSessions
			m.mode = viewSessions
			return m, nil
		case "5":
			m.activeTab = tabDetected
			m.mode = viewDetected
			return m, nil
		case "6":
			m.activeTab = tabCommands
			m.mode = viewCommands
			return m, nil
		case "7":
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

		case ":", ";":
			m.commandMode = true
			m.commandQuery = ""
			return m, nil

		case "g":
			if m.activeTab == tabCommands {
				m.gatewayStage = gatewaySelectAgent
				m.gatewayAgentCursor = 0
				m.gatewayPrompt = ""
				m.gatewayEffective = ""
				m.gatewayProfile = ""
				m.gatewayOptimized = false
				m.gatewayResponse = ""
				m.gatewayErr = ""
				m.gatewayUsage = gatewayUsage{}
				return m, nil
			}

		case "up", "k":
			return m.handleNavUp(), nil

		case "down", "j":
			return m.handleNavDown(), nil

		case "enter":
			updated, cmd := m.handleEnter()
			return updated, cmd

		case "d":
			return m.handleDelete()

		case "e":
			return m.handleEdit()

		case "a":
			return m.handleAdd()

		case "c":
			return m.handleConnectDetected()

		case "r":
			m.refresh()
			m.setStatus("Refreshed", false)
			return m, statusRefreshCmd(m.vault)
		}
	case time.Time:
		return m, tea.Batch(statusRefreshCmd(m.vault), tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return t }))
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

func (m *model) handleCommandInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.commandMode = false
		m.commandQuery = ""
		return m, nil
	case "backspace":
		if len(m.commandQuery) > 0 {
			m.commandQuery = m.commandQuery[:len(m.commandQuery)-1]
		}
		return m, nil
	case "enter":
		query := strings.TrimSpace(m.commandQuery)
		if query == "" {
			m.commandMode = false
			return m, nil
		}

		parts, err := parseCommandLine(query)
		if err != nil {
			m.setStatus(fmt.Sprintf("Parse error: %v", err), true)
			m.commandMode = false
			return m, nil
		}
		if len(parts) == 0 {
			m.commandMode = false
			return m, nil
		}
		if parts[0] == "agentvault" {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			m.setStatus("Provide a command, e.g. ':list' or ':rules init'", true)
			m.commandMode = false
			return m, nil
		}
		if parts[0] == "tui" || parts[0] == "--tui" || parts[0] == "-t" {
			m.setStatus("TUI launch command is not supported from inside TUI", true)
			m.commandMode = false
			return m, nil
		}

		exePath, err := os.Executable()
		if err != nil {
			m.setStatus(fmt.Sprintf("Unable to locate agentvault binary: %v", err), true)
			m.commandMode = false
			return m, nil
		}

		m.commandRunning = true
		cmd := exec.Command(exePath, parts...)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return cliCommandFinishedMsg{
				command: "agentvault " + strings.Join(parts, " "),
				err:     err,
			}
		})
	default:
		if len(msg.String()) == 1 {
			m.commandQuery += msg.String()
		}
		return m, nil
	}
}

func parseCommandLine(input string) ([]string, error) {
	var (
		args     []string
		current  strings.Builder
		inSingle bool
		inDouble bool
		escaped  bool
	)

	flushCurrent := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			flushCurrent()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, fmt.Errorf("unfinished escape")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unclosed quote")
	}
	flushCurrent()
	return args, nil
}

func (m *model) handleGatewayInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.gatewayStage {
	case gatewaySelectAgent:
		switch msg.String() {
		case "esc":
			m.gatewayStage = gatewayOff
			return m, nil
		case "up", "k":
			if m.gatewayAgentCursor > 0 {
				m.gatewayAgentCursor--
			}
			return m, nil
		case "down", "j":
			if m.gatewayAgentCursor < len(m.agents)-1 {
				m.gatewayAgentCursor++
			}
			return m, nil
		case "enter":
			if len(m.agents) == 0 {
				m.setStatus("No agents configured in vault", true)
				return m, nil
			}
			m.gatewayStage = gatewayInputPrompt
			if strings.TrimSpace(m.gatewayPrompt) == "" {
				m.gatewayPrompt = ""
			}
			return m, nil
		}
	case gatewayInputPrompt:
		switch msg.String() {
		case "esc":
			m.gatewayStage = gatewaySelectAgent
			return m, nil
		case "backspace":
			if len(m.gatewayPrompt) > 0 {
				m.gatewayPrompt = m.gatewayPrompt[:len(m.gatewayPrompt)-1]
			}
			return m, nil
		case "enter":
			if strings.TrimSpace(m.gatewayPrompt) == "" {
				m.setStatus("Prompt cannot be empty", true)
				return m, nil
			}
			if len(m.agents) == 0 || m.gatewayAgentCursor >= len(m.agents) {
				m.setStatus("Selected agent is unavailable", true)
				return m, nil
			}
			a := m.agents[m.gatewayAgentCursor]
			effective, profile := optimizePromptForGateway(m.gatewayPrompt, a, m.shared)
			m.gatewayEffective = effective
			m.gatewayProfile = profile
			m.gatewayOptimized = true
			m.gatewayStage = gatewayPreview
			return m, nil
		default:
			if len(msg.String()) == 1 {
				m.gatewayPrompt += msg.String()
			}
			return m, nil
		}
	case gatewayPreview:
		switch msg.String() {
		case "esc":
			m.gatewayStage = gatewayInputPrompt
			return m, nil
		case "n", "N":
			m.gatewayStage = gatewayInputPrompt
			return m, nil
		case "y", "Y", "enter":
			if len(m.agents) == 0 || m.gatewayAgentCursor >= len(m.agents) {
				m.setStatus("Selected agent is unavailable", true)
				return m, nil
			}
			m.gatewayRunning = true
			m.gatewayResponse = ""
			m.gatewayErr = ""
			m.gatewayUsage = gatewayUsage{}
			m.gatewayStage = gatewayRunning
			a := m.agents[m.gatewayAgentCursor]
			return m, runGatewayPromptCmd(a, m.gatewayEffective, 5*time.Minute)
		}
	case gatewayRunning:
		if msg.String() == "esc" {
			m.setStatus("Execution is in progress; wait for completion", true)
			return m, nil
		}
	case gatewayResult:
		switch msg.String() {
		case "esc":
			m.gatewayStage = gatewayOff
			return m, nil
		case "s", "S":
			m.gatewayStage = gatewaySelectAgent
			m.gatewayResponse = ""
			m.gatewayErr = ""
			return m, nil
		case "e", "E":
			m.gatewayStage = gatewayInputPrompt
			m.gatewayResponse = ""
			m.gatewayErr = ""
			return m, nil
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
	case tabRules:
		if m.mode == viewRules && m.ruleCursor > 0 {
			m.ruleCursor--
		}
	case tabSessions:
		if m.mode == viewSessions && m.sessionCursor > 0 {
			m.sessionCursor--
		}
	case tabDetected:
		if m.mode == viewDetected && m.detectedCursor > 0 {
			m.detectedCursor--
		}
	case tabCommands:
		if m.mode == viewCommands && m.commandCursor > 0 {
			m.commandCursor--
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
	case tabRules:
		if m.mode == viewRules && m.ruleCursor < len(m.shared.Rules)-1 {
			m.ruleCursor++
		}
	case tabSessions:
		sessions := m.vault.Sessions()
		if m.mode == viewSessions && m.sessionCursor < len(sessions.Sessions)-1 {
			m.sessionCursor++
		}
	case tabDetected:
		if m.mode == viewDetected && m.detectedCursor < len(m.detected)-1 {
			m.detectedCursor++
		}
	case tabCommands:
		if m.mode == viewCommands && m.commandCursor < len(m.quickCommands)-1 {
			m.commandCursor++
		}
	}
	return *m
}

func (m *model) handleEnter() (model, tea.Cmd) {
	switch m.activeTab {
	case tabAgents:
		if m.mode == viewAgentList && len(m.filteredAgents) > 0 {
			m.mode = viewAgentDetail
		}
	case tabInstructions:
		if m.mode == viewInstructions && len(m.instructions) > 0 {
			m.mode = viewInstructionDetail
		}
	case tabRules:
		if m.mode == viewRules && len(m.shared.Rules) > 0 {
			m.mode = viewRuleDetail
		}
	case tabSessions:
		sessions := m.vault.Sessions()
		if m.mode == viewSessions && len(sessions.Sessions) > 0 {
			m.mode = viewSessionDetail
		}
	case tabDetected:
		if m.mode == viewDetected && len(m.detected) > 0 {
			m.mode = viewDetectedDetail
		}
	case tabCommands:
		if m.mode == viewCommands && len(m.quickCommands) > 0 && m.commandCursor < len(m.quickCommands) {
			qc := m.quickCommands[m.commandCursor]
			parts, err := parseCommandLine(qc.Command)
			if err != nil {
				m.setStatus(fmt.Sprintf("Parse error: %v", err), true)
				return *m, nil
			}
			if len(parts) == 0 {
				return *m, nil
			}
			exePath, err := os.Executable()
			if err != nil {
				m.setStatus(fmt.Sprintf("Unable to locate agentvault binary: %v", err), true)
				return *m, nil
			}
			m.commandRunning = true
			c := exec.Command(exePath, parts...)
			return *m, tea.ExecProcess(c, func(err error) tea.Msg {
				return cliCommandFinishedMsg{
					command: "agentvault " + strings.Join(parts, " "),
					err:     err,
				}
			})
		}
	}
	return *m, nil
}

// handleDelete initiates a delete confirmation for the selected item.
func (m *model) handleDelete() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case tabAgents:
		if m.mode == viewAgentList && len(m.filteredAgents) > 0 && m.cursor < len(m.filteredAgents) {
			a := m.agents[m.filteredAgents[m.cursor]]
			m.deleteTarget = a.Name
			m.deleteType = "agent"
			m.prevMode = m.mode
			m.mode = viewConfirmDelete
		}
	case tabInstructions:
		if m.mode == viewInstructions && len(m.instructions) > 0 && m.instCursor < len(m.instructions) {
			m.deleteTarget = m.instructions[m.instCursor].Name
			m.deleteType = "instruction"
			m.prevMode = m.mode
			m.mode = viewConfirmDelete
		}
	case tabRules:
		if m.mode == viewRules && len(m.shared.Rules) > 0 && m.ruleCursor < len(m.shared.Rules) {
			m.deleteTarget = m.shared.Rules[m.ruleCursor].Name
			m.deleteType = "rule"
			m.prevMode = m.mode
			m.mode = viewConfirmDelete
		}
	}
	return m, nil
}

// handleConfirmDelete processes Y/N on the delete confirmation screen.
func (m *model) handleConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		var err error
		switch m.deleteType {
		case "agent":
			err = m.vault.Remove(m.deleteTarget)
		case "instruction":
			err = m.vault.RemoveInstruction(m.deleteTarget)
		case "rule":
			shared := m.vault.SharedConfig()
			idx := -1
			for i, r := range shared.Rules {
				if r.Name == m.deleteTarget {
					idx = i
					break
				}
			}
			if idx >= 0 {
				shared.Rules = append(shared.Rules[:idx], shared.Rules[idx+1:]...)
				err = m.vault.SetSharedConfig(shared)
			}
		}
		if err != nil {
			m.setStatus(fmt.Sprintf("Error: %v", err), true)
		} else {
			m.setStatus(fmt.Sprintf("Deleted %s %q", m.deleteType, m.deleteTarget), false)
			m.refresh()
			// Reset cursors to stay in bounds
			if m.cursor >= len(m.filteredAgents) && m.cursor > 0 {
				m.cursor--
			}
			if m.instCursor >= len(m.instructions) && m.instCursor > 0 {
				m.instCursor--
			}
			if m.ruleCursor >= len(m.shared.Rules) && m.ruleCursor > 0 {
				m.ruleCursor--
			}
		}
		m.mode = m.prevMode
	case "n", "N", "esc":
		m.mode = m.prevMode
	}
	return m, nil
}

// handleEdit opens the selected instruction in an external editor.
func (m *model) handleEdit() (tea.Model, tea.Cmd) {
	if m.activeTab != tabInstructions || m.mode != viewInstructions {
		return m, nil
	}
	if len(m.instructions) == 0 || m.instCursor >= len(m.instructions) {
		return m, nil
	}

	inst := m.instructions[m.instCursor]
	editor := findEditorPath()
	if editor == "" {
		m.setStatus("No editor found (set $EDITOR, or install nano/vi)", true)
		return m, nil
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "agentvault-*.md")
	if err != nil {
		m.setStatus(fmt.Sprintf("Error: %v", err), true)
		return m, nil
	}
	if _, err := tmpFile.WriteString(inst.Content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		m.setStatus(fmt.Sprintf("Error: %v", err), true)
		return m, nil
	}
	tmpFile.Close()

	m.editingInst = inst.Name
	m.editTmpPath = tmpFile.Name()

	// Launch editor via tea.ExecProcess
	c := exec.Command(editor, tmpFile.Name())
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err: err, tmpPath: tmpFile.Name(), name: inst.Name}
	})
}

// handleEditorFinished processes the result after the editor closes.
func (m *model) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.tmpPath)

	if msg.err != nil {
		m.setStatus(fmt.Sprintf("Editor error: %v", msg.err), true)
		return m, nil
	}

	edited, err := os.ReadFile(msg.tmpPath)
	if err != nil {
		m.setStatus(fmt.Sprintf("Read error: %v", err), true)
		return m, nil
	}

	inst, ok := m.vault.GetInstruction(msg.name)
	if !ok {
		m.setStatus(fmt.Sprintf("Instruction %q not found", msg.name), true)
		return m, nil
	}

	newContent := string(edited)
	if newContent == inst.Content {
		m.setStatus("No changes", false)
		return m, nil
	}

	inst.Content = newContent
	inst.UpdatedAt = time.Now()
	if err := m.vault.SetInstruction(inst); err != nil {
		m.setStatus(fmt.Sprintf("Save error: %v", err), true)
		return m, nil
	}

	m.refresh()
	m.setStatus(fmt.Sprintf("Updated %q (%d bytes)", msg.name, len(newContent)), false)
	return m, nil
}

// handleAdd adds a detected agent to the vault (Detected tab).
func (m *model) handleAdd() (tea.Model, tea.Cmd) {
	if m.activeTab != tabDetected || (m.mode != viewDetected && m.mode != viewDetectedDetail) {
		return m, nil
	}
	if len(m.detected) == 0 || m.detectedCursor >= len(m.detected) {
		return m, nil
	}

	d := m.detected[m.detectedCursor]
	if d.InVault {
		m.setStatus(fmt.Sprintf("%s is already in vault", d.Name), false)
		return m, nil
	}

	newAgent := agent.Agent{
		Name:      d.Name,
		Provider:  agent.Provider(d.Provider),
		Tags:      []string{"auto-detected"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := m.vault.Add(newAgent); err != nil {
		m.setStatus(fmt.Sprintf("Error: %v", err), true)
		return m, nil
	}

	m.refresh()
	m.setStatus(fmt.Sprintf("Added %s to vault", d.Name), false)
	return m, nil
}

func (m *model) handleConnectDetected() (tea.Model, tea.Cmd) {
	if m.activeTab != tabDetected {
		return m, nil
	}
	if len(m.detected) == 0 || m.detectedCursor >= len(m.detected) {
		return m, nil
	}
	d := m.detected[m.detectedCursor]
	if !d.InVault {
		_, _ = m.handleAdd()
		m.refresh()
	}

	target := d.Name
	idx := -1
	for i, a := range m.agents {
		if a.Name == target {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.setStatus(fmt.Sprintf("Could not resolve detected agent %s in vault", d.Name), true)
		return m, nil
	}

	m.activeTab = tabCommands
	m.mode = viewCommands
	m.gatewayStage = gatewaySelectAgent
	m.gatewayAgentCursor = idx
	m.gatewayPrompt = ""
	m.gatewayEffective = ""
	m.gatewayResponse = ""
	m.gatewayErr = ""
	m.gatewayUsage = gatewayUsage{}
	m.setStatus(fmt.Sprintf("Connected to %s", d.Name), false)
	return m, nil
}

// findEditorPath returns the path to a suitable text editor.
func findEditorPath() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		if path, err := exec.LookPath(editor); err == nil {
			return path
		}
	}
	for _, name := range []string{"nano", "vi", "vim"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func (m *model) getModeForTab() viewMode {
	switch m.activeTab {
	case tabAgents:
		return viewAgentList
	case tabInstructions:
		return viewInstructions
	case tabRules:
		return viewRules
	case tabSessions:
		return viewSessions
	case tabDetected:
		return viewDetected
	case tabCommands:
		return viewCommands
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

// ──────────────────────────────────────────────────────────────────
// View rendering
// ──────────────────────────────────────────────────────────────────

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
	case viewRules:
		b.WriteString(m.renderRules())
	case viewRuleDetail:
		b.WriteString(m.renderRuleDetail())
	case viewSessions:
		b.WriteString(m.renderSessions())
	case viewSessionDetail:
		b.WriteString(m.renderSessionDetail())
	case viewDetected:
		b.WriteString(m.renderDetected())
	case viewDetectedDetail:
		b.WriteString(m.renderDetectedDetail())
	case viewCommands:
		b.WriteString(m.renderCommands())
	case viewStatus:
		b.WriteString(m.renderStatus())
	case viewHelp:
		b.WriteString(m.renderHelp())
	case viewConfirmDelete:
		b.WriteString(m.renderConfirmDelete())
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

	tabs := []string{"Agents", "Instructions", "Rules", "Sessions", "Detected", "Commands", "Status"}
	var tabBar strings.Builder
	for i, t := range tabs {
		if tab(i) == m.activeTab {
			tabBar.WriteString(tabActiveStyle.Render(fmt.Sprintf("%d:%s", i+1, t)))
		} else {
			tabBar.WriteString(tabInactiveStyle.Render(fmt.Sprintf("%d:%s", i+1, t)))
		}
		tabBar.WriteString(" ")
	}

	return fmt.Sprintf("%s  %s", title, tabBar.String())
}

func (m model) renderAgentList() string {
	var b strings.Builder

	// Search bar
	if m.searchMode {
		b.WriteString(subtitleStyle.Render("Search: "))
		b.WriteString(m.searchQuery)
		b.WriteString("_")
		b.WriteString("\n\n")
	} else if m.searchQuery != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf("Filter: %s", m.searchQuery)))
		b.WriteString("\n\n")
	}

	if len(m.filteredAgents) == 0 {
		if m.searchQuery != "" {
			b.WriteString(dimStyle.Render("  No agents match the filter."))
		} else {
			b.WriteString(dimStyle.Render("  No agents configured. Use 'agentvault add' or detect tab."))
		}
		b.WriteString("\n")
		return b.String()
	}

	// Column headers
	header := fmt.Sprintf("  %-20s  %-10s  %-20s  %-12s  %s", "NAME", "PROVIDER", "MODEL", "ROLE", "TAGS")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 80)))
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
		role := a.Role
		if role == "" {
			role = "-"
		}
		line := fmt.Sprintf("%s%-20s  %-10s  %-20s  %-12s  %s",
			cursor, truncate(a.Name, 20), a.Provider, truncate(a.Model, 20), truncate(role, 12), tags)
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
	field("Role", a.Role)

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

	if len(a.DisabledRules) > 0 {
		field("Disabled Rules", strings.Join(a.DisabledRules, ", "))
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
			b.WriteString(fmt.Sprintf("    - %s: %s %s%s\n", s.Name, s.Command, strings.Join(s.Args, " "), origin))
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

	b.WriteString(dimStyle.Render("  Auto-source: current project + session project dirs (AGENTS.md / CLAUDE.md / codex.md / .github/copilot-instructions.md)"))
	b.WriteString("\n\n")

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

func (m model) renderRules() string {
	var b strings.Builder
	b.WriteString(dimStyle.Render("  Rules are unified in vault; local instruction files are auto-imported into Instructions."))
	b.WriteString("\n\n")

	rules := m.shared.Rules
	if len(rules) == 0 {
		b.WriteString(dimStyle.Render("  No rules configured."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Use 'agentvault rules init' to add default rules."))
		b.WriteString("\n")
		return b.String()
	}

	// Sort by priority for display
	sorted := make([]agent.UnifiedRule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	header := fmt.Sprintf("  %-25s  %-10s  %-8s  %-8s  %s", "NAME", "CATEGORY", "PRIORITY", "STATUS", "DESCRIPTION")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 85)))
	b.WriteString("\n")

	for i, r := range sorted {
		cursor := "  "
		style := normalStyle
		if i == m.ruleCursor {
			cursor = "> "
			style = selectedStyle
		}

		status := successStyle.Render("ON ")
		if !r.Enabled {
			status = dimStyle.Render("OFF")
		}

		line := fmt.Sprintf("%s%-25s  %-10s  %-8d  %s  %s",
			cursor, truncate(r.Name, 25), truncate(r.Category, 10), r.Priority, status, truncate(r.Description, 30))
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderRuleDetail() string {
	rules := m.shared.Rules
	if m.ruleCursor >= len(rules) {
		return ""
	}

	// Sort to match display order
	sorted := make([]agent.UnifiedRule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	r := sorted[m.ruleCursor]

	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("Rule: %s", r.Name)))
	b.WriteString("\n\n")

	field := func(label, value string) {
		if value == "" {
			value = dimStyle.Render("(not set)")
		}
		b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render(fmt.Sprintf("%-14s", label+":")), value))
	}

	field("Category", r.Category)
	field("Priority", fmt.Sprintf("%d", r.Priority))
	if r.Enabled {
		field("Status", successStyle.Render("Enabled"))
	} else {
		field("Status", warnStyle.Render("Disabled"))
	}
	field("Description", r.Description)

	b.WriteString(fmt.Sprintf("\n  %s\n", labelStyle.Render("Content:")))
	for _, line := range strings.Split(r.Content, "\n") {
		b.WriteString(fmt.Sprintf("    %s\n", line))
	}

	if !r.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("\n  %s  %s\n", dimStyle.Render("Created:"), r.CreatedAt.Format("2006-01-02 15:04")))
	}
	if !r.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  %s  %s\n", dimStyle.Render("Updated:"), r.UpdatedAt.Format("2006-01-02 15:04")))
	}

	return b.String()
}

func (m model) renderSessions() string {
	var b strings.Builder

	sessions := m.vault.Sessions()
	if len(sessions.Sessions) == 0 {
		b.WriteString(dimStyle.Render("  No sessions configured."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Use 'agentvault session create' to create one."))
		b.WriteString("\n")
		return b.String()
	}

	header := fmt.Sprintf("  %-20s  %-12s  %-10s  %-30s  %s", "NAME", "STATUS", "AGENTS", "PROJECT", "ROLE")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 85)))
	b.WriteString("\n")

	for i, s := range sessions.Sessions {
		cursor := "  "
		style := normalStyle
		if i == m.sessionCursor {
			cursor = "> "
			style = selectedStyle
		}

		active := ""
		if s.ID == sessions.ActiveSession {
			active = " *"
		}

		projectDir := s.ProjectDir
		if len(projectDir) > 28 {
			projectDir = "..." + projectDir[len(projectDir)-25:]
		}

		role := s.ActiveRole
		if role == "" {
			role = "-"
		}

		line := fmt.Sprintf("%s%-20s  %-12s  %-10d  %-30s  %s%s",
			cursor, truncate(s.Name, 20), s.Status, len(s.Agents), projectDir, role, active)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  * = active session"))
	b.WriteString("\n")

	if m.statusReport != nil && len(sessions.Sessions) > 0 {
		b.WriteString("\n")
		b.WriteString(labelStyle.Render("  Live provider usage (running sessions):"))
		b.WriteString("\n")

		agentProvider := map[string]string{}
		for _, a := range m.agents {
			agentProvider[a.Name] = string(a.Provider)
		}

		runningFound := false
		for _, s := range sessions.Sessions {
			if s.Status != agent.SessionStatusRunning {
				continue
			}
			runningFound = true
			b.WriteString(fmt.Sprintf("    %s\n", subtitleStyle.Render(s.Name)))
			for _, sa := range s.Agents {
				if !sa.Enabled {
					continue
				}
				providerKey := agentProvider[sa.Name]
				ps, ok := m.statusReport.Providers[providerKey]
				if !ok {
					b.WriteString(fmt.Sprintf("      - %s: provider status unavailable\n", sa.Name))
					continue
				}
				line := fmt.Sprintf("      - %s (%s)", sa.Name, providerKey)
				if ps.Tokens != nil {
					line += fmt.Sprintf(" tokens[in:%d out:%d total:%d]",
						ps.Tokens.InputTokens, ps.Tokens.OutputTokens, ps.Tokens.TotalTokens)
				}
				if ps.Quota != nil && ps.Quota.Primary != nil {
					line += fmt.Sprintf(" quota[used:%.1f%% rem:%.1f%%]",
						ps.Quota.Primary.UsedPercent, ps.Quota.Primary.RemainingPercent)
				}
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
		if !runningFound {
			b.WriteString(dimStyle.Render("    No running sessions."))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m model) renderSessionDetail() string {
	sessions := m.vault.Sessions()
	if m.sessionCursor >= len(sessions.Sessions) {
		return ""
	}
	s := sessions.Sessions[m.sessionCursor]

	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("Session: %s", s.Name)))
	b.WriteString("\n\n")

	field := func(label, value string) {
		if value == "" {
			value = dimStyle.Render("(not set)")
		}
		b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render(fmt.Sprintf("%-14s", label+":")), value))
	}

	field("ID", s.ID)
	field("Status", string(s.Status))
	field("Project", s.ProjectDir)
	field("Role", s.ActiveRole)

	b.WriteString(fmt.Sprintf("\n  %s\n", labelStyle.Render("Agents:")))
	for _, a := range s.Agents {
		status := dimStyle.Render("○")
		if a.Enabled {
			status = successStyle.Render("●")
		}
		role := ""
		if a.Role != "" {
			role = fmt.Sprintf(" [%s]", a.Role)
		}
		task := ""
		if a.Task != "" {
			task = fmt.Sprintf(" - %s", a.Task)
		}
		b.WriteString(fmt.Sprintf("    %s %s%s%s\n", status, a.Name, role, task))
	}

	if !s.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("\n  %s  %s\n", dimStyle.Render("Created:"), s.CreatedAt.Format("2006-01-02 15:04")))
	}
	if !s.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  %s  %s\n", dimStyle.Render("Updated:"), s.UpdatedAt.Format("2006-01-02 15:04")))
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
	b.WriteString(dimStyle.Render("  Press 'a' to add, 'enter' for details, 'c' to connect to prompt gateway."))
	b.WriteString("\n")

	return b.String()
}

func (m model) renderDetectedDetail() string {
	if len(m.detected) == 0 || m.detectedCursor >= len(m.detected) {
		return dimStyle.Render("No detected agent selected.")
	}
	d := m.detected[m.detectedCursor]

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Detected Agent: %s", d.Name)))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render("Provider:"), d.Provider))
	b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render("Version:"), d.Version))
	b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render("Path:"), d.Path))
	if d.ConfigDir != "" {
		b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render("Config Dir:"), d.ConfigDir))
	}
	status := "not in vault"
	if d.InVault {
		status = "in vault"
	}
	b.WriteString(fmt.Sprintf("  %s  %s\n", labelStyle.Render("Vault Status:"), status))

	if m.statusReport != nil {
		if ps, ok := m.statusReport.Providers[d.Provider]; ok {
			b.WriteString("\n")
			b.WriteString(labelStyle.Render("  Usage:"))
			b.WriteString("\n")
			if ps.Tokens != nil {
				b.WriteString(fmt.Sprintf("    Tokens: input=%d output=%d total=%d\n",
					ps.Tokens.InputTokens, ps.Tokens.OutputTokens, ps.Tokens.TotalTokens))
			}
			if ps.Quota != nil && ps.Quota.Primary != nil {
				b.WriteString(fmt.Sprintf("    Primary quota: used=%.1f%% remaining=%.1f%%\n",
					ps.Quota.Primary.UsedPercent, ps.Quota.Primary.RemainingPercent))
			}
			if ps.Error != "" {
				b.WriteString(fmt.Sprintf("    Note: %s\n", ps.Error))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  c: connect to this agent in Prompt Gateway"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  a: add to vault  esc: back"))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderCommands() string {
	if m.gatewayStage != gatewayOff {
		return m.renderGatewayFlow()
	}

	var b strings.Builder

	b.WriteString(subtitleStyle.Render("Prompt + Command Center"))
	b.WriteString("\n\n")
	b.WriteString("  Select a command and press Enter. No need to type every command.\n")
	b.WriteString("  Use ':' only for advanced/custom commands.\n\n")

	b.WriteString(labelStyle.Render("  Quick command menu:"))
	b.WriteString("\n")
	for i, qc := range m.quickCommands {
		cursor := "  "
		style := normalStyle
		if i == m.commandCursor {
			cursor = "> "
			style = selectedStyle
		}
		line := fmt.Sprintf("%s%-40s  :%s", cursor, truncate(qc.Label, 40), qc.Command)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  How to use:"))
	b.WriteString("\n")
	b.WriteString("    1. Use j/k to select an action and Enter to run\n")
	b.WriteString("    2. Press 'g' to open prompt gateway\n")
	b.WriteString("    3. Press ':' only when you need a custom command\n")
	b.WriteString("    4. Press 'r' to refresh data and usage\n")

	if m.lastCommand != "" {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Last command: " + m.lastCommand))
		b.WriteString("\n")
	}

	if m.commandRunning {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("  Running command..."))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderGatewayFlow() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Prompt Gateway"))
	b.WriteString("\n\n")

	if len(m.agents) == 0 {
		b.WriteString(dimStyle.Render("  No configured agents. Add one in the Agents tab first."))
		b.WriteString("\n")
		return b.String()
	}

	switch m.gatewayStage {
	case gatewaySelectAgent:
		b.WriteString(labelStyle.Render("  Step 1: Select agent"))
		b.WriteString("\n\n")
		for i, a := range m.agents {
			cursor := "  "
			style := normalStyle
			if i == m.gatewayAgentCursor {
				cursor = "> "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%-20s  %-10s  %s", cursor, truncate(a.Name, 20), a.Provider, truncate(a.Model, 30))
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
	case gatewayInputPrompt:
		a := m.agents[m.gatewayAgentCursor]
		b.WriteString(labelStyle.Render("  Step 2: Enter prompt"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Agent: %s (%s)", a.Name, a.Provider)))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(m.gatewayPrompt)
		b.WriteString("_\n")
	case gatewayPreview:
		a := m.agents[m.gatewayAgentCursor]
		b.WriteString(labelStyle.Render("  Step 3: Review rewritten prompt"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Agent: %s (%s), profile: %s", a.Name, a.Provider, m.gatewayProfile)))
		b.WriteString("\n\n")
		for _, line := range strings.Split(m.gatewayEffective, "\n") {
			b.WriteString("  ")
			b.WriteString(truncate(line, 110))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Confirm with y/enter, or n to edit prompt."))
		b.WriteString("\n")
	case gatewayRunning:
		b.WriteString(warnStyle.Render("  Step 4: Running prompt..."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Waiting for agent response."))
		b.WriteString("\n")
	case gatewayResult:
		b.WriteString(labelStyle.Render("  Step 5: Response"))
		b.WriteString("\n\n")
		if m.gatewayErr != "" {
			b.WriteString(errorStyle.Render("  Error: " + m.gatewayErr))
			b.WriteString("\n")
		} else {
			for _, line := range strings.Split(m.gatewayResponse, "\n") {
				b.WriteString("  ")
				b.WriteString(truncate(line, 120))
				b.WriteString("\n")
			}
			if m.gatewayUsage.TotalTokens > 0 || m.gatewayUsage.InputTokens > 0 || m.gatewayUsage.OutputTokens > 0 {
				b.WriteString("\n")
				b.WriteString(dimStyle.Render(fmt.Sprintf("  Tokens: input=%d output=%d total=%d",
					m.gatewayUsage.InputTokens, m.gatewayUsage.OutputTokens, m.gatewayUsage.TotalTokens)))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Press 's' to switch agent, 'e' to edit prompt, or esc to exit gateway."))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) renderStatus() string {
	var b strings.Builder

	b.WriteString(subtitleStyle.Render("System Status"))
	b.WriteString("\n\n")
	if m.statusErr != "" {
		b.WriteString(errorStyle.Render("  Live usage error: " + m.statusErr))
		b.WriteString("\n\n")
	}

	// Vault info
	b.WriteString(labelStyle.Render("  Vault:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("    Path:         %s\n", m.vault.Path()))
	b.WriteString(fmt.Sprintf("    Agents:       %d\n", len(m.agents)))
	b.WriteString(fmt.Sprintf("    Instructions: %d\n", len(m.instructions)))
	b.WriteString(fmt.Sprintf("    Rules:        %d\n", len(m.shared.Rules)))
	b.WriteString(fmt.Sprintf("    Roles:        %d\n", len(m.shared.Roles)))
	b.WriteString(fmt.Sprintf("    MCP Servers:  %d (shared)\n", len(m.shared.MCPServers)))

	sessions := m.vault.Sessions()
	b.WriteString(fmt.Sprintf("    Sessions:     %d\n", len(sessions.Sessions)))

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
		status := successStyle.Render("v")
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

	if len(m.localInst) > 0 {
		b.WriteString("\n")
		b.WriteString(labelStyle.Render("  Local Instruction Files (auto-detected):"))
		b.WriteString("\n")
		for _, li := range m.localInst {
			b.WriteString(fmt.Sprintf("    - %s -> %s\n", li.Name, li.Path))
		}
	}

	return b.String()
}

func (m model) renderConfirmDelete() string {
	var b strings.Builder
	b.WriteString(warnStyle.Render(fmt.Sprintf("  Delete %s %q?", m.deleteType, m.deleteTarget)))
	b.WriteString("\n\n")
	b.WriteString("  Press ")
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(" to confirm, ")
	b.WriteString(selectedStyle.Render("n"))
	b.WriteString(" or ")
	b.WriteString(selectedStyle.Render("esc"))
	b.WriteString(" to cancel.")
	b.WriteString("\n")
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
				{"Tab", "Next tab"},
				{"Shift+Tab", "Previous tab"},
				{"1-7", "Jump to tab"},
				{"j/k or Up/Down", "Move cursor"},
				{"Enter", "View details"},
				{"Esc", "Back / Close"},
				{"q", "Quit"},
			},
		},
		{
			title: "Agents Tab",
			keys: [][]string{
				{"/", "Search/filter agents"},
				{"d", "Delete selected agent"},
			},
		},
		{
			title: "Instructions Tab",
			keys: [][]string{
				{"e", "Edit in external editor"},
				{"d", "Delete selected instruction"},
			},
		},
		{
			title: "Rules Tab",
			keys: [][]string{
				{"d", "Delete selected rule"},
			},
		},
		{
			title: "Detected Tab",
			keys: [][]string{
				{"a", "Add agent to vault"},
				{"c", "Connect selected agent to Prompt Gateway"},
				{"enter", "Open detected agent details"},
			},
		},
		{
			title: "General",
			keys: [][]string{
				{":", "Run custom CLI command"},
				{"g", "Prompt gateway (Commands tab)"},
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
	case viewAgentDetail, viewInstructionDetail, viewRuleDetail, viewSessionDetail:
		help = "esc: back  q: quit"
	case viewHelp:
		help = "esc: back  q: quit"
	case viewConfirmDelete:
		help = "y: confirm  n/esc: cancel"
	default:
		if m.searchMode {
			help = "enter: apply filter  esc: cancel"
		} else if m.commandMode {
			help = "enter: run command  esc: cancel"
		} else {
			switch m.activeTab {
			case tabAgents:
				help = "tab: tabs  /: search  d: delete  : run cmd  enter: detail  ?: help  q: quit"
			case tabInstructions:
				help = "tab: tabs  e: edit  d: delete  : run cmd  enter: detail  ?: help  q: quit"
			case tabRules:
				help = "tab: tabs  d: delete  : run cmd  enter: detail  ?: help  q: quit"
			case tabDetected:
				if m.mode == viewDetectedDetail {
					help = "c: connect  a: add  esc: back  q: quit"
				} else {
					help = "tab: tabs  enter: details  c: connect  a: add  : run cmd  ?: help  q: quit"
				}
			case tabCommands:
				if m.gatewayStage != gatewayOff {
					switch m.gatewayStage {
					case gatewaySelectAgent:
						help = "j/k: select agent  enter: next  esc: close gateway"
					case gatewayInputPrompt:
						help = "type: prompt  enter: rewrite  backspace: edit  esc: back"
					case gatewayPreview:
						help = "y/enter: run  n: edit  esc: back"
					case gatewayRunning:
						help = "running..."
					case gatewayResult:
						help = "s: switch agent  e: edit prompt  esc: close gateway"
					}
				} else {
					help = "tab: tabs  j/k: select action  enter: run  g: gateway  : custom cmd  r: refresh  ?: help  q: quit"
				}
			default:
				help = "tab: switch tabs  : run cmd  ?: help  q: quit"
			}
		}
	}
	if m.commandMode {
		help += "\n  command> " + m.commandQuery + "_"
	}
	return helpStyle.Render("  " + help)
}

func runGatewayPromptCmd(a agent.Agent, prompt string, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		resp, usage, err := executeGatewayPrompt(a, prompt, timeout)
		return gatewayFinishedMsg{
			response: resp,
			usage:    usage,
			err:      err,
		}
	}
}

func executeGatewayPrompt(a agent.Agent, prompt string, timeout time.Duration) (string, gatewayUsage, error) {
	switch a.Provider {
	case agent.ProviderOllama:
		return executeGatewayOllama(a, prompt, timeout)
	case agent.ProviderCodex:
		return executeGatewayCodex(a, prompt, timeout)
	case agent.ProviderClaude:
		return executeGatewayClaude(a, prompt, timeout)
	default:
		return "", gatewayUsage{}, fmt.Errorf("provider %q is not supported in TUI gateway yet", a.Provider)
	}
}

func executeGatewayOllama(a agent.Agent, prompt string, timeout time.Duration) (string, gatewayUsage, error) {
	if strings.TrimSpace(a.Model) == "" {
		return "", gatewayUsage{}, errors.New("ollama agent requires a model")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	payload := map[string]any{
		"model":  a.Model,
		"prompt": prompt,
		"stream": false,
	}
	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", gatewayUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", gatewayUsage{}, fmt.Errorf("calling ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return "", gatewayUsage{}, fmt.Errorf("ollama error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Response        string `json:"response"`
		PromptEvalCount int64  `json:"prompt_eval_count"`
		EvalCount       int64  `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", gatewayUsage{}, fmt.Errorf("decoding ollama response: %w", err)
	}
	return strings.TrimSpace(out.Response), gatewayUsage{
		InputTokens:  out.PromptEvalCount,
		OutputTokens: out.EvalCount,
		TotalTokens:  out.PromptEvalCount + out.EvalCount,
	}, nil
}

func statusRefreshCmd(v *vault.Vault) tea.Cmd {
	return func() tea.Msg {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return statusRefreshMsg{err: err}
		}
		report := statuspkg.BuildReport(v, homeDir)
		return statusRefreshMsg{report: report}
	}
}

func executeGatewayCodex(a agent.Agent, prompt string, timeout time.Duration) (string, gatewayUsage, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return "", gatewayUsage{}, errors.New("codex binary not found in PATH")
	}
	tmp, err := os.CreateTemp("", "agentvault-tui-codex-*.txt")
	if err != nil {
		return "", gatewayUsage{}, err
	}
	_ = tmp.Close()
	defer os.Remove(tmp.Name())

	args := []string{"exec", "--json", "--output-last-message", tmp.Name()}
	if strings.TrimSpace(a.Model) != "" {
		args = append(args, "--model", a.Model)
	}
	args = append(args, prompt)

	cmd := exec.Command("codex", args...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(a.APIKey) != "" {
		cmd.Env = append(cmd.Env, "OPENAI_API_KEY="+a.APIKey)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := runCommandWithTimeout(cmd, timeout); err != nil {
		return "", gatewayUsage{}, fmt.Errorf("codex failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	usage := parseGatewayCodexUsage(stdout.String())
	respBytes, _ := os.ReadFile(tmp.Name())
	response := strings.TrimSpace(string(respBytes))
	if response == "" {
		response = strings.TrimSpace(stdout.String())
	}
	return response, usage, nil
}

func executeGatewayClaude(a agent.Agent, prompt string, timeout time.Duration) (string, gatewayUsage, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", gatewayUsage{}, errors.New("claude binary not found in PATH")
	}

	args := []string{"-p", "--output-format", "json"}
	if strings.TrimSpace(a.Model) != "" {
		args = append(args, "--model", a.Model)
	}
	args = append(args, prompt)

	cmd := exec.Command("claude", args...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(a.APIKey) != "" {
		cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+a.APIKey)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := runCommandWithTimeout(cmd, timeout); err != nil {
		return "", gatewayUsage{}, fmt.Errorf("claude failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return "", gatewayUsage{}, errors.New("claude returned empty output")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw, gatewayUsage{}, nil
	}
	response := extractGatewayString(decoded, []string{"result", "response", "output", "content", "text"})
	if response == "" {
		response = raw
	}
	input := extractGatewayInt64(decoded, []string{"input_tokens", "prompt_tokens", "usage.input_tokens", "usage.prompt_tokens"})
	output := extractGatewayInt64(decoded, []string{"output_tokens", "completion_tokens", "usage.output_tokens", "usage.completion_tokens"})
	total := extractGatewayInt64(decoded, []string{"total_tokens", "usage.total_tokens"})
	if total == 0 && (input > 0 || output > 0) {
		total = input + output
	}
	return strings.TrimSpace(response), gatewayUsage{
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  total,
	}, nil
}

func runCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if timeout <= 0 {
		return cmd.Run()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("timed out after %s", timeout)
	}
}

func parseGatewayCodexUsage(raw string) gatewayUsage {
	usage := gatewayUsage{}
	type evt struct {
		Payload struct {
			Type string `json:"type"`
			Info struct {
				TotalTokenUsage struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
					TotalTokens  int64 `json:"total_tokens"`
				} `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	s := bufio.NewScanner(strings.NewReader(raw))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e evt
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Payload.Type != "token_count" {
			continue
		}
		usage = gatewayUsage{
			InputTokens:  e.Payload.Info.TotalTokenUsage.InputTokens,
			OutputTokens: e.Payload.Info.TotalTokenUsage.OutputTokens,
			TotalTokens:  e.Payload.Info.TotalTokenUsage.TotalTokens,
		}
	}
	return usage
}

func optimizePromptForGateway(original string, a agent.Agent, shared agent.SharedConfig) (string, string) {
	prompt := strings.TrimSpace(original)
	if prompt == "" {
		return original, "none"
	}
	profile := "generic"
	switch a.Provider {
	case agent.ProviderOllama:
		profile = "ollama"
	case agent.ProviderCodex, agent.ProviderAider, agent.ProviderMeldbot, agent.ProviderOpenclaw, agent.ProviderNanoclaw:
		profile = "codex"
	case agent.ProviderClaude:
		profile = "claude"
	default:
		name := strings.ToLower(a.Name + " " + a.Model)
		if strings.Contains(name, "copilot") {
			profile = "copilot"
		}
	}

	var rules []string
	for _, r := range shared.Rules {
		if r.Enabled {
			rules = append(rules, "- "+r.Content)
		}
	}
	role := a.Role
	if role == "" {
		role = "software engineer"
	}

	var b strings.Builder
	switch profile {
	case "ollama":
		b.WriteString("You are an expert assistant. Keep responses concise and implementation-focused.\n\n")
	case "codex", "copilot":
		b.WriteString("You are a senior coding agent. Prioritize correctness, minimal diffs, and runnable outputs.\n\n")
	case "claude":
		b.WriteString("You are a careful engineering assistant. Explain assumptions briefly and provide precise changes.\n\n")
	default:
		b.WriteString("You are an expert assistant. Respond with concise, actionable output.\n\n")
	}
	b.WriteString("## Task\n")
	b.WriteString(prompt)
	b.WriteString("\n\n## Context\n")
	b.WriteString("- Intended role: ")
	b.WriteString(role)
	b.WriteString("\n")
	if a.Model != "" {
		b.WriteString("- Model: ")
		b.WriteString(a.Model)
		b.WriteString("\n")
	}
	if len(rules) > 0 {
		b.WriteString("\n## Constraints\n")
		for i, r := range rules {
			if i >= 8 {
				break
			}
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Output format\n")
	b.WriteString("1. Short answer first.\n")
	b.WriteString("2. Concrete steps/changes next.\n")
	b.WriteString("3. Call out assumptions and risks.\n")
	return b.String(), profile
}

func extractGatewayString(data map[string]any, paths []string) string {
	for _, p := range paths {
		if v, ok := lookupGatewayPath(data, p); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func extractGatewayInt64(data map[string]any, paths []string) int64 {
	for _, p := range paths {
		if v, ok := lookupGatewayPath(data, p); ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			case json.Number:
				i, _ := n.Int64()
				return i
			}
		}
	}
	return 0
}

func lookupGatewayPath(data map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = data
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// Run starts the TUI application with an unlocked vault.
func Run() error {
	return RunWithTarget("")
}

// RunWithTarget starts the TUI and opens the requested target tab.
func RunWithTarget(target string) error {
	return RunWithVaultTarget(nil, target)
}

// RunWithVault starts the TUI with a pre-unlocked vault.
func RunWithVault(v *vault.Vault) error {
	return RunWithVaultTarget(v, "")
}

// RunWithVaultTarget starts the TUI with a pre-unlocked vault and target tab.
func RunWithVaultTarget(v *vault.Vault, target string) error {
	var m tea.Model
	if v != nil {
		start := initialModel(v)
		applyStartTarget(&start, target)
		m = start
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
		"  Run 'agentvault init' first, then 'agentvault --tui' to use the TUI.\n\n" +
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
