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
	setTestHomeDir(t, root)
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
		"# Rules\n\nUse `docs/review.md` and `docs/release.md`.\n"+
			"The helper is `scripts/check.sh`.\n"+
			"Do not take `/etc/passwd`, and `absent.txt` is not here.\n")
	if err := os.MkdirAll(filepath.Join(src, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "docs", "review.md"), "review checklist\n")
	writeFile(t, filepath.Join(src, "docs", "release.md"), "release checklist\n")
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "scripts", "check.sh"), "#!/usr/bin/env bash\n")

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
	for _, want := range []string{"AGENTS.md", "docs/review.md", "docs/release.md", "scripts/check.sh"} {
		if !stored[want] {
			t.Errorf("%s was not stored; vault holds %v", want, keysOf(stored))
		}
	}
	if stored["/etc/passwd"] || stored["etc/passwd"] {
		t.Error("an absolute reference was stored")
	}

	// dst is another project, so its referenced files need the flag.
	withFlags(t, instPushCmd, map[string]string{"from-any-project": "true"})
	if err := instPushCmd.RunE(instPushCmd, []string{dst}); err != nil {
		t.Fatalf("push: %v", err)
	}
	for _, rel := range []string{"AGENTS.md", "docs/review.md", "docs/release.md",
		filepath.Join("scripts", "check.sh")} {
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
	setTestHomeDir(t, root)
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

// instructions pull follows references into the vault. A mention of `.env`
// must not store the secrets file there.
func TestPullDoesNotFollowIntoSecretFiles(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
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
	writeFile(t, filepath.Join(src, "AGENTS.md"), "Copy `.env.example` to `.env`.\n")
	writeFile(t, filepath.Join(src, ".env"), "TOKEN=s3cret\n")
	writeFile(t, filepath.Join(src, ".env.example"), "TOKEN=\n")
	if err := instPullCmd.RunE(instPullCmd, []string{src}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	reopened := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := reopened.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	template := false
	for _, inst := range reopened.ListInstructions() {
		if strings.Contains(inst.Content, "s3cret") {
			t.Fatalf("pull stored the secrets file as %q", inst.Filename)
		}
		if filepath.ToSlash(inst.Filename) == ".env.example" {
			template = true
		}
	}
	// Without this the check above passes on a pull that stored nothing.
	if !template {
		t.Errorf(".env.example not stored; stored %d instructions", len(reopened.ListInstructions()))
	}
}

// A UTF-16 AGENTS.md (Windows Notepad writes one) is refused and named; the
// other files are still pulled. Before, it stopped the pull with nothing
// stored, and --strict is what turns the refusal into a failure.
func TestPullSkipsAFileThatIsNotUTF8(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
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
	writeFile(t, filepath.Join(src, "AGENTS.md"), "\xff\xfeh\x00i\x00")
	writeFile(t, filepath.Join(src, "CLAUDE.md"), "claude\n")
	writeFile(t, filepath.Join(src, "GEMINI.md"), "gemini\n")
	if err := instPullCmd.RunE(instPullCmd, []string{src}); err != nil {
		t.Fatalf("pull stopped at the UTF-16 file: %v", err)
	}
	reopened := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := reopened.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, inst := range reopened.ListInstructions() {
		stored[inst.Filename] = true
	}
	if !stored["CLAUDE.md"] || !stored["GEMINI.md"] || stored["AGENTS.md"] {
		t.Errorf("stored %v, want CLAUDE.md and GEMINI.md only", keysOf(stored))
	}

	withFlags(t, instPullCmd, map[string]string{"strict": "true"})
	if err := instPullCmd.RunE(instPullCmd, []string{src}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("--strict with a refused file: %v", err)
	}
}
