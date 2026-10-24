package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestCapabilityEntry(t *testing.T) {
	e, err := capabilityEntry(" http://h:11434/ ", " qwen ", " 32768 ", "Coding, ,REVIEW")
	if err != nil {
		t.Fatal(err)
	}
	if e.EndpointURL != "http://h:11434" || e.ModelName != "qwen" || e.ContextSize != 32768 ||
		!reflect.DeepEqual(e.Capabilities, []string{"coding", "review"}) || e.Source != "manual" {
		t.Fatalf("entry = %+v", e)
	}
	for _, bad := range [][4]string{
		{"", "m", "", ""},
		{"http://h", " ", "", ""},
		{"http://h", "m", "-1", ""},
		{"http://h", "m", "8k", ""},
	} {
		if _, err := capabilityEntry(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Errorf("capabilityEntry(%q) accepted", bad)
		}
	}
}

func openCapabilities(t *testing.T) model {
	t.Helper()
	m := asModel(t, must(actionsTestModel(t).Update(keySpace)))
	m = asModel(t, must(m.Update(runeKey('c'))))
	if m.mode != viewCapabilities {
		t.Fatalf("Space c opened mode %v", m.mode)
	}
	return m
}

func TestCapabilitiesAddAndRemove(t *testing.T) {
	m := openCapabilities(t)
	if !strings.Contains(m.View(), "None recorded.") {
		t.Fatalf("empty list:\n%s", m.View())
	}
	m = asModel(t, must(m.Update(runeKey('a'))))
	m = typeText(t, m, "http://gpu.example:11434/")
	m = asModel(t, must(m.Update(keyTab)))
	m = typeText(t, m, "qwen-coder")
	m = asModel(t, must(m.Update(keyTab)))
	m = typeText(t, m, "32768")
	m = asModel(t, must(m.Update(keyTab)))
	m = typeText(t, m, "coding, review")
	m = asModel(t, must(m.Update(keyEnter)))
	if m.mode != viewCapabilities || m.statusIsError {
		t.Fatalf("after add: mode=%v status=%q", m.mode, m.statusMsg)
	}
	got := m.vault.ListCapabilities()
	if len(got) != 1 || got[0].EndpointURL != "http://gpu.example:11434" || got[0].ContextSize != 32768 ||
		!reflect.DeepEqual(got[0].Capabilities, []string{"coding", "review"}) {
		t.Fatalf("vault = %+v", got)
	}
	if !strings.Contains(m.View(), "qwen-coder") {
		t.Fatalf("list does not show the entry:\n%s", m.View())
	}

	// d then anything but y keeps it; d then y removes it.
	m = asModel(t, must(m.Update(runeKey('d'))))
	if !strings.Contains(m.View(), "Remove qwen-coder?") {
		t.Fatalf("no confirm:\n%s", m.View())
	}
	m = asModel(t, must(m.Update(runeKey('n'))))
	if len(m.vault.ListCapabilities()) != 1 {
		t.Fatal("n removed the entry")
	}
	m = asModel(t, must(m.Update(runeKey('d'))))
	m = asModel(t, must(m.Update(runeKey('y'))))
	if len(m.vault.ListCapabilities()) != 0 || len(m.capabilities) != 0 {
		t.Fatalf("y did not remove: %+v", m.vault.ListCapabilities())
	}
	if m = asModel(t, must(m.Update(keyEsc))); m.mode != viewAgentList {
		t.Fatalf("esc went to %v", m.mode)
	}
}

func TestCapabilityAddRejectsDuplicateAndBadInput(t *testing.T) {
	m := openCapabilities(t)
	if err := m.vault.AddCapability(agent.ModelCapabilityEntry{EndpointURL: "http://h:1", ModelName: "m"}); err != nil {
		t.Fatal(err)
	}
	m = asModel(t, must(m.Update(runeKey('a'))))
	m = typeText(t, m, "http://h:1")
	m = asModel(t, must(m.Update(keyTab)))
	m = typeText(t, m, "m")
	m = asModel(t, must(m.Update(keyEnter)))
	if m.mode != viewCapabilityAdd || !strings.Contains(m.statusMsg, "already listed") {
		t.Fatalf("duplicate: mode=%v status=%q", m.mode, m.statusMsg)
	}
	m = asModel(t, must(m.Update(keyTab)))
	m = typeText(t, m, "x")
	m = asModel(t, must(m.Update(keyEnter)))
	if m.mode != viewCapabilityAdd || !strings.Contains(m.statusMsg, "whole number") {
		t.Fatalf("bad context: mode=%v status=%q", m.mode, m.statusMsg)
	}
	if m = asModel(t, must(m.Update(keyEsc))); m.mode != viewCapabilities {
		t.Fatalf("esc from the form went to %v", m.mode)
	}
}

func TestCapabilityDiscoverRunsCLI(t *testing.T) {
	m := openCapabilities(t)
	m = asModel(t, must(m.Update(runeKey('D'))))
	if m.mode != viewCapabilityDiscover {
		t.Fatalf("D opened %v", m.mode)
	}
	if _, cmd := m.Update(keyEnter); cmd != nil {
		t.Fatal("empty endpoint ran the CLI")
	}
	m = typeText(t, m, "http://user:pw@h:11434")
	if tm, cmd := m.Update(keyEnter); cmd != nil || !strings.Contains(asModel(t, tm).statusMsg, "holds credentials") {
		t.Fatal("an endpoint with credentials was put on the command line")
	}
	m.capDiscover.SetValue("")
	m = typeText(t, m, "http://h:11434")
	if !strings.Contains(m.View(), "agentvault capability discover --endpoint=http://h:11434") {
		t.Fatalf("command line not shown:\n%s", m.View())
	}
	tm, cmd := m.Update(keyEnter)
	if got := asModel(t, tm); cmd == nil || !got.commandRunning || got.mode != viewCapabilities {
		t.Fatalf("enter: cmd=%v running=%v mode=%v, want the CLI run and the list open", cmd != nil, got.commandRunning, got.mode)
	}
	if got := discoverArgs(" http://h:11434 "); !reflect.DeepEqual(got, []string{"capability", "discover", "--endpoint=http://h:11434"}) {
		t.Fatalf("discoverArgs = %q", got)
	}
}

func TestRunCLIMasksCredentialsInLastCommand(t *testing.T) {
	got := shownCommand(discoverArgs("http://user:hunter2@h:11434"))
	if strings.Contains(got, "hunter2") || !strings.HasPrefix(got, "agentvault capability discover") {
		t.Fatalf("shownCommand = %q", got)
	}
	if got := shownCommand([]string{"import", "--", "/tmp/a b.avbundle"}); got != "agentvault import -- '/tmp/a b.avbundle'" {
		t.Fatalf("plain args changed: %q", got)
	}
}

func TestCapabilityCursorClampedAfterShrink(t *testing.T) {
	m := openCapabilities(t)
	for _, name := range []string{"m1", "m2"} {
		if err := m.vault.AddCapability(agent.ModelCapabilityEntry{EndpointURL: "http://h:1", ModelName: name}); err != nil {
			t.Fatal(err)
		}
	}
	m.capabilities = m.vault.ListCapabilities()
	m.capCursor = 1
	// A command removed m2; the reload shrinks the list under the cursor.
	if err := m.vault.RemoveCapability("http://h:1", "m2"); err != nil {
		t.Fatal(err)
	}
	m.capabilities = m.vault.ListCapabilities()
	if !strings.Contains(m.View(), "> m1") {
		t.Fatalf("no entry selected:\n%s", m.View())
	}
	m = asModel(t, must(m.Update(runeKey('d'))))
	m = asModel(t, must(m.Update(runeKey('y'))))
	if len(m.vault.ListCapabilities()) != 0 {
		t.Fatal("d y did nothing with the cursor past the end")
	}
}

func TestCapabilityDiscoverShowsResultLine(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"ok", "arg:--endpoint=http://h:11434"}, // the fake prints its args; the last one is the result line
		{"fail", "Error: fake failure"},
	} {
		useFakeCLI(t, tc.mode)
		m := openCapabilities(t)
		m = asModel(t, must(m.Update(runeKey('D'))))
		m = typeText(t, m, "http://h:11434")
		tm, cmd := m.Update(keyEnter)
		if cmd == nil {
			t.Fatalf("%s: discover did not run", tc.mode)
		}
		m = asModel(t, must(asModel(t, tm).Update(cmd())))
		if m.statusMsg != tc.want || m.statusIsError != (tc.mode == "fail") || m.mode != viewCapabilities {
			t.Errorf("%s: status=%q error=%v mode=%v, want %q", tc.mode, m.statusMsg, m.statusIsError, m.mode, tc.want)
		}
	}
}

func TestCommandSummary(t *testing.T) {
	if got := commandSummary("  + m-a (general)\nAdded 2 model(s) from http://h\n\n", false); got != "Added 2 model(s) from http://h" {
		t.Errorf("success: %q", got)
	}
	if got := commandSummary("Error: discover: refused\nUsage:\n  agentvault capability discover [flags]\n", true); got != "Error: discover: refused" {
		t.Errorf("failure: %q", got)
	}
	if got := commandSummary("no error line\n", true); got != "" {
		t.Errorf("failure without an Error line: %q", got)
	}
}

func TestCommandBridgeShowsOnlyFailureLine(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"ok", "Command completed"}, // a success line can be a printed secret; it is not shown
		{"fail", "Error: fake failure"},
	} {
		useFakeCLI(t, tc.mode)
		m := asModel(t, must(actionsTestModel(t).Update(runeKey(':'))))
		m = typeText(t, m, "config get secret")
		tm, cmd := m.Update(keyEnter)
		if cmd == nil {
			t.Fatalf("%s: command did not run", tc.mode)
		}
		m = asModel(t, must(asModel(t, tm).Update(cmd())))
		if m.statusMsg != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.mode, m.statusMsg, tc.want)
		}
	}
}

func TestTailBufferKeepsTail(t *testing.T) {
	b := &tailBuffer{max: 8}
	_, _ = b.Write([]byte("0123456789"))
	_, _ = b.Write([]byte("ab"))
	if got := b.String(); got != "456789ab" {
		t.Fatalf("tail = %q", got)
	}
}
