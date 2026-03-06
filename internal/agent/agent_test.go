package agent

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		wantErr bool
	}{
		{
			name:    "valid agent",
			agent:   Agent{Name: "test", Provider: ProviderClaude},
			wantErr: false,
		},
		{
			name:    "missing name",
			agent:   Agent{Provider: ProviderClaude},
			wantErr: true,
		},
		{
			name:    "missing provider",
			agent:   Agent{Name: "test"},
			wantErr: true,
		},
		{
			name:    "unknown provider",
			agent:   Agent{Name: "test", Provider: "unknown"},
			wantErr: true,
		},
		{
			name:    "valid claude backend",
			agent:   Agent{Name: "test", Provider: ProviderClaude, Backend: ClaudeBackendOllama},
			wantErr: false,
		},
		{
			name:    "invalid claude backend",
			agent:   Agent{Name: "test", Provider: ProviderClaude, Backend: "invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeClaudeBackend(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ClaudeBackendAnthropic},
		{"anthropic", ClaudeBackendAnthropic},
		{"ollama", ClaudeBackendOllama},
		{"bedrock", ClaudeBackendBedrock},
		{"  OLLAMA  ", ClaudeBackendOllama},
		{"unknown", ClaudeBackendAnthropic},
	}
	for _, tt := range tests {
		if got := NormalizeClaudeBackend(tt.in); got != tt.want {
			t.Fatalf("NormalizeClaudeBackend(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAllProviders(t *testing.T) {
	providers := ValidProviders()
	if len(providers) != 10 {
		t.Errorf("ValidProviders() len = %d, want 10", len(providers))
	}
	for _, p := range providers {
		a := Agent{Name: "test", Provider: p}
		if err := a.Validate(); err != nil {
			t.Errorf("Validate() error for provider %q: %v", p, err)
		}
	}
}

func TestEffectiveSystemPrompt(t *testing.T) {
	shared := SharedConfig{SystemPrompt: "Be helpful and concise."}

	// agent without own prompt uses shared
	a1 := Agent{Name: "a1", Provider: ProviderClaude}
	if got := a1.EffectiveSystemPrompt(shared); got != "Be helpful and concise." {
		t.Errorf("EffectiveSystemPrompt() = %q, want shared prompt", got)
	}

	// agent with own prompt overrides shared
	a2 := Agent{Name: "a2", Provider: ProviderClaude, SystemPrompt: "Custom."}
	if got := a2.EffectiveSystemPrompt(shared); got != "Custom." {
		t.Errorf("EffectiveSystemPrompt() = %q, want agent prompt", got)
	}

	// empty shared, empty agent
	a3 := Agent{Name: "a3", Provider: ProviderClaude}
	if got := a3.EffectiveSystemPrompt(SharedConfig{}); got != "" {
		t.Errorf("EffectiveSystemPrompt() = %q, want empty", got)
	}
}

func TestFilenameForInstruction(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"agents", "AGENTS.md"},
		{"claude", "CLAUDE.md"},
		{"codex", "codex.md"},
		{"copilot", ".github/copilot-instructions.md"},
		{"custom-thing", "custom-thing.md"},
	}
	for _, tt := range tests {
		got := FilenameForInstruction(tt.name)
		if got != tt.want {
			t.Errorf("FilenameForInstruction(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestEffectiveMCPServers(t *testing.T) {
	shared := SharedConfig{
		MCPServers: []MCPServer{
			{Name: "filesystem", Command: "npx", Args: []string{"-y", "@anthropic/mcp-server-filesystem"}},
			{Name: "git", Command: "npx", Args: []string{"-y", "@anthropic/mcp-server-git"}},
		},
	}

	// agent with no MCP servers gets all shared
	a1 := Agent{Name: "a1", Provider: ProviderClaude}
	servers := a1.EffectiveMCPServers(shared)
	if len(servers) != 2 {
		t.Fatalf("EffectiveMCPServers() len = %d, want 2", len(servers))
	}

	// agent with overlapping MCP server overrides shared
	a2 := Agent{
		Name:     "a2",
		Provider: ProviderClaude,
		MCPServers: []MCPServer{
			{Name: "filesystem", Command: "custom-fs", Args: []string{"--custom"}},
		},
	}
	servers = a2.EffectiveMCPServers(shared)
	if len(servers) != 2 {
		t.Fatalf("EffectiveMCPServers() len = %d, want 2", len(servers))
	}
	// first should be the agent-specific one
	if servers[0].Command != "custom-fs" {
		t.Errorf("servers[0].Command = %q, want %q", servers[0].Command, "custom-fs")
	}
	// second should be the non-overlapping shared one
	if servers[1].Name != "git" {
		t.Errorf("servers[1].Name = %q, want %q", servers[1].Name, "git")
	}

	// agent with unique MCP server adds to shared
	a3 := Agent{
		Name:     "a3",
		Provider: ProviderClaude,
		MCPServers: []MCPServer{
			{Name: "custom-tool", Command: "my-tool"},
		},
	}
	servers = a3.EffectiveMCPServers(shared)
	if len(servers) != 3 {
		t.Fatalf("EffectiveMCPServers() len = %d, want 3", len(servers))
	}
}

func TestBuildEffectivePromptSortsRulesByPriorityAndRespectsDisabledRules(t *testing.T) {
	a := Agent{
		Name:          "test",
		Provider:      ProviderClaude,
		DisabledRules: []string{"skip-me"},
	}
	shared := SharedConfig{
		Rules: []UnifiedRule{
			{Name: "late", Content: "Late rule", Priority: 50, Enabled: true},
			{Name: "early", Content: "Early rule", Priority: 10, Enabled: true},
			{Name: "skip-me", Content: "Should not appear", Priority: 1, Enabled: true},
		},
	}

	prompt := a.BuildEffectivePrompt(shared)
	earlyIdx := strings.Index(prompt, "Early rule")
	lateIdx := strings.Index(prompt, "Late rule")
	if earlyIdx == -1 || lateIdx == -1 {
		t.Fatalf("expected both rules in prompt, got: %q", prompt)
	}
	if earlyIdx > lateIdx {
		t.Fatalf("rules not sorted by priority in prompt: %q", prompt)
	}
	if strings.Contains(prompt, "Should not appear") {
		t.Fatalf("disabled rule should not appear in prompt: %q", prompt)
	}
}

func TestBuildEffectivePromptPrioritizesRoleRules(t *testing.T) {
	a := Agent{
		Name:     "test",
		Provider: ProviderClaude,
		Role:     "lead",
	}
	shared := SharedConfig{
		Roles: []Role{
			{Name: "lead", Prompt: "Lead role prompt", Rules: []string{"late"}},
		},
		Rules: []UnifiedRule{
			{Name: "early", Content: "Early rule", Priority: 10, Enabled: true},
			{Name: "late", Content: "Late rule", Priority: 50, Enabled: true},
		},
	}

	prompt := a.BuildEffectivePrompt(shared)
	earlyIdx := strings.Index(prompt, "Early rule")
	lateIdx := strings.Index(prompt, "Late rule")
	if earlyIdx == -1 || lateIdx == -1 {
		t.Fatalf("expected both rules in prompt, got: %q", prompt)
	}
	if lateIdx > earlyIdx {
		t.Fatalf("role rule should be prioritized ahead of non-role rules, got: %q", prompt)
	}
}
