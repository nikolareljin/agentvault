package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/gitid"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// hiddenFixture is matchFixture with the bundle work dir resolving to the
// hidden folder ~/.work/Projects, and a vault to record answers in.
func hiddenFixture(t *testing.T) (*locations.Set, locationOptions, *vault.Vault, string) {
	t.Helper()
	_, set, o := matchFixture(t)
	hiddenWD := filepath.Join(o.Home, ".work", "Projects")
	mustMkdirAll(t, hiddenWD)
	set.WorkDirs[0].HomeRel = ".work/Projects"
	// The checkout is in the hidden work dir: a confirmation covers only
	// projects read from a local checkout there.
	app := filepath.Join(hiddenWD, "app")
	mustMkdirAll(t, filepath.Join(app, ".git"))
	o.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if sameDirT(dir, app) {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	return set, o, v, hiddenWD
}

// importOnce runs one profile's matching and records it, as import does.
func importOnce(t *testing.T, set *locations.Set, o *locationOptions, v *vault.Vault, input string) (locationResult, string) {
	t.Helper()
	var out bytes.Buffer
	o.Out = &out
	o.In = bufio.NewReader(strings.NewReader(input))
	res, err := resolveBundleLocations(context.Background(), set, "", *o)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if err := recordLocations(v, res, o); err != nil {
		t.Fatal(err)
	}
	return res, out.String()
}

func TestHiddenMappingConfirmedOnceNotAskedAgain(t *testing.T) {
	set, o, v, hiddenWD := hiddenFixture(t)
	o.Interactive = true
	if res, out := importOnce(t, set, &o, v, "y\n\n"); len(res.Missed) != 0 || !strings.Contains(out, "inside a hidden folder") {
		t.Fatalf("first import: %+v\n%s", res, out)
	}
	known := v.Locations()
	if known.WorkDirMaps["/old/Projects"] != hiddenWD || known.ConfirmedHidden["/old/Projects"].Target != hiddenWD {
		t.Fatalf("y not saved: %+v", known)
	}

	// Second import on a terminal: no hidden question, shown as confirmed.
	res, out := importOnce(t, set, &o, v, "\n")
	if len(res.Missed) != 0 || strings.Contains(out, "inside a hidden folder") || !strings.Contains(out, "saved mapping (hidden, confirmed)") {
		t.Fatalf("asked again: %+v\n%s", res, out)
	}

	// Second import without a terminal works with --accept-matches.
	o.Interactive, o.AcceptMatches = false, true
	res, out = importOnce(t, set, &o, v, "")
	if len(res.Missed) != 0 || len(res.Remaps) != 1 || res.Remaps[0].To != filepath.Join(hiddenWD, "app") {
		t.Fatalf("non-interactive second import: %+v\n%s", res, out)
	}
	// Only the verified project is rewritten: no prefix remap of the work
	// dir, so other bundle paths under it stay where they are.
	for _, m := range res.Remaps {
		if locations.CleanForeign(m.From) == "/old/Projects" {
			t.Fatalf("whole work dir remapped into the hidden folder: %+v", res.Remaps)
		}
	}
	if !strings.Contains(out, "other paths under it are not rewritten") {
		t.Fatalf("not reported:\n%s", out)
	}
}

// A typed hidden path and a --workdir-map to a hidden folder confirm too.
func TestHiddenMappingConfirmedByTypedPathAndFlag(t *testing.T) {
	set, o, v, _ := hiddenFixture(t)
	other := filepath.Join(o.Home, ".other")
	mustMkdirAll(t, other)
	o.Interactive = true
	importOnce(t, set, &o, v, other+"\n\n")
	if got := v.Locations().ConfirmedHidden["/old/Projects"].Target; got != other {
		t.Fatalf("typed path not confirmed: %q", got)
	}

	// A work dir not found here, answered with a hidden path.
	set3, o3, v3, hidden3 := hiddenFixture(t)
	set3.WorkDirs[0].HomeRel, set3.WorkDirs[0].Label = "Nowhere", "Nowhere"
	o3.Interactive = true
	if _, out := importOnce(t, set3, &o3, v3, hidden3+"\n\n"); !strings.Contains(out, "was not found here") {
		t.Fatalf("not asked for the work dir:\n%s", out)
	}
	if got := v3.Locations().ConfirmedHidden["/old/Projects"].Target; got != hidden3 {
		t.Fatalf("typed work dir not confirmed: %q", got)
	}

	set2, o2, v2, hiddenWD := hiddenFixture(t)
	o2.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	importOnce(t, set2, &o2, v2, "")
	if got := v2.Locations().ConfirmedHidden["/old/Projects"].Target; got != hiddenWD {
		t.Fatalf("--workdir-map not confirmed: %q", got)
	}
}

// The confirmation is for one target: a mapping changed to another hidden
// target is asked again, and the old confirmation is dropped.
func TestHiddenConfirmationDroppedWhenTargetChanges(t *testing.T) {
	set, o, v, hiddenWD := hiddenFixture(t)
	o.Interactive = true
	importOnce(t, set, &o, v, "y\n\n")
	other := filepath.Join(o.Home, ".other")
	mustMkdirAll(t, other)
	if err := recordLocations(v, locationResult{NewMaps: []pathRemap{{From: "/old/Projects", To: other}}}, &o); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Locations().ConfirmedHidden["/old/Projects"]; ok {
		t.Fatalf("confirmation for %s kept after the map changed: %+v", hiddenWD, v.Locations())
	}
	_, out := importOnce(t, set, &o, v, "\n\n")
	if !strings.Contains(out, "inside a hidden folder") {
		t.Fatalf("changed target not asked:\n%s", out)
	}
}

// A bundle cannot confirm a hidden target: its confirmed_hidden (and maps)
// are not this machine's answers.
func TestBundleConfirmedHiddenIgnored(t *testing.T) {
	set, o, v, hiddenWD := hiddenFixture(t)
	// This machine saved the mapping but never confirmed it.
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	set.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	set.ConfirmedHidden = map[string]locations.HiddenConfirm{"/old/Projects": {Target: hiddenWD, Projects: []string{"remote:github.com/acme/app"}}}
	o.AcceptMatches = true
	res, out := importOnce(t, set, &o, v, "")
	if len(res.Missed) != 1 || !strings.Contains(res.Missed[0].Reason, "hidden") {
		t.Fatalf("bundle confirmed a hidden target: %+v\n%s", res, out)
	}
	if len(v.Locations().ConfirmedHidden) != 0 {
		t.Fatalf("bundle confirmation saved: %+v", v.Locations())
	}
}

// A project checked out under a confirmed hidden work dir is covered by that
// confirmation, as under a --workdir-map.
func TestProjectUnderConfirmedHiddenWorkDir(t *testing.T) {
	set, o, v, hiddenWD := hiddenFixture(t)
	app := filepath.Join(hiddenWD, "app")
	mustMkdirAll(t, filepath.Join(app, ".git"))
	o.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if sameDirT(dir, app) {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	o.Known.ConfirmedHidden = map[string]locations.HiddenConfirm{"/old/Projects": {Target: hiddenWD, Projects: []string{"remote:github.com/acme/app"}}}
	o.AcceptMatches = true
	res, out := importOnce(t, set, &o, v, "")
	if len(res.Missed) != 0 || len(res.Stored.Projects) != 1 || res.Stored.Projects[0].Abs != app {
		t.Fatalf("project under a confirmed hidden work dir not accepted: %+v\n%s", res, out)
	}
}

// Confirmations are this machine's answers: an export never carries them.
func TestExportDropsConfirmedHidden(t *testing.T) {
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.SetLocations(locations.Set{
		WorkDirs:        []locations.WorkDir{{ID: "w1", Abs: "/here/Projects"}},
		WorkDirMaps:     map[string]string{"/old/Projects": "/here/.work"},
		ConfirmedHidden: map[string]locations.HiddenConfirm{"/old/Projects": {Target: "/here/.work"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := captureBundleLocations(nil, v, SetupBundle{}, setupCollectOptions{})
	if got == nil || len(got.WorkDirs) != 1 || got.WorkDirMaps != nil || got.ConfirmedHidden != nil {
		t.Fatalf("export carries this machine's answers: %+v", got)
	}
}

// confirmedVault is hiddenFixture with the hidden mapping already saved and
// confirmed in the vault, on a terminal.
func confirmedVault(t *testing.T) (*locations.Set, locationOptions, *vault.Vault, string) {
	t.Helper()
	set, o, v, hiddenWD := hiddenFixture(t)
	o.Interactive = true
	importOnce(t, set, &o, v, "y\n\n")
	if v.Locations().ConfirmedHidden["/old/Projects"].Target != hiddenWD {
		t.Fatalf("setup: %+v", v.Locations())
	}
	return set, o, v, hiddenWD
}

// A work dir row edited in the confirm list is saved as the new mapping.
func TestEditedWorkDirRowSaved(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o.Home, "src")}
	o.Interactive = true
	res, out := importOnce(t, set, &o, v, "1\n"+filepath.Join(o.Home, "Projects")+"\n\n\n")
	if got := v.Locations().WorkDirMaps["/old/Projects"]; got != filepath.Join(o.Home, "Projects") {
		t.Fatalf("edited row not saved: %q %+v\n%s", got, res, out)
	}
}

// A work dir row edited in the list to a hidden folder is asked about
// first; a yes saves and confirms it, a decline changes nothing saved.
func TestEditedRowToHiddenTarget(t *testing.T) {
	setup := func(t *testing.T) (*locations.Set, locationOptions, *vault.Vault, string) {
		_, set, o := matchFixture(t)
		set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
		v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
		if err := v.Init("pw"); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(o.Home, "src")
		if err := recordLocations(v, locationResult{NewMaps: []pathRemap{{From: "/old/Projects", To: src}}}, &o); err != nil {
			t.Fatal(err)
		}
		o.Interactive = true
		return set, o, v, src
	}
	set, o, v, _ := setup(t)
	other := filepath.Join(o.Home, ".other")
	mustMkdirAll(t, other)
	_, out := importOnce(t, set, &o, v, "1\n"+other+"\n\ny\n\n")
	if !strings.Contains(out, other+" is inside a hidden folder") {
		t.Fatalf("new hidden target not asked:\n%s", out)
	}
	if k := v.Locations(); k.WorkDirMaps["/old/Projects"] != other || k.ConfirmedHidden["/old/Projects"].Target != other {
		t.Fatalf("edit to hidden target: %+v", k)
	}

	set, o, v, src := setup(t)
	x := filepath.Join(o.Home, ".x")
	mustMkdirAll(t, x)
	res, out := importOnce(t, set, &o, v, "1\n"+x+"\n\n\n")
	if !strings.Contains(out, x+" is inside a hidden folder") || len(res.Missed) != 1 {
		t.Fatalf("declined hidden edit: %+v\n%s", res, out)
	}
	if k := v.Locations(); k.WorkDirMaps["/old/Projects"] != src || len(k.ConfirmedHidden) != 0 {
		t.Fatalf("declined edit changed the vault: %+v", k)
	}
}

// Skipping a row in the list removes nothing saved.
func TestSkippedRowKeepsSavedMapping(t *testing.T) {
	set, o, v, hiddenWD := confirmedVault(t)
	importOnce(t, set, &o, v, "1\n-\n\n")
	if k := v.Locations(); k.WorkDirMaps["/old/Projects"] != hiddenWD || k.ConfirmedHidden["/old/Projects"].Target != hiddenWD {
		t.Fatalf("skip changed the vault: %+v", k)
	}
}

// A bundle path is only a name: a later bundle naming the same work dir with
// a project the user never confirmed there is asked about again, and without
// a terminal nothing lands in the hidden folder.
func TestConfirmedHiddenDoesNotCoverOtherProjects(t *testing.T) {
	set, o, v, hiddenWD := confirmedVault(t)
	hostile := &locations.Set{
		WorkDirs: set.WorkDirs,
		Projects: append(append([]locations.Project(nil), set.Projects...),
			locations.Project{ID: "p2", WorkDir: "w1", Rel: "evil", Abs: "/old/Projects/evil", GitRemote: "github.com/mallory/evil"}),
	}
	o.Interactive, o.AcceptMatches = false, true
	res, out := importOnce(t, hostile, &o, v, "")
	if len(res.Missed) == 0 || !strings.Contains(res.Missed[0].Reason, "hidden") {
		t.Fatalf("hostile bundle used the confirmation: %+v\n%s", res, out)
	}
	for _, m := range res.Remaps {
		if pathWithin(hiddenWD, m.To) {
			t.Fatalf("remapped into the hidden folder unasked: %+v", m)
		}
	}
	// A project without identity is matched by name only: also asked.
	noID := &locations.Set{WorkDirs: set.WorkDirs, Projects: []locations.Project{{ID: "p3", WorkDir: "w1", Rel: "app", Abs: "/old/Projects/app"}}}
	res, out = importOnce(t, noID, &o, v, "")
	if strings.Contains(out, "(hidden, confirmed)") {
		t.Fatalf("unidentified project used the confirmation: %+v\n%s", res, out)
	}
	for _, m := range res.Remaps {
		if pathWithin(hiddenWD, m.To) {
			t.Fatalf("unidentified project remapped into the hidden folder: %+v", m)
		}
	}

	// On a terminal it is asked; a yes extends the confirmation to the
	// projects read from a checkout here.
	evil := filepath.Join(hiddenWD, "evil")
	mustMkdirAll(t, filepath.Join(evil, ".git"))
	base := o.Identify
	o.Identify = func(ctx context.Context, dir string) (gitid.Repo, error) {
		if sameDirT(dir, evil) {
			return gitid.Repo{Root: dir, Remote: "github.com/mallory/evil"}, nil
		}
		return base(ctx, dir)
	}
	o.Interactive, o.AcceptMatches = true, false
	if _, out = importOnce(t, hostile, &o, v, "y\n\n"); !strings.Contains(out, "inside a hidden folder") {
		t.Fatalf("not asked:\n%s", out)
	}
	c := v.Locations().ConfirmedHidden["/old/Projects"]
	if c.Target != hiddenWD || !c.Covers([]string{"remote:github.com/acme/app", "remote:github.com/mallory/evil"}) {
		t.Fatalf("yes did not extend the confirmation: %+v", c)
	}
	if _, out = importOnce(t, hostile, &o, v, "\n"); strings.Contains(out, "inside a hidden folder") {
		t.Fatalf("asked again after the yes:\n%s", out)
	}
}

// A confirmation in the older form (a bare target, no project list) is read
// and confirms nothing.
func TestOldConfirmedHiddenFormAsksAgain(t *testing.T) {
	set, o, v, hiddenWD := hiddenFixture(t)
	raw := `{"work_dir_maps":{"/old/Projects":` + strconvQuote(hiddenWD) + `},"confirmed_hidden":{"/old/Projects":` + strconvQuote(hiddenWD) + `}}`
	if err := json.Unmarshal([]byte(raw), &o.Known); err != nil {
		t.Fatal(err)
	}
	if o.Known.ConfirmedHidden["/old/Projects"].Target != hiddenWD {
		t.Fatalf("old form not read: %+v", o.Known)
	}
	o.AcceptMatches = true
	if res, out := importOnce(t, set, &o, v, ""); len(res.Missed) == 0 {
		t.Fatalf("old confirmation without a project list was used: %+v\n%s", res, out)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Projects under an edited work dir row are matched again under the new
// target, so this import and the saved mapping agree.
func TestEditedWorkDirRowMovesItsProjects(t *testing.T) {
	oldApp, set, o := matchFixture(t)
	newApp := filepath.Join(o.Home, "Projects", "app")
	mustMkdirAll(t, filepath.Join(newApp, ".git"))
	o.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if dir == oldApp || dir == newApp {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o.Home, "src")}
	o.Interactive = true
	res, out := importOnce(t, set, &o, v, "1\n"+filepath.Join(o.Home, "Projects")+"\n\n\n")
	if len(res.Stored.Projects) != 1 || res.Stored.Projects[0].Abs != newApp {
		t.Fatalf("project not moved with its work dir: %+v\n%s", res, out)
	}
	for _, m := range res.Remaps {
		if m.To == oldApp {
			t.Fatalf("project still remapped to the old target: %+v", res.Remaps)
		}
	}
}

// A confirmation covers a project only through its checkout here: a bundle
// copying a confirmed remote under another name, or a checkout that is gone,
// is asked again.
func TestConfirmedHiddenNeedsLocalCheckout(t *testing.T) {
	set, o, v, hiddenWD := confirmedVault(t)
	o.Interactive, o.AcceptMatches = false, true
	copied := &locations.Set{WorkDirs: set.WorkDirs, Projects: []locations.Project{
		{ID: "p1", WorkDir: "w1", Rel: "other", Abs: "/old/Projects/other", GitRemote: "github.com/acme/app"},
	}}
	if res, out := importOnce(t, copied, &o, v, ""); strings.Contains(out, "(hidden, confirmed)") {
		t.Fatalf("copied remote used the confirmation: %+v\n%s", res, out)
	}
	// Same name, but the checkout here is another repository.
	o.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		return gitid.Repo{Root: dir, Remote: "github.com/someone/else"}, nil
	}
	if res, out := importOnce(t, set, &o, v, ""); strings.Contains(out, "(hidden, confirmed)") {
		t.Fatalf("other local repository used the confirmation: %+v\n%s", res, out)
	}
	_ = hiddenWD

	// The checkout is gone.
	set2, o2, v2, hidden2 := confirmedVault(t)
	if err := os.RemoveAll(filepath.Join(hidden2, "app")); err != nil {
		t.Fatal(err)
	}
	o2.Interactive, o2.AcceptMatches = false, true
	if res, out := importOnce(t, set2, &o2, v2, ""); strings.Contains(out, "(hidden, confirmed)") || len(res.Missed) == 0 {
		t.Fatalf("missing checkout used the confirmation: %+v\n%s", res, out)
	}
}

// A confirmed hidden work dir does not cover a folder: a project whose
// target there is not its verified checkout is asked about (missed here).
func TestConfirmedHiddenCoversOnlyVerifiedCheckouts(t *testing.T) {
	_, set, o := matchFixture(t)
	hiddenWD := filepath.Join(o.Home, ".work")
	app := filepath.Join(hiddenWD, "app")
	mustMkdirAll(t, filepath.Join(app, ".git"))
	o.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if sameDirT(dir, app) {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	o.AcceptMatches = true
	o.confirmedHidden = map[string]bool{"w1": true}
	o.verifiedHidden = map[string]string{}
	var out bytes.Buffer
	var res locationResult
	if err := matchAndConfirm(context.Background(), set, "", map[string]string{"w1": hiddenWD}, map[string]bool{}, o, &out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Missed) != 1 || len(res.Stored.Projects) != 0 {
		t.Fatalf("unverified project under a confirmed hidden work dir accepted: %+v\n%s", res, out.String())
	}
	// The verified checkout itself passes.
	o.verifiedHidden = map[string]string{"/old/Projects/app": app}
	res = locationResult{}
	if err := matchAndConfirm(context.Background(), set, "", map[string]string{"w1": hiddenWD}, map[string]bool{}, o, &out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Missed) != 0 || len(res.Stored.Projects) != 1 {
		t.Fatalf("verified checkout refused: %+v\n%s", res, out.String())
	}
}

// gitRepoAt makes dir a repository with remote github.com/acme/<name>.
func gitRepoAt(t *testing.T, dir, name string) {
	t.Helper()
	mustMkdirAll(t, dir)
	gitOrSkip(t, dir, "init", "-q")
	gitOrSkip(t, dir, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
}

func verify(t *testing.T, rel, to string) bool {
	t.Helper()
	_, ok := localProjectKey(context.Background(), locations.DefaultIdentifier, locations.Project{Rel: rel, GitRemote: "github.com/acme/x"}, to)
	return ok
}

// A folder inside another repository is not that repository's checkout.
func TestLocalCheckoutMustBeItsOwnRoot(t *testing.T) {
	to := t.TempDir()
	gitRepoAt(t, filepath.Join(to, "repo"), "x")
	mustMkdirAll(t, filepath.Join(to, "repo", "sub"))
	if verify(t, "repo/sub", to) {
		t.Fatal("subfolder of a repository verified as its checkout")
	}
	if !verify(t, "repo", to) {
		t.Fatal("the repository itself not verified")
	}
}

// No symlink is followed at or below the target.
func TestLocalCheckoutRefusesSymlinkBelowTarget(t *testing.T) {
	to, elsewhere := t.TempDir(), t.TempDir()
	gitRepoAt(t, filepath.Join(elsewhere, "proj"), "x")
	if err := os.Symlink(elsewhere, filepath.Join(to, "a")); err != nil {
		t.Skip("symlinks not available:", err)
	}
	if verify(t, "a/proj", to) {
		t.Fatal("checkout through a symlinked parent verified")
	}
	if err := os.Symlink(filepath.Join(elsewhere, "proj"), filepath.Join(to, "proj")); err != nil {
		t.Fatal(err)
	}
	if verify(t, "proj", to) {
		t.Fatal("symlinked checkout verified")
	}
}

// A symlink above the target (a symlinked home) does not stop verification.
func TestLocalCheckoutUnderSymlinkedHome(t *testing.T) {
	real, links := t.TempDir(), t.TempDir()
	home := filepath.Join(links, "home")
	if err := os.Symlink(real, home); err != nil {
		t.Skip("symlinks not available:", err)
	}
	gitRepoAt(t, filepath.Join(real, ".work", "app"), "x")
	if !verify(t, "app", filepath.Join(home, ".work")) {
		t.Fatal("checkout under a symlinked home not verified")
	}
}

// sameDirT compares two paths after resolving symlinks and short names
// (macOS /var, Windows 8.3 temp paths), as verification resolves them.
func sameDirT(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
