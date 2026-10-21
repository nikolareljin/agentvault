package cmd

import (
	"encoding/json"
	"reflect"
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
	b.SharedConfig.MCPServers = blankMCPServerSecrets(b.SharedConfig.MCPServers)
	agents := make([]agent.Agent, len(b.Agents))
	for i, a := range b.Agents {
		a.MCPServers = blankMCPServerSecrets(a.MCPServers)
		if secretString(a.BaseURL) {
			a.BaseURL = ""
		}
		if a.ProviderMeta != nil && len(a.ProviderMeta.Extra) > 0 {
			meta := *a.ProviderMeta
			meta.Extra = make(map[string]string, len(a.ProviderMeta.Extra))
			for k, val := range a.ProviderMeta.Extra {
				if isSecretKey(k) || secretString(val) {
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
		b.ProviderConfigs.Codex = &cp
	}
	if c := b.ProviderConfigs.Ollama; c != nil {
		cp := *c
		if secretString(cp.BaseURL) {
			cp.BaseURL = ""
		}
		if len(c.CustomSettings) > 0 {
			cp.CustomSettings = redactSensitiveMap(c.CustomSettings)
		}
		b.ProviderConfigs.Ollama = &cp
	}
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
func restoreClaudeConfig(incoming, existing *agent.ClaudeConfig) *agent.ClaudeConfig {
	if incoming == nil || (!incoming.SettingsRedacted && len(incoming.RedactedFields) == 0) {
		return incoming
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
			return &cp
		}
		return out
	}
	in, err := agent.ClaudeSettingsJSON(incoming)
	if err != nil {
		return out
	}
	var have []byte
	if existing != nil {
		have, _ = agent.ClaudeSettingsJSON(existing)
	}
	restored, _, err := restoreRedactedFields(in, incoming.RedactedFields, have)
	var settings map[string]any
	if err != nil || json.Unmarshal(restored, &settings) != nil {
		return out
	}
	agent.ApplyClaudeSettings(out, settings)
	return out
}

// restoreMCPServerSecrets fills the values an export blanked in incoming from
// the server of the same name in existing. Only an unchanged definition gets
// them: same command and every arg the bundle kept equal. An env value with
// nothing to fill it is removed, so a tool never reads "" as its token.
func restoreMCPServerSecrets(incoming, existing []agent.MCPServer) []agent.MCPServer {
	if incoming == nil {
		return nil
	}
	byName := make(map[string]agent.MCPServer, len(existing))
	for _, s := range existing {
		byName[s.Name] = s
	}
	out := make([]agent.MCPServer, len(incoming))
	for i, s := range incoming {
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
		}
		if s.Env != nil {
			env := make(map[string]string, len(s.Env))
			for k, val := range s.Env {
				if val == "" {
					local, has := e.Env[k]
					if !same || !has {
						continue
					}
					val = local
				}
				env[k] = val
			}
			s.Env = env
		}
		out[i] = s
	}
	return out
}

func sameMCPApartFromBlanks(in, have agent.MCPServer) bool {
	if in.Command != "" && in.Command != have.Command {
		return false
	}
	if len(in.Args) != len(have.Args) {
		return false
	}
	for i, a := range in.Args {
		if a != "" && a != have.Args[i] {
			return false
		}
	}
	for k, val := range in.Env {
		if val != "" && have.Env[k] != val {
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
	if in.ProviderMeta != nil && len(in.ProviderMeta.Extra) > 0 {
		meta := *in.ProviderMeta
		meta.Extra = make(map[string]string, len(in.ProviderMeta.Extra))
		for _, k := range sortedStringKeys(in.ProviderMeta.Extra) {
			val := in.ProviderMeta.Extra[k]
			if val == "" && (isSecretKey(k) || (exists && have.ProviderMeta != nil && secretString(have.ProviderMeta.Extra[k]))) {
				if !same || have.ProviderMeta == nil || have.ProviderMeta.Extra[k] == "" {
					left = append(left, "provider_meta.extra."+k)
					continue
				}
				val = have.ProviderMeta.Extra[k]
			}
			meta.Extra[k] = val
		}
		in.ProviderMeta = &meta
	}
	in.MCPServers = restoreMCPServerSecrets(in.MCPServers, have.MCPServers)
	return in, left
}

// sameAgentEndpoint compares what decides where an agent's key is sent,
// with the bundle's blanks ignored.
func sameAgentEndpoint(in, have agent.Agent) bool {
	if in.Provider != have.Provider || in.Backend != have.Backend {
		return false
	}
	if in.BaseURL != "" && in.BaseURL != have.BaseURL {
		return false
	}
	var im, hm agent.AgentProviderMeta
	if in.ProviderMeta != nil {
		im = *in.ProviderMeta
	}
	if have.ProviderMeta != nil {
		hm = *have.ProviderMeta
	}
	for k, v := range im.Extra {
		if v != "" && hm.Extra[k] != v {
			return false
		}
	}
	im.Extra, hm.Extra = nil, nil
	return reflect.DeepEqual(im, hm)
}
