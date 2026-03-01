package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

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
		for _, inst := range instructions {
			age := ""
			if !inst.UpdatedAt.IsZero() {
				age = inst.UpdatedAt.Format("2006-01-02 15:04")
			}
			fmt.Printf("  %-12s -> %-40s  (%d bytes, %s)\n",
				inst.Name, inst.Filename, len(inst.Content), age)
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
		inst, ok := v.GetInstruction(args[0])
		if !ok {
			return fmt.Errorf("instruction %q not found", args[0])
		}
		fmt.Print(inst.Content)
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

		if filePath == "" && content == "" {
			return fmt.Errorf("provide --file or --content")
		}
		if filePath != "" && content != "" {
			return fmt.Errorf("use either --file or --content, not both")
		}

		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			content = string(data)
		}

		if filename == "" {
			filename = agent.FilenameForInstruction(name)
		}

		// Scan for prompt hijacking
		if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
			fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
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
		fmt.Printf("Instruction %q stored (%d bytes, target: %s).\n", name, len(content), filename)
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
		inst, ok := v.GetInstruction(name)
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
			fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
		}

		inst.Content = newContent
		inst.UpdatedAt = time.Now()
		if err := v.SetInstruction(inst); err != nil {
			return err
		}
		fmt.Printf("Instruction %q updated (%d bytes).\n", name, len(newContent))
		return nil
	},
}

var instRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a stored instruction file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		if err := v.RemoveInstruction(args[0]); err != nil {
			return err
		}
		fmt.Printf("Instruction %q removed.\n", args[0])
		return nil
	},
}

// knownFilenameToName maps filenames on disk to instruction names for pull.
// This should stay in sync with agent.WellKnownInstructions (the reverse map).
var knownFilenameToName = map[string]string{
	"AGENTS.md":      "agents",
	"CLAUDE.md":      "claude",
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
		pulled := 0
		// check well-known files
		for filename, name := range knownFilenameToName {
			p := filepath.Join(dir, filename)
			data, err := os.ReadFile(p)
			if err != nil {
				continue // file doesn't exist, skip
			}
			content := string(data)
			if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
				fmt.Fprintf(os.Stderr, "  [%s] %s\n", name, filename)
				fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
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
			fmt.Printf("  Pulled %s -> %q (%d bytes)\n", filename, name, len(data))
			pulled++
		}
		// also check files in subdirectories
		subdirFiles := map[string]string{
			filepath.Join(".github", "copilot-instructions.md"): "copilot",
		}
		for relPath, name := range subdirFiles {
			fullPath := filepath.Join(dir, relPath)
			if data, err := os.ReadFile(fullPath); err == nil {
				content := string(data)
				if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
					fmt.Fprintf(os.Stderr, "  [%s] %s\n", name, relPath)
					fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
				}
				inst := agent.InstructionFile{
					Name:      name,
					Filename:  relPath,
					Content:   content,
					UpdatedAt: time.Now(),
				}
				if err := v.SetInstruction(inst); err != nil {
					return err
				}
				fmt.Printf("  Pulled %s -> %q (%d bytes)\n", relPath, name, len(data))
				pulled++
			}
		}

		// also check .aider.conf.yml
		aiderPath := filepath.Join(dir, ".aider.conf.yml")
		if data, err := os.ReadFile(aiderPath); err == nil {
			content := string(data)
			if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
				fmt.Fprintf(os.Stderr, "  [aider] .aider.conf.yml\n")
				fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
			}
			inst := agent.InstructionFile{
				Name:      "aider",
				Filename:  ".aider.conf.yml",
				Content:   content,
				UpdatedAt: time.Now(),
			}
			if err := v.SetInstruction(inst); err != nil {
				return err
			}
			fmt.Printf("  Pulled .aider.conf.yml -> %q (%d bytes)\n", "aider", len(data))
			pulled++
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
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p, err)
	}
	content := string(data)

	// Scan for prompt hijacking
	if warnings := agent.CheckHijacking(content); len(warnings) > 0 {
		fmt.Fprintf(os.Stderr, "  [%s] %s\n", name, filename)
		fmt.Fprint(os.Stderr, agent.FormatWarnings(warnings))
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
	fmt.Printf("Pulled %s -> %q (%d bytes)\n", filename, name, len(data))
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
		specificName, _ := cmd.Flags().GetString("name")

		instructions := v.ListInstructions()
		if len(instructions) == 0 {
			fmt.Println("No instruction files stored. Use 'pull' or 'set' first.")
			return nil
		}

		pushed := 0
		for _, inst := range instructions {
			if specificName != "" && inst.Name != specificName {
				continue
			}
			p := filepath.Join(dir, inst.Filename)
			// ensure parent directories exist (for paths like .github/copilot-instructions.md)
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return fmt.Errorf("creating directory for %s: %w", inst.Filename, err)
			}
			if err := os.WriteFile(p, []byte(inst.Content), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", p, err)
			}
			fmt.Printf("  Pushed %q -> %s (%d bytes)\n", inst.Name, inst.Filename, len(inst.Content))
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
		instructions := v.ListInstructions()
		if len(instructions) == 0 {
			fmt.Println("No instruction files stored.")
			return nil
		}

		diffs := 0
		for _, inst := range instructions {
			p := filepath.Join(dir, inst.Filename)
			diskData, err := os.ReadFile(p)
			if err != nil {
				fmt.Printf("  %-12s  %s  (not on disk)\n", inst.Name, inst.Filename)
				diffs++
				continue
			}
			diskContent := string(diskData)
			if diskContent == inst.Content {
				fmt.Printf("  %-12s  %s  (identical)\n", inst.Name, inst.Filename)
			} else {
				vaultLines := strings.Count(inst.Content, "\n")
				diskLines := strings.Count(diskContent, "\n")
				fmt.Printf("  %-12s  %s  (DIFFERS: vault %d lines, disk %d lines)\n",
					inst.Name, inst.Filename, vaultLines, diskLines)
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
					fmt.Printf("  %-12s  %s  (on disk, not in vault)\n", name, filename)
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

		var toScan []agent.InstructionFile
		if len(args) > 0 {
			inst, ok := v.GetInstruction(args[0])
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
				fmt.Printf("--- %s (%s) ---\n", inst.Name, inst.Filename)
				fmt.Print(agent.FormatWarnings(warnings))
				fmt.Println()
				totalWarnings += len(warnings)
			} else {
				fmt.Printf("  %-12s  %s  (clean)\n", inst.Name, inst.Filename)
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

	instSetCmd.Flags().String("file", "", "read content from a file")
	instSetCmd.Flags().String("content", "", "set content inline")
	instSetCmd.Flags().String("filename", "", "override target filename")

	instPullCmd.Flags().String("name", "", "pull only a specific instruction name")
	instPullCmd.Flags().String("file", "", "filename to read (use with --name)")

	instPushCmd.Flags().String("name", "", "push only a specific instruction name")
}
