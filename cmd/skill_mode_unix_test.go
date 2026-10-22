//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// A same-machine import leaves an unchanged file alone: rewriting it reset
// every mode to 0600, and a skill script lost its execute bit. A skill
// script taken with consent keeps the bit it had where it was exported.
func TestImportKeepsSkillScriptModes(t *testing.T) {
	home := t.TempDir()
	script := filepath.Join(home, ".claude", "skills", "s", "scripts", "x.py")
	doc := filepath.Join(home, ".claude", "skills", "s", "SKILL.md")
	mustWriteFile(t, script, "print(1)\n")
	mustWriteFile(t, doc, "---\nname: s\n---\nRun scripts/x.py\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(doc, 0o644); err != nil {
		t.Fatal(err)
	}
	assets := []SetupAsset{
		{LogicalRoot: setupAssetRootProviderClaudeSkill, LogicalPath: "s/scripts/x.py", ContentPresent: true, Content: []byte("print(1)\n"), Mode: 0o755},
		{LogicalRoot: setupAssetRootProviderClaudeSkill, LogicalPath: "s/SKILL.md", ContentPresent: true, Content: []byte("---\nname: s\n---\nRun scripts/x.py\n"), Mode: 0o644},
	}
	if _, warnings, err := applyProviderAssetsWith(home, assets, fixedCtx("")); err != nil || len(warnings) > 0 {
		t.Fatalf("same-machine apply: %v %v", err, warnings)
	}
	for p, want := range map[string]os.FileMode{script: 0o755, doc: 0o644} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", p, info.Mode().Perm(), want)
		}
	}

	fresh := t.TempDir()
	ctx := fixedCtx("")
	ctx.KeepBundleCommands = true
	if _, _, err := applyProviderAssetsWith(fresh, assets[:1], ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(fresh, ".claude", "skills", "s", "scripts", "x.py"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("consented skill script written without its execute bit: %v %v", info, err)
	}
}
