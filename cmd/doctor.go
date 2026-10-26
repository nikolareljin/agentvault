package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check this machine's agent setup",
	Long: `doctor --context estimates what Claude Code loads before the first prompt
in a directory: instruction files per scope, MEMORY.md, and the skill, agent
and command listings of user scope and each enabled plugin. Each row shows
bytes and estimated tokens (bytes / 4). It exits 1 when a file is over its
budget:

  user        ~/.claude/CLAUDE.md, ~/.codex/AGENTS.md    4096 bytes
  directory   CLAUDE.md, CLAUDE.local.md above the dir   3584 bytes
              (a git repository's own files have no budget)
  memory      MEMORY.md                                  120 lines

SessionStart hook output is not measured: measuring it means running the hooks.`,
	RunE: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().Bool("context", false, "estimate what loads before the first prompt, and fail over budget")
	doctorCmd.Flags().String("dir", "", "directory a session starts in (default: current directory)")
	doctorCmd.Flags().Bool("json", false, "output machine-readable JSON")
}

// Budgets, in bytes (lines for memory).
const (
	userInstructionBudget      = 4096
	directoryInstructionBudget = 3584
	memoryLineBudget           = 120
	// Claude Code reads the first 200 lines of MEMORY.md.
	memoryLinesLoaded = 200
)

// contextRow is one source that loads before the first prompt.
type contextRow struct {
	Scope  string `json:"scope"`
	Source string `json:"source"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"tokens"`
	// Lines is set for MEMORY.md, whose budget is in lines.
	Lines int `json:"lines,omitempty"`
	// Budget is in bytes, or in lines for memory; 0 means none.
	Budget     int    `json:"budget,omitempty"`
	BudgetUnit string `json:"budget_unit,omitempty"`
	Over       bool   `json:"over,omitempty"`
	Note       string `json:"note,omitempty"`
}

func runDoctor(cmd *cobra.Command, args []string) error {
	if ok, _ := cmd.Flags().GetBool("context"); !ok {
		return errors.New("nothing to check: use doctor --context")
	}
	// Over budget is a finding, not a usage mistake.
	cmd.SilenceUsage = true
	dir, _ := cmd.Flags().GetString("dir")
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rows := contextRows(home, dir)
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return err
		}
	} else {
		printContextRows(cmd.OutOrStdout(), rows)
	}
	var over []string
	for _, r := range rows {
		if r.Over {
			over = append(over, r.Source)
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("over budget: %s", strings.Join(over, ", "))
	}
	return nil
}

func contextRows(home, dir string) []contextRow {
	var rows []contextRow
	for _, p := range []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md")} {
		if r, ok := fileRow("user", p, userInstructionBudget); ok {
			rows = append(rows, r)
		}
	}
	for _, d := range ancestorsTopDown(dir) {
		budget := directoryInstructionBudget
		scope := "directory"
		if isGitRepo(d) {
			budget, scope = 0, "repository"
		}
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md"} {
			if r, ok := fileRow(scope, filepath.Join(d, name), budget); ok {
				rows = append(rows, r)
			}
		}
	}
	if r, ok := memoryRow(filepath.Join(home, ".claude", "projects", claudeProjectSlug(dir), "memory", "MEMORY.md")); ok {
		rows = append(rows, r)
	}
	rows = append(rows, listingRows("user", filepath.Join(home, ".claude"))...)
	for _, p := range enabledPluginDirs(home) {
		rows = append(rows, listingRows("plugin "+p.id, p.dir)...)
	}
	rows = append(rows, contextRow{Scope: "hooks", Source: "SessionStart hook output", Note: "not measured: it runs the hooks"})
	return rows
}

func estimateTokens(n int) int { return (n + 3) / 4 }

func fileRow(scope, path string, budget int) (contextRow, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return contextRow{}, false
	}
	n := int(info.Size())
	r := contextRow{Scope: scope, Source: path, Bytes: n, Tokens: estimateTokens(n)}
	if budget > 0 {
		r.Budget, r.BudgetUnit, r.Over = budget, "bytes", n > budget
	}
	return r, true
}

func memoryRow(path string) (contextRow, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return contextRow{}, false
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	loaded := lines
	note := ""
	if len(loaded) > memoryLinesLoaded {
		loaded = loaded[:memoryLinesLoaded]
		note = fmt.Sprintf("only the first %d lines load", memoryLinesLoaded)
	}
	n := len(strings.Join(loaded, ""))
	return contextRow{Scope: "memory", Source: path, Bytes: n, Tokens: estimateTokens(n), Lines: len(lines),
		Budget: memoryLineBudget, BudgetUnit: "lines", Over: len(lines) > memoryLineBudget, Note: note}, true
}

// ancestorsTopDown is dir and every directory above it, root first: Claude
// Code reads CLAUDE.md in each.
func ancestorsTopDown(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		out = append(out, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func isGitRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// claudeProjectSlug is the directory name Claude Code keeps a project's
// memory under: the path with every character but letters and digits as "-".
func claudeProjectSlug(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// listingRows sizes the listings Claude Code shows the model for root: each
// skill's, agent's and command's name and description.
func listingRows(scope, root string) []contextRow {
	var rows []contextRow
	kinds := []struct {
		label string
		glob  string
	}{
		{"skills", filepath.Join(root, "skills", "*", "SKILL.md")},
		{"agents", filepath.Join(root, "agents", "*.md")},
		{"commands", filepath.Join(root, "commands", "*.md")},
	}
	for _, k := range kinds {
		files, _ := filepath.Glob(k.glob)
		n, count := 0, 0
		for _, f := range files {
			name, desc := frontmatterNameDescription(f)
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(f), ".md")
				if k.label == "skills" {
					name = filepath.Base(filepath.Dir(f))
				}
			}
			// "- name: description" per entry.
			n += len(name) + len(desc) + 5
			count++
		}
		if count == 0 {
			continue
		}
		rows = append(rows, contextRow{Scope: scope, Source: fmt.Sprintf("%s listing (%d)", k.label, count), Bytes: n, Tokens: estimateTokens(n)})
	}
	return rows
}

func frontmatterNameDescription(path string) (string, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	data = bytes.TrimLeft(data, "\ufeff \t\r\n")
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		var fm struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &fm) == nil {
			return fm.Name, strings.TrimSpace(fm.Description)
		}
		// Not YAML: read "key: value" lines, as the tools do.
		var name, desc string
		for _, l := range lines[1:i] {
			if k, v, ok := strings.Cut(l, ":"); ok {
				switch strings.TrimSpace(k) {
				case "name":
					name = strings.TrimSpace(v)
				case "description":
					desc = strings.TrimSpace(v)
				}
			}
		}
		return name, desc
	}
	return "", ""
}

type pluginDir struct{ id, dir string }

// enabledPluginDirs is each plugin settings.json enables, with its install
// directory from installed_plugins.json.
func enabledPluginDirs(home string) []pluginDir {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if readJSONIfPresent(filepath.Join(home, ".claude", "settings.json"), &settings) != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if readJSONIfPresent(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), &installed) != nil {
		return nil
	}
	var out []pluginDir
	for id, on := range settings.EnabledPlugins {
		entries := installed.Plugins[id]
		if !on || len(entries) == 0 || entries[0].InstallPath == "" {
			continue
		}
		out = append(out, pluginDir{id: id, dir: entries[0].InstallPath})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func printContextRows(out io.Writer, rows []contextRow) {
	total := 0
	fmt.Fprintf(out, "%-10s %8s %8s  %-12s %s\n", "SCOPE", "BYTES", "TOKENS", "BUDGET", "SOURCE")
	for _, r := range rows {
		budget := "-"
		if r.Budget > 0 {
			budget = fmt.Sprintf("%d %s", r.Budget, r.BudgetUnit)
			if r.Over {
				budget = "OVER " + budget
			}
		}
		source := printable(r.Source)
		if r.Lines > 0 {
			source += fmt.Sprintf(" (%d lines)", r.Lines)
		}
		if r.Note != "" {
			source += "  [" + r.Note + "]"
		}
		scope := r.Scope
		if strings.HasPrefix(scope, "plugin ") {
			source = strings.TrimPrefix(scope, "plugin ") + ": " + source
			scope = "plugin"
		}
		fmt.Fprintf(out, "%-10s %8d %8d  %-12s %s\n", scope, r.Bytes, r.Tokens, budget, source)
		total += r.Bytes
	}
	fmt.Fprintf(out, "%-10s %8d %8d\n", "total", total, estimateTokens(total))
}
