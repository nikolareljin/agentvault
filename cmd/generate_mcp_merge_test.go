package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The default output is Claude Desktop's own config. Its other settings must
// survive a regeneration of mcpServers.
func TestMergeMCPServersKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(`{"globalShortcut":"Ctrl+Space","mcpServers":{"old":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{"new":{"command":"y"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["globalShortcut"]) != `"Ctrl+Space"` {
		t.Fatalf("other key lost: %s", out)
	}
	var servers struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers.MCPServers["new"]; !ok || len(servers.MCPServers) != 1 {
		t.Fatalf("mcpServers = %v, want only the generated set", servers.MCPServers)
	}
}

func TestMergeMCPServersRefusesNonObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{}}`)); err == nil {
		t.Fatal("overwrote a non-JSON file")
	}
}

func TestMergeMCPServersNewFile(t *testing.T) {
	gen := []byte(`{"mcpServers":{}}`)
	out, err := mergeMCPServersIntoFile(filepath.Join(t.TempDir(), "absent.json"), gen)
	if err != nil || string(out) != string(gen) {
		t.Fatalf("got %s, %v", out, err)
	}
}
