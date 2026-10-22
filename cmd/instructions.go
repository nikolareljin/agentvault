package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/fsname"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

// getInstructionForCmd fetches an instruction by name, optionally targeting a
// specific scoped variant when scope is provided.
func getInstructionForCmd(v *vault.Vault, name, scope, pattern string) (agent.InstructionFile, bool) {
	if scope != "" {
		key := agent.InstructionKey(agent.InstructionFile{
			Name:             name,
			Scope:            scope,
			DirectoryPattern: pattern,
		})
		return v.GetInstructionByKey(key)
	}
	return v.GetInstruction(name)
}

// validateScopeFlags returns an error if the scope/pattern combination is invalid.
func validateScopeFlags(scope, pattern string) error {
	err := agent.ValidateScopePattern(scope, pattern)
	if err == nil {
		return nil
	}
	var scopeErr *agent.ScopePatternError
	if !errors.As(err, &scopeErr) {
		return err
	}
	switch scopeErr.Kind {
	case agent.ScopePatternErrorNullByte:
		return errors.New("--scope and --directory-pattern must not contain null bytes")
	case agent.ScopePatternErrorDirectoryOnly:
		return errors.New("--directory-pattern is only valid for directory scope")
	case agent.ScopePatternErrorDirectoryRequired:
		return errors.New("--directory-pattern is required for directory scope")
	case agent.ScopePatternErrorParentTraversal:
		return errors.New("--directory-pattern must not begin with \"..\"")
	case agent.ScopePatternErrorInvalidGlob:
		return fmt.Errorf("--directory-pattern has invalid glob syntax: %w", scopeErr.Err)
	case agent.ScopePatternErrorInvalidScope:
		return fmt.Errorf("invalid --scope %q; valid: global, directory, local", scopeErr.Scope)
	default:
		return err
	}
}

// findEditorCommand returns the editor command and args.
// It supports $EDITOR values with flags (e.g. "code --wait").
func findEditorCommand() []string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		parts := splitCommandLine(editor)
		if len(parts) > 0 {
			if path, err := exec.LookPath(parts[0]); err == nil {
				return append([]string{path}, parts[1:]...)
			}
		}
	}
	for _, name := range []string{"nano", "vi", "vim"} {
		if path, err := exec.LookPath(name); err == nil {
			return []string{path}
		}
	}
	return nil
}

// splitCommandLine parses a command string with simple shell-like quoting.
// It intentionally does not treat backslashes as escapes so Windows paths are preserved.
func splitCommandLine(s string) []string {
	var out []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}

	for _, r := range s {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

var instructionsCmd = &cobra.Command{
	Use:     "instructions",
	Aliases: []string{"inst"},
	Short:   "Manage agent instruction files (AGENTS.md, CLAUDE.md, etc.)",
	Long: `Store, distribute, and synchronize instruction files across projects.

Instruction files like AGENTS.md and CLAUDE.md define how AI agents behave
in a project. This command lets you maintain a single canonical set of
instructions in the vault and push them to any project directory, ensuring
all agents behave consistently.

Well-known instruction files:
  agents  -> AGENTS.md
  claude  -> CLAUDE.md
  codex   -> codex.md
  copilot -> .github/copilot-instructions.md`,
}

var instListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored instruction files",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		instructions := v.ListInstructions()
		if len(instructions) == 0 {
			fmt.Println("No instruction files stored. Use 'agentvault instructions pull' or 'set' to add some.")
			return nil
		}
		// Determine if any instruction has scope metadata to show.
		hasScope := false
		for _, inst := range instructions {
			if inst.Scope != "" {
				hasScope = true
				break
			}
		}
		for _, inst := range instructions {
			age := ""
			if !inst.UpdatedAt.IsZero() {
				age = inst.UpdatedAt.Format("2006-01-02 15:04")
			}
			if hasScope {
				scope := inst.Scope
				if scope == "" {
					scope = agent.InstructionScopeGlobal
				}
				pattern := inst.DirectoryPattern
				if pattern == "" {
					pattern = "-"
				}
				fmt.Printf("  %-12s -> %-35s  %-10s  %-30s  (%d bytes, %s)\n",
					printable(inst.Name), printable(inst.Filename), printable(scope), printable(pattern), len(inst.Content), age)
			} else {
				fmt.Printf("  %-12s -> %-40s  (%d bytes, %s)\n",
					printable(inst.Name), printable(inst.Filename), len(inst.Content), age)
			}
		}
		return nil
	},
}

var instShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Print the content of a stored instruction file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		scope, _ := cmd.Flags().GetString("scope")
		pattern, _ := cmd.Flags().GetString("directory-pattern")
		if err := validateScopeFlags(scope, pattern); err != nil {
			return err
		}
		inst, ok := getInstructionForCmd(v, args[0], scope, pattern)
		if !ok {
			return fmt.Errorf("instruction %q not found", args[0])
		}
		fmt.Print(contentText(cmd, inst.Content))
		if inst.Content != "" && !strings.HasSuffix(inst.Content, "\n") {
			fmt.Println()
		}
		return nil
	},
}

var instSetCmd = &cobra.Command{
	Use:   "set [name]",
	Short: "Store an instruction file from disk or inline content",
	Long: `Store an instruction file in the vault. Provide content via --file or --content.

Examples:
  agentvault instructions set agents --file ./AGENTS.md
  agentvault instructions set claude --content "Be thorough and consistent."
  agentvault instructions set custom --file ./my-rules.md --filename CUSTOM.md`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		name := args[0]
		filePath, _ := cmd.Flags().GetString("file")
		content, _ := cmd.Flags().GetString("content")
		filename, _ := cmd.Flags().GetString("filename")

		scope, _ := cmd.Flags().GetString("scope")
		dirPattern, _ := cmd.Flags().GetString("directory-pattern")
		if filePath == "" && content == "" {
			// --filename alone moves a stored instruction, such as one an
			// older version stored under a name push now refuses.
			existing, ok := findInstruction(v, name, scope, dirPattern)
			if filename == "" || !ok {
				return fmt.Errorf("provide --file or --content (or --filename alone to move a stored instruction)")
			}
			content = existing.Content
		}
		if filePath != "" && content != "" {
			return fmt.Errorf("use either --file or --content, not both")
		}

		if filePath != "" {
			data, err := readImportFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			content = string(data)
		}

		if filename == "" {
			filename = agent.FilenameForInstruction(name)
		}

		if err := validateScopeFlags(scope, dirPattern); err != nil {
			return err
		}

		// Scan for prompt hijacking
		if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
			fmt.Fprint(os.Stderr, printableText(agent.FormatWarnings(warnings)))
		}

		inst := agent.InstructionFile{
			Name:             name,
			Filename:         filename,
			Content:          content,
			UpdatedAt:        time.Now(),
			Scope:            scope,
			DirectoryPattern: dirPattern,
		}
		if err := v.SetInstruction(inst); err != nil {
			return err
		}
		fmt.Printf("Instruction %q stored (%d bytes, target: %s).\n", printable(name), len(content), printable(filename))
		return nil
	},
}

var instEditCmd = &cobra.Command{
	Use:   "edit [name]",
	Short: "Edit an instruction file in an external editor",
	Long: `Open a stored instruction file in your preferred editor for editing.
The editor is chosen in order: $EDITOR, nano, vi, vim.

After saving and closing the editor, the updated content is stored in the vault.

Examples:
  agentvault instructions edit agents
  agentvault instructions edit claude
  EDITOR=vim agentvault instructions edit codex`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		pattern, _ := cmd.Flags().GetString("directory-pattern")
		if err := validateScopeFlags(scope, pattern); err != nil {
			return err
		}
		inst, ok := getInstructionForCmd(v, name, scope, pattern)
		if !ok {
			return fmt.Errorf("instruction %q not found", name)
		}

		// Write content to a temp file
		tmpFile, err := os.CreateTemp("", "agentvault-*.md")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)

		if _, err := tmpFile.WriteString(inst.Content); err != nil {
			if closeErr := tmpFile.Close(); closeErr != nil {
				return fmt.Errorf("closing temp file after write failure: %w", closeErr)
			}
			return fmt.Errorf("writing temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return fmt.Errorf("closing temp file: %w", err)
		}

		// Find an editor
		editor := findEditorCommand()
		if len(editor) == 0 {
			return fmt.Errorf("no editor found (set $EDITOR, or install nano or vi)")
		}

		// Open editor
		editorArgs := append(append([]string{}, editor[1:]...), tmpPath)
		editorCmd := exec.Command(editor[0], editorArgs...)
		editorCmd.Stdin = os.Stdin
		editorCmd.Stdout = os.Stdout
		editorCmd.Stderr = os.Stderr

		if err := editorCmd.Run(); err != nil {
			return fmt.Errorf("editor exited with error: %w", err)
		}

		// Read back the edited content
		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return fmt.Errorf("reading edited file: %w", err)
		}

		newContent := string(edited)
		if newContent == inst.Content {
			fmt.Println("No changes made.")
			return nil
		}

		// Scan for prompt hijacking
		if warnings := agent.CheckHijacking(newContent); len(warnings) > 0 {
			fmt.Fprint(os.Stderr, printableText(agent.FormatWarnings(warnings)))
		}

		inst.Content = newContent
		inst.UpdatedAt = time.Now()
		if err := v.SetInstruction(inst); err != nil {
			return err
		}
		fmt.Printf("Instruction %q updated (%d bytes).\n", printable(name), len(newContent))
		return nil
	},
}

var instRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a stored instruction file",
	Long: `Remove a stored instruction file by name. When multiple scoped variants
exist, use --scope (and --directory-pattern for directory scope) to target the
exact variant; omitting --scope prefers the global-scope variant when one exists,
otherwise removes the first match by name.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		scope, _ := cmd.Flags().GetString("scope")
		pattern, _ := cmd.Flags().GetString("directory-pattern")
		if err := validateScopeFlags(scope, pattern); err != nil {
			return err
		}
		name := args[0]
		if scope != "" {
			key := agent.InstructionKey(agent.InstructionFile{
				Name:             name,
				Scope:            scope,
				DirectoryPattern: pattern,
			})
			if err := v.RemoveInstructionByKey(key); err != nil {
				return fmt.Errorf("removing instruction %q (scope %s): %w", name, scope, err)
			}
		} else {
			if err := v.RemoveInstruction(name); err != nil {
				return err
			}
		}
		fmt.Printf("Instruction %q removed.\n", printable(name))
		return nil
	},
}

// knownFilenameToName maps auto-discovered top-level filenames to instruction names for pull.
// It is intentionally partial: entries handled via dedicated path logic (for example
// .github/copilot-instructions.md) are not listed here.
var knownFilenameToName = map[string]string{
	"AGENTS.md":      "agents",
	"CLAUDE.md":      "claude",
	"GEMINI.md":      "gemini",
	"codex.md":       "codex",
	"MELDBOT.md":     "meldbot",
	"OPENCLAW.md":    "openclaw",
	"NANOCLAW.md":    "nanoclaw",
	".cursorrules":   "cursor",
	".windsurfrules": "windsurf",
}

var instPullCmd = &cobra.Command{
	Use:   "pull [directory]",
	Short: "Import instruction files from a project directory into the vault",
	Long: `Read AGENTS.md, CLAUDE.md, and other known instruction files from a
directory and store them in the vault. Existing instructions with the same
name are updated. Use --name to pull a specific file only.

Examples:
  agentvault instructions pull .
  agentvault instructions pull /path/to/project
  agentvault instructions pull . --name agents
  agentvault instructions pull . --file custom.md --name custom`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		dir := args[0]
		specificName, _ := cmd.Flags().GetString("name")
		specificFile, _ := cmd.Flags().GetString("file")

		if specificName != "" && specificFile != "" {
			// pull a specific file with a given name
			return pullSingleFile(v, dir, specificFile, specificName)
		}

		if specificName != "" {
			// pull a specific well-known name
			filename := agent.FilenameForInstruction(specificName)
			return pullSingleFile(v, dir, filename, specificName)
		}

		// auto-discover known files
		pulled, refusedHere := 0, 0
		// A file that is not UTF-8 text is reported and skipped: one UTF-16
		// AGENTS.md must not stop the other files from being pulled.
		store := func(name, filename string, data []byte) error {
			if problem := agent.TextProblem(data); problem != "" {
				fmt.Fprintf(os.Stderr, "  refused %s: %s\n", printable(filename), problem)
				refusedHere++
				return nil
			}
			content := string(data)
			if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
				fmt.Fprintf(os.Stderr, "  [%s] %s\n", printable(name), printable(filename))
				fmt.Fprint(os.Stderr, printableText(agent.FormatWarnings(warnings)))
			}
			inst := agent.InstructionFile{
				Name:      name,
				Filename:  filename,
				Content:   content,
				UpdatedAt: time.Now(),
			}
			if err := v.SetInstruction(inst); err != nil {
				return err
			}
			fmt.Printf("  Pulled %s -> %q (%d bytes)\n", printable(filename), printable(name), len(data))
			pulled++
			return nil
		}
		// Sorted, so a failure leaves the same files stored on every run.
		known := make([]string, 0, len(knownFilenameToName))
		for filename := range knownFilenameToName {
			known = append(known, filename)
		}
		sort.Strings(known)
		for _, filename := range known {
			data := readLocalInstruction(filepath.Join(dir, filename), &refusedHere)
			if data == nil {
				continue
			}
			if err := store(knownFilenameToName[filename], filename, data); err != nil {
				return err
			}
		}
		// also check files in subdirectories, and .aider.conf.yml
		for _, extra := range [][2]string{
			{filepath.Join(".github", "copilot-instructions.md"), "copilot"},
			{".aider.conf.yml", "aider"},
		} {
			data := readLocalInstruction(filepath.Join(dir, extra[0]), &refusedHere)
			if data == nil {
				continue
			}
			if err := store(extra[1], extra[0], data); err != nil {
				return err
			}
		}

		// An instruction file is not self-contained: it can name other files as
		// required reading, and storing it without them exports a rule
		// pointing at nothing on the next machine.
		follow, _ := cmd.Flags().GetBool("follow-references")
		strict, _ := cmd.Flags().GetBool("strict")
		if follow && pulled > 0 {
			res, err := v.PullReferenceClosure(dir)
			if err != nil {
				return err
			}
			for _, inst := range res.Stored {
				if warnings := agent.CheckHijacking(inst.Content); len(warnings) > 0 {
					fmt.Fprintf(os.Stderr, "  [%s] %s\n", printable(inst.Name), printable(inst.Filename))
					fmt.Fprint(os.Stderr, printableText(agent.FormatWarnings(warnings)))
				}
				fmt.Printf("  Pulled %s -> %q (%d bytes, named by an instruction file)\n",
					printable(inst.Filename), printable(inst.Name), len(inst.Content))
			}
			pulled += len(res.Stored)

			// Refusals and missing files are printed, never swallowed. A rule
			// naming a file that cannot travel is the defect this exists to
			// end, and it is invisible if the export is quiet about it.
			incomplete := map[string]bool{}
			for _, r := range res.Refused {
				shown := r.Raw
				if r.Rel != "" {
					shown = r.Rel
				}
				fmt.Fprintf(os.Stderr, "  refused %s: %s\n", printable(shown), printable(r.Reason))
				if skill := agent.SkillDirOf(r.Rel); skill != "" && !incomplete[skill] {
					incomplete[skill] = true
					fmt.Fprintf(os.Stderr, "  skill %s is incomplete in the vault; "+
						"'agentvault export --project %s' carries every file\n", printable(skill), printable(dir))
				}
			}
			for _, r := range res.Missing {
				fmt.Fprintf(os.Stderr, "  named but not present: %s\n", printable(r.Raw))
			}
			if strict && (len(res.Refused) > 0 || len(res.Missing) > 0) {
				return fmt.Errorf("--strict: %d file(s) refused, %d named but not present",
					len(res.Refused)+refusedHere, len(res.Missing))
			}
		}
		if strict && refusedHere > 0 {
			return fmt.Errorf("--strict: %d file(s) refused", refusedHere)
		}

		if pulled > 0 {
			if err := recordProjectLocation(v, dir); err != nil {
				return fmt.Errorf("recording project location: %w", err)
			}
		}
		if pulled == 0 {
			fmt.Println("No instruction files found in", dir)
		} else {
			fmt.Printf("Pulled %d instruction file(s) from %s.\n", pulled, dir)
		}
		return nil
	},
}

func pullSingleFile(v *vault.Vault, dir, filename, name string) error {
	p := filepath.Join(dir, filename)
	data, err := readImportFile(p)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p, err)
	}
	if problem := agent.TextProblem(data); problem != "" {
		return fmt.Errorf("%s: %s", printable(filename), problem)
	}
	content := string(data)

	// Scan for prompt hijacking
	if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
		fmt.Fprintf(os.Stderr, "  [%s] %s\n", printable(name), printable(filename))
		fmt.Fprint(os.Stderr, printableText(agent.FormatWarnings(warnings)))
	}

	inst := agent.InstructionFile{
		Name:      name,
		Filename:  filename,
		Content:   content,
		UpdatedAt: time.Now(),
	}
	if err := v.SetInstruction(inst); err != nil {
		return err
	}
	fmt.Printf("Pulled %s -> %q (%d bytes)\n", printable(filename), printable(name), len(data))
	return nil
}

var instPushCmd = &cobra.Command{
	Use:   "push [directory]",
	Short: "Write stored instruction files to a project directory",
	Long: `Write all stored instruction files (or a specific one) to a target
directory. Existing files are overwritten. Use --name to push a specific
instruction only.

This is the primary way to make agents behave consistently across projects:
store canonical instructions in the vault, then push them wherever needed.

Examples:
  agentvault instructions push .
  agentvault instructions push /path/to/project
  agentvault instructions push . --name agents`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		dir := args[0]
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		specificName, _ := cmd.Flags().GetString("name")

		// A pattern written for this repository's checkout on another
		// machine applies here too: the vault knows both paths.
		instructions := agent.ResolveEffectiveInstructions(aliasDirectoryPatterns(v.ListInstructions(), v.Locations(), dir, nil), dir)
		if len(instructions) == 0 {
			fmt.Println("No instruction files stored. Use 'pull' or 'set' first.")
			return nil
		}

		if err := checkInstructionFilenames(instructions, func(i agent.InstructionFile) bool {
			return specificName == "" || i.Name == specificName
		}); err != nil {
			return err
		}
		pushed := 0
		for _, inst := range instructions {
			if specificName != "" && inst.Name != specificName {
				continue
			}
			if err := writeInstructionFile(dir, inst); err != nil {
				return err
			}
			fmt.Printf("  Pushed %q -> %s (%d bytes)\n", printable(inst.Name), printable(inst.Filename), len(inst.Content))
			pushed++
		}

		if specificName != "" && pushed == 0 {
			return fmt.Errorf("instruction %q not found", specificName)
		}
		fmt.Printf("Pushed %d instruction file(s) to %s.\n", pushed, dir)
		return nil
	},
}

var instDiffCmd = &cobra.Command{
	Use:   "diff [directory]",
	Short: "Compare stored instructions with files in a directory",
	Long: `Show which instruction files differ between the vault and a project
directory. Useful before pushing to see what would change.

Examples:
  agentvault instructions diff .
  agentvault instructions diff /path/to/project`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		dir := args[0]
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		// A pattern written for this repository's checkout on another
		// machine applies here too: the vault knows both paths.
		instructions := agent.ResolveEffectiveInstructions(aliasDirectoryPatterns(v.ListInstructions(), v.Locations(), dir, nil), dir)
		if len(instructions) == 0 {
			fmt.Println("No instruction files stored.")
			return nil
		}

		diffs := 0
		for _, inst := range instructions {
			diskData, err := readInstructionOnDisk(dir, inst)
			if errors.Is(err, errUnsafeInstructionFilename) {
				fmt.Printf("  %-12s  %s  (refused: %v)\n", printable(inst.Name), printable(inst.Filename), err)
				diffs++
				continue
			}
			if err != nil {
				fmt.Printf("  %-12s  %s  (not on disk)\n", printable(inst.Name), printable(inst.Filename))
				diffs++
				continue
			}
			diskContent := string(diskData)
			if diskContent == inst.Content {
				fmt.Printf("  %-12s  %s  (identical)\n", printable(inst.Name), printable(inst.Filename))
			} else {
				vaultLines := strings.Count(inst.Content, "\n")
				diskLines := strings.Count(diskContent, "\n")
				fmt.Printf("  %-12s  %s  (DIFFERS: vault %d lines, disk %d lines)\n",
					printable(inst.Name), printable(inst.Filename), vaultLines, diskLines)
				diffs++
			}
		}

		// also check for known files on disk not in vault
		for filename, name := range knownFilenameToName {
			found := false
			for _, inst := range instructions {
				if inst.Name == name {
					found = true
					break
				}
			}
			if !found {
				p := filepath.Join(dir, filename)
				if _, err := os.Stat(p); err == nil {
					fmt.Printf("  %-12s  %s  (on disk, not in vault)\n", printable(name), printable(filename))
					diffs++
				}
			}
		}

		if diffs == 0 {
			fmt.Println("All instruction files are in sync.")
		} else {
			fmt.Printf("%d difference(s) found.\n", diffs)
		}
		return nil
	},
}

var instScanCmd = &cobra.Command{
	Use:   "scan [name]",
	Short: "Scan instruction files for prompt hijacking patterns",
	Long: `Scan stored instruction files for patterns that could be used to
override or subvert agent behavior (prompt injection/hijacking).

Without arguments, scans all stored instructions. Provide a name to scan
a specific instruction.

Examples:
  agentvault instructions scan
  agentvault instructions scan agents`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		scope, _ := cmd.Flags().GetString("scope")
		pattern, _ := cmd.Flags().GetString("directory-pattern")
		if err := validateScopeFlags(scope, pattern); err != nil {
			return err
		}
		if len(args) == 0 && (scope != "" || pattern != "") {
			return fmt.Errorf("--scope and --directory-pattern require an instruction name")
		}
		var toScan []agent.InstructionFile
		if len(args) > 0 {
			inst, ok := getInstructionForCmd(v, args[0], scope, pattern)
			if !ok {
				return fmt.Errorf("instruction %q not found", args[0])
			}
			toScan = append(toScan, inst)
		} else {
			toScan = v.ListInstructions()
		}

		if len(toScan) == 0 {
			fmt.Println("No instruction files stored.")
			return nil
		}

		totalWarnings := 0
		for _, inst := range toScan {
			warnings := agent.CheckHijacking(inst.Content)
			if len(warnings) > 0 {
				fmt.Printf("--- %s (%s) ---\n", printable(inst.Name), printable(inst.Filename))
				fmt.Print(printableText(agent.FormatWarnings(warnings)))
				fmt.Println()
				totalWarnings += len(warnings)
			} else {
				fmt.Printf("  %-12s  %s  (clean)\n", printable(inst.Name), printable(inst.Filename))
			}
		}

		if totalWarnings == 0 {
			fmt.Println("\nAll instruction files passed hijacking scan.")
		} else {
			fmt.Printf("\n%d total warning(s) across %d file(s).\n", totalWarnings, len(toScan))
		}
		return nil
	},
}

var instPreviewCmd = &cobra.Command{
	Use:   "preview [name]",
	Short: "Show the effective instruction content after scope resolution",
	Long: `Show which instruction wins for a given working directory after applying
scope precedence (local > directory > global). Omit [name] or use --all to list
all resolved instructions instead of a single named one.

Examples:
  agentvault instructions preview agents
  agentvault instructions preview agents --dir /home/user/my-projects-location/example-repo
  agentvault instructions preview --dir /home/user/my-projects-location/example-repo
  agentvault instructions preview --all --dir /home/user/my-projects-location/example-repo`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		dir, _ := cmd.Flags().GetString("dir")
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			dir = cwd
		} else if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		showAll, _ := cmd.Flags().GetBool("all")

		resolved := agent.ResolveEffectiveInstructions(v.ListInstructions(), dir)
		if len(resolved) == 0 {
			fmt.Println("No instruction files stored.")
			return nil
		}

		if showAll || len(args) == 0 {
			fmt.Printf("Effective instructions for directory: %s\n\n", dir)
			for _, inst := range resolved {
				scope := inst.Scope
				if scope == "" {
					scope = agent.InstructionScopeGlobal
				}
				fmt.Printf("=== %s (%s, scope: %s) ===\n", printable(inst.Name), printable(inst.Filename), printable(scope))
				fmt.Println(contentText(cmd, inst.Content))
			}
			return nil
		}

		name := args[0]
		for _, inst := range resolved {
			if inst.Name == name {
				scope := inst.Scope
				if scope == "" {
					scope = agent.InstructionScopeGlobal
				}
				fmt.Printf("=== %s (%s, scope: %s) ===\n", printable(inst.Name), printable(inst.Filename), printable(scope))
				fmt.Print(contentText(cmd, inst.Content))
				if inst.Content != "" && !strings.HasSuffix(inst.Content, "\n") {
					fmt.Println()
				}
				return nil
			}
		}
		return fmt.Errorf("instruction %q not found (or no applicable scope for this directory)", name)
	},
}

var instExportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export stored instructions to a JSON or YAML file",
	Long: `Export all stored instruction files (with scope metadata) to a portable
file. Use --scope to filter by scope level.

Examples:
  agentvault instructions export instructions.json
  agentvault instructions export instructions.yaml --format yaml
  agentvault instructions export global.json --scope global`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		format, _ := cmd.Flags().GetString("format")
		scopeFilter, _ := cmd.Flags().GetString("scope")

		if scopeFilter != "" {
			switch scopeFilter {
			case agent.InstructionScopeGlobal, agent.InstructionScopeDirectory, agent.InstructionScopeLocal:
			default:
				return fmt.Errorf("invalid --scope %q; valid: global, directory, local", scopeFilter)
			}
		}

		instructions := v.ListInstructions()
		if scopeFilter != "" {
			var filtered []agent.InstructionFile
			for _, inst := range instructions {
				s := inst.Scope
				if s == "" {
					s = agent.InstructionScopeGlobal
				}
				if s == scopeFilter {
					filtered = append(filtered, inst)
				}
			}
			instructions = filtered
		}

		data, err := marshalInstructions(instructions, format, args)
		if err != nil {
			return err
		}

		if len(args) == 0 {
			fmt.Println(string(escapeC1JSON(data)))
			return nil
		}
		if err := os.WriteFile(args[0], data, 0600); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}
		fmt.Printf("Exported %d instruction(s) to %s.\n", len(instructions), args[0])
		return nil
	},
}

var instImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import instructions from a JSON or YAML file",
	Long: `Import instruction files from a portable export file into the vault.
Existing instructions with the same name, scope, and directory pattern are
skipped unless --merge is specified.

Examples:
  agentvault instructions import instructions.json
  agentvault instructions import instructions.yaml --merge
  agentvault instructions import instructions.json --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		merge, _ := cmd.Flags().GetBool("merge")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		format, _ := cmd.Flags().GetString("format")

		raw, err := readImportFile(args[0])
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}

		incoming, err := unmarshalInstructions(raw, format, args[0])
		if err != nil {
			return err
		}

		// Filter invalid instructions first so conflicts are only reported for valid ones.
		var validIncoming []agent.InstructionFile
		var invalidIncoming []error
		for _, inst := range incoming {
			if err := agent.ValidateInstructionScope(inst); err != nil {
				invalidIncoming = append(invalidIncoming, err)
			} else {
				validIncoming = append(validIncoming, inst)
			}
		}

		// Deduplicate validIncoming by composite key; last occurrence wins.
		seenKeys := make(map[string]int)
		for i, inst := range validIncoming {
			seenKeys[agent.InstructionKey(inst)] = i
		}
		deduped := make([]agent.InstructionFile, 0, len(seenKeys))
		for i, inst := range validIncoming {
			if seenKeys[agent.InstructionKey(inst)] == i {
				deduped = append(deduped, inst)
			}
		}
		validIncoming = deduped

		existing := v.ListInstructions()
		keep, _ := cmd.Flags().GetBool("keep-bundle-commands")
		validIncoming, unchanged, leftOut, notText := screenImportedInstructions(validIncoming, existing, merge, keep)
		invalidIncoming = append(invalidIncoming, notText...)
		conflicts := agent.CheckInstructionConflicts(existing, validIncoming)
		defer func() {
			if unchanged > 0 {
				fmt.Printf("Unchanged: %d instruction(s) already match this vault.\n", unchanged)
			}
			prefix := "Left out"
			if dryRun {
				prefix = "Dry-run: would leave out"
			}
			for _, l := range leftOut {
				fmt.Printf("%s: %s\n", prefix, printable(l))
			}
		}()

		if dryRun {
			wouldImport := len(validIncoming)
			if !merge {
				wouldImport -= len(conflicts)
			}
			fmt.Printf("Dry-run: would import %d instruction(s).\n", wouldImport)
			if len(invalidIncoming) > 0 {
				fmt.Println("Invalid (would be skipped):")
				for _, e := range invalidIncoming {
					fmt.Printf("  %s\n", printable(e.Error()))
				}
			}
			if len(conflicts) > 0 {
				fmt.Println("Conflicts (existing would win):")
				for _, c := range conflicts {
					if c.DirectoryPattern != "" {
						fmt.Printf("  %s [scope: %s, pattern: %s]: %s\n", printable(c.Name), printable(c.IncomingScope), printable(c.DirectoryPattern), printable(c.ResolutionNote))
					} else {
						fmt.Printf("  %s [scope: %s]: %s\n", printable(c.Name), printable(c.IncomingScope), printable(c.ResolutionNote))
					}
				}
			}
			return nil
		}

		incoming = validIncoming

		// Build conflict set for quick lookup using the composite identity key.
		conflictSet := make(map[string]bool)
		for _, c := range conflicts {
			conflictSet[agent.InstructionKey(agent.InstructionFile{
				Name:             c.Name,
				Scope:            c.IncomingScope,
				DirectoryPattern: c.DirectoryPattern,
			})] = true
		}

		imported, skipped := 0, 0
		for _, inst := range incoming {
			if conflictSet[agent.InstructionKey(inst)] && !merge {
				skipped++
				continue
			}
			if err := v.SetInstruction(inst); err != nil {
				return fmt.Errorf("storing instruction %q: %w", inst.Name, err)
			}
			imported++
		}

		fmt.Printf("Imported %d instruction(s)", imported)
		if skipped > 0 {
			fmt.Printf(", skipped %d (use --merge to update)", skipped)
		}
		fmt.Println(".")
		if len(invalidIncoming) > 0 {
			fmt.Println("Invalid (skipped):")
			for _, e := range invalidIncoming {
				fmt.Printf("  %s\n", printable(e.Error()))
			}
		}
		return nil
	},
}

// screenImportedInstructions sorts what instructions import may write, as
// a bundle import does: an instruction equal to this vault's is unchanged
// and not written again, content that is not UTF-8 text is refused, and one
// that runs code (a command file, frontmatter, a shell line) and would add
// or replace an instruction is left out without keep.
func screenImportedInstructions(in, existing []agent.InstructionFile, merge, keep bool) (out []agent.InstructionFile, unchanged int, leftOut []string, notText []error) {
	byKey := make(map[string]agent.InstructionFile, len(existing))
	for _, e := range existing {
		byKey[agent.InstructionKey(e)] = e
	}
	for _, inst := range in {
		have, exists := byKey[agent.InstructionKey(inst)]
		if exists && sameInstructionFile(have, inst) {
			unchanged++
			continue
		}
		if problem := agent.TextProblem([]byte(inst.Content)); problem != "" {
			notText = append(notText, fmt.Errorf("instruction %q: %s", inst.Name, problem))
			continue
		}
		writes := !exists || merge
		if writes && !keep && (agent.InstructionFileRunsCommands(inst.Filename) || proseRunsCommands(inst.Filename, []byte(inst.Content))) {
			leftOut = append(leftOut, fmt.Sprintf("instruction %s (%s): the file adds or changes something that runs code (use --keep-bundle-commands to take it)", inst.Name, inst.Filename))
			continue
		}
		out = append(out, inst)
	}
	return out, unchanged, leftOut, notText
}

func marshalInstructions(instructions []agent.InstructionFile, format string, args []string) ([]byte, error) {
	if format == "" && len(args) > 0 {
		ext := strings.ToLower(filepath.Ext(args[0]))
		if ext == ".yaml" || ext == ".yml" {
			format = "yaml"
		}
	}
	switch format {
	case "", "json", "yaml":
	default:
		return nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	if format == "yaml" {
		return marshalYAML(instructions)
	}
	return marshalJSON(instructions)
}

func unmarshalInstructions(data []byte, format, filename string) ([]agent.InstructionFile, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	if format == "" {
		ext := strings.ToLower(filepath.Ext(filename))
		if ext == ".yaml" || ext == ".yml" {
			format = "yaml"
		} else if ext == ".json" {
			format = "json"
		}
	}
	if format == "yaml" {
		return unmarshalYAML[[]agent.InstructionFile](data)
	}
	if format == "json" {
		return unmarshalJSONSlice[agent.InstructionFile](data)
	}
	// Autodetect: try JSON first; YAML flow-style arrays also start with '['
	// so byte-sniffing is unreliable.
	if result, err := unmarshalJSONSlice[agent.InstructionFile](data); err == nil {
		return result, nil
	}
	return unmarshalYAML[[]agent.InstructionFile](data)
}

func init() {
	rootCmd.AddCommand(instructionsCmd)
	instructionsCmd.AddCommand(instListCmd)
	instructionsCmd.AddCommand(instShowCmd)
	instructionsCmd.AddCommand(instSetCmd)
	instructionsCmd.AddCommand(instEditCmd)
	instructionsCmd.AddCommand(instRemoveCmd)
	instructionsCmd.AddCommand(instPullCmd)
	instructionsCmd.AddCommand(instPushCmd)
	instructionsCmd.AddCommand(instDiffCmd)
	instructionsCmd.AddCommand(instScanCmd)
	instructionsCmd.AddCommand(instPreviewCmd)
	instructionsCmd.AddCommand(instExportCmd)
	instructionsCmd.AddCommand(instImportCmd)

	instSetCmd.Flags().String("file", "", "read content from a file")
	instSetCmd.Flags().String("content", "", "set content inline")
	instSetCmd.Flags().String("filename", "", "override target filename")
	instSetCmd.Flags().String("scope", "", "scope: global (default), directory, or local")
	instSetCmd.Flags().String("directory-pattern", "", "glob pattern for directory scope (requires --scope directory)")

	instPullCmd.Flags().String("name", "", "pull only a specific instruction name")
	instPullCmd.Flags().String("file", "", "filename to read (use with --name)")
	instPullCmd.Flags().Bool("follow-references", true,
		"also store the files the instructions name, and what those name in turn")
	instPullCmd.Flags().Bool("strict", false,
		"fail if a named file is refused or not present, instead of reporting it")

	instPushCmd.Flags().String("name", "", "push only a specific instruction name")

	instPreviewCmd.Flags().String("dir", "", "directory to resolve scope against (default: cwd)")
	instPreviewCmd.Flags().Bool("all", false, "show all resolved instructions")
	instPreviewCmd.Flags().Bool("raw", false, rawFlagUsage)

	instExportCmd.Flags().String("format", "", "output format: json or yaml (default: json)")
	instExportCmd.Flags().String("scope", "", "export only instructions at this scope")

	instImportCmd.Flags().String("format", "", "input format: json or yaml (autodetect by extension)")
	instImportCmd.Flags().Bool("merge", false, "update existing instructions by name+scope+directory_pattern")
	instImportCmd.Flags().Bool("dry-run", false, "validate and report conflicts without writing")
	instImportCmd.Flags().Bool("keep-bundle-commands", false, "take instructions that run code (command files, frontmatter, shell lines); left out by default")

	instRemoveCmd.Flags().String("scope", "", "target scope: global, directory, or local")
	instRemoveCmd.Flags().String("directory-pattern", "", "target directory pattern (use with --scope directory)")

	instShowCmd.Flags().String("scope", "", "target scope: global, directory, or local")
	instShowCmd.Flags().String("directory-pattern", "", "target directory pattern (use with --scope directory)")
	instShowCmd.Flags().Bool("raw", false, rawFlagUsage)

	instEditCmd.Flags().String("scope", "", "target scope: global, directory, or local")
	instEditCmd.Flags().String("directory-pattern", "", "target directory pattern (use with --scope directory)")

	instScanCmd.Flags().String("scope", "", "target scope: global, directory, or local (only used with a name argument)")
	instScanCmd.Flags().String("directory-pattern", "", "target directory pattern (use with --scope directory)")
}

// writeInstructionFile writes inst under dir. It refuses filenames that
// escape dir and symlinked path components, so a crafted bundle entry such
// as "../../.bashrc" cannot land outside the target.
func writeInstructionFile(dir string, inst agent.InstructionFile) error {
	// The same rule as the vault applies on the way in, so a rooted name such
	// as "/tmp/x" is refused on Windows too, where it has no drive letter.
	if err := agent.ValidateInstructionFilename(inst.Filename); err != nil {
		return fmt.Errorf("instruction %q: %w", inst.Name, err)
	}
	p, err := safeJoinUnder(dir, inst.Filename)
	if err != nil {
		return fmt.Errorf("instruction %q: %w", inst.Name, err)
	}
	if err := ensureNoSymlinkBelow(dir, p); err != nil {
		return fmt.Errorf("instruction %q: %w", inst.Name, err)
	}
	if err := fsname.WriteFile(dir, p, []byte(inst.Content), 0755, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}
	return nil
}

// checkInstructionFilenames refuses the whole push when any selected
// instruction has a filename that is not allowed, so nothing is half written.
func checkInstructionFilenames(list []agent.InstructionFile, selected func(agent.InstructionFile) bool) error {
	var errs []string
	for _, inst := range list {
		if !selected(inst) {
			continue
		}
		if err := agent.ValidateInstructionFilename(inst.Filename); err != nil {
			errs = append(errs, fmt.Sprintf("instruction %q: %v", inst.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("nothing written; change or remove these with 'instructions set <name> --filename <new>' or 'instructions remove <name>':\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

var errUnsafeInstructionFilename = errors.New("unsafe instruction filename")

// readInstructionOnDisk reads inst's file under dir. A vault written before
// filenames were validated can hold an escaping name; it is refused, not read.
func readInstructionOnDisk(dir string, inst agent.InstructionFile) ([]byte, error) {
	if err := agent.ValidateInstructionFilename(inst.Filename); err != nil {
		return nil, fmt.Errorf("%w: %v", errUnsafeInstructionFilename, err)
	}
	if strings.TrimSpace(inst.Filename) == "" {
		// Not placed yet: nothing on disk to compare, not an unsafe name.
		return nil, os.ErrNotExist
	}
	p, err := safeJoinUnder(dir, inst.Filename)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUnsafeInstructionFilename, err)
	}
	return readImportFile(p)
}

// readLocalInstruction reads an instruction file pull looks for. A missing
// one is nil. One that is not a regular file of at most
// maxImportFileBytes (a pipe, /dev/zero behind a symlink) is reported,
// counted in refused and nil, so it neither blocks pull nor stops it.
func readLocalInstruction(path string, refused *int) []byte {
	data, err := readImportFile(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return data
	}
	fmt.Fprintf(os.Stderr, "  refused %s\n", printable(err.Error()))
	*refused++
	return nil
}

// ensureNoSymlinkBelow rejects symlinks in the components of p below root.
// root itself and its ancestors are trusted: the caller chose them.
func ensureNoSymlinkBelow(root string, p string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("destination %q is outside %q", p, root)
	}
	current := filepath.Clean(root)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination directory contains a symlink: %q", current)
		}
	}
	return nil
}

// findInstruction returns the stored instruction with name in scope.
func findInstruction(v *vault.Vault, name, scope, dirPattern string) (agent.InstructionFile, bool) {
	key := agent.InstructionKey(agent.InstructionFile{Name: name, Scope: scope, DirectoryPattern: dirPattern})
	for _, inst := range v.ListInstructions() {
		if agent.InstructionKey(inst) == key {
			return inst, true
		}
	}
	return agent.InstructionFile{}, false
}
