package vault

import "github.com/nikolareljin/agentvault/internal/agent"

// Vault represents the encrypted agent store.
type Vault struct {
	path   string
	agents []agent.Agent
}

// New creates a Vault instance at the given path.
func New(path string) *Vault {
	return &Vault{path: path}
}

// Path returns the vault file path.
func (v *Vault) Path() string {
	return v.path
}

// Init creates a new empty vault file encrypted with the master password.
func (v *Vault) Init(masterPassword string) error {
	return nil // placeholder
}

// Unlock decrypts the vault with the master password.
func (v *Vault) Unlock(masterPassword string) error {
	return nil // placeholder
}

// List returns all agents in the vault.
func (v *Vault) List() []agent.Agent {
	return v.agents
}

// Add appends an agent to the vault.
func (v *Vault) Add(a agent.Agent) error {
	v.agents = append(v.agents, a)
	return nil // placeholder
}

// Remove deletes an agent by name.
func (v *Vault) Remove(name string) error {
	return nil // placeholder
}

// Save encrypts and persists the vault to disk.
func (v *Vault) Save() error {
	return nil // placeholder
}
