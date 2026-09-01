package cmd

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestParseProfileAnswer(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"all":            nil,
		"ALL":            nil,
		"work":           {"work"},
		"work, default ": {"work", "default"},
		"work,,default":  {"work", "default"},
	}
	for in, want := range cases {
		got := parseProfileAnswer(in)
		if len(got) != len(want) {
			t.Fatalf("parseProfileAnswer(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parseProfileAnswer(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

func TestAskLineUsesDefaultOnBlankInput(t *testing.T) {
	var out bytes.Buffer
	reader := bufio.NewReader(strings.NewReader("\n"))
	got, err := askLine(&out, reader, "Output file", "/tmp/default.avbundle")
	if err != nil {
		t.Fatalf("askLine() error = %v", err)
	}
	if got != "/tmp/default.avbundle" {
		t.Fatalf("askLine() = %q, want the default", got)
	}
	if !strings.Contains(out.String(), "[/tmp/default.avbundle]") {
		t.Fatalf("prompt = %q, want the default shown in brackets", out.String())
	}
}

func TestAskLineUsesDefaultOnEOF(t *testing.T) {
	var out bytes.Buffer
	reader := bufio.NewReader(strings.NewReader(""))
	got, err := askLine(&out, reader, "Output file", "fallback")
	if err != nil {
		t.Fatalf("askLine() error = %v", err)
	}
	if got != "fallback" {
		t.Fatalf("askLine() = %q, want the default on EOF", got)
	}
}

func TestAskBoolAnswers(t *testing.T) {
	cases := []struct {
		input string
		def   bool
		want  bool
	}{
		{"\n", true, true},
		{"\n", false, false},
		{"y\n", false, true},
		{"YES\n", false, true},
		{"n\n", true, false},
		{"no\n", true, false},
		{"maybe\ny\n", false, true},
		{"", true, true},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		got, err := askBool(&out, bufio.NewReader(strings.NewReader(tc.input)), "Include keys", tc.def)
		if err != nil {
			t.Fatalf("askBool(%q) error = %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("askBool(%q, default %v) = %v, want %v", tc.input, tc.def, got, tc.want)
		}
	}
}

func TestExpandUserPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	if got := expandUserPath("~/exports/a.avbundle"); got != home+"/exports/a.avbundle" {
		t.Fatalf("expandUserPath() = %q, want the home directory expanded", got)
	}
	if got := expandUserPath("/absolute/path"); got != "/absolute/path" {
		t.Fatalf("expandUserPath() = %q, want the path unchanged", got)
	}
	if got := expandUserPath("  "); got != "" {
		t.Fatalf("expandUserPath() = %q, want an empty string", got)
	}
}

func TestDefaultExportOptionsExportEverything(t *testing.T) {
	opts := defaultExportOptions()
	if !opts.IncludeKeys || !opts.IncludeSecrets || !opts.IncludeSessions ||
		!opts.IncludeTemplates || !opts.IncludeProviderFile || !opts.IncludeSkills || !opts.Encrypt {
		t.Fatalf("defaultExportOptions() = %#v, want every setting included and encryption on", opts)
	}
}

func TestSelectProfilesForExport(t *testing.T) {
	discovered := []discoveredProfile{
		{Name: "default", ConfigDir: "/a"},
		{Name: "work", ConfigDir: "/b"},
	}

	all, err := selectProfilesForExport(discovered, nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("selectProfilesForExport(nil) = %v, %v; want both profiles", all, err)
	}

	one, err := selectProfilesForExport(discovered, []string{"WORK"})
	if err != nil || len(one) != 1 || one[0].Name != "work" {
		t.Fatalf("selectProfilesForExport([WORK]) = %v, %v; want the work profile", one, err)
	}

	every, err := selectProfilesForExport(discovered, []string{"all"})
	if err != nil || len(every) != 2 {
		t.Fatalf("selectProfilesForExport([all]) = %v, %v; want both profiles", every, err)
	}

	if _, err := selectProfilesForExport(discovered, []string{"missing"}); err == nil {
		t.Fatal("selectProfilesForExport([missing]) error = nil, want an error naming the available profiles")
	}
}

func TestExportCommandDefaultsMatchExportEverything(t *testing.T) {
	cmd := &cobra.Command{Use: "export"}
	cmd.Flags().AddFlagSet(exportCmd.Flags())

	opts := defaultExportOptions()
	applyExportFlags(cmd, &opts)
	if !opts.IncludeKeys || !opts.IncludeSecrets || !opts.Encrypt || opts.IncludeStatus {
		t.Fatalf("applyExportFlags() with untouched flags = %#v, want the full-export defaults", opts)
	}
}

func TestExportPlainFlagDisablesEncryption(t *testing.T) {
	cmd := &cobra.Command{Use: "export"}
	cmd.Flags().AddFlagSet(exportCmd.Flags())
	if err := cmd.Flags().Set("plain", "true"); err != nil {
		t.Fatalf("Set(plain) error = %v", err)
	}
	t.Cleanup(func() { _ = cmd.Flags().Set("plain", "false") })

	opts := defaultExportOptions()
	applyExportFlags(cmd, &opts)
	if opts.Encrypt {
		t.Fatal("applyExportFlags() left encryption on, want --plain to turn it off")
	}
}
