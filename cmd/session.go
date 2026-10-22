package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:     "session",
	Aliases: []string{"sess", "workspace"},
	Short:   "Manage multi-agent work sessions",
	Long: `Create and manage sessions where multiple agents work together.

Sessions allow you to:
  - Define a group of agents that work on the same project
  - Start all agents in parallel with a single command
  - Export/import sessions to continue work on different machines
  - Assign roles to agents within a session

Examples:
  agentvault session create my-project     # Create new session
  agentvault session start my-project      # Start all agents
  agentvault session list                  # List all sessions
  agentvault session export my-session.json # Export for another machine`,
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		sessions := v.Sessions()

		if len(sessions.Sessions) == 0 {
			fmt.Println("No sessions configured. Use 'agentvault session create' to get started.")
			return nil
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(sessions.Sessions, "", "  ")
			fmt.Println(string(escapeC1JSON(data)))
			return nil
		}

		fmt.Println("Sessions:")
		fmt.Println(strings.Repeat("─", 70))
		for _, s := range sessions.Sessions {
			active := ""
			if s.ID == sessions.ActiveSession {
				active = " [ACTIVE]"
			}
			fmt.Printf("\n  %s (%s)%s\n", printable(s.Name), printable(s.ID), active)
			fmt.Printf("    Status:  %s\n", printable(string(s.Status)))
			fmt.Printf("    Project: %s\n", printable(s.ProjectDir))
			fmt.Printf("    Agents:  %d\n", len(s.Agents))
			if s.ActiveRole != "" {
				fmt.Printf("    Role:    %s\n", printable(s.ActiveRole))
			}
		}
		fmt.Println()
		return nil
	},
}

var sessionCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new session",
	Long: `Create a new multi-agent session.

By default, all detected/configured agents are included.
Use --agents to specify which agents to include.

Examples:
  agentvault session create my-project
  agentvault session create my-project --dir /path/to/project
  agentvault session create my-project --agents claude,codex
  agentvault session create my-project --role lead-engineer`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		dir, _ := cmd.Flags().GetString("dir")
		agentsStr, _ := cmd.Flags().GetString("agents")
		role, _ := cmd.Flags().GetString("role")

		// Check if session already exists
		if _, exists := v.GetSessionByName(name); exists {
			return fmt.Errorf("session %q already exists", name)
		}

		// Default to current directory
		if dir == "" {
			dir, _ = os.Getwd()
		}
		dir, _ = filepath.Abs(dir)

		// Get agents list
		var agentNames []string
		if agentsStr != "" {
			for _, a := range strings.Split(agentsStr, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					agentNames = append(agentNames, a)
				}
			}
		} else {
			// Default: use all configured agents
			for _, a := range v.List() {
				agentNames = append(agentNames, a.Name)
			}
		}

		if len(agentNames) == 0 {
			return fmt.Errorf("no agents specified and none configured in vault")
		}

		// Verify agents exist
		for _, name := range agentNames {
			if _, ok := v.Get(name); !ok {
				return fmt.Errorf("agent %q not found in vault", name)
			}
		}

		session := agent.NewSession(name, dir, agentNames)
		session.ActiveRole = role

		if err := v.AddSession(session); err != nil {
			return err
		}

		fmt.Printf("Session %q created with %d agent(s).\n", name, len(agentNames))
		fmt.Printf("  ID:      %s\n", printable(session.ID))
		fmt.Printf("  Project: %s\n", printable(session.ProjectDir))
		fmt.Printf("  Agents:  %s\n", strings.Join(agentNames, ", "))
		if role != "" {
			fmt.Printf("  Role:    %s\n", printable(role))
		}
		fmt.Println("\nUse 'agentvault session start " + name + "' to run all agents.")
		return nil
	},
}

var sessionShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show session details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		session, ok := v.GetSessionByName(args[0])
		if !ok {
			return fmt.Errorf("session %q not found", args[0])
		}

		fmt.Printf("Session: %s\n", printable(session.Name))
		fmt.Printf("ID: %s\n", printable(session.ID))
		fmt.Printf("Status: %s\n", printable(string(session.Status)))
		fmt.Printf("Project: %s\n", printable(session.ProjectDir))
		if session.ActiveRole != "" {
			fmt.Printf("Default Role: %s\n", printable(session.ActiveRole))
		}
		fmt.Printf("Created: %s\n", session.CreatedAt.Format("2006-01-02 15:04"))
		fmt.Printf("Updated: %s\n", session.UpdatedAt.Format("2006-01-02 15:04"))

		fmt.Println("\nAgents:")
		for _, a := range session.Agents {
			status := "○"
			if a.Enabled {
				status = "●"
			}
			role := ""
			if a.Role != "" {
				role = fmt.Sprintf(" [%s]", printable(a.Role))
			}
			task := ""
			if a.Task != "" {
				task = fmt.Sprintf(" - %s", printable(a.Task))
			}
			fmt.Printf("  %s %s%s%s\n", status, printable(a.Name), printable(role), task)
		}
		return nil
	},
}

var sessionStartCmd = &cobra.Command{
	Use:   "start [name]",
	Short: "Start all agents in a session",
	Long: `Start all enabled agents in a session.

Each agent is started in the session's project directory.
By default, agents run in parallel.
Use --sequential to start agents in sequence.

Examples:
  agentvault session start my-project
  agentvault session start my-project --sequential
  agentvault session start my-project --agent claude  # Start only one agent`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionStart,
}

var sessionStopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop all running agents in a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		session, ok := v.GetSessionByName(args[0])
		if !ok {
			return fmt.Errorf("session %q not found", args[0])
		}

		stopped := 0
		for i, a := range session.Agents {
			if a.PID > 0 {
				process, err := os.FindProcess(a.PID)
				if err == nil {
					if err := process.Signal(os.Interrupt); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to stop agent %q (pid %d): %v\n", printable(a.Name), a.PID, err)
					}
					stopped++
				}
				session.Agents[i].PID = 0
			}
		}

		session.Status = agent.SessionStatusIdle
		session.UpdatedAt = time.Now()
		if err := v.UpdateSession(session); err != nil {
			return err
		}

		fmt.Printf("Stopped %d agent(s) in session %q.\n", stopped, printable(session.Name))
		return nil
	},
}

var sessionRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		session, ok := v.GetSessionByName(args[0])
		if !ok {
			return fmt.Errorf("session %q not found", args[0])
		}

		if err := v.RemoveSession(session.ID); err != nil {
			return err
		}

		fmt.Printf("Session %q removed.\n", args[0])
		return nil
	},
}

var sessionActivateCmd = &cobra.Command{
	Use:   "activate [name]",
	Short: "Set the active session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		session, ok := v.GetSessionByName(args[0])
		if !ok {
			return fmt.Errorf("session %q not found", args[0])
		}

		if err := v.SetActiveSession(session.ID); err != nil {
			return err
		}

		fmt.Printf("Session %q is now active.\n", args[0])
		return nil
	},
}

var sessionExportCmd = &cobra.Command{
	Use:   "export [name] [file]",
	Short: "Export a session to a file",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		includeKeys, _ := cmd.Flags().GetBool("include-keys")
		if includeKeys {
			confirm, _ := cmd.Flags().GetBool("confirm")
			if err := confirmPlaintextExport(confirm, "API keys and secrets", args[1], "drop --include-keys"); err != nil {
				return err
			}
		}

		session, ok := v.GetSessionByName(args[0])
		if !ok {
			return fmt.Errorf("session %q not found", args[0])
		}

		// Bundle the session with its agents, rules, and roles so the
		// recipient can import everything needed to recreate the session.
		// Process IDs are this machine's: they mean nothing elsewhere.
		session.Agents = append([]agent.SessionAgent(nil), session.Agents...)
		for i := range session.Agents {
			session.Agents[i].PID = 0
		}
		session.Status = agent.SessionStatusIdle
		exportData := sessionExport{
			Session: session,
			Rules:   v.SharedConfig().Rules,
			Roles:   v.SharedConfig().Roles,
		}

		// Get agent configs
		for _, sa := range session.Agents {
			if a, ok := v.Get(sa.Name); ok {
				if !includeKeys {
					// The other secrets go too, as agent export blanks them:
					// MCP server env and secret args, credential extras, a
					// base URL with credentials. Import names each blank.
					var blanks []string
					a, _, blanks = blankAgentForExport(a)
					if len(blanks) > 0 {
						if exportData.Blanks == nil {
							exportData.Blanks = map[string][]string{}
						}
						exportData.Blanks[a.Name] = blanks
					}
				}
				exportData.Agents = append(exportData.Agents, a)
			}
		}

		data, err := json.MarshalIndent(exportData, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding session: %w", err)
		}

		if err := os.WriteFile(args[1], data, 0600); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}

		fmt.Printf("Session %q exported to %s\n", args[0], args[1])
		return nil
	},
}

// sessionExport is the file session export writes: the session, its
// agents, and the rules and roles. Blanks names, per agent, each secret
// export blanked, so import can say what it left out.
type sessionExport struct {
	Session agent.Session       `json:"session"`
	Agents  []agent.Agent       `json:"agents"`
	Blanks  map[string][]string `json:"blanks,omitempty"`
	Rules   []agent.UnifiedRule `json:"rules"`
	Roles   []agent.Role        `json:"roles"`
}

var sessionImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import a session from a file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}

		var importData sessionExport
		if err := json.Unmarshal(data, &importData); err != nil {
			return fmt.Errorf("decoding session: %w", err)
		}
		// The session is settled before anything is written: a clash found
		// later would leave its agents, rules and roles imported without it.
		importData.Session = sessionToImport(v, importData.Session, time.Now())
		_, sameSession := sameSessionInVault(v, importData.Session)
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		prefix, would := "Left out", ""
		if dryRun {
			prefix, would = "Dry-run: would leave out", "Dry-run: would import "
		}

		// Import agents the vault does not have, as agent import adds them:
		// blanks dropped and named, MCP server commands only with
		// --keep-bundle-commands.
		keep, _ := cmd.Flags().GetBool("keep-bundle-commands")
		var newAgents []agent.Agent
		for _, a := range importData.Agents {
			if _, exists := v.Get(a.Name); exists {
				continue
			}
			if err := a.Validate(); err != nil {
				return fmt.Errorf("importing agent %q: %w", printable(a.Name), err)
			}
			var blanks []string
			for _, f := range importData.Blanks[a.Name] {
				if profileBlankField(a, f) {
					blanks = append(blanks, f)
				}
			}
			a = prepareImportedAgent(os.Stderr, a, agent.Agent{}, false, blanks, keep, prefix)
			a.CreatedAt = time.Now()
			a.UpdatedAt = time.Now()
			newAgents = append(newAgents, a)
		}

		shared := v.SharedConfig()
		existingRules := make(map[string]bool)
		for _, r := range shared.Rules {
			existingRules[r.Name] = true
		}
		existingRoles := make(map[string]bool)
		for _, r := range shared.Roles {
			existingRoles[r.Name] = true
		}
		if dryRun {
			for _, a := range newAgents {
				fmt.Printf("  %sagent: %s\n", would, printable(a.Name))
			}
			for _, r := range importData.Rules {
				if !existingRules[r.Name] {
					fmt.Printf("  %srule: %s\n", would, printable(r.Name))
				}
			}
			for _, r := range importData.Roles {
				if !existingRoles[r.Name] {
					fmt.Printf("  %srole: %s\n", would, printable(r.Name))
				}
			}
			if sameSession {
				fmt.Printf("\nSession %q already matches this vault.\n", printable(importData.Session.Name))
			} else {
				fmt.Printf("\n%ssession %q.\n", would, printable(importData.Session.Name))
			}
			return nil
		}
		for _, a := range newAgents {
			if err := v.Add(a); err != nil {
				return fmt.Errorf("importing agent %q: %w", printable(a.Name), err)
			}
			fmt.Printf("  Imported agent: %s\n", printable(a.Name))
		}

		// Import rules
		for _, r := range importData.Rules {
			if !existingRules[r.Name] {
				r.CreatedAt = time.Now()
				r.UpdatedAt = time.Now()
				shared.Rules = append(shared.Rules, r)
				fmt.Printf("  Imported rule: %s\n", printable(r.Name))
			}
		}

		// Import roles
		for _, r := range importData.Roles {
			if !existingRoles[r.Name] {
				r.CreatedAt = time.Now()
				r.UpdatedAt = time.Now()
				shared.Roles = append(shared.Roles, r)
				fmt.Printf("  Imported role: %s\n", printable(r.Name))
			}
		}
		if err := v.SetSharedConfig(shared); err != nil {
			return fmt.Errorf("saving shared config: %w", err)
		}

		if sameSession {
			fmt.Printf("\nSession %q already matches this vault.\n", printable(importData.Session.Name))
			return nil
		}
		if err := v.AddSession(importData.Session); err != nil {
			return err
		}

		fmt.Printf("\nSession %q imported successfully.\n", printable(importData.Session.Name))
		return nil
	},
}

// sessionToImport is s as import adds it: idle, without the process IDs
// of the machine it came from (session stop would signal whatever runs
// under them here), and with a fresh ID and an unused name where the vault
// has them already.
func sessionToImport(v *vault.Vault, s agent.Session, now time.Time) agent.Session {
	s = idleSession(s)
	if have, same := sameSessionInVault(v, s); same {
		// A re-import on the machine that exported it adds nothing.
		return have
	}
	s.CreatedAt, s.UpdatedAt = now, now
	if _, exists := v.GetSession(s.ID); exists || s.ID == "" {
		s.ID = agent.GenerateSessionID()
	}
	if _, exists := v.GetSessionByName(s.Name); exists {
		base := s.Name + "-imported"
		s.Name = base
		for n := 2; ; n++ {
			if _, exists := v.GetSessionByName(s.Name); !exists {
				break
			}
			s.Name = fmt.Sprintf("%s-%d", base, n)
		}
	}
	return s
}

// idleSession is s without process IDs and not running.
func idleSession(s agent.Session) agent.Session {
	s.Agents = append([]agent.SessionAgent(nil), s.Agents...)
	for i := range s.Agents {
		s.Agents[i].PID = 0
	}
	s.Status = agent.SessionStatusIdle
	return s
}

// sameSessionInVault is the vault's session of s's name, and whether it
// holds what s holds: the same agents, directory, role and description.
func sameSessionInVault(v *vault.Vault, s agent.Session) (agent.Session, bool) {
	have, ok := v.GetSessionByName(s.Name)
	if !ok {
		return have, false
	}
	a, b := idleSession(have), idleSession(s)
	return have, a.Description == b.Description && a.ProjectDir == b.ProjectDir &&
		a.ActiveRole == b.ActiveRole && reflect.DeepEqual(a.Agents, b.Agents)
}

// runSessionStart orchestrates the parallel (or sequential) launch of all
// enabled agents in a session. Each agent is started as a subprocess in the
// session's project directory with AGENTVAULT_SESSION and AGENTVAULT_AGENT
// environment variables set for identification.
func runSessionStart(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	session, ok := v.GetSessionByName(args[0])
	if !ok {
		return fmt.Errorf("session %q not found", args[0])
	}

	sequential, _ := cmd.Flags().GetBool("sequential")
	agentFilter, _ := cmd.Flags().GetString("agent")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Get shared config for rules/roles
	shared := v.SharedConfig()

	// Build list of agents to start
	var agentsToStart []agent.SessionAgent
	for _, sa := range session.Agents {
		if !sa.Enabled {
			continue
		}
		if agentFilter != "" && sa.Name != agentFilter {
			continue
		}
		agentsToStart = append(agentsToStart, sa)
	}

	if len(agentsToStart) == 0 {
		fmt.Println("No agents to start.")
		return nil
	}

	fmt.Printf("Starting %d agent(s) in session %q...\n\n", len(agentsToStart), printable(session.Name))

	if dryRun {
		fmt.Println("DRY RUN - would start:")
		for _, sa := range agentsToStart {
			a, ok := v.Get(sa.Name)
			if !ok {
				fmt.Printf("  %s (missing)\n", printable(sa.Name))
				continue
			}
			role := sessionAgentRole(sa, session.ActiveRole)
			if role != "" {
				a.Role = role
			}
			prompt := a.BuildEffectivePrompt(shared)
			fmt.Printf("  %s (%s)\n", printable(sa.Name), a.Provider)
			if role != "" {
				fmt.Printf("    Role: %s\n", printable(role))
			}
			if len(prompt) > 100 {
				fmt.Printf("    Prompt: %s...\n", printableText(prompt[:100]))
			} else {
				fmt.Printf("    Prompt: %s\n", printableText(prompt))
			}
		}
		return nil
	}

	// Update session status
	session.Status = agent.SessionStatusRunning
	session.UpdatedAt = time.Now()
	if err := v.UpdateSession(session); err != nil {
		return err
	}

	if sequential {
		started := 0
		for _, sa := range agentsToStart {
			pid, err := startAgent(v, session, sa, shared, true)
			if err != nil {
				fmt.Printf("  Error starting %s: %v\n", printable(sa.Name), err)
				continue
			}
			for i := range session.Agents {
				if session.Agents[i].Name == sa.Name {
					session.Agents[i].PID = pid
					break
				}
			}
			started++
		}
		if started == 0 {
			session.Status = agent.SessionStatusIdle
		}
	} else {
		// Parallel start with optional concurrency cap from session config.
		parallelLimit := v.Sessions().ParallelLimit
		if parallelLimit < 0 {
			parallelLimit = 0
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		var sem chan struct{}
		if parallelLimit > 0 {
			sem = make(chan struct{}, parallelLimit)
		}
		started := 0
		for _, sa := range agentsToStart {
			wg.Add(1)
			go func(sa agent.SessionAgent) {
				defer wg.Done()
				if sem != nil {
					sem <- struct{}{}
					defer func() { <-sem }()
				}
				pid, err := startAgent(v, session, sa, shared, false)
				if err != nil {
					fmt.Printf("  Error starting %s: %v\n", printable(sa.Name), err)
					return
				}
				mu.Lock()
				for i := range session.Agents {
					if session.Agents[i].Name == sa.Name {
						session.Agents[i].PID = pid
						break
					}
				}
				started++
				mu.Unlock()
			}(sa)
		}
		wg.Wait()
		if started == 0 {
			session.Status = agent.SessionStatusIdle
		}
	}

	session.UpdatedAt = time.Now()
	if err := v.UpdateSession(session); err != nil {
		return err
	}

	fmt.Println("\nAll agents started.")
	return nil
}

// startAgent launches a single agent CLI tool as a subprocess.
// It resolves the provider to a CLI command name, verifies the command
// exists in PATH, then starts it in the session's project directory.
// The process PID is reported for later stop/status tracking.
func startAgent(v *vault.Vault, session agent.Session, sa agent.SessionAgent, shared agent.SharedConfig, attachStdin bool) (int, error) {
	_ = shared // reserved for provider-specific startup behavior

	a, ok := v.Get(sa.Name)
	if !ok {
		return 0, fmt.Errorf("agent %q not found", sa.Name)
	}

	// Determine the command to run based on provider
	var cmdPath string
	var cmdArgs []string

	switch a.Provider {
	case agent.ProviderClaude:
		cmdPath = "claude"
		cmdArgs = []string{}
	case agent.ProviderCodex:
		cmdPath = "codex"
		cmdArgs = []string{}
	case agent.ProviderAider:
		cmdPath = "aider"
		cmdArgs = []string{}
	case agent.ProviderMeldbot:
		cmdPath = "meldbot"
		cmdArgs = []string{}
	case agent.ProviderOpenclaw:
		cmdPath = "openclaw"
		cmdArgs = []string{}
	case agent.ProviderNanoclaw:
		cmdPath = "nanoclaw"
		cmdArgs = []string{}
	default:
		return 0, fmt.Errorf("unsupported provider %s for auto-start", a.Provider)
	}

	// Check if command exists
	if _, err := exec.LookPath(cmdPath); err != nil {
		return 0, fmt.Errorf("%s not found in PATH", cmdPath)
	}

	fmt.Printf("  Starting %s (%s) in %s...\n", printable(sa.Name), a.Provider, printable(session.ProjectDir))

	// Start the process
	cmd := exec.Command(cmdPath, cmdArgs...)
	cmd.Dir = session.ProjectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if attachStdin {
		cmd.Stdin = os.Stdin
	}

	// Set environment variables
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENTVAULT_SESSION=%s", session.ID))
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENTVAULT_AGENT=%s", sa.Name))
	if role := sessionAgentRole(sa, session.ActiveRole); role != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AGENTVAULT_ROLE=%s", role))
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", cmdPath, err)
	}

	fmt.Printf("  Started %s (PID: %d)\n", printable(sa.Name), cmd.Process.Pid)
	return cmd.Process.Pid, nil
}

func sessionAgentRole(sa agent.SessionAgent, sessionDefaultRole string) string {
	if strings.TrimSpace(sa.Role) != "" {
		return strings.TrimSpace(sa.Role)
	}
	return strings.TrimSpace(sessionDefaultRole)
}

func init() {
	rootCmd.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCmd.AddCommand(sessionShowCmd)
	sessionCmd.AddCommand(sessionStartCmd)
	sessionCmd.AddCommand(sessionStopCmd)
	sessionCmd.AddCommand(sessionRemoveCmd)
	sessionCmd.AddCommand(sessionActivateCmd)
	sessionCmd.AddCommand(sessionExportCmd)
	sessionCmd.AddCommand(sessionImportCmd)

	sessionListCmd.Flags().Bool("json", false, "output as JSON")

	sessionCreateCmd.Flags().String("dir", "", "project directory (default: current)")
	sessionCreateCmd.Flags().String("agents", "", "comma-separated agent names")
	sessionCreateCmd.Flags().String("role", "", "default role for all agents")

	sessionStartCmd.Flags().Bool("sequential", false, "start agents sequentially instead of parallel")
	sessionStartCmd.Flags().String("agent", "", "start only a specific agent")
	sessionStartCmd.Flags().Bool("dry-run", false, "show what would be started without starting")
	sessionExportCmd.Flags().Bool("include-keys", false, "include the agents' API keys and other secrets (MCP server env and secret args, credential extras, a base URL with credentials)")
	sessionExportCmd.Flags().Bool("confirm", false, "allow --include-keys to write the keys and secrets in plain text")
	sessionImportCmd.Flags().Bool("dry-run", false, "show what would be imported without writing")
	sessionImportCmd.Flags().Bool("keep-bundle-commands", false, "keep MCP server commands of the agents the file adds; left out by default")
}
