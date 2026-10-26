// Package locations records where projects live in a way another machine can
// resolve: each project names its work dir (a folder of projects, such as
// ~/Projects) and its git identity, not only its absolute path. On import the
// same project is found again by identity, wherever it is checked out.
package locations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/gitid"
)

// WorkDir is a folder that holds projects.
type WorkDir struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	// HomeRel is the path relative to the exporting home, slash-separated:
	// "." for home itself, empty when the work dir is outside home.
	HomeRel string `json:"home_rel,omitempty"`
	Abs     string `json:"abs"`
}

// Project is one checkout.
type Project struct {
	ID          string   `json:"id"`
	WorkDir     string   `json:"work_dir,omitempty"`
	Rel         string   `json:"rel,omitempty"`
	Abs         string   `json:"abs"`
	GitRemote   string   `json:"git_remote,omitempty"`
	RootCommits []string `json:"root_commits,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	// Aliases are other absolute paths this repository was known by, such
	// as its path on the machine a bundle came from.
	Aliases []string `json:"aliases,omitempty"`
}

// Set is every work dir and project a bundle or vault knows.
type Set struct {
	WorkDirs []WorkDir `json:"work_dirs,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	// WorkDirMaps are mappings the user chose, bundle work dir path to a
	// directory here. Only the vault keeps them; later imports reuse them.
	WorkDirMaps map[string]string `json:"work_dir_maps,omitempty"`
	// ConfirmedHidden are saved mappings to a hidden folder that the user
	// confirmed on this machine, keyed by bundle work dir path. A
	// confirmation holds only while WorkDirMaps maps the same path to the
	// same target, and only for the projects it lists. Only the vault keeps
	// them, never a bundle.
	ConfirmedHidden map[string]HiddenConfirm `json:"confirmed_hidden,omitempty"`
	// Named are the work dirs declared by name on this machine. Only the
	// vault keeps them, never a bundle.
	Named []Named `json:"named_work_dirs,omitempty"`
}

// HiddenConfirm is one confirmed hidden mapping: its target and the keys
// (ProjectKey) of the projects that were under the work dir when the user
// said yes. A bundle path is only a name another bundle can reuse, so a
// project not listed here is asked about again.
type HiddenConfirm struct {
	Target   string   `json:"target"`
	Projects []string `json:"projects,omitempty"`
}

// UnmarshalJSON also reads the older form, a bare target string, which
// lists no projects and so confirms nothing.
func (h *HiddenConfirm) UnmarshalJSON(b []byte) error {
	var target string
	if err := json.Unmarshal(b, &target); err == nil {
		*h = HiddenConfirm{Target: target}
		return nil
	}
	type plain HiddenConfirm
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*h = HiddenConfirm(p)
	return nil
}

// Covers reports whether every key is listed. No keys covers nothing.
func (h HiddenConfirm) Covers(keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, k := range h.Projects {
		have[k] = true
	}
	for _, k := range keys {
		if !have[k] {
			return false
		}
	}
	return true
}

// CanonicalKey brings a ProjectKey, with or without a "#subdir" suffix,
// stored by an earlier version up to date (see gitid.CanonicalStored).
func CanonicalKey(key string) string {
	rest, ok := strings.CutPrefix(key, "remote:")
	if !ok {
		return key
	}
	remote, sub, hasSub := strings.Cut(rest, "#")
	key = "remote:" + gitid.CanonicalStored(remote)
	if hasSub {
		key += "#" + sub
	}
	return key
}

// Canonicalize brings the remotes and project keys in s, stored by an
// earlier version, up to date. Projects that then share an identity (an
// SSH and an HTTPS clone of one Azure DevOps repository) are folded into
// one, the other paths kept as aliases, as Merge does.
func (s *Set) Canonicalize() {
	changed := false
	for i := range s.Projects {
		if c := gitid.CanonicalStored(s.Projects[i].GitRemote); c != s.Projects[i].GitRemote {
			s.Projects[i].GitRemote = c
			changed = true
		}
	}
	for k, c := range s.ConfirmedHidden {
		for i := range c.Projects {
			c.Projects[i] = CanonicalKey(c.Projects[i])
		}
		s.ConfirmedHidden[k] = c
	}
	if changed {
		*s = Merge(*s, Set{})
	}
}

// ProjectKey is a project's identity for a hidden confirmation: its remote,
// else its root commits, else its name in the work dir.
func ProjectKey(p Project) string {
	switch {
	case p.GitRemote != "":
		return "remote:" + p.GitRemote
	case len(p.RootCommits) > 0:
		roots := append([]string(nil), p.RootCommits...)
		sort.Strings(roots)
		return "roots:" + strings.Join(roots, ",")
	}
	return "rel:" + p.Rel
}

// Empty reports whether the set names nothing.
func (s *Set) Empty() bool {
	return s == nil || len(s.WorkDirs) == 0 && len(s.Projects) == 0 && len(s.WorkDirMaps) == 0 && len(s.ConfirmedHidden) == 0 && len(s.Named) == 0
}

// Identifier is gitid.Identify; tests replace it.
type Identifier func(ctx context.Context, dir string) (gitid.Repo, error)

// DefaultIdentifier runs gitid.Identify with the hardened git runner.
func DefaultIdentifier(ctx context.Context, dir string) (gitid.Repo, error) {
	return gitid.Identify(ctx, dir, nil)
}

// Capture records the given project directories and work dirs. A project's
// work dir is the longest given work dir that holds it, else its parent. A
// project that is not a git checkout, or no longer exists, keeps its path
// only.
func Capture(ctx context.Context, home string, projectDirs, workDirs []string, identify Identifier) Set {
	if identify == nil {
		identify = DefaultIdentifier
	}
	b := builder{home: cleanAbs(home)}
	for _, w := range workDirs {
		if w = cleanAbs(w); w != "" {
			b.workDir(w)
		}
	}
	for _, p := range projectDirs {
		p = cleanAbs(p)
		if p == "" || b.hasProject(p) {
			continue
		}
		wd := ""
		for _, w := range b.set.WorkDirs {
			if within(w.Abs, p) && p != w.Abs && len(w.Abs) > len(wd) {
				wd = w.Abs
			}
		}
		if wd == "" {
			wd = filepath.Dir(p)
		}
		proj := Project{Abs: p, WorkDir: b.workDir(wd).ID}
		if rel, err := filepath.Rel(wd, p); err == nil {
			proj.Rel = filepath.ToSlash(rel)
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			if r, err := identify(ctx, p); err == nil && sameDir(r.Root, p) {
				proj.GitRemote = r.Remote
				proj.RootCommits = r.RootCommits
				proj.Namespace = namespace(r.Remote)
			}
		}
		proj.ID = fmt.Sprintf("p%d", len(b.set.Projects)+1)
		b.set.Projects = append(b.set.Projects, proj)
	}
	return b.set
}

type builder struct {
	home string
	set  Set
}

func (b *builder) workDir(abs string) WorkDir {
	for _, w := range b.set.WorkDirs {
		if w.Abs == abs {
			return w
		}
	}
	w := WorkDir{ID: fmt.Sprintf("w%d", len(b.set.WorkDirs)+1), Abs: abs, Label: filepath.Base(abs)}
	if b.home != "" && within(b.home, abs) {
		// "." is home itself.
		if rel, err := filepath.Rel(b.home, abs); err == nil {
			w.HomeRel = filepath.ToSlash(rel)
		}
	}
	b.set.WorkDirs = append(b.set.WorkDirs, w)
	return w
}

func (b *builder) hasProject(abs string) bool {
	for _, p := range b.set.Projects {
		if p.Abs == abs {
			return true
		}
	}
	return false
}

// Merge adds the work dirs and projects of other that s does not have (by
// absolute path), renumbering their ids. A project from other already known
// by identity replaces the old path, so the vault keeps one entry per repo.
func Merge(s, other Set) Set {
	out := Set{}
	add := func(w WorkDir) string {
		for _, x := range out.WorkDirs {
			if x.Abs == w.Abs {
				return x.ID
			}
		}
		w.ID = fmt.Sprintf("w%d", len(out.WorkDirs)+1)
		out.WorkDirs = append(out.WorkDirs, w)
		return w.ID
	}
	byID := func(set Set, id string) (WorkDir, bool) {
		for _, w := range set.WorkDirs {
			if w.ID == id {
				return w, true
			}
		}
		return WorkDir{}, false
	}
	var projects []Project
	put := func(set Set, p Project) {
		if w, ok := byID(set, p.WorkDir); ok {
			p.WorkDir = add(w)
		} else {
			p.WorkDir = ""
		}
		for i, x := range projects {
			if x.Abs == p.Abs || sameIdentity(x, p) {
				p.Aliases = aliasesOf(x, p)
				projects[i] = p
				return
			}
		}
		projects = append(projects, p)
	}
	for _, w := range s.WorkDirs {
		add(w)
	}
	for _, p := range s.Projects {
		put(s, p)
	}
	for _, w := range other.WorkDirs {
		add(w)
	}
	for _, p := range other.Projects {
		put(other, p)
	}
	for i := range projects {
		projects[i].ID = fmt.Sprintf("p%d", i+1)
	}
	out.Projects = projects
	for _, m := range []map[string]string{s.WorkDirMaps, other.WorkDirMaps} {
		for k, v := range m {
			if out.WorkDirMaps == nil {
				out.WorkDirMaps = map[string]string{}
			}
			out.WorkDirMaps[k] = v
		}
	}
	// A confirmation is for one (path, target) pair: a mapping changed to
	// another target, or removed, drops it.
	// For the same target the project lists add up: each yes extends it.
	for _, m := range []map[string]HiddenConfirm{s.ConfirmedHidden, other.ConfirmedHidden} {
		for k, v := range m {
			if out.ConfirmedHidden == nil {
				out.ConfirmedHidden = map[string]HiddenConfirm{}
			}
			if old, ok := out.ConfirmedHidden[k]; ok && old.Target == v.Target {
				v.Projects = unionStrings(old.Projects, v.Projects)
			}
			out.ConfirmedHidden[k] = v
		}
	}
	for k, v := range out.ConfirmedHidden {
		if out.WorkDirMaps[k] != v.Target {
			delete(out.ConfirmedHidden, k)
		}
	}
	if len(out.ConfirmedHidden) == 0 {
		out.ConfirmedHidden = nil
	}
	// A name declared in other replaces the same name in s.
	for _, n := range append(append([]Named(nil), s.Named...), other.Named...) {
		if i := namedIndex(out.Named, n.Name); i >= 0 {
			out.Named[i] = n
		} else {
			out.Named = append(out.Named, n)
		}
	}
	return out
}

func namedIndex(ns []Named, name string) int {
	for i, n := range ns {
		if n.Name == name {
			return i
		}
	}
	return -1
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// aliasesOf is every path old and new were known by, other than new.Abs.
func aliasesOf(old, new Project) []string {
	var out []string
	seen := map[string]bool{new.Abs: true}
	for _, a := range append(append([]string{old.Abs}, old.Aliases...), new.Aliases...) {
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// FindByIdentity returns the project with the same identity as r, if any.
func (s Set) FindByIdentity(remote string, roots []string) (Project, bool) {
	probe := Project{GitRemote: remote, RootCommits: roots}
	for _, p := range s.Projects {
		if (remote != "" || len(roots) > 0) && sameIdentity(p, probe) {
			return p, true
		}
	}
	return Project{}, false
}

// sameIdentity is two records with the same remote, or with no remote and
// the same root commits.
func sameIdentity(a, b Project) bool {
	if a.GitRemote != "" || b.GitRemote != "" {
		return a.GitRemote == b.GitRemote
	}
	return len(a.RootCommits) > 0 && strings.Join(a.RootCommits, ",") == strings.Join(b.RootCommits, ",")
}

// namespace is the remote path without host and repository name.
func namespace(remote string) string {
	parts := strings.Split(remote, "/")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[1:len(parts)-1], "/")
}

func cleanAbs(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// within reports whether p is dir or below it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// CleanForeign cleans a path that may come from another OS without
// converting it to this OS's form: a bundle path /home/a/x stays
// forward-slashed on Windows, so it still matches the text it rewrites.
// Trailing separators are removed. A Windows-style path (drive letter or
// backslash) is only trimmed.
func CleanForeign(p string) string {
	p = strings.TrimSpace(p)
	if strings.Contains(p, `\`) || len(p) >= 2 && p[1] == ':' {
		return strings.TrimRight(p, `/\`)
	}
	if p == "" {
		return ""
	}
	return strings.TrimRight(path.Clean(p), "/")
}

// foreignKey is CleanForeign with Windows paths compared without case and
// with forward slashes, for overlap checks between paths of any OS.
func foreignKey(p string) string {
	c := CleanForeign(p)
	if strings.Contains(c, `\`) || len(c) >= 2 && c[1] == ':' {
		c = strings.ToLower(strings.ReplaceAll(c, `\`, "/"))
	}
	return c
}

// Overlaps reports whether a and b are the same path or one is below the
// other, comparing paths of any OS.
func Overlaps(a, b string) bool {
	ka, kb := foreignKey(a), foreignKey(b)
	if ka == "" || kb == "" {
		return false
	}
	return ka == kb || strings.HasPrefix(kb, ka+"/") || strings.HasPrefix(ka, kb+"/")
}

// Below reports whether p is dir or below it, comparing paths of any OS.
func Below(dir, p string) bool {
	kd, kp := foreignKey(dir), foreignKey(p)
	return kd != "" && kp != "" && (kp == kd || strings.HasPrefix(kp, kd+"/"))
}

// HiddenBelowHome reports whether p is under home and some folder between
// home and p (or p itself) has a name starting with ".".
func HiddenBelowHome(p, home string) bool {
	if home == "" {
		return false
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// SameIdentity reports whether two records name one repository: the same
// normalized remote, or no remote on either and a shared root commit set.
func SameIdentity(a, b Project) bool {
	return sameIdentity(a, b)
}

// safeRel is a bundle-supplied relative path that stays where it is joined:
// not absolute, no "..", no drive or backslash tricks.
func safeRel(rel string) bool {
	if rel == "" || strings.ContainsAny(rel, "\\:\x00") || strings.HasPrefix(rel, "/") {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(rel))
}
