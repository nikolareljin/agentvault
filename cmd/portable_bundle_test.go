package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileNameForDir(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"/home/u/.config/agentvault", "default"},
		{"/home/u/.config/agentvault/", "default"},
		{"/home/u/.agentvault", "default"},
		{"/home/u/.config/agentvault-work", "work"},
		{"/home/u/.config/agentvault_home", "home"},
		{"/home/u/.config/vaults/team", "team"},
	}
	for _, tc := range cases {
		if got := profileNameForDir(tc.dir); got != tc.want {
			t.Errorf("profileNameForDir(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestDedupeProfileNames(t *testing.T) {
	in := []discoveredProfile{
		{Name: "default", ConfigDir: "/a"},
		{Name: "default", ConfigDir: "/b"},
		{Name: "work", ConfigDir: "/c"},
	}
	out := dedupeProfileNames(in)
	got := []string{out[0].Name, out[1].Name, out[2].Name}
	want := []string{"default", "default-2", "work"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupeProfileNames() names = %v, want %v", got, want)
		}
	}
}

func TestDiscoverProfilesFindsSiblingConfigDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")

	for _, dir := range []string{"agentvault", "agentvault-work"} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", full, err)
		}
		if err := os.WriteFile(filepath.Join(full, "vault.enc"), []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile error = %v", err)
		}
	}
	// A directory without a vault file must not become a profile.
	if err := os.MkdirAll(filepath.Join(root, "agentvault-empty"), 0700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	profiles := discoverProfiles("")
	names := discoveredProfileNames(profiles)
	if len(profiles) != 2 {
		t.Fatalf("discoverProfiles() = %v, want 2 profiles", names)
	}
	if names[0] != "default" || names[1] != "work" {
		t.Fatalf("discoverProfiles() names = %v, want [default work]", names)
	}
	if !profiles[0].IsDefault {
		t.Fatalf("discoverProfiles()[0].IsDefault = false, want true for the active config dir")
	}
}

func TestDiscoverProfilesHonorsExplicitConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)

	profiles := discoverProfiles("/custom/agentvault-team")
	if len(profiles) != 1 {
		t.Fatalf("discoverProfiles(explicit) = %d profiles, want 1", len(profiles))
	}
	if profiles[0].Name != "team" || profiles[0].ConfigDir != "/custom/agentvault-team" {
		t.Fatalf("discoverProfiles(explicit) = %#v, want the given directory as profile team", profiles[0])
	}
}

func TestDiscoverProfilesIncludesEnvConfigDirs(t *testing.T) {
	root := t.TempDir()
	extra := filepath.Join(t.TempDir(), "team-vault")
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	if err := os.MkdirAll(extra, 0700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(extra, "vault.enc"), []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	t.Setenv(ConfigDirsEnv, extra)

	names := discoveredProfileNames(discoverProfiles(""))
	found := false
	for _, n := range names {
		if n == "team-vault" {
			found = true
		}
	}
	if !found {
		t.Fatalf("discoverProfiles() names = %v, want the %s entry included", names, ConfigDirsEnv)
	}
}

func TestDetectBundleFormat(t *testing.T) {
	portable, err := json.Marshal(PortableBundle{SchemaVersion: PortableBundleSchemaVersion})
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	setup, err := json.Marshal(SetupBundle{Version: "1.0"})
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	cases := []struct {
		name string
		data []byte
		want bundleFormat
	}{
		{"portable", portable, bundleFormatPortable},
		{"setup", setup, bundleFormatSetup},
		{"vault export", []byte(`{"agents":[],"shared":{}}`), bundleFormatVaultData},
		{"ciphertext", []byte{0x00, 0x01, 0x02}, bundleFormatUnknown},
		{"unrelated json", []byte(`{"hello":"world"}`), bundleFormatUnknown},
	}
	for _, tc := range cases {
		if got := detectBundleFormat(tc.data); got != tc.want {
			t.Errorf("detectBundleFormat(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDecodePortableBundleWrapsSetupBundle(t *testing.T) {
	data, err := json.Marshal(SetupBundle{Version: "1.0", SourceMachine: "laptop"})
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	bundle, format, err := decodePortableBundle(data)
	if err != nil {
		t.Fatalf("decodePortableBundle() error = %v", err)
	}
	if format != bundleFormatSetup {
		t.Fatalf("format = %v, want setup", format)
	}
	if len(bundle.Profiles) != 1 || bundle.Profiles[0].Name != "default" {
		t.Fatalf("profiles = %v, want a single default profile", bundle.ProfileNames())
	}
	if bundle.Profiles[0].Setup.SourceMachine != "laptop" {
		t.Fatalf("wrapped setup lost SourceMachine: %#v", bundle.Profiles[0].Setup)
	}
}

func TestDecodePortableBundleWrapsVaultExport(t *testing.T) {
	raw := []byte(`{"agents":[{"name":"alpha"}],"shared":{"system_prompt":"be brief"},"provider_configs":{},"sessions":{}}`)
	bundle, format, err := decodePortableBundle(raw)
	if err != nil {
		t.Fatalf("decodePortableBundle() error = %v", err)
	}
	if format != bundleFormatVaultData {
		t.Fatalf("format = %v, want vault export", format)
	}
	setup := bundle.Profiles[0].Setup
	if len(setup.Agents) != 1 || setup.Agents[0].Name != "alpha" {
		t.Fatalf("agents = %#v, want alpha", setup.Agents)
	}
	if setup.SharedConfig.SystemPrompt != "be brief" {
		t.Fatalf("system prompt = %q, want %q", setup.SharedConfig.SystemPrompt, "be brief")
	}
}

func TestDecodePortableBundleRejectsEmptyProfiles(t *testing.T) {
	data := []byte(`{"schema_version":"2.0","profiles":[]}`)
	if _, _, err := decodePortableBundle(data); err == nil {
		t.Fatal("decodePortableBundle() error = nil, want an error for a bundle with no profiles")
	}
}

func TestPortableBundleMarshalsEmptyProfilesAsArray(t *testing.T) {
	data, err := json.Marshal(PortableBundle{SchemaVersion: PortableBundleSchemaVersion})
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if !strings.Contains(string(data), `"profiles":[]`) {
		t.Fatalf("marshal output = %s, want an empty profiles array", data)
	}
}

func TestEncryptDecryptBundlePayloadRoundTrip(t *testing.T) {
	plaintext := []byte(`{"schema_version":"2.0","profiles":[]}`)
	encrypted, err := encryptBundlePayload(plaintext, "correct horse")
	if err != nil {
		t.Fatalf("encryptBundlePayload() error = %v", err)
	}
	if detectBundleFormat(encrypted) != bundleFormatUnknown {
		t.Fatal("encrypted payload must not parse as JSON")
	}
	decrypted, err := decryptBundlePayload(encrypted, "correct horse")
	if err != nil {
		t.Fatalf("decryptBundlePayload() error = %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("round trip = %s, want %s", decrypted, plaintext)
	}
	if _, err := decryptBundlePayload(encrypted, "wrong password"); err == nil {
		t.Fatal("decryptBundlePayload() with the wrong password error = nil, want an error")
	}
}

func TestEncryptBundlePayloadRejectsShortPassword(t *testing.T) {
	if _, err := encryptBundlePayload([]byte("{}"), "short"); err == nil {
		t.Fatal("encryptBundlePayload() error = nil, want a minimum length error")
	}
}

func TestDefaultExportPathNaming(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)

	got := defaultExportPath(time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC))
	if filepath.Dir(got) != filepath.Join(root, "agentvault", DefaultExportDirName) {
		t.Fatalf("defaultExportPath() dir = %s, want the exports dir under the config dir", filepath.Dir(got))
	}
	if !strings.HasSuffix(got, DefaultExportExtension) {
		t.Fatalf("defaultExportPath() = %s, want the %s extension", got, DefaultExportExtension)
	}
	if !strings.Contains(filepath.Base(got), "20260901-123000") {
		t.Fatalf("defaultExportPath() = %s, want the timestamp in the filename", got)
	}
}

func TestSanitizeFilenamePart(t *testing.T) {
	cases := map[string]string{
		"my-host":    "my-host",
		"host.local": "host-local",
		"a/b":        "a-b",
		"":           "agentvault",
		"///":        "agentvault",
		"Host_01":    "Host_01",
	}
	for in, want := range cases {
		if got := sanitizeFilenamePart(in); got != want {
			t.Errorf("sanitizeFilenamePart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscoverProfilesFindsHiddenHomeDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv(ConfigDirsEnv, "")

	hidden := filepath.Join(root, ".agentvault-legacy")
	if err := os.MkdirAll(hidden, 0700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "vault.enc"), []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	names := discoveredProfileNames(discoverProfiles(""))
	found := false
	for _, n := range names {
		if n == "legacy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("discoverProfiles() names = %v, want the hidden ~/.agentvault-legacy directory included", names)
	}
}

func TestDedupeProfileNamesIsCaseInsensitive(t *testing.T) {
	// Selection resolves names case-insensitively, so dedupe must reserve them the
	// same way or `--profile work` would silently match only one of the two.
	out := dedupeProfileNames([]discoveredProfile{
		{Name: "Work", ConfigDir: "/a"},
		{Name: "work", ConfigDir: "/b"},
	})
	if strings.EqualFold(out[0].Name, out[1].Name) {
		t.Fatalf("dedupeProfileNames() = %q and %q, want names that differ case-insensitively",
			out[0].Name, out[1].Name)
	}
}

func TestDedupeProfileNamesSkipsTakenSuffixes(t *testing.T) {
	// A directory literally named agentvault-work-2 must not collide with the
	// renamed second `work`.
	out := dedupeProfileNames([]discoveredProfile{
		{Name: "work", ConfigDir: "/a"},
		{Name: "work-2", ConfigDir: "/b"},
		{Name: "work", ConfigDir: "/c"},
	})
	seen := make(map[string]struct{}, len(out))
	for _, p := range out {
		key := strings.ToLower(p.Name)
		if _, dup := seen[key]; dup {
			t.Fatalf("dedupeProfileNames() produced duplicate name %q in %v", p.Name, discoveredProfileNames(out))
		}
		seen[key] = struct{}{}
	}
}
