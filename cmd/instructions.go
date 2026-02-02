package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

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
var knownFilenameToName = map[string]string{
	"AGENTS.md": "agents",
	"CLAUDE.md": "claude",
	"codex.md":  "codex",
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
			inst := agent.InstructionFile{
				Name:      name,
				Filename:  filename,
				Content:   string(data),
				UpdatedAt: time.Now(),
			}
			if err := v.SetInstruction(inst); err != nil {
				return err
			}
			fmt.Printf("  Pulled %s -> %q (%d bytes)\n", filename, name, len(data))
			pulled++
		}
		// also check .github/copilot-instructions.md
		copilotPath := filepath.Join(dir, ".github", "copilot-instructions.md")
		if data, err := os.ReadFile(copilotPath); err == nil {
			inst := agent.InstructionFile{
				Name:      "copilot",
				Filename:  ".github/copilot-instructions.md",
				Content:   string(data),
				UpdatedAt: time.Now(),
			}
			if err := v.SetInstruction(inst); err != nil {
				return err
			}
			fmt.Printf("  Pulled .github/copilot-instructions.md -> %q (%d bytes)\n", "copilot", len(data))
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
	inst := agent.InstructionFile{
		Name:      name,
		Filename:  filename,
		Content:   string(data),
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

func init() {
	rootCmd.AddCommand(instructionsCmd)
	instructionsCmd.AddCommand(instListCmd)
	instructionsCmd.AddCommand(instShowCmd)
	instructionsCmd.AddCommand(instSetCmd)
	instructionsCmd.AddCommand(instRemoveCmd)
	instructionsCmd.AddCommand(instPullCmd)
	instructionsCmd.AddCommand(instPushCmd)
	instructionsCmd.AddCommand(instDiffCmd)

	instSetCmd.Flags().String("file", "", "read content from a file")
	instSetCmd.Flags().String("content", "", "set content inline")
	instSetCmd.Flags().String("filename", "", "override target filename")

	instPullCmd.Flags().String("name", "", "pull only a specific instruction name")
	instPullCmd.Flags().String("file", "", "filename to read (use with --name)")

	instPushCmd.Flags().String("name", "", "push only a specific instruction name")
}
