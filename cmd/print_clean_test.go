package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

// Every command that prints vault fields keeps their control characters
// off the terminal. The fields can come from another machine's bundle, and
// names from a vault saved before they were refused.
func TestCommandsPrintNoControlCharacters(t *testing.T) {
	const e = "\x1b]0;t\a\x1b[2J\u009b1;1H\r"
	setStdoutTerminal(t, true)
	a := testAgent()
	a.Name = "cl" + e
	a.Model = "m" + e
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, []agent.UnifiedRule{{Name: "r" + e, Description: "d" + e, Content: "c" + e, Category: "general" + e, Enabled: true}})
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	sc := v.SharedConfig()
	sc.SystemPrompt = "p" + e
	sc.Roles = []agent.Role{{Name: "ro" + e, Title: "t" + e, Description: "d" + e, Prompt: "p" + e, Rules: []string{"r" + e}, Tags: []string{"g" + e}}}
	sc.MCPServers = []agent.MCPServer{{Name: "m" + e, Command: "c" + e}}
	if err := v.SetSharedConfig(sc); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: "i" + e, Filename: "AGENTS.md", Content: "body" + e}); err != nil {
		t.Fatal(err)
	}
	if err := v.AddSession(agent.Session{ID: "s1" + e, Name: "s" + e, ProjectDir: root, ActiveRole: "ro" + e, Agents: []agent.SessionAgent{{Name: a.Name, Enabled: true, Task: "k" + e}}}); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)

	cases := []struct {
		name  string
		cmd   *cobra.Command
		args  []string
		flags map[string]string
	}{
		{"list json", listCmd, nil, map[string]string{"json": "true"}},
		{"rules list", rulesListCmd, nil, nil},
		{"rules list json", rulesListCmd, nil, map[string]string{"json": "true"}},
		{"rules show", rulesShowCmd, []string{"r" + e}, nil},
		{"rules export", rulesExportCmd, nil, nil},
		{"roles list", rolesListCmd, nil, nil},
		{"roles list json", rolesListCmd, nil, map[string]string{"json": "true"}},
		{"roles show", rolesShowCmd, []string{"ro" + e}, nil},
		{"session list", sessionListCmd, nil, nil},
		{"session list json", sessionListCmd, nil, map[string]string{"json": "true"}},
		{"session show", sessionShowCmd, []string{"s" + e}, nil},
		{"session start dry run", sessionStartCmd, []string{"s" + e}, map[string]string{"dry-run": "true"}},
		{"instructions list", instListCmd, nil, nil},
		{"instructions show", instShowCmd, []string{"i" + e}, nil},
		{"instructions preview", instPreviewCmd, nil, map[string]string{"dir": root}},
		{"instructions export", instExportCmd, nil, nil},
		{"config show", configShowCmd, nil, nil},
		{"run", runCmd, []string{a.Name}, nil},
		{"generate env json", generateEnvCmd, []string{"-"}, map[string]string{"format": "json"}},
		{"generate mcp", generateMCPCmd, []string{"-"}, nil},
	}
	for _, tc := range cases {
		withFlags(t, tc.cmd, tc.flags)
		out, err := captureStdoutForTest(t, func() error { return tc.cmd.RunE(tc.cmd, tc.args) })
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		// A CR before LF is kept: instruction files can use CRLF.
		if strings.ContainsAny(strings.ReplaceAll(out, "\r\n", "\n"), "\x1b\a\r\u009b") {
			t.Errorf("%s printed control characters: %q", tc.name, out)
		}
	}
}

func setStdoutTerminal(t *testing.T, on bool) {
	t.Helper()
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return on }
	t.Cleanup(func() { stdoutIsTerminal = prev })
}

// Content meant for a file (`instructions show > CLAUDE.md`, `rules export
// > RULES.md`) is byte-exact when stdout is not a terminal.
func TestContentRedirectedIsByteExact(t *testing.T) {
	const body = "x\x1b[1m\u009b\x07\r\n\tz\u00e9\u2028"
	setStdoutTerminal(t, false)
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, []agent.UnifiedRule{{Name: "r", Description: "d" + body, Content: body, Category: "general", Enabled: true}})
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: "i", Filename: "AGENTS.md", Content: body + "\n"}); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	cases := []struct {
		name  string
		cmd   *cobra.Command
		args  []string
		flags map[string]string
		want  string
	}{
		{"instructions show", instShowCmd, []string{"i"}, nil, body + "\n"},
		{"instructions preview", instPreviewCmd, []string{"i"}, map[string]string{"dir": root}, body + "\n"},
		{"rules export", rulesExportCmd, nil, nil, "### d" + body + "\n" + body + "\n\n"},
	}
	for _, tc := range cases {
		withFlags(t, tc.cmd, tc.flags)
		out, err := captureStdoutForTest(t, func() error { return tc.cmd.RunE(tc.cmd, tc.args) })
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: output %q does not contain %q", tc.name, out, tc.want)
		}
	}
	setStdoutTerminal(t, true)
	withFlags(t, instShowCmd, nil)
	out, _ := captureStdoutForTest(t, func() error { return instShowCmd.RunE(instShowCmd, []string{"i"}) })
	if strings.ContainsAny(out, "\x1b\a\u009b") {
		t.Errorf("terminal output kept control characters: %q", out)
	}
}
