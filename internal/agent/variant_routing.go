package agent

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Variant tiers, from cheapest to deepest.
const (
	VariantTierSimple   = "simple"
	VariantTierStandard = "standard"
	VariantTierDeep     = "deep"
)

// VariantTiers lists the tiers in rank order.
var VariantTiers = []string{VariantTierSimple, VariantTierStandard, VariantTierDeep}

// Variant group routing policies.
const (
	VariantPolicyDefault      = "default"
	VariantPolicyCostFirst    = "cost-first"
	VariantPolicyBalanced     = "balanced"
	VariantPolicyQualityFirst = "quality-first"
)

// VariantPolicies lists the policies a group can have.
var VariantPolicies = []string{VariantPolicyDefault, VariantPolicyCostFirst, VariantPolicyBalanced, VariantPolicyQualityFirst}

// ParseVariantTier checks a tier name. "" is valid and means untiered.
func ParseVariantTier(raw string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(raw))
	if t == "" || tierRank(t) >= 0 {
		return t, nil
	}
	return "", fmt.Errorf("unknown variant tier %q (known: %s)", raw, strings.Join(VariantTiers, ", "))
}

// ParseVariantPolicy checks a policy name. "" means default.
func ParseVariantPolicy(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return VariantPolicyDefault, nil
	}
	for _, known := range VariantPolicies {
		if p == known {
			return p, nil
		}
	}
	return "", fmt.Errorf("unknown variant policy %q (known: %s)", raw, strings.Join(VariantPolicies, ", "))
}

// tierRank returns 0, 1, 2 for simple, standard, deep, or -1.
func tierRank(t string) int {
	for i, known := range VariantTiers {
		if t == known {
			return i
		}
	}
	return -1
}

// VariantPolicyOf returns the policy of group as stored ("" when unset):
// the default member's, else the first set one by name.
func VariantPolicyOf(agents []Agent, group string) string {
	if def, ok := VariantDefault(agents, group); ok && def.Variant.Policy != "" {
		return def.Variant.Policy
	}
	for _, m := range VariantMembers(agents, group) {
		if m.Variant.Policy != "" {
			return m.Variant.Policy
		}
	}
	return ""
}

// SetVariantPolicy stores policy on every member of group, in place.
// VariantPolicyDefault is stored as "".
func SetVariantPolicy(agents []Agent, group, policy string) error {
	p, err := ParseVariantPolicy(policy)
	if err != nil {
		return err
	}
	if p == VariantPolicyDefault {
		p = ""
	}
	found := false
	for i := range agents {
		if agents[i].VariantGroup() != group {
			continue
		}
		found = true
		if agents[i].Variant.Policy != p {
			v := *agents[i].Variant
			v.Policy = p
			agents[i].Variant = &v
			agents[i].UpdatedAt = time.Now()
		}
	}
	if !found {
		return fmt.Errorf("variant group %q not found", group)
	}
	return nil
}

// VariantTargetTier is the tier a policy aims for given the prompt tier:
// cost-first shifts one tier down, quality-first one tier up, and the
// other policies keep it.
func VariantTargetTier(policy, promptTier string) string {
	r := tierRank(promptTier)
	if r < 0 {
		r = tierRank(VariantTierStandard)
	}
	switch policy {
	case VariantPolicyCostFirst:
		if r > 0 {
			r--
		}
	case VariantPolicyQualityFirst:
		if r < len(VariantTiers)-1 {
			r++
		}
	}
	return VariantTiers[r]
}

// VariantCandidates returns the members of group in the order a prompt of
// promptTier tries them under policy. The order is deterministic:
//
//   - default policy (or unset or unknown): the group default only, as
//     before tiers existed.
//   - otherwise, tiered members by distance from VariantTargetTier; at the
//     same distance the lower tier first for cost-first and balanced, the
//     higher first for quality-first; within one tier the group default
//     first, then by agent name.
//   - untiered members (and members with an unknown tier) come last, the
//     default first, then by name, so they run only when no tiered member
//     can.
func VariantCandidates(agents []Agent, group, policy, promptTier string) []Agent {
	members := VariantMembers(agents, group)
	if len(members) == 0 {
		return nil
	}
	def, _ := VariantDefault(agents, group)
	if policy != VariantPolicyCostFirst && policy != VariantPolicyBalanced && policy != VariantPolicyQualityFirst {
		return []Agent{def}
	}
	target := tierRank(VariantTargetTier(policy, promptTier))
	higherFirst := policy == VariantPolicyQualityFirst
	key := func(a Agent) (tiered bool, dist, dir int) {
		r := tierRank(a.Variant.Tier)
		if r < 0 {
			return false, 0, 0
		}
		dist = r - target
		if dist < 0 {
			dist = -dist
		}
		dir = r
		if higherFirst {
			dir = -r
		}
		return true, dist, dir
	}
	sort.SliceStable(members, func(i, j int) bool {
		ti, di, ri := key(members[i])
		tj, dj, rj := key(members[j])
		if ti != tj {
			return ti
		}
		if di != dj {
			return di < dj
		}
		if ri != rj {
			return ri < rj
		}
		ai, aj := members[i].Name == def.Name, members[j].Name == def.Name
		if ai != aj {
			return ai
		}
		return members[i].Name < members[j].Name
	})
	return members
}

// VariantCredentialProblem returns why a cannot run on its Claude backend
// for lack of a credential or setting, or "" when nothing is missing.
// getenv reads the environment (os.Getenv when nil), and gatewayExists
// reports whether a stored gateway exists. a is expected to have its
// stored gateway already resolved into BaseURL and APIKey.
func VariantCredentialProblem(a Agent, getenv func(string) string, gatewayExists func(string) bool) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if a.Provider != ProviderClaude {
		return ""
	}
	meta := a.ProviderMeta
	if meta == nil {
		meta = &AgentProviderMeta{}
	}
	switch NormalizeClaudeBackend(a.Backend) {
	case ClaudeBackendAnthropic:
		if meta.AuthMode == AuthModeOAuth || meta.AuthMode == AuthModeNone {
			return ""
		}
		if strings.TrimSpace(a.APIKey) == "" && strings.TrimSpace(getenv("ANTHROPIC_API_KEY")) == "" {
			return "no anthropic API key (agent key or ANTHROPIC_API_KEY)"
		}
	case ClaudeBackendBedrock:
		if strings.TrimSpace(meta.BedrockRegion) == "" && strings.TrimSpace(meta.AWSRegion) == "" &&
			strings.TrimSpace(getenv("AWS_REGION")) == "" && strings.TrimSpace(getenv("AWS_DEFAULT_REGION")) == "" {
			return "bedrock backend has no AWS region"
		}
	case ClaudeBackendVertex:
		if err := validateVertexTarget(strings.TrimSpace(meta.VertexRegion), strings.TrimSpace(meta.VertexProject)); err != nil {
			return err.Error()
		}
	case ClaudeBackendGateway:
		if name := strings.TrimSpace(meta.GatewayName); name != "" && (gatewayExists == nil || !gatewayExists(name)) {
			return fmt.Sprintf("stored gateway %q not found", name)
		}
		if strings.TrimSpace(a.BaseURL) == "" {
			return "gateway backend has no base URL"
		}
		if err := validateGatewayBaseURL(a.BaseURL); err != nil {
			return err.Error()
		}
	}
	return ""
}
