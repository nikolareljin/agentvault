package cmd

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/router"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start an HTTP API server for the vault",
	Long: `Start a lightweight HTTP server that exposes the vault contents
over a REST API. Intended for integration with other tools (e.g. ForgeMind).

The vault master password must be provided via the AGENTVAULT_PASSWORD
environment variable. An optional API key can be required for all requests
via the AGENTVAULT_SERVE_KEY environment variable.

Endpoints:
  GET /health                  Health check
  GET /api/v1/status           Server and vault status
  POST /api/v1/route           Choose a target for a prompt, without running it
  POST /api/v1/prompt          Route, execute, and return the text with usage

SECURITY

/api/v1/route and /api/v1/prompt execute work on this machine, so they carry
guards the read-only endpoints do not:

  - AGENTVAULT_SERVE_KEY is mandatory for them, including on loopback.
  - Requests carrying an Origin header are refused: they came from a browser,
    and a cross-origin form post is a CORS simple request that no preflight
    would have stopped.
  - Content-Type must be application/json, which a cross-origin form cannot
    set.

Agentic CLI runners (claude, codex, gemini) run sessions with filesystem access
and are refused by /api/v1/prompt unless BOTH AGENTVAULT_SERVE_ALLOW_AGENTIC=true
and AGENTVAULT_SERVE_WORKSPACE=<dir> are set.

PROMPT INJECTION

A caller sending a prompt that contains text it did not author -- retrieved
documents, a scraped page, an email body -- should set "untrusted_content":
true. Routing then avoids agentic runners: if the best target runs an agentic
session, the highest-ranked safe fallback is used instead, and the response
names what it was rerouted from. If no safe target exists the request is
refused. Either way an agentic runner cannot be reached, before and regardless
of AGENTVAULT_SERVE_ALLOW_AGENTIC.

Rerouting needs allow_fallbacks, since the fallback list is what has been
filtered against the caller's own constraints -- rerouting off the full
candidate pool could send a local_only prompt to a remote provider, which
trades a shell for a privacy breach.

This is the only defence here that is not advisory. Wrapping content in
delimiters and instructing a model to ignore instructions inside them is worth
doing, and callers should do it, but it is a request to a system whose entire
purpose is following instructions written in text. An injection that gets
through still cannot use a capability that was never offered.

Erring towards true costs a routing option. Erring towards false costs a shell.

AGENTVAULT_SERVE_WORKSPACE is NOT a sandbox. It sets the working directory; an
auto-approved agent can still write absolute paths, read ~/.ssh and run any
command as this user. Enabling agentic runners grants shell access to whoever
holds the API key. Confinement needs a container or a separate account, which
this process does not provide.
  GET /api/v1/agents           List agents (API keys never exposed)
  GET /api/v1/agents/{name}    Get agent by name

Example:
  AGENTVAULT_PASSWORD=mysecret agentvault serve --host 127.0.0.1 --port 9000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")

		vaultPath := resolveVaultPath()
		v := vault.New(vaultPath)
		if !v.Exists() {
			return fmt.Errorf("vault not found at %s (run 'agentvault init' first)", vaultPath)
		}

		password := os.Getenv("AGENTVAULT_PASSWORD")
		if password == "" {
			return fmt.Errorf("AGENTVAULT_PASSWORD environment variable is required to start the server")
		}

		if err := v.Unlock(password); err != nil {
			return fmt.Errorf("unlocking vault: %w", err)
		}

		apiKey := os.Getenv("AGENTVAULT_SERVE_KEY")
		if !isLoopbackHost(host) && apiKey == "" {
			return fmt.Errorf("AGENTVAULT_SERVE_KEY is required when serving on non-loopback host %q", host)
		}
		mux := newServeMux(v, vaultPath, apiKey)

		addr := fmt.Sprintf("%s:%d", host, port)
		log.Printf("AgentVault API listening on %s (vault: %s, auth: %v)", addr, vaultPath, apiKey != "")
		server := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// Longer than maxPromptTimeout, or the server truncates a response
			// the handler is still allowed to be producing -- which would make
			// the prompt timeout cap meaningless and look like a provider
			// failure rather than a server one.
			WriteTimeout: maxPromptTimeout + 30*time.Second,
			IdleTimeout:  60 * time.Second,
		}
		return server.ListenAndServe()
	},
}

func newServeMux(v *vault.Vault, vaultPath, apiKey string) *http.ServeMux {
	mux := http.NewServeMux()

	writeJSON := func(w http.ResponseWriter, code int, payload any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
	}

	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if apiKey != "" {
				got := r.Header.Get("x-api-key")
				if got == "" {
					got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				}
				if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
					return
				}
			}
			next(w, r)
		}
	}

	// GET /health
	mux.HandleFunc("/health", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	}))

	// GET /api/v1/status
	mux.HandleFunc("/api/v1/status", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		agents := v.List()
		shared := v.SharedConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"agent_count":   len(agents),
			"rule_count":    len(shared.Rules),
			"role_count":    len(shared.Roles),
			"vault_path":    vaultPath,
			"auth_required": apiKey != "",
		})
	}))

	// GET /api/v1/agents
	mux.HandleFunc("/api/v1/agents", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		agents := v.List()
		type agentSummary struct {
			Name     string   `json:"name"`
			Provider string   `json:"provider"`
			Model    string   `json:"model"`
			BaseURL  string   `json:"base_url,omitempty"`
			Tags     []string `json:"tags,omitempty"`
			Role     string   `json:"role,omitempty"`
		}
		out := make([]agentSummary, 0, len(agents))
		for _, a := range agents {
			out = append(out, agentSummary{
				Name:     a.Name,
				Provider: string(a.Provider),
				Model:    a.Model,
				BaseURL:  a.BaseURL,
				Tags:     a.Tags,
				Role:     a.Role,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}))

	// GET /api/v1/agents/{name}
	mux.HandleFunc("/api/v1/agents/", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent name required"})
			return
		}
		a, ok := v.Get(name)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		shared := v.SharedConfig()
		type agentDetail struct {
			Name         string   `json:"name"`
			Provider     string   `json:"provider"`
			Model        string   `json:"model"`
			BaseURL      string   `json:"base_url,omitempty"`
			SystemPrompt string   `json:"system_prompt,omitempty"`
			TaskDesc     string   `json:"task_description,omitempty"`
			Tags         []string `json:"tags,omitempty"`
			Role         string   `json:"role,omitempty"`
		}
		writeJSON(w, http.StatusOK, agentDetail{
			Name:         a.Name,
			Provider:     string(a.Provider),
			Model:        a.Model,
			BaseURL:      a.BaseURL,
			SystemPrompt: a.BuildEffectivePrompt(shared),
			TaskDesc:     a.TaskDesc,
			Tags:         a.Tags,
			Role:         a.Role,
		})
	}))

	// POST /api/v1/route
	//
	// Decides who should handle a prompt without executing it. Useful on its
	// own: a caller can plan, estimate cost, or show the user which model is
	// about to see their data before anything is sent to it.
	mux.HandleFunc("/api/v1/route", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		if !requireStrongAuth(w, r, apiKey, writeJSON) {
			return
		}

		var req routeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if strings.TrimSpace(req.Prompt) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
			return
		}

		// The request's context, so a client that disconnects stops the
		// subprocess or HTTP call being made on its behalf rather than leaving
		// it to run to completion unwatched.
		decision, err := router.RouteContext(r.Context(), router.Request{
			Prompt:            req.Prompt,
			Agents:            v.List(),
			Shared:            v.SharedConfig(),
			Config:            req.toRouterConfig(),
			ModelCapabilities: v.ListCapabilities(),
		})
		if err != nil {
			// Not every routing failure is the caller's fault, and treating
			// them alike sends whoever is debugging in the wrong direction. An
			// unsatisfiable request is their answer; a langgraph script that
			// cannot be found, or an llm-router that will not respond, is an
			// operational problem on this side and has to look like one.
			switch {
			case errors.Is(err, router.ErrEmptyPrompt),
				errors.Is(err, router.ErrNoCandidates),
				errors.Is(err, router.ErrPolicyUnsatisfiable):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// The caller went away. 499 is nginx's, not IANA's, but there
				// is no standard code for it and a 500 would page someone for
				// a client that closed its own connection.
				writeJSON(w, 499, map[string]string{"error": "client closed the request"})
			default:
				log.Printf("routing failed: %s", sanitizeForLog(err.Error()))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "routing failed"})
			}
			return
		}

		reroutedFrom := ""
		if req.UntrustedContent {
			safe, ok := safeCandidateFor(decision)
			if !ok {
				// Answered at routing time rather than leaving the caller to
				// discover it at execution time: a caller that routes, then
				// prompts, should not be told yes and then no.
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error": fmt.Sprintf(
						"the best target for this prompt is runner %q, which runs an agentic "+
							"session and cannot be used for untrusted content, and no "+
							"fallback runner can be. Set allow_fallbacks to let routing "+
							"choose a safe target.",
						decision.Selected.Target.Runner),
					"code":     "untrusted_content_needs_a_safe_runner",
					"selected": decision.Selected.Agent.Name,
				})
				return
			}
			if !strings.EqualFold(safe.Agent.Name, decision.Selected.Agent.Name) {
				reroutedFrom = decision.Selected.Agent.Name
				decision.Selected = safe
			}
		}

		payload := map[string]any{
			"mode":              decision.Mode,
			"intent":            decision.Intent,
			"selected":          decision.Selected,
			"fallbacks":         decision.Fallbacks,
			"untrusted_content": req.UntrustedContent,
			// Every candidate considered, not only the winner. A routing
			// decision nobody can inspect is one nobody can debug when it
			// picks something surprising.
			"candidates": decision.Candidates,
		}
		if reroutedFrom != "" {
			// Named, not silent. The caller asked which agent would handle
			// this; being handed a different one than the router's first
			// choice is exactly the kind of thing that has to appear in the
			// answer rather than in a log nobody reads.
			payload["rerouted_from"] = reroutedFrom
			payload["rerouted_reason"] = "untrusted_content"
		}
		writeJSON(w, http.StatusOK, payload)
	}))

	// POST /api/v1/prompt
	//
	// Routes and executes, returning the text along with token usage and cost.
	// The usage is the point as much as the answer: a caller that has to
	// reconstruct what a call cost will get it wrong, and every service that
	// shipped its own provider layer got it wrong differently.
	mux.HandleFunc("/api/v1/prompt", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		if !requireStrongAuth(w, r, apiKey, writeJSON) {
			return
		}

		var req promptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if strings.TrimSpace(req.Prompt) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
			return
		}

		agents := v.List()
		shared := v.SharedConfig()

		var selected agent.Agent
		var decision router.Decision
		var routed bool

		if name := strings.TrimSpace(req.Agent); name != "" {
			found := false
			for _, a := range agents {
				if strings.EqualFold(a.Name, name) {
					selected, found = a, true
					break
				}
			}
			if !found {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("no agent named %q", name)})
				return
			}
		} else {
			var err error
			decision, err = router.RouteContext(r.Context(), router.Request{
				Prompt:            req.Prompt,
				Agents:            agents,
				Shared:            shared,
				Config:            req.toRouterConfig(),
				ModelCapabilities: v.ListCapabilities(),
			})
			if err != nil {
				switch {
				case errors.Is(err, router.ErrEmptyPrompt),
					errors.Is(err, router.ErrNoCandidates),
					errors.Is(err, router.ErrPolicyUnsatisfiable):
					writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
					writeJSON(w, 499, map[string]string{"error": "client closed the request"})
				default:
					log.Printf("routing failed: %s", sanitizeForLog(err.Error()))
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "routing failed"})
				}
				return
			}
			// Untrusted content reroutes around agentic runners rather than
			// failing on them. The caller described its data, not its
			// preferred runner, so picking a different target is the router
			// doing its job -- unlike the named-agent path below, where a
			// silent substitution really would be a surprise.
			if req.UntrustedContent {
				safe, ok := safeCandidateFor(decision)
				if !ok {
					writeJSON(w, http.StatusForbidden, map[string]any{
						"error": fmt.Sprintf(
							"runner %q runs an agentic session and cannot be used for a prompt "+
								"marked untrusted_content, and no fallback runner can be. Set "+
								"allow_fallbacks to let routing choose a safe target.",
							decision.Selected.Target.Runner),
						"code":     "untrusted_content_needs_a_safe_runner",
						"selected": decision.Selected.Agent.Name,
					})
					return
				}
				decision.Selected = safe
			}

			// The decision carries a redacted AgentView by design -- it never
			// includes credentials, which is what keeps the routing response
			// safe to return. So the executable agent is looked up by name.
			found := false
			for _, a := range agents {
				if strings.EqualFold(a.Name, decision.Selected.Agent.Name) {
					selected, found = a, true
					break
				}
			}
			if !found {
				log.Printf("router selected unknown agent %q", sanitizeForLog(decision.Selected.Agent.Name))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "routing selected an unknown agent"})
				return
			}
			routed = true
		}

		target := agent.ResolveExecutionTarget(selected)
		if !target.Supported {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": fmt.Sprintf("runner %q is not supported for execution", target.Runner),
			})
			return
		}

		// Checked before the operator's allow-flag, so enabling agentic runners
		// cannot re-open this path. A caller declaring its content untrusted is
		// making a statement about the data, and no server configuration should
		// be able to override it.
		if req.UntrustedContent && !httpSafeRunners[target.Runner] {
			log.Printf(
				"refused untrusted-content prompt for agentic runner agent=%q runner=%q remote=%q",
				sanitizeForLog(selected.Name),
				sanitizeForLog(string(target.Runner)),
				sanitizeForLog(r.RemoteAddr),
			)
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": fmt.Sprintf(
					"runner %q runs an agentic session and cannot be used for a prompt "+
						"marked untrusted_content", target.Runner),
				"code":   "untrusted_content_needs_a_safe_runner",
				"agent":  selected.Name,
				"runner": string(target.Runner),
			})
			return
		}

		executionDir := ""
		if !httpSafeRunners[target.Runner] {
			// Refused rather than quietly downgraded to another agent: a caller
			// asking for a coding agent and silently getting a chat model back
			// would be a worse surprise than being told no.
			if !agenticRunnersAllowed() {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error": fmt.Sprintf(
						"runner %q runs an agentic CLI session with filesystem access and is not "+
							"exposed over HTTP by default; set AGENTVAULT_SERVE_ALLOW_AGENTIC=true "+
							"to permit it", target.Runner),
					"agent":  selected.Name,
					"runner": string(target.Runner),
				})
				return
			}

			executionDir = agenticWorkspace()
			if executionDir == "" {
				// Permitting the runner without saying where it may work would
				// run an agent with filesystem access in whatever directory the
				// server was started from. Enabling the capability and choosing
				// its blast radius are one decision, so both are required.
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error": "AGENTVAULT_SERVE_ALLOW_AGENTIC is set but " +
						"AGENTVAULT_SERVE_WORKSPACE is not; a CLI runner would execute in the " +
						"server's own working directory",
					"agent":  selected.Name,
					"runner": string(target.Runner),
				})
				return
			}
		}

		// Bounded. An unbounded timeout on an authenticated endpoint lets one
		// caller hold a goroutine and an upstream connection for as long as it
		// likes -- a slow request is fine, an indefinite one is a way to
		// exhaust the server with a handful of calls.
		timeout := time.Duration(req.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = defaultPromptTimeout
		}
		if timeout > maxPromptTimeout {
			timeout = maxPromptTimeout
		}

		// The request's context, so a client that disconnects ends the upstream
		// call rather than leaving it running unwatched.
		// Logged before execution, not after. If the process is killed mid-run
		// -- or the agent kills it -- an after-the-fact log records nothing,
		// and "what did this server run?" becomes unanswerable. The prompt is
		// deliberately not logged: it can contain the contents of private
		// documents, and this line exists to answer what ran, not what was
		// asked.
		if !httpSafeRunners[target.Runner] {
			log.Printf(
				"AGENTIC EXECUTION agent=%q runner=%q workspace=%q prompt_bytes=%d remote=%q",
				sanitizeForLog(selected.Name),
				sanitizeForLog(string(target.Runner)),
				sanitizeForLog(executionDir),
				len(req.Prompt),
				sanitizeForLog(r.RemoteAddr),
			)
		}

		result, err := executePromptTarget(r.Context(), target, selected, req.Prompt, timeout, executionDir, false, io.Discard, io.Discard)
		if err != nil {
			// Detail to the log, not to the caller. A provider error can carry
			// endpoint URLs, filesystem paths and occasionally a fragment of a
			// credential, and none of that is stable enough for a client to
			// depend on either. The code is what a caller branches on.
			log.Printf(
				"prompt execution failed for agent %q (runner %q): %s",
				sanitizeForLog(selected.Name),
				sanitizeForLog(string(target.Runner)),
				sanitizeForLog(err.Error()),
			)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				writeJSON(w, 499, map[string]string{"error": "client closed the request", "code": "cancelled"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "the provider could not be reached or failed to respond",
				"code":  "execution_failed",
				"agent": selected.Name,
			})
			return
		}

		cost := agent.ComputeCostUSD(&result.Usage, selected.Provider, selected.Model, shared.Pricing)

		payload := map[string]any{
			"text":     result.Response,
			"agent":    selected.Name,
			"provider": string(selected.Provider),
			"model":    selected.Model,
			"runner":   string(target.Runner),
			"usage":    result.Usage,
			"cost_usd": cost,
			"routed":   routed,
			"local":    target.Local,
		}
		if routed {
			payload["mode"] = decision.Mode
			payload["reasons"] = decision.Selected.Reasons
		}
		writeJSON(w, http.StatusOK, payload)
	}))

	return mux
}

// routeRequest is the POST /api/v1/route body.
//
// Deliberately the routing hints the CLI already accepts, under the same
// names. A second vocabulary for the same concepts would mean the HTTP and CLI
// paths could disagree about what "prefer local" means, and only one of them
// would be documented.
type routeRequest struct {
	Prompt string `json:"prompt"`
	// UntrustedContent marks a prompt that carries text this caller did not
	// write -- retrieved documents, a scraped page, an email body, anything a
	// third party influenced.
	//
	// It is the only defence here that is not advisory. Wrapping content in
	// delimiters and telling a model to ignore instructions inside them helps,
	// but it is a request to a system whose whole purpose is following
	// instructions written in text, and a sufficiently determined injection
	// gets through. What an injection cannot do is reach a capability that was
	// never offered: when this is set, routing avoids agentic runners
	// entirely, regardless of AGENTVAULT_SERVE_ALLOW_AGENTIC, so the worst
	// outcome is a wrong answer rather than a command run on this machine.
	//
	// It reroutes rather than refuses. The caller is describing its data, not
	// asking for a particular runner, so choosing a different target is the
	// router doing its job. Refusal is reserved for when no safe target
	// exists, which needs allow_fallbacks to be set.
	//
	// Callers should set it whenever any part of the prompt came from content
	// they did not author. Erring towards true costs a routing option; erring
	// towards false costs a shell.
	UntrustedContent bool   `json:"untrusted_content,omitempty"`
	Mode             string `json:"mode,omitempty"`
	PreferLocal      bool   `json:"prefer_local,omitempty"`
	LocalOnly        bool   `json:"local_only,omitempty"`
	PreferFast       bool   `json:"prefer_fast,omitempty"`
	PreferLowCost    bool   `json:"prefer_low_cost,omitempty"`
	AllowFallbacks   bool   `json:"allow_fallbacks,omitempty"`
	Importance       string `json:"importance,omitempty"` // low|medium|high|critical
	Deadline         string `json:"deadline,omitempty"`   // immediate|normal|background
}

func (rr routeRequest) toRouterConfig() agent.RouterConfig {
	cfg := agent.RouterConfig{}
	if rr.Mode != "" {
		cfg.Mode = rr.Mode
	}
	if rr.Importance != "" {
		cfg.Importance = rr.Importance
	}
	if rr.Deadline != "" {
		cfg.Deadline = rr.Deadline
	}
	cfg.PreferFast = rr.PreferFast
	cfg.PreferLowCost = rr.PreferLowCost
	cfg.AllowFallbacks = rr.AllowFallbacks
	if rr.LocalOnly {
		// local_only is the stronger claim and implies the weaker one, so a
		// caller cannot end up with local_only set and prefer_local unset and
		// wonder which won.
		cfg.LocalOnly = true
		cfg.PreferLocal = true
	} else if rr.PreferLocal {
		cfg.PreferLocal = true
	}
	return cfg
}

// promptRequest is the POST /api/v1/prompt body.
type promptRequest struct {
	routeRequest
	// Agent names the target directly and skips routing. Useful when the
	// caller has already routed, or is reproducing a previous decision.
	Agent      string `json:"agent,omitempty"`
	TimeoutSec int    `json:"timeout_seconds,omitempty"`
}

// Runners this endpoint will execute over HTTP without being asked twice.
//
// These call a model API and return text. The CLI runners do something
// categorically different: claude runs with --permission-mode auto, codex with
// workspace-write, gemini with --approval-mode auto_edit. Those are agentic
// sessions with filesystem access, so exposing them on a socket turns this into
// remote code execution -- for anyone holding the API key, and for anyone who
// obtains it later.
//
// That is a decision an operator should make deliberately, not one they inherit
// by starting a server.
var httpSafeRunners = map[agent.RunnerKind]bool{
	agent.RunnerOllamaHTTP: true,
	agent.RunnerOpenAIHTTP: true,
}

// safeCandidateFor returns the highest-ranked candidate that can be used for a
// prompt carrying untrusted content, preferring the router's own choice.
//
// Refusing outright when the winner happens to be agentic was the wrong shape.
// The caller that sets untrusted_content is describing its data, and a corpus
// service asking a routine question would have had its request rejected purely
// because the router liked a CLI agent for it -- with a perfectly good HTTP
// model sitting in the fallback list. The capability was safe and unusable.
//
// Fallbacks rather than Candidates on purpose. Candidates is the full ranked
// pool including entries the caller's own config excludes, so a local_only
// request could be rerouted to a remote provider -- trading a shell for a
// privacy breach. Fallbacks has already been filtered against that config.
// The cost is that allow_fallbacks must be set for rerouting to happen, which
// the refusal message says.
func safeCandidateFor(d router.Decision) (router.Candidate, bool) {
	if httpSafeRunners[d.Selected.Target.Runner] {
		return d.Selected, true
	}
	for _, c := range d.Fallbacks {
		if httpSafeRunners[c.Target.Runner] {
			return c, true
		}
	}
	return router.Candidate{}, false
}

const (
	defaultPromptTimeout = 120 * time.Second
	// A ceiling rather than a suggestion. A caller asking for longer is
	// silently given this, because failing the request would be worse for a
	// legitimately slow model and the cap is the point either way.
	maxPromptTimeout = 10 * time.Minute
)

func agenticRunnersAllowed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTVAULT_SERVE_ALLOW_AGENTIC")), "true")
}

// sanitizeForLog strips anything that could forge a log line.
//
// This matters most for the audit line, whose whole value is being trustworthy:
// a value carrying a newline could append a second, fabricated
// "AGENTIC EXECUTION" record, and an audit trail an attacker can write to is
// worse than none, because it is believed. Control characters go too -- a
// terminal reading the log should not be steered by its contents.
func sanitizeForLog(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			// Dropped rather than replaced: a control character in an agent
			// name or a path is not information anyone needs in a log.
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 256 {
		return out[:256] + "…"
	}
	return out
}

// requireStrongAuth reports whether this request may reach an endpoint that
// executes anything.
//
// Two checks the read-only endpoints do not need.
//
// An API key is mandatory here even on loopback. `serve` defaults to loopback
// and the key is otherwise optional, which means an unconfigured server running
// agentic runners would execute commands for any local process that can reach
// the port -- including one running as a different user on a shared machine.
//
// And a request carrying an Origin header is refused outright, because it came
// from a browser. A cross-origin form with enctype="text/plain" is a CORS
// simple request: no preflight, so CORS never gets a chance to block it, and
// its body can be crafted as valid JSON. Without this check any page you visit
// can POST to localhost and run commands on your machine. "It only listens on
// loopback" is not a defence against the browser you are reading this in.
func requireStrongAuth(w http.ResponseWriter, r *http.Request, apiKey string, writeJSON func(http.ResponseWriter, int, any)) bool {
	if apiKey == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this endpoint requires AGENTVAULT_SERVE_KEY to be set, including on loopback",
			"code":  "auth_not_configured",
		})
		return false
	}

	if origin := r.Header.Get("Origin"); origin != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "browser-originated requests are not accepted by this endpoint",
			"code":  "origin_rejected",
		})
		return false
	}

	// Belt and braces on the same attack: a simple request cannot set this
	// header, so requiring it excludes form-based cross-origin posts even if an
	// Origin header is somehow absent.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "Content-Type must be application/json",
			"code":  "unsupported_media_type",
		})
		return false
	}

	return true
}

// agenticWorkspace is the directory a CLI runner is allowed to work in.
//
// Required, not optional, and deliberately not defaulted. An empty executionDir
// leaves cmd.Dir empty, which means the agent runs in whatever directory the
// server process happens to have been started from -- for claude that is a
// session with --permission-mode auto, so "wherever systemd put us" is not an
// acceptable answer. An operator enabling agentic runners has to say where.
//
// It is NOT a sandbox, and must not be described as one. cmd.Dir sets the
// working directory; an auto-approved agent can still write absolute paths,
// read ~/.ssh and run arbitrary commands as this user. Actual confinement needs
// a container, a separate account, or seccomp -- none of which this process
// does. Treat enabling agentic runners as granting shell access to whoever
// holds the API key.
func agenticWorkspace() string {
	return strings.TrimSpace(os.Getenv("AGENTVAULT_SERVE_WORKSPACE"))
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	h = strings.Trim(h, "[]")
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().String("host", "127.0.0.1", "host interface to listen on")
	serveCmd.Flags().Int("port", 9000, "port to listen on")
}
