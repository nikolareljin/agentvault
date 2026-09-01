package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// importStrategy decides what happens when a bundle item collides with an item
// already in the vault.
type importStrategy string

const (
	// strategyMerge keeps the existing vault value on every collision and only
	// adds items the vault does not have yet. This is the historical behavior.
	strategyMerge importStrategy = "merge"
	// strategyReplace lets the bundle win on every collision but keeps items that
	// exist only locally.
	strategyReplace importStrategy = "replace"
	// strategyMirror makes the vault match the bundle exactly: the bundle wins on
	// collisions and local-only items are deleted.
	strategyMirror importStrategy = "mirror"
)

// parseImportStrategy validates a --strategy value.
func parseImportStrategy(value string) (importStrategy, error) {
	switch importStrategy(strings.ToLower(strings.TrimSpace(value))) {
	case strategyMerge:
		return strategyMerge, nil
	case strategyReplace:
		return strategyReplace, nil
	case strategyMirror:
		return strategyMirror, nil
	default:
		return "", fmt.Errorf("unknown strategy %q; use merge, replace, or mirror", value)
	}
}

// incomingWins reports whether the bundle value overrides an existing one.
func (s importStrategy) incomingWins() bool {
	return s == strategyReplace || s == strategyMirror
}

// deletesLocalOnly reports whether items absent from the bundle are removed.
func (s importStrategy) deletesLocalOnly() bool {
	return s == strategyMirror
}

// importReport accumulates what an import did, so a dry run and a real run print
// the same thing.
type importReport struct {
	Added   int
	Updated int
	Skipped int
	Removed int
	Lines   []string
}

func (r *importReport) record(action string, format string, args ...any) {
	switch action {
	case "Added":
		r.Added++
	case "Updated":
		r.Updated++
	case "Skipped":
		r.Skipped++
	case "Removed":
		r.Removed++
	}
	r.Lines = append(r.Lines, fmt.Sprintf("  %s: %s", action, fmt.Sprintf(format, args...)))
}

// mergeKeyed reconciles two keyed collections under a strategy and returns the
// resulting collection. Existing order is preserved; new items are appended.
func mergeKeyed[T any](existing []T, incoming []T, keyOf func(T) string, strategy importStrategy, label string, rep *importReport) []T {
	result := append([]T(nil), existing...)
	index := make(map[string]int, len(result))
	for i, item := range result {
		index[keyOf(item)] = i
	}

	incomingKeys := make(map[string]struct{}, len(incoming))
	for _, item := range incoming {
		key := keyOf(item)
		incomingKeys[key] = struct{}{}
		idx, exists := index[key]
		if !exists {
			result = append(result, item)
			index[key] = len(result) - 1
			rep.record("Added", "%s %s", label, displayKey(key))
			continue
		}
		if strategy.incomingWins() {
			result[idx] = item
			rep.record("Updated", "%s %s", label, displayKey(key))
			continue
		}
		rep.record("Skipped", "%s %s (kept local)", label, displayKey(key))
	}

	if !strategy.deletesLocalOnly() {
		return result
	}

	kept := make([]T, 0, len(result))
	for _, item := range result {
		key := keyOf(item)
		if _, ok := incomingKeys[key]; ok {
			kept = append(kept, item)
			continue
		}
		rep.record("Removed", "%s %s (not in bundle)", label, displayKey(key))
	}
	return kept
}

// displayKey renders a composite key (NUL separated) readably.
func displayKey(key string) string {
	parts := strings.Split(key, "\x00")
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, " / ")
}

// agentOp is one pending change to the vault's agent list.
type agentOp struct {
	Kind  string // add | update | remove
	Agent agent.Agent
	Name  string
}

// profileImportPlan holds every change an import would make to one vault, so it
// can be printed for a dry run and committed only when the caller asks.
type profileImportPlan struct {
	AgentOps        []agentOp
	Shared          agent.SharedConfig
	SharedChanged   bool
	ProviderConfigs agent.ProviderConfig
	ProviderChanged bool
	Sessions        agent.SessionConfig
	SessionsChanged bool
	Capabilities    []agent.ModelCapabilityEntry
	CapsChanged     bool
	Report          importReport
}

// planProfileImport computes, but does not apply, the vault changes for one
// bundle profile under the given strategy.
func planProfileImport(v *vault.Vault, setup SetupBundle, strategy importStrategy, now time.Time) profileImportPlan {
	plan := profileImportPlan{}
	rep := &plan.Report

	plan.AgentOps = planAgentOps(v.List(), setup.Agents, strategy, now, rep)

	shared := cloneSharedConfig(v.SharedConfig())
	incomingShared := setup.SharedConfig

	if incomingShared.SystemPrompt != "" && (shared.SystemPrompt == "" || strategy.incomingWins()) {
		if shared.SystemPrompt != incomingShared.SystemPrompt {
			shared.SystemPrompt = incomingShared.SystemPrompt
			rep.Lines = append(rep.Lines, "  Applied: shared system prompt")
		}
	} else if strategy.deletesLocalOnly() && incomingShared.SystemPrompt == "" && shared.SystemPrompt != "" {
		shared.SystemPrompt = ""
		rep.record("Removed", "shared system prompt (not in bundle)")
	}

	if !incomingShared.Router.IsZero() && (shared.Router.IsZero() || strategy.incomingWins()) {
		shared.Router = incomingShared.Router
		rep.Lines = append(rep.Lines, "  Applied: shared router config")
	} else if strategy.deletesLocalOnly() && incomingShared.Router.IsZero() && !shared.Router.IsZero() {
		shared.Router = agent.RouterConfig{}
		rep.record("Removed", "shared router config (not in bundle)")
	}

	shared.MCPServers = mergeKeyed(shared.MCPServers, incomingShared.MCPServers,
		func(s agent.MCPServer) string { return s.Name }, strategy, "MCP server", rep)
	shared.Instructions = mergeKeyed(shared.Instructions, mergeInstructionSources(incomingShared.Instructions, setup.InstructionOverrides, now),
		agent.InstructionKey, strategy, "instruction", rep)
	shared.Rules = mergeKeyed(shared.Rules, incomingShared.Rules,
		func(r agent.UnifiedRule) string { return r.Name }, strategy, "rule", rep)
	shared.Roles = mergeKeyed(shared.Roles, incomingShared.Roles,
		func(r agent.Role) string { return r.Name }, strategy, "role", rep)
	sort.SliceStable(shared.Rules, func(i, j int) bool { return shared.Rules[i].Priority < shared.Rules[j].Priority })
	plan.Shared = shared
	plan.SharedChanged = true

	plan.ProviderConfigs, plan.ProviderChanged = planProviderConfigs(v.ProviderConfigs(), setup.ProviderConfigs, strategy, rep)
	plan.Sessions, plan.SessionsChanged = planSessions(v.Sessions(), setup.Sessions, strategy, now, rep)
	plan.Capabilities = mergeKeyed(v.ListCapabilities(), setup.ModelCapabilities, capabilityKey, strategy, "model capability", rep)
	plan.CapsChanged = true

	return plan
}

// planAgentOps diffs the vault's agents against the bundle's.
func planAgentOps(existing []agent.Agent, incoming []agent.Agent, strategy importStrategy, now time.Time, rep *importReport) []agentOp {
	byName := make(map[string]agent.Agent, len(existing))
	for _, a := range existing {
		byName[a.Name] = a
	}
	incomingNames := make(map[string]struct{}, len(incoming))

	var ops []agentOp
	for _, a := range incoming {
		incomingNames[a.Name] = struct{}{}
		current, exists := byName[a.Name]
		if !exists {
			a.CreatedAt = now
			a.UpdatedAt = now
			ops = append(ops, agentOp{Kind: "add", Agent: a, Name: a.Name})
			rep.record("Added", "agent %s", a.Name)
			continue
		}
		if !strategy.incomingWins() {
			rep.record("Skipped", "agent %s (kept local)", a.Name)
			continue
		}
		// An empty key in the bundle must not wipe a working local credential.
		if a.APIKey == "" {
			a.APIKey = current.APIKey
		}
		a.CreatedAt = current.CreatedAt
		a.UpdatedAt = now
		ops = append(ops, agentOp{Kind: "update", Agent: a, Name: a.Name})
		rep.record("Updated", "agent %s", a.Name)
	}

	if !strategy.deletesLocalOnly() {
		return ops
	}
	for _, a := range existing {
		if _, ok := incomingNames[a.Name]; ok {
			continue
		}
		ops = append(ops, agentOp{Kind: "remove", Name: a.Name})
		rep.record("Removed", "agent %s (not in bundle)", a.Name)
	}
	return ops
}

// planProviderConfigs reconciles the per-provider config blocks.
func planProviderConfigs(existing agent.ProviderConfig, incoming agent.ProviderConfig, strategy importStrategy, rep *importReport) (agent.ProviderConfig, bool) {
	result := existing
	changed := false

	apply := func(name string, cur any, inc any, set func()) {
		incPresent := !isNilProviderConfig(inc)
		curPresent := !isNilProviderConfig(cur)
		switch {
		case incPresent && (!curPresent || strategy.incomingWins()):
			set()
			changed = true
			rep.Lines = append(rep.Lines, fmt.Sprintf("  Applied: %s provider config", name))
		case !incPresent && curPresent && strategy.deletesLocalOnly():
			set()
			changed = true
			rep.record("Removed", "%s provider config (not in bundle)", name)
		}
	}

	apply("Claude", existing.Claude, incoming.Claude, func() { result.Claude = incoming.Claude })
	apply("Codex", existing.Codex, incoming.Codex, func() { result.Codex = incoming.Codex })
	apply("Ollama", existing.Ollama, incoming.Ollama, func() { result.Ollama = incoming.Ollama })
	return result, changed
}

// isNilProviderConfig reports whether a typed provider config pointer is nil.
func isNilProviderConfig(value any) bool {
	switch v := value.(type) {
	case *agent.ClaudeConfig:
		return v == nil
	case *agent.CodexConfig:
		return v == nil
	case *agent.OllamaConfig:
		return v == nil
	default:
		return value == nil
	}
}

// planSessions reconciles session definitions by name, stripping machine-local
// runtime state (PIDs, running status) that must not travel between machines.
func planSessions(existing agent.SessionConfig, incoming agent.SessionConfig, strategy importStrategy, now time.Time, rep *importReport) (agent.SessionConfig, bool) {
	result := existing
	usedIDs := make(map[string]struct{}, len(existing.Sessions))
	existingByName := make(map[string]agent.Session, len(existing.Sessions))
	for _, s := range existing.Sessions {
		usedIDs[s.ID] = struct{}{}
		existingByName[s.Name] = s
	}

	normalized := make([]agent.Session, 0, len(incoming.Sessions))
	for _, s := range incoming.Sessions {
		s.Status = agent.SessionStatusIdle
		s.UpdatedAt = now
		for i := range s.Agents {
			s.Agents[i].PID = 0
		}
		if prior, ok := existingByName[s.Name]; ok {
			// Keep the local ID so anything referencing this session still resolves.
			s.ID = prior.ID
		} else {
			if s.ID == "" {
				s.ID = agent.GenerateSessionID()
			}
			for {
				if _, clash := usedIDs[s.ID]; !clash {
					break
				}
				s.ID = fmt.Sprintf("%s-%d", agent.GenerateSessionID(), now.UnixNano()%1000)
			}
			usedIDs[s.ID] = struct{}{}
		}
		normalized = append(normalized, s)
	}

	result.Sessions = mergeKeyed(existing.Sessions, normalized,
		func(s agent.Session) string { return s.Name }, strategy, "session", rep)

	if incoming.ActiveSession != "" && (result.ActiveSession == "" || strategy.incomingWins()) {
		if sessionIDPresent(result.Sessions, incoming.ActiveSession) {
			result.ActiveSession = incoming.ActiveSession
		}
	}
	if !sessionIDPresent(result.Sessions, result.ActiveSession) {
		result.ActiveSession = ""
	}
	if (incoming.ParallelLimitSet || incoming.ParallelLimit > 0) && (!result.ParallelLimitSet || strategy.incomingWins()) {
		result.ParallelLimit = incoming.ParallelLimit
		result.ParallelLimitSet = true
	}
	if len(incoming.DefaultAgents) > 0 && (len(result.DefaultAgents) == 0 || strategy.incomingWins()) {
		result.DefaultAgents = append([]string(nil), incoming.DefaultAgents...)
	}
	return result, true
}

func sessionIDPresent(sessions []agent.Session, id string) bool {
	if id == "" {
		return false
	}
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

// capabilityKey builds the canonical endpoint+model key for a capability entry.
func capabilityKey(e agent.ModelCapabilityEntry) string {
	return strings.TrimRight(strings.TrimSpace(e.EndpointURL), "/") + "\x00" + strings.TrimSpace(e.ModelName)
}

// mergeInstructionSources folds instruction override assets into the bundle's
// instruction list so both carriers land in the vault as instructions.
func mergeInstructionSources(instructions []agent.InstructionFile, overrides []SetupAsset, now time.Time) []agent.InstructionFile {
	result := append([]agent.InstructionFile(nil), instructions...)
	seen := make(map[string]struct{}, len(result))
	for _, inst := range result {
		seen[agent.InstructionKey(inst)] = struct{}{}
	}
	for _, asset := range overrides {
		name := instructionNameForAsset(asset)
		if name == "" || asset.Missing || !asset.ContentPresent {
			continue
		}
		filename := asset.ProjectRelativePath
		if filename == "" {
			filename = asset.LogicalPath
		}
		filename, err := sanitizeAssetRelativePath(filename)
		if err != nil {
			continue
		}
		inst := agent.InstructionFile{
			Name:      name,
			Filename:  filename,
			Content:   string(asset.Content),
			UpdatedAt: now,
		}
		key := agent.InstructionKey(inst)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, inst)
	}
	return result
}

// cloneSharedConfig deep-copies the slices a plan mutates, so a dry run cannot
// disturb in-memory vault state.
func cloneSharedConfig(sc agent.SharedConfig) agent.SharedConfig {
	out := sc
	out.MCPServers = append([]agent.MCPServer(nil), sc.MCPServers...)
	out.Instructions = append([]agent.InstructionFile(nil), sc.Instructions...)
	out.Rules = append([]agent.UnifiedRule(nil), sc.Rules...)
	out.Roles = append([]agent.Role(nil), sc.Roles...)
	out.PromptSessions = append([]agent.PromptSession(nil), sc.PromptSessions...)
	out.Pricing = append([]agent.ProviderPricing(nil), sc.Pricing...)
	return out
}

// commitProfileImport writes a computed plan into the vault.
func commitProfileImport(v *vault.Vault, plan profileImportPlan) error {
	for _, op := range plan.AgentOps {
		switch op.Kind {
		case "add":
			if err := v.Add(op.Agent); err != nil {
				return fmt.Errorf("adding agent %s: %w", op.Name, err)
			}
		case "update":
			if err := v.Update(op.Agent); err != nil {
				return fmt.Errorf("updating agent %s: %w", op.Name, err)
			}
		case "remove":
			if err := v.Remove(op.Name); err != nil {
				return fmt.Errorf("removing agent %s: %w", op.Name, err)
			}
		}
	}
	if plan.SharedChanged {
		if err := v.SetSharedConfig(plan.Shared); err != nil {
			return fmt.Errorf("updating shared config: %w", err)
		}
	}
	if plan.ProviderChanged {
		if err := v.SetProviderConfigs(plan.ProviderConfigs); err != nil {
			return fmt.Errorf("updating provider configs: %w", err)
		}
	}
	if plan.SessionsChanged {
		if err := v.SetSessions(plan.Sessions); err != nil {
			return fmt.Errorf("updating sessions: %w", err)
		}
	}
	if plan.CapsChanged {
		if err := v.SetCapabilities(plan.Capabilities); err != nil {
			return fmt.Errorf("updating model capabilities: %w", err)
		}
	}
	return nil
}
