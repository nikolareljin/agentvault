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

// generate env --format shell output is meant for eval or source; a model
// from a bundle must not run commands there.
func TestShellQuotedValueRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	value := "$(touch " + mark + ")`touch " + mark + "`\"\\$HOME"
	script := "X=" + shellQuotedValue(value) + "\nprintf %s \"$X\""
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("quoted value ran a command")
	}
	if string(out) != value {
		t.Errorf("value = %q, want %q", out, value)
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
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, generateEnvCmd, map[string]string{"format": "dotenv", "no-keys": "true"})
	out, err := captureStdoutForTest(t, func() error {
		return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
	})
	if err != nil {
		t.Fatal(err)
	}
	prefix := envPrefixForAgent(a)
	got, err := exec.Command("env", "-i", "HOME=/nonexistent", "sh", "-c", out+"\nprintf %s \"$"+prefix+"_MODEL|${"+prefix+"_BASE_URL-unset}\"").Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("sourcing the dotenv file ran a command")
	}
	if want := a.Model + "|unset"; string(got) != want {
		t.Errorf("env = %q, want %q (file %q)", got, want, out)
	}
}
