package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
)

func exportPlainForTest(t *testing.T, root string, flags map[string]string) (string, error) {
	t.Helper()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	seedProfileVault(t, filepath.Join(root, config.AppName), []agent.Agent{{
		Name: "alpha", Provider: agent.ProviderClaude, APIKey: "sk-ant-local-key",
		MCPServers: []agent.MCPServer{{Name: "gh", Command: "gh-mcp", Env: map[string]string{"GITHUB_TOKEN": "ghp-canary-value"}}},
	}}, nil)
	path := filepath.Join(root, "out.json")
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	all := map[string]string{"yes": "true", "plain": "true", "confirm": "true",
		"include-provider-files": "false", "include-skills": "false", "detect": "false"}
	for k, v := range flags {
		all[k] = v
	}
	withFlags(t, exportCmd, all)
	return path, runExport(exportCmd, []string{path})
}

// 0.14.0 and earlier take a bundle by its top-level keys and would write a
// blanked bundle's blanks over local values. A bundle exported without
// secrets must carry none of the keys they recognise.
func TestExportWithoutSecretsIsNotReadableByOlderReleases(t *testing.T) {
	path, err := exportPlainForTest(t, t.TempDir(), map[string]string{"include-secrets": "false"})
	if err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, path)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema_version", "install_guide", "shared_config", "workflow_templates", "provider_files", "project_files", "agents", "shared"} {
		if _, ok := top[k]; ok {
			t.Errorf("blanked bundle has top-level %q, which 0.14.0 reads as a bundle", k)
		}
	}
	if string(top[blankedSchemaKey]) != `"`+BlankedBundleSchemaVersion+`"` {
		t.Errorf("%s = %s, want %q", blankedSchemaKey, top[blankedSchemaKey], BlankedBundleSchemaVersion)
	}
	b, format, err := decodePortableBundle(raw)
	if err != nil || format != bundleFormatPortable || b.SchemaVersion != BlankedBundleSchemaVersion || len(b.Profiles) != 1 {
		t.Fatalf("decode = %v %v %q %d", err, format, b.SchemaVersion, len(b.Profiles))
	}
	if strings.Contains(string(raw), "ghp-canary-value") {
		t.Error("blanked bundle carries the MCP env secret")
	}
}

func TestExportWithSecretsKeepsSchemaVersion(t *testing.T) {
	path, err := exportPlainForTest(t, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, path)
	if !strings.Contains(string(raw), `"schema_version": "`+PortableBundleSchemaVersion+`"`) || strings.Contains(string(raw), blankedSchemaKey) {
		t.Errorf("bundle with secrets: %.200s", raw)
	}
}

// --vault-only writes the vault as it is. Asked to leave keys or secrets
// out, it wrote them anyway, in plain text and without the confirmation a
// secret-bearing plain export needs.
func TestVaultOnlyExportRefusesToLeaveOutSecrets(t *testing.T) {
	for _, flag := range []string{"include-secrets", "include-keys"} {
		t.Run(flag, func(t *testing.T) {
			path, err := exportPlainForTest(t, t.TempDir(), map[string]string{"vault-only": "true", flag: "false"})
			if err == nil || !strings.Contains(err.Error(), "--vault-only") {
				t.Errorf("err = %v, want a --vault-only refusal", err)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("vault-only export written: %s", mustRead(t, path))
			}
		})
	}
}

// agent import --merge filled a blank key from the local agent even when
// the profile sends it to another endpoint.
func TestAgentProfileMergeKeepsKeyOnSameEndpointOnly(t *testing.T) {
	existing := testAgent()
	existing.APIKey = "existing-key"
	existing.BaseURL = "https://api.example"
	imported := testAgent()
	imported.BaseURL = "https://evil.example"
	got, left := prepareAgentProfileMerge(existing, imported, existing.UpdatedAt)
	if got.APIKey != "" || strings.Join(left, ",") != "api_key" {
		t.Errorf("moved endpoint: key %q, left %v", got.APIKey, left)
	}
	imported.BaseURL = existing.BaseURL
	got, left = prepareAgentProfileMerge(existing, imported, existing.UpdatedAt)
	if got.APIKey != "existing-key" || len(left) != 0 {
		t.Errorf("same endpoint: key %q, left %v", got.APIKey, left)
	}
}

// agent export without --include-key blanked only the API key: MCP server
// env, secret extras and a base URL with credentials went out as they were.
// The blanks come back on --merge into the same agent.
func TestAgentProfileExportBlanksSecretsAndMergeFillsThem(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	a := testAgent()
	a.APIKey = "sk-ant-local-key"
	a.BaseURL = "https://user:pw-canary@proxy.example"
	a.ProviderMeta.Extra = map[string]string{"api_token": "extra-canary", "region": "us"}
	a.MCPServers = []agent.MCPServer{{Name: "gh", Command: "gh-mcp", Args: []string{"--token", "arg-canary"}, Env: map[string]string{"GITHUB_TOKEN": "env-canary"}}}
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)

	path := filepath.Join(root, "profile.json")
	if err := agentExportCmd.RunE(agentExportCmd, []string{a.Name, path}); err != nil {
		t.Fatal(err)
	}
	raw := string(mustRead(t, path))
	for _, s := range []string{"sk-ant-local-key", "pw-canary", "extra-canary", "arg-canary", "env-canary"} {
		if strings.Contains(raw, s) {
			t.Errorf("profile exported without --include-key carries %q", s)
		}
	}
	withFlags(t, agentImportCmd, map[string]string{"merge": "true"})
	if err := agentImportCmd.RunE(agentImportCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	v, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := v.Get(a.Name)
	if got.APIKey != a.APIKey || got.BaseURL != a.BaseURL || got.ProviderMeta.Extra["api_token"] != "extra-canary" ||
		got.MCPServers[0].Args[1] != "arg-canary" || got.MCPServers[0].Env["GITHUB_TOKEN"] != "env-canary" {
		t.Errorf("merge did not fill the blanks back: %s", mustJSON(t, got))
	}
}

// A provider file exported without its content cannot be applied, and the
// Claude config's copy of it must not replace this machine's file instead:
// on the exporting machine it may be older than the file.
func TestProviderAssetsToApplyKeepsLocalFileTheBundleWithheld(t *testing.T) {
	home := t.TempDir()
	setup := SetupBundle{
		ProviderConfigs: agent.ProviderConfig{Claude: &agent.ClaudeConfig{CustomSettings: map[string]any{"model": "opus"}}},
		ProviderFiles:   []SetupAsset{{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json", Redacted: true, Sensitive: true}},
	}
	fromConfig := func() int {
		assets, _ := providerAssetsToApply(setup, home, false)
		n := 0
		for _, a := range assets {
			if a.LogicalPath == "settings.json" && !a.Redacted {
				n++
			}
		}
		return n
	}
	if n := fromConfig(); n != 1 {
		t.Errorf("no local settings.json: %d config assets, want 1", n)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"apiKeyHelper":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := fromConfig(); n != 0 {
		t.Errorf("local settings.json and withheld bundle copy: %d config assets, want 0", n)
	}
	setup.ProviderFiles[0].Redacted = false
	if n := fromConfig(); n != 2 {
		t.Errorf("bundle carries the file: %d assets, want config and file", n)
	}
}

func captureStdoutForTest(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.Bytes()
	}()
	runErr := fn()
	os.Stdout = old
	_ = w.Close()
	return string(<-done), runErr
}

// setup export blanks secrets by default, and its 1.x layout is what 0.14.0
// reads as a setup bundle. Without --include-secrets it writes a blanked
// portable bundle, which setup import and setup show read.
func TestSetupExportWithoutSecretsWritesBlankedBundle(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{{Name: "alpha", Provider: agent.ProviderClaude,
		MCPServers: []agent.MCPServer{{Name: "gh", Command: "gh-mcp", Env: map[string]string{"GITHUB_TOKEN": "ghp-canary-value"}}}}}, nil)
	useConfigDir(t, dir)
	for _, secrets := range []bool{false, true} {
		path := filepath.Join(root, "setup.json")
		withFlags(t, setupExportCmd, map[string]string{"include-secrets": map[bool]string{false: "false", true: "true"}[secrets], "confirm": "true"})
		if _, err := captureStdoutForTest(t, func() error { return runSetupExport(setupExportCmd, []string{path}) }); err != nil {
			t.Fatal(err)
		}
		raw := mustRead(t, path)
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		_, blanked := top[blankedSchemaKey]
		_, v1 := top["shared_config"]
		if blanked == secrets || v1 != secrets {
			t.Errorf("include-secrets=%v: blanked %v, 1.x layout %v", secrets, blanked, v1)
		}
		out, err := captureStdoutForTest(t, func() error { return runSetupShow(setupShowCmd, []string{path}) })
		if err != nil || !strings.Contains(out, "Agents (1):") {
			t.Errorf("include-secrets=%v: setup show = %v\n%s", secrets, err, out)
		}
	}
}

// A bundle of an unknown schema version, or with both version keys
// disagreeing, is refused instead of applied with rules it may not follow.
func TestDecodePortableBundleRefusesUnknownOrConflictingVersion(t *testing.T) {
	prof := `"profiles":[{"name":"default","setup":{"version":"1.0"}}]`
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"schema_version":"2.0",` + prof + `}`, true},
		{`{"schema_version":"2.1",` + prof + `}`, true},
		{`{"blanked_schema_version":"2.1",` + prof + `}`, true},
		{`{"blanked_schema_version":"2.1","schema_version":"2.1",` + prof + `}`, true},
		{`{"schema_version":"3.0",` + prof + `}`, false},
		{`{"blanked_schema_version":"2.2",` + prof + `}`, false},
		{`{"schema_version":"",` + prof + `}`, false},
		{`{"blanked_schema_version":"2.1","schema_version":"2.0",` + prof + `}`, false},
	} {
		_, _, err := decodePortableBundle([]byte(tc.body))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.body, err, tc.ok)
		}
	}
}
