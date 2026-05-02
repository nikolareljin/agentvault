package agent

import (
	"fmt"
	"strings"
)

// Auth mode constants for AgentProviderMeta.AuthMode.
const (
	AuthModeAPIKey = "api_key"
	AuthModeOAuth  = "oauth"
	AuthModeIAM    = "iam"
	AuthModeToken  = "token"
	AuthModeNone   = "none"
)

var validAuthModes = []string{AuthModeAPIKey, AuthModeOAuth, AuthModeIAM, AuthModeToken, AuthModeNone}

// ValidateProviderMeta checks that AgentProviderMeta fields are consistent with the
// agent's Provider and Backend. Returns nil for nil meta (existing agents unchanged).
func ValidateProviderMeta(provider Provider, backend string, meta *AgentProviderMeta) error {
	if meta == nil {
		return nil
	}
	if meta.AuthMode != "" {
		globalValid := false
		for _, m := range validAuthModes {
			if meta.AuthMode == m {
				globalValid = true
				break
			}
		}
		if !globalValid {
			return fmt.Errorf("unknown auth_mode %q; valid: api_key, oauth, iam, token, none", meta.AuthMode)
		}
		// For Claude with Bedrock or Ollama backend, auth modes match those providers.
		effectiveProvider := provider
		if provider == ProviderClaude && backend == ClaudeBackendBedrock {
			effectiveProvider = ProviderBedrock
		} else if provider == ProviderClaude && backend == ClaudeBackendOllama {
			effectiveProvider = ProviderOllama
		}
		supported := ProviderAuthModes(effectiveProvider)
		providerValid := false
		for _, m := range supported {
			if meta.AuthMode == m {
				providerValid = true
				break
			}
		}
		if !providerValid {
			if effectiveProvider != provider {
				return fmt.Errorf("auth_mode %q not supported for provider %s (effective: %s); supported: %s",
					meta.AuthMode, provider, effectiveProvider, strings.Join(supported, ", "))
			}
			return fmt.Errorf("auth_mode %q not supported for provider %s; supported: %s",
				meta.AuthMode, provider, strings.Join(supported, ", "))
		}
	}
	if provider == ProviderBedrock || (provider == ProviderClaude && backend == ClaudeBackendBedrock) {
		if meta.AuthMode == AuthModeIAM && meta.BedrockRegion == "" {
			return fmt.Errorf("bedrock_region is required when auth_mode is %q", AuthModeIAM)
		}
	}
	if provider == ProviderCopilot {
		if meta.AuthMode == AuthModeOAuth && meta.CopilotOrg == "" {
			return fmt.Errorf("copilot_org is required when auth_mode is %q", AuthModeOAuth)
		}
	}
	return nil
}

// DefaultProviderMeta returns sensible defaults for a provider/backend pair.
// Returns nil for providers with no required metadata.
func DefaultProviderMeta(provider Provider, backend string) *AgentProviderMeta {
	switch provider {
	case ProviderBedrock:
		return &AgentProviderMeta{AuthMode: AuthModeIAM}
	case ProviderClaude:
		if backend == ClaudeBackendBedrock {
			return &AgentProviderMeta{AuthMode: AuthModeIAM}
		}
		if backend == ClaudeBackendOllama {
			return &AgentProviderMeta{AuthMode: AuthModeNone}
		}
		return &AgentProviderMeta{AuthMode: AuthModeAPIKey}
	case ProviderCopilot:
		return &AgentProviderMeta{AuthMode: AuthModeOAuth}
	case ProviderGemini:
		return &AgentProviderMeta{AuthMode: AuthModeAPIKey}
	case ProviderOpenAI:
		return &AgentProviderMeta{AuthMode: AuthModeAPIKey}
	case ProviderOllama:
		return &AgentProviderMeta{AuthMode: AuthModeNone}
	default:
		return nil
	}
}

// ProviderAuthModes returns the auth modes supported by a provider.
func ProviderAuthModes(provider Provider) []string {
	switch provider {
	case ProviderBedrock:
		return []string{AuthModeIAM, AuthModeAPIKey}
	case ProviderCopilot:
		return []string{AuthModeOAuth, AuthModeToken}
	case ProviderOllama:
		return []string{AuthModeNone, AuthModeAPIKey}
	case ProviderGemini, ProviderOpenAI, ProviderClaude, ProviderCodex:
		return []string{AuthModeAPIKey}
	default:
		return []string{AuthModeAPIKey, AuthModeNone}
	}
}
