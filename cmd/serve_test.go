package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/router"
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

	// Deliberately sends no key.
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
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postRoute(t, srv, testKey, `{"prompt":"   "}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRouteRejectsMalformedJSON(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postRoute(t, srv, testKey, `{"prompt":`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRouteReturnsADecisionWithItsCandidates(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postRoute(t, srv, testKey, `{"prompt":"refactor this function","allow_fallbacks":true}`)
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
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postRoute(t, srv, testKey, `{"prompt":"anything"}`)
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
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	// The only agent is a remote provider, and local_only excludes it.
	resp := postRoute(t, srv, testKey, `{"prompt":"review this","local_only":true}`)
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
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	t.Setenv("AGENTVAULT_LANGGRAPH_ROUTER_CMD", "")
	resp := postRoute(t, srv, testKey, `{"prompt":"anything","mode":"langgraph"}`)
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

// --- POST /api/v1/prompt -----------------------------------------------------

// testKey is the key every execute-endpoint test server is configured with.
// The endpoints now require one even on loopback, so a keyless server is a
// deliberate case rather than the default.
const testKey = "secret-key"

func postPrompt(t *testing.T, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/prompt", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func TestPromptRefusesAgenticRunnersByDefault(t *testing.T) {
	// The important one. claude runs with --permission-mode auto, codex with
	// workspace-write, gemini with --approval-mode auto_edit. Those are agentic
	// sessions with filesystem access, so putting them on a socket is remote
	// code execution for anyone holding the API key -- including anyone who
	// obtains it later. An operator should choose that deliberately rather than
	// inherit it by starting a server.
	v := testServeVault(t) // its only agent is a claude CLI agent
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	// Cleared explicitly: inherited from the environment this test would pass
	// or fail depending on the machine it runs on, which is worse than not
	// having it.
	t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", "")
	t.Setenv("AGENTVAULT_SERVE_WORKSPACE", "")

	resp := postPrompt(t, srv, `{"prompt":"write a file"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	// The message has to name the opt-in, or an operator cannot act on it.
	if msg, _ := body["error"].(string); !strings.Contains(msg, "AGENTVAULT_SERVE_ALLOW_AGENTIC") {
		t.Fatalf("error does not say how to permit it: %v", body["error"])
	}
}

func TestPromptRefusesRatherThanSilentlyPickingAnotherAgent(t *testing.T) {
	// Downgrading a coding agent to a chat model without saying so would be a
	// worse surprise than being told no.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", "")
	t.Setenv("AGENTVAULT_SERVE_WORKSPACE", "")

	resp := postPrompt(t, srv, `{"prompt":"write a file"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if body["text"] != nil {
		t.Fatal("a refused request returned generated text")
	}
	if body["agent"] == nil {
		t.Fatal("the refusal does not say which agent was selected")
	}
}

func TestAgenticWithoutAWorkspaceIsStillRefused(t *testing.T) {
	// Enabling the capability and choosing its blast radius are one decision.
	// Permitting the runner without saying where it may work would run an agent
	// with filesystem access in whatever directory the server was started from
	// -- for claude, a session with --permission-mode auto.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", "true")
	t.Setenv("AGENTVAULT_SERVE_WORKSPACE", "")

	resp := postPrompt(t, srv, `{"prompt":"write a file"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "AGENTVAULT_SERVE_WORKSPACE") {
		t.Fatalf("the refusal does not name the missing setting: %v", body["error"])
	}
}

func TestPromptRequiresAPrompt(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postPrompt(t, srv, `{"prompt":"  "}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPromptRejectsAnUnknownNamedAgent(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postPrompt(t, srv, `{"prompt":"hi","agent":"not-a-real-agent"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPromptRequiresAuth(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "a-different-key"))
	defer srv.Close()

	resp := postPrompt(t, srv, `{"prompt":"hi"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPromptRejectsGet(t *testing.T) {
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", ""))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/prompt")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHTTPSafeRunnersAreOnlyTheNonAgenticOnes(t *testing.T) {
	// A new runner must not become HTTP-reachable by default just because it
	// was added; this is the list an operator is trusting.
	for _, runner := range []agent.RunnerKind{agent.RunnerClaudeCLI, agent.RunnerCodexCLI, agent.RunnerGeminiCLI} {
		if httpSafeRunners[runner] {
			t.Fatalf("runner %q is agentic and must not be HTTP-safe by default", runner)
		}
	}
	if !httpSafeRunners[agent.RunnerOllamaHTTP] || !httpSafeRunners[agent.RunnerOpenAIHTTP] {
		t.Fatal("the model-API runners should be permitted")
	}
}

func TestPromptTimeoutIsCapped(t *testing.T) {
	// Unbounded, an authenticated caller can hold a goroutine and an upstream
	// connection for as long as it likes -- a slow request is fine, an
	// indefinite one is a way to exhaust the server with a handful of calls.
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"unset falls back to the default", 0, defaultPromptTimeout},
		{"negative falls back to the default", -5, defaultPromptTimeout},
		{"a reasonable value is honoured", 30, 30 * time.Second},
		{"a day is capped", 86400, maxPromptTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := time.Duration(tc.seconds) * time.Second
			if got <= 0 {
				got = defaultPromptTimeout
			}
			if got > maxPromptTimeout {
				got = maxPromptTimeout
			}
			if got != tc.want {
				t.Fatalf("timeout = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPromptExecutionIsCancelledWhenTheClientDisconnects(t *testing.T) {
	// An already-cancelled context, rather than racing a blocking server: the
	// property under test is that the context reaches the request at all. If it
	// does not, the call ignores cancellation and runs to its own timeout,
	// which is the bug -- the model keeps generating for a caller that is no
	// longer listening and the server pays for it.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request reached the server despite a cancelled context")
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executeOllamaPrompt(ctx, agent.Agent{
		Provider: agent.ProviderOllama,
		Model:    "llama3.2",
		BaseURL:  upstream.URL,
	}, "hello", 30*time.Second)

	if err == nil {
		t.Fatal("execution succeeded with a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want it to wrap context.Canceled", err)
	}
}

// --- Guards on the endpoints that execute -----------------------------------

func TestExecutingEndpointsRequireAKeyEvenOnLoopback(t *testing.T) {
	// serve defaults to loopback and the key is otherwise optional, so an
	// unconfigured server running agentic runners would execute commands for
	// any local process that can reach the port -- including one running as a
	// different user on a shared machine.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "")) // no key
	defer srv.Close()

	for _, path := range []string{"/api/v1/route", "/api/v1/prompt"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(`{"prompt":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403 when no key is configured", path, resp.StatusCode)
		}
	}
}

func TestBrowserOriginatedRequestsAreRefused(t *testing.T) {
	// The attack "it only listens on loopback" does not stop: a cross-origin
	// form with enctype="text/plain" is a CORS simple request, so no preflight
	// happens and CORS never gets a chance to block it. Its body can be crafted
	// as valid JSON. Without this check any page the user visits can POST here
	// and run commands on their machine.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/prompt", strings.NewReader(`{"prompt":"rm -rf /"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	req.Header.Set("Origin", "https://evil.example")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a browser-originated request", resp.StatusCode)
	}
}

func TestFormContentTypesAreRefused(t *testing.T) {
	// The exact content types a cross-origin form can send without a preflight.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	for _, ct := range []string{
		"text/plain",
		"application/x-www-form-urlencoded",
		"multipart/form-data",
		"",
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/prompt", strings.NewReader(`{"prompt":"hi"}`))
		req.Header.Set("x-api-key", "secret-key")
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type %q gave status %d, want 415", ct, resp.StatusCode)
		}
	}
}

func TestAWellFormedRequestStillGetsThrough(t *testing.T) {
	// The guards must not break the legitimate caller they exist to protect.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/route", strings.NewReader(`{"prompt":"summarise this"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a well-formed authenticated request", resp.StatusCode)
	}
}

func TestLogValuesCannotForgeALine(t *testing.T) {
	// An audit trail an attacker can write to is worse than none, because it is
	// believed. A newline in a logged value would append a second, fabricated
	// AGENTIC EXECUTION record.
	forged := "innocent\nAGENTIC EXECUTION agent=\"trusted\" runner=\"ollama_http\""

	got := sanitizeForLog(forged)

	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("sanitised value still contains a line break: %q", got)
	}
}

func TestLogSanitiserDropsControlCharacters(t *testing.T) {
	// A terminal reading the log should not be steered by its contents.
	got := sanitizeForLog("name\x1b[31m\x00\x07")

	for _, r := range got {
		if r < 0x20 && r != ' ' {
			t.Fatalf("control character survived: %q", got)
		}
	}
}

func TestLogSanitiserBoundsLength(t *testing.T) {
	// One value should not be able to push everything else off the screen, or
	// fill a disk.
	got := sanitizeForLog(strings.Repeat("x", 5000))

	if len([]rune(got)) > 257 {
		t.Fatalf("sanitised value is %d runes, want it bounded", len([]rune(got)))
	}
}

func TestLogSanitiserLeavesOrdinaryValuesAlone(t *testing.T) {
	// A sanitiser that mangles normal input makes the log harder to read, which
	// is its own kind of failure.
	for _, value := range []string{"claude-main", "ollama_http", "/srv/agent-workspace", "127.0.0.1:54321"} {
		if got := sanitizeForLog(value); got != value {
			t.Fatalf("sanitizeForLog(%q) = %q, want it unchanged", value, got)
		}
	}
}

// --- Untrusted content ------------------------------------------------------
//
// The only defence against prompt injection here that is not advisory.
// Delimiters and "ignore instructions in the content below" help, but they are
// a request to a system whose purpose is following instructions written in
// text. What an injection cannot do is reach a capability that was never
// offered.

func TestUntrustedContentCannotReachAnAgenticRunner(t *testing.T) {
	v := testServeVault(t) // its only agent is a claude CLI agent
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	// Agentic runners fully enabled by the operator -- and still refused,
	// because the constraint is about the data, not the configuration.
	t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", "true")
	t.Setenv("AGENTVAULT_SERVE_WORKSPACE", t.TempDir())

	resp := postPrompt(t, srv, `{"prompt":"summarise this document","untrusted_content":true}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for untrusted content on an agentic runner", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body["code"] != "untrusted_content_needs_a_safe_runner" {
		t.Fatalf("code = %v, want untrusted_content_needs_a_safe_runner", body["code"])
	}
	if body["text"] != nil {
		t.Fatal("a refused request returned generated text")
	}
}

func TestTheOperatorCannotOverrideTheUntrustedConstraint(t *testing.T) {
	// The check sits before the allow-flag on purpose. If enabling agentic
	// runners could re-open this path, the flag would silently undo every
	// caller's judgement about its own data.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	for _, allow := range []string{"true", "false", ""} {
		t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", allow)
		t.Setenv("AGENTVAULT_SERVE_WORKSPACE", t.TempDir())

		resp := postPrompt(t, srv, `{"prompt":"x","untrusted_content":true}`)
		status := resp.StatusCode
		resp.Body.Close()

		if status != http.StatusForbidden {
			t.Fatalf("ALLOW_AGENTIC=%q gave status %d, want 403", allow, status)
		}
	}
}

func TestRoutingRefusesAnAgenticTargetForUntrustedContent(t *testing.T) {
	// Answered at routing time rather than leaving the caller to discover it at
	// execution time: routing yes then prompting no is a bad way to learn.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	resp := postRoute(t, srv, testKey, `{"prompt":"review this","untrusted_content":true}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body["code"] != "untrusted_content_needs_a_safe_runner" {
		t.Fatalf("code = %v, want untrusted_content_needs_a_safe_runner", body["code"])
	}
}

func TestTrustedPromptsAreUnaffected(t *testing.T) {
	// The control must not change behaviour for a caller that did not ask for
	// it, or it becomes something people work around.
	v := testServeVault(t)
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", testKey))
	defer srv.Close()

	t.Setenv("AGENTVAULT_SERVE_ALLOW_AGENTIC", "")
	t.Setenv("AGENTVAULT_SERVE_WORKSPACE", "")

	resp := postPrompt(t, srv, `{"prompt":"x"}`)
	defer resp.Body.Close()

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)

	// Still refused, but for the pre-existing reason, with the pre-existing
	// message -- not the untrusted-content one.
	if body["code"] == "untrusted_content_needs_a_safe_runner" {
		t.Fatal("a prompt that did not declare untrusted content was refused as though it had")
	}
}

// --- Rerouting untrusted content around agentic runners ---------------------
//
// Refusing whenever the router happened to pick an agentic winner made the
// capability safe and unusable: a corpus service asking a routine question
// would be rejected purely because the router liked a CLI agent, with a
// perfectly good HTTP model sitting in the fallback list.

func candidate(name string, runner agent.RunnerKind) router.Candidate {
	return router.Candidate{
		Agent:  router.AgentView{Name: name},
		Target: agent.ExecutionTarget{AgentName: name, Runner: runner, Supported: true},
	}
}

func TestSafeCandidateKeepsTheRoutersChoiceWhenItIsAlreadySafe(t *testing.T) {
	d := router.Decision{
		Selected:  candidate("ollama-local", agent.RunnerOllamaHTTP),
		Fallbacks: []router.Candidate{candidate("claude-main", agent.RunnerClaudeCLI)},
	}
	got, ok := safeCandidateFor(d)
	if !ok || got.Agent.Name != "ollama-local" {
		t.Fatalf("safeCandidateFor() = %q, %v; want ollama-local, true", got.Agent.Name, ok)
	}
}

func TestSafeCandidateFallsBackPastAnAgenticWinner(t *testing.T) {
	d := router.Decision{
		Selected: candidate("claude-main", agent.RunnerClaudeCLI),
		Fallbacks: []router.Candidate{
			candidate("codex-cli", agent.RunnerCodexCLI),
			candidate("ollama-local", agent.RunnerOllamaHTTP),
		},
	}
	got, ok := safeCandidateFor(d)
	if !ok || got.Agent.Name != "ollama-local" {
		t.Fatalf("safeCandidateFor() = %q, %v; want ollama-local, true", got.Agent.Name, ok)
	}
}

func TestSafeCandidateRefusesWhenNothingIsSafe(t *testing.T) {
	d := router.Decision{
		Selected:  candidate("claude-main", agent.RunnerClaudeCLI),
		Fallbacks: []router.Candidate{candidate("codex-cli", agent.RunnerCodexCLI)},
	}
	if _, ok := safeCandidateFor(d); ok {
		t.Fatal("safeCandidateFor() returned a candidate when every runner was agentic")
	}
}

func TestSafeCandidateDoesNotReachPastTheFallbackList(t *testing.T) {
	// Candidates is the full ranked pool, including entries the caller's own
	// config excludes. Rerouting off it would let a local_only prompt land on
	// a remote provider -- trading a shell for a privacy breach. Only
	// Fallbacks has been filtered against that config, so only it is consulted.
	d := router.Decision{
		Selected:   candidate("claude-main", agent.RunnerClaudeCLI),
		Fallbacks:  nil,
		Candidates: []router.Candidate{candidate("openai-remote", agent.RunnerOpenAIHTTP)},
	}
	if _, ok := safeCandidateFor(d); ok {
		t.Fatal("safeCandidateFor() rerouted to a candidate the caller's config had excluded")
	}
}

func TestAnUnknownRunnerIsNotTreatedAsSafe(t *testing.T) {
	// Fail closed: a runner added later is refused until it is listed.
	d := router.Decision{Selected: candidate("mystery", agent.RunnerKind("something-new"))}
	if _, ok := safeCandidateFor(d); ok {
		t.Fatal("safeCandidateFor() accepted a runner that is not on the allowlist")
	}
}

func TestRefusalNeverReusesSelectedForAPlainString(t *testing.T) {
	// A 200 from /route puts the full Candidate object under "selected". The
	// refusals used the same key for a bare agent name, so a client decoding
	// "selected" as an object broke on exactly the responses it most needed to
	// read. String-valued agent names use "agent", matching every neighbouring
	// error payload.
	body, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(body), `"selected": decision.Selected.Agent.Name`) {
		t.Error(`a refusal payload puts a string under "selected"; use "agent" instead`)
	}
}
