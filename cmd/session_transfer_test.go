package cmd

import (
	"encoding/json"
	"fmt"
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

func reopenSessionVault(t *testing.T, root string) *vault.Vault {
	t.Helper()
	v := vault.New(filepath.Join(root, config.AppName, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	return v
}

// A session exported without keys, imported where the agent is new, drops
// each blank as agent import does: no empty token or env value is stored.
func TestSessionImportDropsBlankedSecrets(t *testing.T) {
	a := testAgent()
	a.BaseURL = "https://u:CANARY2@h.example/v1"
	a.ProviderMeta = &agent.AgentProviderMeta{Extra: map[string]string{"api_token": "CANARY5", "region": "us"}}
	v, root := sessionTestVault(t, []agent.Agent{a})
	if err := v.AddSession(agent.Session{ID: "s1", Name: "s1", Agents: []agent.SessionAgent{{Name: a.Name, PID: 1}}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "s.json")
	if err := sessionExportCmd.RunE(sessionExportCmd, []string{"s1", out}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var file sessionExport
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Blanks[a.Name]) != 2 || file.Session.Agents[0].PID != 0 {
		t.Fatalf("export: blanks %v, pid %d", file.Blanks, file.Session.Agents[0].PID)
	}
	if err := v.Remove(a.Name); err != nil {
		t.Fatal(err)
	}
	if err := sessionImportCmd.RunE(sessionImportCmd, []string{out}); err != nil {
		t.Fatal(err)
	}
	got, ok := reopenSessionVault(t, root).Get(a.Name)
	if !ok {
		t.Fatal("agent not imported")
	}
	if _, has := got.ProviderMeta.Extra["api_token"]; has || got.ProviderMeta.Extra["region"] != "us" {
		t.Errorf("blank extra stored: %v", got.ProviderMeta.Extra)
	}
}

// A process ID in the file is not kept: session stop would signal it.
// Importing the same session twice gives two distinct names.
func TestSessionImportClearsPIDAndNamesUniquely(t *testing.T) {
	_, root := sessionTestVault(t, nil)
	in := filepath.Join(root, "in.json")
	for i := 0; i < 4; i++ {
		// The first three differ, so each is a new session; the fourth is
		// the vault's "s2" again and adds nothing.
		data, _ := json.Marshal(map[string]any{"session": agent.Session{ID: "s2", Name: "s2",
			Description: fmt.Sprint(i % 3), Agents: []agent.SessionAgent{{Name: "x", PID: 1}}}})
		if err := os.WriteFile(in, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := sessionImportCmd.RunE(sessionImportCmd, []string{in}); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	names := map[string]bool{}
	for _, s := range reopenSessionVault(t, root).Sessions().Sessions {
		if names[s.Name] {
			t.Errorf("duplicate session name %q", s.Name)
		}
		names[s.Name] = true
		for _, sa := range s.Agents {
			if sa.PID != 0 {
				t.Errorf("session %q kept pid %d", s.Name, sa.PID)
			}
		}
	}
	if len(names) != 3 {
		t.Errorf("sessions: %v", names)
	}
}

// session import --dry-run writes nothing.
func TestSessionImportDryRunWritesNothing(t *testing.T) {
	_, root := sessionTestVault(t, nil)
	a := testAgent()
	a.Name = "incoming"
	data, _ := json.Marshal(map[string]any{"session": agent.Session{ID: "s3", Name: "s3"}, "agents": []agent.Agent{a},
		"rules": []agent.UnifiedRule{{Name: "r1", Content: "x"}}})
	in := filepath.Join(root, "in.json")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	withFlags(t, sessionImportCmd, map[string]string{"dry-run": "true"})
	if err := sessionImportCmd.RunE(sessionImportCmd, []string{in}); err != nil {
		t.Fatal(err)
	}
	v := reopenSessionVault(t, root)
	if _, ok := v.Get("incoming"); ok || len(v.Sessions().Sessions) != 0 || len(v.SharedConfig().Rules) != 0 {
		t.Errorf("dry-run wrote to the vault")
	}
}

// Importing the same changed session file again matches the copy the first
// import added; a file naming an agent or rule twice imports the first; an
// import that adds nothing leaves the vault file as it was.
func TestSessionImportRepeatAndDuplicates(t *testing.T) {
	v, root := sessionTestVault(t, nil)
	if err := v.AddSession(agent.Session{ID: "s1", Name: "s1", Description: "here"}); err != nil {
		t.Fatal(err)
	}
	a := testAgent()
	a.Name = "dup"
	b := a
	b.Model = "other"
	data, _ := json.Marshal(map[string]any{"session": agent.Session{ID: "s1", Name: "s1", Description: "there"},
		"agents": []agent.Agent{a, b},
		"rules":  []agent.UnifiedRule{{Name: "r", Content: "1"}, {Name: "r", Content: "2"}}})
	in := filepath.Join(root, "in.json")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := captureStdoutForTest(t, func() error { return sessionImportCmd.RunE(sessionImportCmd, []string{in}) }); err != nil {
			t.Fatal(err)
		}
	}
	run()
	vf := filepath.Join(root, config.AppName, config.VaultFile)
	before, _ := os.ReadFile(vf)
	run()
	after, _ := os.ReadFile(vf)
	if string(before) != string(after) {
		t.Errorf("a second import of the same file rewrote the vault")
	}
	v = reopenSessionVault(t, root)
	if n := len(v.Sessions().Sessions); n != 2 {
		t.Errorf("sessions = %d, want 2 (s1 and s1-imported)", n)
	}
	if got, _ := v.Get("dup"); got.Model != a.Model {
		t.Errorf("agent dup model = %q, want the first", got.Model)
	}
	if r := v.SharedConfig().Rules; len(r) != 1 || r[0].Content != "1" {
		t.Errorf("rules = %+v, want the first r only", r)
	}
}

// A bundle carries no process IDs or running state, as session export.
func TestBundleExportIdlesSessions(t *testing.T) {
	v, root := sessionTestVault(t, nil)
	if err := v.AddSession(agent.Session{ID: "s1", Name: "s1", Status: agent.SessionStatusRunning,
		Agents: []agent.SessionAgent{{Name: "x", PID: 4242}}}); err != nil {
		t.Fatal(err)
	}
	res, err := collectSetupBundle(exportCmd, v, filepath.Join(root, config.AppName), setupCollectOptions{IncludeSessions: true})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Bundle.Sessions.Sessions[0]
	if s.Status != agent.SessionStatusIdle || s.Agents[0].PID != 0 {
		t.Errorf("bundle session = %+v", s)
	}
	if got := v.Sessions().Sessions[0].Agents[0].PID; got != 4242 {
		t.Errorf("export changed the vault's session: pid %d", got)
	}
}
