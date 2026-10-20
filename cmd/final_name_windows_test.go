//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/fsname"
	"golang.org/x/sys/windows"
)

// shortBase returns the 8.3 short name of p, skipping the test when the
// volume does not generate short names.
func shortBase(t *testing.T, p string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatalf("GetShortPathName(%q): %v", p, err)
	}
	short := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(short, filepath.Base(p)) || !strings.Contains(short, "~") {
		// CI must exercise the alias: a skip there would pass unseen.
		if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
			t.Fatalf("no 8.3 short name for %q in CI (got %q)", p, short)
		}
		t.Skipf("no 8.3 short name on this volume for %q", p)
	}
	return short
}

func TestWriterWritesTildeDigitName(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "draft~2.md")
	if err := writeRegularFileAtomic(root, dest, []byte("draft\n"), 0o600); err != nil {
		t.Fatalf("draft~2.md refused: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "draft~2.md" {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func TestWriterRefusesShortNameAlias(t *testing.T) {
	root := t.TempDir()
	long := filepath.Join(root, "settings-long-name.json")
	mustWriteFile(t, long, "orig")
	alias := filepath.Join(root, shortBase(t, long))
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteFile(t, src, "evil")

	if err := writeRegularFileAtomic(root, alias, []byte("evil"), 0o600); err == nil {
		t.Error("writeRegularFileAtomic wrote through the short name")
	}
	if err := copyRegularFile(src, root, alias, 0o600); err == nil {
		t.Error("copyRegularFile wrote through the short name")
	}
	if err := fsname.WriteFile(root, alias, []byte("evil"), 0o700, 0o600); err == nil {
		t.Error("fsname.WriteFile wrote through the short name")
	}
	if got, _ := os.ReadFile(long); string(got) != "orig" {
		t.Errorf("long file changed: %q", got)
	}
}

func TestWriterRefusesShortNameDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "long-directory-name")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, shortBase(t, dir), "x.md")
	if err := writeRegularFileAtomic(root, dest, []byte("x"), 0o600); err == nil {
		t.Error("wrote into a directory through its short name")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.md")); err == nil {
		t.Error("x.md was created in the long-named directory")
	}
}

// Windows stores "notes." as notes. That is the same name to every name
// check, so it is not refused as an alias.
func TestWriterAcceptsTrailingDotAsSameName(t *testing.T) {
	root := t.TempDir()
	if err := fsname.WriteFile(root, filepath.Join(root, "notes."), []byte("x"), 0o700, 0o600); err != nil {
		t.Fatalf("notes.: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "notes")); err != nil || string(got) != "x" {
		t.Errorf("notes = %q, %v", got, err)
	}
}

func TestSanitizeRefusesDeviceNamesOnWindows(t *testing.T) {
	if _, err := sanitizeAssetRelativePath("agents/aux.md"); err == nil {
		t.Error("aux.md accepted on Windows")
	}
}

// A reference spelled as a short name is judged by its long name.
func TestHomeReferenceShortNameJudgedByLongName(t *testing.T) {
	home := t.TempDir()
	netrc := filepath.Join(home, "docs", ".netrc")
	mustWriteFile(t, netrc, "machine x password y\n")
	raw := "docs/" + shortBase(t, netrc)
	r := resolveHomeReference(home, home, raw, nil)
	if r.reason == "" || r.abs != "" {
		t.Errorf("%s: not refused: %+v", raw, r)
	}
}

// --apply-projects refuses a short name for an existing file before it reads
// or backs it up.
func TestApplyProjectsRefusesShortNameAlias(t *testing.T) {
	cfg := t.TempDir()
	project := t.TempDir()
	long := filepath.Join(project, "settings-long-name.json")
	mustWriteFile(t, long, "orig")
	alias := shortBase(t, long)
	mustWriteFile(t, filepath.Join(cfg, setupImportedAssetsDirName, "project", alias), "evil")
	applied, skipped, err := applyStagedProjectAssetsWith(cfg, project, true)
	if err != nil || applied != 0 || len(skipped) != 1 {
		t.Errorf("applied %d, skipped %v, err %v", applied, skipped, err)
	}
	if got, _ := os.ReadFile(long); string(got) != "orig" {
		t.Errorf("long file changed: %q", got)
	}
	matches, _ := filepath.Glob(filepath.Join(project, "*.bak-*"))
	if len(matches) != 0 {
		t.Errorf("backups written: %v", matches)
	}
}
