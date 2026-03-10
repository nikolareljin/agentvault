package cmd

import (
	"fmt"
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
  2) AgentVault config storage (default: ~/.config/agentvault/templates; honors XDG_CONFIG_HOME/--config)
  3) built-in safe defaults

Use these commands to inspect effective content and refresh config templates.`,
}

var templatesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflow templates and effective source",
	RunE:  runTemplatesList,
}

var templatesShowCmd = &cobra.Command{
	Use:   "show <name>",
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
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	fmt.Fprintln(out, "Workflow templates")
	for _, t := range resolved {
		fmt.Fprintf(out, "  - %s (%s, version=%s)\n", t.Filename, t.Source, t.Version)
	}
	for _, warn := range warnings {
		fmt.Fprintf(errOut, "warning: %s\n", warn)
	}
	return nil
}

func runTemplatesShow(cmd *cobra.Command, args []string) error {
	repoDir, err := resolveRepoDir(cmd)
	if err != nil {
		return err
	}
	resolved, warnings, err := workflowtemplates.LoadResolved(resolveConfigDir(), repoDir)
	if err != nil {
		return err
	}
	requested := normalizeTemplateSelector(args[0])
	includeMeta, _ := cmd.Flags().GetBool("metadata")
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	for _, t := range resolved {
		if normalizeTemplateSelector(t.Key) != requested && normalizeTemplateSelector(t.Filename) != requested {
			continue
		}
		if includeMeta {
			fmt.Fprintf(out, "# Source: %s\n# File: %s\n# Version: %s\n\n", t.Source, t.Filename, t.Version)
		}
		fmt.Fprint(out, t.Content)
		if !strings.HasSuffix(t.Content, "\n") {
			fmt.Fprintln(out)
		}
		for _, warn := range filterTemplateWarnings(warnings, t.Key, t.Filename) {
			fmt.Fprintf(errOut, "warning: %s\n", warn)
		}
		return nil
	}
	return fmt.Errorf("unknown template %q; supported: %s", args[0], strings.Join(workflowtemplates.SupportedKeys(), ", "))
}

func runTemplatesRefresh(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	written, err := workflowtemplates.RefreshConfigTemplates(resolveConfigDir(), force)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(written) == 0 {
		fmt.Fprintln(out, "No template bodies were rewritten. Config storage metadata is up to date.")
		return nil
	}
	fmt.Fprintf(out, "Initialized %d workflow template(s) in config storage:\n", len(written))
	for _, t := range written {
		fmt.Fprintf(out, "  - %s (%s)\n", t.Filename, t.Version)
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

func filterTemplateWarnings(warnings []string, key string, filename string) []string {
	if len(warnings) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(warnings))
	quotedFilename := fmt.Sprintf("%q", filename)
	for _, warningText := range warnings {
		// Keep template-specific warnings for the selected template.
		if strings.Contains(warningText, key) || strings.Contains(warningText, quotedFilename) || strings.Contains(warningText, filename) {
			filtered = append(filtered, warningText)
			continue
		}
		// Keep global metadata-level warnings since they affect resolution semantics broadly.
		if strings.Contains(strings.ToLower(warningText), "metadata") {
			filtered = append(filtered, warningText)
		}
	}
	return filtered
}

func normalizeTemplateSelector(value string) string {
	v := strings.TrimSpace(strings.ToLower(value))
	v = strings.TrimSuffix(v, ".txt")
	v = strings.ReplaceAll(v, "-", "_")
	return v
}
