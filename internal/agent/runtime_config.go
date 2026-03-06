package agent

import (
	"os"
	"strings"
)

// ValueSource indicates where an effective runtime value came from.
type ValueSource string

const (
	ValueSourceLocal   ValueSource = "local"
	ValueSourceEnv     ValueSource = "env"
	ValueSourceDefault ValueSource = "default"
	ValueSourceUnset   ValueSource = "unset"
)

// ResolvedValue contains an effective value plus its source.
type ResolvedValue struct {
	Value  string
	Source ValueSource
}

// PromptRuntimeConfig captures effective prompt-runtime values with precedence metadata.
type PromptRuntimeConfig struct {
	Model   ResolvedValue
	APIKey  ResolvedValue
	BaseURL ResolvedValue
}

// ResolvePromptRuntimeConfig resolves runtime values with precedence:
// local agent settings > process environment > built-in defaults.
func ResolvePromptRuntimeConfig(a Agent) PromptRuntimeConfig {
	cfg := PromptRuntimeConfig{
		Model: resolveValue(strings.TrimSpace(a.Model), nil, ""),
	}

	switch a.Provider {
	case ProviderClaude:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"ANTHROPIC_API_KEY"}, "")
		cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
	case ProviderCodex, ProviderOpenAI:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"OPENAI_API_KEY"}, "")
		cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
	case ProviderOllama:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
		cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), []string{"OLLAMA_HOST"}, "http://localhost:11434")
	default:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
		cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
	}

	return cfg
}

func resolveValue(local string, envKeys []string, defaultValue string) ResolvedValue {
	if local != "" {
		return ResolvedValue{Value: local, Source: ValueSourceLocal}
	}
	for _, key := range envKeys {
		if key == "" {
			continue
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return ResolvedValue{Value: value, Source: ValueSourceEnv}
		}
	}
	if defaultValue != "" {
		return ResolvedValue{Value: defaultValue, Source: ValueSourceDefault}
	}
	return ResolvedValue{Source: ValueSourceUnset}
}
