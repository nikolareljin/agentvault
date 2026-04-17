package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
)

type Source string

const (
	SourceWorkspaceFlag Source = "workspace_flag"
	SourceWorkflowRepo  Source = "workflow_repo"
	SourceCurrentDir    Source = "current_directory"
)

type Resolved struct {
	Path      string
	Source    Source
	IsGitRepo bool
}

func Resolve(explicitPath string, workflowRepo string, currentDir string) (Resolved, error) {
	switch {
	case strings.TrimSpace(explicitPath) != "":
		return resolvePath(explicitPath, SourceWorkspaceFlag)
	case strings.TrimSpace(workflowRepo) != "":
		return resolveGitRoot(workflowRepo, SourceWorkflowRepo)
	default:
		return resolvePath(currentDir, SourceCurrentDir)
	}
}

func ResolveCurrentDirDefault(currentDir string) (Resolved, error) {
	resolved, err := resolvePath(currentDir, SourceCurrentDir)
	if err != nil {
		return Resolved{}, err
	}
	if !resolved.IsGitRepo {
		return resolved, nil
	}
	gitRoot, err := findGitRoot(resolved.Path)
	if err != nil {
		return Resolved{}, err
	}
	resolved.Path = gitRoot
	return resolved, nil
}

func resolvePath(raw string, source Source) (Resolved, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return Resolved{}, fmt.Errorf("execution workspace path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolving execution workspace %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Resolved{}, fmt.Errorf("execution workspace %q does not exist", abs)
		}
		return Resolved{}, fmt.Errorf("stat execution workspace %q: %w", abs, err)
	}
	if !info.IsDir() {
		return Resolved{}, fmt.Errorf("execution workspace %q is not a directory", abs)
	}
	return Resolved{
		Path:      abs,
		Source:    source,
		IsGitRepo: agent.IsGitWorktree(abs),
	}, nil
}

func resolveGitRoot(raw string, source Source) (Resolved, error) {
	resolved, err := resolvePath(raw, source)
	if err != nil {
		return Resolved{}, err
	}
	if !resolved.IsGitRepo {
		return resolved, nil
	}
	gitRoot, err := findGitRoot(resolved.Path)
	if err != nil {
		return Resolved{}, err
	}
	resolved.Path = gitRoot
	return resolved, nil
}

func findGitRoot(dir string) (string, error) {
	current := dir
	for {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not resolve git root for %q", dir)
		}
		current = parent
	}
}
