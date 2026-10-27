package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Local installs are recorded with their project; project installs are not:
// the repository carries them.
func TestPluginInventoryLocalProjects(t *testing.T) {
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"a@m":[{"scope":"local","projectPath":"/p/one"},{"scope":"project","projectPath":"/p/two"},{"scope":"local","projectPath":"/p/three"}]}}`)
	inv, err := collectClaudePluginInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(inv.Plugins[0].LocalProjects, ","); got != "/p/one,/p/three" {
		t.Errorf("LocalProjects = %q", got)
	}
}

// A plugin that stays in the bundle keeps only its local installs in
// selected work dirs.
func TestApplyPluginsFiltersLocalProjects(t *testing.T) {
	s, home, acme := companyScope(t)
	mine := filepath.Join(home, "Projects", "x")
	b := SetupBundle{Plugins: &PluginInventory{Plugins: []InstalledPlugin{{
		ID: "a@m", Scope: "user", LocalProjects: []string{filepath.Join(acme, "r"), mine},
		LocalDisabled: []string{filepath.Join(acme, "r"), mine},
		installs:      []pluginInstall{{scope: "user"}, {scope: "local", projectPath: filepath.Join(acme, "r")}, {scope: "local", projectPath: mine}},
	}}}}
	if _, err := s.apply(&b, home); err != nil {
		t.Fatal(err)
	}
	if got := b.Plugins.Plugins[0].LocalProjects; len(got) != 1 || got[0] != mine {
		t.Errorf("LocalProjects = %q", got)
	}
	if got := b.Plugins.Plugins[0].LocalDisabled; len(got) != 1 || got[0] != mine {
		t.Errorf("LocalDisabled = %q", got)
	}
}

func TestPlacePluginProjects(t *testing.T) {
	base := t.TempDir()
	confirmed := filepath.Join(base, "code")
	inside := filepath.Join(confirmed, "app")
	homeOnly := filepath.Join(base, "home", "other")
	mustMkdirAll(t, inside)
	mustMkdirAll(t, homeOnly)
	inv := &PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", LocalProjects: []string{
		"/src/Projects/app", "/src/home/other", "/src/Projects/gone",
	}}}}
	remaps := []pathRemap{{From: "/src/Projects", To: confirmed}, {From: "/src/home", To: filepath.Join(base, "home")}}
	got := placePluginProjects(inv, remaps, []string{confirmed})
	p := got.Plugins[0]
	if strings.Join(p.placed, ",") != inside {
		t.Errorf("placed = %q", p.placed)
	}
	// The home rewrite alone is no confirmation; a missing dir is not placed.
	if strings.Join(p.unplaced, ",") != "/src/home/other,/src/Projects/gone" {
		t.Errorf("unplaced = %q", p.unplaced)
	}
	if inv.Plugins[0].placed != nil {
		t.Error("placePluginProjects changed its input")
	}
}

func TestPlanLocalInstalls(t *testing.T) {
	inv := &PluginInventory{Plugins: []InstalledPlugin{{
		ID: "a@m", Scope: "local", LocalProjects: []string{"/s/one", "/s/two", "/s/three"},
		placed: []string{"/t/one", "/t/two"}, unplaced: []string{"/s/three"},
	}}}
	local := localPlugins{markets: map[string]string{"m": "o/m"}, localAt: map[string][]string{"a@m": {"/t/two"}}}
	steps, notes := planPluginImport(inv, local)
	if len(steps) != 1 || steps[0].dir != "/t/one" || strings.Join(steps[0].args, " ") != "plugin install --scope local a@m" {
		t.Errorf("steps = %+v", steps)
	}
	joined := strings.Join(notes, "\n")
	for _, w := range []string{
		`plugin a@m for project "/t/two": already here`,
		`plugin a@m for project "/s/three": not installed, the project was not placed by this import`,
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("notes lack %q:\n%s", w, joined)
		}
	}
	if strings.Contains(joined, "projects only") || strings.Contains(joined, "project scope") {
		t.Errorf("a local-only plugin reported as project scope:\n%s", joined)
	}

	calls := fakeClaude(t)
	var out bytes.Buffer
	if _, err := importPlugins(context.Background(), inv, local, pluginImportOptions{Policy: pluginsPrint, Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "    (cd /t/one && claude plugin install --scope local a@m)") {
		t.Errorf("print:\n%s", out.String())
	}
	if _, err := importPlugins(context.Background(), inv, local, pluginImportOptions{Policy: pluginsMissing, Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "\n") != "in /t/one: plugin install --scope local a@m" {
		t.Errorf("ran %q", *calls)
	}
}

// Two profiles with the same plugin bring both profiles' local installs.
func TestMergePluginInventoriesUnionsLocal(t *testing.T) {
	a := &PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", placed: []string{"/t/1"}}}}
	b := &PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", placed: []string{"/t/1", "/t/2"}, unplaced: []string{"/s/3"}, placedOff: []string{"/t/2"}}}}
	got := mergePluginInventories([]*PluginInventory{a, b}).Plugins[0]
	if strings.Join(got.placed, ",") != "/t/1,/t/2" || strings.Join(got.unplaced, ",") != "/s/3" || strings.Join(got.placedOff, ",") != "/t/2" {
		t.Errorf("merged = %+v", got)
	}
}

// End to end: a local install follows its project to the place --map gives
// it, and only there.
func TestImportLocalPluginEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "a", "alex")
	dstHome := filepath.Join(base, "b", "alex")
	srcApp := filepath.Join(srcHome, "Projects", "app")
	dstApp := filepath.Join(dstHome, "code", "app")
	mustMkdirAll(t, srcApp)
	mustMkdirAll(t, dstApp)
	mustWriteFile(t, filepath.Join(srcHome, ".claude", "plugins", "known_marketplaces.json"),
		`{"pub":{"source":{"source":"github","repo":"o/pub"}}}`)
	mustWriteFile(t, filepath.Join(srcHome, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"loc@pub":[{"scope":"local","version":"1","projectPath":"`+srcApp+`"}]}}`)

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)
	bundlePath := filepath.Join(base, "local"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{"yes": "true", "include-secrets": "false", "detect": "false"})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	setTestHomeDir(t, dstHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dstHome, ".config"))
	dstCfg := filepath.Join(dstHome, ".config", config.AppName)
	seedProfileVault(t, dstCfg, nil, nil)
	useConfigDir(t, dstCfg)
	// The home rewrite alone would point at dstHome/Projects/app.
	mustMkdirAll(t, filepath.Join(dstHome, "Projects", "app"))

	calls := fakeClaude(t)
	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{"plugins": "missing", "no-match": "true"})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}
	if strings.Contains(strings.Join(*calls, "\n"), "--scope local") {
		t.Errorf("installed for a project nobody placed: %q", *calls)
	}
	if !strings.Contains(in.String(), "not installed, the project was not placed by this import") {
		t.Errorf("report:\n%s", in.String())
	}

	*calls = nil
	in.Reset()
	resetStringArray(t, importCmd, "map", []string{srcApp + "=" + dstApp})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import --map: %v\n%s", err, in.String())
	}
	want := "plugin marketplace add o/pub\nin " + dstApp + ": plugin install --scope local loc@pub"
	if strings.Join(*calls, "\n") != want {
		t.Errorf("ran:\n%s\nwant:\n%s\n%s", strings.Join(*calls, "\n"), want, in.String())
	}
}

// resetStringArray sets a string array flag to vals and empties it after the
// test; withFlags cannot, since Set appends.
func resetStringArray(t *testing.T, c *cobra.Command, name string, vals []string) {
	t.Helper()
	sv := c.Flags().Lookup(name).Value.(pflag.SliceValue)
	if err := sv.Replace(vals); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sv.Replace(nil) })
}

// A bundle from before local_projects says only the scope.
func TestPlanOldBundleLocalScope(t *testing.T) {
	_, notes := planPluginImport(&PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", Scope: "local"}}}, localPlugins{})
	if len(notes) != 1 || notes[0] != "plugin a@m: not installed, installed for projects only (scope local)" {
		t.Errorf("notes = %q", notes)
	}
}

// The real runner runs claude in the step's directory, with the args as
// given and no shell.
func TestRunClaudeUsesDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	bin, dir := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(bin, "claude"), "#!/bin/sh\npwd\nprintf '%s\\n' \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var out bytes.Buffer
	if err := runClaude(context.Background(), dir, nil, &out, "plugin", "install", "--scope", "local", "a@m;x"); err != nil {
		t.Fatal(err)
	}
	want := dir + "\nplugin\ninstall\n--scope\nlocal\na@m;x\n"
	if got, _ := filepath.EvalSymlinks(strings.SplitN(out.String(), "\n", 2)[0]); got != mustEval(t, dir) || !strings.HasSuffix(out.String(), "\nplugin\ninstall\n--scope\nlocal\na@m;x\n") {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// An accepted match and --project-root both confirm a place.
func TestProfilePluginsConfirmedPlaces(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "b")
	setTestHomeDir(t, home)
	matched, root := filepath.Join(base, "m", "app"), filepath.Join(base, "r", "site")
	mustMkdirAll(t, matched)
	mustMkdirAll(t, root)
	setup := SetupBundle{SourceHome: "/src", SourceProject: "/src/site", Plugins: &PluginInventory{Plugins: []InstalledPlugin{
		{ID: "a@m", Scope: "local", LocalProjects: []string{"/src/app"}},
		{ID: "b@m", Scope: "local", LocalProjects: []string{"/src/site"}},
	}}}
	got := profilePlugins(setup, importAssetOptions{Computed: []pathRemap{{From: "/src/app", To: matched}}, ProjectRoot: root})
	if p := got.Plugins[0]; strings.Join(p.placed, ",") != matched {
		t.Errorf("matched: %+v", p)
	}
	if p := got.Plugins[1]; strings.Join(p.placed, ",") != root {
		t.Errorf("project root: %+v", p)
	}
	none := profilePlugins(setup, importAssetOptions{})
	for _, p := range none.Plugins {
		if len(p.placed) != 0 {
			t.Errorf("placed with nothing confirmed: %+v", p)
		}
	}
}

// A link inside a confirmed project does not place a project outside it.
func TestPlacePluginProjectsRefusesLinkOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	root, outside := filepath.Join(base, "code"), filepath.Join(base, "elsewhere")
	mustMkdirAll(t, root)
	mustMkdirAll(t, outside)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	inv := &PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", LocalProjects: []string{"/src/link", "/src/x/../../elsewhere"}}}}
	got := placePluginProjects(inv, []pathRemap{{From: "/src", To: root}}, []string{root})
	if p := got.Plugins[0]; len(p.placed) != 0 || len(p.unplaced) != 2 {
		t.Errorf("placed = %q, unplaced = %q", p.placed, p.unplaced)
	}
}

// A plugin disabled for one project travels disabled, and only there.
func TestLocalDisabledRoundTrip(t *testing.T) {
	home, base := t.TempDir(), t.TempDir()
	on, off := filepath.Join(base, "on"), filepath.Join(base, "off")
	mustMkdirAll(t, on)
	mustWriteFile(t, filepath.Join(off, ".claude", "settings.local.json"), `{"enabledPlugins":{"a@m":false,"b@m":true}}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"a@m":[{"scope":"local","projectPath":`+jsonString(on)+`},{"scope":"local","projectPath":`+jsonString(off)+`}]}}`)
	inv, err := collectClaudePluginInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := inv.Plugins[0].LocalDisabled; len(got) != 1 || got[0] != off {
		t.Fatalf("LocalDisabled = %q", got)
	}

	dst := t.TempDir()
	mustMkdirAll(t, filepath.Join(dst, "on"))
	mustMkdirAll(t, filepath.Join(dst, "off"))
	placed := placePluginProjects(inv, []pathRemap{{From: base, To: dst}}, []string{dst})
	calls := fakeClaude(t)
	var out bytes.Buffer
	local := localPlugins{markets: map[string]string{"m": "o/m"}}
	if _, err := importPlugins(context.Background(), placed, local, pluginImportOptions{Policy: pluginsPrint, Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	offDst := shellQuote(filepath.Join(dst, "off"))
	if !strings.Contains(out.String(), "    (cd "+offDst+" && claude plugin disable --scope local a@m)") {
		t.Errorf("print lacks the local disable:\n%s", out.String())
	}
	if _, err := importPlugins(context.Background(), placed, local, pluginImportOptions{Policy: pluginsMissing, Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	want := "in " + filepath.Join(dst, "on") + ": plugin install --scope local a@m\n" +
		"in " + filepath.Join(dst, "off") + ": plugin install --scope local a@m\n" +
		"in " + filepath.Join(dst, "off") + ": plugin disable --scope local a@m"
	if strings.Join(*calls, "\n") != want {
		t.Errorf("ran:\n%s\nwant:\n%s", strings.Join(*calls, "\n"), want)
	}
}
