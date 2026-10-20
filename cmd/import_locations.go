package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	UserMaps      []pathRemap
	Home          string
	In            *bufio.Reader
	Out           io.Writer
	// Known is what the vault already records; aliases are checked against it.
	Known locations.Set
	// Identify replaces locations.DefaultIdentifier in tests.
	Identify locations.Identifier
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
}

// parseWorkDirMaps reads --workdir-map OLD=NEW. OLD is the bundle's path;
// NEW must be a directory here.
func parseWorkDirMaps(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, m := range values {
		from, to, ok := strings.Cut(m, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("--workdir-map %q: want OLD=NEW", m)
		}
		from = strings.TrimRight(from, `/\`)
		if !remappableRoot(from) {
			return nil, fmt.Errorf("--workdir-map %q: OLD must be an absolute path below the filesystem root", m)
		}
		abs, err := filepath.Abs(expandUserPath(to))
		if err != nil {
			return nil, fmt.Errorf("--workdir-map %q: %w", m, err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("--workdir-map %q: %s is not a directory", m, abs)
		}
		out[filepath.Clean(from)] = filepath.Clean(abs)
	}
	return out, nil
}

// skipGitRunner gives no root commits, so a scan where every bundle project
// has a remote never runs git.
func skipGitRunner(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

// resolveBundleLocations matches the bundle's work dirs and projects to this
// machine. sourceProject is the bundle's --project path.
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
	targets, unmatched := locations.ResolveWorkDirs(*set, o.Home, o.WorkDirMaps)
	userChosen := map[string]bool{}
	for _, w := range set.WorkDirs {
		if _, ok := o.WorkDirMaps[filepath.Clean(w.Abs)]; ok {
			userChosen[w.ID] = true
		}
	}
	// A work dir that holds no project with an identity has nothing to find.
	unmatched = needed(*set, unmatched)
	if len(unmatched) > 0 {
		if !o.Interactive || o.DryRun {
			fmt.Fprintln(out, "  Work dirs from the bundle not found here:")
			for _, w := range unmatched {
				fmt.Fprintf(out, "    %s  (pass --workdir-map %s=<path here>)\n", printable(w.Abs), printable(w.Abs))
				for _, s := range locations.Suggest(o.Home, w) {
					fmt.Fprintf(out, "      maybe: %s\n", printable(s))
				}
			}
			if !o.DryRun {
				return res, errors.New("work dirs not found: pass --workdir-map OLD=NEW for each, or --no-match to keep the bundle's paths")
			}
		} else {
			for _, w := range unmatched {
				to, err := locations.AskWorkDir(w, locations.Suggest(o.Home, w), o.Home, o.In, out)
				if err != nil {
					return res, fmt.Errorf("import aborted: %w", err)
				}
				if to != "" {
					targets[w.ID] = to
					userChosen[w.ID] = true
				}
			}
		}
	}

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
		workDir string
		project *locations.Project
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
		rows = append(rows, locations.Row{From: w.Abs, To: to, Note: "work dir"})
		refs = append(refs, rowRef{workDir: w.ID})
	}
	for i := range proposals {
		p := proposals[i]
		if coveredByUserMap(p.Project.Abs, o.UserMaps) {
			continue
		}
		if p.Target != "" && !allowedTarget(p.Target, o.Home, targets, userChosen) {
			continue
		}
		rows = append(rows, locations.Row{From: p.Project.Abs, To: p.Target, Choices: p.Choices, Note: fmt.Sprintf("score %d: %s", p.Score, p.Reason)})
		refs = append(refs, rowRef{project: &proposals[i].Project})
	}
	if len(rows) == 0 {
		return res, nil
	}

	switch {
	case o.DryRun:
		printRows(out, rows, "would ask to confirm")
	case o.Interactive:
		var err error
		rows, err = locations.Confirm(rows, o.Home, o.In, out)
		if err != nil {
			return res, fmt.Errorf("import aborted: %w", err)
		}
	case o.AcceptMatches:
		printRows(out, rows, "accepted by --accept-matches")
	default:
		printRows(out, rows, "not applied")
		fmt.Fprintln(out, "  Computed matches need confirmation: run on a terminal, pass --accept-matches, or name each path with --map OLD=NEW.")
		fmt.Fprintln(out, "  Project files are not written and bundle paths are rewritten only by --map and the home directory.")
		res.Refused = true
		return res, nil
	}

	for i, r := range rows {
		if r.To == "" {
			if len(r.Choices) > 0 {
				fmt.Fprintf(out, "  skipped %s: ambiguous; pass --map %s=<one of the paths above>\n", printable(r.From), printable(r.From))
			}
			continue
		}
		res.Remaps = append(res.Remaps, pathRemap{From: r.From, To: r.To})
		if p := refs[i].project; p != nil {
			stored := *p
			stored.Aliases = nil
			if safeAlias(p.Abs, o.Home, o.Known) {
				stored.Aliases = []string{p.Abs}
			}
			stored.Abs = r.To
			stored.WorkDir = ""
			stored.Rel = ""
			res.Stored.Projects = append(res.Stored.Projects, stored)
			if sourceProject != "" && filepath.Clean(sourceProject) == filepath.Clean(p.Abs) {
				res.ProjectRoot = r.To
			}
		}
	}
	return res, nil
}

// usableLocations keeps the work dirs and projects whose bundle path can be
// a rewrite source, cleaned the way --workdir-map cleans OLD. It drops "/",
// a relative path such as "lib" (which would rewrite that word in prose)
// and anything with control characters, and warns about each. Bundle
// aliases are dropped: only this vault records aliases.
func usableLocations(in *locations.Set, out io.Writer) *locations.Set {
	res := &locations.Set{}
	clean := func(kind, abs string) (string, bool) {
		c := strings.TrimRight(strings.TrimSpace(abs), `/\`)
		if c != "" && !windowsStylePath(c) {
			c = filepath.Clean(c)
		}
		if !remappableRoot(c) || printable(abs) != abs {
			fmt.Fprintf(out, "  warning: ignoring %s %q from the bundle: not an absolute path below the filesystem root\n", kind, printable(abs))
			return "", false
		}
		return c, true
	}
	for _, w := range in.WorkDirs {
		if c, ok := clean("work dir", w.Abs); ok && printable(w.Label) == w.Label {
			w.Abs = c
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

// safeAlias is a bundle path that may be recorded as another name of a
// project: not this home or above it, and not above another known project
// (an alias of /home/a would let a directory pattern for any path under it
// follow this repository).
func safeAlias(alias, home string, known locations.Set) bool {
	if home != "" && pathWithin(alias, home) {
		return false
	}
	for _, p := range known.Projects {
		for _, a := range append([]string{p.Abs}, p.Aliases...) {
			if a != alias && pathWithin(alias, a) {
				return false
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
