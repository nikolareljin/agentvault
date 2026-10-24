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
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/safefile"
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
	Locations         locations.Set                `json:"locations,omitempty"`
	Gateways          []agent.Gateway              `json:"gateways,omitempty"`
}

// Vault represents the encrypted agent store.
type Vault struct {
	path              string
	agents            []agent.Agent
	shared            agent.SharedConfig
	providerConfigs   agent.ProviderConfig
	sessions          agent.SessionConfig
	modelCapabilities []agent.ModelCapabilityEntry
	locations         locations.Set // work dirs and projects, by identity
	gateways          []agent.Gateway
	variantNotes      []string                   // notes of the last agent change, see VariantNotes
	unknown           map[string]json.RawMessage // top-level fields a newer agentvault wrote; kept on save
	key               []byte                     // derived key, set after Init or Unlock
	header            crypto.Header              // key-derivation header written ahead of the ciphertext
	legacyOnDisk      bool                       // file on disk is format 1; back it up before the first write
	legacySalt        []byte                     // salt of that format 1 file, to recognise it at backup time
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
	header, err := crypto.NewHeader()
	if err != nil {
		return err
	}
	key, err := crypto.DeriveKeyFor(masterPassword, header)
	if err != nil {
		return err
	}
	v.header = header
	v.key = key
	v.agents = []agent.Agent{}
	v.shared = agent.SharedConfig{}
	v.providerConfigs = agent.ProviderConfig{}
	v.sessions = agent.SessionConfig{}
	return v.write()
}

// Unlock decrypts the vault with the master password.
func (v *Vault) Unlock(masterPassword string) error {
	data, err := safefile.Read(v.path)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}
	plaintext, header, key, err := crypto.Open(data, masterPassword)
	if err != nil {
		if errors.Is(err, crypto.ErrHeader) {
			return fmt.Errorf("vault file: %w", err)
		}
		return errors.New("wrong password or corrupted vault")
	}
	// A format 1 vault is upgraded on its next write: derive the key for a
	// current header now, while the password is at hand. Nothing is written
	// by unlocking alone.
	wasLegacy := header.Legacy
	legacySalt := header.Salt
	if wasLegacy {
		upgraded, err := crypto.NewHeader()
		if err != nil {
			crypto.Zero(key)
			return err
		}
		newKey, err := crypto.DeriveKeyFor(masterPassword, upgraded)
		crypto.Zero(key)
		if err != nil {
			return err
		}
		key, header = newKey, upgraded
	}
	vd, err := decodeVaultData(plaintext)
	if err != nil {
		crypto.Zero(key)
		return err
	}
	v.header = header
	v.key = key
	v.legacyOnDisk = wasLegacy
	v.legacySalt = legacySalt
	v.apply(vd, plaintext)
	return nil
}

func decodeVaultData(plaintext []byte) (vaultData, error) {
	var vd vaultData
	if err := json.Unmarshal(plaintext, &vd); err != nil {
		// try legacy format (plain agent array) for backward compat
		var agents []agent.Agent
		if legacyDecodeErr := json.Unmarshal(plaintext, &agents); legacyDecodeErr != nil {
			return vaultData{}, fmt.Errorf("decoding vault data: %w", legacyDecodeErr)
		}
		vd = vaultData{Agents: agents}
	}
	return vd, nil
}

// apply replaces the in-memory state with vd, decoded from plaintext.
func (v *Vault) apply(vd vaultData, plaintext []byte) {
	unknown := unknownVaultFields(plaintext)
	v.agents = vd.Agents
	v.shared = vd.Shared
	v.providerConfigs = vd.ProviderConfigs
	v.sessions = vd.Sessions
	v.modelCapabilities = normalizeCapabilityEntries(vd.ModelCapabilities)
	v.locations = vd.Locations
	v.gateways = vd.Gateways
	v.unknown = unknown
	if v.sessions.ParallelLimit != 0 {
		v.sessions.ParallelLimitSet = true
	} else if sessionParallelLimitDefined(plaintext) {
		v.sessions.ParallelLimitSet = true
	}
}

// ErrVaultRelocked is Reload's error when the file on disk was written
// under another password or format. The vault is locked: writing the old
// state back would undo that change.
var ErrVaultRelocked = errors.New("vault file changed under another password or format; reopen it")

// Unlocked reports whether Save can write: false before Unlock and after
// Reload locked the vault.
func (v *Vault) Unlocked() bool {
	return v.key != nil
}

// lock drops the key, so Save fails until the vault is unlocked again.
func (v *Vault) lock() {
	crypto.Zero(v.key)
	v.key = nil
}

// Reload re-reads the vault file with the key from Unlock, so changes made
// by another process (such as an agentvault command run from the TUI) are
// seen and not overwritten by the next Save.
func (v *Vault) Reload() error {
	if v.key == nil {
		return errors.New("vault is not unlocked")
	}
	data, err := safefile.Read(v.path)
	if err != nil {
		// Unknown content: saving the old state could overwrite it.
		v.lock()
		return fmt.Errorf("reading vault: %w", err)
	}
	if v.legacyOnDisk {
		// Unlocked from a format 1 file and not written since: unchanged
		// while it still starts with that salt. Any agentvault write
		// upgrades it under a header this key was not derived for.
		if len(v.legacySalt) > 0 && bytes.HasPrefix(data, v.legacySalt) {
			return nil
		}
	}
	plaintext, err := crypto.OpenWithKey(data, v.header, v.key)
	if err != nil {
		v.lock()
		if errors.Is(err, crypto.ErrHeaderChanged) {
			return ErrVaultRelocked
		}
		return fmt.Errorf("reloading vault: %w", err)
	}
	vd, err := decodeVaultData(plaintext)
	if err != nil {
		v.lock()
		return err
	}
	v.legacyOnDisk = false
	v.apply(vd, plaintext)
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

// Locations returns the work dirs and projects the vault knows.
func (v *Vault) Locations() locations.Set {
	return v.locations
}

// SetLocations replaces the known work dirs and projects and persists.
func (v *Vault) SetLocations(s locations.Set) error {
	v.locations = s
	return v.Save()
}

// Gateways returns a copy of the stored gateways.
func (v *Vault) Gateways() []agent.Gateway {
	return append([]agent.Gateway(nil), v.gateways...)
}

// GetGateway returns the gateway with the given name.
func (v *Vault) GetGateway(name string) (agent.Gateway, bool) {
	for _, g := range v.gateways {
		if g.Name == name {
			return g, true
		}
	}
	return agent.Gateway{}, false
}

// AddGateway stores a new gateway and persists.
func (v *Vault) AddGateway(g agent.Gateway) error {
	if _, ok := v.GetGateway(g.Name); ok {
		return fmt.Errorf("gateway %q already exists", g.Name)
	}
	v.gateways = append(v.gateways, g)
	return v.Save()
}

// UpdateGateway replaces a gateway by name and persists.
func (v *Vault) UpdateGateway(g agent.Gateway) error {
	for i := range v.gateways {
		if v.gateways[i].Name == g.Name {
			v.gateways[i] = g
			return v.Save()
		}
	}
	return fmt.Errorf("gateway %q not found", g.Name)
}

// RemoveGateway deletes a gateway by name and persists.
func (v *Vault) RemoveGateway(name string) error {
	for i := range v.gateways {
		if v.gateways[i].Name == name {
			v.gateways = append(v.gateways[:i], v.gateways[i+1:]...)
			return v.Save()
		}
	}
	return fmt.Errorf("gateway %q not found", name)
}

// SetGateways replaces all gateways and persists.
func (v *Vault) SetGateways(gs []agent.Gateway) error {
	v.gateways = append([]agent.Gateway(nil), gs...)
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
	v.normalizeVariants(a.Name)
	return v.Save()
}

// Update replaces an agent by name and persists the vault.
func (v *Vault) Update(a agent.Agent) error {
	for i, existing := range v.agents {
		if existing.Name == a.Name {
			v.agents[i] = a
			v.normalizeVariants(a.Name)
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
	v.normalizeVariants("")
	return v.Save()
}

// normalizeVariants keeps one default per variant group after a change to
// agent preferred (which wins when it is marked default) and records a note
// for each group it had to fix, for VariantNotes.
func (v *Vault) normalizeVariants(preferred string) {
	_, v.variantNotes = agent.NormalizeVariantDefaults(v.agents, preferred)
}

// VariantNotes returns the notes of the last Add, Update or Remove: one per
// variant group whose default had to be chosen.
func (v *Vault) VariantNotes() []string {
	return v.variantNotes
}

// SetVariantDefault makes name the default agent of group and persists it.
func (v *Vault) SetVariantDefault(group, name string) error {
	if err := agent.SetVariantDefault(v.agents, group, name); err != nil {
		return err
	}
	return v.Save()
}

// SetVariantPolicy stores policy on every member of group and persists it.
func (v *Vault) SetVariantPolicy(group, policy string) error {
	if err := agent.SetVariantPolicy(v.agents, group, policy); err != nil {
		return err
	}
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
// Each entry is a deep copy: callers may modify the returned Capabilities slice
// without affecting in-memory vault state.
func (v *Vault) ListCapabilities() []agent.ModelCapabilityEntry {
	out := make([]agent.ModelCapabilityEntry, len(v.modelCapabilities))
	for i, e := range v.modelCapabilities {
		out[i] = e
		out[i].Capabilities = append([]string(nil), e.Capabilities...)
	}
	return out
}

// ErrCapabilityExists is AddCapability's error for an endpoint and model
// that are already listed.
var ErrCapabilityExists = errors.New("capability entry already exists")

// ErrCapabilityNotFound is RemoveCapability's error for an endpoint and
// model that are not listed.
var ErrCapabilityNotFound = errors.New("no capability entry found")

// AddCapability adds a new model capability entry. Returns an error if an entry
// for the same endpoint+model combination already exists.
func (v *Vault) AddCapability(entry agent.ModelCapabilityEntry) error {
	entry.EndpointURL = strings.TrimRight(strings.TrimSpace(entry.EndpointURL), "/")
	entry.ModelName = strings.TrimSpace(entry.ModelName)
	if entry.EndpointURL == "" {
		return fmt.Errorf("capability entry requires a non-empty EndpointURL")
	}
	if entry.ModelName == "" {
		return fmt.Errorf("capability entry requires a non-empty ModelName")
	}
	for _, e := range v.modelCapabilities {
		if e.EndpointURL == entry.EndpointURL && e.ModelName == entry.ModelName {
			// No URL in the text: it may carry credentials. Callers that
			// print it mask it first.
			return fmt.Errorf("%w for model %s", ErrCapabilityExists, entry.ModelName)
		}
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	v.modelCapabilities = append(v.modelCapabilities, entry)
	return v.Save()
}

// AddCapabilities adds multiple capability entries in a single vault save.
// Duplicate entries (same endpoint+model) are skipped; the returned int is the
// number actually added.
func (v *Vault) AddCapabilities(entries []agent.ModelCapabilityEntry) (int, error) {
	added := 0
	for _, entry := range entries {
		entry.EndpointURL = strings.TrimRight(strings.TrimSpace(entry.EndpointURL), "/")
		entry.ModelName = strings.TrimSpace(entry.ModelName)
		if entry.EndpointURL == "" || entry.ModelName == "" {
			continue
		}
		dup := false
		for _, e := range v.modelCapabilities {
			if e.EndpointURL == entry.EndpointURL && e.ModelName == entry.ModelName {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if entry.UpdatedAt.IsZero() {
			entry.UpdatedAt = time.Now().UTC()
		}
		v.modelCapabilities = append(v.modelCapabilities, entry)
		added++
	}
	if added == 0 {
		return 0, nil
	}
	return added, v.Save()
}

// normalizeCapabilityEntries returns entries with EndpointURL and ModelName trimmed
// to their canonical form and with exact-duplicate (endpoint,model) pairs removed.
// Called on vault load so all in-memory entries are in canonical form regardless of
// how they were written (direct edit, older version, import).
func normalizeCapabilityEntries(entries []agent.ModelCapabilityEntry) []agent.ModelCapabilityEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]agent.ModelCapabilityEntry, 0, len(entries))
	for _, e := range entries {
		e.EndpointURL = strings.TrimRight(strings.TrimSpace(e.EndpointURL), "/")
		e.ModelName = strings.TrimSpace(e.ModelName)
		if e.EndpointURL == "" || e.ModelName == "" {
			continue
		}
		key := e.EndpointURL + "\x00" + e.ModelName
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}

// SetCapabilities replaces the whole model capability list and persists.
// Entries are normalized the same way as on vault load, so import and direct
// edits converge on identical keys.
func (v *Vault) SetCapabilities(entries []agent.ModelCapabilityEntry) error {
	v.modelCapabilities = normalizeCapabilityEntries(entries)
	return v.Save()
}

// RemoveCapability removes a capability entry by endpoint URL and model name.
// Returns an error if not found.
func (v *Vault) RemoveCapability(endpointURL, modelName string) error {
	endpointURL = strings.TrimRight(strings.TrimSpace(endpointURL), "/")
	modelName = strings.TrimSpace(modelName)
	for i, e := range v.modelCapabilities {
		if e.EndpointURL == endpointURL && e.ModelName == modelName {
			v.modelCapabilities = append(v.modelCapabilities[:i], v.modelCapabilities[i+1:]...)
			return v.Save()
		}
	}
	// No URL in the text, as in AddCapability.
	return fmt.Errorf("%w for model %s", ErrCapabilityNotFound, modelName)
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
	if problem := agent.TextProblem([]byte(inst.Content)); problem != "" {
		// Saving would replace every invalid byte, so the file pushed back
		// would not be the file pulled.
		return fmt.Errorf("instruction %q: %s", inst.Name, problem)
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
	parts := strings.SplitN(key, "\x00", 4)
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
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions, ModelCapabilities: v.modelCapabilities, Locations: v.locations, Gateways: v.gateways}
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
	// Normalize the same way AddCapability does so import and direct-add produce identical keys.
	seenCaps := make(map[string]struct{}, len(v.modelCapabilities))
	for _, c := range v.modelCapabilities {
		k := strings.TrimRight(strings.TrimSpace(c.EndpointURL), "/") + "\x00" + strings.TrimSpace(c.ModelName)
		seenCaps[k] = struct{}{}
	}
	for _, c := range vd.ModelCapabilities {
		c.EndpointURL = strings.TrimRight(strings.TrimSpace(c.EndpointURL), "/")
		c.ModelName = strings.TrimSpace(c.ModelName)
		if c.EndpointURL == "" || c.ModelName == "" {
			continue
		}
		key := c.EndpointURL + "\x00" + c.ModelName
		if _, ok := seenCaps[key]; !ok {
			v.modelCapabilities = append(v.modelCapabilities, c)
			seenCaps[key] = struct{}{}
		}
	}
	// Gateways: keep existing ones, add those not present by name.
	for _, g := range vd.Gateways {
		if _, ok := v.GetGateway(g.Name); !ok && g.Name != "" {
			v.gateways = append(v.gateways, g)
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

// unknownVaultFields returns the top-level fields of a decoded vault that
// vaultData does not know: a newer agentvault wrote them. Saving keeps them,
// so a field added later survives this version's writes.
func unknownVaultFields(plaintext []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(plaintext, &all) != nil {
		return nil // legacy plain agent array
	}
	t := reflect.TypeFor[vaultData]()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		delete(all, name)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// keepUnknownVaultFields adds the unknown fields back to an encoded vault.
func keepUnknownVaultFields(plaintext []byte, unknown map[string]json.RawMessage) ([]byte, error) {
	if len(unknown) == 0 {
		return plaintext, nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(plaintext, &all); err != nil {
		return nil, err
	}
	for k, raw := range unknown {
		if _, ok := all[k]; !ok {
			all[k] = raw
		}
	}
	return json.Marshal(all)
}

// write encrypts the entire vault state and writes it atomically to disk.
// The output format is: [format 2 header][nonce + AES-256-GCM ciphertext].
func (v *Vault) write() error {
	vd := vaultData{Agents: v.agents, Shared: v.shared, ProviderConfigs: v.providerConfigs, Sessions: v.sessions, ModelCapabilities: v.modelCapabilities, Locations: v.locations, Gateways: v.gateways}
	plaintext, err := json.Marshal(vd)
	if err != nil {
		return fmt.Errorf("encoding vault: %w", err)
	}
	if plaintext, err = keepUnknownVaultFields(plaintext, v.unknown); err != nil {
		return fmt.Errorf("encoding vault: %w", err)
	}
	data, err := crypto.Seal(v.header, v.key, plaintext)
	if err != nil {
		return err
	}
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
	if v.legacyOnDisk {
		if err := backupLegacyVault(v.path, v.legacySalt); err != nil {
			return err
		}
	}
	if err := replaceFile(tmpPath, v.path); err != nil {
		return fmt.Errorf("replacing vault file: %w", err)
	}
	cleanup = false
	v.legacyOnDisk = false
	return nil
}

// LegacyBackupSuffix names the copy of a format 1 vault kept when it is first
// rewritten in format 2, so an earlier release can still be pointed at it.
const LegacyBackupSuffix = ".v1.bak"

// backupLegacyVault copies the format 1 vault at path to path+".v1.bak". The
// copy is written and synced under a unique name first, then hard-linked into
// place, so a crash never leaves a partial file at the backup name. Where hard
// links are not supported (FAT, some network shares) the backup name is
// created exclusively and written, and the unique copy is removed only after
// that succeeds. An existing backup is never overwritten: if it holds other
// bytes (an older vault at the same path), this copy stays under its unique
// name. If the file no longer starts with salt, another process has already
// replaced it, and there is no format 1 file left to back up.
func backupLegacyVault(path string, salt []byte) error {
	old, err := safefile.Read(path)
	if err != nil {
		return fmt.Errorf("backing up format 1 vault: %w", err)
	}
	if len(salt) == 0 || !bytes.HasPrefix(old, salt) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".v1.*.bak")
	if err != nil {
		return fmt.Errorf("backing up format 1 vault: %w", err)
	}
	tmp := f.Name()
	if err := writeSynced(f, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backing up format 1 vault: %w", err)
	}
	final := path + LegacyBackupSuffix
	if err := linkFile(tmp, final); err != nil {
		existing, rerr := safefile.Read(final)
		switch {
		case rerr == nil && bytes.Equal(existing, old):
		case rerr == nil || !errors.Is(rerr, os.ErrNotExist):
			// The name holds another file: keep the copy under its unique name.
			return nil
		default:
			// No hard links here: write the backup name directly.
			bf, cerr := os.OpenFile(final, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if cerr != nil {
				return nil
			}
			if werr := writeSynced(bf, old); werr != nil {
				_ = os.Remove(final)
				return nil
			}
		}
	}
	_ = os.Remove(tmp)
	return nil
}

// linkFile is os.Link; tests replace it to act as a file system without
// hard links.
var linkFile = os.Link

func writeSynced(f *os.File, data []byte) error {
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	return errors.Join(werr, serr, cerr)
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
