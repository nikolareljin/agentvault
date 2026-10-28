package cmd

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/locations"
)

// Machine A exports personal and company. Machine B imports only personal:
// B's company tree and memory stay byte-identical, and nothing of A's
// company work dir is written anywhere under B's home.
func TestImportWorkDirsTakesPart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("paths in this test are POSIX")
	}
	base := t.TempDir()
	srcHome := filepath.Join(base, "a", "alex")
	dstHome := filepath.Join(base, "b", "alex")
	srcPersonal := filepath.Join(srcHome, "Projects")
	srcCompany := filepath.Join(srcHome, "work", "acme")
	dstPersonal := filepath.Join(dstHome, "code")
	dstCompany := filepath.Join(dstHome, "corp", "acme")
	for _, d := range []string{srcPersonal, srcCompany, dstPersonal, dstCompany} {
		mustMkdirAll(t, d)
	}
	mem := func(home, dir, body string) string {
		p := filepath.Join(home, ".claude", "projects", claudeProjectSlug(dir), "memory", "MEMORY.md")
		mustWriteFile(t, p, body)
		return p
	}
	mem(srcHome, srcPersonal, "personal note\n")
	mem(srcHome, srcCompany, "company-a secret note\n")
	mem(srcHome, filepath.Join(srcCompany, "api"), "company-a api secret\n")
	dstCompanyMem := mem(dstHome, dstCompany, "machine B company note\n")

	setTestHomeDir(t, srcHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(srcHome, ".config"))
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)
	srcCfg := filepath.Join(srcHome, ".config", config.AppName)
	seedProfileVault(t, srcCfg, nil, nil)
	useConfigDir(t, srcCfg)
	declare(t, srcCfg, map[string]string{"personal": srcPersonal, "company": srcCompany})

	bundlePath := filepath.Join(base, "all"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{
		"yes": "true", "include-secrets": "false", "detect": "false", "include-memory": "true",
	})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	setTestHomeDir(t, dstHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dstHome, ".config"))
	dstCfg := filepath.Join(dstHome, ".config", config.AppName)
	seedProfileVault(t, dstCfg, nil, nil)
	useConfigDir(t, dstCfg)
	declare(t, dstCfg, map[string]string{"personal": dstPersonal, "company": dstCompany})
	before := treeHash(t, dstCompany, filepath.Dir(filepath.Dir(dstCompanyMem)))

	var in bytes.Buffer
	importCmd.SetOut(&in)
	importCmd.SetErr(&in)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{"apply-provider-configs": "true", "accept-matches": "true", "workdirs": "personal"})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}

	got, err := os.ReadFile(filepath.Join(dstHome, ".claude", "projects", claudeProjectSlug(dstPersonal), "memory", "MEMORY.md"))
	if err != nil || string(got) != "personal note\n" {
		t.Fatalf("personal memory not imported: %q %v\n%s", got, err, in.String())
	}
	if after := treeHash(t, dstCompany, filepath.Dir(filepath.Dir(dstCompanyMem))); after != before {
		t.Errorf("import changed machine B's company tree or memory\n%s", in.String())
	}
	_ = filepath.WalkDir(dstHome, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("company-a")) {
			t.Errorf("%s carries the company work dir", p)
		}
		return nil
	})
	if !strings.Contains(in.String(), `work dir company not selected with --workdirs; left out memory folders: 2`) {
		t.Errorf("import does not report what it left out:\n%s", in.String())
	}
}

func TestImportWorkDirsRefusals(t *testing.T) {
	named := func(names ...string) []PortableProfile {
		set := &locations.Set{}
		for i, n := range names {
			set.WorkDirs = append(set.WorkDirs, locations.WorkDir{ID: "w" + string(rune('1'+i)), Abs: "/home/a/" + n + string(rune('0'+i)), Name: n})
		}
		return []PortableProfile{{Name: "default", Setup: SetupBundle{Locations: set}}}
	}
	for _, tc := range []struct {
		name, spec string
		profiles   []PortableProfile
		want       string
	}{
		{"unknown", "home", named("personal", "company"), "no work dir named home (it names: company, personal)"},
		{"no names", "personal", []PortableProfile{{Name: "default"}}, "the bundle names no work dirs"},
		{"empty list", " , ", named("personal"), "names no work dir"},
		{"duplicate", "personal", named("personal", "personal"), "gives the name personal to more than one work dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := filterImportWorkDirs(tc.profiles, tc.spec, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	for _, spec := range []string{"", "all"} {
		if err := filterImportWorkDirs([]PortableProfile{{Name: "default"}}, spec, io.Discard); err != nil {
			t.Errorf("--workdirs %q: %v", spec, err)
		}
	}
}

// A bundle from Windows imported elsewhere: its paths are compared as
// Windows writes them, without case, so nothing under the company work
// dir stays whatever this machine's OS.
func TestImportWorkDirsForeignPaths(t *testing.T) {
	company := `C:\Users\a\Work\Acme`
	setup := SetupBundle{
		SourceOS:   "windows/amd64",
		SourceHome: `C:\Users\a`,
		Locations: &locations.Set{
			WorkDirs: []locations.WorkDir{
				{ID: "w1", Abs: `C:\Users\a\code`, Name: "personal"},
				{ID: "w2", Abs: company, Name: "company"},
			},
			Projects: []locations.Project{
				{ID: "p1", WorkDir: "w1", Abs: `C:\Users\a\code\x`},
				{ID: "p2", WorkDir: "w2", Abs: `c:\users\a\work\acme\api`},
			},
		},
		ProviderFiles: []SetupAsset{
			{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/C--Users-a-Work-Acme-api/memory/MEMORY.md", Content: []byte("x")},
			{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/C--Users-a-code-x/memory/MEMORY.md", Content: []byte("y")},
			{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "CLAUDE.md", Content: []byte(`see ~\Work\Acme\notes`), ContentPresent: true},
		},
		Plugins: &PluginInventory{Plugins: []InstalledPlugin{
			{ID: "only@m", Scope: "local", LocalProjects: []string{`C:\Users\a\Work\Acme\api`}},
			{ID: "both@m", Scope: "local", LocalProjects: []string{`C:\Users\a\Work\Acme\api`, `C:\Users\a\code\x`}, LocalDisabled: []string{`C:\Users\a\Work\Acme\api`}},
		}},
	}
	profiles := []PortableProfile{{Name: "default", Setup: setup}}
	var out bytes.Buffer
	if err := filterImportWorkDirs(profiles, "personal", &out); err != nil {
		t.Fatalf("filter: %v\n%s", err, out.String())
	}
	got := profiles[0].Setup
	var paths []string
	for _, a := range got.ProviderFiles {
		if a.LogicalPath == "CLAUDE.md" && (!a.Redacted || a.Content != nil) {
			t.Errorf("CLAUDE.md names the company work dir and was not withheld")
		}
		paths = append(paths, a.LogicalPath)
	}
	if strings.Join(paths, " ") != "projects/C--Users-a-code-x/memory/MEMORY.md CLAUDE.md" {
		t.Errorf("provider files left: %v", paths)
	}
	if len(got.Locations.Projects) != 1 || got.Locations.Projects[0].ID != "p1" || len(got.Locations.WorkDirs) != 1 {
		t.Errorf("locations left: %+v", got.Locations)
	}
	if len(got.Plugins.Plugins) != 1 || got.Plugins.Plugins[0].ID != "both@m" ||
		strings.Join(got.Plugins.Plugins[0].LocalProjects, " ") != `C:\Users\a\code\x` || len(got.Plugins.Plugins[0].LocalDisabled) != 0 {
		t.Errorf("plugins left: %+v", got.Plugins.Plugins)
	}
}

// macOS compares paths without case, so a bundle from it is filtered the
// same way on Linux: a project or memory folder written with other case
// still belongs to the company work dir.
func TestImportWorkDirsFoldsMacOSCase(t *testing.T) {
	setup := SetupBundle{
		SourceOS:   "darwin/arm64",
		SourceHome: "/Users/a",
		Locations: &locations.Set{
			WorkDirs: []locations.WorkDir{
				{ID: "w1", Abs: "/Users/a/code", Name: "personal"},
				{ID: "w2", Abs: "/Users/a/Work/Acme", Name: "company"},
			},
			Projects: []locations.Project{{ID: "p2", WorkDir: "w2", Abs: "/Users/a/work/acme/api"}},
		},
		ProviderFiles: []SetupAsset{
			{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/-Users-a-work-acme-api/memory/MEMORY.md", Content: []byte("x")},
		},
	}
	profiles := []PortableProfile{{Name: "default", Setup: setup}}
	if err := filterImportWorkDirs(profiles, "personal", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := profiles[0].Setup; len(got.ProviderFiles) != 0 || len(got.Locations.Projects) != 0 {
		t.Errorf("left in: files %v, projects %v", got.ProviderFiles, got.Locations.Projects)
	}
}

func TestImportWorkDirsRefusesMirror(t *testing.T) {
	if err := checkWorkDirsStrategy("personal", strategyMirror); err == nil || !strings.Contains(err.Error(), "--strategy mirror") {
		t.Errorf("mirror with a subset: %v", err)
	}
	for _, tc := range []struct {
		spec     string
		strategy importStrategy
	}{{"all", strategyMirror}, {"", strategyMirror}, {"personal", strategyMerge}, {"personal", strategyReplace}} {
		if err := checkWorkDirsStrategy(tc.spec, tc.strategy); err != nil {
			t.Errorf("%q %s: %v", tc.spec, tc.strategy, err)
		}
	}
}
