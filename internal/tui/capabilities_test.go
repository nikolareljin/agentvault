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
	m = typeText(t, m, "http://h:11434")
	if !strings.Contains(m.View(), "agentvault capability discover --endpoint=http://h:11434") {
		t.Fatalf("command line not shown:\n%s", m.View())
	}
	tm, cmd := m.Update(keyEnter)
	if cmd == nil || !asModel(t, tm).commandRunning {
		t.Fatal("enter did not run the CLI")
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
