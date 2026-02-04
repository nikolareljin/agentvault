package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// HijackWarning represents a detected prompt injection pattern.
type HijackWarning struct {
	Pattern     string // The pattern that matched
	Line        int    // Line number (1-based) where the match was found
	Context     string // The matched line (trimmed)
	Severity    string // "high", "medium", or "low"
	Description string // Human-readable explanation
}

// hijackPattern defines a pattern to search for in instruction content.
type hijackPattern struct {
	regex       *regexp.Regexp
	severity    string
	description string
}

// patterns is the compiled list of prompt injection patterns to detect.
// These are common techniques used to override or subvert AI agent instructions.
var patterns []hijackPattern

func init() {
	raw := []struct {
		pattern     string
		severity    string
		description string
	}{
		// High severity: direct override attempts
		{`(?i)ignore\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions|rules|guidelines|prompts)`, "high",
			"Attempts to override previous instructions"},
		{`(?i)disregard\s+(all\s+)?(previous|prior|above|earlier|the above)`, "high",
			"Attempts to disregard previous instructions"},
		{`(?i)forget\s+(everything|all|what)\s+(you|about|above|before)`, "high",
			"Attempts to reset agent context"},
		{`(?i)new\s+instructions?\s*:`, "high",
			"Declares new instructions to override existing ones"},
		{`(?i)override\s+(all\s+)?(previous|prior|above|earlier|existing)\s+(instructions|rules|settings)`, "high",
			"Direct override of existing instructions"},
		{`(?i)you\s+are\s+now\s+(a|an)\s+`, "high",
			"Attempts to reassign agent identity"},
		{`(?i)from\s+now\s+on\s*,?\s*(you|ignore|forget|disregard)`, "high",
			"Attempts to change agent behavior going forward"},
		{`(?i)system\s*prompt\s*:\s*`, "medium",
			"Contains what appears to be an injected system prompt"},

		// Medium severity: sneaky techniques
		{`(?i)do\s+not\s+follow\s+(the\s+)?(above|previous|prior|earlier)\s+(rules|instructions)`, "medium",
			"Instructs agent to not follow rules"},
		{`(?i)pretend\s+(that\s+)?(you|the)\s+(are|previous|above)`, "medium",
			"Social engineering via pretend scenario"},
		{`(?i)act\s+as\s+if\s+(no|there are no|the)\s+(rules|instructions|restrictions)`, "medium",
			"Attempts to bypass restrictions via role-play"},
		{`(?i)the\s+(real|actual|true)\s+instructions\s+are`, "medium",
			"Claims to provide 'real' instructions to override existing ones"},
		{`(?i)secret\s+(instruction|command|override|mode)`, "medium",
			"References hidden/secret instructions"},
		{`(?i)(admin|root|sudo|superuser)\s+(mode|access|override|command)`, "medium",
			"Attempts privilege escalation via fake admin modes"},
		{`(?i)developer\s+mode\s+(enabled|activated|on)`, "medium",
			"Attempts to activate fake developer mode"},

		// Low severity: suspicious but may be legitimate
		{`(?i)ignore\s+(this|these)\s+(rules?|instructions?)`, "low",
			"May attempt to have agent skip certain rules"},
		{`(?i)don.t\s+(tell|mention|reveal|disclose)\s+(the\s+)?(user|anyone|them)`, "low",
			"May attempt to hide actions from the user"},
		{`(?i)base64\s*:\s*[A-Za-z0-9+/=]{20,}`, "low",
			"Contains base64-encoded content that may hide instructions"},
		{`(?i)<\s*(script|iframe|img\s+onerror)\b`, "low",
			"Contains HTML tags that could be used for injection"},
	}

	patterns = make([]hijackPattern, 0, len(raw))
	for _, r := range raw {
		patterns = append(patterns, hijackPattern{
			regex:       regexp.MustCompile(r.pattern),
			severity:    r.severity,
			description: r.description,
		})
	}
}

// CheckHijacking scans content for potential prompt injection patterns.
// Returns a list of warnings. An empty list means no suspicious patterns found.
func CheckHijacking(content string) []HijackWarning {
	var warnings []HijackWarning
	lines := strings.Split(content, "\n")

	for lineNum, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		for _, p := range patterns {
			if p.regex.MatchString(trimmed) {
				// Truncate context for display
				ctx := trimmed
				if len(ctx) > 120 {
					ctx = ctx[:117] + "..."
				}

				warnings = append(warnings, HijackWarning{
					Pattern:     p.regex.String(),
					Line:        lineNum + 1,
					Context:     ctx,
					Severity:    p.severity,
					Description: p.description,
				})
			}
		}
	}

	return warnings
}

// FormatWarnings produces a human-readable summary of hijacking warnings.
func FormatWarnings(warnings []HijackWarning) string {
	if len(warnings) == 0 {
		return ""
	}

	var sb strings.Builder
	high, medium, low := 0, 0, 0
	for _, w := range warnings {
		switch w.Severity {
		case "high":
			high++
		case "medium":
			medium++
		case "low":
			low++
		}
	}

	sb.WriteString(fmt.Sprintf("⚠ Prompt hijacking scan: %d warning(s) found", len(warnings)))
	if high > 0 {
		sb.WriteString(fmt.Sprintf(" [%d HIGH]", high))
	}
	if medium > 0 {
		sb.WriteString(fmt.Sprintf(" [%d MEDIUM]", medium))
	}
	if low > 0 {
		sb.WriteString(fmt.Sprintf(" [%d LOW]", low))
	}
	sb.WriteString("\n")

	for _, w := range warnings {
		severity := strings.ToUpper(w.Severity)
		sb.WriteString(fmt.Sprintf("  [%s] Line %d: %s\n", severity, w.Line, w.Description))
		sb.WriteString(fmt.Sprintf("         > %s\n", w.Context))
	}

	return sb.String()
}

// HasHighSeverity returns true if any warning is high severity.
func HasHighSeverity(warnings []HijackWarning) bool {
	for _, w := range warnings {
		if w.Severity == "high" {
			return true
		}
	}
	return false
}
