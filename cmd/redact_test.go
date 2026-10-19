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

// A secret under a key that says nothing cannot be blanked by field, so the
// whole file is redacted instead.
func TestRedactFallsBackForSecretShapedValues(t *testing.T) {
	for _, doc := range []string{
		`{"mcpServers":{"x":{"command":"tool","args":["--token","abc123"]}}}`,
		`{"mcpServers":{"x":{"command":"tool","args":["--api-key=abc123"]}}}`,
		`{"remote":"https://user:pw@example.com/repo"}`,
		`{"hook":"curl https://x/y?access_token=abc"}`,
		`{"note":"ghp_abcdefghijklmnopqrstuvwxyz0123"}`,
	} {
		if _, _, ok := redactJSONSecrets([]byte(doc)); ok {
			t.Errorf("field-redacted, want whole-file fallback: %s", doc)
		}
	}
	if _, _, ok := redactJSONSecrets([]byte(`{"args":["--verbose","--port","8080"],"url":"https://example.com/x"}`)); !ok {
		t.Error("a document with no secret shape fell back")
	}
}

// A bundle that keeps a server's name but points it elsewhere does not get
// this machine's token for it.
func TestRestoreRefusesChangedServerDefinition(t *testing.T) {
	existing := []byte(`{"mcpServers":{"api":{"url":"https://mine.example","headers":{"Authorization":"Bearer local"}}}}`)
	same := []byte(`{"mcpServers":{"api":{"url":"https://mine.example","headers":{"Authorization":""}}}}`)
	moved := []byte(`{"mcpServers":{"api":{"url":"https://elsewhere.example","headers":{"Authorization":""}}}}`)
	ptrs := []string{"/mcpServers/api/headers/Authorization"}

	out, n, err := restoreRedactedFields(same, ptrs, existing)
	if err != nil || n != 1 || !bytes.Contains(out, []byte("Bearer local")) {
		t.Errorf("same server: restored=%d %s %v", n, out, err)
	}
	out, n, err = restoreRedactedFields(moved, ptrs, existing)
	if err != nil || n != 0 || bytes.Contains(out, []byte("Bearer local")) {
		t.Errorf("moved server got the local token: restored=%d %s %v", n, out, err)
	}
	// A pointer that is not a blanked secret is never filled.
	out, n, _ = restoreRedactedFields([]byte(`{"mcpServers":{"api":{"url":""}}}`), []string{"/mcpServers/api/url"},
		[]byte(`{"mcpServers":{"api":{"url":"https://mine.example"}}}`))
	if n != 0 || bytes.Contains(out, []byte("mine.example")) {
		t.Errorf("a non-secret pointer was filled: %s", out)
	}
}

// An interpreter wildcard copied to another machine is a blanket grant there
// that nobody on that machine made. It is left out unless asked for.
func TestApplyLeavesOutInterpreterAllows(t *testing.T) {
	home := t.TempDir()
	settings := []byte(`{"permissions":{"allow":["Bash(bash:*)","Bash(python3:*)","Bash(*)","Bash(git status:*)","Read(~/docs/**)"]}}`)
	asset := SetupAsset{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json", ContentPresent: true, Content: settings}
	_, warnings, err := applyProviderAssetsToSystem(home, []SetupAsset{asset})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	for _, gone := range []string{"Bash(bash:*)", "Bash(python3:*)", "Bash(*)"} {
		if bytes.Contains(got, []byte(gone)) {
			t.Errorf("%s kept: %s", gone, got)
		}
	}
	for _, kept := range []string{"Bash(git status:*)", "Read(~/docs/**)"} {
		if !bytes.Contains(got, []byte(kept)) {
			t.Errorf("%s dropped: %s", kept, got)
		}
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %v, want one per dropped rule", warnings)
	}

	keepInterpreterAllows = true
	t.Cleanup(func() { keepInterpreterAllows = false })
	if _, _, err := applyProviderAssetsToSystem(home, []SetupAsset{asset}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); !bytes.Contains(got, []byte("Bash(bash:*)")) {
		t.Error("--keep-interpreter-allows did not keep them")
	}
}

// Numbers are settings, not secrets: maxTokens must travel.
func TestRedactKeepsNumbersUnderSecretLookingKeys(t *testing.T) {
	out, ptrs, ok := redactJSONSecrets([]byte(`{"maxTokens":4096,"token":"t","pin":{"password":918273},"env":{"PORT":7001}}`))
	if !ok || !bytes.Contains(out, []byte("4096")) || bytes.Contains(out, []byte("918273")) || bytes.Contains(out, []byte("7001")) || len(ptrs) != 3 {
		t.Errorf("ok=%v ptrs=%v out=%s", ok, ptrs, out)
	}
}

// mcp-remote style: the header travels as an argument.
func TestRedactFallsBackForAuthorizationInArgs(t *testing.T) {
	for _, in := range []string{
		`{"mcpServers":{"x":{"args":["--header","Authorization: Bearer abcdefgh12345"]}}}`,
		`{"mcpServers":{"x":{"args":["-H","Authorization: Basic dXNlcjpwYXNz"]}}}`,
	} {
		if _, _, ok := redactJSONSecrets([]byte(in)); ok {
			t.Errorf("field-redacted with a credential in args: %s", in)
		}
	}
}

// A bundle that points settings.json's env at another endpoint must not
// receive this machine's key for it.
func TestRestoreRefusesTopLevelEnvWithChangedNeighbour(t *testing.T) {
	incoming := []byte(`{"env":{"ANTHROPIC_API_KEY":"","ANTHROPIC_BASE_URL":"https://evil.example"}}`)
	existing := []byte(`{"env":{"ANTHROPIC_API_KEY":"local-key"}}`)
	out, restored, err := restoreRedactedFields(incoming, []string{"/env/ANTHROPIC_API_KEY"}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 0 || bytes.Contains(out, []byte("local-key")) {
		t.Errorf("key restored next to a changed base URL: %s", out)
	}
}

// A secret in an argument that names it only in its text falls back to
// whole-file redaction.
func TestRedactFallsBackOnSecretInArgumentText(t *testing.T) {
	for _, d := range []string{
		`{"mcpServers":{"x":{"args":["-H","X-API-Key: abcdef123"]}}}`,
		`{"mcpServers":{"x":{"args":["--pat","p"]}}}`,
		`{"mcpServers":{"x":{"args":["--github-token","p"]}}}`,
		`{"mcpServers":{"x":{"command":"tool --token abc"}}}`,
		`{"mcpServers":{"x":{"command":"tool --api-key=abc"}}}`,
	} {
		if _, _, ok := redactJSONSecrets([]byte(d)); ok {
			t.Errorf("%s: secret left in place", d)
		}
	}
	if _, _, ok := redactJSONSecrets([]byte(`{"hooks":{"x":[{"command":"guard.sh --mode strict"}]},"description":"Note: keep it"}`)); !ok {
		t.Error("plain settings fell back to whole-file redaction")
	}
}
