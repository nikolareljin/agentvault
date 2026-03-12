package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

type promptWorkflowKind string

const (
	promptWorkflowImplementIssue promptWorkflowKind = "implement_issue"
	promptWorkflowImplementPR    promptWorkflowKind = "implement_pr"
)

const (
	defaultPromptWorkflowCommandTimeout = 30 * time.Second
	maxPromptWorkflowCommandTimeout     = 2 * time.Minute
)

type promptWorkflowDeps struct {
	lookPath       func(string) (string, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

type promptWorkflowIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

type promptWorkflowPR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
}

type promptWorkflowContext struct {
	Kind          promptWorkflowKind
	RepoRoot      string
	RepoName      string
	CurrentBranch string
	Template      workflowtemplates.ResolvedTemplate
	Warnings      []string
	OperatorNotes string
	Issue         *promptWorkflowIssue
	PR            *promptWorkflowPR
}

func resolvePromptInput(cmd *cobra.Command) (string, []string, error) {
	return resolvePromptInputWithDeps(cmd, defaultPromptWorkflowDeps())
}

func resolvePromptInputWithDeps(cmd *cobra.Command, deps promptWorkflowDeps) (string, []string, error) {
	workflowName, _ := cmd.Flags().GetString("workflow")
	workflowFlag := cmd.Flags().Lookup("workflow")
	if workflowFlag != nil && workflowFlag.Changed && strings.TrimSpace(workflowName) == "" {
		return "", nil, fmt.Errorf("--workflow cannot be empty")
	}
	if strings.TrimSpace(workflowName) == "" {
		if workflowOnlyFlagsChanged(cmd) {
			return "", nil, fmt.Errorf("--repo, --issue, and --pr can only be used together with --workflow")
		}
		text, err := readPromptInput(cmd)
		return text, nil, err
	}

	notes, _, err := readOptionalPromptInput(cmd)
	if err != nil {
		return "", nil, err
	}

	workflowCtx, err := resolvePromptWorkflowContext(cmd, workflowName, notes, deps)
	if err != nil {
		return "", nil, err
	}
	return buildPromptWorkflow(workflowCtx), workflowCtx.Warnings, nil
}

func workflowOnlyFlagsChanged(cmd *cobra.Command) bool {
	for _, name := range []string{"repo", "issue", "pr"} {
		flag := cmd.Flags().Lookup(name)
		if flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

func defaultPromptWorkflowDeps() promptWorkflowDeps {
	return promptWorkflowDeps{
		lookPath:       exec.LookPath,
		commandContext: exec.CommandContext,
	}
}

func withDefaultPromptWorkflowDeps(deps promptWorkflowDeps) promptWorkflowDeps {
	if deps.lookPath == nil {
		deps.lookPath = exec.LookPath
	}
	if deps.commandContext == nil {
		deps.commandContext = exec.CommandContext
	}
	return deps
}

func resolvePromptWorkflowContext(cmd *cobra.Command, rawWorkflow string, operatorNotes string, deps promptWorkflowDeps) (promptWorkflowContext, error) {
	deps = withDefaultPromptWorkflowDeps(deps)
	kind, err := parsePromptWorkflowKind(rawWorkflow)
	if err != nil {
		return promptWorkflowContext{}, err
	}
	if err := validatePromptWorkflowFlags(cmd, kind); err != nil {
		return promptWorkflowContext{}, err
	}

	repoRoot, branch, err := resolvePromptWorkflowRepoContext(cmd, deps)
	if err != nil {
		return promptWorkflowContext{}, err
	}

	resolved, warnings, err := workflowtemplates.LoadResolved(resolveConfigDir(), repoRoot)
	if err != nil {
		return promptWorkflowContext{}, fmt.Errorf("loading workflow templates: %w", err)
	}

	template, err := selectPromptWorkflowTemplate(resolved, kind)
	if err != nil {
		return promptWorkflowContext{}, err
	}

	ctx := promptWorkflowContext{
		Kind:          kind,
		RepoRoot:      repoRoot,
		RepoName:      filepath.Base(repoRoot),
		CurrentBranch: branch,
		Template:      template,
		Warnings:      filterTemplateWarnings(warnings, template.Key, template.Filename),
		OperatorNotes: strings.TrimSpace(operatorNotes),
	}

	switch kind {
	case promptWorkflowImplementIssue:
		issueRef, _ := cmd.Flags().GetString("issue")
		issue, err := fetchPromptWorkflowIssue(cmd.Context(), repoRoot, issueRef, cmd, deps)
		if err != nil {
			return promptWorkflowContext{}, err
		}
		ctx.Issue = issue
	case promptWorkflowImplementPR:
		prRef, _ := cmd.Flags().GetString("pr")
		pr, err := fetchPromptWorkflowPR(cmd.Context(), repoRoot, prRef, cmd, deps)
		if err != nil {
			return promptWorkflowContext{}, err
		}
		ctx.PR = pr
	}

	return ctx, nil
}

func parsePromptWorkflowKind(value string) (promptWorkflowKind, error) {
	switch normalizeTemplateSelector(value) {
	case "implement_issue", "issue":
		return promptWorkflowImplementIssue, nil
	case "implement_pr", "fix_pr", "pr":
		return promptWorkflowImplementPR, nil
	default:
		return "", fmt.Errorf("unknown workflow %q; supported: implement_issue, issue, implement_pr, fix_pr, pr", value)
	}
}

func validatePromptWorkflowFlags(cmd *cobra.Command, kind promptWorkflowKind) error {
	issueRef, _ := cmd.Flags().GetString("issue")
	prRef, _ := cmd.Flags().GetString("pr")

	switch kind {
	case promptWorkflowImplementIssue:
		if strings.TrimSpace(issueRef) == "" {
			return fmt.Errorf("workflow %q requires --issue", kind)
		}
		if strings.TrimSpace(prRef) != "" {
			return fmt.Errorf("workflow %q does not accept --pr", kind)
		}
	case promptWorkflowImplementPR:
		if strings.TrimSpace(prRef) == "" {
			return fmt.Errorf("workflow %q requires --pr", kind)
		}
		if strings.TrimSpace(issueRef) != "" {
			return fmt.Errorf("workflow %q does not accept --issue", kind)
		}
	}
	return nil
}

func resolvePromptWorkflowRepoContext(cmd *cobra.Command, deps promptWorkflowDeps) (string, string, error) {
	repoDir, err := resolveRepoDir(cmd)
	if err != nil {
		return "", "", err
	}
	if _, err := deps.lookPath("git"); err != nil {
		return "", "", fmt.Errorf("git binary not found in PATH")
	}
	info, err := os.Stat(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("workflow repository path %q does not exist", repoDir)
		}
		return "", "", fmt.Errorf("stat workflow repository path %q: %w", repoDir, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("workflow repository path %q is not a directory", repoDir)
	}

	repoRoot, err := runPromptWorkflowCommand(cmd.Context(), repoDir, cmd, deps, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("workflow repository %q is not inside a git repository: %w", repoDir, err)
	}

	branch, err := runPromptWorkflowCommand(cmd.Context(), strings.TrimSpace(repoRoot), cmd, deps, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolving current git branch for %q: %w", strings.TrimSpace(repoRoot), err)
	}

	return strings.TrimSpace(repoRoot), strings.TrimSpace(branch), nil
}

func selectPromptWorkflowTemplate(resolved []workflowtemplates.ResolvedTemplate, kind promptWorkflowKind) (workflowtemplates.ResolvedTemplate, error) {
	key := string(kind)
	for _, item := range resolved {
		if item.Key == key {
			return item, nil
		}
	}
	return workflowtemplates.ResolvedTemplate{}, fmt.Errorf("workflow template %q is not available", key)
}

func fetchPromptWorkflowIssue(parent context.Context, repoRoot string, ref string, cmd *cobra.Command, deps promptWorkflowDeps) (*promptWorkflowIssue, error) {
	if _, err := deps.lookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh binary not found in PATH")
	}
	out, err := runPromptWorkflowCommand(parent, repoRoot, cmd, deps, "gh", "issue", "view", "--json", "number,title,body,url", "--", strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("loading issue %q: %w", strings.TrimSpace(ref), err)
	}
	var issue promptWorkflowIssue
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		return nil, fmt.Errorf("decoding issue %q details: %w", strings.TrimSpace(ref), err)
	}
	return &issue, nil
}

func fetchPromptWorkflowPR(parent context.Context, repoRoot string, ref string, cmd *cobra.Command, deps promptWorkflowDeps) (*promptWorkflowPR, error) {
	if _, err := deps.lookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh binary not found in PATH")
	}
	out, err := runPromptWorkflowCommand(parent, repoRoot, cmd, deps, "gh", "pr", "view", "--json", "number,title,body,url,headRefName,baseRefName", "--", strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("loading pull request %q: %w", strings.TrimSpace(ref), err)
	}
	var pr promptWorkflowPR
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return nil, fmt.Errorf("decoding pull request %q details: %w", strings.TrimSpace(ref), err)
	}
	return &pr, nil
}

func promptWorkflowCommandTimeout(cmd *cobra.Command) time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AGENTVAULT_PROMPT_WORKFLOW_TIMEOUT")); raw != "" {
		if timeout, err := time.ParseDuration(raw); err == nil && timeout > 0 {
			if timeout < defaultPromptWorkflowCommandTimeout {
				timeout = defaultPromptWorkflowCommandTimeout
			}
			if timeout > maxPromptWorkflowCommandTimeout {
				timeout = maxPromptWorkflowCommandTimeout
			}
			return timeout
		}
	}

	if cmd != nil {
		if timeout, err := cmd.Flags().GetDuration("timeout"); err == nil && timeout > 0 {
			derived := timeout / 2
			if derived < defaultPromptWorkflowCommandTimeout {
				derived = defaultPromptWorkflowCommandTimeout
			}
			if derived > maxPromptWorkflowCommandTimeout {
				derived = maxPromptWorkflowCommandTimeout
			}
			return derived
		}
	}

	return defaultPromptWorkflowCommandTimeout
}

func runPromptWorkflowCommand(parent context.Context, dir string, cmd *cobra.Command, deps promptWorkflowDeps, name string, args ...string) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	deps = withDefaultPromptWorkflowDeps(deps)
	commandTimeout := promptWorkflowCommandTimeout(cmd)
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()

	command := deps.commandContext(ctx, name, args...)
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		commandText := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s timed out after %s", commandText, commandTimeout)
		}
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.String())
		}
		if errText == "" {
			return "", fmt.Errorf("%s failed: %w", commandText, err)
		}
		return "", fmt.Errorf("%s failed: %v (%s)", commandText, err, errText)
	}
	return stdout.String(), nil
}

func buildPromptWorkflow(ctx promptWorkflowContext) string {
	var b strings.Builder

	fmt.Fprintln(&b, "## Workflow Request")
	fmt.Fprintf(&b, "- Workflow: %s\n", ctx.Kind)
	fmt.Fprintf(&b, "- Repository: %s\n", ctx.RepoName)
	fmt.Fprintf(&b, "- Repository Path: %s\n", ctx.RepoRoot)
	fmt.Fprintf(&b, "- Current Branch: %s\n", ctx.CurrentBranch)
	fmt.Fprintf(&b, "- Template Source: %s\n", ctx.Template.Source)
	fmt.Fprintf(&b, "- Template Version: %s\n", ctx.Template.Version)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Execution Requirements")
	fmt.Fprintln(&b, "- Treat the workflow template below as the canonical checklist for this run.")
	fmt.Fprintln(&b, "- Work inside the repository shown above and keep actions auditable.")
	fmt.Fprintln(&b, "- If required repository or GitHub context is missing, stop and state the blocker explicitly.")
	fmt.Fprintln(&b, "- In your response, use the exact progress checkpoints listed below.")

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Progress Checkpoints")
	fmt.Fprintln(&b, "1. Intake")
	fmt.Fprintln(&b, "2. Context")
	fmt.Fprintln(&b, "3. Implementation")
	fmt.Fprintln(&b, "4. Validation")
	fmt.Fprintln(&b, "5. Delivery")

	switch ctx.Kind {
	case promptWorkflowImplementIssue:
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Issue Context")
		fmt.Fprintf(&b, "- Issue Number: %d\n", ctx.Issue.Number)
		fmt.Fprintf(&b, "- Issue URL: %s\n", ctx.Issue.URL)
		fmt.Fprintf(&b, "- Issue Title: %s\n", ctx.Issue.Title)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "### Issue Description")
		fmt.Fprint(&b, strings.TrimRight(ctx.Issue.Body, "\n"))
		fmt.Fprintln(&b)
	case promptWorkflowImplementPR:
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Pull Request Context")
		fmt.Fprintf(&b, "- PR Number: %d\n", ctx.PR.Number)
		fmt.Fprintf(&b, "- PR URL: %s\n", ctx.PR.URL)
		fmt.Fprintf(&b, "- PR Title: %s\n", ctx.PR.Title)
		fmt.Fprintf(&b, "- PR Branches: %s -> %s\n", ctx.PR.HeadRefName, ctx.PR.BaseRefName)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "### PR Description")
		fmt.Fprint(&b, strings.TrimRight(ctx.PR.Body, "\n"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Review all unresolved PR comments and conversations before changing code.")
	}

	if ctx.OperatorNotes != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Additional Operator Notes")
		fmt.Fprintln(&b, ctx.OperatorNotes)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Canonical Workflow Template")
	fmt.Fprintln(&b, ctx.Template.Content)

	return b.String()
}
