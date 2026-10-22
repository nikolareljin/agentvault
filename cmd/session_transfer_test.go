package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func sessionTestVault(t *testing.T, agents []agent.Agent) (*vault.Vault, string) {
	t.Helper()
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, agents, nil)
	useConfigDir(t, dir)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	return v, root
}

// session export blanks every agent secret agent export blanks, not only the
// API key, and writes the keys in plain text only with --confirm.
func TestSessionExportBlanksAgentSecrets(t *testing.T) {
	a := testAgent()
	a.APIKey = "sk-ant-CANARY1"
	a.BaseURL = "https://u:CANARY2@h.example/v1"
	a.MCPServers = []agent.MCPServer{{Name: "m", Command: "srv", Args: []string{"--token", "CANARY3"}, Env: map[string]string{"M_TOKEN": "CANARY4"}}}
	v, root := sessionTestVault(t, []agent.Agent{a})
	if err := v.AddSession(agent.Session{ID: "s1", Name: "s1", Agents: []agent.SessionAgent{{Name: a.Name}}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "s.json")
	if err := sessionExportCmd.RunE(sessionExportCmd, []string{"s1", out}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "CANARY") {
		t.Errorf("session export leaked a secret: %s", data)
	}
	withFlags(t, sessionExportCmd, map[string]string{"include-keys": "true"})
	if err := sessionExportCmd.RunE(sessionExportCmd, []string{"s1", out}); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("--include-keys without --confirm: %v", err)
	}
}

// session import adds an agent's MCP server command only with
// --keep-bundle-commands, as agent import does.
func TestSessionImportGatesMCPCommands(t *testing.T) {
	_, root := sessionTestVault(t, nil)
	a := testAgent()
	a.Name = "incoming"
	a.MCPServers = []agent.MCPServer{{Name: "m", Command: "evil-cmd"}}
	data, _ := json.Marshal(map[string]any{"session": agent.Session{ID: "s2", Name: "s2"}, "agents": []agent.Agent{a}})
	in := filepath.Join(root, "in.json")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sessionImportCmd.RunE(sessionImportCmd, []string{in}); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(root, config.AppName, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	got, ok := v.Get("incoming")
	if !ok {
		t.Fatal("agent not imported")
	}
	for _, m := range got.MCPServers {
		if m.Command == "evil-cmd" {
			t.Errorf("MCP server command taken without --keep-bundle-commands: %+v", got.MCPServers)
		}
	}
}
