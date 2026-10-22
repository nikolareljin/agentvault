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

// An SGR code from a bundle is not the TUI's own styling: conceal or
// black-on-black would hide an endpoint the user is looking at.
func TestViewDropsSGRFromVaultData(t *testing.T) {
	v := testVault(t)
	m := initialModel(v)
	m.agents[0].Name = "n\x1b[8mhidden\x1b[0m"
	m.agents[0].BaseURL = "https://\x1b[30;40mevil.example\x1b[0m"
	views := []string{m.View()}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	views = append(views, next.(model).View())
	for _, view := range views {
		if strings.Contains(view, "\x1b[8m") || strings.Contains(view, "\x1b[30;40m") {
			t.Errorf("view keeps SGR from vault data: %q", view)
		}
		if !strings.Contains(view, "n?[8mhidden?[0m") {
			t.Errorf("name missing: %q", view)
		}
	}
	if !strings.Contains(views[1], "evil.example") {
		t.Errorf("base URL missing from detail view")
	}
	if m.agents[0].Name != "n\x1b[8mhidden\x1b[0m" {
		t.Errorf("View changed the model's data: %q", m.agents[0].Name)
	}
}

func TestScrubESCDeepCopies(t *testing.T) {
	type in struct {
		S  string
		L  []string
		M  map[string]*string
		hi string
	}
	p := "p\x1b"
	src := in{S: "a\x1b", L: []string{"\x1bb"}, M: map[string]*string{"k\x1b": &p}, hi: "\x1b"}
	got := scrubESC(src)
	if got.S != "a?" || got.L[0] != "?b" || *got.M["k?"] != "p?" {
		t.Errorf("got %+v", got)
	}
	if src.L[0] != "\x1bb" || p != "p\x1b" {
		t.Errorf("source changed")
	}
}

// Agent output and files read from the working directory are not decoded
// as JSON, so they can hold bytes that are not UTF-8, such as a raw 0x9b.
func TestCleanViewReplacesInvalidUTF8(t *testing.T) {
	if got := cleanView("a\x9b2Jb\xffcé"); got != "a?2Jb?cé" {
		t.Errorf("got %q", got)
	}
}

type scrubNode struct {
	Name   string
	hidden string
	Next   *scrubNode
	Any    any
	Ch     chan int
	Fn     func()
	ByName map[string]scrubNode
	Arr    [2]string
}

// scrubESC must not panic on unexported fields, cycles, interfaces, maps
// of structs, channels or funcs, and must not change its input: View
// renders from the copy while Update keeps saving the original.
func TestScrubESCOddShapes(t *testing.T) {
	n := &scrubNode{Name: "a\x1b[8m", hidden: "h\x1b", Any: "i\x1b", Ch: make(chan int), Fn: func() {},
		ByName: map[string]scrubNode{"k\x1b": {Name: "v\x1b"}}, Arr: [2]string{"x\x1b", "y"}}
	n.Next = n
	got := scrubESC(n)
	if got.Name != "a?[8m" || got.Any != "i?" || got.Arr[0] != "x?" || got.ByName["k?"].Name != "v?" {
		t.Errorf("not scrubbed: %+v", got)
	}
	if n.Name != "a\x1b[8m" || n.Any != "i\x1b" || n.Arr[0] != "x\x1b" || n.ByName["k\x1b"].Name != "v\x1b" {
		t.Errorf("input changed: %+v", n)
	}
	if got.hidden != "h\x1b" || got.Ch != n.Ch {
		t.Errorf("unexported field or channel not carried over")
	}
}
