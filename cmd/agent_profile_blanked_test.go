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

// A profile with blanked secrets keeps its agent out of the "agent" key, so
// 0.14.0 finds no agent and refuses it instead of writing the blanks over
// this machine's values on --merge. A profile with nothing blanked keeps
// the 1.0 layout.
func TestAgentProfileBlankedLayout(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	secret := testAgent()
	secret.Name = "secret"
	secret.ProviderMeta.Extra = map[string]string{"api_token": "extra-canary"}
	plain := testAgent()
	plain.Name = "plain"
	plain.ProviderMeta.Extra = map[string]string{"region": "us"}
	seedProfileVault(t, dir, []agent.Agent{secret, plain}, nil)
	useConfigDir(t, dir)

	keys := func(name string) map[string]json.RawMessage {
		path := filepath.Join(root, name+".json")
		stdout, err := captureStdoutForTest(t, func() error { return agentExportCmd.RunE(agentExportCmd, []string{name, path}) })
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(mustRead(t, path), &m); err != nil {
			t.Fatal(err)
		}
		want := "schema version 1.0"
		if _, ok := m["blanked_agent"]; ok {
			want = "schema version 1.1"
		}
		if !strings.Contains(stdout, want) {
			t.Errorf("export of %s says %q, want %q", name, stdout, want)
		}
		return m
	}
	m := keys("secret")
	if _, ok := m["agent"]; ok {
		t.Errorf("blanked profile has an \"agent\" key 0.14.0 would import: %v", m)
	}
	if string(m["blanked_schema_version"]) != `"1.1"` || m["blanked_agent"] == nil {
		t.Errorf("blanked profile layout: %v", m)
	}
	m = keys("plain")
	if string(m["schema_version"]) != `"1.0"` || m["agent"] == nil || m["blanked_agent"] != nil {
		t.Errorf("plain profile layout: %v", m)
	}
}

// On a machine without the agent, the blanked base URL and extras are
// named, and a blank extra is dropped rather than stored as "".
func TestAgentProfileImportNamesBlanksOnFreshMachine(t *testing.T) {
	a := testAgent()
	a.BaseURL = ""
	a.ProviderMeta.Extra = map[string]string{"endpoint": "", "region": "us"}
	data, err := marshalBlankedAgentProfile(a, []string{"base_url", "provider_meta.extra.endpoint"}, "json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, nil, nil)
	useConfigDir(t, dir)
	path := filepath.Join(root, "p.json")
	mustWriteFile(t, path, string(data))
	stderr := captureStderrForTest(t, func() error {
		return agentImportCmd.RunE(agentImportCmd, []string{path})
	})
	for _, f := range []string{"base_url", "provider_meta.extra.endpoint"} {
		if !strings.Contains(stderr, "agent "+a.Name+" "+f+":") {
			t.Errorf("%s not named: %q", f, stderr)
		}
	}
	v, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := v.Get(a.Name)
	if _, ok := got.ProviderMeta.Extra["endpoint"]; ok || got.ProviderMeta.Extra["region"] != "us" {
		t.Errorf("extras: %v", got.ProviderMeta.Extra)
	}
}

func TestDecodeAgentProfileRefusesEditedOrUnknownBlanked(t *testing.T) {
	for name, raw := range map[string]string{
		"both agents":     `{"blanked_schema_version":"1.1","agent":{"name":"a","provider":"claude"},"blanked_agent":{"name":"a","provider":"claude"}}`,
		"both versions":   `{"schema_version":"1.0","blanked_schema_version":"1.1","blanked_agent":{"name":"a","provider":"claude"}}`,
		"no agent":        `{"blanked_schema_version":"1.1"}`,
		"blanks on plain": `{"schema_version":"1.0","agent":{"name":"a","provider":"claude"},"blanks":["base_url"]}`,
		"unknown version": `{"blanked_schema_version":"1.9","blanked_agent":{"name":"a","provider":"claude"}}`,
		"unknown plain":   `{"schema_version":"2.0","agent":{"name":"a","provider":"claude"}}`,
	} {
		if _, _, _, err := decodeAgentProfile([]byte(raw), "json"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	a, ver, blanks, err := decodeAgentProfile([]byte(`{"blanked_schema_version":"1.1","blanked_agent":{"name":"a","provider":"claude"},"blanks":["base_url"]}`), "")
	if err != nil || a.Name != "a" || ver != agentProfileSchemaVersion || len(blanks) != 1 {
		t.Errorf("blanked profile: %v %q %v %v", a, ver, blanks, err)
	}
}

func captureStderrForTest(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.Bytes()
	}()
	runErr := fn()
	os.Stderr = old
	_ = w.Close()
	out := string(<-done)
	if runErr != nil {
		t.Fatal(runErr)
	}
	return out
}

// Blanks come from the profile: only fields export can blank are read.
func TestDecodeAgentProfileRefusesForeignBlanks(t *testing.T) {
	for _, f := range []string{"api_key", "provider_meta.extra.\x1b]0;x\a", "provider_meta.extra.", "\x1b[2Jbase_url"} {
		raw, _ := json.Marshal(map[string]any{"blanked_schema_version": "1.1",
			"blanked_agent": map[string]string{"name": "a", "provider": "claude"}, "blanks": []string{f}})
		if _, _, _, err := decodeAgentProfile(raw, "json"); err == nil {
			t.Errorf("blank %q accepted", f)
		}
	}
}

// Agent names and fields from a profile or bundle reach the terminal
// without control characters.
func TestImportReportsPrintNoEscapes(t *testing.T) {
	a := testAgent()
	a.Name = "evil"
	a.ProviderMeta.Extra = map[string]string{"api_token\x1b[2J": ""}
	data, err := marshalBlankedAgentProfile(a, nil, "json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, nil, nil)
	useConfigDir(t, dir)
	path := filepath.Join(root, "p.json")
	mustWriteFile(t, path, string(data))
	stderr := captureStderrForTest(t, func() error {
		return agentImportCmd.RunE(agentImportCmd, []string{path})
	})
	if !strings.Contains(stderr, "agent evil provider_meta.extra.api_token?[2J") {
		t.Errorf("no sanitized Left out line: %q", stderr)
	}
	if strings.ContainsAny(stderr, "\x1b\a") {
		t.Errorf("agent import printed control characters: %q", stderr)
	}

	var rep importReport
	plan := profileImportPlan{}
	reportVaultBlanks(&plan, []VaultBlank{{Section: blankRouter, Field: "x\x1b[2J"}}, agent.ProviderConfig{}, true, &rep)
	rep.record("Left out", "agent %s %s", "n\x1b]0;t\a", "f\x9b2J")
	for _, l := range rep.Lines {
		if strings.ContainsAny(l, "\x1b\a\u009b") {
			t.Errorf("report line has control characters: %q", l)
		}
	}
}

// setup show prints another machine's names: no control characters.
func TestSetupShowPrintsNoEscapes(t *testing.T) {
	b := SetupBundle{SourceMachine: "m\x1b]0;x\a", Agents: []agent.Agent{{Name: "a\x1b[2J", Provider: "claude\x9b"}},
		SharedConfig: agent.SharedConfig{MCPServers: []agent.MCPServer{{Name: "s\x1b[1m", Command: "c\x07"}}}}
	out, _ := captureStdoutForTest(t, func() error { printSetupBundle(b); return nil })
	if strings.ContainsAny(out, "\x1b\a\u009b") {
		t.Errorf("setup show printed control characters: %q", out)
	}
}

// A name with control characters is refused: list prints names raw.
func TestImportRefusesControlCharacterNames(t *testing.T) {
	a := testAgent()
	a.Name = "ev\x1b[2Jil"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Errorf("Validate() = %v, want a refusal", err)
	}
	a.Name = "evil"
	a.MCPServers = []agent.MCPServer{{Name: "m\x9b2J", Command: "x"}}
	if err := a.Validate(); err == nil {
		t.Error("Validate() accepted an MCP server name with control characters")
	}
	for _, p := range []PortableProfile{
		{Name: "p\x1b]0;t\a"},
		{Name: "p", Setup: SetupBundle{Agents: []agent.Agent{{Name: "a\r"}}}},
		{Name: "p", Setup: SetupBundle{SharedConfig: agent.SharedConfig{MCPServers: []agent.MCPServer{{Name: "s\x1b"}}}}},
		{Name: "p", Setup: SetupBundle{SharedConfig: agent.SharedConfig{Instructions: []agent.InstructionFile{{Name: "i", Filename: "f\x07"}}}}},
	} {
		if _, bad := controlCharName(p); !bad {
			t.Errorf("controlCharName(%+v) = clean", p)
		}
	}
	if _, bad := controlCharName(PortableProfile{Name: "p", Setup: SetupBundle{Agents: []agent.Agent{{Name: "caf\u00e9"}}}}); bad {
		t.Error("controlCharName() refused a plain non-ASCII name")
	}
}

// Every blank export lists is one import reads: a long extra key once
// made a profile its own import refused.
func TestDecodeAgentProfileReadsEveryBlankExportWrites(t *testing.T) {
	a := testAgent()
	long := "api_token_" + strings.Repeat("k", 300)
	a.BaseURL = "https://u:pw@proxy.example"
	a.ProviderMeta.Extra = map[string]string{long: "secret-value", "region": "us"}
	b := SetupBundle{Agents: []agent.Agent{a}}
	redactVaultSecrets(&b)
	var blanks []string
	for _, vb := range b.VaultBlanks {
		blanks = append(blanks, vb.Field)
	}
	if len(blanks) != 2 {
		t.Fatalf("blanks = %v, want base_url and the extra", blanks)
	}
	data, err := marshalBlankedAgentProfile(b.Agents[0], blanks, "json")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, got, err := decodeAgentProfile(data, ""); err != nil || len(got) != 2 {
		t.Fatalf("decodeAgentProfile() = %v, %v; want both blanks read", got, err)
	}
	// A listed blank whose field is not blank in the profile was edited.
	b.Agents[0].ProviderMeta.Extra[long] = "x"
	data, _ = marshalBlankedAgentProfile(b.Agents[0], blanks, "json")
	if _, _, _, err := decodeAgentProfile(data, ""); err == nil {
		t.Fatal("listed blank with a value accepted")
	}
}

// An agent saved before control characters were refused stays editable.
func TestEditKeepsAgentWithControlCharacterName(t *testing.T) {
	a := testAgent()
	a.Name = "old\x1b[1m"
	a.MCPServers = []agent.MCPServer{{Name: "m\x07", Command: "x"}}
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, editCmd, map[string]string{"model": "m2"})
	if _, err := captureStdoutForTest(t, func() error {
		return editCmd.RunE(editCmd, []string{a.Name})
	}); err != nil {
		t.Fatalf("edit refused an existing agent: %v", err)
	}
	v, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.Get(a.Name); got.Model != "m2" {
		t.Errorf("model = %q, want m2", got.Model)
	}
}

// list prints fields a bundle sets, such as model and tags, without
// control characters.
func TestListPrintsNoEscapes(t *testing.T) {
	var b strings.Builder
	if err := printAgentTable(&b, []agent.Agent{{Name: "n\x1b[1m", Provider: "claude", Model: "m\x1b]0;t\a", Tags: []string{"t\x9b2J"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\x1b\a\u009b") {
		t.Errorf("list printed control characters: %q", b.String())
	}
	if !strings.Contains(b.String(), "n?[1m") {
		t.Errorf("name missing: %q", b.String())
	}
}

// An agent profile's MCP server commands run in the agent: new or changed
// ones are left out unless --keep-bundle-commands.
func TestAgentProfileImportLeavesOutMCPCommands(t *testing.T) {
	local := testAgent()
	local.MCPServers = []agent.MCPServer{{Name: "fs", Command: "npx"}}
	in := local
	in.MCPServers = []agent.MCPServer{{Name: "fs", Command: "evil"}, {Name: "new", Command: "evil2"}}
	data, err := marshalBlankedAgentProfile(in, nil, "json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{local}, nil)
	useConfigDir(t, dir)
	path := filepath.Join(root, "p.json")
	mustWriteFile(t, path, string(data))
	withFlags(t, agentImportCmd, map[string]string{"merge": "true"})
	stderr := captureStderrForTest(t, func() error { return agentImportCmd.RunE(agentImportCmd, []string{path}) })
	v, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := v.Get(local.Name)
	if len(got.MCPServers) != 1 || got.MCPServers[0].Command != "npx" || !strings.Contains(stderr, "MCP server new") {
		t.Errorf("MCP servers = %+v, stderr %q", got.MCPServers, stderr)
	}
	withFlags(t, agentImportCmd, map[string]string{"merge": "true", "keep-bundle-commands": "true"})
	captureStderrForTest(t, func() error { return agentImportCmd.RunE(agentImportCmd, []string{path}) })
	v, _ = openVault()
	got, _ = v.Get(local.Name)
	if len(got.MCPServers) != 2 || got.MCPServers[0].Command != "evil" {
		t.Errorf("--keep-bundle-commands did not take the servers: %+v", got.MCPServers)
	}
}
