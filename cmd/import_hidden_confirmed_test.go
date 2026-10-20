package cmd

import (
	"bufio"
	"bytes"
	"context"
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
	if known.WorkDirMaps["/old/Projects"] != hiddenWD || known.ConfirmedHidden["/old/Projects"] != hiddenWD {
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
	if len(res.Missed) != 0 || len(res.Remaps) == 0 || res.Remaps[0].To != hiddenWD {
		t.Fatalf("non-interactive second import: %+v\n%s", res, out)
	}
}

// A typed hidden path and a --workdir-map to a hidden folder confirm too.
func TestHiddenMappingConfirmedByTypedPathAndFlag(t *testing.T) {
	set, o, v, _ := hiddenFixture(t)
	other := filepath.Join(o.Home, ".other")
	mustMkdirAll(t, other)
	o.Interactive = true
	importOnce(t, set, &o, v, other+"\n\n")
	if got := v.Locations().ConfirmedHidden["/old/Projects"]; got != other {
		t.Fatalf("typed path not confirmed: %q", got)
	}

	// A work dir not found here, answered with a hidden path.
	set3, o3, v3, hidden3 := hiddenFixture(t)
	set3.WorkDirs[0].HomeRel, set3.WorkDirs[0].Label = "Nowhere", "Nowhere"
	o3.Interactive = true
	if _, out := importOnce(t, set3, &o3, v3, hidden3+"\n\n"); !strings.Contains(out, "was not found here") {
		t.Fatalf("not asked for the work dir:\n%s", out)
	}
	if got := v3.Locations().ConfirmedHidden["/old/Projects"]; got != hidden3 {
		t.Fatalf("typed work dir not confirmed: %q", got)
	}

	set2, o2, v2, hiddenWD := hiddenFixture(t)
	o2.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	importOnce(t, set2, &o2, v2, "")
	if got := v2.Locations().ConfirmedHidden["/old/Projects"]; got != hiddenWD {
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
	set.ConfirmedHidden = map[string]string{"/old/Projects": hiddenWD}
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
		if dir == app {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	o.Known.ConfirmedHidden = map[string]string{"/old/Projects": hiddenWD}
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
		ConfirmedHidden: map[string]string{"/old/Projects": "/here/.work"},
	}); err != nil {
		t.Fatal(err)
	}
	got := captureBundleLocations(nil, v, SetupBundle{}, setupCollectOptions{})
	if got == nil || len(got.WorkDirs) != 1 || got.WorkDirMaps != nil || got.ConfirmedHidden != nil {
		t.Fatalf("export carries this machine's answers: %+v", got)
	}
}
