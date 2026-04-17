package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersExplicitWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	workflowRepo := t.TempDir()
	currentDir := t.TempDir()

	got, err := Resolve(workspaceDir, workflowRepo, currentDir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Path != workspaceDir {
		t.Fatalf("path = %q, want %q", got.Path, workspaceDir)
	}
	if got.Source != SourceWorkspaceFlag {
		t.Fatalf("source = %q, want %q", got.Source, SourceWorkspaceFlag)
	}
}

func TestResolveUsesWorkflowRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git dir: %v", err)
	}
	nested := filepath.Join(repoRoot, "nested", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}

	got, err := Resolve("", nested, t.TempDir())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Path != repoRoot {
		t.Fatalf("path = %q, want %q", got.Path, repoRoot)
	}
	if got.Source != SourceWorkflowRepo {
		t.Fatalf("source = %q, want %q", got.Source, SourceWorkflowRepo)
	}
	if !got.IsGitRepo {
		t.Fatal("expected git repo resolution")
	}
}

func TestResolveFallsBackToCurrentDir(t *testing.T) {
	currentDir := t.TempDir()

	got, err := Resolve("", "", currentDir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Path != currentDir {
		t.Fatalf("path = %q, want %q", got.Path, currentDir)
	}
	if got.Source != SourceCurrentDir {
		t.Fatalf("source = %q, want %q", got.Source, SourceCurrentDir)
	}
}

func TestResolveCurrentDirDefaultUsesGitRoot(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git dir: %v", err)
	}
	nested := filepath.Join(repoRoot, "nested", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating nested dir: %v", err)
	}

	got, err := ResolveCurrentDirDefault(nested)
	if err != nil {
		t.Fatalf("ResolveCurrentDirDefault() error = %v", err)
	}
	if got.Path != repoRoot {
		t.Fatalf("path = %q, want %q", got.Path, repoRoot)
	}
	if got.Source != SourceCurrentDir {
		t.Fatalf("source = %q, want %q", got.Source, SourceCurrentDir)
	}
}

func TestResolveErrorsForMissingExplicitWorkspace(t *testing.T) {
	_, err := Resolve(filepath.Join(t.TempDir(), "missing"), "", t.TempDir())
	if err == nil {
		t.Fatal("expected missing workspace error")
	}
}
