package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/textutil"
)

// The Status tab's cost and capability sections show what
// 'agentvault status --cost-report' and 'agentvault capability list' print.

// matrixCapabilities are the matrix columns: the routing labels, then the
// informational ones, with 3-letter headers so a row fits 80 columns. A
// label outside this list goes in the OTHER column.
var matrixCapabilities = []struct{ name, header string }{
	{agent.RouteCapabilityCoding, "COD"},
	{agent.RouteCapabilityReview, "REV"},
	{agent.RouteCapabilityAnalysis, "ANA"},
	{agent.RouteCapabilityDocumentation, "DOC"},
	{agent.RouteCapabilityGeneral, "GEN"},
	{"vision", "VIS"},
	{"embedding", "EMB"},
}

func isMatrixCapability(c string) bool {
	return slices.ContainsFunc(matrixCapabilities, func(mc struct{ name, header string }) bool { return mc.name == c })
}

// costOnEnterStatus refreshes the report with the cost when the Status tab
// opens, so it does not wait for the next 5s tick.
func (m model) costOnEnterStatus() tea.Cmd {
	if m.activeTab != tabStatus {
		return nil
	}
	return statusRefreshCmd(m.vault, true)
}

func (m model) renderStatusCost() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Cost (estimated, from prompt history):"))
	b.WriteString("\n")
	if !m.costRead {
		b.WriteString(dimStyle.Render("    Loading..."))
		b.WriteString("\n")
		return b.String()
	}
	c := m.cost
	if c == nil {
		b.WriteString(dimStyle.Render("    No successful prompts recorded yet."))
		b.WriteString("\n")
		return b.String()
	}
	prompts := "prompts"
	if c.RecordCount == 1 {
		prompts = "prompt"
	}
	b.WriteString(fmt.Sprintf("    Total:        $%.4f (%d %s)\n", c.TotalUSD, c.RecordCount, prompts))
	providers := make([]string, 0, len(c.ByProvider))
	for p := range c.ByProvider {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, p := range providers {
		b.WriteString(fmt.Sprintf("    %-13s $%.4f total, $%.4f this month\n",
			textutil.Printable(p)+":", c.ByProvider[p], c.ThisMonth[p]))
	}
	for _, a := range c.BudgetAlerts {
		b.WriteString(warnStyle.Render(fmt.Sprintf("    Budget: %s spent $%.2f of $%.2f this month (%.0f%%)",
			textutil.Printable(a.Provider), a.SpentUSD, a.BudgetUSD, a.SpentUSD/a.BudgetUSD*100)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderCapabilityMatrix() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Model Capabilities:"))
	b.WriteString("\n")
	if len(m.capabilities) == 0 {
		b.WriteString(dimStyle.Render("    None recorded. Space c adds or discovers them."))
		b.WriteString("\n")
		return b.String()
	}
	header := fmt.Sprintf("    %-28s %8s", "MODEL", "CONTEXT")
	for _, c := range matrixCapabilities {
		header += " " + c.header
	}
	b.WriteString(dimStyle.Render(header + " OTHER"))
	b.WriteString("\n")
	for _, e := range m.capabilities {
		has := make(map[string]bool, len(e.Capabilities))
		var other []string
		for _, c := range e.Capabilities {
			c = strings.ToLower(strings.TrimSpace(c))
			has[c] = true
			if !isMatrixCapability(c) {
				other = append(other, c)
			}
		}
		ctx := "-"
		if e.ContextSize > 0 {
			ctx = fmt.Sprintf("%d", e.ContextSize)
		}
		row := fmt.Sprintf("    %-28s %8s", truncateRunes(textutil.Printable(e.ModelName), 28), ctx)
		for _, c := range matrixCapabilities {
			mark := " - "
			if has[c.name] {
				mark = "yes"
			}
			row += " " + mark
		}
		if len(other) > 0 {
			row += " " + textutil.Printable(strings.Join(other, ","))
		}
		b.WriteString(row + "\n")
		b.WriteString(dimStyle.Render("      " + textutil.Printable(MaskURL(e.EndpointURL))))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("    " + capabilityLegend()))
	b.WriteString("\n")
	return b.String()
}

// capabilityLegend names the matrix columns.
func capabilityLegend() string {
	parts := make([]string, 0, len(matrixCapabilities))
	for _, c := range matrixCapabilities {
		parts = append(parts, c.header+"="+c.name)
	}
	return strings.Join(parts, " ")
}

// truncateRunes is truncate that does not cut a UTF-8 character.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-3]) + "..."
}
