package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindReferencesKeepsPathsAndDropsTheThingsThatLookLikeThem(t *testing.T) {
	content := "Use `docs/review.md` and [the plan](docs/plan.md).\n" +
		"Go is `1.25`, the SDK is `8.0.x`, run `npm install`, see https://example.com/x.md\n" +
		"Ignore `*.sh` and `--flag` and `#anchor`.\n"

	got := FindReferences(content)
	want := map[string]bool{"docs/review.md": true, "docs/plan.md": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("took %q, which is not a file reference", g)
		}
		delete(want, g)
	}
	for missing := range want {
		t.Errorf("missed the reference %q", missing)
	}
}

// Every refusal, by name. The point of this feature is that a file which
// cannot travel is said out loud rather than dropped, so each of these must
// come back with a reason attached.
func TestResolveReferencesRefusesWhatCannotTravel(t *testing.T) {
	dir := t.TempDir()
	// EvalSymlinks, because macOS hands out /var -> /private/var and every
	// reference would otherwise look like an escape.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	outside := t.TempDir()
	if real, err := filepath.EvalSymlinks(outside); err == nil {
		outside = real
	}

	mustWrite(t, filepath.Join(dir, "ok.txt"), "fine")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "not yours")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "big.txt"), strings.Repeat("x", MaxReferenceBytes+1))
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "node_modules", "dep.md"), "vendored")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "escape.txt")); err != nil {
			t.Fatal(err)
		}
	}

	content := "`ok.txt` `/etc/passwd` `../outside.txt` `big.txt` " +
		"`node_modules/dep.md` `sub/` `missing.txt` `escape.txt`"

	byRaw := map[string]Reference{}
	for _, r := range ResolveReferences(dir, ".", content) {
		byRaw[r.Raw] = r
	}

	// "/etc/passwd" is rooted but not absolute on Windows.
	rootedWant := "absolute path"
	if runtime.GOOS == "windows" {
		rootedWant = "rooted or drive-qualified path on Windows"
	}
	cases := []struct {
		raw    string
		status ReferenceStatus
		reason string
	}{
		{"ok.txt", ReferenceTaken, ""},
		{"/etc/passwd", ReferenceRefused, rootedWant},
		{"../outside.txt", ReferenceRefused, "leaves the directory"},
		{"big.txt", ReferenceRefused, "over the"},
		{"node_modules/dep.md", ReferenceRefused, "under node_modules"},
		{"sub/", ReferenceSkipped, "names a directory"},
		{"missing.txt", ReferenceNotFound, "named but not present"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			raw    string
			status ReferenceStatus
			reason string
		}{"escape.txt", ReferenceRefused, "resolves outside the directory"})
	}

	for _, c := range cases {
		got, ok := byRaw[c.raw]
		if !ok {
			t.Errorf("%s: not classified at all", c.raw)
			continue
		}
		if got.Status != c.status {
			t.Errorf("%s: status %q, want %q (reason %q)", c.raw, got.Status, c.status, got.Reason)
		}
		if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: reason %q, want something containing %q", c.raw, got.Reason, c.reason)
		}
	}
}

// A file naming itself, and two naming each other, are ordinary things for
// documentation to do. The closure walker keeps the visited set; this asserts
// the classifier reports one entry per file so that set behaves.
func TestResolveReferencesReportsOneEntryPerFile(t *testing.T) {
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	mustWrite(t, filepath.Join(dir, "a.md"), "x")

	got := ResolveReferences(dir, ".", "`a.md` and `./a.md` again `a.md`")
	taken := 0
	for _, r := range got {
		if r.Status == ReferenceTaken {
			taken++
			if r.Rel != "a.md" {
				t.Errorf("relative path %q, want a.md", r.Rel)
			}
		}
	}
	if taken != 1 {
		t.Errorf("took the same file %d times, want 1", taken)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsWindowsRooted(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"/etc/passwd", true},
		{`\x`, true},
		{`\\server\share\x`, true},
		{`//server/share/x`, true},
		{`\\?\C:\x`, true},
		{"C:", true},
		{"C:x", true},
		{"c:/x", true},
		{`C:\x`, true},
		{"x:y", true},
		{"ok.txt", false},
		{"sub/ok.txt", false},
		{"file.txt:stream", false},
		{"1:x", false},
		{":x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isWindowsRooted("windows", c.p); got != c.want {
			t.Errorf("isWindowsRooted(windows, %q) = %v, want %v", c.p, got, c.want)
		}
		if isWindowsRooted("linux", c.p) {
			t.Errorf("isWindowsRooted(linux, %q) = true, want false", c.p)
		}
	}
}

func TestRootedReasonNamesWindowsCase(t *testing.T) {
	// None of these is filepath.IsAbs on any host, so the result depends on
	// goos only.
	for _, p := range []string{"a:b", "C:x", `\x`} {
		if got, want := rootedReason("windows", p), "rooted or drive-qualified path on Windows"; got != want {
			t.Errorf("rootedReason(windows, %q) = %q, want %q", p, got, want)
		}
		if got := rootedReason("linux", p); got != "" {
			t.Errorf("rootedReason(linux, %q) = %q, want empty", p, got)
		}
	}
}

// Claude Code imports another file with "@path". An address such as
// user@example.com is not one: the "@" must start a word.
func TestFindReferencesAtImports(t *testing.T) {
	content := "Read @docs/rules.md first.\n@~/.claude/personal.md\n" +
		"Mail nik@example.com, ping @team, see @README.md.\n"
	got := FindReferences(content)
	want := map[string]bool{"docs/rules.md": true, "~/.claude/personal.md": true, "README.md": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected reference %q in %v", g, got)
		}
		delete(want, g)
	}
	for w := range want {
		t.Errorf("missing reference %q in %v", w, got)
	}
}

func TestSecretBearingReason(t *testing.T) {
	secret := []string{".env", "app/.env", ".env.production", "certs/server.pem", "deploy.key",
		".ssh/config", "home/.aws/config", "id_ed25519", ".npmrc", "store.p12", ".git-credentials"}
	for _, p := range secret {
		if SecretBearingReason(p) == "" {
			t.Errorf("%s: not flagged", p)
		}
	}
	plain := []string{".env.example", "docs/env.md", "README.md", "keys.md", "scripts/check.sh", "config/app.toml"}
	for _, p := range plain {
		if r := SecretBearingReason(p); r != "" {
			t.Errorf("%s: flagged as %q", p, r)
		}
	}
}

// "Copy `.env.example` to `.env`" names a secrets file. It is refused, and
// the template is taken.
func TestResolveReferencesRefusesSecretFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{".env", ".env.example", "server.pem"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refs := ResolveReferences(dir, ".", "Copy `.env.example` to `.env`; the cert is `server.pem`.")
	got := map[string]ReferenceStatus{}
	for _, r := range refs {
		got[r.Raw] = r.Status
	}
	if got[".env"] != ReferenceRefused || got["server.pem"] != ReferenceRefused {
		t.Errorf("secret files not refused: %v", got)
	}
	if got[".env.example"] != ReferenceTaken {
		t.Errorf(".env.example not taken: %v", got)
	}
}

// A hard link named notes.md to .env is .env under another name.
func TestResolveReferencesRefusesHardLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, ".env"), filepath.Join(dir, "notes.md")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	refs := ResolveReferences(dir, ".", "See `notes.md`.")
	if len(refs) != 1 || refs[0].Status != ReferenceRefused {
		t.Errorf("hard link not refused: %+v", refs)
	}
}
