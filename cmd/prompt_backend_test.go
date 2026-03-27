package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestValidatePromptBackend_BedrockReturnsExplicitError(t *testing.T) {
	a := agent.Agent{
		Name:     "claude-bedrock",
		Provider: agent.ProviderClaude,
		Backend:  agent.ClaudeBackendBedrock,
	}

	err := validatePromptBackend(a, time.Second)
	if err == nil {
		t.Fatalf("expected error for bedrock validation")
	}
	if !strings.Contains(err.Error(), "bedrock backend validation is not supported yet") {
		t.Fatalf("unexpected bedrock validation error: %v", err)
	}
}

func TestExecutePrompt_BedrockReturnsExplicitError(t *testing.T) {
	a := agent.Agent{
		Name:     "claude-bedrock",
		Provider: agent.ProviderClaude,
		Backend:  agent.ClaudeBackendBedrock,
	}

	_, err := executePrompt(a, "hello", time.Second)
	if err == nil {
		t.Fatalf("expected error for bedrock execution")
	}
	if !strings.Contains(err.Error(), "bedrock backend execution is not supported yet") {
		t.Fatalf("unexpected bedrock execution error: %v", err)
	}
}

func TestExecutePromptAppliesRuntimeConfig(t *testing.T) {
	const apiKey = "env-openai-key"
	t.Setenv("OPENAI_API_KEY", apiKey)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Fatalf("authorization header = %q, want bearer token from env", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	result, err := executePrompt(agent.Agent{
		Provider: agent.ProviderOpenAI,
		Model:    "gpt-5",
		BaseURL:  server.URL,
	}, "hello", time.Second)
	if err != nil {
		t.Fatalf("executePrompt() error = %v", err)
	}
	if result.Response != "done" {
		t.Fatalf("response = %q, want done", result.Response)
	}
}

func TestValidateOllamaEndpoint(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	if err := validateOllamaEndpoint(okServer.URL, time.Second, "ollama validation"); err != nil {
		t.Fatalf("validateOllamaEndpoint() error = %v", err)
	}

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer failServer.Close()

	err := validateOllamaEndpoint(failServer.URL, time.Second, "ollama validation")
	if err == nil {
		t.Fatalf("expected status error")
	}
	if !strings.Contains(err.Error(), "ollama validation failed (400): bad request") {
		t.Fatalf("unexpected status error: %v", err)
	}
}

func TestValidateOpenAIEndpoint(t *testing.T) {
	const apiKey = "sk-test-key"
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Fatalf("authorization header = %q, want bearer token", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer okServer.Close()

	for _, baseURL := range []string{okServer.URL, okServer.URL + "/v1"} {
		baseURL := baseURL
		t.Run("ok:"+baseURL, func(t *testing.T) {
			if err := validateOpenAIEndpoint(baseURL, apiKey, time.Second); err != nil {
				t.Fatalf("validateOpenAIEndpoint() error = %v", err)
			}
		})
	}

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid auth"))
	}))
	defer failServer.Close()

	err := validateOpenAIEndpoint(failServer.URL, apiKey, time.Second)
	if err == nil {
		t.Fatalf("expected status error")
	}
	if !strings.Contains(err.Error(), "openai validation failed (401): invalid auth") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestExecuteOpenAIPrompt(t *testing.T) {
	const apiKey = "sk-test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Fatalf("authorization header = %q, want bearer token", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if body.Model != "gpt-5" {
			t.Fatalf("model = %q, want gpt-5", body.Model)
		}
		if len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
			t.Fatalf("unexpected messages payload: %#v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" done "}}],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}}`))
	}))
	defer server.Close()

	for _, baseURL := range []string{server.URL, server.URL + "/v1"} {
		baseURL := baseURL
		t.Run("ok:"+baseURL, func(t *testing.T) {
			result, err := executeOpenAIPrompt(agent.Agent{
				Provider: agent.ProviderOpenAI,
				Model:    "gpt-5",
				APIKey:   apiKey,
				BaseURL:  baseURL,
			}, "hello", time.Second)
			if err != nil {
				t.Fatalf("executeOpenAIPrompt() error = %v", err)
			}
			if result.Response != "done" {
				t.Fatalf("response = %q, want done", result.Response)
			}
			if result.Usage.InputTokens != 7 || result.Usage.OutputTokens != 5 || result.Usage.TotalTokens != 12 {
				t.Fatalf("unexpected usage: %#v", result.Usage)
			}
		})
	}
}

func TestExecuteOpenAIPromptReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limit"))
	}))
	defer server.Close()

	_, err := executeOpenAIPrompt(agent.Agent{
		Provider: agent.ProviderOpenAI,
		Model:    "gpt-5",
		BaseURL:  server.URL,
	}, "hello", time.Second)
	if err == nil {
		t.Fatalf("expected status error")
	}
	if !strings.Contains(err.Error(), "openai error 429: rate limit") {
		t.Fatalf("unexpected execute error: %v", err)
	}
}

func TestEffectivePromptBackend(t *testing.T) {
	tests := []struct {
		name string
		a    agent.Agent
		want string
	}{
		{
			name: "claude defaults to anthropic",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  "",
			},
			want: agent.ClaudeBackendAnthropic,
		},
		{
			name: "claude explicit backend",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  agent.ClaudeBackendOllama,
			},
			want: agent.ClaudeBackendOllama,
		},
		{
			name: "non-claude returns provider",
			a: agent.Agent{
				Provider: agent.ProviderCodex,
			},
			want: string(agent.ProviderCodex),
		},
		{
			name: "invalid claude backend falls back to normalized default",
			a: agent.Agent{
				Provider: agent.ProviderClaude,
				Backend:  "  CUSTOM  ",
			},
			want: agent.ClaudeBackendAnthropic,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := effectivePromptBackend(tt.a)
			if got != tt.want {
				t.Fatalf("effectivePromptBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}
