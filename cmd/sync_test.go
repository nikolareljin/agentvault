package cmd

import (
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestGenerateAgentsMDIncludesUnknownCategories(t *testing.T) {
	shared := agent.SharedConfig{
		Rules: []agent.UnifiedRule{
			{
				Name:        "default-rule",
				Description: "Default",
				Category:    "general",
				Priority:    1,
				Content:     "General rule",
				Enabled:     true,
			},
			{
				Name:        "custom-rule",
				Description: "Custom",
				Category:    "deployment",
				Priority:    2,
				Content:     "Deployment rule",
				Enabled:     true,
			},
		},
	}

	content := generateAgentsMD(shared, false)
	if !strings.Contains(content, "## Deployment") {
		t.Fatalf("generated AGENTS.md missing custom category section:\n%s", content)
	}
	if !strings.Contains(content, "Deployment rule") {
		t.Fatalf("generated AGENTS.md missing custom category content:\n%s", content)
	}
}
