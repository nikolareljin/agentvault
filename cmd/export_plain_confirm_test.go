package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
)

// A plaintext export that carries API keys or secrets needs --confirm.
func TestExportPlainWithKeysNeedsConfirm(t *testing.T) {
	for name, flags := range map[string]map[string]string{
		"keys only":     {"confirm": "false", "include-keys": "true", "include-secrets": "false"},
		"keys, secrets": {"confirm": "false", "include-keys": "true", "include-secrets": "true"},
		"secrets only":  {"confirm": "false", "include-keys": "false", "include-secrets": "true"},
	} {
		path, err := exportPlainForTest(t, t.TempDir(), flags)
		if err == nil || !strings.Contains(err.Error(), "in plain text: anyone who can read") || !strings.Contains(err.Error(), "--confirm") {
			t.Errorf("%s: error = %v, want a refusal saying why", name, err)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("%s: refused export still wrote %s", name, path)
		}
	}
	_, err := exportPlainForTest(t, t.TempDir(), map[string]string{"confirm": "false", "include-keys": "true", "include-secrets": "true", "vault-only": "true"})
	if err == nil || strings.Contains(err.Error(), "include-keys=false") || !strings.Contains(err.Error(), "drop --plain") {
		t.Errorf("--vault-only: error = %v, want a refusal that does not suggest leaving keys out", err)
	}
	if _, err := exportPlainForTest(t, t.TempDir(), map[string]string{"confirm": "false", "include-keys": "false", "include-secrets": "false", "vault-only": "false"}); err != nil {
		t.Errorf("no keys, no secrets: error = %v, want nil", err)
	}
	if _, err := exportPlainForTest(t, t.TempDir(), map[string]string{"confirm": "true", "include-keys": "true", "include-secrets": "true"}); err != nil {
		t.Errorf("--confirm: error = %v, want nil", err)
	}
}

func TestSetupExportPlainWithKeysNeedsConfirm(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{{Name: "alpha", Provider: agent.ProviderClaude, APIKey: "sk-ant-local-key"}}, nil)
	useConfigDir(t, dir)
	path := filepath.Join(root, "setup.json")
	for _, f := range []map[string]string{{"include-keys": "true"}, {"include-secrets": "true"}} {
		f["confirm"] = "false"
		withFlags(t, setupExportCmd, f)
		_, err := captureStdoutForTest(t, func() error { return runSetupExport(setupExportCmd, []string{path}) })
		if err == nil || !strings.Contains(err.Error(), "anyone who can read "+path) {
			t.Errorf("%v: error = %v, want a refusal saying why", f, err)
		}
		f["confirm"] = "true"
		withFlags(t, setupExportCmd, f)
		if _, err := captureStdoutForTest(t, func() error { return runSetupExport(setupExportCmd, []string{path}) }); err != nil {
			t.Errorf("%v: error = %v, want nil", f, err)
		}
	}
}

func TestAgentExportIncludeKeyNeedsConfirm(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	a := testAgent()
	a.APIKey = "sk-ant-local-key"
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	path := filepath.Join(root, "profile.json")
	withFlags(t, agentExportCmd, map[string]string{"include-key": "true", "confirm": "false"})
	err := agentExportCmd.RunE(agentExportCmd, []string{a.Name, path})
	if err == nil || !strings.Contains(err.Error(), "in plain text") {
		t.Fatalf("error = %v, want a refusal", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("refused export still wrote the profile")
	}
	withFlags(t, agentExportCmd, map[string]string{"include-key": "true", "confirm": "true"})
	if err := agentExportCmd.RunE(agentExportCmd, []string{a.Name, path}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, path)), "sk-ant-local-key") {
		t.Fatal("confirmed export left out the key")
	}
}

// The wizard asks before a plaintext export with keys; yes is --confirm.
func TestExportWizardAsksBeforePlaintextKeys(t *testing.T) {
	flags := map[string]string{"project": "", "plain": "true"}
	for _, f := range []string{"include-keys", "include-secrets", "include-sessions", "include-templates", "include-provider-files", "include-skills", "include-memory", "detect", "include-status"} {
		flags[f] = "false"
	}
	flags["include-keys"] = "true"
	withFlags(t, exportCmd, flags)
	for answer, want := range map[string]bool{"y\n": true, "\n": false} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.WriteString("all\n" + answer) // profiles, then the plaintext question
		w.Close()
		old := os.Stdin
		os.Stdin = r
		var out strings.Builder
		exportCmd.SetOut(&out)
		opts := exportOptions{Output: "out.json", IncludeKeys: true}
		err = runExportWizard(exportCmd, &opts, nil)
		os.Stdin = old
		exportCmd.SetOut(nil)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if opts.Confirm != want || !strings.Contains(out.String(), "Write API keys in plain text") {
			t.Errorf("answer %q: Confirm = %v, prompt %q", answer, opts.Confirm, out.String())
		}
	}
}
