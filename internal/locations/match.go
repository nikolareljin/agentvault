package locations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Candidate is a git checkout found on this machine.
type Candidate struct {
	Path        string
	Remote      string
	RootCommits []string
}

// Proposal is where one bundle project would go on this machine. Target is
// empty when the match is ambiguous; Choices then holds the tied candidates.
type Proposal struct {
	Project Project
	Target  string
	Score   int
	Reason  string
	Choices []string
}

// MatchThreshold is the lowest score that counts as the same repository.
const MatchThreshold = 80

// ResolveWorkDirs finds each work dir on this machine: an explicit map from
// the user first (keyed by the bundle's absolute path), then the same path
// relative to home, then a folder of the same name directly under home.
// Bundle data only selects among existing directories under home; it is
// never joined into a path outside it.
func ResolveWorkDirs(set Set, home string, maps map[string]string) (map[string]string, []WorkDir) {
	matched := map[string]string{}
	var unmatched []WorkDir
	for _, w := range set.WorkDirs {
		if to, ok := maps[CleanForeign(w.Abs)]; ok {
			matched[w.ID] = to
			continue
		}
		if home != "" && safeRel(w.HomeRel) {
			if p := filepath.Join(home, filepath.FromSlash(w.HomeRel)); isDir(p) {
				matched[w.ID] = p
				continue
			}
		}
		if home != "" && w.HomeRel == "." {
			matched[w.ID] = home
			continue
		}
		if home != "" && safeRel(w.Label) && w.Label != "." && !strings.Contains(w.Label, "/") {
			if p := filepath.Join(home, w.Label); isDir(p) {
				matched[w.ID] = p
				continue
			}
		}
		unmatched = append(unmatched, w)
	}
	return matched, unmatched
}

// Suggest returns up to three top-level folders of home whose names are
// closest to the work dir's label, best first. Hidden folders are left out.
func Suggest(home string, w WorkDir) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	label := strings.ToLower(w.Label)
	type scored struct {
		path string
		d    int
	}
	var all []scored
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := strings.ToLower(e.Name())
		d := distance(label, name)
		if label != "" && (strings.Contains(name, label) || strings.Contains(label, name)) {
			d = 0
		}
		all = append(all, scored{filepath.Join(home, e.Name()), d})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].path < all[j].path
	})
	var out []string
	for i := 0; i < len(all) && i < 3; i++ {
		out = append(out, all[i].path)
	}
	return out
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// skipDirs are never scanned for checkouts.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

// Scan returns every git checkout under roots, at most maxDepth levels down.
// It does not follow symlinks, skips hidden folders, node_modules and
// vendor, and does not look inside a checkout it found.
func Scan(roots []string, maxDepth int) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
			return
		}
		if depth >= maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	for _, r := range roots {
		if info, err := os.Lstat(r); err == nil && info.IsDir() {
			walk(filepath.Clean(r), 0)
		}
	}
	sort.Strings(out)
	return out
}

// Identify turns scanned paths into candidates. A path that is not the top
// of its own checkout is dropped.
func Identify(ctx context.Context, paths []string, identify Identifier) []Candidate {
	if identify == nil {
		identify = DefaultIdentifier
	}
	var out []Candidate
	for _, p := range paths {
		r, err := identify(ctx, p)
		if err != nil || !sameDir(r.Root, p) {
			continue
		}
		out = append(out, Candidate{Path: p, Remote: r.Remote, RootCommits: r.RootCommits})
	}
	return out
}

// Score rates one candidate for one project, with the reason. A project and
// a candidate that both have root commits and share none are different
// repositories whatever their remotes say.
func Score(p Project, c Candidate, workDirTarget string) (int, string) {
	shared := sharesRoot(p.RootCommits, c.RootCommits)
	if len(p.RootCommits) > 0 && len(c.RootCommits) > 0 && !shared {
		return 0, "no shared root commit"
	}
	score, reason := 0, ""
	switch {
	case p.GitRemote != "" && p.GitRemote == c.Remote:
		score, reason = 100, "same remote "+p.GitRemote
	case shared && (p.GitRemote == "" || c.Remote == ""):
		score, reason = 80, "same root commit"
	case shared:
		score, reason = 30, "same root commit, different remote (fork)"
	default:
		return 0, "no shared identity"
	}
	if workDirTarget != "" && safeRel(p.Rel) && filepath.Clean(c.Path) == filepath.Join(workDirTarget, filepath.FromSlash(p.Rel)) {
		score += 20
		reason += ", same place in work dir"
	}
	if b := baseName(p.Abs); b != "" && b == filepath.Base(c.Path) {
		score += 10
		reason += ", same name"
	}
	return score, reason
}

// baseName is the last element of a path written on either OS.
func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func sharesRoot(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// MatchProjects proposes a target for each project that has an identity.
// The best candidate at or above MatchThreshold wins; two or more at the
// top score make the proposal ambiguous. Projects with no candidate at the
// threshold are left out: they keep the path rewrite they had before.
func MatchProjects(set Set, workDirTargets map[string]string, cands []Candidate) []Proposal {
	var out []Proposal
	for _, p := range set.Projects {
		if p.GitRemote == "" && len(p.RootCommits) == 0 {
			continue
		}
		best, reason := 0, ""
		var top []string
		for _, c := range cands {
			s, r := Score(p, c, workDirTargets[p.WorkDir])
			switch {
			case s < MatchThreshold:
			case s > best:
				best, reason, top = s, r, []string{c.Path}
			case s == best:
				top = append(top, c.Path)
			}
		}
		switch len(top) {
		case 0:
			continue
		case 1:
			out = append(out, Proposal{Project: p, Target: top[0], Score: best, Reason: reason})
		default:
			out = append(out, Proposal{Project: p, Score: best, Reason: fmt.Sprintf("%d checkouts tie at score %d", len(top), best), Choices: top})
		}
	}
	return out
}

func isDir(p string) bool {
	info, err := os.Stat(p) // #nosec G703 -- metadata only: whether a candidate place on this machine is a directory
	return err == nil && info.IsDir()
}
