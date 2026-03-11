package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

func TestResolvePromptWorkflowContextForIssueUsesRepoTemplateAndGitHubContext(t *testing.T) {
	repoDir := initPromptWorkflowGitRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "implement_issue.txt"), []byte("repo issue workflow\n"), 0644); err != nil {
		t.Fatalf("WriteFile(implement_issue.txt): %v", err)
	}

	binDir := t.TempDir()
	writePromptWorkflowStub(t, filepath.Join(binDir, "gh"), `#!/bin/sh
case "$1 $2 $3" in
  "issue view 16")
    printf '%s\n' '{"number":16,"title":"Add guided workflow","body":"Implement issue automation.","url":"https://example.test/issues/16"}'
    ;;
  *)
    echo "unexpected gh invocation: $*" >&2
    exit 1
    ;;
esac
`)

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath)

	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", "implement_issue"); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}
	if err := cmd.Flags().Set("repo", repoDir); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}
	if err := cmd.Flags().Set("issue", "16"); err != nil {
		t.Fatalf("setting issue flag: %v", err)
	}
	if err := cmd.Flags().Set("text", "Keep the implementation minimal."); err != nil {
		t.Fatalf("setting text flag: %v", err)
	}

	prompt, warnings, err := resolvePromptInput(cmd)
	if err != nil {
		t.Fatalf("resolvePromptInput() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("resolvePromptInput() warnings = %v, want none", warnings)
	}
	for _, want := range []string{
		"Workflow: implement_issue",
		"Issue Number: 16",
		"Issue Title: Add guided workflow",
		"Implement issue automation.",
		"Keep the implementation minimal.",
		"repo issue workflow",
		"1. Intake",
		"5. Delivery",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestResolvePromptWorkflowContextForPRRequiresPRFlag(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", "implement_pr"); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}
	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), `workflow "implement_pr" requires --pr`) {
		t.Fatalf("resolvePromptInput() error = %v, want missing --pr guardrail", err)
	}
}

func TestResolvePromptWorkflowContextRejectsNonGitRepo(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", "implement_issue"); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}
	if err := cmd.Flags().Set("repo", t.TempDir()); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}
	if err := cmd.Flags().Set("issue", "16"); err != nil {
		t.Fatalf("setting issue flag: %v", err)
	}

	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), "is not inside a git repository") {
		t.Fatalf("resolvePromptInput() error = %v, want non-git repo guardrail", err)
	}
}

func TestBuildPromptWorkflowForPRIncludesCanonicalTemplateAndReviewDirective(t *testing.T) {
	prompt := buildPromptWorkflow(promptWorkflowContext{
		Kind:          promptWorkflowImplementPR,
		RepoRoot:      "/tmp/repo",
		RepoName:      "repo",
		CurrentBranch: "release/0.5.2",
		Template:      workflowTemplateForTest("implement_pr", "Implement PR Template\nStep A\nStep B\n"),
		PR: &promptWorkflowPR{
			Number:      28,
			Title:       "Fix workflow issues",
			Body:        "Resolve review comments.",
			URL:         "https://example.test/pr/28",
			HeadRefName: "release/0.5.2",
			BaseRefName: "main",
		},
	})

	for _, want := range []string{
		"PR Number: 28",
		"PR Branches: release/0.5.2 -> main",
		"Resolve review comments.",
		"Review all unresolved PR comments and conversations before changing code.",
		"Implement PR Template",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func newPromptWorkflowTestCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("text", "", "")
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("workflow", "", "")
	cmd.Flags().String("repo", "", "")
	cmd.Flags().String("issue", "", "")
	cmd.Flags().String("pr", "", "")
	return cmd
}

func initPromptWorkflowGitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runPromptWorkflowTestCommand(t, repoDir, "git", "init", "-q", "-b", "main")
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		runPromptWorkflowTestCommand(t, repoDir, "git", "init", "-q")
		runPromptWorkflowTestCommand(t, repoDir, "git", "checkout", "-b", "main")
	}
	runPromptWorkflowTestCommand(t, repoDir, "git", "config", "user.email", "test@example.com")
	runPromptWorkflowTestCommand(t, repoDir, "git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatalf("WriteFile(README.md): %v", err)
	}
	runPromptWorkflowTestCommand(t, repoDir, "git", "add", "README.md")
	runPromptWorkflowTestCommand(t, repoDir, "git", "commit", "-qm", "init")
	return repoDir
}

func runPromptWorkflowTestCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v (%s)", name, args, err, strings.TrimSpace(string(out)))
	}
}

func writePromptWorkflowStub(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func workflowTemplateForTest(key string, content string) workflowtemplates.ResolvedTemplate {
	filename, ok := workflowtemplates.FindTemplateFilename(key)
	if !ok {
		filename = key + ".txt"
	}
	return workflowtemplates.ResolvedTemplate{
		TemplateAsset: workflowtemplates.TemplateAsset{
			Key:      key,
			Filename: filename,
			Version:  "repo-local",
			Content:  content,
		},
		Source: "repo-local",
	}
}
