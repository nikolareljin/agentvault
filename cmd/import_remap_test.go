package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemapTextRespectsSegmentBoundaries(t *testing.T) {
	remaps := []pathRemap{{From: "/home/alex", To: "/Users/alex"}}
	in := `{"command":"/home/alex/bin/guard.sh","other":"/home/alexandra/x","bare":"/home/alex"}`
	got, n := remapText(in, remaps)
	if n != 2 {
		t.Errorf("rewrites = %d, want 2: %s", n, got)
	}
	if !strings.Contains(got, "/Users/alex/bin/guard.sh") || !strings.Contains(got, `"bare":"/Users/alex"`) {
		t.Errorf("home not rewritten: %s", got)
	}
	if !strings.Contains(got, "/home/alexandra/x") {
		t.Errorf("a longer name was rewritten: %s", got)
	}
}

// More specific prefixes win: a project under the old home goes to the new
// project root, not to the old project's place under the new home.
func TestBuildPathRemapsOrdersMostSpecificFirst(t *testing.T) {
	remaps, err := buildPathRemaps("/home/alex", "/Users/alex", "/home/alex/src/app", "/Users/alex/work/app",
		[]string{"/mnt/data=/Volumes/data"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := remapText("/home/alex/src/app/AGENTS.md /home/alex/.claude /mnt/data/x", remaps)
	want := "/Users/alex/work/app/AGENTS.md /Users/alex/.claude /Volumes/data/x"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if _, err := buildPathRemaps("", "", "", "", []string{"nope"}); err == nil {
		t.Error("--map without = accepted")
	}
}

// Claude names a project's memory directory after the project's path.
func TestRemapRenamesClaudeProjectDirectories(t *testing.T) {
	remaps := []pathRemap{{From: "/home/alex", To: "/Users/alex"}}
	assets, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/-home-alex-Projects-app/memory/MEMORY.md",
		ContentPresent: true, Content: []byte("see /home/alex/Projects/app/docs"),
	}}, remaps, "/Users/alex")
	if assets[0].LogicalPath != "projects/-Users-alex-Projects-app/memory/MEMORY.md" {
		t.Errorf("logical path = %s", assets[0].LogicalPath)
	}
	if string(assets[0].Content) != "see /Users/alex/Projects/app/docs" {
		t.Errorf("content = %s", assets[0].Content)
	}
	if assets[0].SHA256 != hashSetupAssetContent(assets[0].Content) {
		t.Error("hash not updated after rewrite")
	}
	if len(report) < 2 {
		t.Errorf("report = %v", report)
	}
}

func TestRemapWarnsAboutUncoveredHomePaths(t *testing.T) {
	_, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json",
		ContentPresent: true, Content: []byte(`{"x":"/home/bob/tool"}`),
	}}, nil, "/Users/alex")
	joined := strings.Join(report, "\n")
	if !strings.Contains(joined, "/home/bob") || !strings.Contains(joined, "--map") {
		t.Errorf("no warning for /home/bob: %v", report)
	}
}

func TestRemapLeavesBinaryContentAlone(t *testing.T) {
	bin := []byte{0x7f, 'E', 'L', 'F', 0, '/', 'h', 'o', 'm', 'e', '/', 'a'}
	assets, _ := remapAssets([]SetupAsset{{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "hooks/x",
		ContentPresent: true, Content: bin}}, []pathRemap{{From: "/home/a", To: "/Users/a"}}, "/Users/a")
	if string(assets[0].Content) != string(bin) {
		t.Error("binary content rewritten")
	}
}

// The execute bit from a bundle is honoured only under a tool's hooks
// directory. Elsewhere it is dropped, whatever the bundle says.
func TestApplyKeepsExecOnlyForToolHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no execute bit on Windows")
	}
	home := t.TempDir()
	assets := []SetupAsset{
		{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "hooks/guard.sh", ContentPresent: true, Content: []byte("#!/bin/sh\n"), Mode: 0o755},
		{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "agents/a.md", ContentPresent: true, Content: []byte("a"), Mode: 0o755},
		{LogicalRoot: setupAssetRootProviderHome, LogicalPath: "docs/x.md", ContentPresent: true, Content: []byte("x"), Mode: 0o755},
	}
	if _, warnings, err := applyProviderAssetsToSystem(home, assets); err != nil {
		t.Fatal(err, warnings)
	}
	mode := func(p string) os.FileMode {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if m := mode(filepath.Join(home, ".claude", "hooks", "guard.sh")); m&0o100 == 0 {
		t.Errorf("hook lost its execute bit: %v", m)
	}
	for _, p := range []string{filepath.Join(home, ".claude", "agents", "a.md"), filepath.Join(home, "docs", "x.md")} {
		if m := mode(p); m&0o111 != 0 {
			t.Errorf("%s written executable: %v", p, m)
		}
	}
}

// --apply-projects writes instructions and prose. Git internals, CI, hook
// directories, repository settings and other non-prose files are refused,
// and nothing is written executable.
func TestApplyProjectsAllowlistAndOverwritePolicy(t *testing.T) {
	configDir := t.TempDir()
	target := t.TempDir()
	mk := func(p, body string) SetupAsset {
		return SetupAsset{Kind: setupAssetKindProjectFile, LogicalRoot: setupAssetRootProject, LogicalPath: p,
			ProjectRelativePath: p, ContentPresent: true, Content: []byte(body), Mode: 0o755}
	}
	assets := []SetupAsset{
		mk("AGENTS.md", "new agents"),
		mk("docs/guide.md", "guide"),
		mk(".cursor/rules/go.mdc", "rule"),
		mk(".git/hooks/pre-commit", "evil"),
		mk(".github/workflows/ci.yml", "evil"),
		mk(".husky/pre-push", "evil"),
		mk(".claude/settings.json", `{"hooks":{}}`),
		mk("scripts/check.sh", "#!/bin/sh"),
		mk("existing.md", "from bundle"),
	}
	if _, _, err := stageImportedAssets(configDir, assets); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(target, "existing.md"), "mine")

	applied, skipped, err := applyStagedProjectAssetsWith(configDir, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 3 {
		t.Errorf("applied = %d, want 3; skipped: %v", applied, skipped)
	}
	for _, p := range []string{".git/hooks/pre-commit", ".github/workflows/ci.yml", ".husky/pre-push",
		".claude/settings.json", "scripts/check.sh"} {
		if _, err := os.Stat(filepath.Join(target, p)); !os.IsNotExist(err) {
			t.Errorf("%s was written", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(target, "existing.md")); string(b) != "mine" {
		t.Errorf("merge overwrote an existing file: %q", b)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(target, "AGENTS.md")); err == nil && info.Mode().Perm()&0o111 != 0 {
			t.Errorf("project file written executable: %v", info.Mode())
		}
	}

	if _, _, err := applyStagedProjectAssetsWith(configDir, target, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "existing.md")); string(b) != "from bundle" {
		t.Errorf("replace did not overwrite: %q", b)
	}
}

// Inside instruction directories a script is still a script: it stays staged.
func TestProjectAssetRefusalInsideInstructionDirs(t *testing.T) {
	written := []string{".cursor/rules/go.mdc", ".gemini/commands/ship.toml", ".cursorrules", ".clinerules/style",
		"skills/review/SKILL.md", ".claude/agents/a.md"}
	for _, p := range written {
		if r := projectAssetRefusal(p); r != "" {
			t.Errorf("%s refused: %s", p, r)
		}
	}
	staged := []string{"skills/review/scripts/run.sh", ".claude/commands/x.py", ".aider.conf.yml",
		".gemini/commands/x.sh", ".cursor/rules/hook.js"}
	for _, p := range staged {
		if projectAssetRefusal(p) == "" {
			t.Errorf("%s would be written", p)
		}
	}
}

// A path with a control character can rewrite the terminal it is printed to.
func TestSanitizeAssetPathRefusesControlCharacters(t *testing.T) {
	for _, p := range []string{"docs/\x1b[2Jx.md", "a\nb.md", "x\x00.md"} {
		if _, err := sanitizeAssetRelativePath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestRemapReportQuotesPaths(t *testing.T) {
	_, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "projects/-home-a-x/memory/m\x1b[2J.md",
		ContentPresent: true, Content: []byte("/home/a/x"),
	}}, []pathRemap{{From: "/home/a", To: "/Users/a"}}, "/Users/a")
	for _, line := range report {
		if strings.ContainsRune(line, 0x1b) {
			t.Errorf("raw escape in report: %q", line)
		}
	}
}

// A rule matches only at a segment start, never inside a longer path, and a
// rewritten path is not rewritten again by a later rule.
func TestRemapTextStartBoundaryAndSinglePass(t *testing.T) {
	remaps := []pathRemap{{From: "/home/alex", To: "/Users/alex"}, {From: "/Users/alex", To: "/x"}}
	got, n := remapText(`"/home/alex/a" "/srv/home/alex/b" "/home/alexis/c" "/home/alex"`, remaps)
	want := `"/Users/alex/a" "/srv/home/alex/b" "/home/alexis/c" "/Users/alex"`
	if got != want || n != 2 {
		t.Errorf("got  %s (%d)\nwant %s", got, n, want)
	}
}

// Windows paths in JSON have their backslashes doubled.
func TestRemapTextMatchesJSONEscapedWindowsPaths(t *testing.T) {
	remaps := []pathRemap{{From: `C:\Users\alex`, To: `D:\Users\alex`}}
	got, n := remapText(`{"a":"C:\\Users\\alex\\bin","b":"C:\Users\alex\x","c":"C:\\Users\\alexandra"}`, remaps)
	want := `{"a":"D:\\Users\\alex\\bin","b":"D:\Users\alex\x","c":"C:\\Users\\alexandra"}`
	if got != want || n != 2 {
		t.Errorf("got  %s (%d)\nwant %s", got, n, want)
	}
}

// /home/alex is another machine's home even when this one is /home/alexis.
func TestRemapWarnsAboutPrefixOfTargetHome(t *testing.T) {
	_, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json",
		ContentPresent: true, Content: []byte(`{"x":"/home/alex/tool","y":"C:\\Users\\bob\\x"}`),
	}}, nil, "/home/alexis")
	joined := strings.Join(report, "\n")
	if !strings.Contains(joined, `/home/alex;`) || !strings.Contains(joined, `bob`) {
		t.Errorf("missing warnings: %v", report)
	}
}

// Windows reads .git. as .git and a colon as an alternate data stream.
func TestProjectAssetRefusalWindowsAliases(t *testing.T) {
	for _, p := range []string{".git./hooks/x.md", ".git::$INDEX_ALLOCATION/x.md", "docs/a.md:evil"} {
		if projectAssetRefusal(p) == "" {
			t.Errorf("%s would be written", p)
		}
	}
}
