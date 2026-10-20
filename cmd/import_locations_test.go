package cmd

import (
	"bufio"
	"bytes"
	"context"
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
	_, err := resolveBundleLocations(context.Background(), set, "", o)
	if err == nil || !strings.Contains(out.String(), "--workdir-map /old/Projects=") || !strings.Contains(out.String(), "maybe: ") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	o.WorkDirMaps = map[string]string{"/old/Projects": filepath.Join(o.Home, "src")}
	o.AcceptMatches = true
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Remaps) != 2 {
		t.Fatalf("with --workdir-map: %+v %v", res, err)
	}
	o.WorkDirMaps = nil
	o.Interactive = true
	o.In = bufio.NewReader(strings.NewReader("~/src\n\n"))
	res, err = resolveBundleLocations(context.Background(), set, "", o)
	if err != nil || len(res.Remaps) != 2 {
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
	if err := runImport(importCmd, []string{bundlePath}); err == nil || !strings.Contains(in.String(), "--workdir-map "+filepath.Join(srcHome, "Projects")+"=") {
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
	if safeAlias(self.Abs, self, "/home/me", []locations.Project{other}) {
		t.Error("alias equal to another repository's alias accepted")
	}
	if safeAlias(self.Abs, self, "/home/me", nil, []locations.Project{{Abs: "/old/Projects/app/sub", GitRemote: "github.com/x/y"}}) {
		t.Error("alias above a project accepted earlier in this import")
	}
	if safeAlias("/old/Projects/app/sub", self, "/home/me", []locations.Project{{Abs: "/old/Projects/app", GitRemote: "github.com/x/y"}}) {
		t.Error("alias below another repository accepted")
	}
	same := locations.Project{Abs: "/home/me/src/app", GitRemote: "github.com/acme/app", Aliases: []string{"/old/Projects/app"}}
	if !safeAlias(self.Abs, self, "/home/me", []locations.Project{same}) {
		t.Error("same repository refused its own alias")
	}
	if safeAlias("/home", self, "/home/me", nil) || safeAlias("/home/me", self, "/home/me", nil) {
		t.Error("home or above accepted")
	}
	if !safeAlias("/old/Projects/app", self, "/home/me", nil) {
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
