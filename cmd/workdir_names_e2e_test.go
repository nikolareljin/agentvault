package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
)

// treeHash hashes every file under each root by relative path and content.
func treeHash(t *testing.T, roots ...string) string {
	t.Helper()
	var lines []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			lines = append(lines, p+" "+hex.EncodeToString(sum[:]))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Machine A declares personal and company work dirs and exports only
// personal. Machine B has its own company tree and declared personal at
// another path. Import places personal by name, re-slugs its memory there,
// and leaves B's company tree and memory byte-identical.
func TestWorkDirNamesMoveIsolated(t *testing.T) {
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
	dstCompanyMem := mem(dstHome, dstCompany, "machine B company note\n")
	mustWriteFile(t, filepath.Join(dstCompany, "CLAUDE.md"), "B company rules\n")

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

	bundlePath := filepath.Join(base, "move"+DefaultExportExtension)
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{
		"yes": "true", "include-secrets": "false", "detect": "false", "include-memory": "true", "workdirs": "personal",
	})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decryptBundlePayload(raw, e2eBundlePassword)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"name": "personal"`)) {
		t.Fatalf("bundle does not name the personal work dir")
	}
	if bytes.Contains(payload, []byte("company-a secret")) || bytes.Contains(payload, []byte(`"name": "company"`)) || bytes.Contains(payload, []byte(srcCompany)) {
		t.Fatalf("bundle carries the unselected work dir")
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
	withFlags(t, importCmd, map[string]string{"apply-provider-configs": "true"})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("import: %v\n%s", err, in.String())
	}

	got, err := os.ReadFile(filepath.Join(dstHome, ".claude", "projects", claudeProjectSlug(dstPersonal), "memory", "MEMORY.md"))
	if err != nil || string(got) != "personal note\n" {
		t.Fatalf("personal memory not placed by name: %q %v\n%s", got, err, in.String())
	}
	if _, err := os.Stat(filepath.Join(dstHome, ".claude", "projects", claudeProjectSlug(srcPersonal))); !os.IsNotExist(err) {
		t.Errorf("memory kept the source folder name: %v", err)
	}
	if after := treeHash(t, dstCompany, filepath.Dir(filepath.Dir(dstCompanyMem))); after != before {
		t.Errorf("import changed machine B's company tree or memory")
	}
}

// declare adds named work dirs to the vault in cfgDir.
func declare(t *testing.T, cfgDir string, names map[string]string) {
	t.Helper()
	v := vault.New(filepath.Join(cfgDir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	set := v.Locations()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var err error
	for _, k := range keys {
		if set, err = set.Declare(k, names[k]); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.SetLocations(set); err != nil {
		t.Fatal(err)
	}
}

// remapTo returns where res sends the bundle path from, or "".
func remapTo(res locationResult, from string) string {
	for _, r := range res.Remaps {
		if r.From == from {
			return r.To
		}
	}
	return ""
}

func TestWorkDirPlacedByName(t *testing.T) {
	_, set, o := matchFixture(t)
	set.WorkDirs[0].Name = "personal"
	// Nothing computed: only a name can place it.
	if err := os.RemoveAll(filepath.Join(o.Home, "Projects")); err != nil {
		t.Fatal(err)
	}
	declared := filepath.Join(o.Home, "declared")
	flagged := filepath.Join(o.Home, "flagged")
	byPath := filepath.Join(o.Home, "bypath")
	for _, d := range []string{declared, flagged, byPath} {
		mustMkdirAll(t, d)
	}
	o.AcceptMatches = true
	o.Known.Named = []locations.Named{{Name: "personal", Path: declared}}
	run := func(o locationOptions) locationResult {
		t.Helper()
		res, err := resolveBundleLocations(context.Background(), set, "", o)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if got := remapTo(run(o), "/old/Projects"); got != declared {
		t.Errorf("declared name: got %q, want %q", got, declared)
	}
	o.WorkDirNames = map[string]string{"personal": flagged}
	o.WorkDirMaps = map[string]string{}
	if got := remapTo(run(o), "/old/Projects"); got != flagged {
		t.Errorf("--workdir-map NAME: got %q, want %q", got, flagged)
	}
	if len(o.WorkDirMaps) != 0 {
		t.Errorf("caller's map changed: %v", o.WorkDirMaps)
	}
	o.WorkDirMaps = map[string]string{"/old/Projects": byPath}
	if got := remapTo(run(o), "/old/Projects"); got != byPath {
		t.Errorf("--workdir-map OLD: got %q, want %q", got, byPath)
	}
	if o.WorkDirMaps["/old/Projects"] != byPath || len(o.WorkDirMaps) != 1 {
		t.Errorf("caller's map changed: %v", o.WorkDirMaps)
	}
	// Another name, or none, is not placed by this one.
	o.WorkDirMaps, o.WorkDirNames = nil, nil
	set.WorkDirs[0].Name = "other"
	if got := remapTo(run(o), "/old/Projects"); got != "" {
		t.Errorf("placed by an unrelated name: %q", got)
	}
}

func TestWorkDirNameFromBundleChecked(t *testing.T) {
	_, set, o := matchFixture(t)
	if err := os.RemoveAll(filepath.Join(o.Home, "Projects")); err != nil {
		t.Fatal(err)
	}
	set.WorkDirs[0].Name = "Bad\x1bName"
	o.Known.Named = []locations.Named{{Name: "Bad\x1bName", Path: o.Home}}
	var out bytes.Buffer
	o.Out = &out
	res, err := resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if remapTo(res, "/old/Projects") != "" || !strings.Contains(out.String(), "not a valid work dir name") || strings.Contains(out.String(), "\x1b") {
		t.Errorf("bad name used or printed raw: %+v\n%q", res.Remaps, out.String())
	}

	set.WorkDirs[0].Name = "personal"
	res, err = resolveBundleLocations(context.Background(), set, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missed) != 1 || !strings.Contains(res.Missed[0].Hint, "--workdir-map personal=<path here>") {
		t.Errorf("missed = %+v", res.Missed)
	}
}

func TestCheckWorkDirNames(t *testing.T) {
	profiles := []PortableProfile{{Setup: SetupBundle{Locations: &locations.Set{WorkDirs: []locations.WorkDir{{Abs: "/a", Name: "personal"}}}}}, {}}
	if err := checkWorkDirNames(profiles, map[string]string{"personal": "/x"}); err != nil {
		t.Error(err)
	}
	if err := checkWorkDirNames(profiles, map[string]string{"persnal": "/x"}); err == nil || !strings.Contains(err.Error(), "persnal") {
		t.Errorf("typo accepted: %v", err)
	}
}

func TestExportNamesOnlySelectedWorkDirs(t *testing.T) {
	w := setupTwoWorkDirs(t)
	// A name the vault learned from another machine's bundle is not this
	// machine's to export.
	set := w.v.Locations()
	set.WorkDirs = append(set.WorkDirs, locations.WorkDir{ID: "w9", Abs: filepath.Join(w.home, "elsewhere"), Name: "foreign"})
	if err := w.v.SetLocations(set); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, d := range exportTwo(t, w, "personal").Locations.WorkDirs {
		if d.Name != "" {
			names[d.Name] = d.Abs
		}
	}
	if len(names) != 1 || names["personal"] != w.personal {
		t.Errorf("names = %v", names)
	}
	names = map[string]string{}
	for _, d := range exportTwo(t, w, "all").Locations.WorkDirs {
		if d.Name != "" {
			names[d.Name] = d.Abs
		}
	}
	if len(names) != 2 || names["personal"] != w.personal || names["company"] != w.acme {
		t.Errorf("names = %v", names)
	}
}

func TestRecordLocationsDropsBundleNames(t *testing.T) {
	v, _ := sessionTestVault(t, nil)
	match := locationResult{Stored: locations.Set{
		WorkDirs: []locations.WorkDir{{ID: "w1", Abs: "/x", Name: "personal"}},
		Projects: []locations.Project{{ID: "p1", WorkDir: "w1", Abs: "/x/app", GitRemote: "github.com/me/app"}},
	}}
	o := locationOptions{}
	if err := recordLocations(v, match, &o); err != nil {
		t.Fatal(err)
	}
	for _, d := range v.Locations().WorkDirs {
		if d.Name != "" {
			t.Errorf("vault kept the bundle's name: %+v", d)
		}
	}
	if match.Stored.WorkDirs[0].Name != "personal" {
		t.Errorf("caller's result changed")
	}
}
