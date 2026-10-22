package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// instructions import takes an instruction that runs code only with
// --keep-bundle-commands, as a bundle import does, and an instruction from
// 0.14 holding a NUL byte, equal to the vault's, is unchanged rather than
// failing the whole import.
func TestInstructionsImportGatesCodeAndKeepsEqualNonText(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, nil)
	useConfigDir(t, dir)
	open := func() *vault.Vault {
		v := vault.New(filepath.Join(dir, config.VaultFile))
		if err := v.Unlock(e2eMasterPassword); err != nil {
			t.Fatal(err)
		}
		return v
	}
	nul := agent.InstructionFile{Name: "old", Filename: "CLAUDE.md", Content: "a\x00b\n"}
	v := open()
	sc := v.SharedConfig()
	sc.Instructions = append(sc.Instructions, nul)
	if err := v.SetSharedConfig(sc); err != nil {
		t.Fatal(err)
	}
	in := []agent.InstructionFile{
		nul,
		{Name: "cmd", Filename: ".claude/commands/x.md", Content: "run !`id`\n"},
		{Name: "plain", Filename: "AGENTS.md", Content: "plain\n"},
		{Name: "bin", Filename: "GEMINI.md", Content: "x\x00y"},
	}
	data, _ := json.Marshal(in)
	file := filepath.Join(root, "in.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	withFlags(t, instImportCmd, map[string]string{"merge": "true"})
	out, err := captureStdoutForTest(t, func() error { return instImportCmd.RunE(instImportCmd, []string{file}) })
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	got := map[string]bool{}
	for _, i := range open().ListInstructions() {
		got[i.Name] = true
	}
	if got["cmd"] || got["bin"] || !got["plain"] || !got["old"] {
		t.Errorf("stored %v\n%s", got, out)
	}
	if !strings.Contains(out, "--keep-bundle-commands") || !strings.Contains(out, "Unchanged: 1") {
		t.Errorf("report:\n%s", out)
	}
	withFlags(t, instImportCmd, map[string]string{"keep-bundle-commands": "true"})
	if _, err := captureStdoutForTest(t, func() error { return instImportCmd.RunE(instImportCmd, []string{file}) }); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range open().ListInstructions() {
		found = found || i.Name == "cmd"
	}
	if !found {
		t.Error("--keep-bundle-commands did not take the command file")
	}
}

// A bundle instruction that is not UTF-8 text is not stored, even with
// consent, unless it equals this vault's copy.
func TestValidIncomingInstructionsRefusesNonText(t *testing.T) {
	nul := agent.InstructionFile{Name: "old", Filename: "CLAUDE.md", Content: "a\x00b\n"}
	other := agent.InstructionFile{Name: "new", Filename: "AGENTS.md", Content: "a\x00b\n"}
	rep := &importReport{}
	out := validIncomingInstructions([]agent.InstructionFile{nul, other}, []agent.InstructionFile{nul}, rep)
	if len(out) != 1 || out[0].Name != "old" {
		t.Errorf("out = %+v, report %v", out, rep.Lines)
	}
	if len(rep.Lines) != 1 || !strings.Contains(rep.Lines[0], "Left out: instruction new") {
		t.Errorf("report %v", rep.Lines)
	}
}
