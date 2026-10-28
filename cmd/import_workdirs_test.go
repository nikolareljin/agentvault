package cmd

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
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
	report := in.String()
	if i, h := strings.Index(report, `work dir company not selected with --workdirs; left out memory folders: 2`), strings.Index(report, "Importing "); i < 0 || h < 0 || i < h {
		t.Errorf("import does not report what it left out after its header:\n%s", report)
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
			_, err := filterImportWorkDirs(tc.profiles, tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	for _, spec := range []string{"", "all"} {
		if _, err := filterImportWorkDirs([]PortableProfile{{Name: "default"}}, spec); err != nil {
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
	lines, err := filterImportWorkDirs(profiles, "personal")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if want := `Profile "default": work dir company not selected with --workdirs; left out files naming it (withheld): 1, memory folders: 1, plugins: 1, projects: 1`; strings.Join(lines, "\n") != want {
		t.Errorf("lines = %q, want %q", lines, want)
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
	if _, err := filterImportWorkDirs(profiles, "personal"); err != nil {
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

func TestImportWorkDirMapMustBeSelected(t *testing.T) {
	err := checkWorkDirMapsSelected("personal", map[string]string{"company": "/x", "personal": "/y"})
	if err == nil || !strings.Contains(err.Error(), "--workdir-map company: --workdirs personal leaves that work dir out") {
		t.Errorf("err = %v", err)
	}
	if err := checkWorkDirMapsSelected("personal", map[string]string{"personal": "/y"}); err != nil {
		t.Error(err)
	}
	if err := checkWorkDirMapsSelected("all", map[string]string{"company": "/x"}); err != nil {
		t.Error(err)
	}
}

// An instruction pulled from a company project names it by identity, not
// path, so only the bundle's own project list ties it to the work dir.
func TestImportWorkDirsInstructionByIdentity(t *testing.T) {
	setup := SetupBundle{
		SourceOS:   "linux/amd64",
		SourceHome: "/home/a",
		Locations: &locations.Set{
			WorkDirs: []locations.WorkDir{
				{ID: "w1", Abs: "/home/a/code", Name: "personal"},
				{ID: "w2", Abs: "/home/a/work/acme", Name: "company"},
			},
			Projects: []locations.Project{
				{ID: "p1", WorkDir: "w1", Abs: "/home/a/code/site", GitRemote: "https://example.com/me/site.git"},
				{ID: "p2", WorkDir: "w2", Abs: "/home/a/work/acme/api", GitRemote: "https://example.com/acme/api.git"},
			},
		},
	}
	setup.SharedConfig.Instructions = []agent.InstructionFile{
		{Name: "docs/company.md", Filename: "docs/company.md", Content: "company rules", SourceProject: "remote:https://example.com/acme/api.git"},
		{Name: "docs/site.md", Filename: "docs/site.md", Content: "site rules", SourceProject: "remote:https://example.com/me/site.git"},
	}
	profiles := []PortableProfile{{Name: "default", Setup: setup}}
	if _, err := filterImportWorkDirs(profiles, "personal"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, inst := range profiles[0].Setup.SharedConfig.Instructions {
		names = append(names, inst.Name)
	}
	if strings.Join(names, " ") != "docs/site.md" {
		t.Errorf("instructions left: %v", names)
	}
}

func TestImportWorkDirsNotesUnnamedProfile(t *testing.T) {
	set := &locations.Set{WorkDirs: []locations.WorkDir{{ID: "w1", Abs: "/home/a/code", Name: "personal"}}}
	profiles := []PortableProfile{{Name: "named", Setup: SetupBundle{Locations: set}}, {Name: "plain"}}
	lines, err := filterImportWorkDirs(profiles, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if want := `Profile "plain": names no work dirs; --workdirs does not apply, all of it is imported`; strings.Join(lines, "\n") != want {
		t.Errorf("lines = %q", lines)
	}
}

// A bundle exported with --project inside the company work dir: importing
// personal leaves the project and its files out instead of refusing.
func TestImportWorkDirsDropsSourceProject(t *testing.T) {
	setup := SetupBundle{
		SourceOS: "linux/amd64", SourceHome: "/home/a", SourceProject: "/home/a/work/acme/api",
		Locations: &locations.Set{WorkDirs: []locations.WorkDir{
			{ID: "w1", Abs: "/home/a/code", Name: "personal"},
			{ID: "w2", Abs: "/home/a/work/acme", Name: "company"},
		}},
		ProjectFiles:         []SetupAsset{{LogicalRoot: setupAssetRootProject, LogicalPath: "CLAUDE.md", Content: []byte("acme rules"), ContentPresent: true}},
		InstructionOverrides: []SetupAsset{{LogicalRoot: setupAssetRootProject, LogicalPath: "AGENTS.md", Content: []byte("acme agents"), ContentPresent: true}},
		SkillAssets: []SetupAsset{
			{LogicalRoot: setupAssetRootProject, LogicalPath: "skills/deploy/SKILL.md", Content: []byte("acme deploy"), ContentPresent: true},
			{LogicalRoot: setupAssetRootProviderClaudeSkill, LogicalPath: "notes/SKILL.md", Content: []byte("mine"), ContentPresent: true},
		},
	}
	profiles := []PortableProfile{{Name: "default", Setup: setup}}
	lines, err := filterImportWorkDirs(profiles, "personal")
	if err != nil {
		t.Fatal(err)
	}
	got := profiles[0].Setup
	if got.SourceProject != "" || len(got.ProjectFiles) != 0 || len(got.InstructionOverrides) != 0 || len(got.SkillAssets) != 1 || got.SkillAssets[0].LogicalPath != "notes/SKILL.md" {
		t.Errorf("left in: %q %v %v %v", got.SourceProject, got.ProjectFiles, got.InstructionOverrides, got.SkillAssets)
	}
	if want := `Profile "default": work dir company not selected with --workdirs; left out project files: 3, projects: 1`; strings.Join(lines, "\n") != want {
		t.Errorf("lines = %q", lines)
	}
	// The same project recorded in the bundle's locations is one project.
	setup.Locations = &locations.Set{
		WorkDirs: []locations.WorkDir{
			{ID: "w1", Abs: "/home/a/code", Name: "personal"},
			{ID: "w2", Abs: "/home/a/work/acme", Name: "company"},
		},
		Projects: []locations.Project{{ID: "p1", WorkDir: "w2", Abs: "/home/a/work/acme/api"}},
	}
	profiles = []PortableProfile{{Name: "default", Setup: setup}}
	if lines, err = filterImportWorkDirs(profiles, "personal"); err != nil || !strings.HasSuffix(strings.Join(lines, ""), "projects: 1") {
		t.Errorf("lines = %q, err = %v", lines, err)
	}
}

// A Windows bundle read on any OS: settings and MCP arguments often write
// its paths with forward slashes.
func TestImportWorkDirsWindowsForwardSlashes(t *testing.T) {
	setup := SetupBundle{
		SourceOS: "windows/amd64", SourceHome: `C:\Users\a`,
		Locations: &locations.Set{WorkDirs: []locations.WorkDir{
			{ID: "w1", Abs: `C:\Users\a\code`, Name: "personal"},
			{ID: "w2", Abs: `C:\Users\a\Work\Acme`, Name: "company"},
		}},
		ProviderFiles: []SetupAsset{{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json",
			Content: []byte(`{"additionalDirectories":["C:/Users/a/Work/Acme/api"]}`), ContentPresent: true}},
	}
	setup.SharedConfig.MCPServers = []agent.MCPServer{
		{Name: "acme", Args: []string{"C:/Users/a/Work/Acme/tool"}},
		{Name: "mine", Args: []string{"C:/Users/a/code/tool"}},
	}
	profiles := []PortableProfile{{Name: "default", Setup: setup}}
	if _, err := filterImportWorkDirs(profiles, "personal"); err != nil {
		t.Fatal(err)
	}
	got := profiles[0].Setup
	if a := got.ProviderFiles[0]; !a.Redacted || a.Content != nil {
		t.Errorf("settings.json kept: %s", a.Content)
	}
	if len(got.SharedConfig.MCPServers) != 1 || got.SharedConfig.MCPServers[0].Name != "mine" {
		t.Errorf("MCP servers left: %+v", got.SharedConfig.MCPServers)
	}
}

func TestImportWorkDirsNameWithoutPath(t *testing.T) {
	set := &locations.Set{WorkDirs: []locations.WorkDir{{ID: "w1", Abs: "/home/a/code", Name: "personal"}, {ID: "w2", Name: "company"}}}
	_, err := filterImportWorkDirs([]PortableProfile{{Name: "default", Setup: SetupBundle{Locations: set}}}, "company")
	if err == nil || !strings.Contains(err.Error(), "no work dir named company") {
		t.Errorf("err = %v", err)
	}
}
