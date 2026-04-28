package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestSetupBundleMarshalIncludesWorkflowTemplatesField(t *testing.T) {
	data, err := json.Marshal(SetupBundle{})
	if err != nil {
		t.Fatalf("json.Marshal(SetupBundle{}) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(marshal output) error = %v", err)
	}
	if _, ok := payload["workflow_templates"]; !ok {
		t.Fatalf("marshal output keys = %v, want workflow_templates field present", payload)
	}
	for _, key := range []string{"provider_files", "project_files", "instruction_overrides", "skill_assets"} {
		value, ok := payload[key]
		if !ok {
			t.Fatalf("marshal output keys = %v, want %s field present", payload, key)
		}
		items, ok := value.([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("marshal output field %s = %#v, want empty array", key, value)
		}
	}
}

func TestSelectAgentsForExport(t *testing.T) {
	agents := []agent.Agent{
		{Name: "alpha"},
		{Name: "beta"},
	}
	selected, err := selectAgentsForExport(agents, "beta")
	if err != nil {
		t.Fatalf("selectAgentsForExport() error = %v", err)
	}
	if len(selected) != 1 || selected[0].Name != "beta" {
		t.Fatalf("selectAgentsForExport() = %#v, want only beta", selected)
	}
}

func TestSelectAgentsForExportMissing(t *testing.T) {
	_, err := selectAgentsForExport([]agent.Agent{{Name: "alpha"}}, "beta")
	if err == nil || !strings.Contains(err.Error(), `agent "beta" not found`) {
		t.Fatalf("selectAgentsForExport() err = %v, want missing agent error", err)
	}
}

func TestFilterSessionsForAgents(t *testing.T) {
	input := agent.SessionConfig{
		Sessions: []agent.Session{
			{
				ID: "s1",
				Agents: []agent.SessionAgent{
					{Name: "alpha"},
					{Name: "beta"},
				},
			},
			{
				ID: "s2",
				Agents: []agent.SessionAgent{
					{Name: "gamma"},
				},
			},
		},
		ActiveSession: "s2",
		DefaultAgents: []string{"alpha", "beta"},
	}

	filtered := filterSessionsForAgents(input, []agent.Agent{{Name: "alpha"}})
	if len(filtered.Sessions) != 1 {
		t.Fatalf("filtered sessions = %#v, want one session", filtered.Sessions)
	}
	if len(filtered.Sessions[0].Agents) != 1 || filtered.Sessions[0].Agents[0].Name != "alpha" {
		t.Fatalf("filtered session agents = %#v, want only alpha", filtered.Sessions[0].Agents)
	}
	if filtered.ActiveSession != "" {
		t.Fatalf("filtered active session = %q, want cleared", filtered.ActiveSession)
	}
	if len(filtered.DefaultAgents) != 1 || filtered.DefaultAgents[0] != "alpha" {
		t.Fatalf("filtered default agents = %#v, want only alpha", filtered.DefaultAgents)
	}
}

func TestFilterProjectFilesForStagingSkipsInstructionOverrides(t *testing.T) {
	projectFiles := []SetupAsset{
		{
			Kind:                setupAssetKindProjectFile,
			LogicalRoot:         setupAssetRootProject,
			LogicalPath:         "AGENTS.md",
			ProjectRelativePath: "AGENTS.md",
		},
		{
			Kind:                setupAssetKindProjectFile,
			LogicalRoot:         setupAssetRootProject,
			LogicalPath:         filepath.ToSlash(filepath.Join("docs", "guide.md")),
			ProjectRelativePath: filepath.ToSlash(filepath.Join("docs", "guide.md")),
		},
	}
	instructionOverrides := []SetupAsset{{
		Kind:                setupAssetKindInstruction,
		LogicalRoot:         setupAssetRootProject,
		LogicalPath:         "AGENTS.md",
		ProjectRelativePath: "AGENTS.md",
	}}

	filtered := filterProjectFilesForStaging(projectFiles, instructionOverrides)
	if len(filtered) != 1 {
		t.Fatalf("filterProjectFilesForStaging() = %#v, want one non-instruction project file", filtered)
	}
	if filtered[0].LogicalPath != filepath.ToSlash(filepath.Join("docs", "guide.md")) {
		t.Fatalf("filterProjectFilesForStaging() = %#v, want docs/guide.md preserved", filtered)
	}
}

func TestSetupImportFlagDescribesProviderAssets(t *testing.T) {
	flag := setupImportCmd.Flags().Lookup("apply-provider-configs")
	if flag == nil {
		t.Fatal("apply-provider-configs flag missing")
	}
	if !strings.Contains(flag.Usage, "provider asset files") {
		t.Fatalf("apply-provider-configs usage = %q, want provider asset files mentioned", flag.Usage)
	}
}

func TestBuildInstallGuideMentionsProviderAssets(t *testing.T) {
	guide := generateInstallGuide(SetupBundle{
		ProviderConfigs: agent.ProviderConfig{
			Claude: &agent.ClaudeConfig{},
		},
	})
	for _, step := range guide.Steps {
		if step.Name == "Apply provider configs" {
			if !strings.Contains(step.Description, "provider assets") {
				t.Fatalf("provider config step description = %q, want provider assets mentioned", step.Description)
			}
			if len(step.Commands) == 0 || !strings.Contains(step.Commands[0], "--apply-provider-configs") {
				t.Fatalf("provider config step commands = %v, want --apply-provider-configs example", step.Commands)
			}
			return
		}
	}
	t.Fatal("provider config install guide step missing")
}

func TestResolveAgentEnvAPIKey_ReturnsVaultKeyWhenPresent(t *testing.T) {
	a := agent.Agent{Provider: agent.ProviderClaude, APIKey: "vault-key"}
	if got := resolveAgentEnvAPIKey(a); got != "vault-key" {
		t.Fatalf("resolveAgentEnvAPIKey() = %q, want vault-key", got)
	}
}

func TestResolveAgentEnvAPIKey_FallsBackToEnvVar(t *testing.T) {
	tests := []struct {
		provider agent.Provider
		envVar   string
	}{
		{agent.ProviderClaude, "ANTHROPIC_API_KEY"},
		{agent.ProviderOpenAI, "OPENAI_API_KEY"},
		{agent.ProviderGemini, "GEMINI_API_KEY"},
		{agent.ProviderCodex, "OPENAI_API_KEY"},
	}
	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Setenv(tc.envVar, "env-key-"+string(tc.provider))
			a := agent.Agent{Provider: tc.provider, APIKey: ""}
			got := resolveAgentEnvAPIKey(a)
			if got != "env-key-"+string(tc.provider) {
				t.Fatalf("resolveAgentEnvAPIKey(%s) = %q, want env key", tc.provider, got)
			}
		})
	}
}

func TestResolveAgentEnvAPIKey_GeminiFallsBackToGoogleAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "google-key")
	a := agent.Agent{Provider: agent.ProviderGemini, APIKey: ""}
	if got := resolveAgentEnvAPIKey(a); got != "google-key" {
		t.Fatalf("resolveAgentEnvAPIKey(gemini, GOOGLE_API_KEY) = %q, want google-key", got)
	}
}

func TestResolveAgentEnvAPIKey_ReturnsEmptyForUnknownProvider(t *testing.T) {
	a := agent.Agent{Provider: agent.ProviderOllama, APIKey: ""}
	if got := resolveAgentEnvAPIKey(a); got != "" {
		t.Fatalf("resolveAgentEnvAPIKey(ollama) = %q, want empty", got)
	}
}

func TestResolveAgentEnvAPIKey_ReturnsEmptyWhenEnvVarUnset(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	a := agent.Agent{Provider: agent.ProviderClaude, APIKey: ""}
	if got := resolveAgentEnvAPIKey(a); got != "" {
		t.Fatalf("resolveAgentEnvAPIKey() = %q, want empty when env var unset", got)
	}
}

func TestAgentKeyStatus_Redacted(t *testing.T) {
	a := agent.Agent{Name: "my-claude", Provider: agent.ProviderClaude, APIKey: ""}
	if got := agentKeyStatus(a, false, false); got != "[redacted]" {
		t.Fatalf("agentKeyStatus(includeKeys=false) = %q, want [redacted]", got)
	}
}

func TestAgentKeyStatus_NoKeyNeeded(t *testing.T) {
	a := agent.Agent{Name: "my-ollama", Provider: agent.ProviderOllama, APIKey: ""}
	if got := agentKeyStatus(a, true, false); got != "[no key needed]" {
		t.Fatalf("agentKeyStatus(ollama, includeKeys=true) = %q, want [no key needed]", got)
	}
}

func TestAgentKeyStatus_NoKeyNeededWhenNotIncluded(t *testing.T) {
	a := agent.Agent{Name: "my-ollama", Provider: agent.ProviderOllama, APIKey: ""}
	if got := agentKeyStatus(a, false, false); got != "[no key needed]" {
		t.Fatalf("agentKeyStatus(ollama, includeKeys=false) = %q, want [no key needed]", got)
	}
}

func TestAgentKeyStatus_NoKeyFound(t *testing.T) {
	a := agent.Agent{Name: "my-claude", Provider: agent.ProviderClaude, APIKey: ""}
	if got := agentKeyStatus(a, true, false); got != "[no key found]" {
		t.Fatalf("agentKeyStatus(claude, no key) = %q, want [no key found]", got)
	}
}

func TestAgentKeyStatus_VaultKey(t *testing.T) {
	a := agent.Agent{Name: "my-claude", Provider: agent.ProviderClaude, APIKey: "sk-vault"}
	if got := agentKeyStatus(a, true, false); got != "[vault key included]" {
		t.Fatalf("agentKeyStatus(vault key) = %q, want [vault key included]", got)
	}
}

func TestAgentKeyStatus_EnvKey(t *testing.T) {
	a := agent.Agent{Name: "my-claude", Provider: agent.ProviderClaude, APIKey: "sk-env"}
	if got := agentKeyStatus(a, true, true); got != "[env key included]" {
		t.Fatalf("agentKeyStatus(env key) = %q, want [env key included]", got)
	}
}

func TestSetupImportMergesSharedRouterConfig(t *testing.T) {
	imported := agent.SharedConfig{
		Router: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: "./python/langgraph_router.py", AllowFallbacks: true},
	}

	current := agent.SharedConfig{
		Router: agent.RouterConfig{Mode: "heuristic", PreferLocal: true},
	}

	withoutMerge := current
	if action := mergeSharedRouterConfig(&withoutMerge, imported, false); action != "" {
		t.Fatalf("mergeSharedRouterConfig() action = %q, want empty action without merge", action)
	}
	if withoutMerge.Router.Mode != "heuristic" || !withoutMerge.Router.PreferLocal {
		t.Fatalf("router config changed without merge: %#v", withoutMerge.Router)
	}

	withMerge := current
	if action := mergeSharedRouterConfig(&withMerge, imported, true); action != "Updated" {
		t.Fatalf("mergeSharedRouterConfig() action = %q, want Updated", action)
	}
	if withMerge.Router.Mode != "langgraph" || withMerge.Router.LangGraphCmd != "./python/langgraph_router.py" || !withMerge.Router.AllowFallbacks {
		t.Fatalf("router config after merge = %#v, want imported router", withMerge.Router)
	}

	empty := agent.SharedConfig{}
	if action := mergeSharedRouterConfig(&empty, imported, false); action != "Imported" {
		t.Fatalf("mergeSharedRouterConfig() action = %q, want Imported", action)
	}
	if empty.Router.Mode != "langgraph" {
		t.Fatalf("router config for empty shared config = %#v, want imported router", empty.Router)
	}
}
