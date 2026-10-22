package cmd

import (
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

// A skill is a directory. Pull stores every file it names, a same-machine
// import leaves nothing out, push restores the bytes and keeps the modes,
// and a bundle's changed script or frontmatter is left out without consent.
func TestSkillDirectoryRoundTripAndGate(t *testing.T) {
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
	skill := filepath.Join(src, ".claude", "skills", "review")
	files := map[string]string{
		"CLAUDE.md": "Use the skill in `.claude/skills/review/SKILL.md`.\n",
		".claude/skills/review/SKILL.md": "---\nname: review\ndescription: review a diff\n---\n" +
			"Run `scripts/x.py`, read `references/a.md`, fill `assets/t.json`.\n",
		".claude/skills/review/scripts/x.py":    "#!/usr/bin/env python3\nprint('ok')\n",
		".claude/skills/review/references/a.md": "checklist\n",
		".claude/skills/review/assets/t.json":   "{}\n",
	}
	for rel, body := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, body)
	}
	script := filepath.Join(skill, "scripts", "x.py")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := instPullCmd.RunE(instPullCmd, []string{src}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	v = vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	stored := map[string]agent.InstructionFile{}
	for _, inst := range v.ListInstructions() {
		stored[filepath.ToSlash(inst.Filename)] = inst
	}
	for rel := range files {
		if _, ok := stored[rel]; !ok {
			t.Errorf("%s was not stored; vault holds %d entries", rel, len(stored))
		}
	}

	// Same machine: the bundle holds exactly what the vault holds.
	same := SetupBundle{SharedConfig: agent.SharedConfig{Instructions: v.ListInstructions()}}
	for _, s := range []importStrategy{strategyMerge, strategyReplace, strategyMirror} {
		plan := planProfileImport(v, same, s, time.Now(), importConsent{})
		if strings.Contains(strings.Join(plan.Report.Lines, "\n"), "Left out") {
			t.Errorf("%v: same-machine import left something out: %v", s, plan.Report.Lines)
		}
		if mustJSON(t, plan.Shared.Instructions) != mustJSON(t, v.ListInstructions()) {
			t.Errorf("%v: same-machine import changed the instructions", s)
		}
	}

	// Pushed back over the project: bytes identical, executable bit kept.
	if err := instPushCmd.RunE(instPushCmd, []string{src}); err != nil {
		t.Fatalf("push: %v", err)
	}
	for rel, body := range files {
		got, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil || string(got) != body {
			t.Errorf("%s changed on push: %q %v", rel, got, err)
		}
	}
	if info, err := os.Stat(script); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o755) {
		t.Errorf("script mode after push: %v %v", info.Mode(), err)
	}

	// Tampered bundle: a changed script, a changed frontmatter, a new script.
	var tampered []agent.InstructionFile
	for _, inst := range v.ListInstructions() {
		switch filepath.ToSlash(inst.Filename) {
		case ".claude/skills/review/scripts/x.py":
			inst.Content = "import os; os.system('evil1')\n"
		case ".claude/skills/review/SKILL.md":
			inst.Content = "---\nname: review\nallowed-tools: Bash(evil2)\n---\nhi\n"
		}
		tampered = append(tampered, inst)
	}
	tampered = append(tampered, agent.InstructionFile{Name: ".claude/skills/new/run.sh",
		Filename: ".claude/skills/new/run.sh", Content: "evil3"})
	b := SetupBundle{SharedConfig: agent.SharedConfig{Instructions: tampered}}
	for _, s := range []importStrategy{strategyMerge, strategyReplace, strategyMirror} {
		plan := planProfileImport(v, b, s, time.Now(), importConsent{})
		got := mustJSON(t, plan.Shared.Instructions)
		if strings.Contains(got, "evil") {
			t.Errorf("%v: tampered skill file taken without consent: %s", s, got)
		}
		if !strings.Contains(got, "print('ok')") || !strings.Contains(got, "description: review a diff") {
			t.Errorf("%v: local skill files lost", s)
		}
		report := strings.Join(plan.Report.Lines, "\n")
		for _, want := range []string{"scripts/x.py", "SKILL.md", "new/run.sh"} {
			if !strings.Contains(report, want) {
				t.Errorf("%v: report does not name %s: %s", s, want, report)
			}
		}
	}
	kept := planProfileImport(v, b, strategyReplace, time.Now(), importConsent{KeepCommands: true})
	if got := mustJSON(t, kept.Shared.Instructions); !strings.Contains(got, "evil1") || !strings.Contains(got, "evil3") {
		t.Errorf("--keep-bundle-commands did not take the skill files")
	}
}
