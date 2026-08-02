package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// --- POST /api/v1/route ------------------------------------------------------
//
// Routing was reachable only from the CLI, so every other service that wanted
// routed inference shipped its own provider layer and duplicated the decision,
// the cost tracking and the fallback behaviour. These cover the write surface
// that ends that.

func postRoute(t *testing.T, srv *httptest.Server, apiKey, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/route", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func TestRouteRequiresAuthLikeEveryOtherEndpoint(t *testing.T) {
	// A write endpoint that spends money and reaches external providers must
	// be no easier to reach than the read-only ones.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	resp := postRoute(t, srv, "", `{"prompt":"hello"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRouteRejectsGet(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/route")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestRouteRejectsAnEmptyPrompt(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp := postRoute(t, srv, "", `{"prompt":"   "}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRouteRejectsMalformedJSON(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp := postRoute(t, srv, "", `{"prompt":`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRouteReturnsADecisionWithItsCandidates(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp := postRoute(t, srv, "", `{"prompt":"refactor this function","allow_fallbacks":true}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body["mode"] == nil {
		t.Fatal("response has no mode; a caller cannot tell how the decision was reached")
	}
	if body["selected"] == nil {
		t.Fatal("response has no selection")
	}
	// Every candidate considered, not only the winner: a routing decision
	// nobody can inspect is one nobody can debug when it picks something
	// surprising.
	if _, ok := body["candidates"]; !ok {
		t.Fatal("response omits the candidates that were considered")
	}
}

func TestRouteDoesNotLeakApiKeys(t *testing.T) {
	// The read endpoints are careful never to return credentials. A new
	// endpoint that serialises agents has to be equally careful, and it is
	// easy for that to regress unnoticed.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp := postRoute(t, srv, "", `{"prompt":"anything"}`)
	defer resp.Body.Close()

	// Assert success first. Without this the test passes when routing starts
	// erroring, having never exercised the payload where a leak would appear.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; the leak check never saw a real payload", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if strings.Contains(string(raw), "sk-sensitive") {
		t.Fatal("the routing response contained an agent API key")
	}
	// The field name too: a serialiser that starts emitting an empty api_key
	// today is one that emits a populated one after the next change.
	if strings.Contains(string(raw), "api_key") {
		t.Fatalf("the routing response contained an api_key field: %s", string(raw))
	}
}

func TestUnsatisfiableConstraintsAre422(t *testing.T) {
	// The caller asked for something no agent can do. That is their answer.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	// The only agent is a remote provider, and local_only excludes it.
	resp := postRoute(t, srv, "", `{"prompt":"review this","local_only":true}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestAMisconfiguredRouterIs500Not422(t *testing.T) {
	// langgraph mode with no script path is an operator problem. Reporting it
	// as 422 would tell the caller they got their input wrong and send whoever
	// is debugging it looking in the wrong place.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	t.Setenv("AGENTVAULT_LANGGRAPH_ROUTER_CMD", "")
	resp := postRoute(t, srv, "", `{"prompt":"anything","mode":"langgraph"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestLocalOnlyImpliesPreferLocal(t *testing.T) {
	// The stronger claim implies the weaker one, so a caller cannot end up
	// with local_only set and prefer_local unset and have to wonder which won.
	cfg := routeRequest{LocalOnly: true}.toRouterConfig()

	if !cfg.LocalOnly || !cfg.PreferLocal {
		t.Fatalf("local_only=true gave LocalOnly=%v PreferLocal=%v, want both true", cfg.LocalOnly, cfg.PreferLocal)
	}
}
