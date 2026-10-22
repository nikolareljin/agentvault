package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
  agentvault agent export claude-prod profile.json --include-key --confirm`,
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
		if includeKey {
			confirm, _ := cmd.Flags().GetBool("confirm")
			out := "the output"
			if len(args) == 2 {
				out = args[1]
			}
			if err := confirmPlaintextExport(confirm, "the API key and secrets", out, "drop --include-key"); err != nil {
				return err
			}
		}
		blanked := false
		var blanks []string
		if !includeKey {
			a, blanked, blanks = blankAgentForExport(a)
		}

		format, _ := cmd.Flags().GetString("format")
		// Autodetect format from file extension when not explicit.
		if format == "" && len(args) == 2 {
			ext := strings.ToLower(filepath.Ext(args[1]))
			if ext == ".yaml" || ext == ".yml" {
				format = "yaml"
			}
		}

		marshal, version := marshalAgentProfile, agentProfileSchemaVersion
		if blanked {
			version = blankedAgentProfileSchemaVersion
			marshal = func(a agent.Agent, format string) ([]byte, error) {
				return marshalBlankedAgentProfile(a, blanks, format)
			}
		}
		data, err := marshal(a, format)
		if err != nil {
			return err
		}

		if len(args) == 1 {
			fmt.Println(string(escapeC1JSON(data)))
			return nil
		}

		if err := os.WriteFile(args[1], data, 0600); err != nil {
			return fmt.Errorf("writing profile: %w", err)
		}
		fmt.Printf("Agent %q exported to %s (schema version %s).\n", name, args[1], version)
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

		a, schemaVersion, blanks, err := decodeAgentProfile(raw, format)
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

		prefix := "Left out"
		if dryRun {
			prefix = "Dry-run: would leave out"
		}
		if !exists || merge {
			keep, _ := cmd.Flags().GetBool("keep-bundle-commands")
			a = prepareImportedAgent(os.Stderr, a, existing, exists, blanks, keep, prefix)
		}

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

		if exists && !merge {
			return fmt.Errorf("agent %q already exists; use --merge to update", a.Name)
		}
		if exists {
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

// prepareAgentProfileMerge fills the profile's blanks from the existing
// agent, as a bundle import does: only where the profile points at the same
// endpoint, so a profile cannot send this machine's key elsewhere. left
// names each blank it did not fill.
func prepareAgentProfileMerge(existing, imported agent.Agent, now time.Time) (agent.Agent, []string) {
	imported, left := restoreAgentSecrets(imported, existing, true)
	imported.CreatedAt = existing.CreatedAt
	imported.UpdatedAt = now
	if imported.APIKey == "" && existing.APIKey != "" {
		if sameAgentEndpoint(imported, existing) {
			imported.APIKey = existing.APIKey
		} else {
			left = append(left, "api_key")
		}
	}
	return imported, left
}

// blankAgentForExport is a without its API key and its other secrets,
// blanked as a bundle blanks them, so an import on this machine can fill
// them back. blanked is whether anything changed; blanks names each field.
func blankAgentForExport(a agent.Agent) (out agent.Agent, blanked bool, blanks []string) {
	a.APIKey = ""
	b := SetupBundle{Agents: []agent.Agent{a}}
	redactVaultSecrets(&b)
	for _, vb := range b.VaultBlanks {
		blanks = append(blanks, vb.Field)
	}
	return b.Agents[0], !reflect.DeepEqual(a, b.Agents[0]), blanks
}

// prepareImportedAgent is the agent an import adds, or with exists the one
// it merges over existing: blanks filled from existing where it points at
// the same endpoint and dropped otherwise, MCP server commands it adds or
// changes left out without keep. It writes one prefix line per field it
// leaves out to w.
func prepareImportedAgent(w io.Writer, a, existing agent.Agent, exists bool, blanks []string, keep bool, prefix string) agent.Agent {
	var left []string
	if exists {
		a, left = prepareAgentProfileMerge(existing, a, time.Now())
	} else {
		existing = agent.Agent{}
		a, left = restoreAgentSecrets(a, agent.Agent{}, false)
	}
	for _, f := range blanks {
		if !containsString(left, f) && dropAgentBlank(&a, f) {
			left = append(left, f)
		}
	}
	// MCP server commands run in the agent: new or changed ones need
	// --keep-bundle-commands, as on import.
	rep := &importReport{}
	a.MCPServers = gateBundleCommands(a.MCPServers, existing.MCPServers,
		mcpServerName, sameMCPCommand, func(agent.MCPServer) bool { return true },
		importConsent{KeepCommands: keep}.takesMCP, strategyReplace, "agent "+a.Name+" MCP server", rep)
	for _, l := range rep.Lines {
		fmt.Fprintf(w, "%s\n", strings.Replace(strings.TrimSpace(l), "Left out", prefix, 1))
	}
	for _, f := range left {
		// A server kept as this machine's has its own secrets back.
		if mcpBlankOfKeptServer(f, a.MCPServers, existing.MCPServers) {
			continue
		}
		fmt.Fprintf(w, "%s: agent %s %s: blank in the profile, and not filled from this machine's agent\n", prefix, printable(a.Name), printable(f))
	}
	return a
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentExportCmd)
	agentCmd.AddCommand(agentImportCmd)

	agentExportCmd.Flags().String("format", "", "output format: json or yaml (default: json)")
	agentExportCmd.Flags().Bool("confirm", false, "allow --include-key to write the key and secrets in plain text")
	agentExportCmd.Flags().Bool("include-key", false, "include the API key and the agent's other secrets (MCP server env and secret args, credential extras, a base URL with credentials) in the exported profile")

	agentImportCmd.Flags().String("format", "", "input format: json or yaml (autodetect)")
	agentImportCmd.Flags().Bool("merge", false, "update existing agent with same name")
	agentImportCmd.Flags().Bool("dry-run", false, "validate and report without writing")
	agentImportCmd.Flags().Bool("keep-bundle-commands", false, "keep MCP server commands that the profile adds or changes; left out by default")
}

// mcpBlankOfKeptServer is whether blank names a field of an MCP server that
// was left out, or that is now exactly this machine's server: either way it
// is reported once by the command gate, not as a blank.
func mcpBlankOfKeptServer(blank string, final, local []agent.MCPServer) bool {
	for _, f := range final {
		if !strings.HasPrefix(blank, "MCP server "+f.Name+" ") {
			continue
		}
		for _, l := range local {
			if l.Name == f.Name && reflect.DeepEqual(l, f) {
				return true
			}
		}
		return false
	}
	return strings.HasPrefix(blank, "MCP server ")
}
