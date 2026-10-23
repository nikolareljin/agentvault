package agent

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/textutil"
)

// AgentVariant puts an agent in a named group of interchangeable agents,
// for example one logical Claude setup run through several backends. Each
// group has exactly one default member.
type AgentVariant struct {
	Group   string `json:"group"             yaml:"group"`
	Default bool   `json:"default,omitempty" yaml:"default,omitempty"`
}

// Validate checks the group name. A nil variant is valid.
func (v *AgentVariant) Validate() error {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(v.Group) == "" {
		return errors.New("variant group name is required")
	}
	if strings.ContainsAny(v.Group, " \t\r\n") || textutil.Printable(v.Group) != v.Group {
		return fmt.Errorf("variant group %q has whitespace or control characters", v.Group)
	}
	return nil
}

// VariantGroup returns the agent's group, or "".
func (a Agent) VariantGroup() string {
	if a.Variant == nil {
		return ""
	}
	return a.Variant.Group
}

// IsVariantDefault reports whether the agent is marked default in its group.
func (a Agent) IsVariantDefault() bool {
	return a.Variant != nil && a.Variant.Default
}

// VariantGroups returns the sorted group names used by agents.
func VariantGroups(agents []Agent) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range agents {
		if g := a.VariantGroup(); g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// VariantMembers returns the agents of group sorted by name.
func VariantMembers(agents []Agent, group string) []Agent {
	var out []Agent
	for _, a := range agents {
		if a.VariantGroup() == group {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// VariantDefault returns the default agent of group: the marked one, or the
// first member by name when none is marked.
func VariantDefault(agents []Agent, group string) (Agent, bool) {
	members := VariantMembers(agents, group)
	if len(members) == 0 {
		return Agent{}, false
	}
	for _, m := range members {
		if m.IsVariantDefault() {
			return m, true
		}
	}
	return members[0], true
}

// NormalizeVariantDefaults makes each group have exactly one default, in
// place. When preferred is a default member of its group, the other members
// are cleared. Otherwise a group with several defaults keeps the first by
// name, and a group with none promotes its first member by name. It returns
// the names of the agents it changed and one note per fixed group.
func NormalizeVariantDefaults(agents []Agent, preferred string) (changed []string, notes []string) {
	for _, group := range VariantGroups(agents) {
		var idx []int
		for i := range agents {
			if agents[i].VariantGroup() == group {
				idx = append(idx, i)
			}
		}
		sort.Slice(idx, func(x, y int) bool { return agents[idx[x]].Name < agents[idx[y]].Name })
		keep := -1
		count := 0
		for _, i := range idx {
			if agents[i].IsVariantDefault() {
				count++
				if keep == -1 {
					keep = i
				}
				if agents[i].Name == preferred {
					keep = i
				}
			}
		}
		if count == 0 {
			keep = idx[0]
			notes = append(notes, fmt.Sprintf("variant group %s had no default; using %s", group, agents[keep].Name))
		} else if count > 1 && agents[keep].Name != preferred {
			notes = append(notes, fmt.Sprintf("variant group %s had %d defaults; keeping %s", group, count, agents[keep].Name))
		}
		for _, i := range idx {
			want := i == keep
			if agents[i].Variant.Default != want {
				v := *agents[i].Variant
				v.Default = want
				agents[i].Variant = &v
				changed = append(changed, agents[i].Name)
			}
		}
	}
	return changed, notes
}

// SetVariantDefault marks name the default of group and clears the others.
func SetVariantDefault(agents []Agent, group, name string) error {
	found := false
	for i := range agents {
		if agents[i].Name == name && agents[i].VariantGroup() == group {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("agent %q is not in variant group %q", name, group)
	}
	for i := range agents {
		if agents[i].VariantGroup() != group {
			continue
		}
		v := *agents[i].Variant
		if v.Default != (agents[i].Name == name) {
			v.Default = !v.Default
			agents[i].UpdatedAt = time.Now()
		}
		agents[i].Variant = &v
	}
	return nil
}

// ClaudeBackendDiagnostic returns a note for a Claude backend this binary
// does not know (written by a newer one, or by hand), which would otherwise
// run as anthropic. It returns "" for a known or empty backend.
func ClaudeBackendDiagnostic(raw string) string {
	if _, err := ParseClaudeBackend(raw); err != nil {
		return fmt.Sprintf("unknown claude backend %q (known: %s); it runs as %s", raw, strings.Join(ClaudeBackends, ", "), ClaudeBackendAnthropic)
	}
	return ""
}

// validateClaudeBackendFields checks the settings a Claude backend needs.
func validateClaudeBackendFields(a *Agent) error {
	meta := a.ProviderMeta
	if meta == nil {
		meta = &AgentProviderMeta{}
	}
	switch a.Backend {
	case ClaudeBackendVertex:
		if err := validateVertexTarget(strings.TrimSpace(meta.VertexRegion), strings.TrimSpace(meta.VertexProject)); err != nil {
			return err
		}
	case ClaudeBackendGateway:
		if strings.TrimSpace(a.BaseURL) == "" && strings.TrimSpace(meta.GatewayName) == "" {
			return errors.New("claude gateway backend needs --base-url or --gateway NAME")
		}
		// Both would send the stored gateway's token to another URL.
		if strings.TrimSpace(a.BaseURL) != "" && strings.TrimSpace(meta.GatewayName) != "" {
			return errors.New("claude gateway backend takes --base-url or --gateway NAME, not both")
		}
		if strings.TrimSpace(a.BaseURL) != "" {
			if err := validateGatewayBaseURL(a.BaseURL); err != nil {
				return err
			}
		}
	}
	return nil
}

// Vertex builds its endpoint host from the region, so both values are held
// to Google's syntax: a crafted region must not name another host.
var (
	vertexRegionRE  = regexp.MustCompile(`^[a-z]+(-[a-z0-9]+)*$`)
	vertexProjectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
)

func validateVertexTarget(region, project string) error {
	if region == "" || project == "" {
		return errors.New("claude vertex backend needs a region and a project (--vertex-region, --vertex-project)")
	}
	if !vertexRegionRE.MatchString(region) {
		return fmt.Errorf("claude vertex region %q is not a region name such as us-east5", region)
	}
	if !vertexProjectRE.MatchString(project) {
		return fmt.Errorf("claude vertex project %q is not a Google Cloud project ID", project)
	}
	return nil
}

// SameGatewayURL reports whether two gateway URLs are the same endpoint,
// ignoring surrounding space and a trailing slash.
func SameGatewayURL(a, b string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	return norm(a) != "" && norm(a) == norm(b)
}

func validateGatewayBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("claude gateway base URL must be http:// or https:// with a host")
	}
	if u.User != nil {
		return errors.New("claude gateway base URL must not carry credentials: use --api-key or a stored gateway")
	}
	return nil
}
