package agent

import "testing"

func TestValidateProviderMeta_nil(t *testing.T) {
	for _, p := range ValidProviders() {
		if err := ValidateProviderMeta(p, "", nil); err != nil {
			t.Errorf("provider %s: expected nil error for nil meta, got %v", p, err)
		}
	}
}

func TestValidateProviderMeta_unknownAuthMode(t *testing.T) {
	meta := &AgentProviderMeta{AuthMode: "magic"}
	if err := ValidateProviderMeta(ProviderClaude, "", meta); err == nil {
		t.Error("expected error for unknown auth_mode, got nil")
	}
}

func TestValidateProviderMeta_bedrockIAMRequiresRegion(t *testing.T) {
	meta := &AgentProviderMeta{AuthMode: AuthModeIAM}
	if err := ValidateProviderMeta(ProviderBedrock, "", meta); err == nil {
		t.Error("expected error: bedrock IAM without region")
	}
	meta.BedrockRegion = "us-east-1"
	if err := ValidateProviderMeta(ProviderBedrock, "", meta); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateProviderMeta_claudeBedrockIAMRequiresRegion(t *testing.T) {
	meta := &AgentProviderMeta{AuthMode: AuthModeIAM}
	if err := ValidateProviderMeta(ProviderClaude, ClaudeBackendBedrock, meta); err == nil {
		t.Error("expected error: claude+bedrock IAM without region")
	}
	meta.BedrockRegion = "us-west-2"
	if err := ValidateProviderMeta(ProviderClaude, ClaudeBackendBedrock, meta); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateProviderMeta_copilotOAuthRequiresOrg(t *testing.T) {
	meta := &AgentProviderMeta{AuthMode: AuthModeOAuth}
	if err := ValidateProviderMeta(ProviderCopilot, "", meta); err == nil {
		t.Error("expected error: copilot OAuth without org")
	}
	meta.CopilotOrg = "myorg"
	if err := ValidateProviderMeta(ProviderCopilot, "", meta); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultProviderMeta_roundTrip(t *testing.T) {
	cases := []struct {
		provider Provider
		backend  string
	}{
		{ProviderBedrock, ""},
		{ProviderClaude, ClaudeBackendBedrock},
		{ProviderCopilot, ""},
		{ProviderGemini, ""},
		{ProviderOpenAI, ""},
		{ProviderOllama, ""},
	}
	for _, tc := range cases {
		meta := DefaultProviderMeta(tc.provider, tc.backend)
		if meta == nil {
			continue
		}
		// Bedrock/Claude-bedrock IAM default needs region to validate — supply one.
		if meta.AuthMode == AuthModeIAM {
			meta.BedrockRegion = "us-east-1"
		}
		// Copilot OAuth default needs an org to validate — supply one.
		if tc.provider == ProviderCopilot && meta.AuthMode == AuthModeOAuth {
			meta.CopilotOrg = "testorg"
		}
		if err := ValidateProviderMeta(tc.provider, tc.backend, meta); err != nil {
			t.Errorf("provider %s backend %s: DefaultProviderMeta failed ValidateProviderMeta: %v", tc.provider, tc.backend, err)
		}
	}
}

func TestProviderAuthModes_nonEmpty(t *testing.T) {
	for _, p := range ValidProviders() {
		modes := ProviderAuthModes(p)
		if len(modes) == 0 {
			t.Errorf("provider %s: ProviderAuthModes returned empty slice", p)
		}
	}
}
