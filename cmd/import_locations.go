package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/gitid"
	"github.com/nikolareljin/agentvault/internal/locations"
)

// A bundle names projects by identity as well as by path. Import finds each
// one on this machine, shows the list, and only uses what the user accepts:
// a bundle can name any public repository, including one the user has
// checked out, so a computed match is a proposal, never a write target on
// its own.

// locationOptions are the import flags and I/O for matching.
type locationOptions struct {
	NoMatch       bool
	AcceptMatches bool
	Interactive   bool
	DryRun        bool
	WorkDirMaps   map[string]string
	// WorkDirNames are --workdir-map NAME=PATH: a bundle work dir exported
	// under NAME goes to PATH.
	WorkDirNames map[string]string
	UserMaps     []pathRemap
	Home         string
	In           *bufio.Reader
	Out          io.Writer
	// Known is what the vault already records; aliases are checked against it.
	Known locations.Set
	// Identify replaces locations.DefaultIdentifier in tests.
	Identify locations.Identifier
	// Asked remembers answers across the profiles of one import, so each
	// question is asked once. Nil means no memory.
	Asked *askMemo
	// workDirs are every work dir path this import knows (bundle, vault,
	// mapped targets); an alias may not cover one. bundle is the bundle's
	// own set, used to tell a nested repository's work dir from ~/Projects.
	workDirs []string
	bundle   *locations.Set
	// confirmedHidden are the work dir ids whose saved hidden mapping was
	// confirmed on an earlier import (locations.Set.ConfirmedHidden).
	confirmedHidden map[string]bool
	// declared are the bundle work dirs (cleaned path) placed at this
	// machine's work dir of the same name; each is a proposal to confirm.
	declared map[string]string
	// verifiedHidden are the bundle projects (path) under a confirmed hidden
	// work dir and the local checkout each was verified at. Only that exact
	// target skips the hidden question.
	verifiedHidden map[string]string
}

// askMemo holds the answers given during one import.
type askMemo struct {
	workDir map[string]string // bundle work dir path -> answer ("" = skipped)
	hidden  map[string]string // from + NUL + target -> answer ("" = skipped)
}

func newAskMemo() *askMemo {
	return &askMemo{workDir: map[string]string{}, hidden: map[string]string{}}
}

// askHidden is ConfirmHidden, asked once per entry per import. remembered
// is true when the answer was given for an earlier profile.
func askHidden(o locationOptions, from, to string, out io.Writer) (answer string, remembered bool, err error) {
	key := from + "\x00" + to
	if o.Asked != nil {
		if a, ok := o.Asked.hidden[key]; ok {
			return a, true, nil
		}
	}
	got, err := locations.ConfirmHidden(from, to, o.Home, o.In, out)
	if err == nil && o.Asked != nil {
		o.Asked.hidden[key] = got
	}
	return got, false, err
}

// askWorkDir is AskWorkDir, asked once per work dir per import. remembered
// is true when the answer was given for an earlier profile.
func askWorkDir(o locationOptions, w locations.WorkDir, out io.Writer) (answer string, remembered bool, err error) {
	key := locations.CleanForeign(w.Abs)
	if o.Asked != nil {
		if a, ok := o.Asked.workDir[key]; ok {
			return a, true, nil
		}
	}
	got, err := locations.AskWorkDir(w, locations.Suggest(o.Home, w), o.Home, o.In, out)
	if err == nil && o.Asked != nil {
		o.Asked.workDir[key] = got
	}
	return got, false, err
}

// addNewMap records a mapping to save, once per bundle path.
func (r *locationResult) addNewMap(from, to string) {
	key := locations.CleanForeign(from)
	for i, m := range r.NewMaps {
		if locations.CleanForeign(m.From) == key {
			r.NewMaps[i].To = to
			return
		}
	}
	r.NewMaps = append(r.NewMaps, pathRemap{From: from, To: to})
}

// addConfirmedHidden records that the user confirmed from -> to, a target in
// a hidden folder, so a later import with the same saved mapping does not
// ask again.
// projects are the ProjectKey of each bundle project under the work dir:
// the confirmation covers those and no others.
func (r *locationResult) addConfirmedHidden(from, to string, projects []string) {
	key := locations.CleanForeign(from)
	for i, m := range r.ConfirmedHidden {
		if locations.CleanForeign(m.From) == key {
			r.ConfirmedHidden[i] = confirmedHidden{From: from, To: to, Projects: projects}
			return
		}
	}
	r.ConfirmedHidden = append(r.ConfirmedHidden, confirmedHidden{From: from, To: to, Projects: projects})
}

// confirmedHidden is a hidden work dir target the user confirmed, with the
// projects it covers.
type confirmedHidden struct {
	From, To string
	Projects []string
}

// localHiddenKeys reads, from this machine, the identity of each bundle
// project under the work dir at path from, at its place under target to.
// A key is returned only when that checkout exists (no symlink), is its own
// repository root, has an identity (remote; root commits except on
// Windows), and the bundle claims the same identity. all is false when any
// project fails, or there is none: a bundle's claims alone confirm nothing.
func localHiddenKeys(ctx context.Context, o locationOptions, set *locations.Set, from, to string) (keys []string, all bool) {
	keys, _, all = localHiddenCheckouts(ctx, o, set, from, to)
	return keys, all
}

// localHiddenCheckouts is localHiddenKeys with the verified checkout of
// each project, bundle path to local directory.
func localHiddenCheckouts(ctx context.Context, o locationOptions, set *locations.Set, from, to string) (keys []string, dirs map[string]string, all bool) {
	dirs = map[string]string{}
	identify := o.Identify
	if identify == nil {
		identify = locations.DefaultIdentifier
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all = true
	for _, w := range set.WorkDirs {
		if locations.CleanForeign(w.Abs) != locations.CleanForeign(from) {
			continue
		}
		for _, p := range set.Projects {
			if p.WorkDir != w.ID {
				continue
			}
			key, ok := localProjectKey(ctx, identify, p, to)
			if !ok {
				all = false
				continue
			}
			keys = append(keys, key)
			dirs[locations.CleanForeign(p.Abs)] = filepath.Clean(filepath.Join(to, filepath.FromSlash(p.Rel)))
		}
	}
	return keys, dirs, all && len(keys) > 0
}

func localProjectKey(ctx context.Context, identify locations.Identifier, p locations.Project, to string) (string, bool) {
	if p.Rel == "" || p.Rel == "." {
		return "", false
	}
	// Symlinks in the target the user chose (home, the target itself) are
	// resolved; none is followed at or below the target, so the checkout is
	// where the bundle says, and git's resolved root compares equal.
	base, err := filepath.EvalSymlinks(to)
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, filepath.FromSlash(p.Rel))
	if !pathWithin(base, dir) || filepath.Clean(dir) == filepath.Clean(base) {
		return "", false
	}
	cur := base
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Clean(filepath.FromSlash(p.Rel))), "/") {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil || !info.IsDir() {
			return "", false
		}
	}
	// It must be its own repository, not a folder inside another one.
	r, err := identify(ctx, dir)
	if err != nil {
		return "", false
	}
	root := filepath.Clean(r.Root)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if root != filepath.Clean(dir) {
		return "", false
	}
	local := locations.Project{GitRemote: r.Remote}
	if runtime.GOOS != "windows" {
		local.RootCommits = r.RootCommits
	}
	key := locations.ProjectKey(local)
	if strings.HasPrefix(key, "rel:") || key != locations.ProjectKey(p) {
		return "", false
	}
	return key, true
}

// flagHint is a flag line to paste into a shell: the bundle path in single
// quotes, which POSIX shells and PowerShell both take literally ($, `, ",
// ! included), and the placeholder outside them: '/a b'=<path here>. A
// path outside hintSafePath gets no ready command.
func flagHint(flag, from string) string {
	// cmd.exe ignores single quotes, so a Windows path with a space would
	// split there.
	if !hintSafePath.MatchString(from) || windowsStylePath(from) && strings.Contains(from, " ") {
		return "map " + printable(from) + " with " + flag + " (no ready command: the path holds characters a shell may interpret)"
	}
	return flag + " '" + from + "'=<path here>"
}

// hintSafePath is what flagHint quotes: ASCII letters, digits, space and
// / \ . _ : + , @ -. "=" is out: OLD=NEW splits at the first one. Anything else (cmd.exe & | ^ < > ( ) ! " %, quotes
// of any kind, non-ASCII) gets no ready command.
var hintSafePath = regexp.MustCompile(`^[A-Za-z0-9 /\\._:+,@-]+$`)

// shellQuote makes s one shell word.
func shellQuote(s string) string {
	safe := s != ""
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._-=+:,@%", c)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// locationResult is what matching decided for one profile.
type locationResult struct {
	// Remaps are the accepted rewrites, bundle path to local path.
	Remaps []pathRemap
	// Refused is set when computed matches were found but not accepted: no
	// computed path may be used, and project files are not written.
	Refused bool
	// ProjectRoot is the local path of the bundle's source project, when it
	// was matched and accepted.
	ProjectRoot string
	// Stored is what to record in the vault: each accepted project at its
	// local path, with the bundle path kept as an alias.
	Stored locations.Set
	// NewMaps are work dir mappings the user gave (--workdir-map or an
	// answer); they are saved in the vault for later imports.
	NewMaps []pathRemap
	// ConfirmedHidden are the NewMaps (or saved mappings) to a hidden folder
	// the user confirmed here; they are saved so the next import skips the
	// question.
	ConfirmedHidden []confirmedHidden
	// Missed are the work dirs and projects that could not be placed.
	Missed []missedEntry
	// SourceSkipped is set when the bundle's --project is among them.
	SourceSkipped bool
}

// parseWorkDirMaps reads --workdir-map OLD=NEW. OLD is the bundle's path,
// or the name a work dir was exported under; NEW must be a directory here.
// A name has no "/", "\\" or ":", so it cannot be read as a path.
func parseWorkDirMaps(values []string) (paths, names map[string]string, err error) {
	paths, names = map[string]string{}, map[string]string{}
	for _, m := range values {
		from, to, ok := strings.Cut(m, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, nil, fmt.Errorf("--workdir-map %q: want OLD=NEW or NAME=NEW", m)
		}
		isName := locations.ValidName(from)
		if !isName {
			from = locations.CleanForeign(from)
			if !remappableRoot(from) {
				return nil, nil, fmt.Errorf("--workdir-map %q: OLD must be a work dir name or an absolute path below the filesystem root", m)
			}
		}
		abs, err := filepath.Abs(expandUserPath(to))
		if err != nil {
			return nil, nil, fmt.Errorf("--workdir-map %q: %w", m, err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, nil, fmt.Errorf("--workdir-map %q: %s is not a directory", m, abs)
		}
		if isName {
			names[from] = filepath.Clean(abs)
		} else {
			paths[from] = filepath.Clean(abs)
		}
	}
	return paths, names, nil
}

// checkWorkDirNames refuses a --workdir-map NAME that no selected profile
// exported, so a typo is not silently ignored.
func checkWorkDirNames(profiles []PortableProfile, names map[string]string) error {
	have := map[string]bool{}
	for _, p := range profiles {
		if p.Setup.Locations == nil {
			continue
		}
		for _, w := range p.Setup.Locations.WorkDirs {
			if w.Name != "" {
				have[w.Name] = true
			}
		}
	}
	var unknown []string
	for n := range names {
		if !have[n] {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("--workdir-map: the bundle has no work dir named %s", strings.Join(unknown, ", "))
}

// byName places each bundle work dir that was exported under a name and
// that no --workdir-map OLD names. chosen is --workdir-map OLD=NEW plus
// NAME=PATH: the user typed both. declared maps a bundle work dir to this
// machine's work dir of the same name, if that directory still exists. The
// bundle picks that name, so declared is only a proposal, confirmed like a
// saved mapping. Both maps are new.
func byName(set *locations.Set, o locationOptions) (chosen, declared map[string]string) {
	chosen = make(map[string]string, len(o.WorkDirMaps))
	for k, v := range o.WorkDirMaps {
		chosen[k] = v
	}
	declared = map[string]string{}
	for _, w := range set.WorkDirs {
		key := locations.CleanForeign(w.Abs)
		if _, given := chosen[key]; given || w.Name == "" {
			continue
		}
		if to, ok := o.WorkDirNames[w.Name]; ok {
			chosen[key] = to
		} else if n, ok := o.Known.Lookup(w.Name); ok {
			if info, err := os.Stat(n.Path); err == nil && info.IsDir() {
				declared[key] = filepath.Clean(n.Path)
			}
		}
	}
	return chosen, declared
}

// skipGitRunner gives no root commits, so a scan where every bundle project
// has a remote never runs git.
func skipGitRunner(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

// resolveBundleLocations matches the bundle's work dirs and projects to this
// machine. sourceProject is the bundle's --project path. A work dir that
// cannot be placed does not stop the import: it is recorded in Missed, its
// projects are left out, and the rest goes on. On a terminal the user is
// then asked for each missed work dir, and only its projects are matched
// again.
func resolveBundleLocations(ctx context.Context, set *locations.Set, sourceProject string, o locationOptions) (locationResult, error) {
	var res locationResult
	if set.Empty() || o.NoMatch {
		return res, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	set = usableLocations(set, out)
	o.WorkDirMaps, o.declared = byName(set, o)
	// Saved mappings come from earlier imports; a declared name wins over
	// them, and --workdir-map over both.
	maps := map[string]string{}
	for k, v := range o.Known.WorkDirMaps {
		maps[k] = v
	}
	for k, v := range o.declared {
		maps[k] = v
	}
	for k, v := range o.WorkDirMaps {
		maps[k] = v
		res.addNewMap(k, v)
		if locations.HiddenBelowHome(v, o.Home) {
			keys, _ := localHiddenKeys(ctx, o, set, k, v)
			res.addConfirmedHidden(k, v, keys)
		}
	}
	targets, unmatched := locations.ResolveWorkDirs(*set, o.Home, maps)
	userChosen := map[string]bool{}
	for _, w := range set.WorkDirs {
		if _, ok := o.WorkDirMaps[locations.CleanForeign(w.Abs)]; ok {
			userChosen[w.ID] = true
		}
	}
	// A saved mapping to a hidden folder that was confirmed here before, for
	// the same target and for every project the bundle puts under it, is not
	// asked again. Only the vault (o.Known) is read: a bundle cannot confirm
	// anything, and a bundle reusing the path with other projects is asked.
	o.confirmedHidden = map[string]bool{}
	o.verifiedHidden = map[string]string{}
	for _, w := range set.WorkDirs {
		key := locations.CleanForeign(w.Abs)
		to, ok := targets[w.ID]
		if !ok || userChosen[w.ID] {
			continue
		}
		if saved, ok := o.Known.WorkDirMaps[key]; ok && saved == to {
			c := o.Known.ConfirmedHidden[key]
			if c.Target != to {
				continue
			}
			keys, dirs, all := localHiddenCheckouts(ctx, o, set, w.Abs, to)
			if !all || !c.Covers(keys) {
				continue
			}
			for k, d := range dirs {
				o.verifiedHidden[k] = d
			}
			o.confirmedHidden[w.ID] = true
		}
	}
	missed := map[string]string{} // work dir id -> reason
	for _, w := range needed(*set, unmatched) {
		missed[w.ID] = "not found here"
	}
	// A computed target in a hidden folder needs its own yes, whatever
	// --accept-matches says; --workdir-map naming it is that yes.
	for _, w := range set.WorkDirs {
		to, ok := targets[w.ID]
		if !ok || userChosen[w.ID] || o.confirmedHidden[w.ID] || !locations.HiddenBelowHome(to, o.Home) {
			continue
		}
		if o.Interactive && !o.DryRun {
			got, remembered, err := askHidden(o, w.Abs, to, out)
			if err != nil {
				return res, fmt.Errorf("import aborted: %w", err)
			}
			if got != "" {
				if !remembered {
					res.addNewMap(w.Abs, got)
					if locations.HiddenBelowHome(got, o.Home) {
						keys, _ := localHiddenKeys(ctx, o, set, w.Abs, got)
						res.addConfirmedHidden(w.Abs, got, keys)
					}
				}
				targets[w.ID], userChosen[w.ID] = got, true
				continue
			}
		}
		delete(targets, w.ID)
		if len(needed(*set, []locations.WorkDir{w})) > 0 {
			missed[w.ID] = "target " + to + " is in a hidden folder and was not confirmed"
		}
	}

	// Work dirs this machine vouches for (vault, saved and given mappings)
	// never become an alias; the bundle's own work dirs may, only for a
	// repository nested in the project (see nestedWorkDir).
	o.workDirs = nil
	o.bundle = set
	for _, w := range o.Known.WorkDirs {
		o.workDirs = append(o.workDirs, w.Abs)
	}
	for k, v := range maps {
		o.workDirs = append(o.workDirs, k, v)
	}
	// Local targets too, computed or chosen: an alias may not name one.
	for _, t := range targets {
		o.workDirs = append(o.workDirs, t)
	}
	if err := matchAndConfirm(ctx, without(set, missed), sourceProject, targets, userChosen, o, out, &res); err != nil {
		return res, err
	}

	for _, w := range set.WorkDirs {
		reason, ok := missed[w.ID]
		if !ok {
			continue
		}
		if o.Interactive && !o.DryRun {
			to, remembered, err := askWorkDir(o, w, out)
			if err != nil {
				return res, fmt.Errorf("import aborted: %w", err)
			}
			if to != "" {
				targets[w.ID], userChosen[w.ID] = to, true
				o.workDirs = append(o.workDirs, to)
				if !remembered {
					res.addNewMap(w.Abs, to)
					if locations.HiddenBelowHome(to, o.Home) {
						keys, _ := localHiddenKeys(ctx, o, set, w.Abs, to)
						res.addConfirmedHidden(w.Abs, to, keys)
					}
				}
				sub := only(set, w.ID)
				if err := matchAndConfirm(ctx, sub, sourceProject, map[string]string{w.ID: to}, map[string]bool{w.ID: true}, o, out, &res); err != nil {
					return res, err
				}
				continue
			}
		}
		hint := flagHint("--workdir-map", w.Abs)
		if w.Name != "" {
			hint = "--workdir-map " + w.Name + "=<path here>, or agentvault workdir add " + w.Name + " <path here>"
		}
		res.Missed = append(res.Missed, missedEntry{Path: w.Abs, Reason: reason, Hint: hint, Suggest: locations.Suggest(o.Home, w)})
		if sourceProject != "" {
			for _, p := range set.Projects {
				if p.WorkDir == w.ID && locations.CleanForeign(p.Abs) == locations.CleanForeign(sourceProject) {
					res.SourceSkipped = true
				}
			}
		}
	}
	pruneAliases(&res, o)
	return res, nil
}

// pruneAliases drops stored aliases that equal or contain a work dir known
// at the end of the run: targets chosen after a project was stored (answers
// to later questions) apply to its alias too.
func pruneAliases(res *locationResult, o locationOptions) {
	for i, p := range res.Stored.Projects {
		var keep []string
		for _, a := range p.Aliases {
			ok := true
			for _, w := range o.workDirs {
				if locations.Below(a, w) {
					ok = false
				}
			}
			if ok {
				keep = append(keep, a)
			}
		}
		res.Stored.Projects[i].Aliases = keep
	}
}

// missedEntry is a work dir or project that could not be placed.
type missedEntry struct {
	Path, Reason, Hint string
	Suggest            []string
}

// without is set minus the projects of the given work dirs.
func without(set *locations.Set, skip map[string]string) *locations.Set {
	out := &locations.Set{WorkDirs: set.WorkDirs}
	for _, p := range set.Projects {
		if _, ok := skip[p.WorkDir]; !ok {
			out.Projects = append(out.Projects, p)
		}
	}
	for _, w := range set.WorkDirs {
		if _, ok := skip[w.ID]; ok {
			out.WorkDirs = removeWorkDir(out.WorkDirs, w.ID)
		}
	}
	return out
}

func removeWorkDir(ws []locations.WorkDir, id string) []locations.WorkDir {
	var out []locations.WorkDir
	for _, w := range ws {
		if w.ID != id {
			out = append(out, w)
		}
	}
	return out
}

// only is the work dir id and its projects.
func only(set *locations.Set, id string) *locations.Set {
	out := &locations.Set{}
	for _, w := range set.WorkDirs {
		if w.ID == id {
			out.WorkDirs = append(out.WorkDirs, w)
		}
	}
	for _, p := range set.Projects {
		if p.WorkDir == id {
			out.Projects = append(out.Projects, p)
		}
	}
	return out
}

// matchAndConfirm scans for set's projects, shows the work dir and project
// matches as one list and adds what is accepted to res.
func matchAndConfirm(ctx context.Context, set *locations.Set, sourceProject string, targets map[string]string, userChosen map[string]bool, o locationOptions, out io.Writer, res *locationResult) error {
	identify := o.Identify
	if identify == nil {
		identify = locations.DefaultIdentifier
		if !needsRootCommits(*set) {
			identify = func(ctx context.Context, dir string) (gitid.Repo, error) {
				return gitid.Identify(ctx, dir, skipGitRunner)
			}
		}
	}
	roots := []string{}
	for _, id := range sortedStringKeys(targets) {
		roots = append(roots, targets[id])
	}
	if o.Home != "" {
		roots = append(roots, o.Home)
	}
	cands := locations.Identify(ctx, locations.Scan(roots, 3), identify)
	proposals := locations.MatchProjects(*set, targets, cands)

	// Rows: computed work dir matches, then projects. Work dirs the user
	// named are theirs and need no confirmation.
	type rowRef struct {
		project *locations.Project
		// workDir and proposed are a work dir row's id and its target before
		// the list was shown.
		workDir, proposed string
	}
	var rows []locations.Row
	var refs []rowRef
	for _, w := range set.WorkDirs {
		to, ok := targets[w.ID]
		if !ok {
			continue
		}
		if userChosen[w.ID] {
			res.Remaps = append(res.Remaps, pathRemap{From: w.Abs, To: to})
			continue
		}
		// A confirmation covers its verified projects, not the folder: no
		// prefix rewrite, so other bundle paths under it stay as they are.
		if o.confirmedHidden[w.ID] {
			fmt.Fprintf(out, "  %s -> %s: saved mapping (hidden, confirmed) for its projects only; other paths under it are not rewritten\n", printable(w.Abs), printable(to))
			continue
		}
		note := "work dir"
		if _, ok := o.declared[locations.CleanForeign(w.Abs)]; ok {
			note = "work dir, declared here as " + w.Name
		} else if _, saved := o.Known.WorkDirMaps[locations.CleanForeign(w.Abs)]; saved {
			note = "work dir, saved mapping"
		}
		rows = append(rows, locations.Row{From: w.Abs, To: to, Note: note})
		refs = append(refs, rowRef{workDir: w.ID, proposed: to})
	}
	for i := range proposals {
		p := proposals[i]
		if coveredByUserMap(p.Project.Abs, o.UserMaps) {
			continue
		}
		if p.Target != "" && !allowedTarget(p.Target, o.Home, targets, userChosen) {
			continue
		}
		if p.Target != "" && locations.HiddenBelowHome(p.Target, o.Home) && !underChosen(p.Target, targets, userChosen) && o.verifiedHidden[locations.CleanForeign(p.Project.Abs)] != filepath.Clean(p.Target) {
			got := ""
			if o.Interactive && !o.DryRun {
				var err error
				if got, _, err = askHidden(o, p.Project.Abs, p.Target, out); err != nil {
					return fmt.Errorf("import aborted: %w", err)
				}
			}
			if got == "" {
				res.Missed = append(res.Missed, missedEntry{Path: p.Project.Abs, Reason: "target " + p.Target + " is in a hidden folder and was not confirmed", Hint: flagHint("--map", p.Project.Abs) + ", or confirm it on a terminal"})
				if sourceProject != "" && locations.CleanForeign(sourceProject) == locations.CleanForeign(p.Project.Abs) {
					res.SourceSkipped = true
				}
				continue
			}
			p.Target = got
		}
		rows = append(rows, locations.Row{From: p.Project.Abs, To: p.Target, Choices: p.Choices, Note: fmt.Sprintf("score %d: %s", p.Score, p.Reason)})
		refs = append(refs, rowRef{project: &proposals[i].Project})
	}
	if len(rows) == 0 {
		return nil
	}

	switch {
	case o.DryRun:
		printRows(out, rows, "would ask to confirm")
	case o.Interactive:
		var err error
		rows, err = locations.Confirm(rows, o.Home, o.In, out)
		if err != nil {
			return fmt.Errorf("import aborted: %w", err)
		}
	case o.AcceptMatches:
		printRows(out, rows, "accepted by --accept-matches")
	default:
		printRows(out, rows, "not applied")
		fmt.Fprintln(out, "  Computed matches need confirmation: run on a terminal, pass --accept-matches, or name each path with --map OLD=NEW.")
		fmt.Fprintln(out, "  Project files are not written and bundle paths are rewritten only by --map and the home directory.")
		res.Refused = true
		return nil
	}

	// edited are the work dirs whose row the user changed, id to the new
	// target ("" when a new hidden target was declined). Their project rows
	// were matched against the old target, so they are matched again below.
	edited := map[string]string{}
	var editedOrder []string
	for i, r := range rows {
		if p := refs[i].project; p != nil {
			if _, ok := edited[p.WorkDir]; ok {
				continue
			}
		}
		if r.To == "" {
			if len(r.Choices) > 0 {
				fmt.Fprintf(out, "  skipped %s: ambiguous; pass --map %s=<one of the paths above>\n", printable(r.From), printable(r.From))
			}
			continue
		}
		// A work dir row edited in the list is the user's mapping: saved
		// like an answer. A new target in a hidden folder needs its own yes.
		if refs[i].project == nil && r.To != refs[i].proposed {
			to := r.To
			if locations.HiddenBelowHome(to, o.Home) {
				got, _, err := askHidden(o, r.From, to, out)
				if err != nil {
					return fmt.Errorf("import aborted: %w", err)
				}
				if got == "" {
					res.Missed = append(res.Missed, missedEntry{Path: r.From, Reason: "target " + to + " is in a hidden folder and was not confirmed", Hint: flagHint("--workdir-map", r.From)})
					edited[refs[i].workDir] = ""
					editedOrder = append(editedOrder, refs[i].workDir)
					continue
				}
				to = got
				if locations.HiddenBelowHome(to, o.Home) {
					keys, _ := localHiddenKeys(ctx, o, set, r.From, to)
					res.addConfirmedHidden(r.From, to, keys)
				}
			}
			res.addNewMap(r.From, to)
			r.To = to
			edited[refs[i].workDir] = to
			editedOrder = append(editedOrder, refs[i].workDir)
		}
		res.Remaps = append(res.Remaps, pathRemap{From: r.From, To: r.To})
		if p := refs[i].project; p != nil {
			stored := *p
			stored.Aliases = nil
			if safeAlias(p.Abs, *p, r.To, o.Home, o.workDirs, o.bundle, o.Known.Projects, res.Stored.Projects) {
				stored.Aliases = []string{p.Abs}
			}
			stored.Abs = r.To
			stored.WorkDir = ""
			stored.Rel = ""
			res.Stored.Projects = append(res.Stored.Projects, stored)
			if sourceProject != "" && locations.CleanForeign(sourceProject) == locations.CleanForeign(p.Abs) {
				res.ProjectRoot = r.To
			}
		}
	}
	for _, id := range editedOrder {
		to := edited[id]
		if to == "" {
			for _, p := range set.Projects {
				if p.WorkDir == id && sourceProject != "" && locations.CleanForeign(sourceProject) == locations.CleanForeign(p.Abs) {
					res.SourceSkipped = true
				}
			}
			continue
		}
		o.workDirs = append(o.workDirs, to)
		if err := matchAndConfirm(ctx, only(set, id), sourceProject, map[string]string{id: to}, map[string]bool{id: true}, o, out, res); err != nil {
			return err
		}
	}
	return nil
}

// printMissed is the end-of-import list of what could not be placed.
func printMissed(out io.Writer, missed []missedEntry) {
	fmt.Fprintf(out, "\nNot imported (%d):\n", len(missed))
	for _, m := range missed {
		fmt.Fprintf(out, "  %s: %s\n    pass %s\n", printable(m.Path), printable(m.Reason), printable(m.Hint))
		for _, s := range m.Suggest {
			fmt.Fprintf(out, "    maybe: %s\n", printable(s))
		}
	}
}

// nestedWorkDir reports whether the work dir at alias (the project's own
// path) holds only repositories nested inside this project: the bundle has
// a work dir there that is not the project's own work dir, and every
// project in it sits below alias. ~/Projects, where the project claims the
// folder that holds its siblings, is not.
func nestedWorkDir(alias string, p locations.Project, bundle *locations.Set) bool {
	if bundle == nil {
		return false
	}
	found := false
	for _, w := range bundle.WorkDirs {
		if !locations.Below(w.Abs, alias) || !locations.Below(alias, w.Abs) {
			continue
		}
		if w.ID == p.WorkDir {
			return false
		}
		for _, q := range bundle.Projects {
			// A project with no identity proves nothing about the layout.
			if q.WorkDir == w.ID && (!locations.Below(alias, q.Abs) || locations.Below(q.Abs, alias) || q.GitRemote == "" && len(q.RootCommits) == 0) {
				return false
			}
		}
		found = true
	}
	return found
}

// underChosen is a path inside a work dir target the user named (by
// --workdir-map or an answer): that choice confirms what is under it.
func underChosen(p string, targets map[string]string, userChosen map[string]bool) bool {
	for id, t := range targets {
		if userChosen[id] && pathWithin(t, p) {
			return true
		}
	}
	return false
}

// dedupeMissed keeps the first entry per path, so a work dir missed by
// several profiles is listed once.
func dedupeMissed(in []missedEntry) []missedEntry {
	seen := map[string]bool{}
	var out []missedEntry
	for _, m := range in {
		if !seen[m.Path] {
			seen[m.Path] = true
			out = append(out, m)
		}
	}
	return out
}

// usableLocations keeps the work dirs and projects whose bundle path can be
// a rewrite source, cleaned the way --workdir-map cleans OLD. It drops "/",
// a relative path such as "lib" (which would rewrite that word in prose)
// and anything with control characters, and warns about each. Bundle
// aliases are dropped: only this vault records aliases.
func usableLocations(in *locations.Set, out io.Writer) *locations.Set {
	res := &locations.Set{}
	clean := func(kind, abs string) (string, bool) {
		c := locations.CleanForeign(abs)
		if !remappableRoot(c) || printable(abs) != abs {
			fmt.Fprintf(out, "  warning: ignoring %s %q from the bundle: not an absolute path below the filesystem root\n", kind, printable(abs))
			return "", false
		}
		return c, true
	}
	// Export writes each name once; a name on two work dirs was edited in,
	// and placing both at one target would merge them.
	named := map[string]int{}
	for _, w := range in.WorkDirs {
		if w.Name != "" {
			named[w.Name]++
		}
	}
	for _, w := range in.WorkDirs {
		if w.Name != "" && named[w.Name] > 1 {
			fmt.Fprintf(out, "  warning: ignoring the name %q from the bundle: it names more than one work dir\n", printable(w.Name))
			w.Name = ""
		}
		if c, ok := clean("work dir", w.Abs); ok && printable(w.Label) == w.Label {
			w.Abs = c
			if w.Name != "" && !locations.ValidName(w.Name) {
				fmt.Fprintf(out, "  warning: ignoring the name %q of work dir %s from the bundle: not a valid work dir name\n", printable(w.Name), printable(c))
				w.Name = ""
			}
			res.WorkDirs = append(res.WorkDirs, w)
		}
	}
	for _, p := range in.Projects {
		if c, ok := clean("project", p.Abs); ok {
			p.Abs = c
			p.Aliases = nil
			res.Projects = append(res.Projects, p)
		}
	}
	return res
}

// safeAlias is a bundle path that may be recorded as another name of
// project p: not this home or above it, and not equal to, above or below
// any path or alias of a project with a different identity, among those
// the vault knows and those already accepted in this import. Otherwise a
// directory pattern for that other project would follow this one.
func safeAlias(alias string, p locations.Project, target, home string, workDirs []string, bundle *locations.Set, known ...[]locations.Project) bool {
	if home != "" && locations.Below(alias, home) {
		return false
	}
	// A directory that exists here is what it is; a bundle cannot rename
	// it. Only the project's own matched checkout may carry its path.
	if info, err := os.Lstat(alias); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || filepath.Clean(alias) != filepath.Clean(target) {
			return false
		}
	}
	// ~/Projects as the alias of one repository would pull every pattern
	// for the folder onto it.
	for _, w := range workDirs {
		if locations.Below(alias, w) {
			return false
		}
	}
	if bundle != nil {
		for _, w := range bundle.WorkDirs {
			if !locations.Below(alias, w.Abs) {
				continue
			}
			if !locations.Below(w.Abs, alias) || !nestedWorkDir(alias, p, bundle) {
				return false
			}
		}
	}
	for _, set := range known {
		for _, q := range set {
			if locations.SameIdentity(p, q) {
				continue
			}
			for _, a := range append([]string{q.Abs}, q.Aliases...) {
				if locations.Overlaps(alias, a) {
					return false
				}
			}
		}
	}
	return true
}

// needed keeps the work dirs that hold a project with an identity.
func needed(set locations.Set, ws []locations.WorkDir) []locations.WorkDir {
	var out []locations.WorkDir
	for _, w := range ws {
		for _, p := range set.Projects {
			if p.WorkDir == w.ID && (p.GitRemote != "" || len(p.RootCommits) > 0) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func needsRootCommits(set locations.Set) bool {
	for _, p := range set.Projects {
		if p.GitRemote == "" && len(p.RootCommits) > 0 {
			return true
		}
	}
	return false
}

// coveredByUserMap is a path that a --map rule already rewrites: --map
// always wins over a computed match.
func coveredByUserMap(p string, maps []pathRemap) bool {
	for _, m := range maps {
		if p == m.From || strings.HasPrefix(p, m.From+"/") || strings.HasPrefix(p, m.From+`\`) {
			return true
		}
	}
	return false
}

// allowedTarget is under this home or under a work dir the user named.
func allowedTarget(p, home string, targets map[string]string, userChosen map[string]bool) bool {
	if home != "" && pathWithin(home, p) {
		return true
	}
	for id, t := range targets {
		if userChosen[id] && pathWithin(t, p) {
			return true
		}
	}
	return false
}

func pathWithin(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func printRows(out io.Writer, rows []locations.Row, status string) {
	fmt.Fprintf(out, "  Matched projects (%s):\n", status)
	for i, r := range rows {
		to := r.To
		if to == "" {
			to = "(ambiguous)"
		}
		fmt.Fprintf(out, "    %d. %s -> %s  [%s]\n", i+1, printable(r.From), printable(to), printable(r.Note))
		for _, c := range r.Choices {
			fmt.Fprintf(out, "         candidate: %s\n", printable(c))
		}
	}
}

// mergeComputedRemaps adds accepted computed rewrites to the flag-built
// ones, longest prefix first, leaving out any a --map rule covers.
func mergeComputedRemaps(base, computed, userMaps []pathRemap) []pathRemap {
	out := append([]pathRemap{}, base...)
	for _, c := range computed {
		if c.From == c.To || !remappableRoot(c.From) || printable(c.From) != c.From || coveredByUserMap(c.From, userMaps) {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].From) > len(out[j].From) })
	return out
}
