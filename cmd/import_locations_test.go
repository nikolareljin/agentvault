package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/gitid"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/pflag"
)

// matchFixture is a bundle naming /old/Projects/app (github.com/acme/app)
// and a home where that repository is checked out as ~/src/app.
func matchFixture(t *testing.T) (string, *locations.Set, locationOptions) {
	t.Helper()
	home := t.TempDir()
	app := filepath.Join(home, "src", "app")
	mustMkdirAll(t, filepath.Join(app, ".git"))
	mustMkdirAll(t, filepath.Join(home, "Projects"))
	set := &locations.Set{
		WorkDirs: []locations.WorkDir{{ID: "w1", Label: "Projects", HomeRel: "Projects", Abs: "/old/Projects"}},
		Projects: []locations.Project{{ID: "p1", WorkDir: "w1", Rel: "app", Abs: "/old/Projects/app", GitRemote: "github.com/acme/app"}},
	}
	ids := map[string]gitid.Repo{app: {Root: app, Remote: "github.com/acme/app"}}
	o := locationOptions{
		Home: home,
		Identify: func(_ context.Context, dir string) (gitid.Repo, error) {
			if r, ok := ids[dir]; ok {
				return r, nil
			}
			return gitid.Repo{}, gitid.ErrNotRepo
		},
	}
	return app, set, o
}

func TestMatchNotConfirmedWithoutTerminalOrFlag(t *testing.T) {
	_, set, o := matchFixture(t)
	var out bytes.Buffer
	o.Out = &out
	res, err := resolveBundleLocations(context.Background(), set, "/old/Projects/app", o)
	if err != nil {
		t.Fatal(err)
	}
	// A unique, score-100 match is still only a proposal.
	if !res.Refused || len(res.Remaps) != 0 || res.ProjectRoot != "" || !res.Stored.Empty() {
		t.Fatalf("unconfirmed match used: %+v", res)
	}
	if !strings.Contains(out.String(), "/old/Projects/app -> ") || !strings.Contains(out.String(), "--accept-matches") {
		t.Errorf("list not printed:\n%s", out.String())
	}
}

func TestMatchAcceptedByFlag(t *testing.T) {
	app, set, o := matchFixture(t)
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "/old/Projects/app", o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused || res.ProjectRoot != app {
		t.Fatalf("got %+v", res)
	}
	found := false
	for _, r := range res.Remaps {
		found = found || r.From == "/old/Projects/app" && r.To == app
	}
	if !found {
		t.Errorf("remaps %+v", res.Remaps)
	}
	if len(res.Stored.Projects) != 1 || res.Stored.Projects[0].Abs != app || res.Stored.Projects[0].Aliases[0] != "/old/Projects/app" {
		t.Errorf("stored %+v", res.Stored)
	}
}

func TestMatchConfirmedInteractivelyWithEditedTarget(t *testing.T) {
	_, set, o := matchFixture(t)
	other := filepath.Join(o.Home, "elsewhere")
	mustMkdirAll(t, other)
	var out bytes.Buffer
	o.Out, o.Interactive = &out, true
	// Rows: 1 is the work dir, 2 the project. Point the project elsewhere.
	o.In = bufio.NewReader(strings.NewReader("2\n" + other + "\n\n"))
	res, err := resolveBundleLocations(context.Background(), set, "/old/Projects/app", o)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if res.ProjectRoot != other {
		t.Errorf("project root %q, want %q\n%s", res.ProjectRoot, other, out.String())
	}

	o.In = bufio.NewReader(strings.NewReader("q\n"))
	if _, err := resolveBundleLocations(context.Background(), set, "", o); err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Errorf("q did not abort: %v", err)
	}
}

func TestMapWinsOverComputedMatch(t *testing.T) {
	_, set, o := matchFixture(t)
	o.AcceptMatches = true
	o.UserMaps = []pathRemap{{From: "/old/Projects", To: "/mine"}}
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Remaps {
		if r.From == "/old/Projects/app" {
			t.Errorf("computed match beat --map: %+v", res.Remaps)
		}
	}
	merged := mergeComputedRemaps([]pathRemap{{From: "/old/Projects", To: "/mine"}}, []pathRemap{{From: "/old/Projects/app", To: "/x"}, {From: "/old/lib", To: "/y"}}, o.UserMaps)
	if len(merged) != 2 || merged[0].From != "/old/Projects" && merged[1].From != "/old/Projects" {
		t.Errorf("merged %+v", merged)
	}
}

func TestUnmatchedWorkDirNeedsAnswer(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	var out bytes.Buffer
	o.Out = &out
	// Not found: recorded, not fatal; the work dir's projects are left out.
	miss, err := resolveBundleLocations(context.Background(), set, "/old/Projects/app", o)
	if err != nil || len(miss.Missed) != 1 || !miss.SourceSkipped || len(miss.Remaps) != 0 {
		t.Fatalf("missed: %+v %v", miss, err)
	}
	var sum bytes.Buffer
	printMissed(&sum, miss.Missed)
	if !strings.Contains(sum.String(), "pass --workdir-map '/old/Projects'=<path here>") || !strings.Contains(sum.String(), "maybe: ") {
		t.Errorf("summary:\n%s", sum.String())
	}
	o.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o.Home, "src")}
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Remaps) != 2 {
		t.Fatalf("with --workdir-map: %+v %v", res, err)
	}
	if len(res.NewMaps) != 1 || res.NewMaps[0].To != filepath.Join(o.Home, "src") {
		t.Errorf("--workdir-map not kept for the vault: %+v", res.NewMaps)
	}
	o.WorkDirMaps = nil
	o.Interactive = true
	o.In = bufio.NewReader(strings.NewReader("~/src\n\n"))
	res, err = resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Remaps) != 2 || len(res.Missed) != 0 || len(res.NewMaps) != 1 {
		t.Fatalf("interactive: %+v %v\n%s", res, err, out.String())
	}
	if !strings.Contains(out.String(), "was not found here") {
		t.Errorf("not asked:\n%s", out.String())
	}
}

func TestAmbiguousMatchSkippedEvenWithAccept(t *testing.T) {
	_, set, o := matchFixture(t)
	second := filepath.Join(o.Home, "Projects", "app-copy")
	mustMkdirAll(t, filepath.Join(second, ".git"))
	inner := o.Identify
	o.Identify = func(ctx context.Context, dir string) (gitid.Repo, error) {
		if dir == second {
			return gitid.Repo{Root: second, Remote: "github.com/acme/app"}, nil
		}
		return inner(ctx, dir)
	}
	set.Projects[0].Rel = "zzz"
	set.Projects[0].Abs = "/old/Projects/zzz"
	var out bytes.Buffer
	o.Out, o.AcceptMatches = &out, true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Remaps {
		if r.From == "/old/Projects/zzz" {
			t.Errorf("ambiguous match applied: %+v", res.Remaps)
		}
	}
	if !strings.Contains(out.String(), "ambiguous; pass --map /old/Projects/zzz=") {
		t.Errorf("no hint:\n%s", out.String())
	}
}

func TestParseWorkDirMaps(t *testing.T) {
	dir := t.TempDir()
	if m, err := parseWorkDirMaps([]string{"/old/P/=" + dir}); err != nil || m["/old/P"] != dir {
		t.Errorf("%v %v", m, err)
	}
	for _, bad := range []string{"x", "rel=" + dir, "/=" + dir, "/old=/no/such/dir"} {
		if _, err := parseWorkDirMaps([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAliasDirectoryPatterns(t *testing.T) {
	here := filepath.Join(t.TempDir(), "app")
	known := locations.Set{Projects: []locations.Project{{Abs: here, GitRemote: "github.com/acme/app", Aliases: []string{"/old/Projects/app"}}}}
	insts := []agent.InstructionFile{
		{Name: "a", Scope: agent.InstructionScopeDirectory, DirectoryPattern: "/old/Projects/app/web"},
		{Name: "b", Scope: agent.InstructionScopeDirectory, DirectoryPattern: "/old/Projects/application"},
		{Name: "c", Scope: agent.InstructionScopeGlobal},
	}
	id := func(_ context.Context, d string) (gitid.Repo, error) {
		return gitid.Repo{Root: here, Remote: "github.com/acme/app"}, nil
	}
	got := aliasDirectoryPatterns(insts, known, here, id)
	if got[0].DirectoryPattern != filepath.ToSlash(here)+"/web" {
		t.Errorf("alias not applied: %q", got[0].DirectoryPattern)
	}
	if got[1].DirectoryPattern != "/old/Projects/application" {
		t.Errorf("prefix without boundary rewritten: %q", got[1].DirectoryPattern)
	}
	other := func(_ context.Context, d string) (gitid.Repo, error) {
		return gitid.Repo{Root: here, Remote: "github.com/someone/else"}, nil
	}
	if got := aliasDirectoryPatterns(insts, known, here, other); got[0].DirectoryPattern != "/old/Projects/app/web" {
		t.Errorf("other repository aliased: %q", got[0].DirectoryPattern)
	}
}

func gitOrSkip(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func setStringArray(t *testing.T, fs *pflag.FlagSet, name string, values []string) {
	t.Helper()
	f := fs.Lookup(name)
	sv := f.Value.(pflag.SliceValue)
	if err := sv.Replace(values); err != nil {
		t.Fatal(err)
	}
	f.Changed = true
	t.Cleanup(func() { _ = sv.Replace(nil); f.Changed = false })
}

// Export a git project from one home, import into another home where the
// same repository sits elsewhere and is cloned over HTTPS instead of SSH.
// The project files land in the clone without --project-root.
func TestImportFindsProjectByRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "home", "alex")
	dstHome := filepath.Join(base, "Users", "alex")
	srcProject := filepath.Join(srcHome, "Projects", "app")
	dstProject := filepath.Join(dstHome, "code", "app")
	for _, d := range []string{srcProject, dstProject} {
		mustMkdirAll(t, d)
	}
	gitOrSkip(t, srcProject, "init", "-q")
	gitOrSkip(t, srcProject, "remote", "add", "origin", "git@github.com:Acme/App.git")
	gitOrSkip(t, dstProject, "init", "-q")
	gitOrSkip(t, dstProject, "remote", "add", "origin", "https://github.com/acme/app")
	mustWriteFile(t, filepath.Join(srcProject, "AGENTS.md"), "rules for "+srcProject+"\n")

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)
	bundlePath := filepath.Join(base, "b"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{"yes": "true", "include-secrets": "false", "detect": "false", "project": srcProject})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	setTestHomeDir(t, dstHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dstHome, ".config"))
	dstCfg := filepath.Join(dstHome, ".config", config.AppName)
	seedProfileVault(t, dstCfg, nil, nil)
	useConfigDir(t, dstCfg)
	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{"apply-projects": "true"})

	// Projects/ does not exist here: without --workdir-map nothing is written.
	if err := runImport(importCmd, []string{bundlePath}); err == nil || !strings.Contains(in.String(), "--workdir-map '"+filepath.Join(srcHome, "Projects")+"'=") {
		t.Fatalf("missing work dir not reported: %v\n%s", err, in.String())
	}
	setStringArray(t, importCmd.Flags(), "workdir-map", []string{filepath.Join(srcHome, "Projects") + "=" + filepath.Join(dstHome, "code")})

	// Found, but not confirmed: refused, nothing written into the clone.
	in.Reset()
	if err := runImport(importCmd, []string{bundlePath}); err == nil || !strings.Contains(err.Error(), "--accept-matches") {
		t.Fatalf("unconfirmed match not refused: %v\n%s", err, in.String())
	}
	if _, err := os.Stat(filepath.Join(dstProject, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("project file written without confirmation:\n%s", in.String())
	}

	withFlags(t, importCmd, map[string]string{"accept-matches": "true"})
	in.Reset()
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}
	b, err := os.ReadFile(filepath.Join(dstProject, "AGENTS.md"))
	if err != nil || string(b) != "rules for "+dstProject+"\n" {
		t.Fatalf("AGENTS.md = %q, %v\n%s", b, err, in.String())
	}
	v, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := v.Locations().FindByIdentity("github.com/acme/app", nil)
	if !ok || p.Abs != dstProject || len(p.Aliases) == 0 || p.Aliases[0] != srcProject {
		t.Errorf("vault locations: %+v", v.Locations())
	}
}

// A hostile bundle can claim any remote. A project path of "lib" or "/"
// would turn a match into a rewrite of that word, or of every path.
func TestBundlePathsThatCannotBeRewriteSourcesAreDropped(t *testing.T) {
	for _, abs := range []string{"lib", "/", "C:\\", "/old/\x1b[2Japp"} {
		_, set, o := matchFixture(t)
		set.Projects[0].Abs = abs
		o.AcceptMatches = true
		var out bytes.Buffer
		o.Out = &out
		res, err := resolveBundleLocations(context.Background(), set, "", o)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Remaps {
			if r.From == abs {
				t.Errorf("%q became a rewrite source: %+v", abs, res.Remaps)
			}
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Errorf("control character printed for %q", abs)
		}
		if !strings.Contains(out.String(), "warning: ignoring project") {
			t.Errorf("no warning for %q:\n%s", abs, out.String())
		}
	}
}

func TestComputedRemapsValidatedAtMerge(t *testing.T) {
	got := mergeComputedRemaps(nil, []pathRemap{{From: "lib", To: "/x"}, {From: "/", To: "/x"}, {From: "/a/\x1bb", To: "/x"}, {From: "/ok", To: "/x"}}, nil)
	if len(got) != 1 || got[0].From != "/ok" {
		t.Errorf("got %+v", got)
	}
}

// Aliases in a bundle are claims; only the validated bundle path is kept,
// and never one that is home, above home, or above another project.
func TestBundleAliasesNotPersisted(t *testing.T) {
	_, set, o := matchFixture(t)
	o.AcceptMatches = true
	set.Projects[0].Aliases = []string{"/", "/home/victim"}
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Stored.Projects[0].Aliases; len(a) != 1 || a[0] != "/old/Projects/app" {
		t.Errorf("aliases %v", a)
	}
	for _, kind := range []string{"home", "above home", "above a project"} {
		_, set, o := matchFixture(t)
		o.AcceptMatches = true
		bad := map[string]string{"home": o.Home, "above home": filepath.Dir(o.Home), "above a project": "/old"}[kind]
		if bad == "/old" {
			o.Known = locations.Set{Projects: []locations.Project{{Abs: "/elsewhere", Aliases: []string{"/old/other"}}}}
			set.Projects[0].Abs = "/old"
			set.Projects[0].Rel = "old"
		} else {
			set.Projects[0].Abs = bad
		}
		res, err := resolveBundleLocations(context.Background(), set, "", o)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range res.Stored.Projects {
			if len(p.Aliases) != 0 {
				t.Errorf("alias %v stored for bundle path %q", p.Aliases, bad)
			}
		}
	}
}

// An alias must not name a path of a different repository, including one
// accepted earlier in the same import. The same repository may share it.
func TestAliasRejectedWhenItNamesAnotherProject(t *testing.T) {
	other := locations.Project{Abs: "/elsewhere/lib", GitRemote: "github.com/acme/lib", Aliases: []string{"/old/Projects/app"}}
	self := locations.Project{Abs: "/old/Projects/app", GitRemote: "github.com/acme/app"}
	if safeAlias(self.Abs, self, "/home/me", nil, nil, []locations.Project{other}) {
		t.Error("alias equal to another repository's alias accepted")
	}
	if safeAlias(self.Abs, self, "/home/me", nil, nil, nil, []locations.Project{{Abs: "/old/Projects/app/sub", GitRemote: "github.com/x/y"}}) {
		t.Error("alias above a project accepted earlier in this import")
	}
	if safeAlias("/old/Projects/app/sub", self, "/home/me", nil, nil, []locations.Project{{Abs: "/old/Projects/app", GitRemote: "github.com/x/y"}}) {
		t.Error("alias below another repository accepted")
	}
	same := locations.Project{Abs: "/home/me/src/app", GitRemote: "github.com/acme/app", Aliases: []string{"/old/Projects/app"}}
	if !safeAlias(self.Abs, self, "/home/me", nil, nil, []locations.Project{same}) {
		t.Error("same repository refused its own alias")
	}
	if safeAlias("/home", self, "/home/me", nil, nil, nil) || safeAlias("/home/me", self, "/home/me", nil, nil, nil) {
		t.Error("home or above accepted")
	}
	if !safeAlias("/old/Projects/app", self, "/home/me", nil, nil, nil) {
		t.Error("plain alias refused")
	}
	if !locations.Overlaps(`C:\Users\A\x`, "c:/users/a") || locations.Overlaps("/a/bc", "/a/b") {
		t.Error("Overlaps")
	}
}

// Two projects in one bundle: the second one's alias sits under the first
// one's, accepted moments earlier in the same import. It must be refused.
func TestAliasCheckedAgainstThisImport(t *testing.T) {
	_, set, o := matchFixture(t)
	lib := filepath.Join(o.Home, "src", "lib")
	mustMkdirAll(t, filepath.Join(lib, ".git"))
	inner := o.Identify
	o.Identify = func(ctx context.Context, dir string) (gitid.Repo, error) {
		if dir == lib {
			return gitid.Repo{Root: lib, Remote: "github.com/acme/lib"}, nil
		}
		return inner(ctx, dir)
	}
	set.Projects = append(set.Projects, locations.Project{ID: "p2", WorkDir: "w1", Rel: "app/lib", Abs: "/old/Projects/app/lib", GitRemote: "github.com/acme/lib"})
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stored.Projects) != 2 || len(res.Stored.Projects[0].Aliases) != 1 || len(res.Stored.Projects[1].Aliases) != 0 {
		t.Errorf("stored %+v", res.Stored.Projects)
	}
}

// Profile 1 records app with alias /old/Projects/app. Profile 2 claims a
// different repository at /old/Projects/app/lib: its alias would sit under
// app's, so the second profile must see what the first one recorded.
func TestAliasCheckedAcrossProfiles(t *testing.T) {
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	_, set, o := matchFixture(t)
	o.AcceptMatches = true
	first, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(first.Stored.Projects) != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	if err := recordLocations(v, first, &o); err != nil {
		t.Fatal(err)
	}

	lib := filepath.Join(o.Home, "src", "lib")
	mustMkdirAll(t, filepath.Join(lib, ".git"))
	inner := o.Identify
	o.Identify = func(ctx context.Context, dir string) (gitid.Repo, error) {
		if dir == lib {
			return gitid.Repo{Root: lib, Remote: "github.com/acme/lib"}, nil
		}
		return inner(ctx, dir)
	}
	second := &locations.Set{
		WorkDirs: set.WorkDirs,
		Projects: []locations.Project{{ID: "p1", WorkDir: "w1", Rel: "app/lib", Abs: "/old/Projects/app/lib", GitRemote: "github.com/acme/lib"}},
	}
	res, err := resolveBundleLocations(context.Background(), second, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stored.Projects) != 1 || len(res.Stored.Projects[0].Aliases) != 0 {
		t.Errorf("alias under the first profile's project accepted: %+v", res.Stored.Projects)
	}
}

// One work dir missing does not hold up another.
func TestMissingWorkDirSkipsOnlyItsProjects(t *testing.T) {
	app, set, o := matchFixture(t)
	set.WorkDirs = append(set.WorkDirs, locations.WorkDir{ID: "w2", Label: "Gone", HomeRel: "Gone", Abs: "/old/Gone"})
	set.Projects = append(set.Projects, locations.Project{ID: "p2", WorkDir: "w2", Rel: "x", Abs: "/old/Gone/x", GitRemote: "github.com/acme/x"})
	set.WorkDirs[0].HomeRel = "src"
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "/old/Projects/app", o)
	if err != nil || res.ProjectRoot != app || len(res.Missed) != 1 || res.Missed[0].Path != "/old/Gone" || res.SourceSkipped {
		t.Fatalf("%+v %v", res, err)
	}
}

// A mapping saved by an earlier import is reused, and still confirmed.
func TestSavedWorkDirMappingReusedAndShown(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	o.Known.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o.Home, "src")}
	var out bytes.Buffer
	o.Out, o.AcceptMatches = &out, true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Missed) != 0 || len(res.Remaps) != 2 || !strings.Contains(out.String(), "saved mapping") {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}

	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	if err := recordLocations(v, locationResult{NewMaps: []pathRemap{{From: "/old/Projects/", To: "/here"}}}, &o); err != nil {
		t.Fatal(err)
	}
	if v.Locations().WorkDirMaps["/old/Projects"] != "/here" || o.Known.WorkDirMaps["/old/Projects"] != "/here" {
		t.Errorf("mapping not saved: %+v", v.Locations())
	}
}

// Targets in hidden folders need their own yes; --accept-matches is not one.
func TestHiddenTargetsNeedExplicitConfirmation(t *testing.T) {
	_, set, o := matchFixture(t)
	hiddenWD := filepath.Join(o.Home, ".work", "Projects")
	mustMkdirAll(t, hiddenWD)
	set.WorkDirs[0].HomeRel = ".work/Projects"
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Missed) != 1 || !strings.Contains(res.Missed[0].Reason, "hidden") {
		t.Fatalf("hidden work dir accepted: %+v %v", res, err)
	}

	// A --workdir-map to the hidden folder is the confirmation.
	o.WorkDirMaps = map[string]string{"/old/Projects": hiddenWD}
	if res, err = resolveBundleLocations(context.Background(), set, "", o); err != nil || len(res.Missed) != 0 {
		t.Fatalf("explicit map refused: %+v %v", res, err)
	}
	o.WorkDirMaps = nil

	// On a terminal: y confirms the work dir; then the project list.
	var out bytes.Buffer
	o.Out, o.Interactive = &out, true
	o.In = bufio.NewReader(strings.NewReader("y\n\n"))
	if res, err = resolveBundleLocations(context.Background(), set, "", o); err != nil || len(res.Missed) != 0 || !strings.Contains(out.String(), "inside a hidden folder") {
		t.Fatalf("interactive: %+v %v\n%s", res, err, out.String())
	}

	// A project whose checkout is in a hidden folder.
	_, set2, o2 := matchFixture(t)
	hiddenApp := filepath.Join(o2.Home, "src", ".cache", "app")
	mustMkdirAll(t, filepath.Join(hiddenApp, ".git"))
	o2.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if dir == hiddenApp {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	set2.WorkDirs[0].HomeRel = "src"
	// Scan skips hidden folders, so name the hidden parent as the work dir.
	o2.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o2.Home, "src", ".cache")}
	o2.AcceptMatches = true
	res, err = resolveBundleLocations(context.Background(), set2, "/old/Projects/app", o2)
	if err != nil {
		t.Fatal(err)
	}
	// The --workdir-map naming the hidden folder confirms what is under it.
	if res.ProjectRoot != hiddenApp || res.SourceSkipped || len(res.Missed) != 0 {
		t.Errorf("project under a mapped hidden work dir not accepted: %+v", res)
	}

	// Without such a choice, a project found in a hidden folder is not
	// covered by --accept-matches. Reached only if a hidden work dir target
	// was not confirmed first, so drive matchAndConfirm directly.
	_, set3, o3 := matchFixture(t)
	hiddenWD3 := filepath.Join(o3.Home, ".w")
	hidden3 := filepath.Join(hiddenWD3, "app")
	mustMkdirAll(t, filepath.Join(hidden3, ".git"))
	if err := os.RemoveAll(filepath.Join(o3.Home, "src")); err != nil {
		t.Fatal(err)
	}
	o3.Identify = func(_ context.Context, dir string) (gitid.Repo, error) {
		if dir == hidden3 {
			return gitid.Repo{Root: dir, Remote: "github.com/acme/app"}, nil
		}
		return gitid.Repo{}, gitid.ErrNotRepo
	}
	o3.AcceptMatches = true
	var r3 locationResult
	if err := matchAndConfirm(context.Background(), set3, "/old/Projects/app", map[string]string{"w1": hiddenWD3}, map[string]bool{}, o3, io.Discard, &r3); err != nil {
		t.Fatal(err)
	}
	if r3.ProjectRoot != "" || len(r3.Missed) != 1 || !r3.SourceSkipped {
		t.Errorf("hidden project accepted by --accept-matches: %+v", r3)
	}
}

// A different path typed at the hidden-folder prompt is saved like an
// answer to the missing-work-dir question.
func TestHiddenPromptReplacementIsSaved(t *testing.T) {
	_, set, o := matchFixture(t)
	mustMkdirAll(t, filepath.Join(o.Home, ".work", "Projects"))
	set.WorkDirs[0].HomeRel = ".work/Projects"
	src := filepath.Join(o.Home, "src")
	var out bytes.Buffer
	o.Out, o.Interactive = &out, true
	o.In = bufio.NewReader(strings.NewReader(src + "\n\n"))
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.NewMaps) != 1 || res.NewMaps[0].To != src {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
}

// Two profiles naming the same missing work dir: one question, one entry.
func TestMissingWorkDirAskedOncePerImport(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	var out bytes.Buffer
	o.Out, o.Interactive, o.Asked = &out, true, newAskMemo()
	o.In = bufio.NewReader(strings.NewReader("\n")) // skip it, once
	var all []missedEntry
	for i := 0; i < 2; i++ {
		res, err := resolveBundleLocations(context.Background(), set, "", o)
		if err != nil {
			t.Fatalf("profile %d: %v\n%s", i+1, err, out.String())
		}
		all = append(all, res.Missed...)
	}
	if n := strings.Count(out.String(), "was not found here"); n != 1 {
		t.Errorf("asked %d times", n)
	}
	if got := dedupeMissed(all); len(got) != 1 {
		t.Errorf("summary lists %d entries", len(got))
	}
}

func TestShellQuoteHint(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b=/c":             "/a/b=/c",
		"/My Dir=<path here>": "'/My Dir=<path here>'",
		"/it's=x":             `'/it'\''s=x'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// A bundle project at the work dir's own path must not become an alias,
// or every pattern for ~/Projects would follow that one repository.
func TestWorkDirNeverBecomesAlias(t *testing.T) {
	app, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel = "src"
	set.Projects[0].Abs, set.Projects[0].Rel = "/old/Projects", "."
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Stored.Projects) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	insts := []agent.InstructionFile{{Name: "a", Scope: agent.InstructionScopeDirectory, DirectoryPattern: "/old/Projects/other"}}
	id := func(_ context.Context, d string) (gitid.Repo, error) {
		return gitid.Repo{Root: app, Remote: "github.com/acme/app"}, nil
	}
	if got := aliasDirectoryPatterns(insts, res.Stored, app, id); got[0].DirectoryPattern != "/old/Projects/other" {
		t.Errorf("work dir pattern pulled onto one repository: %q", got[0].DirectoryPattern)
	}
}

// Work dir mappings are this machine's; a bundle never carries them.
func TestExportLeavesOutWorkDirMappings(t *testing.T) {
	v := vault.New(filepath.Join(t.TempDir(), "vault.enc"))
	if err := v.Init("pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.SetLocations(locations.Set{
		Projects:    []locations.Project{{ID: "p1", Abs: "/h/src/app", GitRemote: "github.com/acme/app"}},
		WorkDirMaps: map[string]string{"/old/Projects": "/h/src"},
	}); err != nil {
		t.Fatal(err)
	}
	got := captureBundleLocations(nil, v, SetupBundle{}, setupCollectOptions{})
	if got == nil || len(got.Projects) != 1 || got.WorkDirMaps != nil {
		t.Errorf("bundle locations %+v", got)
	}
}

// A real bundle with two profiles that both name a work dir missing here:
// the import goes on, the summary lists it once and counts 1, and nothing
// is asked (no terminal). The once-per-import memory for questions on a
// terminal is TestMissingWorkDirAskedOncePerImport.
func TestImportTwoProfilesListMissingWorkDirOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "home", "alex")
	dstHome := filepath.Join(base, "Users", "alex")
	srcProject := filepath.Join(srcHome, "Projects", "app")
	mustMkdirAll(t, srcProject)
	mustMkdirAll(t, dstHome)
	gitOrSkip(t, srcProject, "init", "-q")
	gitOrSkip(t, srcProject, "remote", "add", "origin", "git@github.com:acme/app.git")

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)
	encPath := filepath.Join(base, "one"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{"yes": "true", "include-secrets": "false", "detect": "false", "project": srcProject})
	if err := runExport(exportCmd, []string{encPath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	raw, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decryptBundlePayload(raw, e2eBundlePassword)
	if err != nil {
		t.Fatal(err)
	}
	var bundle PortableBundle
	if err := json.Unmarshal(payload, &bundle); err != nil || len(bundle.Profiles) != 1 || bundle.Profiles[0].Setup.Locations == nil {
		t.Fatalf("bundle: %v %+v", err, bundle.Profiles)
	}
	second := bundle.Profiles[0]
	second.Name = "second"
	bundle.Profiles = append(bundle.Profiles, second)
	plain, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(base, "two.json")
	mustWriteFile(t, bundlePath, string(plain))

	setTestHomeDir(t, dstHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dstHome, ".config"))
	dstCfg := filepath.Join(dstHome, ".config", config.AppName)
	seedProfileVault(t, dstCfg, nil, nil)
	useConfigDir(t, dstCfg)
	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{"profile": "all"})
	err = runImport(importCmd, []string{bundlePath})
	if err == nil || !strings.HasPrefix(err.Error(), "1 work dir(s)") {
		t.Fatalf("err = %v\n%s", err, in.String())
	}
	if !strings.Contains(in.String(), "Not imported (1):") {
		t.Errorf("summary count:\n%s", in.String())
	}
	if n := strings.Count(in.String(), "pass --workdir-map '"+filepath.Join(srcHome, "Projects")+"'="); n != 1 {
		t.Errorf("work dir listed %d times:\n%s", n, in.String())
	}
	if strings.Contains(in.String(), "was not found here") {
		t.Errorf("asked without a terminal:\n%s", in.String())
	}
	if strings.Count(in.String(), "Profile \"") != 2 {
		t.Errorf("both profiles not imported:\n%s", in.String())
	}
}

func TestFlagHintQuoting(t *testing.T) {
	for from, want := range map[string]string{
		"/old/Projects":     "--workdir-map '/old/Projects'=<path here>",
		"/old/My Projects":  "--workdir-map '/old/My Projects'=<path here>",
		`C:\Users\a\My Dir`: `--workdir-map 'C:\Users\a\My Dir'=<path here>`,
		`C:\a$(touch x)\b`:  `--workdir-map 'C:\a$(touch x)\b'=<path here>`,
		`C:\a"b`:            `--workdir-map 'C:\a"b'=<path here>`,
		"/a/`id`!":          "--workdir-map '/a/`id`!'=<path here>",
	} {
		if got := flagHint("--workdir-map", from); got != want {
			t.Errorf("flagHint(%q) = %s, want %s", from, got, want)
		}
	}
	for _, from := range []string{"/old/it's", `C:\%USERPROFILE%\x`} {
		got := flagHint("--workdir-map", from)
		if strings.Contains(got, "=<path here>") || !strings.Contains(got, "no ready command") {
			t.Errorf("flagHint(%q) = %s, want no pasteable command", from, got)
		}
	}
}

// A project whose own path is the work dir of a repository nested in it
// keeps its alias; ~/Projects as a project is still refused.
func TestNestedWorkDirKeepsAlias(t *testing.T) {
	bundle := &locations.Set{
		WorkDirs: []locations.WorkDir{{ID: "w1", Abs: "/old/Projects"}, {ID: "w2", Abs: "/old/Projects/app"}},
		Projects: []locations.Project{
			{ID: "p1", WorkDir: "w1", Abs: "/old/Projects/app", GitRemote: "github.com/acme/app"},
			{ID: "p2", WorkDir: "w2", Abs: "/old/Projects/app/lib", GitRemote: "github.com/acme/lib"},
			{ID: "p3", WorkDir: "w1", Abs: "/old/Projects/other", GitRemote: "github.com/acme/other"},
		},
	}
	var wds []string // bundle work dirs only; the vault knows none
	if !safeAlias("/old/Projects/app", bundle.Projects[0], "/home/me", wds, bundle) {
		t.Error("nested repository's work dir took the parent project's alias")
	}
	if safeAlias("/old/Projects", locations.Project{WorkDir: "w1", Abs: "/old/Projects", GitRemote: "github.com/x/y"}, "/home/me", wds, bundle) {
		t.Error("~/Projects accepted as an alias")
	}
	if safeAlias("/old", bundle.Projects[0], "/home/me", wds, bundle) {
		t.Error("alias above work dirs accepted")
	}
	// A work dir at the project's path that also holds something outside it.
	bad := &locations.Set{WorkDirs: bundle.WorkDirs, Projects: append(append([]locations.Project{}, bundle.Projects...), locations.Project{WorkDir: "w2", Abs: "/old/Projects/app"})}
	if safeAlias("/old/Projects/app", bundle.Projects[0], "/home/me", wds, bad) {
		t.Error("work dir holding the project itself accepted")
	}
}

// An answer remembered from an earlier profile is not added again.
func TestRememberedAnswerSavedOnce(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].HomeRel, set.WorkDirs[0].Label = "Code", "Code"
	var out bytes.Buffer
	o.Out, o.Interactive, o.Asked = &out, true, newAskMemo()
	o.In = bufio.NewReader(strings.NewReader("~/src\n\n\n"))
	first, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(first.NewMaps) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(second.NewMaps) != 0 {
		t.Fatalf("second profile saved the answer again: %+v %v", second.NewMaps, err)
	}
	var r locationResult
	r.addNewMap("/a/", "/x")
	r.addNewMap("/a", "/y")
	if len(r.NewMaps) != 1 || r.NewMaps[0].To != "/y" {
		t.Errorf("addNewMap: %+v", r.NewMaps)
	}
}

// The nested exception only covers the bundle's own work dirs, and only
// when every project in them has an identity.
func TestNestedExceptionDoesNotTrustBundle(t *testing.T) {
	p1 := locations.Project{ID: "p1", WorkDir: "w1", Abs: "/home/u/Projects", GitRemote: "github.com/acme/app"}
	bundle := &locations.Set{
		WorkDirs: []locations.WorkDir{{ID: "w1", Abs: "/home/u"}, {ID: "w2", Abs: "/home/u/Projects"}},
		Projects: []locations.Project{p1, {ID: "p2", WorkDir: "w2", Abs: "/home/u/Projects/zz", GitRemote: "github.com/acme/zz"}},
	}
	// Vault knows ~/Projects as a work dir: refused even with a nested layout.
	if safeAlias(p1.Abs, p1, "/other/home", []string{"/home/u/Projects"}, bundle) {
		t.Error("vault work dir accepted as alias")
	}
	// Without the vault entry the nested layout qualifies...
	if !safeAlias(p1.Abs, p1, "/other/home", nil, bundle) {
		t.Error("nested layout refused")
	}
	// ...but not when the nested project has no identity.
	bundle.Projects[1].GitRemote = ""
	if safeAlias(p1.Abs, p1, "/other/home", nil, bundle) {
		t.Error("fake no-identity nested project made the alias acceptable")
	}
}

// Through resolveBundleLocations: a vault-known work dir is passed to safeAlias.
func TestVaultWorkDirRefusedAsAlias(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs = append(set.WorkDirs, locations.WorkDir{ID: "w2", Abs: "/old/Projects/app"})
	set.Projects = append(set.Projects, locations.Project{ID: "p2", WorkDir: "w2", Abs: "/old/Projects/app/lib", GitRemote: "github.com/acme/lib"})
	o.AcceptMatches = true
	o.Known.WorkDirs = []locations.WorkDir{{ID: "w1", Abs: "/old/Projects/app"}}
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Stored.Projects {
		if p.GitRemote == "github.com/acme/app" && len(p.Aliases) != 0 {
			t.Errorf("alias of a vault work dir stored: %+v", p)
		}
	}
}
