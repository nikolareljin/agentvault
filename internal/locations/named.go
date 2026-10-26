package locations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Named is a work dir declared by name, with its path on this machine: a
// personal ~/Projects, one per employer or client. Only the vault keeps
// them; on another machine the same name is placed by asking.
type Named struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// AllWorkDirs is the selection that takes every declared work dir; no work
// dir may have it as its name.
const AllWorkDirs = "all"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidName reports whether name can name a work dir: lowercase letters,
// digits and '-', at most 32, not starting with '-'. It has no '/', '\' or
// ':', so NAME=PATH never reads as an absolute OLD=NEW mapping.
func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

// Lookup returns the work dir declared as name.
func (s Set) Lookup(name string) (Named, bool) {
	for _, n := range s.Named {
		if n.Name == name {
			return n, true
		}
	}
	return Named{}, false
}

// Declare returns s with name declared at dir. dir must be an existing
// directory. A name is declared once, and two declared paths may not be the
// same or one inside the other: nested trees would leave it unclear which
// work dir a project's memory belongs to.
func (s Set) Declare(name, dir string) (Set, error) {
	if !ValidName(name) {
		return s, fmt.Errorf("work dir name %q: use lowercase letters, digits and '-' (at most 32, not starting with '-')", name)
	}
	if name == AllWorkDirs {
		return s, fmt.Errorf("work dir name %q is reserved: export --workdirs %s means every work dir", name, AllWorkDirs)
	}
	if _, ok := s.Lookup(name); ok {
		return s, fmt.Errorf("work dir %s is already declared; remove it first", name)
	}
	abs := cleanAbs(dir)
	if abs == "" {
		return s, fmt.Errorf("work dir %s: no path given", name)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return s, fmt.Errorf("work dir %s: %w", name, err)
	}
	if !info.IsDir() {
		return s, fmt.Errorf("work dir %s: %s is not a directory", name, abs)
	}
	for _, n := range s.Named {
		if overlaps(n.Path, abs) {
			return s, fmt.Errorf("work dir %s: %s overlaps work dir %s at %s; declared work dirs may not be the same or nested", name, abs, n.Name, n.Path)
		}
	}
	out := s
	out.Named = append(append([]Named(nil), s.Named...), Named{Name: name, Path: abs})
	return out, nil
}

// Undeclare returns s without name, and whether it was declared.
func (s Set) Undeclare(name string) (Set, bool) {
	out := s
	out.Named = nil
	found := false
	for _, n := range s.Named {
		if n.Name == name {
			found = true
			continue
		}
		out.Named = append(out.Named, n)
	}
	return out, found
}

// overlaps reports whether a and b are the same directory or one holds the
// other, comparing both the paths as given and their symlink-resolved forms
// (on macOS /var is /private/var), without case on Windows and macOS.
func overlaps(a, b string) bool {
	as, bs := forms(a), forms(b)
	for _, x := range as {
		for _, y := range bs {
			if within(x, y) || within(y, x) {
				return true
			}
		}
	}
	return false
}

func forms(p string) []string {
	out := []string{foldCase(filepath.Clean(p))}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		out = append(out, foldCase(filepath.Clean(r)))
	}
	return out
}

// caseInsensitivePaths is whether this OS compares paths without case
// (Windows, and macOS on its default volumes); tests set it.
var caseInsensitivePaths = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

func foldCase(p string) string {
	if caseInsensitivePaths {
		return strings.ToLower(p)
	}
	return p
}

// Holds reports whether p is the work dir or below it, compared as for
// Declare: as given and symlink-resolved, without case on Windows and macOS.
func (n Named) Holds(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	for _, x := range forms(n.Path) {
		for _, y := range forms(p) {
			if within(x, y) {
				return true
			}
		}
	}
	return false
}

// Roots returns the work dir's path as declared and, when it differs, with
// symlinks resolved: tools record a directory in either form.
func (n Named) Roots() []string {
	out := []string{filepath.Clean(n.Path)}
	if r, err := filepath.EvalSymlinks(n.Path); err == nil && filepath.Clean(r) != out[0] {
		out = append(out, filepath.Clean(r))
	}
	return out
}
