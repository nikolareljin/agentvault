package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// twoWorkDirs is a home with a personal work dir (Projects) and an
// employer's (work/acme), an undeclared sibling (Projects-acme), and the
// traces each leaves: Claude memory, plugins, Codex trust, sessions,
// directory instructions and recorded projects.
type twoWorkDirs struct {
	v                         *vault.Vault
	cfgDir, home              string
	personal, acme, acmeRepo  string
	personalRepo, siblingRepo string
}

func setupTwoWorkDirs(t *testing.T) twoWorkDirs {
	t.Helper()
	v, root := sessionTestVault(t, nil)
	w := twoWorkDirs{v: v, cfgDir: filepath.Join(root, config.AppName), home: root}
	w.personal = filepath.Join(root, "Projects")
	w.personalRepo = filepath.Join(w.personal, "site")
	w.acme = filepath.Join(root, "work", "acme")
	w.acmeRepo = filepath.Join(w.acme, "team", "billing.api")
	w.siblingRepo = filepath.Join(root, "Projects-acme", "x")
	for _, d := range []string{w.personalRepo, w.acmeRepo, w.siblingRepo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mem := func(dir string) {
		write(filepath.Join(root, ".claude", "projects", claudeProjectSlug(dir), "memory", "MEMORY.md"), "- note\n")
	}
	mem(w.personal)
	mem(w.personalRepo)
	mem(w.acme)
	mem(w.acmeRepo)
	mem(filepath.Join(w.acme, "deleted")) // a checkout that is gone
	mem(w.siblingRepo)

	write(filepath.Join(root, ".claude", "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"acme-only@m":[{"scope":"project","projectPath":`+jsonString(w.acmeRepo)+`,"version":"1"}],
		"both@m":[{"scope":"project","projectPath":`+jsonString(w.acmeRepo)+`},{"scope":"local","projectPath":`+jsonString(w.personalRepo)+`}],
		"user@m":[{"scope":"user","projectPath":`+jsonString(w.acmeRepo)+`}],
		"site@m":[{"scope":"project","projectPath":`+jsonString(w.personalRepo)+`}]}}`)
	write(filepath.Join(root, ".codex", "config.toml"), `model = "gpt"
# trusted checkouts
[projects.`+tomlString(w.personalRepo)+`]
trust_level = "trusted"

[projects.`+tomlString(w.acmeRepo)+`]
trust_level = "trusted"

[profiles.fast]
model = "mini"
`)

	set, err := v.Locations().Declare("personal", w.personal)
	if err != nil {
		t.Fatal(err)
	}
	if set, err = set.Declare("company", w.acme); err != nil {
		t.Fatal(err)
	}
	set.Projects = []locations.Project{
		{ID: "p1", Abs: w.personalRepo, GitRemote: "github.com/me/site"},
		{ID: "p2", Abs: w.acmeRepo, GitRemote: "github.com/acme-corp/billing"},
	}
	if err := v.SetLocations(set); err != nil {
		t.Fatal(err)
	}
	if err := v.SetCodexConfig(&agent.CodexConfig{TrustedProjects: map[string]string{w.personalRepo: "trusted", w.acmeRepo: "trusted"}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []agent.Session{{ID: "s1", Name: "site", ProjectDir: w.personalRepo}, {ID: "s2", Name: "billing", ProjectDir: w.acmeRepo}} {
		if err := v.AddSession(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, inst := range []agent.InstructionFile{
		{Name: "site-rules", Filename: "AGENTS.md", Content: "x", Scope: "directory", DirectoryPattern: w.personal + "/**"},
		{Name: "acme-rules", Filename: "AGENTS.md", Content: "y", Scope: "directory", DirectoryPattern: w.acme + "/**"},
		{Name: "notes/acme.md", Filename: "notes/acme.md", Content: "z", SourceProject: "remote:github.com/acme-corp/billing"},
	} {
		if err := v.SetInstruction(inst); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// tomlString is a TOML literal string, which takes a Windows path as is.
func tomlString(s string) string {
	return "'" + s + "'"
}

func exportTwo(t *testing.T, w twoWorkDirs, sel string) SetupBundle {
	t.Helper()
	res, err := collectSetupBundle(exportCmd, w.v, w.cfgDir, setupCollectOptions{
		IncludeProviderFiles: true, IncludeMemory: true, IncludeSessions: true, IncludeSecrets: true,
		WorkDirSelection: sel,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Bundle
}

func memoryFolders(b SetupBundle) map[string]bool {
	out := map[string]bool{}
	for _, a := range b.ProviderFiles {
		if m := memoryAssetPath.FindStringSubmatch(a.LogicalPath); m != nil && a.LogicalRoot == setupAssetRootProviderClaude {
			out[m[1]] = true
		}
	}
	return out
}

// The acceptance line: exporting only personal carries nothing from the
// employer's work dir, checked on the encoded bundle.
func TestExportWorkDirsPersonalCarriesNothingFromCompany(t *testing.T) {
	w := setupTwoWorkDirs(t)
	b := exportTwo(t, w, "personal")
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{
		jsonString(w.acme)[1 : len(jsonString(w.acme))-1],
		filepath.ToSlash(w.acme),
		claudeProjectSlug(w.acme),
		"acme-corp", "acme-only@m", "acme-rules", "notes/acme.md", "billing",
	} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("bundle carries %q from the unselected work dir", leak)
		}
	}

	got := memoryFolders(b)
	for _, keep := range []string{w.personal, w.personalRepo, w.siblingRepo} {
		if !got[claudeProjectSlug(keep)] {
			t.Errorf("memory for %s left out", keep)
		}
	}
	ids := map[string]bool{}
	for _, p := range b.Plugins.Plugins {
		ids[p.ID] = true
	}
	for _, id := range []string{"both@m", "user@m", "site@m"} {
		if !ids[id] {
			t.Errorf("plugin %s left out; it is installed outside the company work dir", id)
		}
	}
	if b.ProviderConfigs.Codex.TrustedProjects[w.personalRepo] == "" {
		t.Error("personal trusted project left out")
	}
	var codex []byte
	for _, a := range b.ProviderFiles {
		if a.LogicalRoot == setupAssetRootProviderCodex && a.LogicalPath == "config.toml" {
			codex = a.Content
		}
	}
	if !bytes.Contains(codex, []byte("[profiles.fast]")) || !bytes.Contains(codex, []byte("# trusted checkouts")) ||
		!bytes.Contains(codex, []byte(tomlString(w.personalRepo))) {
		t.Errorf("config.toml lost lines it should keep:\n%s", codex)
	}
	if len(b.Sessions.Sessions) != 1 || b.Sessions.Sessions[0].Name != "site" {
		t.Errorf("sessions = %+v", b.Sessions.Sessions)
	}
	var declined *DeclinedAsset
	for i, d := range b.Declined {
		if d.Path == "work dir company" {
			declined = &b.Declined[i]
		}
	}
	if declined == nil || !strings.Contains(declined.Reason, "memory folders: 3") || !strings.Contains(declined.Reason, "plugins: 1") {
		t.Fatalf("declined = %+v", b.Declined)
	}
}

// With every work dir selected the export is what it was before names.
func TestExportWorkDirsAllKeepsEverything(t *testing.T) {
	w := setupTwoWorkDirs(t)
	b := exportTwo(t, w, "all")
	if got := memoryFolders(b); len(got) != 6 {
		t.Fatalf("memory folders = %v, want all 6", got)
	}
	if len(b.Plugins.Plugins) != 4 || len(b.Sessions.Sessions) != 2 {
		t.Fatalf("plugins %d sessions %d", len(b.Plugins.Plugins), len(b.Sessions.Sessions))
	}
	for _, d := range b.Declined {
		if strings.HasPrefix(d.Path, "work dir ") {
			t.Fatalf("declined with all selected: %+v", d)
		}
	}
}

func TestExportWorkDirsRefusals(t *testing.T) {
	w := setupTwoWorkDirs(t)
	for _, c := range []struct {
		opts setupCollectOptions
		want string
	}{
		{setupCollectOptions{WorkDirSelection: "nope"}, "no work dir named nope"},
		{setupCollectOptions{WorkDirSelection: "personal", ProjectDir: w.acmeRepo}, "--project"},
		{setupCollectOptions{WorkDirSelection: "personal", WorkDirs: []string{w.acme}}, "--workdir"},
	} {
		_, err := collectSetupBundle(exportCmd, w.v, w.cfgDir, c.opts)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.opts, err, c.want)
		}
	}
}

func TestExportWorkDirsNoneDeclared(t *testing.T) {
	v, root := sessionTestVault(t, nil)
	if _, err := collectSetupBundle(exportCmd, v, filepath.Join(root, config.AppName), setupCollectOptions{WorkDirSelection: "personal"}); err == nil ||
		!strings.Contains(err.Error(), "no work dirs are declared") {
		t.Fatalf("err = %v", err)
	}
}

// A project that Codex names outside its own table cannot be removed line
// by line; the file is withheld rather than shipped with it.
func TestWithoutCodexProjectsBlocksDottedKeys(t *testing.T) {
	acme := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(acme, 0o755); err != nil {
		t.Fatal(err)
	}
	s := workDirScope{unselected: []locations.Named{{Name: "company", Path: acme}}, left: map[string]map[string]int{}}
	for _, body := range []string{
		"projects." + tomlString(filepath.Join(acme, "r")) + ".trust_level = \"trusted\"\n",
		"projects = { " + tomlString(filepath.Join(acme, "r")) + " = { trust_level = \"trusted\" } }\n",
	} {
		if _, _, blocked := s.withoutCodexProjects([]byte(body)); blocked != "company" {
			t.Errorf("not blocked: %q", body)
		}
	}
	// A header-looking line inside a multi-line string is text.
	// A literal multi-line string takes a Windows path's backslashes as written.
	body := "notify = '''\n[projects." + tomlString(filepath.Join(acme, "r")) + "]\n'''\nmodel = \"x\"\n"
	out, dropped, blocked := s.withoutCodexProjects([]byte(body))
	if blocked != "" || len(dropped) != 0 || string(out) != body {
		t.Errorf("multi-line string edited: out=%q dropped=%v blocked=%q", out, dropped, blocked)
	}
}

func TestMemoryOwnerByProofAndPrefix(t *testing.T) {
	root := t.TempDir()
	personal, acme := filepath.Join(root, "p"), filepath.Join(root, "p-acme")
	for _, d := range []string{filepath.Join(personal, "a-b"), filepath.Join(acme, "r")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := workDirScope{
		selected:   []locations.Named{{Name: "personal", Path: personal}},
		unselected: []locations.Named{{Name: "company", Path: acme}},
	}
	// p/a-b and p-acme... share slugs by prefix; proof decides.
	if got := s.memoryOwner(claudeProjectSlug(filepath.Join(personal, "a-b"))); got != "" {
		t.Errorf("proved personal folder owned by %q", got)
	}
	if got := s.memoryOwner(claudeProjectSlug(filepath.Join(acme, "r"))); got != "company" {
		t.Errorf("company folder owner = %q", got)
	}
	if got := s.memoryOwner(claudeProjectSlug(filepath.Join(acme, "gone"))); got != "company" {
		t.Errorf("deleted company checkout owner = %q", got)
	}
	// p/acme/x has the slug of p-acme/x but exists under personal: kept.
	if err := os.MkdirAll(filepath.Join(personal, "acme", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := s.memoryOwner(claudeProjectSlug(filepath.Join(personal, "acme", "x"))); got != "" {
		t.Errorf("folder proved under personal owned by %q", got)
	}
}

func TestApplyDetectedDropsTrustedProjects(t *testing.T) {
	root := t.TempDir()
	acme := filepath.Join(root, "acme")
	s := workDirScope{unselected: []locations.Named{{Name: "company", Path: acme}}, left: map[string]map[string]int{}}
	b := SetupBundle{DetectedAgents: []DetectedAgent{
		{Name: "codex", Settings: map[string]any{"trusted_projects": []string{filepath.Join(root, "me"), filepath.Join(acme, "r")}}},
		{Name: "codex2", Settings: map[string]any{"trusted_projects": []string{filepath.Join(acme, "r")}}},
	}}
	if _, err := s.apply(&b, root); err != nil {
		t.Fatal(err)
	}
	if got := b.DetectedAgents[0].Settings["trusted_projects"].([]string); len(got) != 1 || got[0] != filepath.Join(root, "me") {
		t.Errorf("trusted = %v", got)
	}
	if _, ok := b.DetectedAgents[1].Settings["trusted_projects"]; ok {
		t.Error("an emptied list stayed")
	}
	if s.left["company"]["trusted projects"] != 2 {
		t.Errorf("left = %v", s.left)
	}
}

// A carried file or instruction that still names the work dir is withheld;
// one naming a different folder with a longer name is not.
func TestApplyMentionsWithholdsFilesNamingWorkDir(t *testing.T) {
	home := t.TempDir()
	acme := filepath.Join(home, "work", "acme")
	s := workDirScope{unselected: []locations.Named{{Name: "company", Path: acme}}, left: map[string]map[string]int{}}
	asset := func(path, body string) SetupAsset {
		return SetupAsset{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: path, ContentPresent: true, Content: []byte(body)}
	}
	b := SetupBundle{
		ProviderFiles: []SetupAsset{
			asset("settings.json", `{"permissions":{"additionalDirectories":[`+jsonString(acme)+`]}}`),
			asset("hooks/x.sh", "cd ~/work/acme/team && make\n"),
			asset("CLAUDE.md", "Old notes live in ~/work/acme-old/x and ~/work/acmes.\n"),
		},
		SharedConfig: agent.SharedConfig{Instructions: []agent.InstructionFile{
			{Name: "a", Content: "run $HOME/work/acme/bin/t"},
			{Name: "b", Content: "nothing here"},
		}},
	}
	if _, err := s.apply(&b, home); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, false, true} {
		if got := b.ProviderFiles[i].ContentPresent; got != want {
			t.Errorf("%s carried = %v, want %v", b.ProviderFiles[i].LogicalPath, got, want)
		}
	}
	if len(b.SharedConfig.Instructions) != 1 || b.SharedConfig.Instructions[0].Name != "b" {
		t.Errorf("instructions = %+v", b.SharedConfig.Instructions)
	}
	if data, _ := json.Marshal(b); bytes.Contains(data, []byte("work/acme/")) || bytes.Contains(data, []byte(jsonString(acme))) {
		t.Errorf("bundle still names the work dir: %s", data)
	}
}

func companyScope(t *testing.T) (*workDirScope, string, string) {
	t.Helper()
	home := t.TempDir()
	acme := filepath.Join(home, "work", "acme")
	return &workDirScope{unselected: []locations.Named{{Name: "company", Path: acme}}, left: map[string]map[string]int{}}, home, acme
}

func TestApplyFiltersVaultPartsAndDeclines(t *testing.T) {
	s, home, acme := companyScope(t)
	repo := filepath.Join(acme, "billing")
	b := SetupBundle{
		Agents: []agent.Agent{{Name: "me"}, {Name: "acme-bot", SystemPrompt: "Work in " + repo}, {Name: "you"}},
		ProviderConfigs: agent.ProviderConfig{
			Claude: &agent.ClaudeConfig{
				MCPServers:     map[string]any{"local": map[string]any{"command": "x"}, "acme": map[string]any{"args": []string{repo}}},
				CustomSettings: map[string]any{"permissions": map[string]any{"additionalDirectories": []string{repo}}, "theme": "dark"},
			},
			Codex: &agent.CodexConfig{CustomSettings: map[string]any{"projects": map[string]any{repo: map[string]any{"trust_level": "trusted"}}}},
		},
		Plugins: &PluginInventory{Marketplaces: []PluginMarketplace{{Name: "own", Source: repo + "/mp", Kind: "directory"}, {Name: "pub", Source: "o/r", Kind: "github"}}},
		Declined: []DeclinedAsset{
			{Path: "projects/" + claudeProjectSlug(repo) + "/memory/secret.md", Category: DeclinedByPolicy, Reason: "sensitive"},
			{Path: "settings.local.json", Category: DeclinedByPolicy, Reason: "local"},
		},
	}
	keep, err := s.apply(&b, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Agents) != 2 || keep[1] || !keep[0] || !keep[2] {
		t.Errorf("agents = %v keep = %v", b.Agents, keep)
	}
	if got := keepParallel([]bool{true, false, true}, keep); len(got) != 2 || !got[0] || !got[1] {
		t.Errorf("keepParallel = %v", got)
	}
	if _, ok := b.ProviderConfigs.Claude.MCPServers["acme"]; ok || len(b.ProviderConfigs.Claude.MCPServers) != 1 {
		t.Errorf("mcp = %v", b.ProviderConfigs.Claude.MCPServers)
	}
	if _, ok := b.ProviderConfigs.Claude.CustomSettings["permissions"]; ok || b.ProviderConfigs.Claude.CustomSettings["theme"] != "dark" {
		t.Errorf("custom = %v", b.ProviderConfigs.Claude.CustomSettings)
	}
	if len(b.Plugins.Marketplaces) != 1 || b.Plugins.Marketplaces[0].Name != "pub" {
		t.Errorf("marketplaces = %v", b.Plugins.Marketplaces)
	}
	if data, _ := json.Marshal(b); bytes.Contains(data, []byte(claudeProjectSlug(repo))) || bytes.Contains(data, []byte(filepath.ToSlash(acme)+"/")) {
		t.Errorf("bundle names the work dir: %s", data)
	}
}

// A field no filter knows still cannot carry the work dir: the export stops.
func TestApplyVerifyStopsOnUnknownField(t *testing.T) {
	s, home, acme := companyScope(t)
	b := SetupBundle{SharedConfig: agent.SharedConfig{SystemPrompt: "Work in " + filepath.Join(acme, "x") + " only."}}
	_, err := s.apply(&b, home)
	if err == nil || !strings.Contains(err.Error(), "shared_config (work dir company)") {
		t.Fatalf("err = %v", err)
	}
}
