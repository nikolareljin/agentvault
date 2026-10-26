package locations

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, n := range []string{"personal", "company-a", "a", "0", strings.Repeat("a", 32)} {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range []string{"", "-a", "Personal", "a/b", `a\b`, "c:", "a b", "a_b", "a.b", strings.Repeat("a", 33), "caf\u00e9"} {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
}

func TestDeclareRefusesOverlap(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, filepath.Join(root, "Projects", "client"), filepath.Join(root, "Projects-acme"), filepath.Join(root, "work", "a"))
	s, err := Set{}.Declare("personal", filepath.Join(root, "Projects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, rel, want string }{
		{"same", "Projects", "overlaps work dir personal"},
		{"inside", "Projects/client", "overlaps work dir personal"},
		{"around", ".", "overlaps work dir personal"},
		{"personal", "work/a", "already declared"},
		{"Bad", "work/a", "use lowercase"},
		{"all", "work/a", "reserved"},
	} {
		if _, err := s.Declare(c.name, filepath.Join(root, c.rel)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Declare(%s, %s) = %v, want %q", c.name, c.rel, err, c.want)
		}
	}
	if _, err := s.Declare("missing", filepath.Join(root, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Declare(missing) = %v, want a not-exist error", err)
	}
	// A sibling whose name shares a prefix is not nested.
	s2, err := s.Declare("acme", filepath.Join(root, "Projects-acme"))
	if err != nil {
		t.Fatalf("sibling refused: %v", err)
	}
	if len(s2.Named) != 2 || len(s.Named) != 1 {
		t.Fatalf("Declare changed its receiver or lost a name: %v / %v", s.Named, s2.Named)
	}
}

func TestDeclareRefusesNotDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Set{}).Declare("x", f); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("got %v", err)
	}
}

func TestDeclareRefusesOverlapThroughSymlink(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, filepath.Join(root, "real", "inner"))
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	s, err := Set{}.Declare("a", filepath.Join(root, "real"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Declare("b", filepath.Join(link, "inner")); err == nil {
		t.Fatal("a path inside a declared work dir through a symlink was accepted")
	}
}

func TestUndeclareAndMergeKeepNames(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, filepath.Join(root, "a"), filepath.Join(root, "b"))
	s, _ := Set{}.Declare("a", filepath.Join(root, "a"))
	s, _ = s.Declare("b", filepath.Join(root, "b"))

	// Every vault write goes through Merge; a name must survive it.
	m := Merge(s, Set{WorkDirs: []WorkDir{{ID: "w1", Abs: "/x"}}})
	if len(m.Named) != 2 || m.Named[0].Name != "a" || m.Named[1].Name != "b" {
		t.Fatalf("Merge dropped names: %v", m.Named)
	}
	if (&Set{Named: s.Named}).Empty() {
		t.Fatal("a set with only names reports Empty")
	}

	u, ok := s.Undeclare("a")
	if !ok || len(u.Named) != 1 || u.Named[0].Name != "b" || len(s.Named) != 2 {
		t.Fatalf("Undeclare: ok=%v %v (receiver %v)", ok, u.Named, s.Named)
	}
	if _, ok := u.Undeclare("a"); ok {
		t.Fatal("Undeclare of an unknown name reported found")
	}
}

// On Windows C:\Users\Me\Projects and c:\users\me\projects are one folder.
func TestDeclareRefusesOverlapWithoutCase(t *testing.T) {
	old := caseInsensitivePaths
	t.Cleanup(func() { caseInsensitivePaths = old })
	root := t.TempDir()
	mkdirs(t, filepath.Join(root, "Work", "a"), filepath.Join(root, "work", "b"))
	s, err := Set{}.Declare("w", filepath.Join(root, "Work"))
	if err != nil {
		t.Fatal(err)
	}
	// Work and work are two folders only on a case-sensitive filesystem.
	upper, _ := os.Stat(filepath.Join(root, "Work"))
	lower, _ := os.Stat(filepath.Join(root, "work"))
	if !os.SameFile(upper, lower) {
		caseInsensitivePaths = false
		if _, err := s.Declare("b", filepath.Join(root, "work", "b")); err != nil {
			t.Fatalf("case-sensitive: distinct folders refused: %v", err)
		}
	}
	caseInsensitivePaths = true
	if _, err := s.Declare("b", filepath.Join(root, "work", "b")); err == nil {
		t.Fatal("case-insensitive: a path inside a declared work dir by case was accepted")
	}
}
