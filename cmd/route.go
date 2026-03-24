package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	routerpkg "github.com/nikolareljin/agentvault/internal/router"
	"github.com/spf13/cobra"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Explain which agent target would handle a prompt",
	Long: `Analyze a prompt and select the best execution target without running it.

This command uses the same routing logic as 'agentvault prompt --auto' and is
useful for inspecting why AgentVault prefers a given agent, runner, and model.`,
	RunE: runRoute,
}

func init() {
	rootCmd.AddCommand(routeCmd)
	routeCmd.Flags().String("text", "", "prompt text")
	routeCmd.Flags().String("file", "", "read prompt text from file")
	routeCmd.Flags().Bool("json", false, "output machine-readable JSON")
	routeCmd.Flags().String("router", "", "router mode override: heuristic|langgraph")
	routeCmd.Flags().String("langgraph-cmd", "", "langgraph router command override (or set AGENTVAULT_LANGGRAPH_ROUTER_CMD)")
	routeCmd.Flags().Bool("prefer-local", false, "prefer local execution targets during routing")
	routeCmd.Flags().Bool("prefer-fast", false, "prefer lower-latency targets during routing")
	routeCmd.Flags().Bool("prefer-low-cost", false, "prefer lower-cost targets during routing")
	routeCmd.Flags().Bool("local-only", false, "restrict routing to local execution targets only")
}

func runRoute(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	text, _, err := readOptionalPromptInput(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("prompt is empty")
	}
	decision, err := routerpkg.Route(routerpkg.Request{
		Prompt: text,
		Agents: v.List(),
		Shared: v.SharedConfig(),
		Config: promptRouterOverride(cmd),
	})
	if err != nil {
		return err
	}
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(decision)
	}

	selected := decision.Selected
	fmt.Fprintf(os.Stdout, "Selected: %s\n", selected.Agent.Name)
	fmt.Fprintf(os.Stdout, "Provider: %s\n", selected.Agent.Provider)
	fmt.Fprintf(os.Stdout, "Runner: %s\n", selected.Target.Runner)
	fmt.Fprintf(os.Stdout, "Model: %s\n", chooseDisplayValue(selected.Target.Model))
	fmt.Fprintf(os.Stdout, "Task class: %s\n", decision.Intent.TaskClass)
	fmt.Fprintln(os.Stdout, "Reasons:")
	for _, reason := range selected.Reasons {
		fmt.Fprintf(os.Stdout, " - %s\n", reason)
	}
	if len(decision.Fallbacks) > 0 {
		fmt.Fprintln(os.Stdout, "Fallbacks:")
		for _, fallback := range decision.Fallbacks {
			fmt.Fprintf(os.Stdout, " - %s via %s (%s)\n", fallback.Agent.Name, fallback.Target.Runner, chooseDisplayValue(fallback.Target.Model))
		}
	}
	return nil
}

func chooseDisplayValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
