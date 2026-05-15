package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage individual agent profiles (export, import)",
	Long: `Export and import single-agent profiles in JSON or YAML format.
Agent profiles capture full provider metadata for portable, round-trip setup.`,
}

var agentExportCmd = &cobra.Command{
	Use:   "export <name> [file]",
	Short: "Export a single agent profile to JSON or YAML",
	Long: `Export a single configured agent to a portable profile file.
If no file is given, the profile is printed to stdout.

Examples:
  agentvault agent export claude-prod
  agentvault agent export claude-prod profile.yaml --format yaml
  agentvault agent export claude-prod profile.json --include-key`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		a, ok := v.Get(name)
		if !ok {
			return fmt.Errorf("agent %q not found", name)
		}

		includeKey, _ := cmd.Flags().GetBool("include-key")
		if !includeKey {
			a.APIKey = ""
		}

		format, _ := cmd.Flags().GetString("format")
		// Autodetect format from file extension when not explicit.
		if format == "" && len(args) == 2 {
			ext := strings.ToLower(filepath.Ext(args[1]))
			if ext == ".yaml" || ext == ".yml" {
				format = "yaml"
			}
		}

		data, err := marshalAgentProfile(a, format)
		if err != nil {
			return err
		}

		if len(args) == 1 {
			fmt.Println(string(data))
			return nil
		}

		if err := os.WriteFile(args[1], data, 0600); err != nil {
			return fmt.Errorf("writing profile: %w", err)
		}
		fmt.Printf("Agent %q exported to %s (schema version %s).\n", name, args[1], agentProfileSchemaVersion)
		return nil
	},
}

var agentImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import an agent profile from JSON or YAML",
	Long: `Import a single agent profile from a JSON or YAML file. The format is
autodetected from the file extension or first byte. Use --merge to update an
existing agent with the same name. Use --dry-run to validate without writing.

Examples:
  agentvault agent import profile.json
  agentvault agent import profile.yaml --merge
  agentvault agent import profile.json --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		raw, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading profile: %w", err)
		}

		format, _ := cmd.Flags().GetString("format")
		// Autodetect from extension when not explicit.
		if format == "" {
			ext := strings.ToLower(filepath.Ext(args[0]))
			if ext == ".yaml" || ext == ".yml" {
				format = "yaml"
			}
		}

		a, schemaVersion, err := unmarshalAgentProfile(raw, format)
		if err != nil {
			return err
		}
		if schemaVersion != agentProfileSchemaVersion {
			fmt.Fprintf(os.Stderr, "warning: profile schema version %q (expected %q); proceeding\n",
				schemaVersion, agentProfileSchemaVersion)
		}

		if err := a.Validate(); err != nil {
			return fmt.Errorf("invalid agent profile: %w", err)
		}

		merge, _ := cmd.Flags().GetBool("merge")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		existing, exists := v.Get(a.Name)

		if dryRun {
			if exists && !merge {
				fmt.Printf("Dry-run: agent %q already exists; --merge required to update.\n", a.Name)
			} else if exists {
				fmt.Printf("Dry-run: would update agent %q.\n", a.Name)
			} else {
				fmt.Printf("Dry-run: would add agent %q.\n", a.Name)
			}
			return nil
		}

		if exists {
			if !merge {
				return fmt.Errorf("agent %q already exists; use --merge to update", a.Name)
			}
			a = prepareAgentProfileMerge(existing, a, time.Now())
			if err := v.Update(a); err != nil {
				return fmt.Errorf("updating agent: %w", err)
			}
			fmt.Printf("Agent %q updated.\n", a.Name)
		} else {
			if err := v.Add(a); err != nil {
				return fmt.Errorf("adding agent: %w", err)
			}
			fmt.Printf("Agent %q imported.\n", a.Name)
		}
		return nil
	},
}

func prepareAgentProfileMerge(existing, imported agent.Agent, now time.Time) agent.Agent {
	imported.CreatedAt = existing.CreatedAt
	imported.UpdatedAt = now
	if imported.APIKey == "" {
		imported.APIKey = existing.APIKey
	}
	return imported
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentExportCmd)
	agentCmd.AddCommand(agentImportCmd)

	agentExportCmd.Flags().String("format", "", "output format: json or yaml (default: json)")
	agentExportCmd.Flags().Bool("include-key", false, "include API key in exported profile")

	agentImportCmd.Flags().String("format", "", "input format: json or yaml (autodetect)")
	agentImportCmd.Flags().Bool("merge", false, "update existing agent with same name")
	agentImportCmd.Flags().Bool("dry-run", false, "validate and report without writing")
}
