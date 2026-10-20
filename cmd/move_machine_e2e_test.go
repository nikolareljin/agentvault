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
	// api moves to another URL on machine B; the server keyed by a home path
	// is the same on both once remapped.
	w(filepath.Join(srcHome, ".claude.json"), `{"oauthAccount":{"email":"a@x"},"mcpServers":{"fs":{"command":"`+srcHome+`/bin/fs-mcp"},`+
		`"api":{"type":"http","url":"https://new.example/mcp","headers":{"Authorization":"src-api-tok"}},`+
		`"`+srcHome+`/srv":{"type":"http","url":"https://srv.example/mcp","headers":{"Authorization":"src-srv-tok"}},`+
		`"new1":{"command":"new1-mcp"},"new2":{"command":"new2-mcp"},"shared":{"command":"shared-from-a"}}}`)
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
	for _, secret := range []string{"src-key", "s3cret", `"login"`, "src-api-tok", "src-srv-tok"} {
		if bytes.Contains(payload, []byte(secret)) {
			t.Errorf("bundle carries secret %s", secret)
		}
	}

	// --- machine B ---
	w(filepath.Join(dstHome, ".claude.json"), `{"oauthAccount":{"email":"b@x"},"mcpServers":{`+
		`"api":{"type":"http","url":"https://old.example/mcp","headers":{"Authorization":"dst-api-tok"}},`+
		`"`+dstHome+`/srv":{"type":"http","url":"https://srv.example/mcp","headers":{"Authorization":"dst-srv-tok"}},`+
		`"local1":{"command":"local1-mcp"},"shared":{"command":"shared-on-b"}}}`)
	const dstSettings = `{"env":{"ANTHROPIC_API_KEY":"dst-key"},"localOnly":"kept"}`
	w(filepath.Join(dstHome, ".claude", "settings.json"), dstSettings)
	w(filepath.Join(dstProject, "AGENTS.md"), "machine B's own instructions\n")
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
		"keep-bundle-commands": "true", "mcp-servers": "fs,api,new1",
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
	// Union: a key only this machine had stays. The file it replaced is
	// backed up under a timestamped name and listed.
	if !strings.Contains(settings, `"localOnly": "kept"`) {
		t.Errorf("local-only key lost: %s", settings)
	}
	backups, _ := filepath.Glob(filepath.Join(dstHome, ".claude", "settings.json.bak-*"))
	if len(backups) != 1 || read(backups[0]) != dstSettings || !strings.Contains(in.String(), backups[0]) {
		t.Errorf("settings backup = %v\n%s", backups, in.String())
	}
	claudeJSON := read(filepath.Join(dstHome, ".claude.json"))
	// Not interactive: the token of a server whose URL changed stays behind;
	// the token of a server under a remapped key comes back.
	if strings.Contains(claudeJSON, "dst-api-tok") || !strings.Contains(claudeJSON, "https://new.example/mcp") {
		t.Errorf("changed server got this machine's token without a yes: %s", claudeJSON)
	}
	if !strings.Contains(claudeJSON, `"Authorization": "dst-srv-tok"`) {
		t.Errorf("secret under remapped key not restored: %s", claudeJSON)
	}
	// Conflict, not interactive: back up and replace.
	if agents := read(filepath.Join(dstProject, "AGENTS.md")); strings.Contains(agents, "machine B") {
		t.Errorf("conflicting AGENTS.md kept: %s", agents)
	}
	if b, _ := filepath.Glob(filepath.Join(dstProject, "AGENTS.md.bak-*")); len(b) != 1 || read(b[0]) != "machine B's own instructions\n" {
		t.Errorf("AGENTS.md backup = %v", b)
	}
	if !strings.Contains(claudeJSON, "b@x") || !strings.Contains(claudeJSON, dstHome+"/bin/fs-mcp") {
		t.Errorf(".claude.json = %s", claudeJSON)
	}
	// MCP servers one by one: machine B's own server stays, the picked new
	// one arrives, the unpicked one and the changed shared one do not.
	for _, want := range []string{"local1-mcp", "new1-mcp", "shared-on-b"} {
		if !strings.Contains(claudeJSON, want) {
			t.Errorf("%s missing from .claude.json: %s", want, claudeJSON)
		}
	}
	for _, bad := range []string{"new2-mcp", "shared-from-a"} {
		if strings.Contains(claudeJSON, bad) {
			t.Errorf("%s taken without a pick: %s", bad, claudeJSON)
		}
	}
	if !strings.Contains(in.String(), `MCP servers: `+filepath.Join(dstHome, ".claude.json")+`: added "fs", "new1"; replaced "api"; kept local "shared"; left out "new2"`) {
		t.Errorf("MCP report missing:\n%s", in.String())
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
	if !strings.Contains(read(filepath.Join(dstProject, "AGENTS.md")), "docs/style.md") {
		t.Error("project instruction file not written with --apply-projects")
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
