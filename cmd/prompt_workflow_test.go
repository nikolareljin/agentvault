package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

func TestResolvePromptWorkflowContextForIssueUsesRepoTemplateAndGitHubContext(t *testing.T) {
	setPromptWorkflowTestConfigDir(t, t.TempDir())

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "implement_issue.txt"), []byte("repo issue workflow\n"), 0644); err != nil {
		t.Fatalf("WriteFile(implement_issue.txt): %v", err)
	}

	origLookPath := promptWorkflowLookPath
	promptWorkflowLookPath = func(file string) (string, error) {
		if file == "gh" || file == "git" {
			return file, nil
		}
		return origLookPath(file)
	}
	origCommandContext := promptWorkflowCommandContext
	promptWorkflowCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "git" || name == "gh" {
			helperArgs := append([]string{"-test.run=TestPromptWorkflowHelperProcess", "--", name}, args...)
			cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"PROMPT_WORKFLOW_TEST_REPO_DIR="+repoDir,
			)
			return cmd
		}
		return origCommandContext(ctx, name, args...)
	}
	defer func() {
		promptWorkflowLookPath = origLookPath
		promptWorkflowCommandContext = origCommandContext
	}()

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

func TestPromptWorkflowHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || sep+1 >= len(args) {
		fmt.Fprintln(os.Stderr, "missing helper command")
		os.Exit(2)
	}

	command := args[sep+1]
	commandArgs := args[sep+2:]
	repoDir := os.Getenv("PROMPT_WORKFLOW_TEST_REPO_DIR")

	switch command {
	case "git":
		if repoDir == "" {
			fmt.Fprintln(os.Stderr, "missing test repo dir")
			os.Exit(2)
		}
		switch strings.Join(commandArgs, " ") {
		case "rev-parse --show-toplevel":
			fmt.Fprintln(os.Stdout, repoDir)
			os.Exit(0)
		case "rev-parse --abbrev-ref HEAD":
			fmt.Fprintln(os.Stdout, "main")
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unexpected git invocation: %s\n", strings.Join(commandArgs, " "))
			os.Exit(1)
		}
	case "gh":
		if got := strings.Join(commandArgs, " "); got != "issue view --json number,title,body,url -- 16" {
			fmt.Fprintf(os.Stderr, "unexpected gh invocation: %s\n", got)
			os.Exit(1)
		}
		payload := promptWorkflowIssue{
			Number: 16,
			Title:  "Add guided workflow",
			Body:   "Implement issue automation.",
			URL:    "https://example.test/issues/16",
		}
		if err := json.NewEncoder(os.Stdout).Encode(payload); err != nil {
			fmt.Fprintf(os.Stderr, "encoding helper payload: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unexpected helper command: %s\n", command)
		os.Exit(1)
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

func TestResolvePromptWorkflowContextRejectsMissingRepoPath(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", "implement_issue"); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}
	if err := cmd.Flags().Set("repo", filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}
	if err := cmd.Flags().Set("issue", "16"); err != nil {
		t.Fatalf("setting issue flag: %v", err)
	}

	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("resolvePromptInput() error = %v, want missing repo path guardrail", err)
	}
}

func TestResolvePromptInputRejectsWorkflowOnlyFlagsWithoutWorkflow(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("repo", t.TempDir()); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}

	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), "--repo, --issue, and --pr can only be used together with --workflow") {
		t.Fatalf("resolvePromptInput() error = %v, want workflow-only flag guardrail", err)
	}
}

func TestResolvePromptInputRejectsEmptyWorkflowValue(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", ""); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}

	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), "--workflow cannot be empty") {
		t.Fatalf("resolvePromptInput() error = %v, want empty workflow guardrail", err)
	}
}

func TestResolvePromptWorkflowContextRequiresGitBinary(t *testing.T) {
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

	origLookPath := promptWorkflowLookPath
	promptWorkflowLookPath = func(file string) (string, error) {
		if file == "git" {
			return "", exec.ErrNotFound
		}
		return origLookPath(file)
	}
	defer func() {
		promptWorkflowLookPath = origLookPath
	}()

	if _, _, err := resolvePromptInput(cmd); err == nil || !strings.Contains(err.Error(), "git binary not found in PATH") {
		t.Fatalf("resolvePromptInput() error = %v, want missing git guardrail", err)
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

func setPromptWorkflowTestConfigDir(t *testing.T, dir string) {
	t.Helper()

	orig, err := rootCmd.PersistentFlags().GetString("config")
	if err != nil {
		t.Fatalf("reading config flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("config", dir); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", orig)
	})
}
