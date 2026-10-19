package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
)

// The whole move, through the commands: one home with every tool set up and a
// project whose instructions name other files, exported; a second home, with
// a different path and its own login token, importing with
// --apply-provider-configs and --apply-projects. Every file arrives, every
// path names the new home, and no secret travels. Hook scripts stay behind
// and are reported; TestApplyKeepsExecOnlyForToolHooks covers the execute bit.
func TestMoveToAnotherMachine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "home", "alex")
	dstHome := filepath.Join(base, "Users", "alex")
	srcProject := filepath.Join(srcHome, "src", "app")
	dstProject := filepath.Join(dstHome, "work", "app")
	for _, d := range []string{srcHome, dstHome, srcProject, dstProject} {
		mustMkdirAll(t, d)
	}

	// --- machine A ---
	w := func(p, body string) { mustWriteFile(t, p, body) }
	w(filepath.Join(srcHome, ".claude", "CLAUDE.md"), "@~/notes/team.md\n")
	w(filepath.Join(srcHome, "notes", "team.md"), "team rules; repo is "+srcProject+"\n")
	w(filepath.Join(srcHome, ".claude", "settings.json"),
		`{"hooks":{"Stop":[{"hooks":[{"command":"`+srcHome+`/.claude/hooks/notify.sh"}]}]},"env":{"ANTHROPIC_API_KEY":"src-key"}}`)
	w(filepath.Join(srcHome, ".claude", "hooks", "notify.sh"), "#!/bin/sh\necho done\n")
	if err := os.Chmod(filepath.Join(srcHome, ".claude", "hooks", "notify.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	w(filepath.Join(srcHome, ".claude.json"), `{"oauthAccount":{"email":"a@x"},"mcpServers":{"fs":{"command":"`+srcHome+`/bin/fs-mcp"}}}`)
	w(filepath.Join(srcHome, ".claude", ".credentials.json"), `{"token":"login"}`)
	w(filepath.Join(srcHome, ".gemini", "GEMINI.md"), "gemini rules\n")
	w(filepath.Join(srcHome, ".codex", "AGENTS.md"), "codex rules\n")
	w(filepath.Join(srcProject, "AGENTS.md"), "Read `docs/style.md`. Copy `.env.example` to `.env`.\n")
	w(filepath.Join(srcProject, "docs", "style.md"), "style\n")
	w(filepath.Join(srcProject, ".env"), "TOKEN=s3cret\n")
	w(filepath.Join(srcProject, ".env.example"), "TOKEN=\n")
	w(filepath.Join(srcProject, ".mcp.json"), `{"mcpServers":{}}`)

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)

	bundlePath := filepath.Join(base, "move"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{
		"yes": "true", "include-secrets": "false", "detect": "false", "project": srcProject,
	})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decryptBundlePayload(raw, e2eBundlePassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"src-key", "s3cret", `"login"`} {
		if bytes.Contains(payload, []byte(secret)) {
			t.Errorf("bundle carries secret %s", secret)
		}
	}

	// --- machine B ---
	w(filepath.Join(dstHome, ".claude.json"), `{"oauthAccount":{"email":"b@x"}}`)
	w(filepath.Join(dstHome, ".claude", "settings.json"), `{"env":{"ANTHROPIC_API_KEY":"dst-key"}}`)
	setTestHomeDir(t, dstHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dstHome, ".config"))
	dstCfg := filepath.Join(dstHome, ".config", config.AppName)
	seedProfileVault(t, dstCfg, nil, nil)
	useConfigDir(t, dstCfg)

	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{
		"apply-provider-configs": "true", "apply-projects": "true", "project-root": dstProject,
	})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}

	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		}
		return string(b)
	}
	settings := read(filepath.Join(dstHome, ".claude", "settings.json"))
	if !strings.Contains(settings, dstHome+"/.claude/hooks/notify.sh") || strings.Contains(settings, srcHome) {
		t.Errorf("hook path not remapped: %s", settings)
	}
	if !strings.Contains(settings, "dst-key") {
		t.Errorf("this machine's token was lost: %s", settings)
	}
	// Hook scripts are code that can embed tokens and cannot be field-redacted,
	// so without --include-secrets they stay behind, and import names each one.
	if _, err := os.Stat(filepath.Join(dstHome, ".claude", "hooks", "notify.sh")); !os.IsNotExist(err) {
		t.Errorf("hook script carried without --include-secrets: %v", err)
	}
	if !strings.Contains(in.String(), "path=hooks/notify.sh") {
		t.Errorf("import did not report the hook script it left behind:\n%s", in.String())
	}
	claudeJSON := read(filepath.Join(dstHome, ".claude.json"))
	if !strings.Contains(claudeJSON, "b@x") || !strings.Contains(claudeJSON, dstHome+"/bin/fs-mcp") {
		t.Errorf(".claude.json = %s", claudeJSON)
	}
	if !strings.Contains(read(filepath.Join(dstHome, "notes", "team.md")), dstProject) {
		t.Errorf("referenced note not remapped to the new project root")
	}
	if read(filepath.Join(dstHome, ".gemini", "GEMINI.md")) != "gemini rules\n" ||
		read(filepath.Join(dstHome, ".codex", "AGENTS.md")) != "codex rules\n" {
		t.Error("Gemini or Codex instructions missing")
	}
	if _, err := os.Stat(filepath.Join(dstHome, ".claude", ".credentials.json")); !os.IsNotExist(err) {
		t.Error("login token carried")
	}
	if read(filepath.Join(dstProject, "docs", "style.md")) != "style\n" {
		t.Error("referenced project file missing")
	}
	if _, err := os.Stat(filepath.Join(dstProject, ".env")); !os.IsNotExist(err) {
		t.Error(".env carried into the project")
	}
	if _, err := os.Stat(filepath.Join(dstProject, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("repository-scope .mcp.json written")
	}
}
