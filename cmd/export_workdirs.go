package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/locations"
)

// workDirScope is which declared work dirs an export takes. What belongs to
// a work dir it leaves out (memory, project-scope plugins, recorded
// projects, trusted projects, sessions, directory instructions) stays on
// this machine: an employer's setup never reaches a personal bundle.
type workDirScope struct {
	selected, unselected []locations.Named
	// known is the vault's projects, for instructions that name their
	// project by identity rather than path.
	known locations.Set
	// left counts what was left out per unselected name, by kind.
	left map[string]map[string]int
	// needles are the spellings of each unselected work dir's path.
	needles map[string][]string
}

// resolveWorkDirScope reads --workdirs: "all", or names declared with
// `agentvault workdir add`. An empty spec is "all".
func resolveWorkDirScope(known locations.Set, spec string) (workDirScope, error) {
	s := workDirScope{known: known, left: map[string]map[string]int{}}
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == locations.AllWorkDirs {
		s.selected = known.Named
		return s, nil
	}
	if len(known.Named) == 0 {
		return s, fmt.Errorf("--workdirs %s: no work dirs are declared; see agentvault workdir add", printable(spec))
	}
	want := map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if _, ok := known.Lookup(name); !ok {
			return s, fmt.Errorf("--workdirs: no work dir named %s (declared: %s)", printable(name), declaredNames(known))
		}
		want[name] = true
	}
	if len(want) == 0 {
		return s, fmt.Errorf("--workdirs %q names no work dir; use all, or names (declared: %s)", spec, declaredNames(known))
	}
	for _, n := range known.Named {
		if want[n.Name] {
			s.selected = append(s.selected, n)
		} else {
			s.unselected = append(s.unselected, n)
		}
	}
	return s, nil
}

func declaredNames(known locations.Set) string {
	var names []string
	for _, n := range known.Named {
		names = append(names, n.Name)
	}
	return strings.Join(names, ", ")
}

// excluded returns the unselected work dir that holds p.
func (s workDirScope) excluded(p string) (string, bool) {
	// Windows extended-length form, as Codex writes project keys.
	p = strings.TrimPrefix(p, `\\?\`)
	for _, n := range s.unselected {
		if n.Holds(p) {
			return n.Name, true
		}
	}
	return "", false
}

func (s workDirScope) leave(name, kind string) {
	if s.left[name] == nil {
		s.left[name] = map[string]int{}
	}
	s.left[name][kind]++
}

// checkPaths refuses an export whose --project or --workdir lies in a work
// dir it leaves out: the two flags ask for opposite things.
func (s workDirScope) checkPaths(projectDir string, workDirs []string) error {
	if projectDir != "" {
		if name, ok := s.excluded(absOrEmpty(projectDir)); ok {
			return fmt.Errorf("--project %s is in work dir %s, which --workdirs leaves out", printable(projectDir), printable(name))
		}
	}
	for _, w := range workDirs {
		if name, ok := s.excluded(absOrEmpty(w)); ok {
			return fmt.Errorf("--workdir %s is in work dir %s, which --workdirs leaves out", printable(w), printable(name))
		}
	}
	return nil
}

// apply removes from bundle what belongs to an unselected work dir and
// records one Declined entry per work dir with counts. The entry names no
// path: a memory folder's name is the project's path, and the bundle is
// what must not carry it. It returns which agents stay (parallel to
// bundle.Agents before the call), and an error when something still names
// an unselected work dir after every filter: the export then stops rather
// than ship it.
func (s *workDirScope) apply(bundle *SetupBundle, home string) ([]bool, error) {
	keepAgents := make([]bool, len(bundle.Agents))
	for i := range keepAgents {
		keepAgents[i] = true
	}
	if len(s.unselected) == 0 {
		return keepAgents, nil
	}
	if s.left == nil {
		s.left = map[string]map[string]int{}
	}
	s.needles = map[string][]string{}
	for _, n := range s.unselected {
		s.needles[n.Name] = workDirNeedles(n, home)
	}
	s.applyMemory(bundle, home)
	s.applyPlugins(bundle)
	s.applyLocations(bundle)
	s.applyCodex(bundle)
	s.applySessions(bundle)
	s.applyInstructions(bundle)
	s.applyDetected(bundle)
	s.applyDeclined(bundle)
	// Then anything else that names the work dir: a hook script path,
	// additionalDirectories, an MCP argument, a note in CLAUDE.md.
	s.applyMentions(bundle)
	keepAgents = s.applyStructured(bundle)
	pruneSessionRefs(&bundle.Sessions, bundle.Agents, keepAgents)
	pruneAgentBlanks(bundle)
	bundle.VaultBlanks = dropMentioning(*s, bundle.VaultBlanks, "blanked values")
	for _, n := range s.unselected {
		kinds := s.left[n.Name]
		if len(kinds) == 0 {
			continue
		}
		var parts []string
		for k, c := range kinds {
			parts = append(parts, fmt.Sprintf("%s: %d", k, c))
		}
		sort.Strings(parts)
		bundle.Declined = append(bundle.Declined, DeclinedAsset{
			Path:     "work dir " + n.Name,
			Category: DeclinedByPolicy,
			Reason:   "not selected with --workdirs; left out " + strings.Join(parts, ", "),
		})
	}
	return keepAgents, s.verify(bundle)
}

// --- memory --------------------------------------------------------------

var memoryAssetPath = regexp.MustCompile(`^projects/([^/]+)/memory(/|$)`)

func (s workDirScope) applyMemory(bundle *SetupBundle, home string) {
	keep := bundle.ProviderFiles[:0]
	verdict := map[string]string{} // memory folder -> unselected name, "" to keep
	for _, a := range bundle.ProviderFiles {
		m := memoryAssetPath.FindStringSubmatch(a.LogicalPath)
		if a.LogicalRoot != setupAssetRootProviderClaude || m == nil {
			keep = append(keep, a)
			continue
		}
		folder := m[1]
		name, seen := verdict[folder]
		if !seen {
			name = s.memoryOwner(folder)
			verdict[folder] = name
			if name != "" {
				s.leave(name, "memory folders")
			}
		}
		if name == "" {
			keep = append(keep, a)
		}
	}
	bundle.ProviderFiles = keep
}

// memoryOwner returns the unselected work dir a Claude memory folder belongs
// to, or "" to keep it. Claude names the folder claudeProjectSlug(cwd),
// which is lossy, and the slug of ~/Projects is a prefix of the slug of
// ~/Projects-acme. So a folder is kept when a directory under a selected
// work dir has exactly its name, and otherwise left out when its name starts
// with an unselected work dir's slug: that also covers a deleted checkout,
// and excluding too much costs a line in the report where including too
// much leaks an employer's memory.
func (s workDirScope) memoryOwner(folder string) string {
	for _, n := range s.selected {
		for _, root := range n.Roots() {
			if slugUnder(root, folder) {
				return ""
			}
		}
	}
	for _, n := range s.unselected {
		for _, root := range n.Roots() {
			slug := claudeProjectSlug(root)
			// No existing directory can clear a prefix match: the slug of an
			// undeclared ~/work/acme-tools is also the slug of a deleted
			// ~/work/acme/tools. Only proof under a selected work dir (above)
			// keeps such a folder.
			if slugEqual(folder, slug) || slugHasPrefix(folder, slug+"-") {
				return n.Name
			}
		}
	}
	return ""
}

// slugUnder reports whether root or a directory below it has the slug
// target.
func slugUnder(root, target string) bool {
	return len(slugMatches(root, target, nil)) > 0
}

// slugMatches appends to out every directory, root or below it, whose slug
// is target. It descends only into entries whose slug still leads to
// target, and not through symlinks.
func slugMatches(root, target string, out []string) []string {
	slug := claudeProjectSlug(root)
	if slugEqual(slug, target) {
		return append(out, root)
	}
	if !slugHasPrefix(target, slug+"-") {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out = slugMatches(filepath.Join(root, e.Name()), target, out)
		}
	}
	return out
}

// caseInsensitiveOS is whether paths compare without case; tests set it.
var caseInsensitiveOS = func() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// Paths on Windows and macOS (by default) compare without case, so their
// slugs do too.
func slugEqual(a, b string) bool {
	if caseInsensitiveOS() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func slugHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && slugEqual(s[:len(prefix)], prefix)
}

// --- plugins -------------------------------------------------------------

// applyPlugins leaves out a plugin installed only for projects in unselected
// work dirs. A plugin installed for the user, or for any other project,
// stays.
func (s workDirScope) applyPlugins(bundle *SetupBundle) {
	if bundle.Plugins == nil {
		return
	}
	keep := bundle.Plugins.Plugins[:0]
	for _, p := range bundle.Plugins.Plugins {
		if name, ok := s.pluginOwner(p); ok {
			s.leave(name, "plugins")
			continue
		}
		keep = append(keep, p)
	}
	bundle.Plugins.Plugins = keep
}

func (s workDirScope) pluginOwner(p InstalledPlugin) (string, bool) {
	if len(p.installs) == 0 {
		return "", false
	}
	owner := ""
	for _, in := range p.installs {
		if in.projectPath == "" {
			return "", false
		}
		name, ok := s.excluded(in.projectPath)
		if !ok {
			return "", false
		}
		owner = name
	}
	return owner, true
}

// --- recorded projects ---------------------------------------------------

func (s workDirScope) applyLocations(bundle *SetupBundle) {
	set := bundle.Locations
	if set == nil {
		return
	}
	var projects []locations.Project
	for _, p := range set.Projects {
		if name, ok := s.excluded(p.Abs); ok {
			s.leave(name, "projects")
			continue
		}
		projects = append(projects, p)
	}
	set.Projects = projects
	used := map[string]bool{}
	for _, p := range set.Projects {
		used[p.WorkDir] = true
	}
	var workDirs []locations.WorkDir
	for _, w := range set.WorkDirs {
		if _, ok := s.excluded(w.Abs); ok && !used[w.ID] {
			continue
		}
		workDirs = append(workDirs, w)
	}
	set.WorkDirs = workDirs
	if set.Empty() {
		bundle.Locations = nil
	}
}

// --- Codex ---------------------------------------------------------------

func (s workDirScope) applyCodex(bundle *SetupBundle) {
	if c := bundle.ProviderConfigs.Codex; c != nil && len(c.TrustedProjects) > 0 {
		trusted := make(map[string]string, len(c.TrustedProjects))
		for p, level := range c.TrustedProjects {
			if name, ok := s.excluded(p); ok {
				s.leave(name, "trusted projects")
				continue
			}
			trusted[p] = level
		}
		cc := *c
		cc.TrustedProjects = trusted
		bundle.ProviderConfigs.Codex = &cc
	}
	for i, a := range bundle.ProviderFiles {
		if a.LogicalRoot != setupAssetRootProviderCodex || a.LogicalPath != "config.toml" || !a.ContentPresent {
			continue
		}
		out, dropped, blocked := s.withoutCodexProjects(a.Content)
		if blocked != "" {
			// A project this cannot remove line by line: the file stays
			// here rather than carry it.
			bundle.ProviderFiles[i].Content, bundle.ProviderFiles[i].ContentPresent = nil, false
			bundle.ProviderFiles[i].SHA256, bundle.ProviderFiles[i].SizeBytes = "", 0
			bundle.ProviderFiles[i].Redacted = true
			s.leave(blocked, "Codex config.toml (a project not in its own table)")
			continue
		}
		if dropped == nil {
			continue
		}
		for _, name := range dropped {
			s.leave(name, "Codex project tables")
		}
		bundle.ProviderFiles[i].Content = out
		bundle.ProviderFiles[i].SizeBytes = int64(len(out))
		bundle.ProviderFiles[i].SHA256 = hashSetupAssetContent(out)
	}
}

// codexProjectHeader matches a table header for one project, with the key
// as a basic or literal TOML string: [projects."/p"] or
// [projects.'/p'.sub].
// tomlHeaderLine is a [table] or [[array]] header line, not a row of a
// multi-line array such as  ["x"],
var tomlHeaderLine = regexp.MustCompile(`^\s*\[\[?[^\[\],]*\]\]?\s*(#.*)?$`)

var codexProjectHeader = regexp.MustCompile(`^\s*\[\s*projects\s*\.\s*("(?:[^"\\]|\\.)*"|'[^']*')\s*[.\]]`)

// withoutCodexProjects removes the [projects."PATH"] tables whose PATH lies
// in an unselected work dir, keeping every other line as written. It then
// decodes the result; a project still there (a dotted key, an inline table,
// a file that does not decode) is returned as blocked, with the work dir it
// belongs to, and the caller does not ship the file.
func (s workDirScope) withoutCodexProjects(content []byte) (out []byte, dropped []string, blocked string) {
	var buf bytes.Buffer
	skipping, inMulti, depth := false, false, 0
	// SplitAfter keeps each line's own ending, so a CRLF file stays CRLF.
	for _, raw := range bytes.SplitAfter(content, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r\n")
		// A line inside a multi-line string is text, not a header.
		// A row of a multi-line array ([ "x" ]) is a value, not a header.
		header := !inMulti && depth == 0 && tomlHeaderLine.MatchString(line)
		if header {
			skipping = false
			if m := codexProjectHeader.FindStringSubmatch(line); m != nil {
				var key struct{ K string }
				if _, err := toml.Decode("K = "+m[1], &key); err == nil {
					if name, ok := s.excluded(key.K); ok {
						skipping = true
						dropped = append(dropped, name)
					}
				}
			}
		}
		if !header && !inMulti {
			depth += bracketDepth(line)
			if depth < 0 {
				depth = 0
			}
		}
		if (strings.Count(line, `"""`)+strings.Count(line, `'''`))%2 == 1 {
			inMulti = !inMulti
		}
		if !skipping {
			buf.Write(raw)
		}
	}
	out = content
	if len(dropped) > 0 {
		out = buf.Bytes()
	}
	var doc struct {
		Projects map[string]any `toml:"projects"`
	}
	if _, err := toml.Decode(string(out), &doc); err != nil {
		// The edit broke a file that decoded: do not ship what it made.
		if len(dropped) > 0 {
			var orig map[string]any
			if _, oerr := toml.Decode(string(content), &orig); oerr == nil {
				return nil, nil, dropped[0]
			}
		}
		for _, n := range s.unselected {
			for _, root := range n.Roots() {
				if bytes.Contains(out, []byte(filepath.ToSlash(root))) || bytes.Contains(out, []byte(root)) {
					return nil, nil, n.Name
				}
			}
		}
		return out, dropped, ""
	}
	for p := range doc.Projects {
		if name, ok := s.excluded(p); ok {
			return nil, nil, name
		}
	}
	return out, dropped, ""
}

// --- sessions, instructions, detected agents ----------------------------

func (s workDirScope) applySessions(bundle *SetupBundle) {
	var keep []agent.Session
	for _, sess := range bundle.Sessions.Sessions {
		if name, ok := s.excluded(sess.ProjectDir); ok {
			s.leave(name, "sessions")
			continue
		}
		keep = append(keep, sess)
	}
	if len(keep) != len(bundle.Sessions.Sessions) {
		bundle.Sessions.Sessions = keep
	}
}

// applyInstructions leaves out instructions for a directory in an
// unselected work dir, and files pulled from a project there.
func (s workDirScope) applyInstructions(bundle *SetupBundle) {
	excludedKeys := map[string]string{}
	for _, p := range s.known.Projects {
		if name, ok := s.excluded(p.Abs); ok {
			excludedKeys[locations.CanonicalKey(locations.ProjectKey(p))] = name
		}
	}
	var keep []agent.InstructionFile
	for _, inst := range bundle.SharedConfig.Instructions {
		if name, ok := s.instructionOwner(inst, excludedKeys); ok {
			s.leave(name, "instructions")
			continue
		}
		keep = append(keep, inst)
	}
	if len(keep) != len(bundle.SharedConfig.Instructions) {
		bundle.SharedConfig.Instructions = keep
	}
}

func (s workDirScope) instructionOwner(inst agent.InstructionFile, excludedKeys map[string]string) (string, bool) {
	if inst.SourceProject != "" {
		if name, ok := excludedKeys[locations.CanonicalKey(inst.SourceProject)]; ok {
			return name, true
		}
	}
	if inst.DirectoryPattern != "" {
		if name, ok := s.excluded(globLiteralPrefix(inst.DirectoryPattern)); ok {
			return name, true
		}
	}
	return "", false
}

// globLiteralPrefix is the directory part of pattern before its first glob
// character: /w/acme/** gives /w/acme.
func globLiteralPrefix(pattern string) string {
	i := strings.IndexAny(pattern, "*?[{")
	if i < 0 {
		return pattern
	}
	return filepath.Dir(pattern[:i] + "x")
}

func (s workDirScope) applyDetected(bundle *SetupBundle) {
	for i := range bundle.DetectedAgents {
		list, ok := bundle.DetectedAgents[i].Settings["trusted_projects"].([]string)
		if !ok {
			continue
		}
		var keep []string
		for _, p := range list {
			if name, ok := s.excluded(p); ok {
				s.leave(name, "trusted projects")
				continue
			}
			keep = append(keep, p)
		}
		if len(keep) == 0 {
			delete(bundle.DetectedAgents[i].Settings, "trusted_projects")
		} else {
			bundle.DetectedAgents[i].Settings["trusted_projects"] = keep
		}
	}
}

// --- anything else that names the work dir ------------------------------

// mentions returns the unselected work dir that text names.
func (s workDirScope) mentions(text []byte) (string, bool) {
	for _, n := range s.unselected {
		for _, needle := range s.needles[n.Name] {
			if containsPath(text, needle) {
				return n.Name, true
			}
		}
	}
	return "", false
}

func (s workDirScope) mentionsValue(v any) (string, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return s.mentions(data)
}

// applyMentions withholds each carried file, and drops each instruction,
// whose text names an unselected work dir: as an absolute path, with
// forward slashes, or relative to home (~/, $HOME/, ${HOME}/). The formats
// that hold paths are many (settings, hooks, MCP arguments, prose), so this
// reads the text rather than each format.
func (s workDirScope) applyMentions(bundle *SetupBundle) {
	for _, group := range []*[]SetupAsset{&bundle.ProviderFiles, &bundle.SkillAssets, &bundle.ProjectFiles, &bundle.InstructionOverrides} {
		for i := range *group {
			a := &(*group)[i]
			name, ok := s.mentions([]byte(a.LogicalPath))
			if !ok && a.ContentPresent {
				name, ok = s.mentions(a.Content)
			}
			if ok {
				a.Content, a.ContentPresent, a.SHA256, a.SizeBytes, a.Redacted = nil, false, "", 0, true
				s.leave(name, "files naming it (withheld)")
			}
		}
	}
	bundle.SharedConfig.Instructions = dropMentioning(s, bundle.SharedConfig.Instructions, "instructions")
}

// dropMentioning returns list without the entries that name an unselected
// work dir, counting them as kind.
func dropMentioning[T any](s workDirScope, list []T, kind string) []T {
	if len(list) == 0 {
		return list
	}
	out := list[:0:0]
	for _, v := range list {
		if name, ok := s.mentionsValue(v); ok {
			s.leave(name, kind)
			continue
		}
		out = append(out, v)
	}
	if len(out) == len(list) {
		return list
	}
	return out
}

// dropKeysMentioning removes the entries of m whose key or value names an
// unselected work dir.
func dropKeysMentioning[V any](s workDirScope, m map[string]V, kind string) map[string]V {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]V, len(m))
	for k, v := range m {
		name, ok := s.mentions([]byte(k))
		if !ok {
			name, ok = s.mentionsValue(v)
		}
		if ok {
			s.leave(name, kind)
			continue
		}
		out[k] = v
	}
	return out
}

// applyStructured filters the vault parts of the bundle entry by entry.
// It returns which agents stay.
func (s workDirScope) applyStructured(bundle *SetupBundle) []bool {
	keepAgents := make([]bool, len(bundle.Agents))
	var agents []agent.Agent
	for i, a := range bundle.Agents {
		if name, ok := s.mentionsValue(a); ok {
			s.leave(name, "agents")
			continue
		}
		keepAgents[i] = true
		agents = append(agents, a)
	}
	if len(agents) != len(bundle.Agents) {
		bundle.Agents = agents
	}
	bundle.Sessions.Sessions = dropMentioning(s, bundle.Sessions.Sessions, "sessions")
	sc := &bundle.SharedConfig
	sc.MCPServers = dropMentioning(s, sc.MCPServers, "MCP servers")
	sc.Rules = dropMentioning(s, sc.Rules, "rules")
	sc.Roles = dropMentioning(s, sc.Roles, "roles")
	sc.Pricing = dropMentioning(s, sc.Pricing, "pricing")
	bundle.Templates.Assets = dropMentioning(s, bundle.Templates.Assets, "workflow templates")
	bundle.ModelCapabilities = dropMentioning(s, bundle.ModelCapabilities, "model capabilities")
	bundle.Gateways = dropMentioning(s, bundle.Gateways, "gateways")
	bundle.DetectedAgents = dropMentioning(s, bundle.DetectedAgents, "detected agents")
	if p := bundle.Plugins; p != nil {
		before := map[string]bool{}
		for _, m := range p.Marketplaces {
			before[m.Name] = true
		}
		p.Marketplaces = dropMentioning(s, p.Marketplaces, "plugin marketplaces")
		for _, m := range p.Marketplaces {
			delete(before, m.Name)
		}
		p.Plugins = dropMentioning(s, p.Plugins, "plugins")
		// A plugin from a marketplace left out cannot be installed there.
		var keep []InstalledPlugin
		for _, pl := range p.Plugins {
			if i := strings.LastIndex(pl.ID, "@"); i >= 0 && before[pl.ID[i+1:]] {
				continue
			}
			keep = append(keep, pl)
		}
		p.Plugins = keep
	}
	if c := bundle.ProviderConfigs.Claude; c != nil {
		cc := *c
		cc.MCPServers = dropKeysMentioning(s, cc.MCPServers, "Claude MCP servers")
		cc.CustomSettings = dropKeysMentioning(s, cc.CustomSettings, "Claude settings")
		cc.Keybindings = dropKeysMentioning(s, cc.Keybindings, "Claude settings")
		cc.EnabledPlugins = dropKeysMentioning(s, cc.EnabledPlugins, "Claude settings")
		cc.AutoApprove = dropMentioning(s, cc.AutoApprove, "Claude settings")
		bundle.ProviderConfigs.Claude = &cc
	}
	if c := bundle.ProviderConfigs.Codex; c != nil {
		cc := *c
		cc.TrustedProjects = dropKeysMentioning(s, cc.TrustedProjects, "trusted projects")
		cc.Rules = dropKeysMentioning(s, cc.Rules, "Codex rules")
		cc.CustomSettings = dropKeysMentioning(s, cc.CustomSettings, "Codex settings")
		if projects, ok := cc.CustomSettings["projects"].(map[string]any); ok {
			cc.CustomSettings["projects"] = dropKeysMentioning(s, projects, "trusted projects")
		}
		bundle.ProviderConfigs.Codex = &cc
	}
	if bundle.StatusSnapshot != nil {
		if name, ok := s.mentionsValue(bundle.StatusSnapshot); ok {
			s.leave(name, "status snapshot")
			bundle.StatusSnapshot = nil
		}
	}
	return keepAgents
}

// applyDeclined drops declines that name an unselected work dir: a memory
// file the collector refused is listed by its folder, which is the
// project's path.
func (s workDirScope) applyDeclined(bundle *SetupBundle) {
	var keep []DeclinedAsset
	for _, d := range bundle.Declined {
		if m := memoryAssetPath.FindStringSubmatch(strings.TrimPrefix(filepath.ToSlash(d.Path), ".claude/")); m != nil {
			if name := s.memoryOwner(m[1]); name != "" {
				s.leave(name, "declined entries")
				continue
			}
		}
		if name, ok := s.mentionsValue(d); ok {
			s.leave(name, "declined entries")
			continue
		}
		if name, ok := s.excluded(d.Path); ok && filepath.IsAbs(d.Path) {
			s.leave(name, "declined entries")
			continue
		}
		keep = append(keep, d)
	}
	if len(keep) != len(bundle.Declined) {
		bundle.Declined = keep
	}
}

// verify fails when any part of the bundle still names an unselected work
// dir. Each filter above handles a known field; this catches the field
// added later that none of them knows.
func (s workDirScope) verify(bundle *SetupBundle) error {
	data, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var hits []string
	for field, raw := range fields {
		if name, ok := s.mentions(raw); ok {
			hits = append(hits, fmt.Sprintf("%s (work dir %s)", field, name))
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Strings(hits)
	return fmt.Errorf("--workdirs: the bundle still names an unselected work dir in %s; export it with --workdirs all, or remove the reference", strings.Join(hits, ", "))
}

// workDirNeedles are the spellings of a work dir's path a file may hold.
func workDirNeedles(n locations.Named, home string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, root := range n.Roots() {
		add(root)
		add(filepath.ToSlash(root))
		if home != "" {
			if rel, err := filepath.Rel(home, root); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				rel = filepath.ToSlash(rel)
				add("~/" + rel)
				add("$HOME/" + rel)
				add("${HOME}/" + rel)
				back := strings.ReplaceAll(rel, "/", `\`)
				add(`~\` + back)
				add(`%USERPROFILE%\` + back)
				add(`$env:USERPROFILE\` + back)
			}
		}
	}
	// The same spellings as JSON writes them: a Windows path's backslashes
	// doubled, & < > as \u0026 \u003c \u003e. Vault parts are matched in
	// their encoded form.
	for _, p := range append([]string(nil), out...) {
		if enc, err := json.Marshal(p); err == nil {
			add(string(enc[1 : len(enc)-1]))
		}
	}
	return out
}

// containsPath reports whether text holds needle as a whole path: what
// follows it ends the path or starts a component, so ~/work/acme does not
// match ~/work/acme-old, while "in ~/work/acme." does. '.', '-' and '_'
// continue a name only when a name character follows them; any byte >= 0x80
// ends it, which can only make more text count as naming the work dir.
func containsPath(text []byte, needle string) bool {
	hay, nd := text, []byte(needle)
	if caseInsensitiveOS() {
		hay, nd = bytes.ToLower(text), bytes.ToLower(nd)
	}
	for off := 0; ; {
		i := bytes.Index(hay[off:], nd)
		if i < 0 {
			return false
		}
		if !continuesName(hay[off+i+len(nd):]) {
			return true
		}
		off += i + 1
	}
}

// continuesName reports whether rest, the text after a match, carries on
// the last path component.
func continuesName(rest []byte) bool {
	for _, c := range rest {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			return true
		case c == '.' || c == '-' || c == '_':
			continue
		default:
			return false
		}
	}
	return false
}

// pruneSessionRefs keeps the session config consistent after agents or
// sessions were left out: a session loses the agents that are gone (and is
// dropped when none of its agents is left), default agents lose them too,
// and an active session that is gone is cleared.
func pruneSessionRefs(sc *agent.SessionConfig, agents []agent.Agent, keepAgents []bool) {
	dropped := false
	for _, k := range keepAgents {
		dropped = dropped || !k
	}
	if dropped {
		names := map[string]bool{}
		for _, a := range agents {
			names[a.Name] = true
		}
		var sessions []agent.Session
		for _, sess := range sc.Sessions {
			if len(sess.Agents) > 0 {
				var kept []agent.SessionAgent
				for _, sa := range sess.Agents {
					if names[sa.Name] {
						kept = append(kept, sa)
					}
				}
				if len(kept) == 0 {
					continue
				}
				sess.Agents = kept
			}
			sessions = append(sessions, sess)
		}
		sc.Sessions = sessions
		var defaults []string
		for _, n := range sc.DefaultAgents {
			if names[n] {
				defaults = append(defaults, n)
			}
		}
		sc.DefaultAgents = defaults
	}
	if sc.ActiveSession != "" {
		found := false
		for _, sess := range sc.Sessions {
			found = found || sess.ID == sc.ActiveSession
		}
		if !found {
			sc.ActiveSession = ""
		}
	}
}

// pruneAgentBlanks drops the blanked-value notes of agents that were left
// out: import has nothing to fill, and the note would still carry the
// agent's name.
func pruneAgentBlanks(bundle *SetupBundle) {
	names := map[string]bool{}
	for _, a := range bundle.Agents {
		names[a.Name] = true
	}
	var keep []VaultBlank
	for _, b := range bundle.VaultBlanks {
		if b.Section == blankAgent && !names[b.Name] {
			continue
		}
		keep = append(keep, b)
	}
	if len(keep) != len(bundle.VaultBlanks) {
		bundle.VaultBlanks = keep
	}
}

// bracketDepth is the change in [ ] nesting over one TOML line, not
// counting brackets inside strings or after a comment.
func bracketDepth(line string) int {
	d := 0
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return d
		case c == '[':
			d++
		case c == ']':
			d--
		}
	}
	return d
}
