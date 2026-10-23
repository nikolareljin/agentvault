package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
)

func asModel(t *testing.T, tm tea.Model) model {
	t.Helper()
	switch v := tm.(type) {
	case model:
		return v
	case *model:
		return *v
	}
	t.Fatalf("Update() returned %T", tm)
	return model{}
}

// press sends one navigation key and counts it as a step.
func press(t *testing.T, m model, k tea.KeyMsg, steps *int) model {
	t.Helper()
	*steps++
	tm, _ := m.Update(k)
	return asModel(t, tm)
}

// typeText sends text one rune at a time. Typing is not a navigation step.
func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		tm, _ := m.Update(k)
		m = asModel(t, tm)
	}
	return m
}

var (
	keySpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyTab   = tea.KeyMsg{Type: tea.KeyTab}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func actionsTestModel(t *testing.T) model {
	t.Helper()
	m := initialModel(testVault(t))
	m.cwd = t.TempDir()
	return m
}

// Issue 10: frequent tasks are reachable in <=3 navigation steps from the
// start screen. Typed text does not count.
func TestActionStepCounts(t *testing.T) {
	const maxSteps = 3

	t.Run("add agent", func(t *testing.T) {
		m, steps := actionsTestModel(t), 0
		m = press(t, m, keySpace, &steps)
		m = press(t, m, runeKey('a'), &steps)
		if m.mode != viewAddAgent {
			t.Fatalf("mode = %v, want add form", m.mode)
		}
		m = typeText(t, m, "new-one")
		m = press(t, m, keyEnter, &steps)
		if _, ok := m.vault.Get("new-one"); !ok {
			t.Fatalf("agent not saved; status %q", m.statusMsg)
		}
		if steps > maxSteps {
			t.Fatalf("add agent took %d steps, want <= %d", steps, maxSteps)
		}
	})

	t.Run("switch agent", func(t *testing.T) {
		m, steps := actionsTestModel(t), 0
		m = press(t, m, keySpace, &steps)
		m = press(t, m, runeKey('s'), &steps)
		m = press(t, m, keyEnter, &steps)
		if m.currentAgent != "claude-main" {
			t.Fatalf("currentAgent = %q, want claude-main", m.currentAgent)
		}
		if steps > maxSteps {
			t.Fatalf("switch took %d steps, want <= %d", steps, maxSteps)
		}
	})

	t.Run("prompt", func(t *testing.T) {
		m, steps := actionsTestModel(t), 0
		m.currentAgent = "gemini-fast"
		m = press(t, m, keySpace, &steps)
		m = press(t, m, runeKey('p'), &steps)
		if m.gatewayStage != gatewayInputPrompt {
			t.Fatalf("stage = %v, want prompt input (agent selection skipped)", m.gatewayStage)
		}
		m = typeText(t, m, "say hi")
		m = press(t, m, keyEnter, &steps)
		if m.gatewayStage != gatewayRunning {
			t.Fatalf("stage = %v, want running; status %q", m.gatewayStage, m.statusMsg)
		}
		if got := m.agents[m.gatewayAgentCursor].Name; got != "gemini-fast" {
			t.Fatalf("prompt runs %q, want current agent gemini-fast", got)
		}
		if m.gatewayWorkspace == "" {
			t.Fatal("default workspace not set")
		}
		if steps > maxSteps {
			t.Fatalf("prompt took %d steps, want <= %d", steps, maxSteps)
		}
	})

	t.Run("usage", func(t *testing.T) {
		m, steps := actionsTestModel(t), 0
		m = press(t, m, keySpace, &steps)
		m = press(t, m, runeKey('u'), &steps)
		if m.activeTab != tabStatus || m.mode != viewStatus {
			t.Fatalf("tab/mode = %v/%v, want Status", m.activeTab, m.mode)
		}
		if steps > maxSteps {
			t.Fatalf("usage took %d steps, want <= %d", steps, maxSteps)
		}
	})
}

func TestActionsMenuOpensFromEveryTab(t *testing.T) {
	m := actionsTestModel(t)
	for tb := tab(0); tb < numTabs; tb++ {
		m.activeTab = tb
		m.mode = m.getModeForTab()
		got := asModel(t, must(m.Update(keySpace)))
		if got.mode != viewActions {
			t.Fatalf("tab %d: mode = %v, want actions", tb, got.mode)
		}
		back := asModel(t, must(got.Update(keyEsc)))
		if back.mode != m.mode {
			t.Fatalf("tab %d: esc returned to %v, want %v", tb, back.mode, m.mode)
		}
	}
}

func must(tm tea.Model, _ tea.Cmd) tea.Model { return tm }

func TestActionsMenuRunCommandOpensCommandMode(t *testing.T) {
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey(':'))))
	if !m.commandMode || m.mode != viewAgentList {
		t.Fatalf("commandMode=%v mode=%v, want command entry on the Agents list", m.commandMode, m.mode)
	}
}

func openAddForm(t *testing.T) model {
	t.Helper()
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	return asModel(t, must(m.Update(runeKey('a'))))
}

func TestAddAgentFormRejectsBadName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		m := openAddForm(t)
		before := len(m.vault.List())
		m.addInputs[addFieldName].SetValue(name)
		m = asModel(t, must(m.Update(keyEnter)))
		if m.mode != viewAddAgent {
			t.Fatalf("name %q: form closed, want it kept open", name)
		}
		if !m.statusIsError || !strings.Contains(m.statusMsg, "agent name") {
			t.Fatalf("name %q: status = %q, want name error", name, m.statusMsg)
		}
		if len(m.vault.List()) != before {
			t.Fatalf("name %q: vault changed", name)
		}
	}
}

func TestAddAgentFormRejectsBadProvider(t *testing.T) {
	m := openAddForm(t)
	m.addInputs[addFieldName].SetValue("p-test")
	m.addProviders = []agent.Provider{"not-a-provider"}
	m = asModel(t, must(m.Update(keyEnter)))
	if !strings.Contains(m.statusMsg, "unknown provider") {
		t.Fatalf("status = %q, want unknown provider", m.statusMsg)
	}
	if _, ok := m.vault.Get("p-test"); ok {
		t.Fatal("agent with bad provider was saved")
	}
}

func TestAddAgentFormRejectsDuplicate(t *testing.T) {
	m := openAddForm(t)
	m = typeText(t, m, "claude-main")
	m = asModel(t, must(m.Update(keyEnter)))
	if !strings.Contains(m.statusMsg, "already exists") {
		t.Fatalf("status = %q, want duplicate error", m.statusMsg)
	}
}

func TestAddAgentFormSavesAllFields(t *testing.T) {
	m := openAddForm(t)
	m = typeText(t, m, "local llama")
	m = asModel(t, must(m.Update(keyTab))) // provider
	for i := 0; i < 3; i++ {               // claude -> gemini -> codex -> ollama
		m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyRight})))
	}
	m = asModel(t, must(m.Update(keyTab))) // model
	m = typeText(t, m, "llama3")
	m = asModel(t, must(m.Update(keyTab))) // base URL
	m = typeText(t, m, "http://localhost:11434")
	m = asModel(t, must(m.Update(keyEnter)))

	a, ok := m.vault.Get("local llama")
	if !ok {
		t.Fatalf("agent not saved; status %q", m.statusMsg)
	}
	if a.Provider != agent.ProviderOllama || a.Model != "llama3" || a.BaseURL != "http://localhost:11434" {
		t.Fatalf("saved %+v", a)
	}
	if a.APIKey != "" {
		t.Fatal("form must not set an API key")
	}
	if m.mode != viewAgentList || !strings.Contains(m.statusMsg, "--api-key") {
		t.Fatalf("mode=%v status=%q, want back on list with key hint", m.mode, m.statusMsg)
	}
}

func TestSwitchAgentSetsCurrentAndPromptSkipsSelection(t *testing.T) {
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('s'))))
	m = asModel(t, must(m.Update(runeKey('j'))))
	m = asModel(t, must(m.Update(keyEnter)))
	if m.currentAgent != "gemini-fast" {
		t.Fatalf("currentAgent = %q, want gemini-fast", m.currentAgent)
	}
	if !strings.Contains(m.View(), "Current agent: gemini-fast") {
		t.Fatal("header does not show the current agent")
	}

	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('p'))))
	if m.gatewayStage != gatewayInputPrompt || m.agents[m.gatewayAgentCursor].Name != "gemini-fast" {
		t.Fatalf("stage=%v agent=%q, want prompt input for gemini-fast", m.gatewayStage, m.agents[m.gatewayAgentCursor].Name)
	}

	// The workspace and preview steps stay reachable with tab.
	m = typeText(t, m, "hello")
	m = asModel(t, must(m.Update(keyTab)))
	if m.gatewayStage != gatewaySelectWorkspace {
		t.Fatalf("stage = %v, want workspace selection", m.gatewayStage)
	}
}

func TestPromptWithoutCurrentAgentSelectsOneAndRemembersIt(t *testing.T) {
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('p'))))
	if m.gatewayStage != gatewaySelectAgent {
		t.Fatalf("stage = %v, want agent selection", m.gatewayStage)
	}
	m = asModel(t, must(m.Update(keyEnter)))
	if m.currentAgent != "claude-main" {
		t.Fatalf("currentAgent = %q, want claude-main", m.currentAgent)
	}
}

func TestCurrentAgentRemovedFromVault(t *testing.T) {
	m := actionsTestModel(t)
	m.currentAgent = "gone"
	if m.currentAgentIndex() != -1 {
		t.Fatal("missing agent resolved to an index")
	}
}

func TestHelpBarIsContextual(t *testing.T) {
	m := actionsTestModel(t)
	bars := map[string]string{}
	bars["list"] = m.renderHelpBar()
	m = asModel(t, must(m.Update(keySpace)))
	bars["actions"] = m.renderHelpBar()
	add := asModel(t, must(m.Update(runeKey('a'))))
	bars["add"] = add.renderHelpBar()
	sw := asModel(t, must(m.Update(runeKey('s'))))
	bars["switch"] = sw.renderHelpBar()
	m.currentAgent = "claude-main"
	pr := asModel(t, must(m.Update(runeKey('p'))))
	bars["prompt"] = pr.renderHelpBar()

	want := map[string]string{
		"list":    "space actions",
		"actions": "a add agent",
		"add":     "enter save",
		"switch":  "enter make current",
		"prompt":  "tab pick workspace + preview",
	}
	for name, w := range want {
		got := cleanView(bars[name])
		got = stripSGR(got)
		if !strings.Contains(got, w) {
			t.Errorf("%s help bar = %q, want %q", name, got, w)
		}
	}
	seen := map[string]string{}
	for name, b := range bars {
		if other, dup := seen[b]; dup {
			t.Errorf("%s and %s show the same help bar", name, other)
		}
		seen[b] = name
	}
}

func TestFullHelpStartsWithCurrentScreen(t *testing.T) {
	m := actionsTestModel(t)
	m = asModel(t, must(m.Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('a'))))
	m = asModel(t, must(m.Update(keyEsc)))
	m = asModel(t, must(m.Update(runeKey('?'))))
	v := stripSGR(m.View())
	if !strings.Contains(v, "This screen") || !strings.Contains(v, "Actions menu") {
		t.Fatalf("help view missing sections:\n%s", v)
	}
	if !strings.Contains(v, "agentvault edit NAME --api-key KEY") {
		t.Fatal("help view does not say how to set an API key")
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestProgramDrivesActionsMenu runs the real tea.Program without a TTY and
// adds an agent through the actions menu.
func TestProgramDrivesActionsMenu(t *testing.T) {
	v := testVault(t)
	in := strings.NewReader("")
	var out strings.Builder
	p := tea.NewProgram(initialModel(v), tea.WithInput(in), tea.WithOutput(&out), tea.WithoutRenderer())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	for _, k := range []tea.KeyMsg{keySpace, runeKey('a')} {
		p.Send(k)
	}
	for _, r := range "via-program" {
		p.Send(runeKey(r))
	}
	p.Send(keyEnter)
	p.Send(runeKey('q'))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Get("via-program"); !ok {
		t.Fatal("program run did not add the agent")
	}
}
