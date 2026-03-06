package agent

import "testing"

func TestResolvePromptRuntimeConfig_PrefersLocalOverEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-openai-key")
	a := Agent{
		Provider: ProviderCodex,
		Model:    "gpt-5-codex",
		APIKey:   "local-openai-key",
	}

	cfg := ResolvePromptRuntimeConfig(a)
	if cfg.APIKey.Value != "local-openai-key" {
		t.Fatalf("api key = %q, want local value", cfg.APIKey.Value)
	}
	if cfg.APIKey.Source != ValueSourceLocal {
		t.Fatalf("api key source = %q, want %q", cfg.APIKey.Source, ValueSourceLocal)
	}
}

func TestResolvePromptRuntimeConfig_UsesEnvFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
	a := Agent{
		Provider: ProviderClaude,
		Model:    "claude-sonnet",
	}

	cfg := ResolvePromptRuntimeConfig(a)
	if cfg.APIKey.Value != "env-anthropic-key" {
		t.Fatalf("api key = %q, want env value", cfg.APIKey.Value)
	}
	if cfg.APIKey.Source != ValueSourceEnv {
		t.Fatalf("api key source = %q, want %q", cfg.APIKey.Source, ValueSourceEnv)
	}
}

func TestResolvePromptRuntimeConfig_UsesDefaultFallback(t *testing.T) {
	a := Agent{
		Provider: ProviderOllama,
		Model:    "llama3.2",
	}

	cfg := ResolvePromptRuntimeConfig(a)
	if cfg.BaseURL.Value != "http://localhost:11434" {
		t.Fatalf("base url = %q, want default", cfg.BaseURL.Value)
	}
	if cfg.BaseURL.Source != ValueSourceDefault {
		t.Fatalf("base url source = %q, want %q", cfg.BaseURL.Source, ValueSourceDefault)
	}
}

func TestResolvePromptRuntimeConfig_UsesEnvForOllamaBaseURL(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://env-ollama:11434")
	a := Agent{
		Provider: ProviderOllama,
		Model:    "llama3.2",
	}

	cfg := ResolvePromptRuntimeConfig(a)
	if cfg.BaseURL.Value != "http://env-ollama:11434" {
		t.Fatalf("base url = %q, want env value", cfg.BaseURL.Value)
	}
	if cfg.BaseURL.Source != ValueSourceEnv {
		t.Fatalf("base url source = %q, want %q", cfg.BaseURL.Source, ValueSourceEnv)
	}
}
