package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindReferencesKeepsPathsAndDropsTheThingsThatLookLikeThem(t *testing.T) {
	content := "Use `implement_pr.txt` and [the plan](docs/plan.md).\n" +
		"Go is `1.25`, the SDK is `8.0.x`, run `npm install`, see https://example.com/x.md\n" +
		"Ignore `*.sh` and `--flag` and `#anchor`.\n"

	got := FindReferences(content)
	want := map[string]bool{"implement_pr.txt": true, "docs/plan.md": true}
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
	for _, r := range ResolveReferences(dir, content) {
		byRaw[r.Raw] = r
	}

	cases := []struct {
		raw    string
		status ReferenceStatus
		reason string
	}{
		{"ok.txt", ReferenceTaken, ""},
		{"/etc/passwd", ReferenceRefused, "absolute path"},
		{"../outside.txt", ReferenceRefused, "leaves the directory via .."},
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

	got := ResolveReferences(dir, "`a.md` and `./a.md` again `a.md`")
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
