package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRedactJSONSecrets(t *testing.T) {
	in := []byte(`{
		"model": "opus",
		"apiKeyHelper": "~/bin/key.sh",
		"hooks": {"PreToolUse": [{"hooks": [{"command": "guard.sh"}]}]},
		"mcpServers": {
			"gh": {"command": "gh-mcp", "env": {"GITHUB_TOKEN": "ghp_x", "DEBUG": "1"}},
			"web": {"url": "https://x", "headers": {"Authorization": "Bearer y"}}
		},
		"auth": {"github": {"user": "u", "pass": "p"}},
		"list": [{"token": "t"}],
		"port": 8080
	}`)
	out, ptrs, ok := redactJSONSecrets(in)
	if !ok {
		t.Fatal("not redacted")
	}
	for _, secret := range []string{"ghp_x", "Bearer y", `"p"`, `"t"`, `"u"`} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("secret %s carried:\n%s", secret, out)
		}
	}
	for _, kept := range []string{"opus", "guard.sh", "gh-mcp", "https://x", "8080"} {
		if !bytes.Contains(out, []byte(kept)) {
			t.Errorf("%s lost:\n%s", kept, out)
		}
	}
	want := []string{
		"/apiKeyHelper", "/auth/github/pass", "/auth/github/user", "/list/0/token",
		"/mcpServers/gh/env/DEBUG", "/mcpServers/gh/env/GITHUB_TOKEN", "/mcpServers/web/headers/Authorization",
	}
	if !reflect.DeepEqual(ptrs, want) {
		t.Errorf("pointers = %v\nwant       %v", ptrs, want)
	}
	if _, _, ok := redactJSONSecrets([]byte(`[1,2]`)); ok {
		t.Error("a non-object was field-redacted; it must fall back to whole-file redaction")
	}
}

// Blanked values come back from the file already on this machine; ones it
// does not have are left out, never written as "".
func TestRestoreRedactedFields(t *testing.T) {
	incoming := []byte(`{"model":"opus","env":{"A_TOKEN":"","OTHER":""}}`)
	existing := []byte(`{"model":"sonnet","env":{"A_TOKEN":"local-secret"}}`)
	out, restored, err := restoreRedactedFields(incoming, []string{"/env/A_TOKEN", "/env/OTHER"}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 1 {
		t.Errorf("restored = %d, want 1", restored)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if env["A_TOKEN"] != "local-secret" {
		t.Errorf("A_TOKEN = %v", env["A_TOKEN"])
	}
	if _, ok := env["OTHER"]; ok {
		t.Errorf("an unknown secret was written as an empty value: %s", out)
	}
	if doc["model"] != "opus" {
		t.Errorf("non-secret field not taken from the bundle: %v", doc["model"])
	}
}

// End to end: settings.json exported without secrets arrives with its hooks,
// and the token already on the destination survives the import.
func TestSettingsTravelWithoutSecretsAndKeepLocalToken(t *testing.T) {
	src := t.TempDir()
	setTestHomeDir(t, src)
	mustWriteFile(t, filepath.Join(src, ".claude", "settings.json"),
		`{"hooks":{"Stop":[{"hooks":[{"command":"notify"}]}]},"env":{"ANTHROPIC_API_KEY":"src-key"}}`)
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "settings.json")
	if a == nil || bytes.Contains(a.Content, []byte("src-key")) {
		t.Fatalf("settings asset = %+v", a)
	}

	dst := t.TempDir()
	mustWriteFile(t, filepath.Join(dst, ".claude", "settings.json"), `{"env":{"ANTHROPIC_API_KEY":"dst-key"}}`)
	if _, warnings, err := applyProviderAssetsToSystem(dst, []SetupAsset{*a}); err != nil {
		t.Fatal(err, warnings)
	}
	got, _ := os.ReadFile(filepath.Join(dst, ".claude", "settings.json"))
	if !bytes.Contains(got, []byte("notify")) || !bytes.Contains(got, []byte("dst-key")) || bytes.Contains(got, []byte("src-key")) {
		t.Errorf("applied settings = %s", got)
	}
}
