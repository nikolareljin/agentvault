package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Templates travel in bundles, so templates show and list must not print
// their escape sequences, and --raw must give the content byte for byte.
func TestTemplatesOutputHasNoControlCharacters(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	content := "a\x1b]0;t\x07\x1b[2Jb\r\n\tc\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "templates", "evil.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "templates", "metadata.json"), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_pr": "evil.txt"},
  "versions": {"implement_pr": "v\u001b[31m"}
}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("config", "") })
	if err := rootCmd.PersistentFlags().Set("config", cfgDir); err != nil {
		t.Fatal(err)
	}
	run := func(raw bool, f func() error) string {
		buf := new(bytes.Buffer)
		for _, c := range []*cobra.Command{templatesShowCmd, templatesListCmd} {
			c.SetOut(buf)
			c.SetErr(buf)
			_ = c.Flags().Set("repo", t.TempDir())
		}
		_ = templatesShowCmd.Flags().Set("metadata", "true")
		if raw {
			_ = templatesShowCmd.Flags().Set("raw", "true")
		}
		t.Cleanup(func() {
			for _, c := range []*cobra.Command{templatesShowCmd, templatesListCmd} {
				c.SetOut(nil)
				c.SetErr(nil)
				_ = c.Flags().Set("repo", "")
			}
			_ = templatesShowCmd.Flags().Set("metadata", "false")
			_ = templatesShowCmd.Flags().Set("raw", "false")
		})
		if err := f(); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	for name, out := range map[string]string{
		"show": run(false, func() error { return runTemplatesShow(templatesShowCmd, []string{"evil.txt"}) }),
		"list": run(false, func() error { return runTemplatesList(templatesListCmd, nil) }),
	} {
		if strings.ContainsAny(out, "\x1b\x07") {
			t.Errorf("templates %s printed a control character: %q", name, out)
		}
	}
	if out := run(true, func() error { return runTemplatesShow(templatesShowCmd, []string{"evil.txt"}) }); !strings.HasSuffix(out, content) {
		t.Errorf("templates show --raw = %q, want it to end with %q", out, content)
	}
}
