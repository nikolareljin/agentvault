package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/textutil"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// The capabilities screen lists, adds and removes model capability entries,
// as 'agentvault capability list|add|remove' do. Discovery queries an
// endpoint over the network, so it runs 'agentvault capability discover'.

// Add-capability form fields.
const (
	capFieldEndpoint = iota
	capFieldModel
	capFieldContext
	capFieldCaps
	capFieldCount
)

func (m *model) openCapabilities() tea.Cmd {
	m.mode = viewCapabilities
	m.capCursor = 0
	m.capConfirm = false
	return nil
}

func (m *model) openCapabilityAdd() tea.Cmd {
	m.mode = viewCapabilityAdd
	m.capInputs = []textinput.Model{
		newFormInput("http://localhost:11434"),
		newFormInput("e.g. qwen2.5-coder:7b"),
		newFormInput("optional: context window in tokens"),
		newFormInput("comma separated: coding,review,analysis,documentation,general,vision,embedding"),
	}
	m.capFocus = capFieldEndpoint
	return m.capInputs[capFieldEndpoint].Focus()
}

func (m *model) openCapabilityDiscover() tea.Cmd {
	m.mode = viewCapabilityDiscover
	m.capDiscover = newFormInput("http://localhost:11434")
	return m.capDiscover.Focus()
}

// capabilityEntry reads the add form, normalized as 'capability add' does.
func capabilityEntry(endpoint, model, context, caps string) (agent.ModelCapabilityEntry, error) {
	e := agent.ModelCapabilityEntry{
		EndpointURL: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		ModelName:   strings.TrimSpace(model),
		Source:      "manual",
		UpdatedAt:   time.Now().UTC(),
	}
	if e.EndpointURL == "" || e.ModelName == "" {
		return e, errors.New("endpoint and model are required")
	}
	if c := strings.TrimSpace(context); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 0 {
			return e, fmt.Errorf("context %q: want a whole number >= 0", c)
		}
		e.ContextSize = n
	}
	for _, c := range strings.Split(caps, ",") {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			e.Capabilities = append(e.Capabilities, c)
		}
	}
	return e, nil
}

func (m *model) handleCapabilitiesInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.capabilities)
	// A reload after a command can shrink the list under the cursor.
	m.capCursor = max(0, min(m.capCursor, n-1))
	if m.capConfirm {
		m.capConfirm = false
		if msg.String() == "y" && m.capCursor < n {
			e := m.capabilities[m.capCursor]
			if err := m.vault.RemoveCapability(e.EndpointURL, e.ModelName); err != nil {
				m.setStatus("Error: "+err.Error(), true)
				return m, nil
			}
			m.capabilities = m.vault.ListCapabilities()
			if m.capCursor >= len(m.capabilities) && m.capCursor > 0 {
				m.capCursor--
			}
			m.setStatus("Removed "+e.ModelName, false)
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, keys.Back):
		m.closeOverlay()
	case key.Matches(msg, keys.Up):
		if m.capCursor > 0 {
			m.capCursor--
		}
	case key.Matches(msg, keys.Down):
		if m.capCursor < n-1 {
			m.capCursor++
		}
	case key.Matches(msg, keys.CapAdd):
		return m, m.openCapabilityAdd()
	case key.Matches(msg, keys.CapDiscover):
		return m, m.openCapabilityDiscover()
	case key.Matches(msg, keys.Delete):
		if n > 0 {
			m.capConfirm = true
		}
	}
	return m, nil
}

func (m *model) handleCapabilityAddInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.mode = viewCapabilities
		return m, nil
	case key.Matches(msg, keys.NextField):
		return m, m.focusCapabilityField((m.capFocus + 1) % capFieldCount)
	case key.Matches(msg, keys.PrevField):
		return m, m.focusCapabilityField((m.capFocus + capFieldCount - 1) % capFieldCount)
	case key.Matches(msg, keys.Submit):
		in := m.capInputs
		e, err := capabilityEntry(in[capFieldEndpoint].Value(), in[capFieldModel].Value(), in[capFieldContext].Value(), in[capFieldCaps].Value())
		if err == nil {
			err = m.vault.AddCapability(e)
		}
		if errors.Is(err, vault.ErrCapabilityExists) {
			err = fmt.Errorf("%s at this endpoint is already listed; remove it first", e.ModelName)
		}
		if err != nil {
			m.setStatus("Error: "+err.Error(), true)
			return m, nil
		}
		m.capabilities = m.vault.ListCapabilities()
		m.capCursor = len(m.capabilities) - 1
		m.mode = viewCapabilities
		m.setStatus("Added "+e.ModelName, false)
		return m, nil
	}
	var cmd tea.Cmd
	m.capInputs[m.capFocus], cmd = m.capInputs[m.capFocus].Update(msg)
	return m, cmd
}

func (m *model) focusCapabilityField(i int) tea.Cmd {
	m.capInputs[m.capFocus].Blur()
	m.capFocus = i
	return m.capInputs[i].Focus()
}

// discoverArgs builds 'agentvault capability discover' for endpoint.
func discoverArgs(endpoint string) []string {
	// One argument, so an endpoint starting with '-' is not read as a flag.
	return []string{"capability", "discover", "--endpoint=" + strings.TrimSpace(endpoint)}
}

func (m *model) handleCapabilityDiscoverInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.mode = viewCapabilities
		return m, nil
	case key.Matches(msg, keys.Submit):
		endpoint := strings.TrimSpace(m.capDiscover.Value())
		if endpoint == "" {
			m.setStatus("Error: endpoint is required", true)
			return m, nil
		}
		if MaskURL(endpoint) != endpoint {
			// The child's argv is readable by other local users (ps).
			m.setStatus("Error: the endpoint holds credentials; the TUI does not pass them on a command line. Run agentvault capability discover yourself.", true)
			return m, nil
		}
		return m, m.runDiscover(endpoint)
	}
	var cmd tea.Cmd
	m.capDiscover, cmd = m.capDiscover.Update(msg)
	return m, cmd
}

// runDiscover runs 'capability discover' on the terminal, since it may ask
// for the master password, and copies its output so the result line ("Added
// N model(s)" or the error) is still on the status bar after the TUI redraws.
func (m *model) runDiscover(endpoint string) tea.Cmd {
	exe, err := agentvaultExecutable()
	if err != nil {
		m.setStatus(fmt.Sprintf("Unable to locate agentvault binary: %v", err), true)
		return nil
	}
	// Back to the list, which the reload after the command fills.
	m.mode = viewCapabilities
	m.commandRunning = true
	args := discoverArgs(endpoint)
	buf := &tailBuffer{max: 64 << 10}
	c := exec.Command(exe, args...)
	c.Stdout = io.MultiWriter(os.Stdout, buf)
	c.Stderr = io.MultiWriter(os.Stderr, buf)
	line := shownCommand(args)
	return runPreviewProcess(c, func(err error) tea.Msg {
		return cliCommandFinishedMsg{command: line, err: err, summary: commandSummary(buf.String(), err != nil)}
	})
}

// commandSummary picks the line of a command's output for the status bar:
// the "Error:" line when it failed (cobra prints usage after it), else the
// last line.
func commandSummary(output string, failed bool) string {
	var last string
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		if failed && strings.HasPrefix(l, "Error:") {
			return textutil.Printable(l)
		}
		if l != "" {
			last = l
		}
	}
	if failed {
		return ""
	}
	return textutil.Printable(last)
}

func (m model) renderCapabilities() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Model Capabilities"))
	b.WriteString("\n\n")
	if len(m.capabilities) == 0 {
		b.WriteString(dimStyle.Render("  None recorded. a adds one, D discovers an endpoint's models."))
		b.WriteString("\n")
		return b.String()
	}
	cursor := max(0, min(m.capCursor, len(m.capabilities)-1))
	for i, e := range m.capabilities {
		caps := strings.Join(e.Capabilities, ",")
		if caps == "" {
			caps = "-"
		}
		ctx := "-"
		if e.ContextSize > 0 {
			ctx = strconv.Itoa(e.ContextSize)
		}
		line := fmt.Sprintf("%-28s %8s  %s", truncateRunes(textutil.Printable(e.ModelName), 28), ctx, textutil.Printable(caps))
		if i == cursor {
			b.WriteString(selectedStyle.Render("> " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    " + textutil.Printable(MaskURL(e.EndpointURL)) + "  (" + textutil.Printable(e.Source) + ")"))
		b.WriteString("\n")
	}
	if m.capConfirm && m.capCursor < len(m.capabilities) {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(fmt.Sprintf("  Remove %s? y to confirm, any other key cancels.",
			textutil.Printable(m.capabilities[m.capCursor].ModelName))))
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderCapabilityAdd() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Add Model Capability"))
	b.WriteString("\n\n")
	for i, label := range []string{"Endpoint", "Model", "Context", "Capabilities"} {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%s%-13s", formMarker(i == m.capFocus), label)))
		b.WriteString(" " + m.capInputs[i].View() + "\n")
	}
	return b.String()
}

func (m model) renderCapabilityDiscover() string {
	var b strings.Builder
	b.WriteString(subtitleStyle.Render("Discover Models"))
	b.WriteString("\n\n")
	b.WriteString(labelStyle.Render(fmt.Sprintf("%s%-13s", formMarker(true), "Endpoint")))
	b.WriteString(" " + m.capDiscover.View() + "\n\n")
	b.WriteString(dimStyle.Render("  Asks the endpoint for its models (/v1/models, then /health) and adds an entry for each."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Runs: " + textutil.Printable(displayCommand(discoverArgs(MaskURL(m.capDiscover.Value()))))))
	b.WriteString("\n")
	return b.String()
}
