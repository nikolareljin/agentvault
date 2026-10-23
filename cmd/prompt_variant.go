package cmd

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	routerpkg "github.com/nikolareljin/agentvault/internal/router"
	"github.com/spf13/cobra"
)

// variantChoice records which member of a variant group a prompt runs and
// why. It goes to stderr, to --json output and to prompt history.
type variantChoice struct {
	Variant string `json:"variant"`
	Group   string `json:"group"`
	Policy  string `json:"policy"`
	Tier    string `json:"tier"`
	// Fallbacks has one "AGENT: reason" entry per member skipped before
	// Variant, in the order they were tried.
	Fallbacks []string `json:"-"`
	// Overridden is true when --variant or --tier was given.
	Overridden bool `json:"-"`
}

// FallbackReason joins the skipped members and their reasons.
func (c *variantChoice) FallbackReason() string {
	return strings.Join(c.Fallbacks, "; ")
}

// promptLookPath finds provider CLIs; tests replace it.
var promptLookPath = exec.LookPath

func promptFlagString(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return strings.TrimSpace(f.Value.String())
	}
	return ""
}

// resolveVariantChoice picks the member of group to run for promptText:
// the --variant pin, else the group policy over the prompt's tier (or
// --tier), skipping members that fail the pre-flight check. The default
// policy runs the group default with no check, as before policies existed.
func resolveVariantChoice(cmd *cobra.Command, v vaultLike, group, promptText string) (agent.Agent, *variantChoice, error) {
	agents := v.List()
	pin := promptFlagString(cmd, "variant")
	tierFlag := promptFlagString(cmd, "tier")
	stored := agent.VariantPolicyOf(agents, group)
	policy, err := agent.ParseVariantPolicy(stored)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: variant group %s has unknown policy %q; using %s\n", printable(group), printable(stored), agent.VariantPolicyDefault)
		policy = agent.VariantPolicyDefault
	}
	choice := &variantChoice{Group: group, Policy: policy, Overridden: pin != "" || tierFlag != ""}
	if tierFlag != "" {
		t, err := agent.ParseVariantTier(tierFlag)
		if err != nil {
			return agent.Agent{}, nil, err
		}
		choice.Tier = t
	} else {
		choice.Tier = routerpkg.ScoreComplexity(promptText).Tier
	}

	if pin != "" {
		for _, m := range agent.VariantMembers(agents, group) {
			if m.Name == pin {
				choice.Variant = m.Name
				return withVariantGateway(v, m), choice, nil
			}
		}
		return agent.Agent{}, nil, fmt.Errorf("agent %q is not in variant group %q", pin, group)
	}

	candidates := agent.VariantCandidates(agents, group, policy, choice.Tier)
	if policy == agent.VariantPolicyDefault {
		choice.Variant = candidates[0].Name
		return withVariantGateway(v, candidates[0]), choice, nil
	}
	for _, c := range candidates {
		resolved := withVariantGateway(v, c)
		if reason := variantPreflight(v, resolved); reason != "" {
			choice.Fallbacks = append(choice.Fallbacks, c.Name+": "+reason)
			continue
		}
		choice.Variant = c.Name
		return resolved, choice, nil
	}
	return agent.Agent{}, nil, fmt.Errorf("no variant of group %q can run: %s", group, choice.FallbackReason())
}

// variantPreflight returns why a cannot start, or "": its runner must be
// one prompt can execute, its CLI must be on PATH, and its backend must
// have the credential or settings it needs. It makes no network call.
func variantPreflight(v vaultLike, a agent.Agent) string {
	rt := agent.ResolvePromptRuntimeConfig(a)
	resolved := a
	resolved.APIKey = rt.APIKey.Value
	resolved.BaseURL = rt.BaseURL.Value
	gatewayExists := func(name string) bool {
		lookup, ok := v.(promptGatewayLookup)
		if !ok {
			return false
		}
		_, found := lookup.GetGateway(name)
		return found
	}
	if p := agent.VariantCredentialProblem(resolved, nil, gatewayExists); p != "" {
		return p
	}
	target := agent.ResolveExecutionTarget(resolved)
	bin := ""
	switch target.Runner {
	case agent.RunnerClaudeCLI:
		bin = "claude"
	case agent.RunnerCodexCLI:
		bin = "codex"
	case agent.RunnerGeminiCLI:
		bin = "gemini"
	case agent.RunnerOllamaHTTP, agent.RunnerOpenAIHTTP:
		return ""
	case agent.RunnerBedrockAPI:
		return "bedrock backend execution is not supported yet"
	default:
		return fmt.Sprintf("runner %q is not supported by prompt", target.Runner)
	}
	if _, err := promptLookPath(bin); err != nil {
		return bin + " binary not found in PATH"
	}
	return ""
}

// printVariantChoice writes the one-line summary and one line per fallback.
func printVariantChoice(w io.Writer, c *variantChoice) {
	fmt.Fprintf(w, "variant: %s (group %s, policy %s, tier %s)\n", printable(c.Variant), printable(c.Group), c.Policy, c.Tier)
	for _, f := range c.Fallbacks {
		name, reason, _ := strings.Cut(f, ": ")
		fmt.Fprintf(w, "fallback from %s: %s\n", printable(name), printable(reason))
	}
}

// showVariantChoice reports whether the choice line is printed: not for
// the default policy without overrides, whose output is unchanged.
func showVariantChoice(c *variantChoice) bool {
	return c != nil && (c.Policy != agent.VariantPolicyDefault || c.Overridden)
}

// addVariantChoiceJSON adds the choice fields to a --json payload.
func addVariantChoiceJSON(payload map[string]any, c *variantChoice) {
	if c == nil {
		return
	}
	payload["variant"] = c.Variant
	payload["group"] = c.Group
	payload["policy"] = c.Policy
	payload["tier"] = c.Tier
	payload["fallback_reason"] = c.FallbackReason()
}

var errVariantFlagsNeedGroup = errors.New("--variant and --tier need a variant group name, not an agent name")
