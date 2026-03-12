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
	"time"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

func TestResolvePromptWorkflowContextForIssueUsesRepoTemplateAndGitHubContext(t *testing.T) {
	setPromptWorkflowTestConfigDir(t, t.TempDir())

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "implement_issue.txt"), []byte("repo issue workflow\n"), 0644); err != nil {
		t.Fatalf("WriteFile(implement_issue.txt): %v", err)
	}

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

	prompt, warnings, err := resolvePromptInputWithDeps(cmd, newPromptWorkflowTestDeps(repoDir))
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

func TestResolvePromptWorkflowContextForPRUsesGitHubPRContext(t *testing.T) {
	setPromptWorkflowTestConfigDir(t, t.TempDir())

	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "implement_pr.txt"), []byte("repo pr workflow\n"), 0644); err != nil {
		t.Fatalf("WriteFile(implement_pr.txt): %v", err)
	}

	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", "implement_pr"); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}
	if err := cmd.Flags().Set("repo", repoDir); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}
	if err := cmd.Flags().Set("pr", "25"); err != nil {
		t.Fatalf("setting pr flag: %v", err)
	}
	if err := cmd.Flags().Set("text", "Focus on unresolved comments."); err != nil {
		t.Fatalf("setting text flag: %v", err)
	}

	prompt, warnings, err := resolvePromptInputWithDeps(cmd, newPromptWorkflowTestDeps(repoDir))
	if err != nil {
		t.Fatalf("resolvePromptInput() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("resolvePromptInput() warnings = %v, want none", warnings)
	}
	for _, want := range []string{
		"Workflow: implement_pr",
		"PR Number: 25",
		"PR Branches: release/0.5.2 -> main",
		"Resolve workflow review threads.",
		"Focus on unresolved comments.",
		"repo pr workflow",
		"Review all unresolved PR comments and conversations before changing code.",
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

	if os.Getenv("PROMPT_WORKFLOW_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "simulated helper failure")
		os.Exit(1)
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
		switch got := strings.Join(commandArgs, " "); got {
		case "issue view --json number,title,body,url -- 16":
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
		case "pr view --json number,title,body,url,headRefName,baseRefName -- 25":
			payload := promptWorkflowPR{
				Number:      25,
				Title:       "Prompt workflow hardening",
				Body:        "Resolve workflow review threads.",
				URL:         "https://example.test/pr/25",
				HeadRefName: "release/0.5.2",
				BaseRefName: "main",
			}
			if err := json.NewEncoder(os.Stdout).Encode(payload); err != nil {
				fmt.Fprintf(os.Stderr, "encoding helper payload: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unexpected gh invocation: %s\n", got)
			os.Exit(1)
		}
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
	if _, _, err := resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps()); err == nil || !strings.Contains(err.Error(), `workflow "implement_pr" requires --pr`) {
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

	if _, _, err := resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps()); err == nil || !strings.Contains(err.Error(), "is not inside a git repository") {
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

	if _, _, err := resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps()); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("resolvePromptInput() error = %v, want missing repo path guardrail", err)
	}
}

func TestResolvePromptInputRejectsWorkflowOnlyFlagsWithoutWorkflow(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("repo", t.TempDir()); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}

	if _, _, err := resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps()); err == nil || !strings.Contains(err.Error(), "--repo, --issue, and --pr can only be used together with --workflow") {
		t.Fatalf("resolvePromptInput() error = %v, want workflow-only flag guardrail", err)
	}
}

func TestResolvePromptInputRejectsEmptyWorkflowValue(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("workflow", ""); err != nil {
		t.Fatalf("setting workflow flag: %v", err)
	}

	if _, _, err := resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps()); err == nil || !strings.Contains(err.Error(), "--workflow cannot be empty") {
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

	deps := promptWorkflowDeps{
		lookPath: func(file string) (string, error) {
			if file == "git" {
				return "", exec.ErrNotFound
			}
			return file, nil
		},
		commandContext: exec.CommandContext,
	}

	if _, _, err := resolvePromptInputWithDeps(cmd, deps); err == nil || !strings.Contains(err.Error(), "git binary not found in PATH") {
		t.Fatalf("resolvePromptInput() error = %v, want missing git guardrail", err)
	}
}

func TestRunPromptWorkflowCommandUsesEnvAndIncludesCommandOnFailure(t *testing.T) {
	cmd := newPromptWorkflowTestCommand()
	if err := cmd.Flags().Set("timeout", "5m"); err != nil {
		t.Fatalf("setting timeout flag: %v", err)
	}
	t.Setenv("AGENTVAULT_PROMPT_WORKFLOW_TIMEOUT", "45s")

	deps := promptWorkflowDeps{
		lookPath: func(file string) (string, error) { return file, nil },
		commandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if deadline, ok := ctx.Deadline(); !ok {
				t.Fatalf("expected workflow command deadline")
			} else if remaining := time.Until(deadline); remaining < 40*time.Second || remaining > 46*time.Second {
				t.Fatalf("unexpected workflow command timeout window: %s", remaining)
			}
			helperArgs := append([]string{"-test.run=TestPromptWorkflowHelperProcess", "--", name}, args...)
			helper := exec.CommandContext(ctx, os.Args[0], helperArgs...)
			helper.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"PROMPT_WORKFLOW_HELPER_FAIL=1",
			)
			return helper
		},
	}

	_, err := runPromptWorkflowCommand(context.Background(), t.TempDir(), cmd, deps, "gh", "issue", "view", "--json", "number,title", "--", "16")
	if err == nil {
		t.Fatal("runPromptWorkflowCommand() error = nil, want command failure")
	}
	if !strings.Contains(err.Error(), "gh issue view --json number,title -- 16 failed:") {
		t.Fatalf("runPromptWorkflowCommand() error = %v, want command text", err)
	}
	if !strings.Contains(err.Error(), "simulated helper failure") {
		t.Fatalf("runPromptWorkflowCommand() error = %v, want stderr text", err)
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
	cmd.Flags().Duration("timeout", 5*time.Minute, "")
	return cmd
}

func newPromptWorkflowTestDeps(repoDir string) promptWorkflowDeps {
	return promptWorkflowDeps{
		lookPath: func(file string) (string, error) {
			if file == "gh" || file == "git" {
				return file, nil
			}
			return exec.LookPath(file)
		},
		commandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name != "git" && name != "gh" {
				return exec.CommandContext(ctx, name, args...)
			}
			helperArgs := append([]string{"-test.run=TestPromptWorkflowHelperProcess", "--", name}, args...)
			helper := exec.CommandContext(ctx, os.Args[0], helperArgs...)
			helper.Env = append(os.Environ(),
				"GO_WANT_HELPER_PROCESS=1",
				"PROMPT_WORKFLOW_TEST_REPO_DIR="+repoDir,
			)
			return helper
		},
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
