package agent

import (
	"math"
	"strings"
	"testing"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

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
		{
			name:    "valid claude backend uppercase",
			agent:   Agent{Name: "test", Provider: ProviderClaude, Backend: "  OLLAMA  "},
			wantErr: false,
		},
		{
			name:    "backend rejected for non-claude provider",
			agent:   Agent{Name: "test", Provider: ProviderOllama, Backend: ClaudeBackendBedrock},
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
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeClaudeBackend(tt.in); got != tt.want {
				t.Fatalf("NormalizeClaudeBackend(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseClaudeBackend(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty defaults to anthropic", in: "", want: ClaudeBackendAnthropic},
		{name: "anthropic", in: "anthropic", want: ClaudeBackendAnthropic},
		{name: "ollama", in: "ollama", want: ClaudeBackendOllama},
		{name: "bedrock", in: "bedrock", want: ClaudeBackendBedrock},
		{name: "trim and lowercase", in: "  OLLAMA  ", want: ClaudeBackendOllama},
		{name: "unknown returns error", in: "unknown", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseClaudeBackend(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseClaudeBackend(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Fatalf("ParseClaudeBackend(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAllProviders(t *testing.T) {
	providers := ValidProviders()
	if len(providers) != 12 {
		t.Errorf("ValidProviders() len = %d, want 12", len(providers))
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
	agentUsingSharedPrompt := Agent{Name: "agent-using-shared-prompt", Provider: ProviderClaude}
	if got := agentUsingSharedPrompt.EffectiveSystemPrompt(shared); got != "Be helpful and concise." {
		t.Errorf("EffectiveSystemPrompt() = %q, want shared prompt", got)
	}

	// agent with own prompt overrides shared
	agentWithCustomPrompt := Agent{Name: "agent-with-custom-prompt", Provider: ProviderClaude, SystemPrompt: "Custom."}
	if got := agentWithCustomPrompt.EffectiveSystemPrompt(shared); got != "Custom." {
		t.Errorf("EffectiveSystemPrompt() = %q, want agent prompt", got)
	}

	// empty shared, empty agent
	agentWithNoPrompt := Agent{Name: "agent-with-no-prompt", Provider: ProviderClaude}
	if got := agentWithNoPrompt.EffectiveSystemPrompt(SharedConfig{}); got != "" {
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
	agentUsingSharedServers := Agent{Name: "agent-using-shared-servers", Provider: ProviderClaude}
	servers := agentUsingSharedServers.EffectiveMCPServers(shared)
	if len(servers) != 2 {
		t.Fatalf("EffectiveMCPServers() len = %d, want 2", len(servers))
	}

	// agent with overlapping MCP server overrides shared
	agentWithOverrideServer := Agent{
		Name:     "agent-with-override-server",
		Provider: ProviderClaude,
		MCPServers: []MCPServer{
			{Name: "filesystem", Command: "custom-fs", Args: []string{"--custom"}},
		},
	}
	servers = agentWithOverrideServer.EffectiveMCPServers(shared)
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
	agentWithAdditionalServer := Agent{
		Name:     "agent-with-additional-server",
		Provider: ProviderClaude,
		MCPServers: []MCPServer{
			{Name: "custom-tool", Command: "my-tool"},
		},
	}
	servers = agentWithAdditionalServer.EffectiveMCPServers(shared)
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

func TestComputeCostUSD(t *testing.T) {
	pricing := []ProviderPricing{
		{Provider: ProviderClaude, ModelPattern: "haiku", InputPer1KTokens: 0.00025, OutputPer1KTokens: 0.00125, CachedPer1KTokens: 0.00003},
		{Provider: ProviderClaude, ModelPattern: "sonnet", InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015, CachedPer1KTokens: 0.00030},
		{Provider: ProviderClaude, InputPer1KTokens: 0.003, OutputPer1KTokens: 0.015},
		{Provider: ProviderOllama, InputPer1KTokens: 0, OutputPer1KTokens: 0},
	}

	tests := []struct {
		name     string
		usage    *PromptTokenUsage
		provider Provider
		model    string
		pricing  []ProviderPricing
		want     float64
	}{
		{
			name:     "nil usage returns zero",
			usage:    nil,
			provider: ProviderClaude,
			model:    "claude-3-5-sonnet",
			pricing:  pricing,
			want:     0,
		},
		{
			name:     "empty pricing returns zero",
			usage:    &PromptTokenUsage{InputTokens: 1000, OutputTokens: 500},
			provider: ProviderClaude,
			model:    "claude-3-5-sonnet",
			pricing:  nil,
			want:     0,
		},
		{
			name:     "provider not in pricing returns zero",
			usage:    &PromptTokenUsage{InputTokens: 1000, OutputTokens: 500},
			provider: ProviderCodex,
			model:    "gpt-4o",
			pricing:  pricing,
			want:     0,
		},
		{
			name:     "catch-all claude match",
			usage:    &PromptTokenUsage{InputTokens: 1000, OutputTokens: 1000},
			provider: ProviderClaude,
			model:    "claude-3-opus",
			pricing:  pricing,
			// 1000/1000*0.003 + 1000/1000*0.015 = 0.018
			want: 0.018,
		},
		{
			name:     "specific model pattern beats catch-all",
			usage:    &PromptTokenUsage{InputTokens: 1000, OutputTokens: 1000},
			provider: ProviderClaude,
			model:    "claude-3-5-sonnet-20241022",
			pricing:  pricing,
			// sonnet: 1000/1000*0.003 + 1000/1000*0.015 = 0.018 (same rates here but uses sonnet entry)
			want: 0.018,
		},
		{
			name:     "haiku pricing with cached tokens",
			usage:    &PromptTokenUsage{InputTokens: 1000, CachedInputTokens: 500, OutputTokens: 200},
			provider: ProviderClaude,
			model:    "claude-3-haiku",
			pricing:  pricing,
			// 1000/1000*0.00025 + 500/1000*0.00003 + 200/1000*0.00125 = 0.00025 + 0.000015 + 0.00025 = 0.000515
			want: 0.000515,
		},
		{
			name:     "ollama returns zero",
			usage:    &PromptTokenUsage{InputTokens: 10000, OutputTokens: 5000},
			provider: ProviderOllama,
			model:    "llama3",
			pricing:  pricing,
			want:     0,
		},
		{
			name:     "longer model pattern wins over shorter",
			usage:    &PromptTokenUsage{InputTokens: 1000, OutputTokens: 0},
			provider: ProviderClaude,
			model:    "claude-3-5-haiku-20241022",
			pricing: []ProviderPricing{
				{Provider: ProviderClaude, ModelPattern: "haiku", InputPer1KTokens: 0.001, OutputPer1KTokens: 0},
				{Provider: ProviderClaude, ModelPattern: "3-5-haiku", InputPer1KTokens: 0.002, OutputPer1KTokens: 0},
				{Provider: ProviderClaude, InputPer1KTokens: 0.003, OutputPer1KTokens: 0},
			},
			// "3-5-haiku" is longer than "haiku" — should win
			want: 0.002,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCostUSD(tt.usage, tt.provider, tt.model, tt.pricing)
			if !approxEq(got, tt.want) {
				t.Errorf("ComputeCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}
