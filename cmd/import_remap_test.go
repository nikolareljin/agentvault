package cmd

import (
	"encoding/json"
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

// OLD must be an absolute path, and a bundle's own home or project of "/"
// or a relative path is not used: either would rewrite ordinary text.
func TestBuildPathRemapsRefusesRootsAndRelativePaths(t *testing.T) {
	for _, m := range []string{"src=lib", "/=/x", `C:\=D:\`, "~/a=/b"} {
		if _, err := buildPathRemaps("", "", "", "", []string{m}); err == nil {
			t.Errorf("--map %s accepted", m)
		}
	}
	if _, err := buildPathRemaps("", "", "", "", []string{`C:\Users\a=/home/a`}); err != nil {
		t.Errorf("Windows OLD refused: %v", err)
	}
	remaps, err := buildPathRemaps("/", "/home/alex", "app", "/srv/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaps) != 0 {
		t.Errorf("bundle roots used: %v", remaps)
	}
}

// A path moved between Windows and POSIX gets the separators of its new home
// all the way to its end, and stays valid JSON.
func TestRemapTextConvertsSeparatorsAcrossStyles(t *testing.T) {
	w2p := []pathRemap{{From: `C:\Users\alex`, To: "/home/b"}}
	got, n := remapText(`{"a":"C:\\Users\\alex\\bin\\x.exe --flag","b":"c:\\users\\ALEX\\y\n"}`, w2p)
	want := `{"a":"/home/b/bin/x.exe --flag","b":"/home/b/y\n"}`
	if got != want || n != 2 {
		t.Errorf("got  %s (%d)\nwant %s", got, n, want)
	}
	got, _ = remapText(`run C:\Users\alex\bin\x now`, w2p)
	if got != `run /home/b/bin/x now` {
		t.Errorf("plain text: %s", got)
	}
	p2w := []pathRemap{{From: "/home/alex", To: `C:\Users\b`}}
	got, _ = remapText(`{"a":"/home/alex/bin/x","b":"/home/alex"}`, p2w)
	want = `{"a":"C:\\Users\\b\\bin\\x","b":"C:\\Users\\b"}`
	if got != want || !json.Valid([]byte(got)) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	got, _ = remapText("cmd: /home/alex/bin/x:/usr/bin", p2w)
	if got != `cmd: C:\Users\b\bin\x:/usr/bin` {
		t.Errorf("plain text: %s", got)
	}
}

// A Windows home is found whatever its case.
func TestRemapWarnsAboutLowerCaseWindowsHome(t *testing.T) {
	_, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json",
		ContentPresent: true, Content: []byte(`{"x":"c:\\users\\bob\\x"}`),
	}}, nil, "/home/b")
	if !strings.Contains(strings.Join(report, "\n"), "bob") {
		t.Errorf("no warning: %v", report)
	}
}

// This machine's own Windows home is not foreign in its JSON spelling or
// another case.
func TestRemapNoWarningForOwnWindowsHome(t *testing.T) {
	_, report := remapAssets([]SetupAsset{{
		LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "settings.json",
		ContentPresent: true, Content: []byte(`{"x":"C:\\Users\\me\\x","y":"c:\\users\\ME\\y"}`),
	}}, nil, `C:\Users\me`)
	if len(report) != 0 {
		t.Errorf("warned about own home: %v", report)
	}
}

// Inside a JSON string the path runs to the closing quote, spaces included.
func TestRemapTextConvertsJSONPathWithSpaces(t *testing.T) {
	got, _ := remapText(`{"a":"C:\\Users\\a\\My Docs\\x","b":"q"}`, []pathRemap{{From: `C:\Users\a`, To: "/home/b"}})
	if want := `{"a":"/home/b/My Docs/x","b":"q"}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	got, _ = remapText(`{"a":"C:\\Users\\a\\x --out C:\\tmp\\y","b":"C:\\Users\\a\\x d:\\z ~/w /v/u"}`, []pathRemap{{From: `C:\Users\a`, To: "/home/b"}})
	if want := `{"a":"/home/b/x --out C:\\tmp\\y","b":"/home/b/x d:\\z ~/w /v/u"}`; got != want {
		t.Errorf("unrelated path converted:\ngot  %s\nwant %s", got, want)
	}
	got, _ = remapText(`{"a":"/home/a/My Docs/x"}`, []pathRemap{{From: "/home/a", To: `C:\Users\b`}})
	if want := `{"a":"C:\\Users\\b\\My Docs\\x"}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestForeignHomePathsStartAPath(t *testing.T) {
	got := foreignHomePaths(`cd /mnt/c/Users/b/x; /home/alex/bin "C:\\Users\\Ann\\y" /srv/home/z ~/home/q`)
	want := []string{"/home/alex", `C:\\Users\\Ann`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("foreignHomePaths = %q, want %q", got, want)
	}
	if got := foreignHomePaths("/Users/b/notes"); len(got) != 1 || got[0] != "/Users/b" {
		t.Fatalf("at start of text: %q", got)
	}
}

// C:/Users/alex is a Windows home too, as Git Bash and Node write it.
func TestRemapWindowsHomeWithForwardSlashes(t *testing.T) {
	w2p := []pathRemap{{From: `C:\Users\alex`, To: "/home/alex"}}
	got, n := remapText(`{"a":"c:/Users/alex/bin/x","b":"C:/Users/alexandra"}`, w2p)
	if n != 1 || got != `{"a":"/home/alex/bin/x","b":"C:/Users/alexandra"}` {
		t.Fatalf("w2p = %d %s", n, got)
	}
	w2w := []pathRemap{{From: `C:\Users\alex`, To: `D:\Users\bo`}}
	got, _ = remapText(`{"a":"C:/Users/alex/x"}`, w2w)
	if got != `{"a":"D:\\Users\\bo/x"}` {
		t.Fatalf("w2w = %s", got)
	}
	if got := foreignHomePaths(`"c:/Users/zed/x"`); len(got) != 1 || got[0] != "c:/Users/zed" {
		t.Fatalf("foreignHomePaths = %q", got)
	}
	if isTargetHome("c:/users/BO", `C:\Users\bo`) != true {
		t.Fatal("own home in forward slashes reported as foreign")
	}
}

// file:///home/alex is the home path after a URL scheme.
func TestRemapTextFileURL(t *testing.T) {
	out, n := remapText(`{"u":"file:///home/alex/x"}`, []pathRemap{{"/home/alex", "/Users/alex"}})
	if n != 1 || out != `{"u":"file:///Users/alex/x"}` {
		t.Errorf("got %s (%d)", out, n)
	}
	if got := foreignHomePaths("see file:///home/bob/x"); len(got) != 1 || got[0] != "/home/bob" {
		t.Errorf("warning missed: %v", got)
	}
}
