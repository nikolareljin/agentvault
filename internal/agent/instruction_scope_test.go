package agent

import (
	"sort"
	"testing"
)

func TestResolveEffectiveInstructions_globalOnly(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Filename: "AGENTS.md", Content: "global agents"},
	}
	result := ResolveEffectiveInstructions(instructions, "")
	if len(result) != 1 || result[0].Content != "global agents" {
		t.Errorf("expected global instruction, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_directoryWinsOverGlobal(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "dir-specific", Scope: InstructionScopeDirectory, DirectoryPattern: "/home/user/Projects/myrepo"},
	}
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/myrepo")
	if len(result) != 1 || result[0].Content != "dir-specific" {
		t.Errorf("expected directory instruction to win, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_directoryNoMatch(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "dir-specific", Scope: InstructionScopeDirectory, DirectoryPattern: "/home/user/Projects/myrepo"},
	}
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/other")
	if len(result) != 1 || result[0].Content != "global" {
		t.Errorf("expected global to win when directory doesn't match, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_localAlwaysWins(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "dir-specific", Scope: InstructionScopeDirectory, DirectoryPattern: "/home/user/*"},
		{Name: "agents", Content: "local", Scope: InstructionScopeLocal},
	}
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/myrepo")
	if len(result) != 1 || result[0].Content != "local" {
		t.Errorf("expected local to win, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_multipleNames(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global agents", Scope: InstructionScopeGlobal},
		{Name: "claude", Content: "global claude", Scope: InstructionScopeGlobal},
		{Name: "claude", Content: "dir claude", Scope: InstructionScopeDirectory, DirectoryPattern: "/repo"},
	}
	result := ResolveEffectiveInstructions(instructions, "/repo")
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(result), result)
	}
	if result[0].Content != "global agents" {
		t.Errorf("agents: expected global, got %q", result[0].Content)
	}
	if result[1].Content != "dir claude" {
		t.Errorf("claude: expected dir-specific, got %q", result[1].Content)
	}
}

func TestResolveEffectiveInstructions_emptyWorkDirDisablesDirectory(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "dir", Scope: InstructionScopeDirectory, DirectoryPattern: "/repo"},
	}
	result := ResolveEffectiveInstructions(instructions, "")
	if len(result) != 1 || result[0].Content != "global" {
		t.Errorf("expected global when workDir is empty, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_wildcardPattern(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "wildcard", Scope: InstructionScopeDirectory, DirectoryPattern: "/home/user/Projects/*"},
	}
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/myrepo")
	if len(result) != 1 || result[0].Content != "wildcard" {
		t.Errorf("expected wildcard to match, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_directoryMatchesSubdir(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "repo-root", Scope: InstructionScopeDirectory, DirectoryPattern: "/home/user/Projects/myrepo"},
	}
	// workDir is a subdirectory of the pattern — should still match.
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/myrepo/src/pkg")
	if len(result) != 1 || result[0].Content != "repo-root" {
		t.Errorf("expected directory instruction to apply in subdirectory, got %+v", result)
	}
}

func TestResolveEffectiveInstructions_relativeDirectoryPatternWithSeparator(t *testing.T) {
	instructions := []InstructionFile{
		{Name: "agents", Content: "global", Scope: InstructionScopeGlobal},
		{Name: "agents", Content: "repo-subdir", Scope: InstructionScopeDirectory, DirectoryPattern: "myrepo/*"},
	}
	result := ResolveEffectiveInstructions(instructions, "/home/user/Projects/myrepo/src/pkg")
	if len(result) != 1 || result[0].Content != "repo-subdir" {
		t.Errorf("expected relative directory pattern to match absolute workDir suffix, got %+v", result)
	}
}

func TestValidateScopePattern_rejectsInvalidDirectoryGlob(t *testing.T) {
	err := ValidateScopePattern(InstructionScopeDirectory, "/home/user/[broken")
	if err == nil {
		t.Fatal("expected invalid glob pattern to fail validation")
	}
}

func TestValidateScopePattern_allowsNonTraversalDotDotPrefix(t *testing.T) {
	err := ValidateScopePattern(InstructionScopeDirectory, "..repo/*")
	if err != nil {
		t.Fatalf("expected non-traversal pattern to pass validation, got %v", err)
	}
}

func TestValidateScopePattern_rejectsLeadingParentSegment(t *testing.T) {
	for _, pattern := range []string{"..", "../repo/*"} {
		if err := ValidateScopePattern(InstructionScopeDirectory, pattern); err == nil {
			t.Fatalf("expected leading parent segment %q to fail validation", pattern)
		}
	}
}

func TestInstructionKey_normalizesDirectoryPatternSeparators(t *testing.T) {
	backslash := InstructionFile{
		Name:             "agents",
		Scope:            InstructionScopeDirectory,
		DirectoryPattern: `C:\repo\*`,
	}
	forwardSlash := InstructionFile{
		Name:             "agents",
		Scope:            InstructionScopeDirectory,
		DirectoryPattern: "C:/repo/*",
	}

	if InstructionKey(backslash) != InstructionKey(forwardSlash) {
		t.Fatalf("expected equivalent directory patterns to share an instruction key")
	}
}

func TestValidateInstructionScope_rejectsInvalidDirectoryGlob(t *testing.T) {
	inst := InstructionFile{
		Name:             "agents",
		Scope:            InstructionScopeDirectory,
		DirectoryPattern: "/repo/[broken",
	}
	err := ValidateInstructionScope(inst)
	if err == nil {
		t.Fatal("expected invalid glob pattern to fail instruction validation")
	}
}

func TestCheckInstructionConflicts_sameScope(t *testing.T) {
	existing := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeGlobal, Content: "old"},
	}
	incoming := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeGlobal, Content: "new"},
	}
	conflicts := CheckInstructionConflicts(existing, incoming)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Name != "agents" {
		t.Errorf("expected conflict on 'agents', got %q", conflicts[0].Name)
	}
}

func TestCheckInstructionConflicts_differentScope(t *testing.T) {
	existing := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeGlobal, Content: "old"},
	}
	incoming := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeDirectory, Content: "new", DirectoryPattern: "/repo"},
	}
	conflicts := CheckInstructionConflicts(existing, incoming)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for different scopes, got %+v", conflicts)
	}
}

func TestCheckInstructionConflicts_noOverlap(t *testing.T) {
	existing := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeGlobal},
	}
	incoming := []InstructionFile{
		{Name: "claude", Scope: InstructionScopeGlobal},
	}
	conflicts := CheckInstructionConflicts(existing, incoming)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for different names, got %+v", conflicts)
	}
}

func TestCheckInstructionConflicts_emptyScopeTreatedAsGlobal(t *testing.T) {
	existing := []InstructionFile{
		{Name: "agents", Scope: "", Content: "old"},
	}
	incoming := []InstructionFile{
		{Name: "agents", Scope: InstructionScopeGlobal, Content: "new"},
	}
	conflicts := CheckInstructionConflicts(existing, incoming)
	if len(conflicts) != 1 {
		t.Errorf("expected 1 conflict (empty scope = global), got %d", len(conflicts))
	}
}
