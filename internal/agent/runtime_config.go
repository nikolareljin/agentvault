package agent

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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
		switch NormalizeClaudeBackend(a.Backend) {
		case ClaudeBackendOllama:
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), []string{"OLLAMA_HOST"}, "http://localhost:11434")
		case ClaudeBackendBedrock, ClaudeBackendVertex:
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
		case ClaudeBackendGateway:
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), []string{"ANTHROPIC_BASE_URL"}, "")
		default:
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"ANTHROPIC_API_KEY"}, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
		}
	case ProviderCodex, ProviderOpenAI:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"OPENAI_API_KEY"}, "")
		cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
	case ProviderGemini:
		cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, "")
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

// ClaudeRuntimeEnv returns the environment the claude CLI reads to run
// agent a on its backend. gw is the stored gateway a gateway backend names
// in ProviderMeta.GatewayName, or nil; the agent's own base URL and key win
// over it. The anthropic and ollama backends need nothing here.
func ClaudeRuntimeEnv(a Agent, gw *Gateway) (map[string]string, error) {
	meta := a.ProviderMeta
	if meta == nil {
		meta = &AgentProviderMeta{}
	}
	env := map[string]string{}
	switch NormalizeClaudeBackend(a.Backend) {
	case ClaudeBackendBedrock:
		env["CLAUDE_CODE_USE_BEDROCK"] = "1"
		region := strings.TrimSpace(meta.BedrockRegion)
		if region == "" {
			region = strings.TrimSpace(meta.AWSRegion)
		}
		if region != "" {
			env["AWS_REGION"] = region
		}
		if p := strings.TrimSpace(meta.AWSProfile); p != "" {
			env["AWS_PROFILE"] = p
		}
	case ClaudeBackendVertex:
		region, project := strings.TrimSpace(meta.VertexRegion), strings.TrimSpace(meta.VertexProject)
		if region == "" || project == "" {
			return nil, errors.New("claude vertex backend needs a region and a project")
		}
		env["CLAUDE_CODE_USE_VERTEX"] = "1"
		env["CLOUD_ML_REGION"] = region
		env["ANTHROPIC_VERTEX_PROJECT_ID"] = project
	case ClaudeBackendGateway:
		base := strings.TrimSpace(a.BaseURL)
		if base == "" && gw != nil {
			base = strings.TrimSpace(gw.URL)
		}
		if base == "" {
			if meta.GatewayName != "" {
				return nil, fmt.Errorf("claude gateway backend: stored gateway %q not found", meta.GatewayName)
			}
			return nil, errors.New("claude gateway backend needs a base URL")
		}
		if err := validateGatewayBaseURL(base); err != nil {
			return nil, err
		}
		env["ANTHROPIC_BASE_URL"] = base
		switch key := strings.TrimSpace(a.APIKey); {
		case key != "" && meta.AuthMode == AuthModeToken:
			env["ANTHROPIC_AUTH_TOKEN"] = key
		case key != "":
			env["ANTHROPIC_API_KEY"] = key
		case gw != nil && strings.TrimSpace(gw.Token) != "":
			env["ANTHROPIC_AUTH_TOKEN"] = strings.TrimSpace(gw.Token)
		}
	}
	return env, nil
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

func LookupPath(data map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = data
	for _, p := range parts {
		switch value := cur.(type) {
		case map[string]any:
			next, ok := value[p]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			index, err := strconv.Atoi(p)
			if err != nil || index < 0 || index >= len(value) {
				return nil, false
			}
			cur = value[index]
		default:
			return nil, false
		}
	}
	return cur, true
}
