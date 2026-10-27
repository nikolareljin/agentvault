package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
)

// fakeClaude records each claude CLI call; a call whose joined args are in
// fail returns an error.
func fakeClaude(t *testing.T, fail ...string) *[]string {
	t.Helper()
	var calls []string
	orig := runClaude
	runClaude = func(_ context.Context, _ io.Reader, _ io.Writer, args ...string) error {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		for _, f := range fail {
			if line == f {
				return errors.New("exit status 1")
			}
		}
		return nil
	}
	t.Cleanup(func() { runClaude = orig })
	return &calls
}

func boolPtr(b bool) *bool { return &b }

// pluginFixture is a bundle and a machine that differ in every way the
// plan knows.
func pluginFixture() (*PluginInventory, localPlugins) {
	inv := &PluginInventory{
		Marketplaces: []PluginMarketplace{
			{Name: "pub", Source: "o/pub", Kind: "github"},
			{Name: "same", Source: "o/same", Kind: "github"},
			{Name: "moved", Source: "o/new", Kind: "github"},
			{Name: "dir", Source: "/home/x/mkt", Kind: "directory"},
		},
		Plugins: []InstalledPlugin{
			{ID: "fresh@pub", Version: "1.0", Scope: "user"},
			{ID: "off@pub", Scope: "user", Enabled: boolPtr(false)},
			{ID: "flip@same", Version: "2.0", Scope: "user", Enabled: boolPtr(false)},
			{ID: "old@same", Version: "3.0", Scope: "user"},
			{ID: "kept@same", Version: "1.0", Scope: "user", Enabled: boolPtr(true)},
			{ID: "proj@same", Scope: "project"},
		},
	}
	local := localPlugins{
		markets:   map[string]string{"same": "o/same", "moved": "o/old"},
		installed: map[string]string{"flip@same": "2.0", "old@same": "2.9", "kept@same": "1.0"},
		enabled:   map[string]bool{"flip@same": true, "kept@same": true},
	}
	return inv, local
}

func TestPlanPluginImport(t *testing.T) {
	inv, local := pluginFixture()
	steps, notes := planPluginImport(inv, local)
	var got []string
	for _, s := range steps {
		line := strings.Join(s.args, " ")
		if s.conflict {
			line += " [conflict]"
		}
		if s.then != nil {
			line += " then " + strings.Join(s.then, " ")
		}
		got = append(got, line)
	}
	want := []string{
		"plugin marketplace add o/pub",
		"plugin install fresh@pub",
		"plugin install off@pub then plugin disable --scope user off@pub",
		"plugin disable --scope user flip@same [conflict]",
		"plugin update old@same [conflict]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	joined := strings.Join(notes, "\n")
	for _, w := range []string{
		`marketplace "same": already here`,
		`marketplace "moved": kept, source here "o/old", in bundle "o/new"`,
		`marketplace "dir": not added, a local directory`,
		`plugin kept@same: already here`,
		`plugin proj@same: not installed, installed for projects only`,
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("notes lack %q:\n%s", w, joined)
		}
	}
	for _, s := range steps {
		if strings.Contains(s.what, "flip@same") && !strings.Contains(s.what, "enabled here, disabled in bundle") {
			t.Errorf("conflict does not show both sides: %q", s.what)
		}
		if strings.Contains(s.what, "old@same") && !strings.Contains(s.what, `"2.9" here, "3.0" in bundle`) {
			t.Errorf("version conflict does not show both sides: %q", s.what)
		}
	}
}

func TestImportPluginsPolicies(t *testing.T) {
	cases := []struct {
		policy  pluginPolicy
		answers string
		want    []string
	}{
		{pluginsPrint, "", nil},
		{pluginsMissing, "", []string{
			"plugin marketplace add o/pub", "plugin install fresh@pub",
			"plugin install off@pub", "plugin disable --scope user off@pub",
		}},
		{pluginsBundle, "", []string{
			"plugin marketplace add o/pub", "plugin install fresh@pub",
			"plugin install off@pub", "plugin disable --scope user off@pub",
			"plugin disable --scope user flip@same", "plugin update old@same",
		}},
		// marketplace yes, fresh no, off yes, flip no, old yes.
		{pluginsAsk, "y\nn\nyes\n\ny\n", []string{
			"plugin marketplace add o/pub",
			"plugin install off@pub", "plugin disable --scope user off@pub",
			"plugin update old@same",
		}},
	}
	for _, c := range cases {
		t.Run(string(c.policy), func(t *testing.T) {
			calls := fakeClaude(t)
			inv, local := pluginFixture()
			var out, prompts bytes.Buffer
			failed, err := importPlugins(context.Background(), inv, local, pluginImportOptions{
				Policy: c.policy, In: bufio.NewReader(strings.NewReader(c.answers)), Out: &out, Err: &prompts,
			})
			if err != nil || failed != 0 {
				t.Fatalf("failed=%d err=%v", failed, err)
			}
			if strings.Join(*calls, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("ran:\n%s\nwant:\n%s\nreport:\n%s", strings.Join(*calls, "\n"), strings.Join(c.want, "\n"), out.String())
			}
			report := out.String()
			switch c.policy {
			case pluginsPrint:
				if !strings.Contains(report, "    claude plugin install fresh@pub") {
					t.Errorf("print did not list the command:\n%s", report)
				}
			case pluginsMissing:
				if !strings.Contains(report, "kept: disable plugin flip@same (enabled here, disabled in bundle) not done") {
					t.Errorf("missing did not report the kept conflict:\n%s", report)
				}
			case pluginsAsk:
				if !strings.Contains(prompts.String(), "install plugin fresh@pub, it can run code on this machine? [y/N]") {
					t.Errorf("install prompt does not warn:\n%s", prompts.String())
				}
				if strings.Contains(prompts.String(), "update plugin old@same to the marketplace's latest (\"2.9\" here, \"3.0\" in bundle), it can") {
					t.Errorf("update prompt warns as an install:\n%s", prompts.String())
				}
			}
		})
	}
}

// A plugin whose marketplace was declined or failed is not attempted, and
// a failed step is counted.
func TestImportPluginsNeedsMarketplace(t *testing.T) {
	inv := &PluginInventory{
		Marketplaces: []PluginMarketplace{{Name: "a", Source: "o/a", Kind: "github"}, {Name: "b", Source: "o/b", Kind: "github"}},
		Plugins:      []InstalledPlugin{{ID: "x@a"}, {ID: "y@b"}, {ID: "z@nowhere"}},
	}
	local := localPlugins{markets: map[string]string{}, installed: map[string]string{}, enabled: map[string]bool{}}

	calls := fakeClaude(t, "plugin marketplace add o/b")
	var out bytes.Buffer
	failed, err := importPlugins(context.Background(), inv, local, pluginImportOptions{
		Policy: pluginsAsk, In: bufio.NewReader(strings.NewReader("n\ny\n")), Out: &out, Err: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1\n%s", failed, out.String())
	}
	if strings.Join(*calls, "\n") != "plugin marketplace add o/b" {
		t.Errorf("ran %q", *calls)
	}
	for _, w := range []string{
		"kept: add marketplace a (o/a) not done",
		"failed: add marketplace b (o/b): exit status 1",
		`not done: install plugin x@a: marketplace "a" is not on this machine`,
		`not done: install plugin y@b: marketplace "b" is not on this machine`,
		`not done: install plugin z@nowhere: marketplace "nowhere" is not on this machine`,
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}
}

func TestResolvePluginPolicy(t *testing.T) {
	for _, c := range []struct {
		flag        string
		interactive bool
		want        pluginPolicy
		err         bool
	}{
		{"", true, pluginsAsk, false},
		{"", false, pluginsPrint, false},
		{"ask", false, "", true},
		{"ask", true, pluginsAsk, false},
		{"missing", false, pluginsMissing, false},
		{"bundle", false, pluginsBundle, false},
		{"all", false, "", true},
	} {
		got, err := resolvePluginPolicy(c.flag, c.interactive)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("resolvePluginPolicy(%q, %v) = %q, %v", c.flag, c.interactive, got, err)
		}
	}
}

// Scope is "user" when any install is, and Enabled comes from the user's
// settings.
func TestPluginInventoryScopeAndEnabled(t *testing.T) {
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"both@m":[{"scope":"project","version":"1","projectPath":"/p"},{"scope":"user","version":"2"}],"proj@m":[{"scope":"local","projectPath":"/p"}],"plain@m":[{"scope":"user"}]}}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"both@m":false,"proj@m":true}}`)
	inv, err := collectClaudePluginInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]InstalledPlugin{}
	for _, p := range inv.Plugins {
		byID[p.ID] = p
	}
	if p := byID["both@m"]; p.Scope != "user" || p.Version != "2" || p.Enabled == nil || *p.Enabled {
		t.Errorf("both@m = %+v", p)
	}
	if p := byID["proj@m"]; p.userScope() || p.Enabled == nil || !*p.Enabled {
		t.Errorf("proj@m = %+v", p)
	}
	if p := byID["plain@m"]; !p.userScope() || p.Enabled != nil {
		t.Errorf("plain@m = %+v", p)
	}
}

// End to end: export carries enabled state, a non-interactive import runs
// nothing by default and installs with --plugins missing.
func TestImportPluginsEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "a", "alex")
	dstHome := filepath.Join(base, "b", "alex")
	mustWriteFile(t, filepath.Join(srcHome, ".claude", "plugins", "known_marketplaces.json"),
		`{"pub":{"source":{"source":"github","repo":"o/pub"}}}`)
	mustWriteFile(t, filepath.Join(srcHome, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"on@pub":[{"scope":"user","version":"1"}],"off@pub":[{"scope":"user","version":"1"}]}}`)
	mustWriteFile(t, filepath.Join(srcHome, ".claude", "settings.json"), `{"enabledPlugins":{"on@pub":true,"off@pub":false}}`)

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)

	bundlePath := filepath.Join(base, "plugins"+DefaultExportExtension)
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
	mustMkdirAll(t, dstHome)

	calls := fakeClaude(t)
	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}
	if len(*calls) != 0 {
		t.Errorf("import without a terminal or --plugins ran %q", *calls)
	}
	if !strings.Contains(in.String(), "claude plugin disable --scope user off@pub") {
		t.Errorf("printed commands lack the bundle's disabled state:\n%s", in.String())
	}

	withFlags(t, importCmd, map[string]string{"plugins": "missing"})
	in.Reset()
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import --plugins missing: %v\n%s", err, in.String())
	}
	want := "plugin marketplace add o/pub\nplugin install off@pub\nplugin disable --scope user off@pub\nplugin install on@pub"
	if strings.Join(*calls, "\n") != want {
		t.Errorf("ran:\n%s\nwant:\n%s\n%s", strings.Join(*calls, "\n"), want, in.String())
	}

	// A failed step makes the import exit non-zero.
	*calls = nil
	runClaude = func(_ context.Context, _ io.Reader, _ io.Writer, args ...string) error {
		return errors.New("exit status 1")
	}
	in.Reset()
	if err := runImport(importCmd, []string{bundlePath}); err == nil || !strings.Contains(err.Error(), "plugin step(s) failed") {
		t.Errorf("failed plugin steps: err = %v\n%s", err, in.String())
	}

	withFlags(t, importCmd, map[string]string{"plugins": "ask"})
	if err := runImport(importCmd, []string{bundlePath}); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Errorf("--plugins ask without a terminal: err = %v", err)
	}
	_ = os.Remove(bundlePath)
}

// A dry run lists the commands and runs none, whatever the policy.
func TestImportPluginsDryRunRunsNothing(t *testing.T) {
	calls := fakeClaude(t)
	inv, local := pluginFixture()
	var out bytes.Buffer
	if _, err := importPlugins(context.Background(), inv, local, pluginImportOptions{Policy: pluginsBundle, DryRun: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("dry run ran %q", *calls)
	}
	if !strings.Contains(out.String(), "    claude plugin update old@same") {
		t.Errorf("dry run did not list the commands:\n%s", out.String())
	}
}

// claude gets the terminal only when a person is answering, and a failed
// disable after an install says the plugin is installed.
func TestImportPluginsTerminalAndPartialFailure(t *testing.T) {
	inv := &PluginInventory{Plugins: []InstalledPlugin{{ID: "off@m", Enabled: boolPtr(false)}}}
	local := localPlugins{markets: map[string]string{"m": "o/m"}, installed: map[string]string{}, enabled: map[string]bool{}}
	tty := strings.NewReader("")
	for _, c := range []struct {
		policy  pluginPolicy
		wantTTY bool
	}{{pluginsAsk, true}, {pluginsMissing, false}, {pluginsBundle, false}} {
		var gotTTY []bool
		orig := runClaude
		runClaude = func(_ context.Context, in io.Reader, _ io.Writer, args ...string) error {
			gotTTY = append(gotTTY, in != nil)
			if args[1] == "disable" {
				return errors.New("exit status 1")
			}
			return nil
		}
		var out bytes.Buffer
		failed, err := importPlugins(context.Background(), inv, local, pluginImportOptions{
			Policy: c.policy, In: bufio.NewReader(strings.NewReader("y\n")), Terminal: tty, Out: &out, Err: io.Discard,
		})
		runClaude = orig
		if err != nil || failed != 1 {
			t.Fatalf("%s: failed=%d err=%v", c.policy, failed, err)
		}
		if len(gotTTY) != 2 || gotTTY[0] != c.wantTTY || gotTTY[1] != c.wantTTY {
			t.Errorf("%s: terminal handed to claude = %v, want %v", c.policy, gotTTY, c.wantTTY)
		}
		if !strings.Contains(out.String(), "failed: install plugin off@m (disabled, as in the bundle): installed, but not disabled: exit status 1") {
			t.Errorf("%s: report:\n%s", c.policy, out.String())
		}
	}
}

func TestImportIncompleteNamesEachPart(t *testing.T) {
	if err := importIncomplete(0, 0); err != nil {
		t.Errorf("nothing missed: %v", err)
	}
	err := importIncomplete(2, 1)
	if err == nil || !strings.Contains(err.Error(), "2 plugin step(s) failed") || !strings.Contains(err.Error(), "1 work dir(s) or project(s) not imported") {
		t.Errorf("err = %v", err)
	}
}
