package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func tempVaultPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test-vault.enc")
}

func TestNew(t *testing.T) {
	v := New("/tmp/test.enc")
	if v.Path() != "/tmp/test.enc" {
		t.Errorf("Path() = %q, want %q", v.Path(), "/tmp/test.enc")
	}
}

func TestInitCreatesFile(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !v.Exists() {
		t.Fatal("Init() did not create vault file")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("vault file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := v.Init("master"); err == nil {
		t.Error("Init() should fail when vault already exists")
	}
}

func TestUnlockEmptyVault(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	v2 := New(path)
	if err := v2.Unlock("master"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	if len(v2.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(v2.List()))
	}
}

func TestUnlockWrongPassword(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("correct"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	v2 := New(path)
	if err := v2.Unlock("wrong"); err == nil {
		t.Error("Unlock() should fail with wrong password")
	}
}

func TestAddAndList(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	a := agent.Agent{
		Name:      "test-claude",
		Provider:  agent.ProviderClaude,
		Model:     "claude-3-opus",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := v.Add(a); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	agents := v.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}
	if agents[0].Name != "test-claude" {
		t.Errorf("agent name = %q, want %q", agents[0].Name, "test-claude")
	}
}

func TestAddDuplicate(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	a := agent.Agent{Name: "dup", Provider: agent.ProviderOpenAI}
	_ = v.Add(a)
	if err := v.Add(a); err == nil {
		t.Error("Add() should fail for duplicate agent name")
	}
}

func TestAddPersistsAcrossUnlock(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	a := agent.Agent{
		Name:     "persisted",
		Provider: agent.ProviderGemini,
		Model:    "gemini-pro",
		APIKey:   "sk-secret-key",
	}
	if err := v.Add(a); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	v2 := New(path)
	if err := v2.Unlock("master"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	agents := v2.List()
	if len(agents) != 1 {
		t.Fatalf("after reopen: List() len = %d, want 1", len(agents))
	}
	if agents[0].APIKey != "sk-secret-key" {
		t.Errorf("after reopen: APIKey = %q, want %q", agents[0].APIKey, "sk-secret-key")
	}
}

func TestGet(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.Add(agent.Agent{Name: "a1", Provider: agent.ProviderClaude})
	_ = v.Add(agent.Agent{Name: "a2", Provider: agent.ProviderOpenAI})

	got, ok := v.Get("a1")
	if !ok {
		t.Fatal("Get() not found")
	}
	if got.Name != "a1" {
		t.Errorf("Get() name = %q, want %q", got.Name, "a1")
	}
	_, ok = v.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for unknown agent")
	}
}

func TestUpdate(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.Add(agent.Agent{Name: "up", Provider: agent.ProviderClaude, Model: "old"})

	updated := agent.Agent{Name: "up", Provider: agent.ProviderClaude, Model: "new"}
	if err := v.Update(updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, _ := v.Get("up")
	if got.Model != "new" {
		t.Errorf("after Update() model = %q, want %q", got.Model, "new")
	}
}

func TestUpdateNotFound(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	err := v.Update(agent.Agent{Name: "ghost", Provider: agent.ProviderClaude})
	if err == nil {
		t.Error("Update() should fail for unknown agent")
	}
}

func TestRemove(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.Add(agent.Agent{Name: "keep", Provider: agent.ProviderClaude})
	_ = v.Add(agent.Agent{Name: "drop", Provider: agent.ProviderOpenAI})

	if err := v.Remove("drop"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if len(v.List()) != 1 {
		t.Fatalf("after Remove() len = %d, want 1", len(v.List()))
	}
	if v.List()[0].Name != "keep" {
		t.Errorf("remaining agent = %q, want %q", v.List()[0].Name, "keep")
	}
}

func TestRemovePersists(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.Add(agent.Agent{Name: "a1", Provider: agent.ProviderClaude})
	_ = v.Add(agent.Agent{Name: "a2", Provider: agent.ProviderOpenAI})
	_ = v.Remove("a1")

	v2 := New(path)
	_ = v2.Unlock("master")
	if len(v2.List()) != 1 {
		t.Fatalf("after reopen: len = %d, want 1", len(v2.List()))
	}
}

func TestRemoveNotFound(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	if err := v.Remove("ghost"); err == nil {
		t.Error("Remove() should fail for unknown agent")
	}
}

func TestSaveWithoutUnlock(t *testing.T) {
	v := New("/tmp/never-created.enc")
	if err := v.Save(); err == nil {
		t.Error("Save() should fail when vault is not unlocked")
	}
}

func TestSharedConfig(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")

	shared := v.SharedConfig()
	if shared.SystemPrompt != "" {
		t.Errorf("initial shared prompt = %q, want empty", shared.SystemPrompt)
	}

	sc := agent.SharedConfig{
		SystemPrompt: "Be helpful.",
		MCPServers: []agent.MCPServer{
			{Name: "fs", Command: "npx", Args: []string{"-y", "mcp-fs"}},
		},
	}
	if err := v.SetSharedConfig(sc); err != nil {
		t.Fatalf("SetSharedConfig() error = %v", err)
	}

	// persists across reopen
	v2 := New(path)
	_ = v2.Unlock("master")
	shared2 := v2.SharedConfig()
	if shared2.SystemPrompt != "Be helpful." {
		t.Errorf("after reopen: shared prompt = %q, want %q", shared2.SystemPrompt, "Be helpful.")
	}
	if len(shared2.MCPServers) != 1 {
		t.Fatalf("after reopen: shared MCPs len = %d, want 1", len(shared2.MCPServers))
	}
	if shared2.MCPServers[0].Name != "fs" {
		t.Errorf("after reopen: MCP name = %q, want %q", shared2.MCPServers[0].Name, "fs")
	}
}

func TestExportData(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.Add(agent.Agent{Name: "exp1", Provider: agent.ProviderClaude, Model: "opus"})
	_ = v.Add(agent.Agent{Name: "exp2", Provider: agent.ProviderOpenAI, APIKey: "sk-key"})

	data, err := v.ExportData()
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("ExportData() did not return valid JSON")
	}

	var vd struct {
		Agents []agent.Agent      `json:"agents"`
		Shared agent.SharedConfig `json:"shared"`
	}
	if err := json.Unmarshal(data, &vd); err != nil {
		t.Fatalf("unmarshaling export: %v", err)
	}
	if len(vd.Agents) != 2 {
		t.Errorf("export agents len = %d, want 2", len(vd.Agents))
	}
}

func TestImportData(t *testing.T) {
	// create source vault
	srcPath := tempVaultPath(t)
	src := New(srcPath)
	_ = src.Init("src")
	_ = src.Add(agent.Agent{Name: "imp1", Provider: agent.ProviderClaude})
	_ = src.Add(agent.Agent{Name: "imp2", Provider: agent.ProviderGemini})
	_ = src.SetSharedConfig(agent.SharedConfig{
		SystemPrompt: "Imported prompt.",
		MCPServers:   []agent.MCPServer{{Name: "imported-mcp", Command: "test"}},
	})
	exportJSON, _ := src.ExportData()

	// create target vault with one overlapping agent
	dstPath := tempVaultPath(t)
	dst := New(dstPath)
	_ = dst.Init("dst")
	_ = dst.Add(agent.Agent{Name: "imp1", Provider: agent.ProviderOpenAI}) // same name

	imported, skipped, err := dst.ImportData(exportJSON)
	if err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}
	if imported != 1 {
		t.Errorf("imported = %d, want 1", imported)
	}
	if len(skipped) != 1 || skipped[0] != "imp1" {
		t.Errorf("skipped = %v, want [imp1]", skipped)
	}

	// verify the import
	agents := dst.List()
	if len(agents) != 2 {
		t.Fatalf("after import: len = %d, want 2", len(agents))
	}
	// first agent should be the original (not overwritten)
	if agents[0].Provider != agent.ProviderOpenAI {
		t.Errorf("original agent provider = %q, want openai", agents[0].Provider)
	}

	// shared config should be merged
	shared := dst.SharedConfig()
	if shared.SystemPrompt != "Imported prompt." {
		t.Errorf("shared prompt = %q, want %q", shared.SystemPrompt, "Imported prompt.")
	}
	if len(shared.MCPServers) != 1 {
		t.Errorf("shared MCPs len = %d, want 1", len(shared.MCPServers))
	}
}

func TestImportDataDoesNotOverwriteExistingSharedPrompt(t *testing.T) {
	dstPath := tempVaultPath(t)
	dst := New(dstPath)
	_ = dst.Init("dst")
	_ = dst.SetSharedConfig(agent.SharedConfig{SystemPrompt: "Existing."})

	importJSON := `{"agents":[],"shared":{"system_prompt":"New."}}`
	_, _, err := dst.ImportData([]byte(importJSON))
	if err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}
	if dst.SharedConfig().SystemPrompt != "Existing." {
		t.Errorf("shared prompt = %q, should not be overwritten", dst.SharedConfig().SystemPrompt)
	}
}

func TestImportDataDeduplicatesImportedSessionIDs(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	importJSON := `{
		"agents": [],
		"shared": {},
		"sessions": {
			"sessions": [
				{"id":"sess-1","name":"A","project_dir":"/tmp/a","agents":[],"status":"idle"},
				{"id":"sess-1","name":"B","project_dir":"/tmp/b","agents":[],"status":"idle"}
			]
		}
	}`
	if _, _, err := v.ImportData([]byte(importJSON)); err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}

	sessions := v.Sessions().Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "sess-1" {
		t.Fatalf("session ID = %q, want sess-1", sessions[0].ID)
	}
}

func TestImportDataDoesNotOverwriteUnlimitedParallelLimit(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := v.SetSessions(agent.SessionConfig{
		DefaultAgents: []string{"claude"},
		ParallelLimit: 0, // meaningful: unlimited
	}); err != nil {
		t.Fatalf("SetSessions() error = %v", err)
	}

	importJSON := `{
		"agents": [],
		"shared": {},
		"sessions": {"parallel_limit": 4}
	}`
	if _, _, err := v.ImportData([]byte(importJSON)); err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}

	if got := v.Sessions().ParallelLimit; got != 0 {
		t.Fatalf("ParallelLimit = %d, want 0", got)
	}
}

func TestImportDataImportsParallelLimitForEmptySessionConfig(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	importJSON := `{
		"agents": [],
		"shared": {},
		"sessions": {"parallel_limit": 4}
	}`
	if _, _, err := v.ImportData([]byte(importJSON)); err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}

	if got := v.Sessions().ParallelLimit; got != 4 {
		t.Fatalf("ParallelLimit = %d, want 4", got)
	}
	if !v.Sessions().ParallelLimitSet {
		t.Fatal("ParallelLimitSet = false, want true")
	}
}

func TestImportDataImportsParallelLimitWhenDefaultAgentsAlsoProvided(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	importJSON := `{
		"agents": [],
		"shared": {},
		"sessions": {
			"default_agents": ["claude"],
			"parallel_limit": 3
		}
	}`
	if _, _, err := v.ImportData([]byte(importJSON)); err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}

	sessions := v.Sessions()
	if got := sessions.ParallelLimit; got != 3 {
		t.Fatalf("ParallelLimit = %d, want 3", got)
	}
	if !sessions.ParallelLimitSet {
		t.Fatal("ParallelLimitSet = false, want true")
	}
	if len(sessions.DefaultAgents) != 1 || sessions.DefaultAgents[0] != "claude" {
		t.Fatalf("DefaultAgents = %v, want [claude]", sessions.DefaultAgents)
	}
}

func TestImportDataDoesNotOverwriteExplicitUnlimitedParallelLimit(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	if err := v.Init("master"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := v.SetSessions(agent.SessionConfig{
		ParallelLimit:    0,
		ParallelLimitSet: true,
	}); err != nil {
		t.Fatalf("SetSessions() error = %v", err)
	}

	importJSON := `{
		"agents": [],
		"shared": {},
		"sessions": {"parallel_limit": 4}
	}`
	if _, _, err := v.ImportData([]byte(importJSON)); err != nil {
		t.Fatalf("ImportData() error = %v", err)
	}

	sessions := v.Sessions()
	if got := sessions.ParallelLimit; got != 0 {
		t.Fatalf("ParallelLimit = %d, want 0", got)
	}
	if !sessions.ParallelLimitSet {
		t.Fatal("ParallelLimitSet = false, want true")
	}
}

func TestExistsReturnsFalse(t *testing.T) {
	v := New("/tmp/nonexistent-vault-" + t.Name() + ".enc")
	if v.Exists() {
		t.Error("Exists() should return false for nonexistent path")
	}
}

func TestSetAndGetInstruction(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")

	inst := agent.InstructionFile{
		Name:      "agents",
		Filename:  "AGENTS.md",
		Content:   "# Repository Guidelines\nBe consistent.",
		UpdatedAt: time.Now(),
	}
	if err := v.SetInstruction(inst); err != nil {
		t.Fatalf("SetInstruction() error = %v", err)
	}
	got, ok := v.GetInstruction("agents")
	if !ok {
		t.Fatal("GetInstruction() not found")
	}
	if got.Content != inst.Content {
		t.Errorf("content = %q, want %q", got.Content, inst.Content)
	}

	// update existing
	inst.Content = "# Updated Guidelines"
	if err := v.SetInstruction(inst); err != nil {
		t.Fatalf("SetInstruction() update error = %v", err)
	}
	got, _ = v.GetInstruction("agents")
	if got.Content != "# Updated Guidelines" {
		t.Errorf("after update: content = %q", got.Content)
	}
	// should still be 1 instruction, not 2
	if len(v.ListInstructions()) != 1 {
		t.Errorf("ListInstructions() len = %d, want 1", len(v.ListInstructions()))
	}
}

func TestInstructionPersistsAcrossUnlock(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{
		Name:     "claude",
		Filename: "CLAUDE.md",
		Content:  "Be thorough and precise.",
	})

	v2 := New(path)
	_ = v2.Unlock("master")
	got, ok := v2.GetInstruction("claude")
	if !ok {
		t.Fatal("instruction not found after reopen")
	}
	if got.Content != "Be thorough and precise." {
		t.Errorf("after reopen: content = %q", got.Content)
	}
}

func TestRemoveInstruction(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "x"})
	_ = v.SetInstruction(agent.InstructionFile{Name: "claude", Filename: "CLAUDE.md", Content: "y"})

	if err := v.RemoveInstruction("agents"); err != nil {
		t.Fatalf("RemoveInstruction() error = %v", err)
	}
	if len(v.ListInstructions()) != 1 {
		t.Fatalf("after remove: len = %d, want 1", len(v.ListInstructions()))
	}
	_, ok := v.GetInstruction("agents")
	if ok {
		t.Error("agents instruction should be gone")
	}
}

func TestRemoveInstructionNotFound(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	if err := v.RemoveInstruction("ghost"); err == nil {
		t.Error("RemoveInstruction() should fail for unknown name")
	}
}

func TestExportIncludesInstructions(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	_ = v.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "exported"})

	data, _ := v.ExportData()
	var vd struct {
		Shared struct {
			Instructions []agent.InstructionFile `json:"instructions"`
		} `json:"shared"`
	}
	_ = json.Unmarshal(data, &vd)
	if len(vd.Shared.Instructions) != 1 {
		t.Fatalf("export instructions len = %d, want 1", len(vd.Shared.Instructions))
	}
	if vd.Shared.Instructions[0].Content != "exported" {
		t.Errorf("exported content = %q", vd.Shared.Instructions[0].Content)
	}
}

func TestImportMergesInstructions(t *testing.T) {
	srcPath := tempVaultPath(t)
	src := New(srcPath)
	_ = src.Init("src")
	_ = src.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "from-src"})
	_ = src.SetInstruction(agent.InstructionFile{Name: "claude", Filename: "CLAUDE.md", Content: "from-src-claude"})
	exportJSON, _ := src.ExportData()

	dstPath := tempVaultPath(t)
	dst := New(dstPath)
	_ = dst.Init("dst")
	// dst already has "agents" - should not be overwritten
	_ = dst.SetInstruction(agent.InstructionFile{Name: "agents", Filename: "AGENTS.md", Content: "existing"})

	_, _, _ = dst.ImportData(exportJSON)

	// "agents" should keep its existing content
	got, _ := dst.GetInstruction("agents")
	if got.Content != "existing" {
		t.Errorf("agents content = %q, should not be overwritten", got.Content)
	}
	// "claude" should be imported
	got2, ok := dst.GetInstruction("claude")
	if !ok {
		t.Fatal("claude instruction should be imported")
	}
	if got2.Content != "from-src-claude" {
		t.Errorf("claude content = %q, want %q", got2.Content, "from-src-claude")
	}
}

func TestAddWithMCPServers(t *testing.T) {
	path := tempVaultPath(t)
	v := New(path)
	_ = v.Init("master")
	a := agent.Agent{
		Name:     "mcp-agent",
		Provider: agent.ProviderClaude,
		MCPServers: []agent.MCPServer{
			{Name: "fs", Command: "npx", Args: []string{"-y", "mcp-fs"}},
			{Name: "git", Command: "npx", Args: []string{"-y", "mcp-git"}},
		},
	}
	if err := v.Add(a); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// reopen and verify MCP servers persist
	v2 := New(path)
	_ = v2.Unlock("master")
	got, ok := v2.Get("mcp-agent")
	if !ok {
		t.Fatal("agent not found after reopen")
	}
	if len(got.MCPServers) != 2 {
		t.Fatalf("MCPServers len = %d, want 2", len(got.MCPServers))
	}
	if got.MCPServers[0].Name != "fs" {
		t.Errorf("MCP[0].Name = %q, want %q", got.MCPServers[0].Name, "fs")
	}
}
