package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
)

// Every import refuses a file over the cap and a directory before reading
// it, so a huge file or a device cannot exhaust memory.
func TestImportCommandsRefuseOversizeAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	t.Setenv("AGENTVAULT_IMPORT_PASSWORD", e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{testAgent()}, nil)
	useConfigDir(t, dir)

	big := filepath.Join(root, "big.json")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: no disk is used for the hole.
	if err := f.Truncate(maxImportFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	notFile := filepath.Join(root, "dir.json")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatal(err)
	}

	cmds := map[string]func(string) error{
		"import":              func(p string) error { return importCmd.RunE(importCmd, []string{p}) },
		"setup import":        func(p string) error { return setupImportCmd.RunE(setupImportCmd, []string{p}) },
		"setup show":          func(p string) error { return setupShowCmd.RunE(setupShowCmd, []string{p}) },
		"agent import":        func(p string) error { return agentImportCmd.RunE(agentImportCmd, []string{p}) },
		"session import":      func(p string) error { return sessionImportCmd.RunE(sessionImportCmd, []string{p}) },
		"instructions import": func(p string) error { return instImportCmd.RunE(instImportCmd, []string{p}) },
	}
	for name, run := range cmds {
		for path, want := range map[string]string{big: "larger than", notFile: "not a regular file"} {
			_, err := captureStdoutForTest(t, func() error { return run(path) })
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s %s: err = %v, want %q", name, filepath.Base(path), err, want)
			}
		}
	}
}
