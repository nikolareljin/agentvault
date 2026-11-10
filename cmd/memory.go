package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	"github.com/nikolareljin/agentvault/internal/memory"
	"github.com/spf13/cobra"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Local work memory: the memory.api@1 server over one SQLite file",
	Long: `Work memory is the inbox an agent writes to during a session: decisions,
ideas, prompts, notes and task states, one row each, plus the transcripts
an importer stores next to them. One process owns the database file;
every client goes through the loopback HTTP API and never opens the file.

Subcommands:
  serve    Start the API server on 127.0.0.1`,
}

var memoryServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve memory.api@1 on 127.0.0.1",
	Long: `Serve the memory.api@1 routes on 127.0.0.1 and nothing else:

  GET  /healthz              static {"status":"ok"}, the only route without a credential
  GET  /entries[?state=S]    {"entries": [memory.entry@1, ...]}
  GET  /search?q=TEXT        {"query": ..., "hits": [...]} over entries and transcripts
  POST /entries              one memory.entry@1 in, 201 and the stored row out

The server owns id, created_at, state (inbox), exported_pr (null) and
provenance (api) of a posted row and overwrites whatever the client sent
for them.

Port: --port, else AGENTVAULT_MEMORY_PORT, else 9030. Database:
--db, else AGENTVAULT_MEMORY_DB, else $XDG_DATA_HOME/agentvault/memory.db
(~/.local/share/agentvault/memory.db). AGENTVAULT_MEMORY_HOST set to
anything but 127.0.0.1 is refused before bind; the database holds
transcripts.

SECURITY

  - Host must be 127.0.0.1:<port> or localhost:<port>, else 400 (a DNS
    rebinding page keeps its own name as Host).
  - An Origin header that is not this server's origin is 403, and no
    Access-Control-* header is ever sent, so no page from another origin
    can read a response.
  - POST needs Content-Type: application/json, else 415; a cross-site
    HTML form cannot set it. Bodies over 1 MiB are 413.
  - Every route but GET /healthz needs Authorization: Bearer <token>.
    The token is generated on first start into memory.token next to the
    database, mode 0600, compared in constant time. A token in the query
    string or a cookie is not a credential.
  - Every response carries a Content-Security-Policy that allows nothing
    and X-Content-Type-Options: nosniff. Non-2xx bodies are
    application/problem+json with a stable "code".

--describe prints the memory.api@1 descriptor for the configured port and
exits.

Example:
  agentvault memory serve --port 9030
  curl -H "Authorization: Bearer $(cat ~/.local/share/agentvault/memory.token)" \
       http://127.0.0.1:9030/entries?state=inbox`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if host, set := os.LookupEnv("AGENTVAULT_MEMORY_HOST"); set && host != memory.BindAddress {
			return fmt.Errorf("AGENTVAULT_MEMORY_HOST=%q: memory.api@1 binds %s only; unset it", host, memory.BindAddress)
		}
		port, err := memoryPort(cmd)
		if err != nil {
			return err
		}
		if describe, _ := cmd.Flags().GetBool("describe"); describe {
			shown := port
			if shown == 0 {
				shown = memory.DefaultPort
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(memory.Describe(shown))
		}
		dbPath, _ := cmd.Flags().GetString("db")
		if dbPath == "" {
			dbPath = os.Getenv("AGENTVAULT_MEMORY_DB")
		}
		if dbPath == "" {
			if dbPath, err = memory.DefaultDBPath(); err != nil {
				return err
			}
		}
		store, err := memory.Open(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		token, err := memory.LoadOrCreateToken(filepath.Dir(store.Path()))
		if err != nil {
			return err
		}
		ln, err := memory.Listen(port)
		if err != nil {
			return err
		}
		// What the kernel gave, not what the environment said.
		bound := ln.Addr().(*net.TCPAddr)
		srv := memory.NewServer(store, token, bound.Port, cmd.ErrOrStderr())
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "agentvault memory: serving http://%s/ (memory.api@1, loopback only)\n", bound)
		fmt.Fprintf(out, "agentvault memory: database %s\n", store.Path())
		fmt.Fprintf(out, "agentvault memory: API clients: Authorization: Bearer <%s>\n",
			filepath.Join(filepath.Dir(store.Path()), memory.TokenFileName))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if memoryServeReady != nil {
			memoryServeReady(bound)
		}
		return srv.Serve(ctx, ln)
	},
}

// memoryServeReady, when set by a test, is told the bound address once the
// listener is up and before the server blocks.
var memoryServeReady func(*net.TCPAddr)

// memoryPort is --port, else AGENTVAULT_MEMORY_PORT, else DefaultPort. 0
// means an ephemeral port; anything else outside 1-65535 is refused.
func memoryPort(cmd *cobra.Command) (int, error) {
	var port int
	var origin string
	if cmd.Flags().Changed("port") {
		port, _ = cmd.Flags().GetInt("port")
		origin = "--port"
	} else if raw := os.Getenv("AGENTVAULT_MEMORY_PORT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("AGENTVAULT_MEMORY_PORT=%q is not a port number", raw)
		}
		port, origin = n, "AGENTVAULT_MEMORY_PORT"
	} else {
		return memory.DefaultPort, nil
	}
	if port != 0 && (port < 1 || port > 65535) {
		return 0, fmt.Errorf("%s=%d is outside 1-65535", origin, port)
	}
	return port, nil
}

func init() {
	memoryServeCmd.Flags().Int("port", memory.DefaultPort, "port on 127.0.0.1 (0 for an ephemeral one)")
	memoryServeCmd.Flags().String("db", "", "database file (default $XDG_DATA_HOME/agentvault/memory.db)")
	memoryServeCmd.Flags().Bool("describe", false, "print the memory.api@1 descriptor and exit")
	memoryCmd.AddCommand(memoryServeCmd)
	rootCmd.AddCommand(memoryCmd)
}
