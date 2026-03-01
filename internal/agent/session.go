package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Session represents a workspace where multiple agents work together.
//
// A session binds a set of agents to a project directory, allowing them
// to be started in parallel with a single command. Sessions can be
// exported/imported to replicate the exact same multi-agent setup on
// a different machine (e.g., moving from a laptop to a workstation).
//
// Each agent in the session can have an override role and a specific task,
// enabling different agents to focus on different aspects of the same project.
type Session struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	ProjectDir  string         `json:"project_dir"`           // Working directory
	Agents      []SessionAgent `json:"agents"`                // Agents in this session
	ActiveRole  string         `json:"active_role,omitempty"` // Default role for all agents
	Status      SessionStatus  `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// SessionAgent represents an agent configuration within a session.
// It references an agent by name from the vault and adds session-specific
// overrides (role, task assignment, priority order for sequential runs).
type SessionAgent struct {
	Name     string `json:"name"`               // Agent name from vault
	Role     string `json:"role,omitempty"`     // Override role for this agent
	Task     string `json:"task,omitempty"`     // Specific task for this agent
	Priority int    `json:"priority,omitempty"` // Execution priority (lower = first)
	Enabled  bool   `json:"enabled"`            // Whether to include in parallel runs
	PID      int    `json:"pid,omitempty"`      // Process ID if running
}

// SessionStatus represents the state of a session.
type SessionStatus string

const (
	SessionStatusIdle    SessionStatus = "idle"
	SessionStatusRunning SessionStatus = "running"
	SessionStatusPaused  SessionStatus = "paused"
	SessionStatusDone    SessionStatus = "done"
)

// SessionConfig holds session-related settings in the vault.
type SessionConfig struct {
	Sessions      []Session `json:"sessions,omitempty"`
	ActiveSession string    `json:"active_session,omitempty"` // Currently active session ID
	DefaultAgents []string  `json:"default_agents,omitempty"` // Default agents for new sessions
	ParallelLimit int       `json:"parallel_limit,omitempty"` // Max concurrent agents (0 = unlimited)
	// ParallelLimitSet tracks whether parallel_limit was explicitly configured,
	// allowing import/merge logic to distinguish unset from explicit 0 (unlimited).
	ParallelLimitSet bool `json:"parallel_limit_set,omitempty"`
}

// GetSession returns a session by ID.
func (sc *SessionConfig) GetSession(id string) (Session, bool) {
	for _, s := range sc.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return Session{}, false
}

// GetSessionByName returns a session by name.
func (sc *SessionConfig) GetSessionByName(name string) (Session, bool) {
	for _, s := range sc.Sessions {
		if s.Name == name {
			return s, true
		}
	}
	return Session{}, false
}

// AddSession adds a new session.
func (sc *SessionConfig) AddSession(s Session) {
	sc.Sessions = append(sc.Sessions, s)
}

// UpdateSession updates an existing session.
func (sc *SessionConfig) UpdateSession(s Session) bool {
	for i, existing := range sc.Sessions {
		if existing.ID == s.ID {
			sc.Sessions[i] = s
			return true
		}
	}
	return false
}

// RemoveSession removes a session by ID.
func (sc *SessionConfig) RemoveSession(id string) bool {
	for i, s := range sc.Sessions {
		if s.ID == id {
			sc.Sessions = append(sc.Sessions[:i], sc.Sessions[i+1:]...)
			return true
		}
	}
	return false
}

// GenerateSessionID creates a unique session ID.
func GenerateSessionID() string {
	now := time.Now().UTC()
	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// Fallback keeps IDs unique enough even if crypto/rand is unavailable.
		return fmt.Sprintf("%d", now.UnixNano())
	}
	return fmt.Sprintf("%d-%s", now.UnixNano(), hex.EncodeToString(suffix[:]))
}

// NewSession creates a new session with defaults.
func NewSession(name, projectDir string, agents []string) Session {
	sessionAgents := make([]SessionAgent, len(agents))
	for i, a := range agents {
		sessionAgents[i] = SessionAgent{
			Name:     a,
			Priority: i,
			Enabled:  true,
		}
	}

	return Session{
		ID:         GenerateSessionID(),
		Name:       name,
		ProjectDir: projectDir,
		Agents:     sessionAgents,
		Status:     SessionStatusIdle,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}
