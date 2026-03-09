package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
	"github.com/spf13/cobra"
)

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "Manage workflow templates used by issue/PR automation workflows",
	Long: `Workflow templates are resolved with explicit precedence:
  1) repository-local files (./implement_issue.txt, etc.)
  2) AgentVault config storage (~/.config/agentvault/templates)
  3) built-in safe defaults

Use these commands to inspect effective content and refresh config templates.`,
}

var templatesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflow templates and effective source",
	RunE:  runTemplatesList,
}

var templatesShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show effective workflow template content",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplatesShow,
}

var templatesRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Write built-in templates into config storage",
	RunE:  runTemplatesRefresh,
}

func init() {
	rootCmd.AddCommand(templatesCmd)
	templatesCmd.AddCommand(templatesListCmd)
	templatesCmd.AddCommand(templatesShowCmd)
	templatesCmd.AddCommand(templatesRefreshCmd)

	templatesListCmd.Flags().String("repo", "", "repository path for repo-local overrides (default: current directory)")
	templatesShowCmd.Flags().String("repo", "", "repository path for repo-local overrides (default: current directory)")
	templatesShowCmd.Flags().Bool("metadata", false, "include metadata header before template content")
	templatesRefreshCmd.Flags().Bool("force", false, "overwrite existing config templates with built-in defaults")
}

func runTemplatesList(cmd *cobra.Command, args []string) error {
	repoDir, err := resolveRepoDir(cmd)
	if err != nil {
		return err
	}
	resolved, warnings, err := workflowtemplates.LoadResolved(resolveConfigDir(), repoDir)
	if err != nil {
		return err
	}
	fmt.Println("Workflow templates")
	for _, t := range resolved {
		fmt.Printf("  - %s (%s, version=%s)\n", t.Filename, t.Source, t.Version)
	}
	for _, warn := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warn)
	}
	return nil
}

func runTemplatesShow(cmd *cobra.Command, args []string) error {
	repoDir, err := resolveRepoDir(cmd)
	if err != nil {
		return err
	}
	wantKey, ok := workflowtemplates.FindTemplateKey(args[0])
	if !ok {
		return fmt.Errorf("unknown template %q; supported: %s", args[0], strings.Join(workflowtemplates.SupportedKeys(), ", "))
	}
	resolved, warnings, err := workflowtemplates.LoadResolved(resolveConfigDir(), repoDir)
	if err != nil {
		return err
	}
	includeMeta, _ := cmd.Flags().GetBool("metadata")
	for _, t := range resolved {
		if t.Key != wantKey {
			continue
		}
		if includeMeta {
			fmt.Printf("# Source: %s\n# File: %s\n# Version: %s\n\n", t.Source, t.Filename, t.Version)
		}
		fmt.Print(t.Content)
		if !strings.HasSuffix(t.Content, "\n") {
			fmt.Println()
		}
		for _, warn := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warn)
		}
		return nil
	}
	return fmt.Errorf("template %q was not resolved", wantKey)
}

func runTemplatesRefresh(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	written, err := workflowtemplates.RefreshConfigTemplates(resolveConfigDir(), force)
	if err != nil {
		return err
	}
	if len(written) == 0 {
		fmt.Println("No templates written. Config storage is already initialized.")
		return nil
	}
	fmt.Printf("Initialized %d workflow template(s) in config storage:\n", len(written))
	for _, t := range written {
		fmt.Printf("  - %s (%s)\n", t.Filename, t.Version)
	}
	return nil
}

func resolveRepoDir(cmd *cobra.Command) (string, error) {
	repoDir, _ := cmd.Flags().GetString("repo")
	if strings.TrimSpace(repoDir) == "" {
		repoDir = "."
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("resolving repo path: %w", err)
	}
	return abs, nil
}
