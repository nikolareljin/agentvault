package locations

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/gitid"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func fakeIdentify(ids map[string]gitid.Repo) Identifier {
	return func(_ context.Context, dir string) (gitid.Repo, error) {
		r, ok := ids[dir]
		if !ok {
			return gitid.Repo{}, gitid.ErrNotRepo
		}
		r.Root = dir
		return r, nil
	}
}

func TestCaptureRecordsWorkDirAndIdentity(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Projects", "client", "app")
	plain := filepath.Join(home, "notes")
	mkdirs(t, app, plain)
	ids := map[string]gitid.Repo{app: {Remote: "github.com/acme/app", RootCommits: []string{"r1"}}}
	set := Capture(context.Background(), home, []string{app, plain, filepath.Join(home, "gone")}, []string{filepath.Join(home, "Projects")}, fakeIdentify(ids))
	if len(set.Projects) != 3 {
		t.Fatalf("projects: %+v", set.Projects)
	}
	p := set.Projects[0]
	if p.Rel != "client/app" || p.GitRemote != "github.com/acme/app" || p.Namespace != "acme" || p.WorkDir != "w1" {
		t.Errorf("app: %+v", p)
	}
	if set.WorkDirs[0].HomeRel != "Projects" || set.WorkDirs[0].Label != "Projects" {
		t.Errorf("work dir: %+v", set.WorkDirs[0])
	}
	if set.Projects[1].GitRemote != "" || set.Projects[1].Rel != "notes" {
		t.Errorf("plain: %+v", set.Projects[1])
	}
	if w := set.WorkDirs[1]; w.HomeRel != "." {
		t.Errorf("home as work dir: %+v", w)
	}
}

func TestResolveWorkDirs(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, filepath.Join(home, "Projects"), filepath.Join(home, "code"), filepath.Join(home, "src"))
	set := Set{WorkDirs: []WorkDir{
		{ID: "w1", Label: "Projects", HomeRel: "Projects", Abs: "/home/a/Projects"},
		{ID: "w2", Label: "code", HomeRel: "dev/code", Abs: "/home/a/dev/code"},
		{ID: "w3", Label: "work", HomeRel: "work", Abs: "/home/a/work"},
		{ID: "w4", Label: "x", HomeRel: "../../etc", Abs: "/home/a/x"},
		{ID: "w5", Label: "..", HomeRel: "", Abs: "/srv"},
		{ID: "w6", Label: "home", HomeRel: ".", Abs: "/home/a"},
		{ID: "w7", Label: "Elsewhere", HomeRel: "Elsewhere", Abs: "/home/a/Elsewhere"},
		{ID: "w8", Label: ".", Abs: "/home/a/dot"},
	}}
	maps := map[string]string{"/home/a/Elsewhere": filepath.Join(home, "src")}
	got, unmatched := ResolveWorkDirs(set, home, maps)
	want := map[string]string{
		"w1": filepath.Join(home, "Projects"),
		"w2": filepath.Join(home, "code"),
		"w6": home,
		"w7": filepath.Join(home, "src"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matched %v, want %v", got, want)
	}
	var ids []string
	for _, w := range unmatched {
		ids = append(ids, w.ID)
	}
	if strings.Join(ids, ",") != "w3,w4,w5,w8" {
		t.Errorf("unmatched %v", ids)
	}
}

func TestSuggestRanksClosestTopLevelFolders(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, filepath.Join(home, "projects-old"), filepath.Join(home, "Music"), filepath.Join(home, ".Projects"), filepath.Join(home, "Pictures"), filepath.Join(home, "Documents"))
	got := Suggest(home, WorkDir{Label: "Projects"})
	if len(got) != 3 || got[0] != filepath.Join(home, "projects-old") {
		t.Fatalf("got %v", got)
	}
	for _, g := range got {
		if strings.HasPrefix(filepath.Base(g), ".") {
			t.Errorf("hidden folder suggested: %s", g)
		}
	}
}

func TestScanSkipsLinksHiddenAndVendored(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mkdirs(t,
		filepath.Join(root, "a", ".git"),
		filepath.Join(root, "a", "nested", ".git"),
		filepath.Join(root, "g", "b", ".git"),
		filepath.Join(root, "node_modules", "c", ".git"),
		filepath.Join(root, ".hidden", "d", ".git"),
		filepath.Join(root, "1", "2", "3", "4", ".git"),
		filepath.Join(outside, "e", ".git"),
	)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got := Scan([]string{root}, 3)
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "g", "b")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestScoreAndMatch(t *testing.T) {
	wd := "/h/src"
	p := Project{ID: "p1", WorkDir: "w1", Rel: "app", Abs: "/old/Projects/app", GitRemote: "github.com/acme/app", RootCommits: []string{"r1"}}
	cases := []struct {
		c    Candidate
		want int
	}{
		{Candidate{Path: "/h/src/app", Remote: "github.com/acme/app", RootCommits: []string{"r1"}}, 130},
		{Candidate{Path: "/h/other/app2", Remote: "github.com/acme/app"}, 100},
		{Candidate{Path: "/h/x/app", Remote: "github.com/fork/app", RootCommits: []string{"r1"}}, 40},
		{Candidate{Path: "/h/x/y", RootCommits: []string{"r1"}}, 80},
		// Same remote claimed, different history: not the same repository.
		{Candidate{Path: "/h/src/app", Remote: "github.com/acme/app", RootCommits: []string{"r2"}}, 0},
		{Candidate{Path: "/h/src/app"}, 0},
	}
	for _, c := range cases {
		if got, _ := Score(p, c.c, wd); got != c.want {
			t.Errorf("Score(%+v) = %d, want %d", c.c, got, c.want)
		}
	}
	set := Set{Projects: []Project{p, {ID: "p2", Abs: "/old/x"}, {ID: "p3", Abs: "/old/lib", GitRemote: "gitlab.com/g/lib"}}}
	cands := []Candidate{
		{Path: "/h/src/app", Remote: "github.com/acme/app"},
		{Path: "/h/a/lib", Remote: "gitlab.com/g/lib"},
		{Path: "/h/b/lib", Remote: "gitlab.com/g/lib"},
	}
	got := MatchProjects(set, map[string]string{"w1": wd}, cands)
	if len(got) != 2 {
		t.Fatalf("proposals %+v", got)
	}
	if got[0].Target != "/h/src/app" || got[0].Score != 130 {
		t.Errorf("app: %+v", got[0])
	}
	if got[1].Target != "" || len(got[1].Choices) != 2 {
		t.Errorf("lib should be ambiguous: %+v", got[1])
	}
}

func TestMergeKeepsOneEntryPerRepository(t *testing.T) {
	a := Set{WorkDirs: []WorkDir{{ID: "w1", Abs: "/h/P"}}, Projects: []Project{{ID: "p1", WorkDir: "w1", Abs: "/h/P/app", GitRemote: "github.com/o/app"}}}
	b := Set{WorkDirs: []WorkDir{{ID: "w1", Abs: "/h/src"}}, Projects: []Project{{ID: "p1", WorkDir: "w1", Abs: "/h/src/app", GitRemote: "github.com/o/app"}, {ID: "p2", WorkDir: "w1", Abs: "/h/src/b"}}}
	m := Merge(a, b)
	if len(m.Projects) != 2 || m.Projects[0].Abs != "/h/src/app" || m.Projects[0].WorkDir != "w2" || len(m.WorkDirs) != 2 {
		t.Fatalf("merged %+v", m)
	}
	if a := m.Projects[0].Aliases; len(a) != 1 || a[0] != "/h/P/app" {
		t.Errorf("old path not kept as alias: %v", a)
	}
	if _, ok := m.FindByIdentity("github.com/o/app", nil); !ok {
		t.Error("FindByIdentity missed")
	}
	if _, ok := m.FindByIdentity("", nil); ok {
		t.Error("empty identity matched")
	}
}

func TestSafeRel(t *testing.T) {
	for rel, want := range map[string]bool{
		"app": true, "a/b": true, "": false, "..": false, "a/../../b": false,
		"/etc": false, `a\b`: false, "C:x": false, "a\x00": false,
	} {
		if got := safeRel(rel); got != want {
			t.Errorf("safeRel(%q) = %v", rel, got)
		}
	}
}

func dialog(t *testing.T, rows []Row, home, input string) ([]Row, string, error) {
	t.Helper()
	var out strings.Builder
	got, err := Confirm(rows, home, bufio.NewReader(strings.NewReader(input)), &out)
	return got, out.String(), err
}

func TestConfirmDialog(t *testing.T) {
	home := t.TempDir()
	alt := filepath.Join(home, "alt")
	mkdirs(t, alt)
	rows := []Row{
		{From: "/a/app", To: "/h/app", Note: "score 100"},
		{From: "/a/lib", Choices: []string{"/h/1/lib", "/h/2/lib"}},
	}
	got, out, err := dialog(t, rows, home, "\n")
	if err != nil || got[0].To != "/h/app" || got[1].To != "" {
		t.Fatalf("accept all: %+v %v", got, err)
	}
	if !strings.Contains(out, "1. /a/app -> /h/app") || !strings.Contains(out, "(choose one)") || !strings.Contains(out, "b) /h/2/lib") {
		t.Errorf("listing:\n%s", out)
	}
	got, _, err = dialog(t, rows, home, "2\nb\n1\n~/alt\n\n")
	if err != nil || got[0].To != alt || got[1].To != "/h/2/lib" {
		t.Fatalf("edit: %+v %v", got, err)
	}
	got, _, err = dialog(t, rows, home, "1\n-\n\n")
	if err != nil || got[0].To != "" {
		t.Fatalf("skip: %+v %v", got, err)
	}
	got, out, err = dialog(t, rows, home, "1\nrelative/path\n1\n/no/such/dir\n9\n\n")
	if err != nil || got[0].To != "/h/app" {
		t.Fatalf("bad input changed a row: %+v %v", got, err)
	}
	if !strings.Contains(out, "not an absolute path") || !strings.Contains(out, "not a directory") || !strings.Contains(out, `No row "9"`) {
		t.Errorf("errors not shown:\n%s", out)
	}
	_, out, _ = dialog(t, []Row{{From: "/a/\x1b]0;x\x07app", To: "/h/app"}}, home, "\n")
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control characters reached the terminal: %q", out)
	}
	if _, _, err := dialog(t, rows, home, "q\n"); !errors.Is(err, ErrAborted) {
		t.Errorf("q: %v", err)
	}
	if _, _, err := dialog(t, rows, home, ""); !errors.Is(err, ErrAborted) {
		t.Errorf("EOF must abort, got %v", err)
	}
	if _, _, err := dialog(t, rows, home, "1\n"); !errors.Is(err, ErrAborted) {
		t.Errorf("EOF mid-edit must abort, got %v", err)
	}
	if rows[0].To != "/h/app" {
		t.Errorf("Confirm changed its input: %+v", rows)
	}
}

func TestAskWorkDir(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, filepath.Join(home, "src"))
	w := WorkDir{Abs: "/a/Projects", Label: "Projects"}
	sugg := []string{filepath.Join(home, "src")}
	ask := func(in string) (string, string, error) {
		var out strings.Builder
		got, err := AskWorkDir(w, sugg, home, bufio.NewReader(strings.NewReader(in)), &out)
		return got, out.String(), err
	}
	if got, out, err := ask("1\n"); err != nil || got != sugg[0] || !strings.Contains(out, "1. "+sugg[0]) {
		t.Errorf("number: %q %v\n%s", got, err, out)
	}
	if got, _, err := ask("5\n~/src\n"); err != nil || got != sugg[0] {
		t.Errorf("path: %q %v", got, err)
	}
	if got, _, err := ask("\n"); err != nil || got != "" {
		t.Errorf("skip: %q %v", got, err)
	}
	if _, _, err := ask("q\n"); !errors.Is(err, ErrAborted) {
		t.Errorf("q: %v", err)
	}
	if _, _, err := ask(""); !errors.Is(err, ErrAborted) {
		t.Errorf("EOF: %v", err)
	}
}
