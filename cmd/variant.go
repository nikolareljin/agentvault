package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var variantCmd = &cobra.Command{
	Use:   "variant",
	Short: "List and pick the default of agent variant groups",
	Long: `A variant group ties existing agents together as alternates of one
logical setup, for example one Claude setup run through anthropic, bedrock,
vertex and a gateway. Each group has one default agent. Put an agent in a
group with 'agentvault add|edit NAME --variant-group GROUP [--variant-default]'.

'agentvault prompt GROUP' runs the group's default agent. Runtime routing
and complexity-aware selection between variants are separate features.`,
}

var variantListCmd = &cobra.Command{
	Use:   "list [group]",
	Short: "List variant groups, their agents and the default",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		group := ""
		if len(args) == 1 {
			group = args[0]
		}
		return printVariantList(os.Stdout, v.List(), group)
	},
}

var variantDefaultCmd = &cobra.Command{
	Use:   "default <group> <agent>",
	Short: "Make an agent the default of its variant group",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		if err := v.SetVariantDefault(args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Variant group %s: default is now %s.\n", printable(args[0]), printable(args[1]))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(variantCmd)
	variantCmd.AddCommand(variantListCmd, variantDefaultCmd)
}

// printVariantList prints each group (or only group) with its members.
func printVariantList(out io.Writer, agents []agent.Agent, group string) error {
	groups := agent.VariantGroups(agents)
	if group != "" {
		if len(agent.VariantMembers(agents, group)) == 0 {
			return fmt.Errorf("variant group %q not found", group)
		}
		groups = []string{group}
	}
	if len(groups) == 0 {
		fmt.Fprintln(out, "No variant groups. Use 'agentvault edit NAME --variant-group GROUP' to add one.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "GROUP\tAGENT\tDEFAULT\tBACKEND\tMODEL")
	for _, g := range groups {
		def, _ := agent.VariantDefault(agents, g)
		for _, m := range agent.VariantMembers(agents, g) {
			mark := ""
			if m.Name == def.Name {
				mark = "yes"
			}
			model := m.Model
			if model == "" {
				model = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", printable(g), printable(m.Name), mark, printable(variantBackendLabel(m)), printable(model))
		}
	}
	return w.Flush()
}

// variantBackendLabel is the backend a member runs on: the Claude backend,
// or the provider for other agents.
func variantBackendLabel(a agent.Agent) string {
	if a.Provider != agent.ProviderClaude {
		return string(a.Provider)
	}
	label := string(a.Provider) + "/" + agent.NormalizeClaudeBackend(a.Backend)
	if agent.ClaudeBackendDiagnostic(a.Backend) != "" {
		label = string(a.Provider) + "/" + a.Backend + " (unknown)"
	}
	return label
}

// registerVariantFlags adds the variant group and Claude backend setting
// flags shared by add and edit.
func registerVariantFlags(fs *pflag.FlagSet) {
	fs.String("variant-group", "", "variant group this agent belongs to (\"\" on edit removes it)")
	fs.Bool("variant-default", false, "make this agent the default of its variant group")
	fs.String("vertex-region", "", "claude vertex backend: Google Cloud region (CLOUD_ML_REGION)")
	fs.String("vertex-project", "", "claude vertex backend: Google Cloud project (ANTHROPIC_VERTEX_PROJECT_ID)")
	fs.String("gateway", "", "claude gateway backend: stored gateway whose URL and token to use")
	fs.String("auth-mode", "", "auth mode (api_key, token, iam, oauth, none); token sends the key as ANTHROPIC_AUTH_TOKEN")
}

// applyVariantFlags copies the variant and backend setting flags that were
// given into a. Unset flags leave a unchanged.
func applyVariantFlags(cmd *cobra.Command, a *agent.Agent) error {
	fs := cmd.Flags()
	if fs.Changed("variant-group") {
		g, _ := fs.GetString("variant-group")
		g = strings.TrimSpace(g)
		if g == "" {
			a.Variant = nil
		} else {
			def := a.IsVariantDefault() && a.VariantGroup() == g
			a.Variant = &agent.AgentVariant{Group: g, Default: def}
		}
	}
	if fs.Changed("variant-default") {
		d, _ := fs.GetBool("variant-default")
		if a.Variant == nil {
			return errors.New("--variant-default needs --variant-group")
		}
		vv := *a.Variant
		vv.Default = d
		a.Variant = &vv
	}
	meta := func() *agent.AgentProviderMeta {
		if a.ProviderMeta == nil {
			a.ProviderMeta = &agent.AgentProviderMeta{}
		}
		return a.ProviderMeta
	}
	if fs.Changed("vertex-region") {
		meta().VertexRegion, _ = fs.GetString("vertex-region")
	}
	if fs.Changed("vertex-project") {
		meta().VertexProject, _ = fs.GetString("vertex-project")
	}
	if fs.Changed("gateway") {
		meta().GatewayName, _ = fs.GetString("gateway")
	}
	if fs.Changed("auth-mode") {
		meta().AuthMode, _ = fs.GetString("auth-mode")
	}
	return nil
}

// checkVariantGateway refuses a Claude gateway agent naming a stored
// gateway this vault does not have.
func checkVariantGateway(v *vault.Vault, a agent.Agent) error {
	if a.ProviderMeta == nil || a.ProviderMeta.GatewayName == "" {
		return nil
	}
	if _, ok := v.GetGateway(a.ProviderMeta.GatewayName); !ok {
		return fmt.Errorf("gateway %q not found (see agentvault gateway list)", a.ProviderMeta.GatewayName)
	}
	return nil
}

// printVariantNotes prints the vault's variant notes to stderr.
func printVariantNotes(v *vault.Vault) {
	for _, n := range v.VariantNotes() {
		fmt.Fprintf(os.Stderr, "note: %s\n", printable(n))
	}
}
