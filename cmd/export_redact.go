package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// Without --include-secrets the provider files are blanked, but the bundle
// also carries the vault's own copies of setup: the Claude settings that
// `setup pull` stored whole (env, apiKeyHelper, MCP headers) and the MCP
// servers of the shared config and of each agent. These are blanked here the
// same way, and import fills them back from this machine's vault.

// redactVaultSecrets blanks the secret values in the vault-derived parts of
// b. It copies what it changes, so the vault's own values are not touched.
func redactVaultSecrets(b *SetupBundle) {
	var blanks []VaultBlank
	b.SharedConfig.MCPServers = blankMCPServerSecrets(b.SharedConfig.MCPServers)
	router := b.SharedConfig.Router
	b.SharedConfig.Router = redactRouter(router)
	for _, f := range routerSecretFields(&router) {
		if *f.Value != "" && secretString(*f.Value) {
			blanks = append(blanks, VaultBlank{Section: blankRouter, Field: f.Name})
		}
	}
	agents := make([]agent.Agent, len(b.Agents))
	for i, a := range b.Agents {
		a.MCPServers = blankMCPServerSecrets(a.MCPServers)
		if secretString(a.BaseURL) {
			a.BaseURL = ""
			blanks = append(blanks, VaultBlank{Section: blankAgent, Name: a.Name, Field: "base_url"})
		}
		if a.ProviderMeta != nil && len(a.ProviderMeta.Extra) > 0 {
			meta := *a.ProviderMeta
			meta.Extra = make(map[string]string, len(a.ProviderMeta.Extra))
			for _, k := range sortedStringKeys(a.ProviderMeta.Extra) {
				val := a.ProviderMeta.Extra[k]
				if exportBlanksExtra(k, val) {
					if val != "" {
						blanks = append(blanks, VaultBlank{Section: blankAgent, Name: a.Name, Field: "provider_meta.extra." + k})
					}
					val = ""
				}
				meta.Extra[k] = val
			}
			a.ProviderMeta = &meta
		}
		agents[i] = a
	}
	b.Agents = agents
	b.ProviderConfigs.Claude = redactClaudeConfig(b.ProviderConfigs.Claude)
	if c := b.ProviderConfigs.Codex; c != nil && len(c.CustomSettings) > 0 {
		cp := *c
		cp.CustomSettings = redactSensitiveMap(c.CustomSettings)
		blanks = append(blanks, droppedSettings("Codex", c.CustomSettings, cp.CustomSettings)...)
		b.ProviderConfigs.Codex = &cp
	}
	if c := b.ProviderConfigs.Ollama; c != nil {
		cp := *c
		if secretString(cp.BaseURL) {
			cp.BaseURL = ""
			blanks = append(blanks, VaultBlank{Section: blankProvider, Name: "Ollama", Field: "base_url"})
		}
		if len(c.CustomSettings) > 0 {
			cp.CustomSettings = redactSensitiveMap(c.CustomSettings)
			blanks = append(blanks, droppedSettings("Ollama", c.CustomSettings, cp.CustomSettings)...)
		}
		b.ProviderConfigs.Ollama = &cp
	}
	b.VaultBlanks = blanks
}

// VaultBlank is one value an export blanked or dropped from the vault's
// copies: an agent's base URL or extra, a router setting, or a provider
// config's base URL or top-level custom setting.
type VaultBlank struct {
	Section string `json:"section"`
	Name    string `json:"name,omitempty"`
	Field   string `json:"field"`
}

const (
	blankAgent    = "agent"
	blankRouter   = "router"
	blankProvider = "provider"
)

// subject is how the import report names b, as its Left out lines do.
func (b VaultBlank) subject() string {
	switch b.Section {
	case blankAgent:
		return "agent " + b.Name + " " + b.Field
	case blankRouter:
		return "shared router config " + b.Field
	default:
		return b.Name + " provider config " + b.Field
	}
}

// droppedSettings names the top-level custom settings redaction changed.
func droppedSettings(provider string, orig, redacted map[string]any) []VaultBlank {
	var out []VaultBlank
	for _, k := range sortedKeys(orig) {
		if r, ok := redacted[k]; !ok || !jsonEqual(r, orig[k]) {
			out = append(out, VaultBlank{Section: blankProvider, Name: provider, Field: "customSettings." + k})
		}
	}
	return out
}

// exportBlanksExtra reports whether export blanks the agent provider extra
// k=v. Import fills a blank extra only where this says export made it.
func exportBlanksExtra(k, v string) bool {
	return isSecretKey(k) || secretContainers[strings.ToLower(k)] || secretString(v)
}

// routerSecretFields are the router settings that can carry a credential:
// a command line and two URLs.
func routerSecretFields(r *agent.RouterConfig) []struct {
	Name  string
	Value *string
} {
	return []struct {
		Name  string
		Value *string
	}{
		{"langgraph_command", &r.LangGraphCmd},
		{"llm_router_url", &r.LLMRouterURL},
		{"local_ai_ollama_url", &r.LocalAIOllamaURL},
	}
}

// redactRouter blanks each router setting that carries a secret.
func redactRouter(r agent.RouterConfig) agent.RouterConfig {
	for _, f := range routerSecretFields(&r) {
		if secretString(*f.Value) {
			*f.Value = ""
		}
	}
	return r
}

// restoreRouterSecrets returns this machine's router when incoming is
// exactly what export makes of it. Otherwise incoming stays as it is and
// left names each blank this machine had a secret for.
func restoreRouterSecrets(incoming, existing agent.RouterConfig) (agent.RouterConfig, []string) {
	if redactRouter(existing) == incoming {
		return existing, nil
	}
	var left []string
	in := incoming
	have := routerSecretFields(&existing)
	for i, f := range routerSecretFields(&in) {
		if *f.Value == "" && secretString(*have[i].Value) {
			left = append(left, f.Name)
		}
	}
	return incoming, left
}

// blankMCPServerSecrets returns copies of servers with every env value, every
// secret-shaped arg and the value after a secret flag ("--token", "x") blanked.
// Blanking keeps the positions, so import can fill each one back.
func blankMCPServerSecrets(servers []agent.MCPServer) []agent.MCPServer {
	if servers == nil {
		return nil
	}
	out := make([]agent.MCPServer, len(servers))
	for i, s := range servers {
		if secretString(s.Command) {
			s.Command = ""
		}
		s.Args = blankSecretArgs(s.Args)
		if s.Env != nil {
			env := make(map[string]string, len(s.Env))
			for k := range s.Env {
				env[k] = ""
			}
			s.Env = env
		}
		out[i] = s
	}
	return out
}

func blankSecretArgs(args []string) []string {
	if args == nil {
		return nil
	}
	out := append([]string(nil), args...)
	for i, a := range args {
		if secretFlag.MatchString(a) {
			if strings.Contains(a, "=") {
				out[i] = ""
			} else if i+1 < len(out) {
				out[i+1] = ""
			}
			continue
		}
		if secretString(a) {
			out[i] = ""
		}
	}
	return out
}

// redactClaudeConfig blanks the secrets of the settings.json c writes and
// records where they were. A config whose secrets cannot be placed (a token
// under a neutral key) travels without its settings, as the file would.
func redactClaudeConfig(c *agent.ClaudeConfig) *agent.ClaudeConfig {
	if c == nil {
		return nil
	}
	out := &agent.ClaudeConfig{
		EnabledPlugins: map[string]bool{},
		Keybindings:    c.Keybindings,
		CustomSettings: map[string]any{},
	}
	data, err := agent.ClaudeSettingsJSON(c)
	if err != nil {
		out.SettingsRedacted = true
		return out
	}
	blanked, pointers, ok := redactJSONSecrets(data)
	var settings map[string]any
	if !ok || json.Unmarshal(blanked, &settings) != nil {
		out.SettingsRedacted = true
		return out
	}
	agent.ApplyClaudeSettings(out, settings)
	out.RedactedFields = pointers
	return out
}

// restoreClaudeConfig fills the secrets an export blanked in incoming from
// existing, this machine's vault copy, under the rules provider files use.
// A config exported without its settings keeps this machine's settings.
// left names each blank that was not filled.
func restoreClaudeConfig(incoming, existing *agent.ClaudeConfig) (*agent.ClaudeConfig, []string) {
	if incoming == nil || (!incoming.SettingsRedacted && len(incoming.RedactedFields) == 0) {
		return incoming, nil
	}
	out := &agent.ClaudeConfig{
		EnabledPlugins: map[string]bool{},
		Keybindings:    incoming.Keybindings,
		CustomSettings: map[string]any{},
	}
	if incoming.SettingsRedacted {
		if existing != nil {
			cp := *existing
			cp.Keybindings = incoming.Keybindings
			return &cp, nil
		}
		return out, []string{"settings"}
	}
	in, err := agent.ClaudeSettingsJSON(incoming)
	if err != nil {
		return out, []string{"settings"}
	}
	var have []byte
	if existing != nil {
		have, _ = agent.ClaudeSettingsJSON(existing)
	}
	res, err := restoreRedactedFieldsWith(in, incoming.RedactedFields, have, nil)
	var settings map[string]any
	if err != nil || json.Unmarshal(res.Content, &settings) != nil {
		return out, []string{"settings"}
	}
	agent.ApplyClaudeSettings(out, settings)
	left := append([]string(nil), res.Removed...)
	sort.Strings(left)
	return out, left
}

// restoreMCPServerSecrets fills the values an export blanked in incoming from
// the server of the same name in existing. Only an unchanged definition gets
// them: same command, every arg the bundle kept equal and the same env
// names. An env value with nothing to fill it is removed, so a tool never
// reads "" as its token. left names each blank that was not filled.
func restoreMCPServerSecrets(incoming, existing []agent.MCPServer) ([]agent.MCPServer, []mcpBlankLeft) {
	if incoming == nil {
		return nil, nil
	}
	var left []mcpBlankLeft
	byName := make(map[string]agent.MCPServer, len(existing))
	for _, s := range existing {
		byName[s.Name] = s
	}
	out := make([]agent.MCPServer, 0, len(incoming))
	for _, s := range incoming {
		e, ok := byName[s.Name]
		same := ok && sameMCPApartFromBlanks(s, e)
		if same {
			if s.Command == "" {
				s.Command = e.Command
			}
			if len(s.Args) > 0 {
				args := append([]string(nil), s.Args...)
				for j := range args {
					if args[j] == "" {
						args[j] = e.Args[j]
					}
				}
				s.Args = args
			}
		} else {
			// A blank command was lost to redaction and cannot run: a
			// server new here is left out, a changed one stays as it is
			// here rather than be written with an empty command.
			if s.Command == "" && (!ok || e.Command != "") {
				if ok {
					left = append(left, mcpBlankLeft{s.Name, "command (this machine's server kept)"})
					out = append(out, e)
				} else {
					left = append(left, mcpBlankLeft{s.Name, "command (server not added)"})
				}
				continue
			}
			for j, a := range s.Args {
				if a == "" {
					left = append(left, mcpBlankLeft{s.Name, fmt.Sprintf("args[%d]", j)})
				}
			}
		}
		if s.Env != nil {
			env := make(map[string]string, len(s.Env))
			for _, k := range sortedStringKeys(s.Env) {
				val := s.Env[k]
				if val == "" {
					local, has := e.Env[k]
					if !same || !has {
						left = append(left, mcpBlankLeft{s.Name, "env." + k})
						continue
					}
					val = local
				}
				env[k] = val
			}
			s.Env = env
		}
		out = append(out, s)
	}
	return out, left
}

// mcpBlankLeft is a blank of an MCP server that import could not fill.
type mcpBlankLeft struct{ Server, Field string }

func sameMCPApartFromBlanks(in, have agent.MCPServer) bool {
	if in.Command != "" && in.Command != have.Command {
		return false
	}
	if len(in.Args) != len(have.Args) || len(in.Env) != len(have.Env) {
		return false
	}
	for i, a := range in.Args {
		if a != "" && a != have.Args[i] {
			return false
		}
	}
	// Export blanks every env value but keeps the names, so a missing
	// name (API_URL dropped to reach a default endpoint) is a change.
	for k, val := range in.Env {
		hv, ok := have.Env[k]
		if !ok || (val != "" && hv != val) {
			return false
		}
	}
	return true
}

// restoreAgentSecrets fills an agent's blanked base URL and provider extras
// from the local agent of the same name, only when the bundle's agent points
// at the same place: same provider, backend and provider settings, and every
// value it kept equal to the local one. Otherwise a blank is dropped and
// named in left, so a bundle cannot send this machine's key elsewhere.
func restoreAgentSecrets(in, have agent.Agent, exists bool) (agent.Agent, []string) {
	var left []string
	same := exists && sameAgentEndpoint(in, have)
	if in.BaseURL == "" && secretString(have.BaseURL) {
		if same {
			in.BaseURL = have.BaseURL
		} else if exists {
			left = append(left, "base_url")
		}
	}
	var haveExtra map[string]string
	if exists && have.ProviderMeta != nil {
		haveExtra = have.ProviderMeta.Extra
	}
	if in.ProviderMeta != nil && len(in.ProviderMeta.Extra) > 0 {
		meta := *in.ProviderMeta
		meta.Extra = make(map[string]string, len(in.ProviderMeta.Extra))
		for _, k := range sortedStringKeys(in.ProviderMeta.Extra) {
			val := in.ProviderMeta.Extra[k]
			if val == "" && exportBlanksExtra(k, haveExtra[k]) {
				if !same || haveExtra[k] == "" {
					left = append(left, "provider_meta.extra."+k)
					continue
				}
				val = have.ProviderMeta.Extra[k]
			}
			meta.Extra[k] = val
		}
		in.ProviderMeta = &meta
	}
	servers, mcpLeft := restoreMCPServerSecrets(in.MCPServers, have.MCPServers)
	in.MCPServers = servers
	for _, l := range mcpLeft {
		left = append(left, "MCP server "+l.Server+" "+l.Field)
	}
	return in, left
}

// sameAgentEndpoint compares what decides where an agent's key is sent,
// with the bundle's blanks ignored.
func sameAgentEndpoint(in, have agent.Agent) bool {
	if in.Provider != have.Provider || in.Backend != have.Backend {
		return false
	}
	// A blank matches only a value export blanks: a blank plain base URL
	// or extra would send the key to the provider's default endpoint.
	if in.BaseURL != have.BaseURL && (in.BaseURL != "" || !secretString(have.BaseURL)) {
		return false
	}
	var im, hm agent.AgentProviderMeta
	if in.ProviderMeta != nil {
		im = *in.ProviderMeta
	}
	if have.ProviderMeta != nil {
		hm = *have.ProviderMeta
	}
	if len(im.Extra) != len(hm.Extra) {
		return false
	}
	for k, v := range im.Extra {
		hv, ok := hm.Extra[k]
		if !ok || (v != hv && (v != "" || !exportBlanksExtra(k, hv))) {
			return false
		}
	}
	im.Extra, hm.Extra = nil, nil
	return reflect.DeepEqual(im, hm)
}

// restoreCustomSettings returns this machine's settings when incoming is
// exactly what export makes of them and allowed is true, so the secret keys
// export dropped stay. Settings that differ in any other way are taken as
// the bundle has them, and left names the top-level keys of existing whose
// secrets that loses: those where the bundle holds no secret of its own.
func restoreCustomSettings(incoming, existing map[string]any, allowed bool) (map[string]any, []string) {
	if len(existing) == 0 {
		return incoming, nil
	}
	redacted := redactSensitiveMap(existing)
	want, err1 := json.Marshal(redacted)
	got, err2 := json.Marshal(incoming)
	if len(incoming) == 0 {
		got = []byte("{}")
	}
	if allowed && err1 == nil && err2 == nil && string(want) == string(got) {
		return existing, nil
	}
	var left []string
	inRedacted := redactSensitiveMap(incoming)
	for _, k := range sortedKeys(existing) {
		if r, ok := redacted[k]; ok && jsonEqual(r, existing[k]) {
			continue // no secret here on this machine
		}
		if iv, ok := incoming[k]; ok && !jsonEqual(inRedacted[k], iv) {
			continue // the bundle carries its own secret
		}
		left = append(left, "customSettings."+k)
	}
	return incoming, left
}

// blankGatewaySecrets blanks a gateway's tokens and basic auth password, as
// an export without --include-keys blanks agent API keys. The user stays.
func blankGatewaySecrets(g *agent.Gateway) {
	g.Token = ""
	g.PreviousToken = ""
	g.BasicAuthPassword = ""
}

// restoreGatewaySecrets fills the blanked secrets of each bundle gateway
// from the local gateway of the same name, only when both point at the same
// URL (and, for the password, the same user). A bundle cannot send this
// machine's token to another host. A blank that is not filled and would
// replace a local secret is named in a Left out line.
func restoreGatewaySecrets(existing, incoming []agent.Gateway, strategy importStrategy, rep *importReport) []agent.Gateway {
	byName := make(map[string]agent.Gateway, len(existing))
	for _, g := range existing {
		byName[g.Name] = g
	}
	out := make([]agent.Gateway, 0, len(incoming))
	for _, g := range incoming {
		// A bad bundle entry changes nothing here, not even under mirror.
		if err := g.ValidateTarget(); err != nil {
			rep.record("Skipped", "gateway %s: %v", g.Name, err)
			if have, ok := byName[g.Name]; ok {
				out = append(out, have)
			}
			continue
		}
		have, exists := byName[g.Name]
		if exists {
			sameURL := g.URL == have.URL
			for _, f := range []struct {
				name     string
				in, keep *string
				ok       bool
			}{
				{"token", &g.Token, &have.Token, sameURL},
				{"previous_token", &g.PreviousToken, &have.PreviousToken, sameURL},
				{"basic_auth_password", &g.BasicAuthPassword, &have.BasicAuthPassword, sameURL && g.BasicAuthUser == have.BasicAuthUser},
			} {
				if *f.in != "" || *f.keep == "" {
					continue
				}
				if f.ok {
					*f.in = *f.keep
				} else if strategy.incomingWins() {
					rep.record("Left out", "gateway %s %s: blank in the bundle, and the bundle's gateway differs from this machine's", g.Name, f.name)
				}
			}
			// Timestamps are this machine's when nothing else changed, so a
			// re-import of the same bundle is a no-op.
			if g.Token == have.Token && g.PreviousToken == have.PreviousToken && g.URL == have.URL &&
				g.BasicAuthUser == have.BasicAuthUser && g.BasicAuthPassword == have.BasicAuthPassword {
				g.CreatedAt, g.UpdatedAt, g.RotatedAt = have.CreatedAt, have.UpdatedAt, have.RotatedAt
			}
		}
		out = append(out, g)
	}
	return out
}
