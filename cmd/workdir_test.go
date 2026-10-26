package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
)

func storedNamed(t *testing.T, cfgDir string) []locations.Named {
	t.Helper()
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	return v.Locations().Named
}

func TestWorkdirAddListRemove(t *testing.T) {
	cfgDir := scopeTestVault(t)
	root := t.TempDir()
	personal, company := filepath.Join(root, "Projects"), filepath.Join(root, "work", "company-a")
	for _, d := range []string{personal, company} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range [][]string{{"personal", personal}, {"company-a", company}} {
		if _, err := runPricing(t, workdirAddCmd, a, nil); err != nil {
			t.Fatalf("add %v: %v", a, err)
		}
	}
	if _, err := runPricing(t, workdirAddCmd, []string{"client", filepath.Join(personal, ".")}, nil); err == nil {
		t.Fatal("a second name for the same path was accepted")
	}
	got := storedNamed(t, cfgDir)
	if len(got) != 2 || got[0] != (locations.Named{Name: "personal", Path: personal}) || got[1].Name != "company-a" {
		t.Fatalf("stored %v", got)
	}

	out, err := runPricing(t, workdirListCmd, nil, map[string]string{"json": "true"})
	if err != nil {
		t.Fatal(err)
	}
	var listed []locations.Named
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("list --json = %q (%v)", out, err)
	}

	if _, err := runPricing(t, workdirRemoveCmd, []string{"personal"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, workdirRemoveCmd, []string{"personal"}, nil); err == nil {
		t.Fatal("removing an unknown name succeeded")
	}
	if got := storedNamed(t, cfgDir); len(got) != 1 || got[0].Name != "company-a" {
		t.Fatalf("after remove: %v", got)
	}
	if _, err := os.Stat(personal); err != nil {
		t.Fatal("remove touched the directory")
	}
}

func TestWorkdirListEmptyAndEscaped(t *testing.T) {
	scopeTestVault(t)
	out, err := runPricing(t, workdirListCmd, nil, map[string]string{"json": "true"})
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list --json = %q, %v", out, err)
	}
	dir := filepath.Join(t.TempDir(), "evil\x1b]0;x\x07\u009b2J")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skip("filesystem refuses control characters:", err)
	}
	if _, err := runPricing(t, workdirAddCmd, []string{"evil", dir}, nil); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []map[string]string{nil, {"json": "true"}} {
		out, err = runPricing(t, workdirListCmd, nil, flags)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out, "\x1b\x07\u009b") {
			t.Fatalf("list %v printed raw control characters: %q", flags, out)
		}
	}
	_, err = runPricing(t, workdirAddCmd, []string{"again", dir}, nil)
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\x07\u009b") {
		t.Fatalf("overlap error = %q, want an escaped refusal", err)
	}
}

// A bundle never carries this machine's declarations.
func TestExportLocationsDropNamed(t *testing.T) {
	cfgDir := scopeTestVault(t)
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, workdirAddCmd, []string{"personal", dir}, nil); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	got := captureBundleLocations(nil, v, SetupBundle{SourceHome: dir}, setupCollectOptions{WorkDirs: []string{dir}})
	if got == nil {
		t.Fatal("expected the --workdir to be recorded")
	}
	if len(got.Named) > 0 {
		t.Fatalf("bundle carries declarations: %v", got.Named)
	}
}

// Export and import record locations by merging into the vault; that write
// must keep the declarations.
func TestRecordLocationsKeepsNamed(t *testing.T) {
	cfgDir := scopeTestVault(t)
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runPricing(t, workdirAddCmd, []string{"personal", dir}, nil); err != nil {
		t.Fatal(err)
	}
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	add := locations.Set{WorkDirMaps: map[string]string{"/old/Projects": dir}}
	if err := v.SetLocations(locations.Merge(v.Locations(), add)); err != nil {
		t.Fatal(err)
	}
	if got := storedNamed(t, cfgDir); len(got) != 1 || got[0].Name != "personal" {
		t.Fatalf("declarations after a merge write: %v", got)
	}
}
