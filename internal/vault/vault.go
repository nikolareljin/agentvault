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
	Agents            []agent.Agent                `json:"agents"`
	Shared            agent.SharedConfig           `json:"shared"`
	ProviderConfigs   agent.ProviderConfig         `json:"provider_configs"`
	Sessions          agent.SessionConfig          `json:"sessions"`
	ModelCapabilities []agent.ModelCapabilityEntry `json:"model_capabilities,omitempty"`
}

// Vault represents the encrypted agent store.
type Vault struct {
	path              string
	agents            []agent.Agent
	shared            agent.SharedConfig
	providerConfigs   agent.ProviderConfig
	sessions          agent.SessionConfig
	modelCapabilities []agent.ModelCapabilityEntry
	key               []byte // derived key, set after Init or Unlock
	salt              []byte // persisted salt
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
		if legacyDecodeErr := json.Unmarshal(plaintext, &agents); legacyDecodeErr != nil {
			return fmt.Errorf("decoding vault data: %w", legacyDecodeErr)
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
	v.modelCapabilities = vd.ModelCapabilities
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

// ListCapabilities returns all model capability entries.
func (v *Vault) ListCapabilities() []agent.ModelCapabilityEntry {
	return append([]agent.ModelCapabilityEntry(nil), v.modelCapabilities...)
}

// AddCapability adds a new model capability entry. Returns an error if an entry
// for the same endpoint+model combination already exists.
func (v *Vault) AddCapability(entry agent.ModelCapabilityEntry) error {
	for _, e := range v.modelCapabilities {
		if e.EndpointURL == entry.EndpointURL && e.ModelName == entry.ModelName {
			return fmt.Errorf("capability entry for %s/%s already exists; remove it first with 'agentvault capability remove'", entry.EndpointURL, entry.ModelName)
		}
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	v.modelCapabilities = append(v.modelCapabilities, entry)
	return v.Save()
}

// RemoveCapability removes a capability entry by endpoint URL and model name.
// Returns an error if not found.
func (v *Vault) RemoveCapability(endpointURL, modelName string) error {
	for i, e := range v.modelCapabilities {
		if e.EndpointURL == endpointURL && e.ModelName == modelName {
			v.modelCapabilities = append(v.modelCapabilities[:i], v.modelCapabilities[i+1:]...)
			return v.Save()
		}
	}
	return fmt.Errorf("no capability entry found for %s/%s", endpointURL, modelName)
}

// GetInstruction returns a stored instruction file by name. When multiple
// scoped variants exist, the global-scope variant is returned first; otherwise
// the first match is returned. Use GetInstructionByKey for an exact lookup.
func (v *Vault) GetInstruction(name string) (agent.InstructionFile, bool) {
	var first *agent.InstructionFile
	for i := range v.shared.Instructions {
		inst := &v.shared.Instructions[i]
		if inst.Name != name {
			continue
		}
		if first == nil {
			first = inst
		}
		scope := inst.Scope
		if scope == "" {
			scope = agent.InstructionScopeGlobal
		}
		if scope == agent.InstructionScopeGlobal {
			return *inst, true
		}
	}
	if first != nil {
		return *first, true
	}
	return agent.InstructionFile{}, false
}

// SetInstruction stores or updates an instruction file in the vault.
// Identity is the composite key (Name + Scope + DirectoryPattern), so global
// and directory-scoped variants of the same name coexist.
func (v *Vault) SetInstruction(inst agent.InstructionFile) error {
	if err := agent.ValidateInstructionScope(inst); err != nil {
		return err
	}
	inst = normalizeInstructionForStorage(inst)
	key := agent.InstructionKey(inst)
	for i, existing := range v.shared.Instructions {
		if agent.InstructionKey(existing) == key {
			v.shared.Instructions[i] = inst
			return v.Save()
		}
	}
	v.shared.Instructions = append(v.shared.Instructions, inst)
	return v.Save()
}

func normalizeInstructionForStorage(inst agent.InstructionFile) agent.InstructionFile {
	// Normalize "global" to "" so the field stays omitempty-friendly in exports.
	if inst.Scope == agent.InstructionScopeGlobal {
		inst.Scope = ""
	}
	if inst.Scope == agent.InstructionScopeDirectory {
		inst.DirectoryPattern = agent.NormalizeDirectoryPattern(inst.DirectoryPattern)
	}
	return inst
}

// GetInstructionByKey returns the instruction with the given composite key
// (Name + Scope + DirectoryPattern). Use this when multiple scoped variants
// of the same name may coexist and the caller knows the exact variant.
func (v *Vault) GetInstructionByKey(key string) (agent.InstructionFile, bool) {
	for _, inst := range v.shared.Instructions {
		if agent.InstructionKey(inst) == key {
			return inst, true
		}
	}
	return agent.InstructionFile{}, false
}

// RemoveInstructionByKey removes the instruction with the given composite key.
// Use this when multiple scoped variants of the same name may coexist.
func (v *Vault) RemoveInstructionByKey(key string) error {
	for i, inst := range v.shared.Instructions {
		if agent.InstructionKey(inst) == key {
			v.shared.Instructions = append(v.shared.Instructions[:i], v.shared.Instructions[i+1:]...)
			return v.Save()
		}
	}
	parts := strings.SplitN(key, "\x00", 3)
	name, scope, pattern := "", "", ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		scope = parts[1]
	}
	if len(parts) > 2 {
		pattern = parts[2]
	}
	if pattern != "" {
		return fmt.Errorf("instruction not found: name=%q scope=%q pattern=%q", name, scope, pattern)
	}
	return fmt.Errorf("instruction not found: name=%q scope=%q", name, scope)
}

// RemoveInstruction removes a stored instruction file by name.
// When multiple scoped variants exist, prefers the global-scope variant
// (matching GetInstruction behavior) so that show and remove operate on
// the same variant when --scope is omitted.
func (v *Vault) RemoveInstruction(name string) error {
	first := -1
	for i, inst := range v.shared.Instructions {
		if inst.Name != name {
			continue
		}
		scope := inst.Scope
		if scope == "" {
			scope = agent.InstructionScopeGlobal
		}
		if scope == agent.InstructionScopeGlobal {
			v.shared.Instructions = append(v.shared.Instructions[:i], v.shared.Instructions[i+1:]...)
			return v.Save()
		}
		if first == -1 {
			first = i
		}
	}
	if first == -1 {
		return fmt.Errorf("instruction %q not found", name)
	}
	v.shared.Instructions = append(v.shared.Instructions[:first], v.shared.Instructions[first+1:]...)
	return v.Save()
}

// ListInstructions returns all stored instruction files.
func (v *Vault) ListInstructions() []agent.InstructionFile {
	return v.shared.Instructions
}

// ExportData returns the vault contents as JSON (for export to a file).
func (v *Vault) ExportData() ([]byte, error) {
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions, ModelCapabilities: v.modelCapabilities}
	return json.MarshalIndent(vd, "", "  ")
}

// ImportData merges agents plus shared/provider/session config from JSON into this vault.
// Agents with duplicate names are reported in skippedAgents. Instructions that fail
// scope validation are reported in invalidInstructions. Shared config fields (system
// prompt, MCP servers, instructions, rules, roles, and prompt sessions) are merged using
// a "don't overwrite existing" strategy. Provider configs and sessions are merged with
// the same non-destructive behavior, so existing vault values take precedence over
// imported values to prevent accidental data loss.
// conflicts reports instruction Name+Scope+DirectoryPattern collisions where existing wins;
// same name at different scopes (or same scope with different directory patterns) coexist.
func (v *Vault) ImportData(data []byte) (imported int, skippedAgents []string, invalidInstructions []string, conflicts []agent.InstructionConflict, err error) {
	var vd vaultData
	if err := json.Unmarshal(data, &vd); err != nil {
		return 0, nil, nil, nil, fmt.Errorf("decoding import data: %w", err)
	}
	importedParallelLimitDefined := sessionParallelLimitDefined(data) || vd.Sessions.ParallelLimitSet || vd.Sessions.ParallelLimit != 0
	for _, a := range vd.Agents {
		_, exists := v.Get(a.Name)
		if exists {
			skippedAgents = append(skippedAgents, a.Name)
			continue
		}
		v.agents = append(v.agents, a)
		imported++
	}
	// Filter invalid instructions first so conflicts are only reported for valid ones.
	var validIncoming []agent.InstructionFile
	for _, inst := range vd.Shared.Instructions {
		if err := agent.ValidateInstructionScope(inst); err != nil {
			invalidInstructions = append(invalidInstructions, err.Error())
			continue
		}
		validIncoming = append(validIncoming, normalizeInstructionForStorage(inst))
	}
	conflicts = agent.CheckInstructionConflicts(v.shared.Instructions, validIncoming)
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
	// merge instructions by composite key (Name + Scope + DirectoryPattern)
	seenInst := make(map[string]struct{})
	for _, inst := range v.shared.Instructions {
		seenInst[agent.InstructionKey(inst)] = struct{}{}
	}
	for _, inst := range validIncoming {
		key := agent.InstructionKey(inst)
		if _, ok := seenInst[key]; !ok {
			v.shared.Instructions = append(v.shared.Instructions, inst)
			seenInst[key] = struct{}{}
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
		normalizedID := normalizePromptSessionID(s.ID)
		if normalizedID == "" {
			continue
		}
		seenPromptSessions[normalizedID] = struct{}{}
	}
	importedPromptSessions := make([]agent.PromptSession, 0, len(vd.Shared.PromptSessions))
	for _, s := range vd.Shared.PromptSessions {
		normalizedID := normalizePromptSessionID(s.ID)
		if normalizedID == "" || isPromptSessionIDOverlong(normalizedID) {
			s.ID = generateUniquePromptSessionID(seenPromptSessions)
		} else {
			s.ID = normalizedID
		}
		if _, ok := seenPromptSessions[s.ID]; ok {
			continue
		}
		seenPromptSessions[s.ID] = struct{}{}
		importedPromptSessions = appendNewestPromptSessions(importedPromptSessions, s, agent.PromptSessionRetentionLimit)
	}
	for _, s := range importedPromptSessions {
		v.shared.PromptSessions = append(v.shared.PromptSessions, sanitizeImportedPromptSession(s))
	}
	if !vd.Shared.Router.IsZero() && v.shared.Router.IsZero() {
		v.shared.Router = vd.Shared.Router
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
	// Merge model capabilities: keep existing entries; add imported ones not already present (keyed by endpoint+model).
	seenCaps := make(map[string]struct{}, len(v.modelCapabilities))
	for _, c := range v.modelCapabilities {
		seenCaps[c.EndpointURL+"\x00"+c.ModelName] = struct{}{}
	}
	for _, c := range vd.ModelCapabilities {
		key := c.EndpointURL + "\x00" + c.ModelName
		if _, ok := seenCaps[key]; !ok {
			v.modelCapabilities = append(v.modelCapabilities, c)
			seenCaps[key] = struct{}{}
		}
	}
	// Only import parallel limit when current session config is otherwise empty,
	// to avoid overwriting an intentional existing 0 (unlimited) setting.
	if wasSessionConfigUnset && importedParallelLimitDefined {
		v.sessions.ParallelLimit = vd.Sessions.ParallelLimit
		v.sessions.ParallelLimitSet = true
	}
	return imported, skippedAgents, invalidInstructions, conflicts, v.Save()
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

func normalizePromptSessionID(value string) string {
	return strings.TrimSpace(value)
}

func isPromptSessionIDOverlong(value string) bool {
	if value == "" {
		return false
	}
	runes := 0
	for range value {
		runes++
		if runes > agent.PromptSessionIDMaxRunes {
			return true
		}
	}
	return false
}

func appendNewestPromptSessions(sessions []agent.PromptSession, incoming agent.PromptSession, limit int) []agent.PromptSession {
	if limit <= 0 {
		return sessions[:0]
	}
	sessions = append(sessions, incoming)
	sort.SliceStable(sessions, func(i, j int) bool {
		return promptSessionTimestamp(sessions[i]).Before(promptSessionTimestamp(sessions[j]))
	})
	if len(sessions) <= limit {
		return sessions
	}
	copy(sessions, sessions[len(sessions)-limit:])
	return sessions[:limit]
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
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions, ModelCapabilities: v.modelCapabilities}
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
