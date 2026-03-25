package cmd

import (
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerRouteConfigFlags(fs *pflag.FlagSet) {
	fs.String("route-capabilities", "", "comma-separated routing capabilities: general,coding,review,analysis")
	fs.String("latency-tier", "", "routing latency tier: low|medium|high")
	fs.String("cost-tier", "", "routing cost tier: low|medium|high")
	fs.String("privacy-tier", "", "routing privacy tier: local|restricted|remote")
	fs.Int("route-priority", 0, "routing priority score adjustment")
	fs.Bool("disable-routing", false, "exclude this agent from automatic routing")
}

func applyRouteConfigFlags(cmd *cobra.Command, cfg *agent.RouteConfig) {
	if cmd.Flags().Changed("route-capabilities") {
		values, _ := cmd.Flags().GetString("route-capabilities")
		cfg.Capabilities = splitCommaList(values)
	}
	if cmd.Flags().Changed("latency-tier") {
		cfg.LatencyTier, _ = cmd.Flags().GetString("latency-tier")
		cfg.LatencyTier = strings.ToLower(strings.TrimSpace(cfg.LatencyTier))
	}
	if cmd.Flags().Changed("cost-tier") {
		cfg.CostTier, _ = cmd.Flags().GetString("cost-tier")
		cfg.CostTier = strings.ToLower(strings.TrimSpace(cfg.CostTier))
	}
	if cmd.Flags().Changed("privacy-tier") {
		cfg.PrivacyTier, _ = cmd.Flags().GetString("privacy-tier")
		cfg.PrivacyTier = strings.ToLower(strings.TrimSpace(cfg.PrivacyTier))
	}
	if cmd.Flags().Changed("route-priority") {
		cfg.Priority, _ = cmd.Flags().GetInt("route-priority")
	}
	if cmd.Flags().Changed("disable-routing") {
		cfg.Disabled, _ = cmd.Flags().GetBool("disable-routing")
	}
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func resolvedRoutingAgents(agents []agent.Agent) []agent.Agent {
	resolved := make([]agent.Agent, len(agents))
	for i, original := range agents {
		copyAgent := original
		runtimeCfg := agent.ResolvePromptRuntimeConfig(original)
		if runtimeCfg.Model.Value != "" {
			copyAgent.Model = runtimeCfg.Model.Value
		}
		if runtimeCfg.APIKey.Value != "" {
			copyAgent.APIKey = runtimeCfg.APIKey.Value
		}
		if runtimeCfg.BaseURL.Value != "" {
			copyAgent.BaseURL = runtimeCfg.BaseURL.Value
		}
		resolved[i] = copyAgent
	}
	return resolved
}
