package cmd

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/config"
)

func writeCodexHome(t *testing.T, config string, cached ...string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// cached are marketplace/name/version.
	for _, c := range cached {
		if err := os.MkdirAll(filepath.Join(dir, "plugins", "cache", filepath.FromSlash(c)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestCollectCodexPluginInventory(t *testing.T) {
	home := writeCodexHome(t, `model = "m"

[marketplaces.git]
source_type = "git"
source = "https://user:tok@example.com/o/r.git"
ref = "v1"
sparse_paths = [".agents", "plugins/a"]
last_revision = "abc"

[marketplaces.loc]
source_type = "local"
source = "/src/mkt"

[plugins."a@git"]
enabled = false

[plugins."b@loc"]
`, "git/a/1.0.0")
	inv, err := collectCodexPluginInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Marketplaces) != 2 || len(inv.Plugins) != 2 {
		t.Fatalf("inv = %+v", inv)
	}
	g := inv.Marketplaces[0]
	if g.Name != "git" || g.Source != "https://example.com/o/r.git" || g.Ref != "v1" || strings.Join(g.Sparse, ",") != ".agents,plugins/a" {
		t.Errorf("git marketplace = %+v", g)
	}
	if l := inv.Marketplaces[1]; l.Kind != "local" || l.Source != "/src/mkt" {
		t.Errorf("local marketplace = %+v", l)
	}
	a, b := inv.Plugins[0], inv.Plugins[1]
	if a.ID != "a@git" || a.Version != "1.0.0" || *a.Enabled {
		t.Errorf("a = %+v", a)
	}
	// No enabled key: Codex counts it enabled. Not in the cache: no version.
	if b.ID != "b@loc" || b.Version != "" || !*b.Enabled {
		t.Errorf("b = %+v", b)
	}
}

func TestCollectCodexPluginInventoryNone(t *testing.T) {
	if inv, err := collectCodexPluginInventory(writeCodexHome(t, "model = \"m\"\n")); inv != nil || err != nil {
		t.Errorf("inv=%+v err=%v", inv, err)
	}
	if inv, err := collectCodexPluginInventory(t.TempDir()); inv != nil || err != nil {
		t.Errorf("no file: inv=%+v err=%v", inv, err)
	}
	if _, err := collectCodexPluginInventory(writeCodexHome(t, "[x\n")); err == nil {
		t.Error("a damaged config.toml must not export as empty")
	}
}

// A table copied from another machine installs nothing: only the cache
// counts.
func TestReadLocalCodexPluginsNeedsCache(t *testing.T) {
	l, err := readLocalCodexPlugins(writeCodexHome(t, "[plugins.\"a@m\"]\nenabled = true\n[plugins.\"b@m\"]\nenabled = false\n", "m/b/2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.installed["a@m"]; ok {
		t.Error("a@m has no cache and counts as installed")
	}
	if l.installed["b@m"] != "2" || l.enabled["b@m"] {
		t.Errorf("b@m: %+v", l)
	}
}

func TestPlanCodexPluginImport(t *testing.T) {
	off, on := false, true
	inv := &PluginInventory{
		Marketplaces: []PluginMarketplace{
			{Name: "git", Source: "o/r", Kind: "git", Ref: "v1", Sparse: []string{".agents", "plugins/a"}},
			{Name: "badref", Source: "o/r", Kind: "git", Ref: "--upload-pack=x"},
			{Name: "loc", Source: "/src/mkt", Kind: "local"},
			{Name: "placed", Source: "/src/mkt2", Kind: "local", placed: "/here/mkt2"},
			{Name: "same", Source: "x/y", Kind: "git"},
		},
		Plugins: []InstalledPlugin{
			{ID: "new@git", Enabled: &off},
			{ID: "on@git", Enabled: &on},
			{ID: "differs@git", Enabled: &off, Version: "2"},
			{ID: "bad;id@git"},
		},
	}
	local := localPlugins{
		markets:   map[string]string{"same": "x/y"},
		installed: map[string]string{"differs@git": "1"},
		enabled:   map[string]bool{"differs@git": true, "on@git": true},
	}
	steps, notes := planCodexPluginImport(inv, local)
	got := map[string]pluginStep{}
	for _, s := range steps {
		got[s.what] = s
	}
	add := got[`add marketplace from "o/r" (named "git")`]
	if strings.Join(add.args, " ") != "plugin marketplace add --ref v1 --sparse .agents --sparse plugins/a o/r" {
		t.Errorf("git add = %v", add.args)
	}
	if p := got[`add marketplace from "/here/mkt2" (named "placed")`]; strings.Join(p.args, " ") != "plugin marketplace add /here/mkt2" {
		t.Errorf("placed add = %v", p.args)
	}
	inst := got["install plugin new@git (disabled, as in the bundle)"]
	if strings.Join(inst.args, " ") != "plugin add new@git" || inst.setOn == nil || *inst.setOn || inst.market != "git" {
		t.Errorf("install = %+v", inst)
	}
	// In config.toml here, but not in the cache: installed again.
	if _, ok := got["install plugin on@git"]; !ok {
		t.Errorf("on@git not installed: %v", steps)
	}
	dis := got["disable plugin differs@git (enabled here, disabled in bundle)"]
	if dis.args != nil || dis.setOn == nil || *dis.setOn || !dis.conflict {
		t.Errorf("disable = %+v", dis)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{
		`marketplace "badref": not added, its ref or sparse paths are not plain paths`,
		`marketplace "loc": not added, a local directory on the exporting machine ("/src/mkt") that this import did not place`,
		`marketplace "same": already here`,
		`plugin differs@git: version "1" here, "2" in bundle; kept`,
		`plugin "bad;id@git": skipped, not name@marketplace`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes lack %q:\n%s", want, joined)
		}
	}
	if len(steps) != 5 {
		t.Errorf("steps = %d: %+v", len(steps), steps)
	}
}

func TestSetCodexPluginEnabled(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ name, in, want string }{
		{"flip", "model = \"m\"\n\n[plugins.\"a@m\"]\nenabled = true # on\n\n[x]\ny = 1\n",
			"model = \"m\"\n\n[plugins.\"a@m\"]\nenabled = false\n\n[x]\ny = 1\n"},
		{"no key", "[plugins.\"a@m\"]\n[x]\ny = 1\n", "[plugins.\"a@m\"]\nenabled = false\n[x]\ny = 1\n"},
		{"last table, no key", "[plugins.\"a@m\"]", "[plugins.\"a@m\"]\nenabled = false\n"},
		{"no table", "model = \"m\"", "model = \"m\"\n\n[plugins.\"a@m\"]\nenabled = false\n"},
		{"crlf", "[plugins.\"a@m\"]\r\nenabled = true\r\n", "[plugins.\"a@m\"]\r\nenabled = false\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := writeCodexHome(t, tc.in)
			backup, err := setCodexPluginEnabled(home, "a@m", false, at)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(home, ".codex", "config.toml")
			if got, _ := os.ReadFile(dest); string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if old, _ := os.ReadFile(backup); string(old) != tc.in {
				t.Errorf("backup %q = %q", backup, old)
			}
		})
	}
	// A layout the line edit cannot handle is refused and left alone.
	in := "plugins.\"a@m\".enabled = true\n"
	home := writeCodexHome(t, in)
	if _, err := setCodexPluginEnabled(home, "a@m", false, at); err == nil {
		t.Error("dotted key layout: want an error")
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); string(got) != in {
		t.Errorf("refused edit changed the file: %q", got)
	}
	// No config.toml at all.
	empty := t.TempDir()
	if _, err := setCodexPluginEnabled(empty, "a@m", true, at); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(empty, ".codex", "config.toml")); string(got) != "[plugins.\"a@m\"]\nenabled = true\n" {
		t.Errorf("new file = %q", got)
	}
}

func TestKeepLocalCodexPluginTables(t *testing.T) {
	bundle := "model = \"b\"\n\n[marketplaces.m]\nsource_type = \"local\"\nsource = \"/x\"\n\n[plugins.\"a@m\"]\nenabled = true\n\n[mcp_servers.s]\ncommand = \"c\"\n"
	local := "model = \"l\"\n\n[plugins.\"z@o\"]\nenabled = false\n"
	out, changed, ok := keepLocalCodexPluginTables([]byte(bundle), []byte(local))
	if !ok || !changed {
		t.Fatalf("ok=%v changed=%v", ok, changed)
	}
	want := "model = \"b\"\n\n[mcp_servers.s]\ncommand = \"c\"\n\n[plugins.\"z@o\"]\nenabled = false\n"
	if string(out) != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
	// The same tables on both sides: the bundle's text as it is.
	same := "model = \"b\"\n[plugins.\"z@o\"]\nenabled = false\n"
	if out, changed, ok := keepLocalCodexPluginTables([]byte(same), []byte(local)); !ok || changed || string(out) != same {
		t.Errorf("same: %q %v %v", out, changed, ok)
	}
	// Nothing here: the bundle's tables go and none come back.
	out, _, ok = keepLocalCodexPluginTables([]byte(bundle), nil)
	if !ok || strings.Contains(string(out), "plugins") || strings.Contains(string(out), "marketplaces") {
		t.Errorf("no local file: %q %v", out, ok)
	}
	// Dotted keys outside a table cannot be kept apart: refused.
	if _, _, ok := keepLocalCodexPluginTables([]byte("plugins.\"a@m\".enabled = true\n"), []byte(local)); ok {
		t.Error("dotted top-level plugins key: want ok=false")
	}
}

func TestImportCodexPluginsRunsThenSetsEnabled(t *testing.T) {
	var calls []string
	orig := runCodex
	runCodex = func(_ context.Context, _ string, _ io.Reader, _ io.Writer, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { runCodex = orig })
	home := writeCodexHome(t, "model = \"m\"\n")
	off := false
	inv := &PluginInventory{
		Marketplaces: []PluginMarketplace{{Name: "m", Source: "o/r", Kind: "git"}},
		Plugins:      []InstalledPlugin{{ID: "a@m", Enabled: &off}},
	}
	var out bytes.Buffer
	var backups []string
	failed, err := importPlugins(context.Background(), inv, localPlugins{}, pluginImportOptions{
		Tool: codexPluginTool(home), Policy: pluginsBundle, Out: &out, Err: io.Discard, Backups: &backups,
	})
	if err != nil || failed != 0 {
		t.Fatalf("failed=%d err=%v\n%s", failed, err, out.String())
	}
	if strings.Join(calls, "|") != "plugin marketplace add o/r|plugin add a@m" {
		t.Errorf("calls = %v", calls)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(got), "[plugins.\"a@m\"]\nenabled = false\n") || len(backups) != 1 {
		t.Errorf("config = %q backups = %v", got, backups)
	}
	if !strings.HasPrefix(out.String(), "\nCodex plugins:\n") {
		t.Errorf("report:\n%s", out.String())
	}

	// print runs nothing and says how to disable it by hand.
	calls = nil
	out.Reset()
	if _, err := importPlugins(context.Background(), inv, localPlugins{}, pluginImportOptions{
		Tool: codexPluginTool(home), Policy: pluginsPrint, Out: &out, Err: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	want := "    codex plugin add a@m\n    then set enabled = false under [plugins.\"a@m\"] in ~/.codex/config.toml\n"
	if len(calls) != 0 || !strings.Contains(out.String(), want) {
		t.Errorf("calls=%v\n%s", calls, out.String())
	}
}

// Under missing, a conflict (enabled state) is kept; under ask, enabling
// says it can run code.
func TestCodexPluginConflictPolicies(t *testing.T) {
	on := true
	inv := &PluginInventory{Plugins: []InstalledPlugin{{ID: "a@m", Enabled: &on}}}
	local := localPlugins{installed: map[string]string{"a@m": "1"}, enabled: map[string]bool{"a@m": false}}
	home := writeCodexHome(t, "[plugins.\"a@m\"]\nenabled = false\n")
	var out bytes.Buffer
	if _, err := importPlugins(context.Background(), inv, local, pluginImportOptions{Tool: codexPluginTool(home), Policy: pluginsMissing, Out: &out, Err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "kept: enable plugin a@m (disabled here, enabled in bundle) not done") {
		t.Errorf("missing:\n%s", out.String())
	}
	var prompt bytes.Buffer
	out.Reset()
	if _, err := importPlugins(context.Background(), inv, local, pluginImportOptions{Tool: codexPluginTool(home), Policy: pluginsAsk,
		In: bufio.NewReader(strings.NewReader("y\n")), Out: &out, Err: &prompt}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.String(), "it can run code on this machine") {
		t.Errorf("prompt = %q", prompt.String())
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); !strings.Contains(string(got), "enabled = true") {
		t.Errorf("config = %q", got)
	}
}

func TestApplyDropsCodexMarketplaceInUnselectedWorkDir(t *testing.T) {
	s, home, acme := companyScope(t)
	b := SetupBundle{CodexPlugins: &PluginInventory{
		Marketplaces: []PluginMarketplace{{Name: "own", Source: filepath.Join(acme, "mp"), Kind: "local"}, {Name: "pub", Source: "o/r", Kind: "git"}},
		Plugins:      []InstalledPlugin{{ID: "x@own", Scope: "user"}, {ID: "y@pub", Scope: "user"}},
	}}
	if _, err := s.apply(&b, home); err != nil {
		t.Fatal(err)
	}
	if len(b.CodexPlugins.Marketplaces) != 1 || len(b.CodexPlugins.Plugins) != 1 || b.CodexPlugins.Plugins[0].ID != "y@pub" {
		t.Errorf("codex = %+v", b.CodexPlugins)
	}
}

func TestPlaceCodexMarketplaces(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "corp")
	mustMkdirAll(t, filepath.Join(root, "mkt"))
	mustMkdirAll(t, filepath.Join(base, "elsewhere", "mkt"))
	inv := &PluginInventory{Marketplaces: []PluginMarketplace{
		{Name: "in", Source: "/src/corp/mkt", Kind: "local"},
		{Name: "out", Source: "/src/elsewhere/mkt", Kind: "local"},
		{Name: "git", Source: "/src/corp/mkt", Kind: "git"},
	}}
	remaps := []pathRemap{{From: "/src/corp", To: root}, {From: "/src/elsewhere", To: filepath.Join(base, "elsewhere")}}
	got := placeCodexMarketplaces(inv, remaps, []string{root})
	if got.Marketplaces[0].placed != filepath.Join(root, "mkt") {
		t.Errorf("in: %+v", got.Marketplaces[0])
	}
	// Rewritten, but not under a confirmed place: not placed.
	if got.Marketplaces[1].placed != "" || got.Marketplaces[2].placed != "" {
		t.Errorf("out/git placed: %+v", got.Marketplaces)
	}
	if inv.Marketplaces[0].placed != "" {
		t.Error("the input was changed")
	}
}

// The bundle's config.toml is applied with this machine's plugin tables,
// next to a per-server MCP pick.
func TestApplyCodexConfigKeepsLocalPluginTables(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".codex", "config.toml")
	mustWriteFile(t, cfg, "model = \"a\"\n\n[plugins.\"z@o\"]\nenabled = false\n")
	bundle := "model = \"a\"\n\n[marketplaces.m]\nsource_type = \"git\"\nsource = \"o/r\"\n\n[plugins.\"a@m\"]\nenabled = true\n\n[mcp_servers.s]\ncommand = \"c\"\n"
	ctx := fixedCtx("")
	ctx.MCPServers, _ = parseMCPSelection("s")
	_, warnings, err := applyProviderAssetsWith(home, []SetupAsset{codexAsset(bundle)}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := readString(t, cfg)
	if !strings.Contains(got, "[plugins.\"z@o\"]\nenabled = false") || strings.Contains(got, "a@m") || strings.Contains(got, "marketplaces") ||
		!strings.Contains(got, "[mcp_servers.s]") {
		t.Errorf("config:\n%s", got)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), `kept this machine's [plugins] and [marketplaces]; the bundle's are under "Codex plugins"`) {
		t.Errorf("warnings = %v", warnings)
	}
}

// Export records the Codex plugins; import without a terminal prints the
// codex commands and changes nothing.
func TestCodexPluginsExportImport(t *testing.T) {
	base := t.TempDir()
	srcHome, dstHome := filepath.Join(base, "a"), filepath.Join(base, "b")
	mustWriteFile(t, filepath.Join(srcHome, ".codex", "config.toml"),
		"[marketplaces.m]\nsource_type = \"git\"\nsource = \"o/r\"\nref = \"v1\"\n\n[plugins.\"a@m\"]\nenabled = false\n")
	mustMkdirAll(t, filepath.Join(srcHome, ".codex", "plugins", "cache", "m", "a", "1.0.0"))
	mustWriteFile(t, filepath.Join(dstHome, ".codex", "config.toml"), "model = \"b\"\n")

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
	withFlags(t, exportCmd, map[string]string{"yes": "true", "include-secrets": "false", "detect": "false", "include-provider-files": "true"})
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
	withFlags(t, importCmd, map[string]string{"plugins": "print"})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}
	for _, want := range []string{"Codex plugins:", "    codex plugin marketplace add --ref v1 o/r\n",
		"    codex plugin add a@m\n    then set enabled = false under [plugins.\"a@m\"] in ~/.codex/config.toml\n"} {
		if !strings.Contains(in.String(), want) {
			t.Errorf("%q missing:\n%s", want, in.String())
		}
	}
	if got := readString(t, filepath.Join(dstHome, ".codex", "config.toml")); got != "model = \"b\"\n" {
		t.Errorf("print changed config.toml: %q", got)
	}
}
