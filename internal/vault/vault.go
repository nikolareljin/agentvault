// Package vault implements the encrypted agent store.
//
// The vault file format is: [16-byte salt][nonce + AES-256-GCM ciphertext].
// The JSON payload contains all agents, shared config, provider configs,
// and sessions. The file is stored with mode 0600 for security.
//
// Workflow:
//  1. Init() creates a new vault with a master password
//  2. Unlock() decrypts an existing vault
//  3. CRUD operations modify in-memory state
//  4. Save() re-encrypts and persists to disk
//
// The vault supports a legacy format (plain agent array) for backward
// compatibility with older versions.
package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
	"github.com/nikolareljin/agentvault/internal/textutil"
)

// vaultData is the JSON structure persisted inside the encrypted file.
// All fields are serialized together as a single encrypted blob.
type vaultData struct {
	Agents          []agent.Agent        `json:"agents"`
	Shared          agent.SharedConfig   `json:"shared"`
	ProviderConfigs agent.ProviderConfig `json:"provider_configs"`
	Sessions        agent.SessionConfig  `json:"sessions"`
}

// Vault represents the encrypted agent store.
type Vault struct {
	path            string
	agents          []agent.Agent
	shared          agent.SharedConfig
	providerConfigs agent.ProviderConfig
	sessions        agent.SessionConfig
	key             []byte // derived key, set after Init or Unlock
	salt            []byte // persisted salt
}

// New creates a Vault instance at the given path.
func New(path string) *Vault {
	return &Vault{path: path}
}

// Path returns the vault file path.
func (v *Vault) Path() string {
	return v.path
}

// Exists returns true if the vault file exists on disk.
func (v *Vault) Exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

// Init creates a new empty vault file encrypted with the master password.
func (v *Vault) Init(masterPassword string) error {
	if v.Exists() {
		return errors.New("vault already exists at " + v.path)
	}
	dir := filepath.Dir(v.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating vault directory: %w", err)
	}
	salt, err := crypto.GenerateSalt()
	if err != nil {
		return err
	}
	key, err := crypto.DeriveKey(masterPassword, salt)
	if err != nil {
		return err
	}
	v.salt = salt
	v.key = key
	v.agents = []agent.Agent{}
	v.shared = agent.SharedConfig{}
	v.providerConfigs = agent.ProviderConfig{}
	v.sessions = agent.SessionConfig{}
	return v.write()
}

// Unlock decrypts the vault with the master password.
func (v *Vault) Unlock(masterPassword string) error {
	data, err := os.ReadFile(v.path)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}
	if len(data) < crypto.SaltLen {
		return errors.New("vault file is corrupted (too short)")
	}
	salt := data[:crypto.SaltLen]
	ciphertext := data[crypto.SaltLen:]

	key, err := crypto.DeriveKey(masterPassword, salt)
	if err != nil {
		return err
	}
	plaintext, err := crypto.Decrypt(ciphertext, key)
	if err != nil {
		return errors.New("wrong password or corrupted vault")
	}
	var vd vaultData
	if err := json.Unmarshal(plaintext, &vd); err != nil {
		// try legacy format (plain agent array) for backward compat
		var agents []agent.Agent
		if err2 := json.Unmarshal(plaintext, &agents); err2 != nil {
			return fmt.Errorf("decoding vault data: %w", err)
		}
		vd = vaultData{Agents: agents}
	}
	// Copy the salt out of the file buffer to avoid retaining/aliasing the entire encrypted blob.
	v.salt = append([]byte(nil), salt...)
	v.key = key
	v.agents = vd.Agents
	v.shared = vd.Shared
	v.providerConfigs = vd.ProviderConfigs
	v.sessions = vd.Sessions
	if v.sessions.ParallelLimit != 0 {
		v.sessions.ParallelLimitSet = true
	} else if sessionParallelLimitDefined(plaintext) {
		v.sessions.ParallelLimitSet = true
	}
	return nil
}

// SharedConfig returns the vault's shared configuration.
func (v *Vault) SharedConfig() agent.SharedConfig {
	return v.shared
}

// SetSharedConfig updates the shared configuration and persists.
func (v *Vault) SetSharedConfig(sc agent.SharedConfig) error {
	v.shared = sc
	return v.Save()
}

// ProviderConfigs returns the vault's provider-specific configurations.
func (v *Vault) ProviderConfigs() agent.ProviderConfig {
	return v.providerConfigs
}

// SetProviderConfigs updates the provider configurations and persists.
func (v *Vault) SetProviderConfigs(pc agent.ProviderConfig) error {
	v.providerConfigs = pc
	return v.Save()
}

// SetClaudeConfig updates Claude-specific configuration.
func (v *Vault) SetClaudeConfig(cc *agent.ClaudeConfig) error {
	v.providerConfigs.Claude = cc
	return v.Save()
}

// SetCodexConfig updates Codex-specific configuration.
func (v *Vault) SetCodexConfig(cc *agent.CodexConfig) error {
	v.providerConfigs.Codex = cc
	return v.Save()
}

// SetOllamaConfig updates Ollama-specific configuration.
func (v *Vault) SetOllamaConfig(oc *agent.OllamaConfig) error {
	v.providerConfigs.Ollama = oc
	return v.Save()
}

// Sessions returns the vault's session configuration.
func (v *Vault) Sessions() agent.SessionConfig {
	return v.sessions
}

// SetSessions updates the session configuration and persists.
func (v *Vault) SetSessions(sc agent.SessionConfig) error {
	// Treat SetSessions as an explicit session-config write, including a
	// deliberate unlimited parallel limit (0).
	sc.ParallelLimitSet = true
	v.sessions = sc
	return v.Save()
}

// GetSession returns a session by ID.
func (v *Vault) GetSession(id string) (agent.Session, bool) {
	return v.sessions.GetSession(id)
}

// GetSessionByName returns a session by name.
func (v *Vault) GetSessionByName(name string) (agent.Session, bool) {
	return v.sessions.GetSessionByName(name)
}

// AddSession adds a new session to the vault.
func (v *Vault) AddSession(s agent.Session) error {
	if _, exists := v.sessions.GetSession(s.ID); exists {
		return fmt.Errorf("session %q already exists", s.ID)
	}
	v.sessions.AddSession(s)
	return v.Save()
}

// UpdateSession updates an existing session.
func (v *Vault) UpdateSession(s agent.Session) error {
	if !v.sessions.UpdateSession(s) {
		return fmt.Errorf("session %q not found", s.ID)
	}
	return v.Save()
}

// RemoveSession removes a session by ID.
func (v *Vault) RemoveSession(id string) error {
	if !v.sessions.RemoveSession(id) {
		return fmt.Errorf("session %q not found", id)
	}
	return v.Save()
}

// SetActiveSession sets the currently active session.
func (v *Vault) SetActiveSession(id string) error {
	v.sessions.ActiveSession = id
	return v.Save()
}

// List returns all agents in the vault.
func (v *Vault) List() []agent.Agent {
	return v.agents
}

// Get returns an agent by name.
func (v *Vault) Get(name string) (agent.Agent, bool) {
	for _, a := range v.agents {
		if a.Name == name {
			return a, true
		}
	}
	return agent.Agent{}, false
}

// Add appends an agent to the vault and persists it.
func (v *Vault) Add(a agent.Agent) error {
	for _, existing := range v.agents {
		if existing.Name == a.Name {
			return fmt.Errorf("agent %q already exists", a.Name)
		}
	}
	v.agents = append(v.agents, a)
	return v.Save()
}

// Update replaces an agent by name and persists the vault.
func (v *Vault) Update(a agent.Agent) error {
	for i, existing := range v.agents {
		if existing.Name == a.Name {
			v.agents[i] = a
			return v.Save()
		}
	}
	return fmt.Errorf("agent %q not found", a.Name)
}

// Remove deletes an agent by name and persists the vault.
func (v *Vault) Remove(name string) error {
	idx := -1
	for i, a := range v.agents {
		if a.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("agent %q not found", name)
	}
	v.agents = append(v.agents[:idx], v.agents[idx+1:]...)
	return v.Save()
}

// Save encrypts and persists the vault to disk.
func (v *Vault) Save() error {
	if v.key == nil {
		return errors.New("vault is not unlocked")
	}
	return v.write()
}

// GetInstruction returns a stored instruction file by name.
func (v *Vault) GetInstruction(name string) (agent.InstructionFile, bool) {
	for _, inst := range v.shared.Instructions {
		if inst.Name == name {
			return inst, true
		}
	}
	return agent.InstructionFile{}, false
}

// SetInstruction stores or updates an instruction file in the vault.
func (v *Vault) SetInstruction(inst agent.InstructionFile) error {
	for i, existing := range v.shared.Instructions {
		if existing.Name == inst.Name {
			v.shared.Instructions[i] = inst
			return v.Save()
		}
	}
	v.shared.Instructions = append(v.shared.Instructions, inst)
	return v.Save()
}

// RemoveInstruction removes a stored instruction file by name.
func (v *Vault) RemoveInstruction(name string) error {
	idx := -1
	for i, inst := range v.shared.Instructions {
		if inst.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("instruction %q not found", name)
	}
	v.shared.Instructions = append(v.shared.Instructions[:idx], v.shared.Instructions[idx+1:]...)
	return v.Save()
}

// ListInstructions returns all stored instruction files.
func (v *Vault) ListInstructions() []agent.InstructionFile {
	return v.shared.Instructions
}

// ExportData returns the vault contents as JSON (for export to a file).
func (v *Vault) ExportData() ([]byte, error) {
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions}
	return json.MarshalIndent(vd, "", "  ")
}

// ImportData merges agents plus shared/provider/session config from JSON into this vault.
// Agents with duplicate names are skipped. Shared config fields (system prompt,
// MCP servers, instructions, rules, roles, and prompt sessions) are merged using a
// "don't overwrite existing" strategy. Provider configs and sessions are merged
// with the same non-destructive behavior, so existing vault values take
// precedence over imported values to prevent accidental data loss.
func (v *Vault) ImportData(data []byte) (imported int, skipped []string, err error) {
	var vd vaultData
	if err := json.Unmarshal(data, &vd); err != nil {
		return 0, nil, fmt.Errorf("decoding import data: %w", err)
	}
	importedParallelLimitDefined := sessionParallelLimitDefined(data) || vd.Sessions.ParallelLimitSet || vd.Sessions.ParallelLimit != 0
	for _, a := range vd.Agents {
		_, exists := v.Get(a.Name)
		if exists {
			skipped = append(skipped, a.Name)
			continue
		}
		v.agents = append(v.agents, a)
		imported++
	}
	// merge shared system prompt (don't overwrite existing)
	if vd.Shared.SystemPrompt != "" && v.shared.SystemPrompt == "" {
		v.shared.SystemPrompt = vd.Shared.SystemPrompt
	}
	// merge shared MCP servers
	seenMCP := make(map[string]struct{})
	for _, s := range v.shared.MCPServers {
		seenMCP[s.Name] = struct{}{}
	}
	for _, s := range vd.Shared.MCPServers {
		if _, ok := seenMCP[s.Name]; !ok {
			v.shared.MCPServers = append(v.shared.MCPServers, s)
		}
	}
	// merge instructions (don't overwrite existing by name)
	seenInst := make(map[string]struct{})
	for _, inst := range v.shared.Instructions {
		seenInst[inst.Name] = struct{}{}
	}
	for _, inst := range vd.Shared.Instructions {
		if _, ok := seenInst[inst.Name]; !ok {
			v.shared.Instructions = append(v.shared.Instructions, inst)
		}
	}
	// merge shared rules (don't overwrite existing by name)
	seenRules := make(map[string]struct{})
	for _, r := range v.shared.Rules {
		seenRules[r.Name] = struct{}{}
	}
	for _, r := range vd.Shared.Rules {
		if _, ok := seenRules[r.Name]; ok {
			continue
		}
		v.shared.Rules = append(v.shared.Rules, r)
		seenRules[r.Name] = struct{}{}
	}
	// merge shared roles (don't overwrite existing by name)
	seenRoles := make(map[string]struct{})
	for _, r := range v.shared.Roles {
		seenRoles[r.Name] = struct{}{}
	}
	for _, r := range vd.Shared.Roles {
		if _, ok := seenRoles[r.Name]; ok {
			continue
		}
		v.shared.Roles = append(v.shared.Roles, r)
		seenRoles[r.Name] = struct{}{}
	}
	// merge prompt sessions (don't overwrite existing by ID)
	seenPromptSessions := make(map[string]struct{})
	for _, s := range v.shared.PromptSessions {
		normalizedID := truncatePromptImportField(s.ID)
		if normalizedID == "" {
			continue
		}
		seenPromptSessions[normalizedID] = struct{}{}
	}
	importedPromptSessions := make([]agent.PromptSession, 0, len(vd.Shared.PromptSessions))
	for _, s := range vd.Shared.PromptSessions {
		trimmedOriginalID := strings.TrimSpace(s.ID)
		normalizedID := truncatePromptImportField(s.ID)
		if normalizedID == "" || isPromptSessionIDOverlong(trimmedOriginalID) {
			s.ID = generateUniquePromptSessionID(seenPromptSessions)
		} else {
			s.ID = normalizedID
		}
		if _, ok := seenPromptSessions[s.ID]; ok {
			continue
		}
		seenPromptSessions[s.ID] = struct{}{}
		importedPromptSessions = append(importedPromptSessions, s)
	}
	if len(importedPromptSessions) > agent.PromptSessionRetentionLimit {
		sort.SliceStable(importedPromptSessions, func(i, j int) bool {
			return promptSessionTimestamp(importedPromptSessions[i]).Before(promptSessionTimestamp(importedPromptSessions[j]))
		})
		start := len(importedPromptSessions) - agent.PromptSessionRetentionLimit
		capped := make([]agent.PromptSession, agent.PromptSessionRetentionLimit)
		copy(capped, importedPromptSessions[start:])
		importedPromptSessions = capped
	}
	for _, s := range importedPromptSessions {
		v.shared.PromptSessions = append(v.shared.PromptSessions, sanitizeImportedPromptSession(s))
	}
	if len(v.shared.PromptSessions) > agent.PromptSessionRetentionLimit {
		sort.SliceStable(v.shared.PromptSessions, func(i, j int) bool {
			return promptSessionTimestamp(v.shared.PromptSessions[i]).Before(promptSessionTimestamp(v.shared.PromptSessions[j]))
		})
		start := len(v.shared.PromptSessions) - agent.PromptSessionRetentionLimit
		capped := make([]agent.PromptSession, agent.PromptSessionRetentionLimit)
		copy(capped, v.shared.PromptSessions[start:])
		v.shared.PromptSessions = capped
	}
	// merge provider configs (don't overwrite existing)
	if v.providerConfigs.Claude == nil && vd.ProviderConfigs.Claude != nil {
		v.providerConfigs.Claude = vd.ProviderConfigs.Claude
	}
	if v.providerConfigs.Codex == nil && vd.ProviderConfigs.Codex != nil {
		v.providerConfigs.Codex = vd.ProviderConfigs.Codex
	}
	if v.providerConfigs.Ollama == nil && vd.ProviderConfigs.Ollama != nil {
		v.providerConfigs.Ollama = vd.ProviderConfigs.Ollama
	}
	// Capture whether session config was empty before merge mutations.
	wasSessionConfigUnset := isSessionConfigUnset(v.sessions)
	// merge sessions (don't overwrite existing by ID)
	seenSessions := make(map[string]struct{})
	for _, s := range v.sessions.Sessions {
		seenSessions[s.ID] = struct{}{}
	}
	for _, s := range vd.Sessions.Sessions {
		if _, ok := seenSessions[s.ID]; !ok {
			v.sessions.Sessions = append(v.sessions.Sessions, s)
			seenSessions[s.ID] = struct{}{}
		}
	}
	// merge session config
	if len(v.sessions.DefaultAgents) == 0 && len(vd.Sessions.DefaultAgents) > 0 {
		v.sessions.DefaultAgents = vd.Sessions.DefaultAgents
	}
	if v.sessions.ActiveSession == "" && vd.Sessions.ActiveSession != "" {
		if _, ok := seenSessions[vd.Sessions.ActiveSession]; ok {
			v.sessions.ActiveSession = vd.Sessions.ActiveSession
		}
	}
	// Only import parallel limit when current session config is otherwise empty.
	// This avoids overwriting an intentional existing 0 (unlimited) setting.
	if wasSessionConfigUnset && importedParallelLimitDefined {
		v.sessions.ParallelLimit = vd.Sessions.ParallelLimit
		v.sessions.ParallelLimitSet = true
	}
	return imported, skipped, v.Save()
}

func promptSessionTimestamp(s agent.PromptSession) time.Time {
	if !s.EndedAt.IsZero() {
		return s.EndedAt
	}
	if !s.StartedAt.IsZero() {
		return s.StartedAt
	}
	return time.Time{}
}

func generateUniquePromptSessionID(seen map[string]struct{}) string {
	for {
		id := agent.GenerateSessionID()
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		return id
	}
}

func sanitizeImportedPromptSession(session agent.PromptSession) agent.PromptSession {
	session.Name = truncatePromptImportField(session.Name)
	session.AgentName = truncatePromptImportField(session.AgentName)
	session.Provider = truncatePromptImportField(session.Provider)
	session.Model = truncatePromptImportField(session.Model)
	entries := session.Entries
	if len(entries) > agent.PromptSessionEntryLimit {
		start := len(entries) - agent.PromptSessionEntryLimit
		capped := make([]agent.PromptTranscriptEntry, agent.PromptSessionEntryLimit)
		copy(capped, entries[start:])
		entries = capped
	}
	for i := range entries {
		entries[i].Prompt = truncatePromptImportField(entries[i].Prompt)
		entries[i].EffectivePrompt = truncatePromptImportField(entries[i].EffectivePrompt)
		entries[i].ResponsePreview = truncatePromptImportField(entries[i].ResponsePreview)
		entries[i].Error = truncatePromptImportField(entries[i].Error)
	}
	session.Entries = entries
	return session
}

func truncatePromptImportField(value string) string {
	trimmed := strings.TrimSpace(value)
	return textutil.TruncateRunesWithEllipsis(trimmed, agent.PromptTranscriptFieldMaxRunes)
}

func isPromptSessionIDOverlong(value string) bool {
	if value == "" {
		return false
	}
	runes := 0
	for range value {
		runes++
		if runes > agent.PromptTranscriptFieldMaxRunes {
			return true
		}
	}
	return false
}

func isSessionConfigUnset(sc agent.SessionConfig) bool {
	return len(sc.Sessions) == 0 &&
		sc.ActiveSession == "" &&
		len(sc.DefaultAgents) == 0 &&
		!sc.ParallelLimitSet
}

func sessionParallelLimitDefined(data []byte) bool {
	var envelope struct {
		Sessions json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return false
	}
	if len(envelope.Sessions) == 0 {
		return false
	}
	var sessions map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Sessions, &sessions); err != nil {
		return false
	}
	_, ok := sessions["parallel_limit"]
	return ok
}

// write encrypts the entire vault state and writes it atomically to disk.
// The output format is: [salt bytes][nonce + AES-256-GCM ciphertext].
func (v *Vault) write() error {
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions}
	plaintext, err := json.Marshal(vd)
	if err != nil {
		return fmt.Errorf("encoding vault: %w", err)
	}
	ciphertext, err := crypto.Encrypt(plaintext, v.key)
	if err != nil {
		return err
	}
	data := make([]byte, len(v.salt)+len(ciphertext))
	copy(data, v.salt)
	copy(data[len(v.salt):], ciphertext)
	dir := filepath.Dir(v.path)
	tmp, err := os.CreateTemp(dir, ".agentvault-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp vault file: %w", err)
	}

	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp vault file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temp vault file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting temp vault permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp vault file: %w", err)
	}
	if err := replaceFile(tmpPath, v.path); err != nil {
		return fmt.Errorf("replacing vault file: %w", err)
	}
	cleanup = false
	return nil
}

func replaceFile(source, target string) error {
	err := os.Rename(source, target)
	if err == nil {
		return nil
	}

	// Windows rename cannot overwrite an existing file, unlike POSIX.
	if runtime.GOOS == "windows" {
		backup := target + ".bak"
		_ = os.Remove(backup)
		if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(source, target); err != nil {
			if restoreErr := os.Rename(backup, target); restoreErr != nil && !os.IsNotExist(restoreErr) {
				return fmt.Errorf("renaming replacement failed: %v; restoring original failed: %v", err, restoreErr)
			}
			return err
		}
		_ = os.Remove(backup)
		return nil
	}

	return err
}
