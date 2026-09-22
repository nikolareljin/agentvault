package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// The command path, not only the walker. An instruction file naming three
// siblings must pull four files, push them all back where they were, and say
// out loud what it would not take.
func TestPullFollowsReferencesAndPushRestoresThem(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)

	cfgDir := filepath.Join(root, config.AppName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Init(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, cfgDir)

	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "AGENTS.md"),
		"# Rules\n\nUse `implement_pr.txt` and `implement_issue.txt`.\n"+
			"The helper is `scripts/hygiene.sh`.\n"+
			"Do not take `/etc/passwd`, and `absent.txt` is not here.\n")
	writeFile(t, filepath.Join(src, "implement_pr.txt"), "pr template\n")
	writeFile(t, filepath.Join(src, "implement_issue.txt"), "issue template\n")
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "scripts", "hygiene.sh"), "#!/usr/bin/env bash\n")

	if err := instPullCmd.RunE(instPullCmd, []string{src}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	reopened := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := reopened.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, inst := range reopened.ListInstructions() {
		stored[filepath.ToSlash(inst.Filename)] = true
	}
	for _, want := range []string{"AGENTS.md", "implement_pr.txt", "implement_issue.txt", "scripts/hygiene.sh"} {
		if !stored[want] {
			t.Errorf("%s was not stored; vault holds %v", want, keysOf(stored))
		}
	}
	if stored["/etc/passwd"] || stored["etc/passwd"] {
		t.Error("an absolute reference was stored")
	}

	if err := instPushCmd.RunE(instPushCmd, []string{dst}); err != nil {
		t.Fatalf("push: %v", err)
	}
	for _, rel := range []string{"AGENTS.md", "implement_pr.txt", "implement_issue.txt",
		filepath.Join("scripts", "hygiene.sh")} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("%s was not pushed: %v", rel, err)
			continue
		}
		want, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s did not round-trip", rel)
		}
	}
}

// --strict turns a report into a refusal, which is the difference between
// knowing a rule points at nothing and finding out on the next machine.
func TestPullStrictFailsOnAReferenceItCannotTake(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)

	cfgDir := filepath.Join(root, config.AppName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Init(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, cfgDir)

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "AGENTS.md"), "the template is `absent.txt`\n")

	withFlags(t, instPullCmd, map[string]string{"strict": "true"})
	err := instPullCmd.RunE(instPullCmd, []string{src})
	if err == nil {
		t.Fatal("--strict accepted an instruction file naming a file that is not there")
	}
	if !strings.Contains(err.Error(), "not present") {
		t.Errorf("error %q does not say what was wrong", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
