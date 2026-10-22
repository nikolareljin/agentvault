package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// setup apply --generate wrote .env.agents with raw values: a model with a
// newline added a variable, an escape reached the file as is.
func TestSetupApplyGenerateQuotesEnv(t *testing.T) {
	a := testAgent()
	a.Model = "m\nLD_PRELOAD=/tmp/x.so"
	b := testAgent()
	b.Name = "plain"
	b.Model = "$(touch pwn)"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a, b}, nil)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	project := filepath.Join(root, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	withFlags(t, setupApplyCmd, map[string]string{"generate": "true"})
	_, err := captureStdoutForTest(t, func() error { return setupApplyCmd.RunE(setupApplyCmd, []string{project}) })
	if err == nil {
		t.Errorf("setup apply --generate left a value out but exited 0")
	}
	data, rerr := os.ReadFile(filepath.Join(project, ".env.agents"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	got := string(data)
	if strings.Contains(got, "\nLD_PRELOAD") || !strings.Contains(got, "CLAUDE_PLAIN_MODEL='$(touch pwn)'") {
		t.Errorf(".env.agents not quoted:\n%s", got)
	}
}

// A cloned repository can plant AGENTS.md, .github or .env as a link out of
// it; sync to, setup apply --generate and generate env must not write
// through it, dangling or not.
func TestProjectWritersRefuseSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	dir := filepath.Join(root, "proj")
	for _, d := range []string{outside, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(name, target string) string {
		p := filepath.Join(dir, name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	agentsMD := link("AGENTS.md", filepath.Join(outside, "dangling.md"))
	if err := writeIfAllowed(dir, agentsMD, "x", false); err == nil {
		t.Errorf("writeIfAllowed wrote through a dangling symlink")
	}
	link(".github", outside)
	if err := writeIfAllowed(dir, filepath.Join(dir, ".github", "copilot-instructions.md"), "x", true); err == nil {
		t.Errorf("writeIfAllowed wrote through a symlinked directory")
	}
	env := link(".env", filepath.Join(outside, "bashrc"))
	if err := writeEnvFile("", env, "K=v\n"); err == nil {
		t.Errorf("writeEnvFile wrote through a symlink")
	}
	if err := writeEnvFile(dir, env, "K=v\n"); err == nil {
		t.Errorf("writeEnvFile under root wrote through a symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files written outside the project: %v", entries)
	}
	plain := filepath.Join(dir, "plain.md")
	if err := writeIfAllowed(dir, plain, "x", false); err != nil {
		t.Errorf("writeIfAllowed refused a plain file: %v", err)
	}
	if err := writeEnvFile("", filepath.Join(dir, ".env.plain"), "K=v\n"); err != nil {
		t.Errorf("writeEnvFile refused a plain file: %v", err)
	}
}

// A vault written by 0.14 can hold names with terminal escapes; the lines
// of setup apply, instructions diff and the prompt-mode menu print them.
func TestVaultNamesPrintedClean(t *testing.T) {
	esc := "i\x1b]0;t\x07\x1b[2J"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, nil)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: esc, Filename: "X.md", Content: "vault\n"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	project := filepath.Join(root, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	withFlags(t, setupApplyCmd, map[string]string{"generate": "false"})
	out, err := captureStdoutForTest(t, func() error { return setupApplyCmd.RunE(setupApplyCmd, []string{project}) })
	if err != nil || !strings.Contains(out, "Pushed") || strings.Contains(out, "\x1b") {
		t.Errorf("setup apply: err = %v, out = %q", err, out)
	}
	if err := os.WriteFile(filepath.Join(project, "X.md"), []byte("disk\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdoutForTest(t, func() error { return instDiffCmd.RunE(instDiffCmd, []string{project}) })
	if err != nil || !strings.Contains(out, "DIFFERS") || strings.Contains(out, "\x1b") {
		t.Errorf("instructions diff: err = %v, out = %q", err, out)
	}
	var menu strings.Builder
	old := promptModeOutput
	promptModeOutput = &menu
	t.Cleanup(func() { promptModeOutput = old })
	a, b := testAgent(), testAgent()
	a.Name, b.Name = esc, "b"
	_, _, _ = selectPromptModeAgent(bufio.NewReader(strings.NewReader("\n")), []agent.Agent{a, b})
	if strings.Contains(menu.String(), "\x1b") || !strings.Contains(menu.String(), "1.") {
		t.Errorf("prompt-mode menu = %q", menu.String())
	}
}

// Import stored any instruction a bundle named, so .git/config reached the
// vault and the next push wrote it.
func TestImportSkipsUnsafeInstructionFilenames(t *testing.T) {
	v := newTestVault(t)
	setup := SetupBundle{SharedConfig: agent.SharedConfig{Instructions: []agent.InstructionFile{
		{Name: "gitcfg", Filename: ".git/config", Content: "[core]\n\tfsmonitor = touch x\n"},
		{Name: "up", Filename: "../up.md", Content: "x"},
		{Name: "ok", Filename: "OK.md", Content: "fine"},
	}}}
	plan := planProfileImport(v, setup, strategyMerge, time.Now(), importConsent{})
	var names []string
	for _, inst := range plan.Shared.Instructions {
		names = append(names, inst.Name)
	}
	if len(names) != 1 || names[0] != "ok" {
		t.Errorf("planned instructions = %v, want [ok]", names)
	}
	if plan.Report.Skipped < 2 {
		t.Errorf("report.Skipped = %d, want the two refused instructions", plan.Report.Skipped)
	}
}

// A 0.14 vault entry whose filename is now refused fails push and apply
// with the allowed forms named, before any file is written.
func TestPushRefusesOldVaultFilenamesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, nil)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	sc := v.SharedConfig()
	sc.Instructions = []agent.InstructionFile{
		{Name: "a", Filename: "AGENTS.md", Content: "ok"},
		{Name: "z", Filename: "sub/.vscode/tasks.json", Content: "evil"},
	}
	if err := v.SetSharedConfig(sc); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	for name, run := range map[string]func(string) error{
		"push":  func(p string) error { return instPushCmd.RunE(instPushCmd, []string{p}) },
		"apply": func(p string) error { return setupApplyCmd.RunE(setupApplyCmd, []string{p}) },
	} {
		project := filepath.Join(root, name)
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		withFlags(t, setupApplyCmd, map[string]string{"generate": "false"})
		_, err := captureStdoutForTest(t, func() error { return run(project) })
		if err == nil || !strings.Contains(err.Error(), "allowed:") || !strings.Contains(err.Error(), "nothing written") {
			t.Errorf("%s: err = %v, want a refusal naming the allowed forms", name, err)
		}
		entries, _ := os.ReadDir(project)
		if len(entries) != 0 {
			t.Errorf("%s wrote %d entries before refusing", name, len(entries))
		}
	}
}

// setup apply names each staged project file it does not write, as import
// --apply-projects does, instead of dropping it without a word.
func TestSetupApplyReportsStagedLeftOut(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, nil)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "a\n"}); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	staged := filepath.Join(dir, setupImportedAssetsDirName, "project", ".claude", "commands")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "x.md"), []byte("run !`id`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	withFlags(t, setupApplyCmd, map[string]string{"generate": "false"})
	out, err := captureStdoutForTest(t, func() error { return setupApplyCmd.RunE(setupApplyCmd, []string{project}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Left out: ") || !strings.Contains(out, "x.md: runs commands") {
		t.Errorf("setup apply did not name the file it left out:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "commands", "x.md")); err == nil {
		t.Error("staged command file written without consent")
	}
}
