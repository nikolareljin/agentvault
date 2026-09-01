package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// goOSArch reports the build platform in the form used by bundle metadata.
func goOSArch() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// runExportWizard asks what to export. Every question has a default that means
// "everything", so pressing Enter through the whole wizard produces a complete,
// encrypted export in the default directory. Questions whose flag was set
// explicitly on the command line are skipped.
func runExportWizard(cmd *cobra.Command, opts *exportOptions, discovered []discoveredProfile) error {
	out := cmd.OutOrStdout()
	reader := bufio.NewReader(os.Stdin)

	fmt.Fprintln(out, "AgentVault export")
	fmt.Fprintln(out, "Press Enter to accept the default shown in brackets.")
	fmt.Fprintln(out)

	fmt.Fprintf(out, "Config profiles found on this machine (%d):\n", len(discovered))
	for _, p := range discovered {
		marker := " "
		if p.IsDefault {
			marker = "*"
		}
		fmt.Fprintf(out, "  %s %-16s %s\n", marker, p.Name, p.ConfigDir)
	}
	fmt.Fprintln(out)

	if !cmd.Flags().Changed("profile") {
		answer, err := askLine(out, reader, "Profiles to export (comma separated, or 'all')", "all")
		if err != nil {
			return err
		}
		opts.Profiles = parseProfileAnswer(answer)
	}

	if strings.TrimSpace(opts.Output) == "" {
		defaultPath := defaultExportPath(nowFunc())
		answer, err := askLine(out, reader, "Output file", defaultPath)
		if err != nil {
			return err
		}
		opts.Output = expandUserPath(answer)
	}

	questions := []struct {
		flag   string
		prompt string
		target *bool
	}{
		{"include-keys", "Include API keys", &opts.IncludeKeys},
		{"include-secrets", "Include secret-bearing provider and asset file content", &opts.IncludeSecrets},
		{"include-sessions", "Include multi-agent session definitions", &opts.IncludeSessions},
		{"include-templates", "Include workflow templates", &opts.IncludeTemplates},
		{"include-provider-files", "Include provider home files (~/.claude, ~/.codex, ~/.copilot)", &opts.IncludeProviderFile},
		{"include-skills", "Include skill assets", &opts.IncludeSkills},
		{"detect", "Include detected agent information", &opts.IncludeDetected},
		{"include-status", "Include a provider token/quota status snapshot", &opts.IncludeStatus},
	}
	for _, q := range questions {
		if cmd.Flags().Changed(q.flag) {
			continue
		}
		value, err := askBool(out, reader, q.prompt, *q.target)
		if err != nil {
			return err
		}
		*q.target = value
	}

	if !cmd.Flags().Changed("project") {
		answer, err := askLine(out, reader, "Also capture project-local assets from directory (blank for none)", "")
		if err != nil {
			return err
		}
		opts.ProjectDir = expandUserPath(answer)
	}

	if !cmd.Flags().Changed("encrypt") && !cmd.Flags().Changed("plain") {
		value, err := askBool(out, reader, "Encrypt the bundle with a password", opts.Encrypt)
		if err != nil {
			return err
		}
		opts.Encrypt = value
	}

	fmt.Fprintln(out)
	return nil
}

// parseProfileAnswer turns a comma separated wizard answer into profile names.
// "all" and an empty answer both mean every discovered profile.
func parseProfileAnswer(answer string) []string {
	answer = strings.TrimSpace(answer)
	if answer == "" || strings.EqualFold(answer, "all") {
		return nil
	}
	var names []string
	for _, part := range strings.Split(answer, ",") {
		if part = strings.TrimSpace(part); part != "" {
			names = append(names, part)
		}
	}
	return names
}

// askLine prompts for free text and returns def when the answer is blank.
func askLine(out io.Writer, reader *bufio.Reader, prompt string, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(out, "%s: ", prompt)
	}
	line, err := reader.ReadString('\n')
	if err != nil && (line == "" || err != io.EOF) {
		if err == io.EOF {
			fmt.Fprintln(out)
			return def, nil
		}
		return "", fmt.Errorf("reading answer: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// askBool prompts a yes/no question, returning def when the answer is blank.
func askBool(out io.Writer, reader *bufio.Reader, prompt string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(out, "%s? [%s]: ", prompt, hint)
		line, err := reader.ReadString('\n')
		if err != nil && (line == "" || err != io.EOF) {
			if err == io.EOF {
				fmt.Fprintln(out)
				return def, nil
			}
			return false, fmt.Errorf("reading answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(out, "  Please answer y or n.")
		}
	}
}

// expandUserPath resolves a leading ~ so wizard answers can use home shorthand.
func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	return path
}
