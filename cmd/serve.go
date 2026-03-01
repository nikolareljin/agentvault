package cmd

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

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
				SystemPrompt: a.EffectiveSystemPrompt(shared),
				TaskDesc:     a.TaskDesc,
				Tags:         a.Tags,
				Role:         a.Role,
			})
		}))

		addr := fmt.Sprintf("%s:%d", host, port)
		log.Printf("AgentVault API listening on %s (vault: %s, auth: %v)", addr, vaultPath, apiKey != "")
		server := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		return server.ListenAndServe()
	},
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
