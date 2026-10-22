package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
)

// generate env --format shell and run --env output is eval'd or sourced: a
// value from a bundle must run nothing and come back byte for byte, in
// every POSIX shell on the machine.
func TestShellExportLineRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	values := []string{
		"$(touch " + mark + ")`touch " + mark + "`\"\\$HOME",
		"it's '' a 'quote'",
		"line1\nline2\r\n\tend\n",
		"!! !$ ${HOME} \\n é 日本 \u2028",
		"",
	}
	shells := []string{}
	for _, sh := range []string{"sh", "bash", "dash", "zsh"} {
		if _, err := exec.LookPath(sh); err == nil {
			shells = append(shells, sh)
		}
	}
	for _, value := range values {
		line := shellExportLine("X", value)
		file := filepath.Join(dir, "env.sh")
		if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, sh := range shells {
			for _, how := range []string{". " + file, "eval \"$(cat " + file + ")\"", line} {
				out, err := exec.Command(sh, "-c", how+"\nprintf %s \"$X\"").Output()
				if err != nil {
					t.Fatalf("%s %q: %v", sh, how, err)
				}
				if string(out) != value {
					t.Errorf("%s %q: value = %q, want %q", sh, how, out, value)
				}
			}
		}
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("quoted value ran a command")
	}
	for _, bad := range []string{"a\x1b[2Jb", "a\rb", "a\u009bb", "a\x00b"} {
		if got := shellExportLine("X", bad); !strings.HasPrefix(got, "# X left out") {
			t.Errorf("%q: got %q", bad, got)
		}
	}
}

// A name saved before control characters were refused must not start a
// new line, and so a command, in the generated shell file.
func TestGenerateEnvShellNameStaysInComment(t *testing.T) {
	a := testAgent()
	a.Name = "old\nexport PWN=1 #"
	a.Model = "m\x1b]0;t\a"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, generateEnvCmd, map[string]string{"format": "shell"})
	out, err := captureStdoutForTest(t, func() error {
		return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\nexport PWN") || strings.ContainsAny(out, "\x1b\a") {
		t.Errorf("output = %q", out)
	}
}

// run --env output is meant for eval; a model from a bundle must not run
// a command there.
func TestRunEnvRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	mark := filepath.Join(t.TempDir(), "ran")
	a := testAgent()
	a.Model = "$(touch " + mark + ")"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, runCmd, map[string]string{"env": "true"})
	out, err := captureStdoutForTest(t, func() error { return runCmd.RunE(runCmd, []string{a.Name}) })
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command("sh", "-c", out+"\nprintf %s \"$AGENT_MODEL\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("run --env output ran a command")
	}
	if string(got) != a.Model {
		t.Errorf("AGENT_MODEL = %q, want %q", got, a.Model)
	}
}

// A dotenv file from generate env is sourced by shells too; a model from a
// bundle must stay a literal value there.
func TestGenerateDotenvRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	mark := filepath.Join(t.TempDir(), "ran")
	a := testAgent()
	a.Model = "$(touch " + mark + ")`touch " + mark + "`$HOME\\"
	a.BaseURL = "https://h.example/it's"
	a.APIKey = "k${HOME}"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, generateEnvCmd, map[string]string{"format": "dotenv"})
	out, err := captureStdoutForTest(t, func() error {
		return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
	})
	if err != nil {
		t.Fatal(err)
	}
	prefix := envPrefixForAgent(a)
	got, err := exec.Command("env", "-i", "HOME=/nonexistent", "sh", "-c", out+"\nprintf %s \"$"+prefix+"_MODEL|${"+prefix+"_BASE_URL-unset}|${"+prefix+"_API_KEY-unset}\"").Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("sourcing the dotenv file ran a command")
	}
	if want := a.Model + "|unset|unset"; string(got) != want {
		t.Errorf("env = %q, want %q (file %q)", got, want, out)
	}
}

// The provider is not validated on import, so it is as hostile as the
// name: "claude;cmd;" made generate env print `export CLAUDE;CMD;_X_MODEL=`.
func TestEnvPrefixForAgentIsAName(t *testing.T) {
	for _, p := range []string{"claude;touch PWNED;", "$(id)", "a b`c`", "", "-", "9x"} {
		got := envPrefixForAgent(agent.Agent{Name: "x'y", Provider: agent.Provider(p)})
		for _, r := range got {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				t.Fatalf("provider %q: prefix %q has %q", p, got, r)
			}
		}
		if got == "" || got[0] >= '0' && got[0] <= '9' || got[0] == '_' {
			t.Errorf("provider %q: prefix %q is not a variable name", p, got)
		}
	}
	if got := envPrefixForAgent(agent.Agent{Name: "my-claude", Provider: "claude"}); got != "CLAUDE_MY_CLAUDE" {
		t.Errorf("prefix = %q, want CLAUDE_MY_CLAUDE", got)
	}
}
