package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/nikolareljin/agentvault/internal/agent"
)

// keyRegistry holds every key binding the TUI shows. The help bar and the
// '?' screen are rendered from it, so a key is described in one place.
type keyRegistry struct {
	Actions, Help, Quit, Back, Tabs, PrevTab, JumpTab, Up, Down, Detail key.Binding
	Search, Delete, Edit, Command, Refresh, Gateway, AddDetected        key.Binding
	Connect, Backend                                                    key.Binding

	// Actions menu shortcuts.
	ActAdd, ActSwitch, ActPrompt, ActUsage, ActCommand key.Binding

	// Flows.
	Select, Submit, NextField, PrevField, CycleNext, CyclePrev, ClearAgent key.Binding
	GwRun, GwReview, GwConfirm, GwRedo, GwSwitch, GwEdit, Type, Erase      key.Binding
}

func bind(keyNames []string, helpKey, desc string) key.Binding {
	if keyNames == nil {
		// Display-only entry. bubbles/help hides bindings with nil keys.
		keyNames = []string{}
	}
	return key.NewBinding(key.WithKeys(keyNames...), key.WithHelp(helpKey, desc))
}

// as is b with a description for one context; the keys stay the same.
func as(b key.Binding, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
}

var keys = keyRegistry{
	Actions:     bind([]string{" "}, "space", "actions"),
	Help:        bind([]string{"?"}, "?", "help"),
	Quit:        bind([]string{"q", "ctrl+c"}, "q", "quit"),
	Back:        bind([]string{"esc"}, "esc", "back"),
	Tabs:        bind([]string{"tab"}, "tab", "next tab"),
	PrevTab:     bind([]string{"shift+tab"}, "shift+tab", "previous tab"),
	JumpTab:     bind([]string{"1", "2", "3", "4", "5", "6", "7", "8"}, "1-8", "jump to tab"),
	Up:          bind([]string{"up", "k"}, "k/up", "up"),
	Down:        bind([]string{"down", "j"}, "j/down", "down"),
	Detail:      bind([]string{"enter"}, "enter", "detail"),
	Search:      bind([]string{"/"}, "/", "search"),
	Delete:      bind([]string{"d"}, "d", "delete"),
	Edit:        bind([]string{"e"}, "e", "edit in editor"),
	Command:     bind([]string{":", ";"}, ":", "run cmd"),
	Refresh:     bind([]string{"r"}, "r", "refresh"),
	Gateway:     bind([]string{"g"}, "g", "prompt gateway"),
	AddDetected: bind([]string{"a"}, "a", "add to vault"),
	Connect:     bind([]string{"c"}, "c", "connect to gateway"),
	Backend:     bind([]string{"b"}, "b", "cycle claude backend"),

	ActAdd:     bind([]string{"a"}, "a", "add agent"),
	ActSwitch:  bind([]string{"s"}, "s", "switch agent"),
	ActPrompt:  bind([]string{"p"}, "p", "prompt"),
	ActUsage:   bind([]string{"u"}, "u", "usage"),
	ActCommand: bind([]string{":"}, ":", "run command"),

	Select:     bind([]string{"enter"}, "enter", "select"),
	Submit:     bind([]string{"enter"}, "enter", "save"),
	NextField:  bind([]string{"tab", "down"}, "tab", "next field"),
	PrevField:  bind([]string{"shift+tab", "up"}, "shift+tab", "previous field"),
	CycleNext:  bind([]string{"right", "l", " "}, "right", "next provider"),
	CyclePrev:  bind([]string{"left", "h"}, "left", "previous provider"),
	ClearAgent: bind([]string{"x"}, "x", "clear current"),

	GwRun:     bind([]string{"enter"}, "enter", "run"),
	GwReview:  bind([]string{"tab"}, "tab", "pick workspace + preview"),
	GwConfirm: bind([]string{"y", "Y", "enter"}, "y/enter", "run"),
	GwRedo:    bind([]string{"n", "N"}, "n", "edit workspace"),
	GwSwitch:  bind([]string{"s", "S"}, "s", "switch agent"),
	GwEdit:    bind([]string{"e", "E"}, "e", "edit prompt"),
	Type:      bind(nil, "type", "text"),
	Erase:     bind([]string{"backspace"}, "backspace", "erase"),
}

// helpBindings is the help bar for the screen m is on.
func (m model) helpBindings() []key.Binding {
	k := keys
	switch m.mode {
	case viewActions:
		return []key.Binding{k.ActAdd, k.ActSwitch, k.ActPrompt, k.ActUsage, k.ActCommand, as(k.Select, "run"), k.Back}
	case viewAddAgent:
		return []key.Binding{as(k.Type, "field"), k.NextField, as(k.CycleNext, "provider (on Provider)"), k.Submit, as(k.Back, "cancel")}
	case viewSwitchAgent:
		return []key.Binding{as(k.Down, "move"), as(k.Select, "make current"), k.ClearAgent, as(k.Back, "cancel")}
	case viewAgentDetail, viewInstructionDetail, viewRuleDetail, viewSessionDetail:
		if m.mode == viewAgentDetail && m.selectedAgentIsClaude() {
			return []key.Binding{as(k.Backend, "cycle backend"), k.Actions, k.Back, k.Quit}
		}
		return []key.Binding{k.Actions, k.Back, k.Quit}
	case viewHelp:
		return []key.Binding{k.Back, k.Quit}
	case viewConfirmDelete:
		return []key.Binding{bind([]string{"y"}, "y", "confirm"), bind([]string{"n", "esc"}, "n/esc", "cancel")}
	}
	if m.searchMode {
		return []key.Binding{as(k.Select, "apply filter"), as(k.Back, "cancel")}
	}
	if m.commandMode {
		return []key.Binding{as(k.Select, "run command"), as(k.Back, "cancel")}
	}
	switch m.activeTab {
	case tabAgents:
		return []key.Binding{k.Actions, as(k.Tabs, "tabs"), k.Search, k.Delete, k.Command, k.Detail, k.Help, k.Quit}
	case tabInstructions:
		return []key.Binding{k.Actions, as(k.Tabs, "tabs"), as(k.Edit, "edit"), k.Delete, k.Command, k.Detail, k.Help, k.Quit}
	case tabRules:
		return []key.Binding{k.Actions, as(k.Tabs, "tabs"), k.Delete, k.Command, k.Detail, k.Help, k.Quit}
	case tabDetected:
		if m.mode == viewDetectedDetail {
			return []key.Binding{as(k.Connect, "connect"), as(k.AddDetected, "add"), k.Back, k.Quit}
		}
		return []key.Binding{k.Actions, as(k.Tabs, "tabs"), as(k.Detail, "details"), as(k.Connect, "connect"), as(k.AddDetected, "add"), k.Command, k.Help, k.Quit}
	case tabCommands:
		return m.gatewayBindings()
	case tabAbout:
		return []key.Binding{k.Actions, as(k.Tabs, "tabs"), k.Help, k.Quit}
	}
	return []key.Binding{k.Actions, as(k.Tabs, "tabs"), k.Command, k.Help, k.Quit}
}

func (m model) gatewayBindings() []key.Binding {
	k := keys
	switch m.gatewayStage {
	case gatewaySelectAgent:
		return []key.Binding{as(k.Down, "select agent"), as(k.Select, "next"), as(k.Back, "close gateway")}
	case gatewayInputPrompt:
		if m.gatewayQuick {
			return []key.Binding{as(k.Type, "prompt"), as(k.GwRun, "run in "+shortPath(m.cwd)), k.GwReview, as(k.Back, "change agent")}
		}
		return []key.Binding{as(k.Type, "prompt"), as(k.GwRun, "rewrite"), k.Erase, k.Back}
	case gatewaySelectWorkspace:
		return []key.Binding{as(k.Type, "workspace path"), as(k.Select, "continue"), k.Erase, k.Back}
	case gatewayPreview:
		return []key.Binding{k.GwConfirm, k.GwRedo, k.Back}
	case gatewayRunning:
		return []key.Binding{bind(nil, "running...", "")}
	case gatewayResult:
		return []key.Binding{k.GwSwitch, k.GwEdit, as(k.Back, "close gateway")}
	}
	return []key.Binding{k.Actions, as(k.Tabs, "tabs"), as(k.Down, "select action"), as(k.Detail, "run"), k.Gateway, as(k.Command, "custom cmd"), k.Refresh, k.Help, k.Quit}
}

func shortPath(p string) string {
	if p == "" {
		return "current dir"
	}
	return truncate(p, 30)
}

func (m model) selectedAgentIsClaude() bool {
	return m.activeTab == tabAgents &&
		m.cursor >= 0 &&
		m.cursor < len(m.filteredAgents) &&
		m.filteredAgents[m.cursor] >= 0 &&
		m.filteredAgents[m.cursor] < len(m.agents) &&
		m.agents[m.filteredAgents[m.cursor]].Provider == agent.ProviderClaude
}

func newHelpModel() help.Model {
	h := help.New()
	h.ShortSeparator = "  "
	h.FullSeparator = "    "
	h.Ellipsis = "..."
	h.Styles.ShortKey = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	h.Styles.ShortDesc = helpStyle
	h.Styles.ShortSeparator = helpStyle
	return h
}

// helpSection is one group of the '?' screen.
type helpSection struct {
	title string
	binds []key.Binding
}

// helpSections builds the '?' screen from the registry. The first section
// is the screen the user came from, so the help is contextual.
func (m model) helpSections(from model) []helpSection {
	k := keys
	agentsTab := []key.Binding{k.Search, as(k.Delete, "delete selected agent")}
	if m.hasClaudeAgent {
		agentsTab = append(agentsTab, as(k.Backend, "cycle Claude backend in agent detail"))
	}
	return []helpSection{
		{"This screen", from.helpBindings()},
		{"Actions menu (space, from any tab)", []key.Binding{
			as(k.ActAdd, "add agent: name, provider, model, base URL"),
			as(k.ActSwitch, "switch the current agent (this session)"),
			as(k.ActPrompt, "prompt the current agent in the current dir"),
			as(k.ActUsage, "usage and provider status (Status tab)"),
			as(k.ActCommand, "run an agentvault CLI command"),
		}},
		{"Navigation", []key.Binding{k.Tabs, k.PrevTab, k.JumpTab, as(k.Down, "move cursor"), as(k.Detail, "view details"), as(k.Back, "back / close"), k.Quit}},
		{"Agents tab", agentsTab},
		{"Instructions tab", []key.Binding{k.Edit, as(k.Delete, "delete selected instruction")}},
		{"Rules tab", []key.Binding{as(k.Delete, "delete selected rule")}},
		{"Detected tab", []key.Binding{k.AddDetected, as(k.Connect, "connect selected agent to Prompt Gateway"), as(k.Detail, "open detected agent details")}},
		{"General", []key.Binding{as(k.Command, "run custom CLI command"), as(k.Gateway, "prompt gateway (Commands tab)"), as(k.Refresh, "refresh data"), as(k.Help, "show this help")}},
		{"API keys", []key.Binding{bind(nil, "agentvault edit NAME --api-key KEY", "set a key (the TUI does not take secrets)")}},
	}
}

func renderHelpSections(sections []helpSection) string {
	var b strings.Builder
	for _, s := range sections {
		b.WriteString(subtitleStyle.Render("  " + s.title))
		b.WriteString("\n")
		for _, kb := range s.binds {
			h := kb.Help()
			b.WriteString(fmt.Sprintf("    %-20s %s\n", h.Key, h.Desc))
		}
		b.WriteString("\n")
	}
	return b.String()
}
