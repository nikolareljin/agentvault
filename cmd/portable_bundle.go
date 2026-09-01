package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/crypto"
)

// PortableBundleSchemaVersion is the schema version of multi-profile bundles
// written by `agentvault export`.
const PortableBundleSchemaVersion = "2.0"

// DefaultExportDirName is the directory under the config dir where exports land
// when no output path is given.
const DefaultExportDirName = "exports"

// DefaultExportExtension is the file extension used for generated bundles.
const DefaultExportExtension = ".avbundle"

// ExportPasswordEnv supplies the bundle password for non-interactive exports.
const ExportPasswordEnv = "AGENTVAULT_EXPORT_PASSWORD"

// ImportPasswordEnv supplies the bundle password for non-interactive imports.
const ImportPasswordEnv = "AGENTVAULT_IMPORT_PASSWORD"

// ConfigDirsEnv lists extra agentvault config directories to consider as
// profiles, separated by the OS path list separator.
const ConfigDirsEnv = "AGENTVAULT_CONFIG_DIRS"

// nowFunc is the clock used for generated filenames; overridable in tests.
var nowFunc = time.Now

// PortableBundle is the portable, multi-profile export format. One bundle can
// carry several machine profiles so a single file replicates every agentvault
// configuration present on a machine.
type PortableBundle struct {
	SchemaVersion string            `json:"schema_version"`
	CreatedAt     time.Time         `json:"created_at"`
	SourceMachine string            `json:"source_machine"`
	SourceOS      string            `json:"source_os"`
	Profiles      []PortableProfile `json:"profiles"`
}

// PortableProfile is one agentvault configuration directory captured in a bundle.
// Setup reuses the v1 SetupBundle payload so v1 and v2 share all collection,
// staging and apply logic.
type PortableProfile struct {
	Name      string      `json:"name"`
	ConfigDir string      `json:"config_dir,omitempty"`
	Machine   string      `json:"machine,omitempty"`
	OS        string      `json:"os,omitempty"`
	Setup     SetupBundle `json:"setup"`
}

// MarshalJSON normalizes a nil profile slice to [] for stable bundle output.
func (b PortableBundle) MarshalJSON() ([]byte, error) {
	type alias PortableBundle
	out := b
	if out.Profiles == nil {
		out.Profiles = []PortableProfile{}
	}
	return json.Marshal(alias(out))
}

// ProfileNames returns the profile names in bundle order.
func (b PortableBundle) ProfileNames() []string {
	names := make([]string, 0, len(b.Profiles))
	for _, p := range b.Profiles {
		names = append(names, p.Name)
	}
	return names
}

// FindProfile returns the profile with the given name, matched case-insensitively.
func (b PortableBundle) FindProfile(name string) (PortableProfile, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, p := range b.Profiles {
		if strings.ToLower(p.Name) == want {
			return p, true
		}
	}
	return PortableProfile{}, false
}

// discoveredProfile is a candidate agentvault config directory found on disk.
type discoveredProfile struct {
	Name      string
	ConfigDir string
	IsDefault bool
}

// discoverProfiles finds every agentvault config directory on this machine that
// holds a vault file. When explicitConfigDir is non-empty (the --config flag)
// only that directory is returned, because the caller asked for one profile.
//
// Discovery order is deterministic: the active config dir first, then the XDG
// and home fallbacks, then sibling `agentvault-*` directories, then anything
// listed in AGENTVAULT_CONFIG_DIRS. Duplicates are collapsed by resolved path.
func discoverProfiles(explicitConfigDir string) []discoveredProfile {
	if strings.TrimSpace(explicitConfigDir) != "" {
		return []discoveredProfile{{
			Name:      profileNameForDir(explicitConfigDir),
			ConfigDir: explicitConfigDir,
			IsDefault: true,
		}}
	}

	active := config.Dir()
	candidates := []string{active}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", config.AppName),
			filepath.Join(home, "."+config.AppName),
		)
		candidates = append(candidates, siblingProfileDirs(filepath.Join(home, ".config"))...)
	}
	candidates = append(candidates, siblingProfileDirs(filepath.Dir(active))...)
	candidates = append(candidates, envConfigDirs()...)

	seen := make(map[string]struct{}, len(candidates))
	var found []discoveredProfile
	for _, dir := range candidates {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		key := dir
		if abs, err := filepath.Abs(dir); err == nil {
			key = abs
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := os.Stat(filepath.Join(dir, config.VaultFile)); err != nil {
			continue
		}
		found = append(found, discoveredProfile{
			Name:      profileNameForDir(dir),
			ConfigDir: dir,
			IsDefault: key == absOrSelf(active),
		})
	}
	return dedupeProfileNames(found)
}

// siblingProfileDirs returns `agentvault*` directories directly under parent.
// This picks up side-by-side setups such as ~/.config/agentvault-work.
func siblingProfileDirs(parent string) []string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), config.AppName) {
			continue
		}
		dirs = append(dirs, filepath.Join(parent, e.Name()))
	}
	sort.Strings(dirs)
	return dirs
}

// envConfigDirs parses AGENTVAULT_CONFIG_DIRS into a directory list.
func envConfigDirs() []string {
	raw := strings.TrimSpace(os.Getenv(ConfigDirsEnv))
	if raw == "" {
		return nil
	}
	var dirs []string
	for _, part := range strings.Split(raw, string(os.PathListSeparator)) {
		part = strings.TrimSpace(part)
		if part != "" {
			dirs = append(dirs, part)
		}
	}
	return dirs
}

// profileNameForDir derives a stable profile name from a config directory path.
// The canonical agentvault directory becomes "default"; a suffixed directory
// such as agentvault-work becomes "work".
func profileNameForDir(dir string) string {
	base := filepath.Base(strings.TrimRight(dir, string(os.PathSeparator)))
	base = strings.TrimPrefix(base, ".")
	if base == config.AppName || base == "" {
		return "default"
	}
	if trimmed := strings.TrimPrefix(base, config.AppName); trimmed != base {
		trimmed = strings.Trim(trimmed, "-_.")
		if trimmed != "" {
			return trimmed
		}
	}
	return base
}

// dedupeProfileNames makes profile names unique by suffixing collisions.
func dedupeProfileNames(profiles []discoveredProfile) []discoveredProfile {
	used := make(map[string]int, len(profiles))
	for i := range profiles {
		name := profiles[i].Name
		if n, ok := used[name]; ok {
			n++
			used[name] = n
			profiles[i].Name = fmt.Sprintf("%s-%d", name, n)
			continue
		}
		used[name] = 1
	}
	return profiles
}

func absOrSelf(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// defaultExportDir returns the directory bundles are written to when no output
// path is supplied.
func defaultExportDir() string {
	return filepath.Join(effectiveConfigDir(), DefaultExportDirName)
}

// defaultExportPath builds the default bundle path, named by host and timestamp
// so repeated exports never silently overwrite each other.
func defaultExportPath(now time.Time) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "agentvault"
	}
	host = sanitizeFilenamePart(host)
	name := fmt.Sprintf("%s-%s%s", host, now.Format("20060102-150405"), DefaultExportExtension)
	return filepath.Join(defaultExportDir(), name)
}

// sanitizeFilenamePart reduces a string to characters safe in a filename.
func sanitizeFilenamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "agentvault"
	}
	return out
}

// newPortableBundle returns an empty bundle stamped with this machine's identity.
func newPortableBundle(now time.Time) PortableBundle {
	host, _ := os.Hostname()
	return PortableBundle{
		SchemaVersion: PortableBundleSchemaVersion,
		CreatedAt:     now,
		SourceMachine: host,
		SourceOS:      runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// encryptBundlePayload encrypts plaintext with a password-derived key, using the
// same [salt][ciphertext] layout as the vault file itself.
func encryptBundlePayload(plaintext []byte, password string) ([]byte, error) {
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	salt, err := crypto.GenerateSalt()
	if err != nil {
		return nil, err
	}
	key, err := crypto.DeriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	ciphertext, err := crypto.Encrypt(plaintext, key)
	if err != nil {
		return nil, err
	}
	return append(salt, ciphertext...), nil
}

// decryptBundlePayload reverses encryptBundlePayload.
func decryptBundlePayload(data []byte, password string) ([]byte, error) {
	if len(data) < crypto.SaltLen {
		return nil, errors.New("bundle file is too short (corrupted?)")
	}
	key, err := crypto.DeriveKey(password, data[:crypto.SaltLen])
	if err != nil {
		return nil, err
	}
	plaintext, err := crypto.Decrypt(data[crypto.SaltLen:], key)
	if err != nil {
		return nil, errors.New("wrong password or corrupted bundle")
	}
	return plaintext, nil
}

// bundleFormat identifies which on-disk export layout a payload uses.
type bundleFormat int

const (
	bundleFormatUnknown bundleFormat = iota
	// bundleFormatPortable is the v2 multi-profile bundle.
	bundleFormatPortable
	// bundleFormatSetup is the v1 `setup export` bundle.
	bundleFormatSetup
	// bundleFormatVaultData is the legacy `export` vault payload.
	bundleFormatVaultData
)

func (f bundleFormat) String() string {
	switch f {
	case bundleFormatPortable:
		return "portable bundle (schema 2.x)"
	case bundleFormatSetup:
		return "setup bundle (schema 1.x)"
	case bundleFormatVaultData:
		return "vault export"
	default:
		return "unknown"
	}
}

// detectBundleFormat inspects decoded JSON and reports which export layout it is.
// The three formats are distinguished by marker keys rather than by trying each
// decoder in turn, because all three decode successfully into one another with
// mostly-zero fields.
func detectBundleFormat(data []byte) bundleFormat {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return bundleFormatUnknown
	}
	if _, ok := probe["profiles"]; ok {
		if _, hasSchema := probe["schema_version"]; hasSchema {
			return bundleFormatPortable
		}
	}
	// "agents" and "provider_configs" appear in both a setup bundle and a legacy
	// vault export, so only keys unique to the setup schema identify it.
	for _, key := range []string{"install_guide", "shared_config", "workflow_templates", "provider_files", "project_files"} {
		if _, ok := probe[key]; ok {
			return bundleFormatSetup
		}
	}
	for _, key := range []string{"agents", "shared"} {
		if _, ok := probe[key]; ok {
			return bundleFormatVaultData
		}
	}
	return bundleFormatUnknown
}

// decodePortableBundle parses any supported export payload into the v2 shape.
// v1 setup bundles and legacy vault exports are wrapped in a single "default"
// profile so downstream import logic only handles one structure.
func decodePortableBundle(data []byte) (PortableBundle, bundleFormat, error) {
	format := detectBundleFormat(data)
	switch format {
	case bundleFormatPortable:
		var bundle PortableBundle
		if err := json.Unmarshal(data, &bundle); err != nil {
			return PortableBundle{}, format, fmt.Errorf("decoding portable bundle: %w", err)
		}
		if len(bundle.Profiles) == 0 {
			return PortableBundle{}, format, errors.New("portable bundle contains no profiles")
		}
		return bundle, format, nil
	case bundleFormatSetup:
		var setup SetupBundle
		if err := json.Unmarshal(data, &setup); err != nil {
			return PortableBundle{}, format, fmt.Errorf("decoding setup bundle: %w", err)
		}
		return wrapSetupBundle(setup), format, nil
	case bundleFormatVaultData:
		setup, err := setupBundleFromVaultExport(data)
		if err != nil {
			return PortableBundle{}, format, err
		}
		return wrapSetupBundle(setup), format, nil
	default:
		return PortableBundle{}, format, errors.New("unrecognized export format")
	}
}

// wrapSetupBundle lifts a v1 setup bundle into a single-profile v2 bundle.
func wrapSetupBundle(setup SetupBundle) PortableBundle {
	return PortableBundle{
		SchemaVersion: PortableBundleSchemaVersion,
		CreatedAt:     setup.CreatedAt,
		SourceMachine: setup.SourceMachine,
		SourceOS:      setup.SourceOS,
		Profiles: []PortableProfile{{
			Name:    "default",
			Machine: setup.SourceMachine,
			OS:      setup.SourceOS,
			Setup:   setup,
		}},
	}
}

// setupBundleFromVaultExport converts a legacy vault export into a setup bundle
// so it can flow through the same import path as newer formats.
func setupBundleFromVaultExport(data []byte) (SetupBundle, error) {
	var payload struct {
		Agents          json.RawMessage `json:"agents"`
		Shared          json.RawMessage `json:"shared"`
		ProviderConfigs json.RawMessage `json:"provider_configs"`
		Sessions        json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return SetupBundle{}, fmt.Errorf("decoding vault export: %w", err)
	}
	bundle := SetupBundle{Version: "1.0", CreatedAt: time.Now()}
	if len(payload.Agents) > 0 {
		if err := json.Unmarshal(payload.Agents, &bundle.Agents); err != nil {
			return SetupBundle{}, fmt.Errorf("decoding vault export agents: %w", err)
		}
	}
	if len(payload.Shared) > 0 {
		if err := json.Unmarshal(payload.Shared, &bundle.SharedConfig); err != nil {
			return SetupBundle{}, fmt.Errorf("decoding vault export shared config: %w", err)
		}
	}
	if len(payload.ProviderConfigs) > 0 {
		if err := json.Unmarshal(payload.ProviderConfigs, &bundle.ProviderConfigs); err != nil {
			return SetupBundle{}, fmt.Errorf("decoding vault export provider configs: %w", err)
		}
	}
	if len(payload.Sessions) > 0 {
		if err := json.Unmarshal(payload.Sessions, &bundle.Sessions); err != nil {
			return SetupBundle{}, fmt.Errorf("decoding vault export sessions: %w", err)
		}
	}
	return bundle, nil
}
