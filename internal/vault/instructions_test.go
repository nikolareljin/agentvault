package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestPullPushRoundTrip(t *testing.T) {
	// Simulate: pull from project A, push to project B, verify identical.
	projectA := t.TempDir()
	projectB := t.TempDir()

	agentsContent := "# Repository Guidelines\n\n- Use Go\n- Table-driven tests\n"
	claudeContent := "# Claude Instructions\n\nBe thorough and precise.\n"

	os.WriteFile(filepath.Join(projectA, "AGENTS.md"), []byte(agentsContent), 0644)
	os.WriteFile(filepath.Join(projectA, "CLAUDE.md"), []byte(claudeContent), 0644)

	// Create vault and pull from project A
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")

	// Pull: read files from projectA into vault
	for filename, name := range map[string]string{
		"AGENTS.md": "agents",
		"CLAUDE.md": "claude",
	} {
		data, err := os.ReadFile(filepath.Join(projectA, filename))
		if err != nil {
			t.Fatalf("reading %s: %v", filename, err)
		}
		inst := agent.InstructionFile{
			Name:      name,
			Filename:  filename,
			Content:   string(data),
			UpdatedAt: time.Now(),
		}
		if err := v.SetInstruction(inst); err != nil {
			t.Fatalf("SetInstruction(%s) error = %v", name, err)
		}
	}

	if len(v.ListInstructions()) != 2 {
		t.Fatalf("after pull: len = %d, want 2", len(v.ListInstructions()))
	}

	// Push: write files from vault to project B
	for _, inst := range v.ListInstructions() {
		p := filepath.Join(projectB, inst.Filename)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
		if err := os.WriteFile(p, []byte(inst.Content), 0644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}

	// Verify project B files match project A
	gotAgents, _ := os.ReadFile(filepath.Join(projectB, "AGENTS.md"))
	if string(gotAgents) != agentsContent {
		t.Errorf("AGENTS.md mismatch: got %q", string(gotAgents))
	}
	gotClaude, _ := os.ReadFile(filepath.Join(projectB, "CLAUDE.md"))
	if string(gotClaude) != claudeContent {
		t.Errorf("CLAUDE.md mismatch: got %q", string(gotClaude))
	}
}

func TestPullUpdatesExisting(t *testing.T) {
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")

	// store original
	_ = v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md", Content: "old content",
	})

	// pull updated file
	_ = v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md", Content: "new content",
		UpdatedAt: time.Now(),
	})

	got, _ := v.GetInstruction("agents")
	if got.Content != "new content" {
		t.Errorf("after update: content = %q, want %q", got.Content, "new content")
	}
	// still only 1 instruction
	if len(v.ListInstructions()) != 1 {
		t.Errorf("len = %d, want 1", len(v.ListInstructions()))
	}
}

func TestPushCreatesSubdirectories(t *testing.T) {
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{
		Name:     "copilot",
		Filename: ".github/copilot-instructions.md",
		Content:  "Copilot rules here.",
	})

	targetDir := t.TempDir()
	for _, inst := range v.ListInstructions() {
		p := filepath.Join(targetDir, inst.Filename)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
		if err := os.WriteFile(p, []byte(inst.Content), 0644); err != nil {
			t.Fatalf("writing: %v", err)
		}
	}

	got, err := os.ReadFile(filepath.Join(targetDir, ".github", "copilot-instructions.md"))
	if err != nil {
		t.Fatalf("reading pushed file: %v", err)
	}
	if string(got) != "Copilot rules here." {
		t.Errorf("pushed content = %q", string(got))
	}
}

func TestDiffDetectsChanges(t *testing.T) {
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md", Content: "vault version",
	})
	_ = v.SetInstruction(agent.InstructionFile{
		Name: "claude", Filename: "CLAUDE.md", Content: "vault claude",
	})

	dir := t.TempDir()
	// agents: identical
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("vault version"), 0644)
	// claude: different
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("disk version"), 0644)

	instructions := v.ListInstructions()
	identical := 0
	differs := 0
	missing := 0

	for _, inst := range instructions {
		p := filepath.Join(dir, inst.Filename)
		diskData, err := os.ReadFile(p)
		if err != nil {
			missing++
			continue
		}
		if string(diskData) == inst.Content {
			identical++
		} else {
			differs++
		}
	}

	if identical != 1 {
		t.Errorf("identical = %d, want 1", identical)
	}
	if differs != 1 {
		t.Errorf("differs = %d, want 1", differs)
	}
	if missing != 0 {
		t.Errorf("missing = %d, want 0", missing)
	}
}

func TestDiffDetectsMissingOnDisk(t *testing.T) {
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{
		Name: "claude", Filename: "CLAUDE.md", Content: "in vault only",
	})

	dir := t.TempDir() // empty directory, no CLAUDE.md

	instructions := v.ListInstructions()
	missing := 0
	for _, inst := range instructions {
		p := filepath.Join(dir, inst.Filename)
		if _, err := os.ReadFile(p); err != nil {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("missing = %d, want 1", missing)
	}
}

func TestMultipleInstructionFilesCoexist(t *testing.T) {
	vaultPath := tempVaultPath(t)
	v := New(vaultPath)
	_ = v.Init("master")

	names := []string{"agents", "claude", "codex", "copilot"}
	for _, name := range names {
		_ = v.SetInstruction(agent.InstructionFile{
			Name:     name,
			Filename: agent.FilenameForInstruction(name),
			Content:  "Instructions for " + name,
		})
	}

	if len(v.ListInstructions()) != 4 {
		t.Fatalf("len = %d, want 4", len(v.ListInstructions()))
	}

	// reopen and verify all persist
	reopenedVault := New(vaultPath)
	_ = reopenedVault.Unlock("master")
	if len(reopenedVault.ListInstructions()) != 4 {
		t.Fatalf("after reopen: len = %d, want 4", len(reopenedVault.ListInstructions()))
	}
	for _, name := range names {
		inst, ok := reopenedVault.GetInstruction(name)
		if !ok {
			t.Errorf("instruction %q not found after reopen", name)
			continue
		}
		want := "Instructions for " + name
		if inst.Content != want {
			t.Errorf("%s content = %q, want %q", name, inst.Content, want)
		}
	}
}
