package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/crypto"
)

// vaultData is the JSON structure persisted inside the encrypted file.
type vaultData struct {
	Agents []agent.Agent      `json:"agents"`
	Shared agent.SharedConfig `json:"shared"`
}

// Vault represents the encrypted agent store.
type Vault struct {
	path   string
	agents []agent.Agent
	shared agent.SharedConfig
	key    []byte // derived key, set after Init or Unlock
	salt   []byte // persisted salt
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
	v.salt = salt
	v.key = key
	v.agents = vd.Agents
	v.shared = vd.Shared
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
	vd := vaultData{Agents: v.agents, Shared: v.shared}
	return json.MarshalIndent(vd, "", "  ")
}

// ImportData merges agents and shared config from JSON into this vault.
// Agents with duplicate names are skipped.
func (v *Vault) ImportData(data []byte) (imported int, skipped []string, err error) {
	var vd vaultData
	if err := json.Unmarshal(data, &vd); err != nil {
		return 0, nil, fmt.Errorf("decoding import data: %w", err)
	}
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
	return imported, skipped, v.Save()
}

// write encrypts agents and writes the vault file.
func (v *Vault) write() error {
	vd := vaultData{Agents: v.agents, Shared: v.shared}
	plaintext, err := json.Marshal(vd)
	if err != nil {
		return fmt.Errorf("encoding vault: %w", err)
	}
	ciphertext, err := crypto.Encrypt(plaintext, v.key)
	if err != nil {
		return err
	}
	data := append(v.salt, ciphertext...)
	if err := os.WriteFile(v.path, data, 0600); err != nil {
		return fmt.Errorf("writing vault: %w", err)
	}
	return nil
}
