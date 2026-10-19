package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func declinedHas(d []DeclinedAsset, path, category string) bool {
	for _, x := range d {
		if x.Path == path && x.Category == category {
			return true
		}
	}
	return false
}

// Every agent tool's user-level setup is carried, not only Claude's and
// Codex's, and settings that can hold tokens are redacted without
// --include-secrets.
func TestCollectSetupAssets_CarriesEveryToolsUserSetup(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".gemini", "GEMINI.md"), "gemini rules\n")
	mustWriteFile(t, filepath.Join(home, ".gemini", "settings.json"), `{"apiKey":"x"}`)
	mustWriteFile(t, filepath.Join(home, ".gemini", "commands", "ship.toml"), "prompt = 'ship'\n")
	mustWriteFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "codex rules\n")
	mustWriteFile(t, filepath.Join(home, ".codex", "prompts", "review.md"), "review\n")
	mustWriteFile(t, filepath.Join(home, ".copilot", "config.json"), `{"token":"x"}`)
	mustWriteFile(t, filepath.Join(home, ".copilot", "agents", "a.md"), "agent\n")
	mustWriteFile(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{}}`)
	mustWriteFile(t, filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), `{"mcpServers":{}}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "output-styles", "terse.md"), "terse\n")

	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		root, path string
		redacted   bool
	}{
		{setupAssetRootProviderGemini, "GEMINI.md", false},
		{setupAssetRootProviderGemini, "settings.json", true},
		{setupAssetRootProviderGemini, "commands/ship.toml", false},
		{setupAssetRootProviderCodex, "AGENTS.md", false},
		{setupAssetRootProviderCodex, "prompts/review.md", false},
		{setupAssetRootProviderCopilotCLI, "config.json", true},
		{setupAssetRootProviderCopilotCLI, "agents/a.md", false},
		{setupAssetRootProviderCursor, "mcp.json", true},
		{setupAssetRootProviderWindsurf, "mcp_config.json", true},
		{setupAssetRootProviderClaude, "output-styles/terse.md", false},
	} {
		a := findAsset(assets.ProviderFiles, c.root, c.path)
		if a == nil {
			t.Errorf("%s/%s not carried", c.root, c.path)
			continue
		}
		if a.Redacted != c.redacted {
			t.Errorf("%s/%s redacted = %v, want %v", c.root, c.path, a.Redacted, c.redacted)
		}
		if !c.redacted && !a.ContentPresent {
			t.Errorf("%s/%s carried without content", c.root, c.path)
		}
	}
}

// Login tokens stay behind even with --include-secrets, from any directory
// that is walked.
func TestCollectSetupAssets_NeverCarriesCredentialFiles(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".gemini", "commands", "oauth_creds.json"), `{"refresh_token":"x"}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "hooks", ".credentials.json"), `{"t":"x"}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "hooks", "guard.sh"), "#!/bin/sh\n")

	assets, _, err := collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets.ProviderFiles {
		switch filepath.Base(a.LogicalPath) {
		case "oauth_creds.json", ".credentials.json":
			t.Errorf("credential file carried: %s/%s", a.LogicalRoot, a.LogicalPath)
		}
	}
	if findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "hooks/guard.sh") == nil {
		t.Error("hooks/guard.sh not carried")
	}
}

// From ~/.claude.json only the MCP servers travel. The signed-in account and
// per-project state stay, and import merges instead of replacing the file.
func TestClaudeUserMCPServersExportAndMerge(t *testing.T) {
	src := t.TempDir()
	setTestHomeDir(t, src)
	mustWriteFile(t, filepath.Join(src, ".claude.json"),
		`{"oauthAccount":{"email":"a@b"},"projects":{"/x":{}},"mcpServers":{"gh":{"command":"gh-mcp"}}}`)

	assets, _, err := collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, ".claude.json")
	if a == nil || !a.ContentPresent {
		t.Fatalf("~/.claude.json MCP servers not carried: %+v", a)
	}
	if bytes.Contains(a.Content, []byte("oauthAccount")) || bytes.Contains(a.Content, []byte("projects")) {
		t.Fatalf("carried more than mcpServers: %s", a.Content)
	}

	dst := t.TempDir()
	mustWriteFile(t, filepath.Join(dst, ".claude.json"), `{"oauthAccount":{"email":"other@b"},"mcpServers":{}}`)
	applied, warnings, err := applyProviderAssetsToSystem(dst, []SetupAsset{*a})
	if err != nil || applied != 1 {
		t.Fatalf("apply = %d, %v, %v", applied, warnings, err)
	}
	var got map[string]json.RawMessage
	data, _ := os.ReadFile(filepath.Join(dst, ".claude.json"))
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got["oauthAccount"]), "other@b") {
		t.Errorf("the destination's account was replaced: %s", data)
	}
	if !strings.Contains(string(got["mcpServers"]), "gh-mcp") {
		t.Errorf("mcpServers not merged: %s", data)
	}

	redacted, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := findAsset(redacted.ProviderFiles, setupAssetRootProviderHome, ".claude.json"); r == nil || !r.Redacted || r.ContentPresent {
		t.Errorf("without --include-secrets the MCP servers must be redacted: %+v", r)
	}
}

func TestCollectSetupAssets_MemoryOnlyWhenAsked(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".claude", "projects", "-home-x-proj", "memory", "MEMORY.md"), "- fact\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "projects", "-home-x-proj", "session.jsonl"), "{}\n")
	mustWriteFile(t, filepath.Join(home, ".codex", "memories", "m.md"), "memory\n")

	without, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range without.ProviderFiles {
		if strings.Contains(a.LogicalPath, "memor") {
			t.Fatalf("memory carried without --include-memory: %s", a.LogicalPath)
		}
	}
	with, _, err := collectSetupAssets(setupAssetOptions{IncludeMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if findAsset(with.ProviderFiles, setupAssetRootProviderClaude, "projects/-home-x-proj/memory/MEMORY.md") == nil {
		t.Error("Claude project memory not carried")
	}
	if findAsset(with.ProviderFiles, setupAssetRootProviderCodex, "memories/m.md") == nil {
		t.Error("Codex memories not carried")
	}
	if findAsset(with.ProviderFiles, setupAssetRootProviderClaude, "projects/-home-x-proj/session.jsonl") != nil {
		t.Error("a session transcript was carried as memory")
	}
}

// A user-level CLAUDE.md that imports or names other files arrives with
// them, followed through the files they name in turn.
func TestCollectSetupAssets_FollowsUserInstructionReferences(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"),
		"@~/Projects/AGENTS.md\nSee `rules/style.md` and `/etc/hosts` and `rules/gone.md`.\n")
	mustWriteFile(t, filepath.Join(home, "Projects", "AGENTS.md"), "Read [the checklist](docs/check.md).\n")
	mustWriteFile(t, filepath.Join(home, "Projects", "docs", "check.md"), "check\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "rules", "style.md"), "style\n")

	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Projects/AGENTS.md", "Projects/docs/check.md", ".claude/rules/style.md"} {
		a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, want)
		if a == nil || !a.ContentPresent {
			t.Errorf("~/%s not carried", want)
		}
	}
	if !declinedHas(assets.Declined, "/etc/hosts", DeclinedByPolicy) {
		t.Errorf("/etc/hosts not declined by policy: %v", assets.Declined)
	}
	if !declinedHas(assets.Declined, "rules/gone.md", DeclinedAbsent) {
		t.Errorf("rules/gone.md not recorded as absent: %v", assets.Declined)
	}
}

// The scripts a settings.json runs are carried, wherever under home they live.
func TestCollectSetupAssets_CarriesScriptsSettingsRun(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	settings := map[string]any{
		"hooks": map[string]any{"PreToolUse": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": "bash ~/bin/guard.sh --strict"}},
		}}},
		"statusLine": map[string]any{"type": "command", "command": filepath.Join(home, "scripts", "status.sh")},
	}
	data, _ := json.Marshal(settings)
	mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), string(data))
	mustWriteFile(t, filepath.Join(home, "bin", "guard.sh"), "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(home, "scripts", "status.sh"), "#!/bin/sh\n")

	// Without --include-secrets: settings.json is redacted, and the scripts
	// it runs must still be found.
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bin/guard.sh", "scripts/status.sh"} {
		if findAsset(assets.ProviderFiles, setupAssetRootProviderHome, want) == nil {
			t.Errorf("~/%s not carried", want)
		}
	}
}

// Project scope: every tool's instruction files and rule directories are
// carried, with what they name; each tool's repository settings are listed.
func TestCollectSetupAssets_ProjectInstructionsClosureAndRepoSettings(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "Follow `docs/a.md` and everything in `checklists/`.\n")
	mustWriteFile(t, filepath.Join(project, "docs", "a.md"), "Then [b](b.md).\n")
	mustWriteFile(t, filepath.Join(project, "docs", "b.md"), "b\n")
	mustWriteFile(t, filepath.Join(project, "checklists", "release.md"), "release\n")
	mustWriteFile(t, filepath.Join(project, "checklists", "review.md"), "review\n")
	mustWriteFile(t, filepath.Join(project, "GEMINI.md"), "gemini\n")
	mustWriteFile(t, filepath.Join(project, ".github", "instructions", "go.instructions.md"), "go\n")
	mustWriteFile(t, filepath.Join(project, ".cursor", "rules", "x.mdc"), "x\n")
	mustWriteFile(t, filepath.Join(project, "CONVENTIONS.md"), "conv\n")
	mustWriteFile(t, filepath.Join(project, ".mcp.json"), `{"mcpServers":{}}`)
	mustWriteFile(t, filepath.Join(project, ".gemini", "settings.json"), `{}`)

	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AGENTS.md", "docs/a.md", "docs/b.md", "checklists/release.md",
		"checklists/review.md", "GEMINI.md", ".github/instructions/go.instructions.md",
		".cursor/rules/x.mdc", "CONVENTIONS.md"} {
		if findAsset(assets.ProjectFiles, setupAssetRootProject, want) == nil {
			t.Errorf("%s not carried", want)
		}
	}
	for _, repo := range []string{".mcp.json", ".gemini/settings.json"} {
		if findAsset(assets.ProjectFiles, setupAssetRootProject, repo) != nil {
			t.Errorf("%s carried; repository settings are listed only", repo)
		}
		if !declinedHas(assets.Declined, repo, DeclinedRepoScope) {
			t.Errorf("%s not listed as repository settings", repo)
		}
	}
	seen := map[string]int{}
	for _, a := range assets.ProjectFiles {
		seen[a.LogicalPath]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("%s carried %d times", p, n)
		}
	}
}

func TestPluginInventoryAndInstallCommands(t *testing.T) {
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		`{"official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"}},"mine":{"source":{"source":"directory","path":"/home/x/mkt"}}}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"superpowers@official":[{"scope":"user","version":"5.1.0","installPath":"/home/x/.claude/plugins/cache/s"}]}}`)
	inv, err := collectClaudePluginInventory(home)
	if err != nil || inv == nil {
		t.Fatalf("inventory = %+v, %v", inv, err)
	}
	var out bytes.Buffer
	printPluginInstallCommands(&out, inv)
	got := out.String()
	for _, want := range []string{
		"claude plugin marketplace add anthropics/claude-plugins-official",
		"claude plugin install superpowers@official",
		`# "mine" is a local directory`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/home/x/.claude/plugins/cache") {
		t.Error("a machine-local cache path leaked into the commands")
	}

	if empty, err := collectClaudePluginInventory(t.TempDir()); err != nil || empty != nil {
		t.Errorf("no plugins dir: got %+v, %v; want nil, nil", empty, err)
	}
}

// An instruction that says "put the token in `.env`" must not carry .env,
// at project scope or from a user-level file.
func TestCollectSetupAssets_NeverFollowsIntoSecretFiles(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "Put the token in `.env`; see `config/`.\n")
	mustWriteFile(t, filepath.Join(project, ".env"), "TOKEN=s3cret\n")
	mustWriteFile(t, filepath.Join(project, "config", "app.toml"), "a = 1\n")
	mustWriteFile(t, filepath.Join(project, "config", "tls.key"), "KEY\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "Deploy keys: `~/.ssh/id_ed25519`, `~/.netrc`.\n")
	mustWriteFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "PRIVATE\n")
	mustWriteFile(t, filepath.Join(home, ".netrc"), "machine x password y\n")

	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project, IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]SetupAsset{}, assets.ProjectFiles...), assets.ProviderFiles...)
	for _, a := range all {
		if bytes.Contains(a.Content, []byte("s3cret")) || bytes.Contains(a.Content, []byte("PRIVATE")) ||
			bytes.Contains(a.Content, []byte("password y")) || a.LogicalPath == "config/tls.key" {
			t.Errorf("secret carried: %s/%s", a.LogicalRoot, a.LogicalPath)
		}
	}
	if findAsset(assets.ProjectFiles, setupAssetRootProject, "config/app.toml") == nil {
		t.Error("config/app.toml (a named directory's plain file) not carried")
	}
	if !declinedHas(assets.Declined, ".env", DeclinedByPolicy) {
		t.Errorf(".env refusal not recorded: %v", assets.Declined)
	}
}

// The home root is an allowlist: ~/.claude.json, merged, and prose. A bundle
// from someone else must not plant startup files, keys, PATH entries or
// anything executable, whatever the spelling.
func TestApplyHomeRootIsAnAllowlist(t *testing.T) {
	home := t.TempDir()
	var assets []SetupAsset
	refused := []string{".bashrc", ".BASHRC", " .bashrc", " .claude.json", "./.claude.json", "docs//x.md", ".zshrc", ".ssh/authorized_keys", ".SSH/authorized_keys",
		".netrc", ".config/autostart/x.desktop", "Library/LaunchAgents/x.plist", ".hidden-at-root",
		"bin/guard.sh", ".local/bin/git", "notes/run.sh"}
	for _, p := range refused {
		assets = append(assets, SetupAsset{LogicalRoot: setupAssetRootProviderHome, LogicalPath: p,
			ContentPresent: true, Content: []byte("payload")})
	}
	assets = append(assets, SetupAsset{LogicalRoot: setupAssetRootProviderHome, LogicalPath: "docs/howto.md",
		ContentPresent: true, Content: []byte("how\n")})
	applied, warnings, err := applyProviderAssetsToSystem(home, assets)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (docs/howto.md); warnings: %v", applied, warnings)
	}
	for _, p := range refused {
		if _, err := os.Lstat(filepath.Join(home, strings.TrimSpace(p))); !os.IsNotExist(err) {
			t.Errorf("%q was written", p)
		}
	}
	info, err := os.Stat(filepath.Join(home, "docs", "howto.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("prose written executable: %v", info.Mode())
	}
}

// The keys merged into ~/.claude.json are fixed in code: a bundle that asks
// to merge oauthAccount does not get to replace the account.
func TestApplyClaudeJSONIgnoresBundleMergeKeys(t *testing.T) {
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"email":"mine@x"}}`)
	_, warnings, err := applyProviderAssetsToSystem(home, []SetupAsset{{
		LogicalRoot: setupAssetRootProviderHome, LogicalPath: ".claude.json", ContentPresent: true,
		Content:   []byte(`{"oauthAccount":{"email":"attacker@x"},"mcpServers":{"a":{"command":"x"}}}`),
		MergeKeys: []string{"oauthAccount", "mcpServers"},
	}})
	if err != nil {
		t.Fatal(err, warnings)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !bytes.Contains(data, []byte("mine@x")) || bytes.Contains(data, []byte("attacker@x")) {
		t.Errorf("account replaced from the bundle: %s", data)
	}
	if !bytes.Contains(data, []byte(`"mcpServers"`)) {
		t.Errorf("mcpServers not merged: %s", data)
	}
}

// A reference named like a note that is a symlink to a credential file is
// refused by its resolved path.
func TestHomeReferenceRefusesSymlinkToSecret(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".netrc"), "machine x password y\n")
	if err := os.Symlink(filepath.Join(home, ".netrc"), filepath.Join(home, "notes.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "See `~/notes.md`.\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets.ProviderFiles {
		if bytes.Contains(a.Content, []byte("password y")) {
			t.Fatalf("credential carried through a symlink as %s", a.LogicalPath)
		}
	}
}

// Without --include-secrets a referenced script is redacted; referenced
// prose is carried.
func TestHomeReferencedNonProseIsSensitive(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "Run `~/bin/deploy.sh`, read `~/docs/howto.md`.\n")
	mustWriteFile(t, filepath.Join(home, "bin", "deploy.sh"), "TOKEN=abc ./deploy\n")
	mustWriteFile(t, filepath.Join(home, "docs", "howto.md"), "how\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, "bin/deploy.sh"); a == nil || !a.Redacted {
		t.Errorf("bin/deploy.sh not redacted: %+v", a)
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, "docs/howto.md"); a == nil || !a.ContentPresent {
		t.Errorf("docs/howto.md not carried: %+v", a)
	}
}

// Values from a bundle printed as shell commands must not carry shell syntax.
func TestPluginInstallCommandsRefuseShellSyntax(t *testing.T) {
	var out bytes.Buffer
	printPluginInstallCommands(&out, &PluginInventory{
		Marketplaces: []PluginMarketplace{{Name: "m", Source: "x/y; rm -rf ~", Kind: "github"}},
		Plugins:      []InstalledPlugin{{ID: "p@m$(curl evil)"}, {ID: "ok@m"}},
	})
	for _, line := range strings.Split(out.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "claude ") && strings.ContainsAny(trimmed, ";$()`|&") {
			t.Errorf("unsafe command printed: %s", line)
		}
	}
	if !strings.Contains(out.String(), "claude plugin install ok@m") {
		t.Errorf("valid plugin not printed:\n%s", out.String())
	}
}

// Rooted on every OS, including the Windows forms filepath.IsAbs rejects
// there: no drive, or a drive with no separator.
func TestIsRootedPath(t *testing.T) {
	for _, p := range []string{"/etc/hosts", `\Windows\x`, "C:x", `c:\x`, "D:/x", "a:b"} {
		if !isRootedPath(p) {
			t.Errorf("%q not rooted", p)
		}
	}
	for _, p := range []string{"docs/a.md", "./x", "~/x", "notes:2026.md"} {
		if isRootedPath(p) {
			t.Errorf("%q rooted", p)
		}
	}
}

// A reference that is a symlink to a file under home (a dotfiles checkout) is
// carried with the target's content. It must not abort the export.
func TestHomeReferenceThroughSymlinkIsCarried(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, "dotfiles", "rules.md"), "rules body\n")
	if err := os.Symlink(filepath.Join(home, "dotfiles", "rules.md"), filepath.Join(home, "rules.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "@~/rules.md\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatalf("export failed on a symlinked reference: %v", err)
	}
	a := findAsset(assets.ProviderFiles, setupAssetRootProviderHome, "rules.md")
	if a == nil || string(a.Content) != "rules body\n" {
		t.Errorf("rules.md not carried through its symlink: %+v", a)
	}
}

// ~/.claude.json is carried as its mcpServers only. A reference to it, in any
// case spelling, must not carry the whole file (account, project history).
func TestHomeReferenceNeverCarriesWholeClaudeJSON(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"email":"x"},"mcpServers":{}}`)
	// On a case-sensitive file system this stands in for the same file seen
	// through another spelling on macOS or Windows.
	mustWriteFile(t, filepath.Join(home, ".CLAUDE.json"), `{"oauthAccount":{"email":"x"},"mcpServers":{}}`)
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "See `~/.claude.json` and `~/.CLAUDE.json`.\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets.ProviderFiles {
		if bytes.Contains(a.Content, []byte("oauthAccount")) {
			t.Fatalf("whole ~/.claude.json carried as %s", a.LogicalPath)
		}
	}
}

// macOS and Windows file systems ignore case: .SSH is .ssh there.
func TestSecretBearingDirsIgnoreCase(t *testing.T) {
	for _, p := range []string{".SSH/config", "x/.Aws/credentials", ".GnuPG/key.txt"} {
		if r := homeAssetRefusal(p); r == "" {
			t.Errorf("%s: not refused", p)
		}
	}
	if agent.SecretBearingReason(".SSH/config") == "" {
		t.Error(".SSH/config not secret-bearing")
	}
	if !isCredentialFile(filepath.Join("x", "Auth.JSON")) {
		t.Error("Auth.JSON not treated as a credential file")
	}
}

// A project symlink named like prose that points at .env is refused by the
// resolved name, as on the home side.
func TestProjectReferenceRefusesSymlinkToSecret(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".env"), "API_TOKEN=s3cret\n")
	if err := os.Symlink(".env", filepath.Join(project, "notes.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "See `notes.md`.\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project, IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets.ProjectFiles {
		if bytes.Contains(a.Content, []byte("s3cret")) {
			t.Fatalf(".env carried through a symlink as %s", a.LogicalPath)
		}
	}
}

// A .local. file stays behind unless named with --include-local, also when an
// instruction file names it, at project and at user scope.
func TestReferencedLocalFilesAreDeclined(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "Read `notes.local.md`.\n")
	mustWriteFile(t, filepath.Join(project, "notes.local.md"), "project local\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "Read `~/me.local.md`.\n")
	mustWriteFile(t, filepath.Join(home, "me.local.md"), "home local\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	if findAsset(assets.ProjectFiles, setupAssetRootProject, "notes.local.md") != nil {
		t.Error("project notes.local.md carried without --include-local")
	}
	if findAsset(assets.ProviderFiles, setupAssetRootProviderHome, "me.local.md") != nil {
		t.Error("~/me.local.md carried without --include-local")
	}
	if !declinedHas(assets.Declined, "notes.local.md", DeclinedByPolicy) || !declinedHas(assets.Declined, "me.local.md", DeclinedByPolicy) {
		t.Errorf("referenced .local. files not reported as declined: %v", assets.Declined)
	}
}

// A project file an instruction names that is not prose (a script, a config
// file) is redacted without --include-secrets, as at user scope.
func TestProjectReferencedNonProseIsSensitive(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "Run `scripts/deploy.sh`, read `docs/howto.md`.\n")
	mustWriteFile(t, filepath.Join(project, "scripts", "deploy.sh"), "TOKEN=abc ./deploy\n")
	mustWriteFile(t, filepath.Join(project, "docs", "howto.md"), "how\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	if a := findAsset(assets.ProjectFiles, setupAssetRootProject, "scripts/deploy.sh"); a == nil || !a.Redacted {
		t.Errorf("scripts/deploy.sh not redacted: %+v", a)
	}
	if a := findAsset(assets.ProjectFiles, setupAssetRootProject, "docs/howto.md"); a == nil || !a.ContentPresent {
		t.Errorf("docs/howto.md not carried: %+v", a)
	}
}

// A prose-named symlink to a config file is judged by its target too: it is
// redacted without --include-secrets.
func TestHomeReferenceProseSymlinkToConfigIsSensitive(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".config", "x", "settings.json"), `{"token":"s3cret"}`)
	if err := os.Symlink(filepath.Join(home, ".config", "x", "settings.json"), filepath.Join(home, "notes.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mustWriteFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "See `~/notes.md`.\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets.ProviderFiles {
		if bytes.Contains(a.Content, []byte("s3cret")) {
			t.Fatalf("config carried unredacted through a prose-named symlink as %s", a.LogicalPath)
		}
	}
}

// A marketplace added from a URL with a token in it must not carry the token.
func TestPluginInventoryStripsURLCredentials(t *testing.T) {
	home := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		`{"team":{"source":{"source":"git","url":"https://x-access-token:ghp_s3cret@github.com/o/r.git"}}}`)
	inv, err := collectClaudePluginInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(inv)
	if bytes.Contains(data, []byte("s3cret")) || bytes.Contains(data, []byte("x-access-token")) {
		t.Fatalf("credential carried in plugin inventory: %s", data)
	}
	if inv == nil || len(inv.Marketplaces) != 1 || inv.Marketplaces[0].Source != "https://github.com/o/r.git" {
		t.Errorf("source not kept without its credentials: %+v", inv)
	}
}

func TestWithoutURLCredentials(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"owner/repo", "owner/repo"},
		{"/home/me/market", "/home/me/market"},
		{"https://github.com/o/r.git", "https://github.com/o/r.git"},
		{"https://x-access-token:tok@github.com/o/r.git", "https://github.com/o/r.git"},
		{"https://github.com/o/r.git?token=tok#frag", "https://github.com/o/r.git"},
		{"ssh://git@host/o/r", "ssh://host/o/r"},
		{"git@github.com:o/r.git", "github.com:o/r.git"},
		{"user:tok@host:path", ""},
		{"https:tok@host", ""},
		{"file://tok@host/x", ""},
		{"https:///nohost", ""},
		{"tok@host", ""},
		{"", ""},
	} {
		if got := withoutURLCredentials(c.in); got != c.want {
			t.Errorf("withoutURLCredentials(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Login token files are never written from a bundle, under any tool root: a
// bundle carrying someone else's token would sign this machine in as them.
func TestApplyNeverWritesCredentialFiles(t *testing.T) {
	home := t.TempDir()
	assets := []SetupAsset{
		{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: ".credentials.json", ContentPresent: true, Content: []byte("{}")},
		{LogicalRoot: setupAssetRootProviderCodex, LogicalPath: "Auth.json", ContentPresent: true, Content: []byte("{}")},
		{LogicalRoot: setupAssetRootProviderGemini, LogicalPath: "oauth_creds.json", ContentPresent: true, Content: []byte("{}")},
	}
	applied, _, err := applyProviderAssetsToSystem(home, assets)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Errorf("applied = %d credential files, want 0", applied)
	}
}

// A second spelling of a token file is the same file once joined.
func TestApplyRefusesUncleanCredentialPaths(t *testing.T) {
	home := t.TempDir()
	var assets []SetupAsset
	for _, p := range []string{"x/../.credentials.json", "./auth.json", "auth.json.", "oauth_creds.json/."} {
		assets = append(assets, SetupAsset{LogicalRoot: setupAssetRootProviderCodex, LogicalPath: p,
			ContentPresent: true, Content: []byte("{}")})
	}
	applied, _, err := applyProviderAssetsToSystem(home, assets)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
	for _, f := range []string{".credentials.json", "auth.json", "auth.json.", "oauth_creds.json"} {
		if _, err := os.Lstat(filepath.Join(home, ".codex", f)); !os.IsNotExist(err) {
			t.Errorf("%s written", f)
		}
	}
}

// A symlinked optional file (a dotfiles link) is skipped with a warning; it
// must not end the export. Before this change these files were not collected
// at all, so the export did not fail on them.
func TestSymlinkedOptionalFilesDoNotAbortExport(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(home, "dotfiles", "GEMINI.md"), "g\n")
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "a\n")
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "dotfiles", "GEMINI.md"), filepath.Join(home, ".gemini", "GEMINI.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(filepath.Join(project, "AGENTS.md"), filepath.Join(project, "CONVENTIONS.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project}); err != nil {
		t.Fatalf("export failed on a symlinked optional file: %v", err)
	}
}

// Hook scripts are code: redacted without --include-secrets.
func TestClaudeHookScriptsAreSensitive(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, ".claude", "hooks", "notify.sh"), "curl https://hooks.example/T0K3N\n")
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "hooks/notify.sh"); a == nil || !a.Redacted {
		t.Errorf("hooks/notify.sh not redacted: %+v", a)
	}
}

// A script settings.json runs that cannot be carried is reported, not dropped.
func TestSettingsRunScriptsDeclinedAreReported(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/bin/gone.sh && ~/.ssh/run.sh"}]}]}}`
	mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), settings)
	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !declinedHas(assets.Declined, "~/bin/gone.sh", DeclinedAbsent) {
		t.Errorf("~/bin/gone.sh not reported absent: %v", assets.Declined)
	}
	if !declinedHas(assets.Declined, "~/.ssh/run.sh", DeclinedByPolicy) {
		t.Errorf("~/.ssh/run.sh not reported as refused: %v", assets.Declined)
	}
}

func TestSecretBearingReasonEnvSuffixAndTrailingDot(t *testing.T) {
	for _, p := range []string{"prod.env", "deploy/app.env", ".env.", "id_rsa "} {
		if agent.SecretBearingReason(p) == "" {
			t.Errorf("%q not flagged", p)
		}
	}
	if r := agent.SecretBearingReason("docs/env.md"); r != "" {
		t.Errorf("docs/env.md flagged: %s", r)
	}
}

// Memory is off unless asked: in the export defaults (the wizard's default
// answer too) and in the flag.
func TestIncludeMemoryDefaultsOff(t *testing.T) {
	if defaultExportOptions().IncludeMemory {
		t.Error("defaultExportOptions includes memory")
	}
	f := exportCmd.Flags().Lookup("include-memory")
	if f == nil || f.DefValue != "false" {
		t.Errorf("--include-memory default = %v, want false", f)
	}
}

// Windows drops trailing dots and spaces from every path segment, so
// ".aws./config" and ".config/fish./config.fish" are .aws/config and
// .config/fish/config.fish there.
func TestSecretAndStartupDirsIgnoreTrailingDots(t *testing.T) {
	for _, p := range []string{".aws./config", ".docker ./config.json", "x/.SSH./config"} {
		if agent.SecretBearingReason(p) == "" {
			t.Errorf("%q not flagged", p)
		}
	}
	for _, p := range []string{".config/fish./notes.md", "Library/LaunchAgents./x.txt"} {
		if homeAssetRefusal(p) == "" {
			t.Errorf("%q not refused", p)
		}
	}
}

// A dotfiles link at ~/.claude/settings.json or CLAUDE.md is skipped with a
// warning, like the other optional files; it must not end the export.
func TestSymlinkedClaudeSettingsDoNotAbortExport(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	mustWriteFile(t, filepath.Join(home, "dotfiles", "settings.json"), "{}\n")
	mustWriteFile(t, filepath.Join(home, "dotfiles", "CLAUDE.md"), "c\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"settings.json", "CLAUDE.md"} {
		if err := os.Symlink(filepath.Join(home, "dotfiles", f), filepath.Join(home, ".claude", f)); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	}
	if _, _, err := collectSetupAssets(setupAssetOptions{}); err != nil {
		t.Fatalf("export failed on a symlinked ~/.claude file: %v", err)
	}
}

// The Copilot CLI can keep login tokens in ~/.copilot/config.json. They never
// travel, even with --include-secrets; the rest of the file does, and the
// export says what was removed. Import strips them from an older bundle too.
func TestCopilotConfigLoginTokensNeverTravel(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	cfg := `{"model":"gpt-5","max_tokens":100,"maxOutputTokens":200,"tokenLimit":300,"minOutputTokens":5,"minted_token":"SECRET4","output-token-count":9,"access_tokens":["SECRET3"],"copilot_tokens":{"u":"ghu_SECRET1"},"logged_in_users":[{"login":"u"}],` +
		`"last_logged_in_user":{"login":"u"},"nested":{"oauthToken":"SECRET2","keep":1}}`
	mustWriteFile(t, filepath.Join(home, ".copilot", "config.json"), cfg)
	assets, _, err := collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	a := findAsset(assets.ProviderFiles, setupAssetRootProviderCopilotCLI, "config.json")
	if a == nil || !a.ContentPresent {
		t.Fatalf("config.json not carried: %+v", a)
	}
	body := string(a.Content)
	for _, bad := range []string{"SECRET", "copilot_tokens", "logged_in_users", "last_logged_in_user"} {
		if strings.Contains(body, bad) {
			t.Errorf("config.json carries %q: %s", bad, body)
		}
	}
	if !strings.Contains(body, `"model"`) || !strings.Contains(body, `"keep"`) ||
		!strings.Contains(body, `"max_tokens"`) || !strings.Contains(body, `"maxOutputTokens"`) || !strings.Contains(body, `"tokenLimit"`) ||
		!strings.Contains(body, `"minOutputTokens"`) || !strings.Contains(body, `"output-token-count"`) {
		t.Errorf("settings lost: %s", body)
	}
	if !declinedHas(assets.Declined, "~/.copilot/config.json", DeclinedByPolicy) {
		t.Errorf("removed tokens not reported: %v", assets.Declined)
	}

	// Unparseable: skipped, never carried raw.
	mustWriteFile(t, filepath.Join(home, ".copilot", "config.json"), `{"copilot_tokens":"ghu_X"`)
	assets, _, err = collectSetupAssets(setupAssetOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderCopilotCLI, "config.json"); a != nil {
		t.Errorf("unparseable config.json carried: %s", a.Content)
	}

	// Import from a bundle that still holds them.
	dst := t.TempDir()
	mustWriteFile(t, filepath.Join(dst, ".copilot", "config.json"), `{"copilot_tokens":{"me":"LOCAL"}}`)
	in := SetupAsset{Kind: setupAssetKindProviderFile, LogicalRoot: setupAssetRootProviderCopilotCLI,
		LogicalPath: "config.json", Content: []byte(cfg), ContentPresent: true}
	if _, _, err := applyProviderAssetsToSystem(dst, []SetupAsset{in}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, ".copilot", "config.json"))
	if strings.Contains(string(got), "SECRET") || strings.Contains(string(got), "logged_in_users") {
		t.Errorf("import wrote login tokens: %s", got)
	}
	if !strings.Contains(string(got), "LOCAL") || !strings.Contains(string(got), `"model"`) {
		t.Errorf("import lost this machine's sign-in or the settings: %s", got)
	}
}

// A .env or key file inside a tool's own directory, and any file with a
// second hard link, is redacted without --include-secrets: its name is no
// proof of what it holds.
func TestDirSecretsAndHardLinksAreRedacted(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(home, ".claude", "agents", "sub", ".env"), "TOKEN=leak1\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "agents", "k.pem"), "leak2\n")
	mustWriteFile(t, filepath.Join(home, ".claude", "agents", "ok.md"), "fine\n")
	mustWriteFile(t, filepath.Join(project, ".cursor", "rules", ".env"), "leak3\n")
	mustWriteFile(t, filepath.Join(project, ".env"), "leak4\n")
	mustWriteFile(t, filepath.Join(project, "AGENTS.md"), "See `hl.md`.\n")
	if err := os.Link(filepath.Join(project, ".env"), filepath.Join(project, "hl.md")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	assets, _, err := collectSetupAssets(setupAssetOptions{ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]SetupAsset{}, assets.ProviderFiles...), assets.ProjectFiles...)
	for _, a := range all {
		if strings.Contains(string(a.Content), "leak") {
			t.Errorf("%s %s carried its secret", a.LogicalRoot, a.LogicalPath)
		}
	}
	if a := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "agents/ok.md"); a == nil || !a.ContentPresent {
		t.Errorf("agents/ok.md not carried: %+v", a)
	}
}
