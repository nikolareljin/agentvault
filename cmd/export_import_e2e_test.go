package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

const e2eMasterPassword = "master-password"
const e2eBundlePassword = "bundle-password"

// seedProfileVault creates a config directory holding an initialized vault with
// the given agents and shared rules.
func seedProfileVault(t *testing.T, dir string, agents []agent.Agent, rules []agent.UnifiedRule) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Init(e2eMasterPassword); err != nil {
		t.Fatalf("vault.Init(%s) error = %v", dir, err)
	}
	for _, a := range agents {
		if err := v.Add(a); err != nil {
			t.Fatalf("vault.Add(%s) error = %v", a.Name, err)
		}
	}
	if len(rules) > 0 {
		if err := v.SetSharedConfig(agent.SharedConfig{Rules: rules}); err != nil {
			t.Fatalf("vault.SetSharedConfig() error = %v", err)
		}
	}
}

// withFlags sets command flags for the duration of a test and restores them.
func withFlags(t *testing.T, cmd *cobra.Command, values map[string]string) {
	t.Helper()
	previous := make(map[string]string, len(values))
	for name := range values {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("command %q has no flag %q", cmd.Name(), name)
		}
		previous[name] = flag.Value.String()
	}
	for name, value := range values {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("Set(%s=%s) error = %v", name, value, err)
		}
	}
	t.Cleanup(func() {
		for name, value := range previous {
			_ = cmd.Flags().Set(name, value)
			cmd.Flags().Lookup(name).Changed = false
		}
	})
}

// useConfigDir points the global --config flag at dir for the duration of a test.
func useConfigDir(t *testing.T, dir string) {
	t.Helper()
	previous, _ := rootCmd.PersistentFlags().GetString("config")
	if err := rootCmd.PersistentFlags().Set("config", dir); err != nil {
		t.Fatalf("Set(config) error = %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", previous)
		rootCmd.PersistentFlags().Lookup("config").Changed = false
	})
}

// TestExportAllProfilesThenMirrorImport walks the whole cross-machine flow: a
// source machine with two config profiles is exported into one encrypted
// bundle, then a third machine mirrors one of those profiles and ends up with
// exactly that profile's agents and rules.
func TestExportAllProfilesThenMirrorImport(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)

	seedProfileVault(t, filepath.Join(root, config.AppName),
		[]agent.Agent{{Name: "home-claude", Provider: agent.ProviderClaude, Model: "opus"}},
		[]agent.UnifiedRule{{Name: "no-emoji", Priority: 1}})
	seedProfileVault(t, filepath.Join(root, config.AppName+"-work"),
		[]agent.Agent{{Name: "work-codex", Provider: agent.ProviderCodex}},
		[]agent.UnifiedRule{{Name: "work-rule", Priority: 1}})

	bundlePath := filepath.Join(root, "everything"+DefaultExportExtension)
	var exportOut bytes.Buffer
	exportCmd.SetOut(&exportOut)
	exportCmd.SetErr(&exportOut)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	// Provider file and skill collection reads the real home layout, which the
	// temp HOME does not have; keep the export focused on vault-held settings.
	withFlags(t, exportCmd, map[string]string{
		"yes":                    "true",
		"include-provider-files": "false",
		"include-skills":         "false",
		"detect":                 "false",
	})

	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("runExport() error = %v\noutput: %s", err, exportOut.String())
	}

	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("ReadFile(bundle) error = %v", err)
	}
	if detectBundleFormat(raw) != bundleFormatUnknown {
		t.Fatal("bundle parsed as plaintext JSON, want an encrypted file by default")
	}
	payload, err := decryptBundlePayload(raw, e2eBundlePassword)
	if err != nil {
		t.Fatalf("decryptBundlePayload() error = %v", err)
	}
	bundle, format, err := decodePortableBundle(payload)
	if err != nil {
		t.Fatalf("decodePortableBundle() error = %v", err)
	}
	if format != bundleFormatPortable {
		t.Fatalf("format = %v, want a portable bundle", format)
	}
	if len(bundle.Profiles) != 2 {
		t.Fatalf("profiles = %v, want both config directories captured", bundle.ProfileNames())
	}
	if !strings.Contains(exportOut.String(), "Profiles: 2") {
		t.Fatalf("export summary = %q, want it to report 2 profiles", exportOut.String())
	}

	// A third machine that already has unrelated settings mirrors the work profile.
	targetDir := filepath.Join(root, "machine-c")
	seedProfileVault(t, targetDir,
		[]agent.Agent{{Name: "stale-local", Provider: agent.ProviderOllama}},
		[]agent.UnifiedRule{{Name: "stale-rule", Priority: 1}})
	useConfigDir(t, targetDir)

	var importOut bytes.Buffer
	importCmd.SetOut(&importOut)
	importCmd.SetErr(&importOut)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{
		"strategy": string(strategyMirror),
		"profile":  "work",
		"confirm":  "true",
	})

	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("runImport() error = %v\noutput: %s", err, importOut.String())
	}

	target := vault.New(filepath.Join(targetDir, config.VaultFile))
	if err := target.Unlock(e2eMasterPassword); err != nil {
		t.Fatalf("target vault Unlock() error = %v", err)
	}
	agents := target.List()
	if len(agents) != 1 || agents[0].Name != "work-codex" {
		t.Fatalf("target agents = %#v, want only work-codex after a mirror import", agents)
	}
	rules := target.SharedConfig().Rules
	if len(rules) != 1 || rules[0].Name != "work-rule" {
		t.Fatalf("target rules = %#v, want only work-rule after a mirror import", rules)
	}
}

// TestImportDryRunLeavesVaultUntouched checks that a dry run reports changes
// without writing any of them.
func TestImportDryRunLeavesVaultUntouched(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(ConfigDirsEnv, "")
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv(ExportPasswordEnv, e2eBundlePassword)
	t.Setenv(ImportPasswordEnv, e2eBundlePassword)

	sourceDir := filepath.Join(root, config.AppName)
	seedProfileVault(t, sourceDir,
		[]agent.Agent{{Name: "incoming", Provider: agent.ProviderClaude}}, nil)

	bundlePath := filepath.Join(root, "source.json")
	var exportOut bytes.Buffer
	exportCmd.SetOut(&exportOut)
	exportCmd.SetErr(&exportOut)
	t.Cleanup(func() { exportCmd.SetOut(nil); exportCmd.SetErr(nil) })
	withFlags(t, exportCmd, map[string]string{
		"yes":                    "true",
		"plain":                  "true",
		"confirm":                "true",
		"include-provider-files": "false",
		"include-skills":         "false",
		"detect":                 "false",
	})
	if err := runExport(exportCmd, []string{bundlePath}); err != nil {
		t.Fatalf("runExport() error = %v\noutput: %s", err, exportOut.String())
	}
	if detectBundleFormat(mustRead(t, bundlePath)) != bundleFormatPortable {
		t.Fatal("--plain export is not readable JSON")
	}

	targetDir := filepath.Join(root, "machine-b")
	seedProfileVault(t, targetDir, []agent.Agent{{Name: "local-only", Provider: agent.ProviderOllama}}, nil)
	useConfigDir(t, targetDir)

	var importOut bytes.Buffer
	importCmd.SetOut(&importOut)
	importCmd.SetErr(&importOut)
	t.Cleanup(func() { importCmd.SetOut(nil); importCmd.SetErr(nil) })
	withFlags(t, importCmd, map[string]string{
		"strategy": string(strategyMirror),
		"dry-run":  "true",
	})
	if err := runImport(importCmd, []string{bundlePath}); err != nil {
		t.Fatalf("runImport() error = %v\noutput: %s", err, importOut.String())
	}

	if !strings.Contains(importOut.String(), "Dry run") {
		t.Fatalf("import output = %q, want it to announce the dry run", importOut.String())
	}
	target := vault.New(filepath.Join(targetDir, config.VaultFile))
	if err := target.Unlock(e2eMasterPassword); err != nil {
		t.Fatalf("target vault Unlock() error = %v", err)
	}
	agents := target.List()
	if len(agents) != 1 || agents[0].Name != "local-only" {
		t.Fatalf("target agents = %#v, want the vault untouched by a dry run", agents)
	}
}

// TestImportMirrorRefusesWithoutConfirmation guards the destructive path.
func TestImportMirrorRefusesWithoutConfirmation(t *testing.T) {
	err := confirmDestructiveImport(false, false, os.Stdin, &bytes.Buffer{}, []PortableProfile{{Name: "work"}})
	if err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("confirmDestructiveImport() error = %v, want it to demand --confirm when not on a terminal", err)
	}
	if err := confirmDestructiveImport(true, false, os.Stdin, &bytes.Buffer{}, nil); err != nil {
		t.Fatalf("confirmDestructiveImport(confirm) error = %v, want nil", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return data
}
