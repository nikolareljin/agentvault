package cmd

import (
	"bufio"
	"bytes"
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
}

// resolveWorkDirScope reads --workdirs: "all", or names declared with
// `agentvault workdir add`. An empty spec is "all".
func resolveWorkDirScope(known locations.Set, spec string) (workDirScope, error) {
	s := workDirScope{known: known, left: map[string]map[string]int{}}
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "all" {
		s.selected = known.Named
		return s, nil
	}
	if len(known.Named) == 0 {
		return s, fmt.Errorf("--workdirs %s: no work dirs are declared; see agentvault workdir add", printable(spec))
	}
	want := map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if _, ok := known.Lookup(name); !ok {
			return s, fmt.Errorf("--workdirs: no work dir named %s (declared: %s)", printable(name), declaredNames(known))
		}
		want[name] = true
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
// what must not carry it.
func (s workDirScope) apply(bundle *SetupBundle, home string) {
	if len(s.unselected) == 0 {
		return
	}
	s.applyMemory(bundle, home)
	s.applyPlugins(bundle)
	s.applyLocations(bundle)
	s.applyCodex(bundle)
	s.applySessions(bundle)
	s.applyInstructions(bundle)
	s.applyDetected(bundle)
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
			if slugEqual(folder, slug) || slugHasPrefix(folder, slug+"-") {
				return n.Name
			}
		}
	}
	return ""
}

// slugUnder reports whether root or a directory below it has the slug
// target. It descends only into entries whose slug still leads to target,
// and not through symlinks.
func slugUnder(root, target string) bool {
	slug := claudeProjectSlug(root)
	if slugEqual(slug, target) {
		return true
	}
	if !slugHasPrefix(target, slug+"-") {
		return false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if slugUnder(filepath.Join(root, e.Name()), target) {
			return true
		}
	}
	return false
}

// caseInsensitiveOS is whether paths compare without case; tests set it.
var caseInsensitiveOS = func() bool { return runtime.GOOS == "windows" }

// Paths on Windows compare without case, so their slugs do too.
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
var codexProjectHeader = regexp.MustCompile(`^\s*\[\s*projects\s*\.\s*("(?:[^"\\]|\\.)*"|'[^']*')\s*[.\]]`)

// withoutCodexProjects removes the [projects."PATH"] tables whose PATH lies
// in an unselected work dir, keeping every other line as written. It then
// decodes the result; a project still there (a dotted key, an inline table,
// a file that does not decode) is returned as blocked, with the work dir it
// belongs to, and the caller does not ship the file.
func (s workDirScope) withoutCodexProjects(content []byte) (out []byte, dropped []string, blocked string) {
	var buf bytes.Buffer
	skipping, inMulti := false, false
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// A line inside a multi-line string is text, not a header.
		if !inMulti && strings.HasPrefix(strings.TrimSpace(line), "[") {
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
		if (strings.Count(line, `"""`)+strings.Count(line, `'''`))%2 == 1 {
			inMulti = !inMulti
		}
		if !skipping {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if sc.Err() != nil {
		return nil, nil, s.unselected[0].Name
	}
	out = content
	if len(dropped) > 0 {
		out = buf.Bytes()
	}
	var doc struct {
		Projects map[string]any `toml:"projects"`
	}
	if _, err := toml.Decode(string(out), &doc); err != nil {
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
