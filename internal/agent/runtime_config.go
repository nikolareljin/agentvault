package agent

import (
	"errors"
	"fmt"
	"net"
	"net/url"
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

// OllamaBaseURLEnv names the Ollama endpoint. OllamaHostEnv is read only when
// it is unset, and only for this release: it is also Ollama's own bind address.
const (
	OllamaBaseURLEnv = "OLLAMA_BASE_URL"
	OllamaHostEnv    = "OLLAMA_HOST"
)

var ollamaBaseURLEnvKeys = []string{OllamaBaseURLEnv, OllamaHostEnv}

// ResolvedValue contains an effective value plus its source. EnvKey names the
// variable the value came from when Source is ValueSourceEnv.
type ResolvedValue struct {
	Value  string
	Source ValueSource
	EnvKey string
}

// Deprecation returns a one-line notice when the value came from a deprecated
// environment variable, else "".
func (r ResolvedValue) Deprecation() string {
	if r.Source == ValueSourceEnv && r.EnvKey == OllamaHostEnv {
		return OllamaHostEnv + " is deprecated, set " + OllamaBaseURLEnv
	}
	return ""
}

// OllamaBaseURLFromEnv returns the Ollama endpoint from the environment and
// the variable it came from, or "" and "" when neither is set.
func OllamaBaseURLFromEnv() (value, key string) {
	r := resolveOllamaBaseURL("", "")
	return r.Value, r.EnvKey
}

// resolveOllamaBaseURL is resolveValue for the Ollama endpoint. A value from
// the environment is read the way Ollama reads OLLAMA_HOST: "host" or
// "host:port" means http, port 11434. A listen-on-all address (0.0.0.0, ::)
// becomes loopback, which Windows needs to connect.
func resolveOllamaBaseURL(local, defaultValue string) ResolvedValue {
	r := resolveValue(local, ollamaBaseURLEnvKeys, defaultValue)
	if r.Source == ValueSourceEnv {
		r.Value = normalizeOllamaEnvURL(r.Value)
	}
	return r
}

func normalizeOllamaEnvURL(v string) string {
	if !strings.Contains(v, "://") {
		hostport, path, _ := strings.Cut(v, "/")
		if _, _, err := net.SplitHostPort(hostport); err != nil {
			hostport = net.JoinHostPort(strings.Trim(hostport, "[]"), "11434")
		}
		v = "http://" + hostport
		if path != "" {
			v += "/" + path
		}
	}
	u, err := url.Parse(v)
	if err != nil {
		return v
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
		loopback := "127.0.0.1"
		if ip.To4() == nil {
			loopback = "::1"
		}
		if port := u.Port(); port != "" {
			u.Host = net.JoinHostPort(loopback, port)
		} else if ip.To4() == nil {
			u.Host = "[" + loopback + "]"
		} else {
			u.Host = loopback
		}
	}
	return u.String()
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
			cfg.BaseURL = resolveOllamaBaseURL(strings.TrimSpace(a.BaseURL), "http://localhost:11434")
		case ClaudeBackendBedrock, ClaudeBackendVertex:
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
		case ClaudeBackendGateway:
			// No fallback to the shell's ANTHROPIC_* values: an Anthropic key
			// must not be sent to a gateway the agent names.
			cfg.APIKey = resolveValue(strings.TrimSpace(a.APIKey), nil, "")
			cfg.BaseURL = resolveValue(strings.TrimSpace(a.BaseURL), nil, "")
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
		cfg.BaseURL = resolveOllamaBaseURL(strings.TrimSpace(a.BaseURL), "http://localhost:11434")
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
		if err := validateVertexTarget(region, project); err != nil {
			return nil, err
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
		case gw != nil && strings.TrimSpace(gw.Token) != "" && SameGatewayURL(base, gw.URL):
			// The stored token goes only to the stored gateway's own URL.
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
			return ResolvedValue{Value: value, Source: ValueSourceEnv, EnvKey: key}
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
