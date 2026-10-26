package cmd

import (
	"github.com/nikolareljin/agentvault/internal/config"

	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// pinHome points the home directory at an empty folder, so a test's temp
// dirs are outside it on every OS (on Windows they sit under USERPROFILE).
func pinHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestDeclineWorkDirRepoScopeSettings(t *testing.T) {
	pinHome(t)
	work := filepath.Join(t.TempDir(), "work")
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, repo := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(work, repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha/.claude/settings.json")
	write("alpha/.mcp.json")
	write("beta/.gemini/settings.json")
	write("gamma/.claude/settings.json") // gamma is the --project: listed there
	write("notrepo/.claude/settings.json")
	write(".claude/settings.json")

	got := declineWorkDirRepoScopeSettings([]string{work, work}, filepath.Join(work, "gamma"))
	var paths []string
	for _, d := range got {
		if d.Category != DeclinedRepoScope || d.Reason == "" {
			t.Errorf("%s: category %q reason %q", d.Path, d.Category, d.Reason)
		}
		paths = append(paths, d.Path)
	}
	want := []string{
		"work/.claude/settings.json",
		"work/alpha/.claude/settings.json",
		"work/alpha/.mcp.json",
		"work/beta/.gemini/settings.json",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	// The same project named through a symlink is still skipped.
	link := filepath.Join(t.TempDir(), "gamma-link")
	if err := os.Symlink(filepath.Join(work, "gamma"), link); err == nil {
		for _, d := range declineWorkDirRepoScopeSettings([]string{work}, link) {
			if d.Path == "work/gamma/.claude/settings.json" {
				t.Fatalf("project named through a symlink listed again: %+v", d)
			}
		}
	}
	if got := declineWorkDirRepoScopeSettings(nil, ""); len(got) != 0 {
		t.Fatalf("no work dirs: got %v", got)
	}
}

func TestCollectSetupBundleListsWorkDirRepoSettings(t *testing.T) {
	v, root := sessionTestVault(t, nil)
	pinHome(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Join(work, "alpha", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "alpha", ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "alpha", ".claude", "settings.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := collectSetupBundle(exportCmd, v, filepath.Join(root, config.AppName), setupCollectOptions{WorkDirs: []string{work}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Bundle.Declined {
		if d.Path == "work/alpha/.claude/settings.json" && d.Category == DeclinedRepoScope {
			found = true
		}
	}
	if !found {
		t.Fatalf("declined = %+v, want work/alpha/.claude/settings.json", res.Bundle.Declined)
	}
	for _, f := range res.Bundle.ProjectFiles {
		if filepath.Base(f.LogicalPath) == "settings.json" {
			t.Fatalf("repository settings exported: %+v", f)
		}
	}
}

func TestWorkDirLabel(t *testing.T) {
	home := t.TempDir()
	cases := []struct{ abs, want string }{
		{filepath.Join(home, "a", "Projects"), "a/Projects"},
		{filepath.Join(home, "b", "Projects"), "b/Projects"},
		{filepath.Join(home, "Projects"), "Projects"},
		{filepath.Join(filepath.Dir(home), "elsewhere", "Projects"), "Projects"},
		{home, filepath.Base(home)},
	}
	for _, c := range cases {
		if got := workDirLabel(c.abs, home); got != c.want {
			t.Errorf("workDirLabel(%q) = %q, want %q", c.abs, got, c.want)
		}
	}
	if got := workDirLabel(filepath.Join(home, "x"), ""); got != "x" {
		t.Errorf("no home: %q", got)
	}
}
