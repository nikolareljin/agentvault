package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func testServeVault(t *testing.T) *vault.Vault {
	t.Helper()
	v := vault.New(t.TempDir() + "/vault.enc")
	if err := v.Init("test-pass"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := v.Add(agent.Agent{
		Name:     "claude-main",
		Provider: agent.ProviderClaude,
		Model:    "claude-3-7-sonnet",
		APIKey:   "sk-sensitive",
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	return v
}

func TestServeMuxUnauthorizedWithoutAPIKeyHeader(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestServeMuxAcceptsBearerToken(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServeMuxMethodNotAllowed(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/agents", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestServeMuxAgentsResponseExcludesAPIKey(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/agents", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("len(payload) = %d, want 1", len(payload))
	}
	if _, found := payload[0]["api_key"]; found {
		t.Fatalf("unexpected api_key field in response: %#v", payload[0])
	}
}
