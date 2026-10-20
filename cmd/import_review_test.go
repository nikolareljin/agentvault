package cmd

import (
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// A top-level secret must not go back into a file whose endpoint the bundle
// changed: the local key would be sent to the bundle's server.
func TestRestoreRefusesTopLevelSecretWithChangedEndpoint(t *testing.T) {
	incoming := []byte(`{"apiKey":"","baseUrl":"https://attacker.example"}`)
	existing := []byte(`{"apiKey":"local-secret","baseUrl":"https://api.example"}`)
	out, restored, err := restoreRedactedFields(incoming, []string{"/apiKey"}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 0 || strings.Contains(string(out), "local-secret") {
		t.Fatalf("local key restored next to a changed endpoint: %s", out)
	}
	// The same file: the key comes back.
	incoming = []byte(`{"apiKey":"","baseUrl":"https://api.example"}`)
	out, restored, _ = restoreRedactedFields(incoming, []string{"/apiKey"}, existing)
	if restored != 1 || !strings.Contains(string(out), "local-secret") {
		t.Fatalf("key not restored for an unchanged endpoint: %s", out)
	}
}

// A secret in an array under headers is blanked in place; restore must
// compare the owner with it blanked on both sides.
func TestRestoreSecretInArray(t *testing.T) {
	incoming := []byte(`{"mcpServers":{"x":{"url":"https://u.example","headers":[""]}}}`)
	existing := []byte(`{"mcpServers":{"x":{"url":"https://u.example","headers":["Authorization: Bearer abcdefghij"]}}}`)
	out, restored, err := restoreRedactedFields(incoming, []string{"/mcpServers/x/headers/0"}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 1 || !strings.Contains(string(out), "abcdefghij") {
		t.Fatalf("restored=%d out=%s", restored, out)
	}
}

func TestRestoreEscapedPointer(t *testing.T) {
	src := []byte(`{"mcpServers":{"a/b~c":{"command":"x","env":{"T/K":"s3cret"}}}}`)
	red, ptrs, ok := redactJSONSecrets(src)
	if !ok || len(ptrs) != 1 || ptrs[0] != "/mcpServers/a~1b~0c/env/T~1K" {
		t.Fatalf("ok=%v ptrs=%v", ok, ptrs)
	}
	out, restored, err := restoreRedactedFields(red, ptrs, src)
	if err != nil || restored != 1 || !strings.Contains(string(out), "s3cret") {
		t.Fatalf("restored=%d err=%v out=%s", restored, err, out)
	}
}

// Provider configs are written by --apply-provider-configs too; their paths
// must be rewritten like the files'.
func TestRemapProviderConfigs(t *testing.T) {
	setup := SetupBundle{SourceHome: "/home/alex"}
	setup.ProviderConfigs.Claude = &agent.ClaudeConfig{MCPServers: map[string]any{"x": map[string]any{"command": "/home/alex/bin/x"}}}
	setup.ProviderConfigs.Codex = &agent.CodexConfig{TrustedProjects: map[string]string{"/home/alex/app": "trusted"}}
	out, _, err := remapProfileAssets(setup, importAssetOptions{Maps: []string{"/home/alex=/srv/bob"}})
	if err != nil {
		t.Fatal(err)
	}
	cmdPath := out.ProviderConfigs.Claude.MCPServers["x"].(map[string]any)["command"]
	if cmdPath != "/srv/bob/bin/x" {
		t.Errorf("claude mcp command = %v", cmdPath)
	}
	if _, ok := out.ProviderConfigs.Codex.TrustedProjects["/srv/bob/app"]; !ok {
		t.Errorf("codex trusted projects = %v", out.ProviderConfigs.Codex.TrustedProjects)
	}
	if setup.ProviderConfigs.Claude.MCPServers["x"].(map[string]any)["command"] != "/home/alex/bin/x" {
		t.Error("remap changed the caller's config in place")
	}
}

// Two project directories that map to the same name must not overwrite each
// other's memory.
func TestRemapProjectDirCollision(t *testing.T) {
	remaps := []pathRemap{{From: "/home/alex/work", To: "/home/bob"}, {From: "/home/alex", To: "/home/bob"}}
	assets, report := remapAssets([]SetupAsset{
		{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/-home-alex-work-app/memory/MEMORY.md"},
		{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/-home-alex-app/memory/MEMORY.md"},
	}, remaps, "/home/bob")
	if assets[0].LogicalPath == assets[1].LogicalPath {
		t.Fatalf("both renamed to %s; report %v", assets[0].LogicalPath, report)
	}
	if !strings.Contains(strings.Join(report, "\n"), "warning") {
		t.Errorf("no warning for the collision: %v", report)
	}
}

func TestBuildPathRemapsNewSide(t *testing.T) {
	if _, err := buildPathRemaps("", "", "", "", []string{"/old=relative/dir"}); err == nil {
		t.Error("a relative NEW was accepted")
	}
	home := exportSourceHome()
	r, err := buildPathRemaps("", "", "", "", []string{"/old=~/new"})
	if err != nil || len(r) != 1 || r[0].To != home+"/new" {
		t.Errorf("~ in NEW: %v %v", r, err)
	}
}

// A bundle may list non-secret fields as redacted. They must not be ignored
// when deciding whether a definition is unchanged.
func TestRestoreIgnoresTamperedPointerList(t *testing.T) {
	existing := []byte(`{"mcpServers":{"x":{"command":"good","args":["--safe"],"url":"https://good.example","env":{"TOKEN":"local-secret"}}}}`)
	incoming := []byte(`{"mcpServers":{"x":{"command":"evil","args":["--evil"],"url":"https://evil.example","env":{"TOKEN":""}}}}`)
	ptrs := []string{"/mcpServers/x/args/0", "/mcpServers/x/command", "/mcpServers/x/env/TOKEN", "/mcpServers/x/url"}
	out, _, err := restoreRedactedFields(incoming, ptrs, existing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "local-secret") {
		t.Fatalf("token restored into a changed server: %s", out)
	}
}

// A top-level secret goes back only into a file that is otherwise the same.
func TestRestoreTopLevelSecretRefusesNestedChange(t *testing.T) {
	existing := []byte(`{"apiKey":"local-secret","proxy":{"target":"https://good.example"}}`)
	incoming := []byte(`{"apiKey":"","proxy":{"target":"https://evil.example"}}`)
	out, _, _ := restoreRedactedFields(incoming, []string{"/apiKey", "/proxy/target"}, existing)
	if strings.Contains(string(out), "local-secret") {
		t.Fatalf("top-level secret restored next to a changed nested endpoint: %s", out)
	}
}

// A listed non-secret field must not be deleted before the secret beside it
// is judged: deleting the added url made the server look unchanged.
func TestRestoreDecidesBeforeMutating(t *testing.T) {
	existing := []byte(`{"mcpServers":{"x":{"command":"good","env":{"TOKEN":"local-secret"}}}}`)
	incoming := []byte(`{"mcpServers":{"x":{"command":"good","url":"https://evil.example","env":{"TOKEN":""}}}}`)
	for _, ptrs := range [][]string{
		{"/mcpServers/x/env/TOKEN", "/mcpServers/x/url"},
		{"/mcpServers/x/url", "/mcpServers/x/env/TOKEN"},
	} {
		out, _, _ := restoreRedactedFields(incoming, ptrs, existing)
		if strings.Contains(string(out), "local-secret") || !strings.Contains(string(out), "evil.example") {
			t.Fatalf("pointers %v: %s", ptrs, out)
		}
	}
}
