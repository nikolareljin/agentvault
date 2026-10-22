package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The bundle's source machine, profile list, declined assets and install
// guide reach the terminal: no control characters from the file.
func TestImportListAndGuideStripControlCharacters(t *testing.T) {
	const evil = "x\x1b]0;t\a\x1b[2J\x9b2J\ry"
	bundle := PortableBundle{SourceMachine: evil, SourceOS: evil, Profiles: []PortableProfile{{
		Name: evil, ConfigDir: evil,
		Setup: SetupBundle{
			Declined: []DeclinedAsset{
				{Path: evil, Category: DeclinedRepoScope, Reason: evil},
				{Path: evil, Category: DeclinedByPolicy, Reason: evil},
				{Path: evil, Category: DeclinedAbsent, Reason: evil},
			},
			InstallGuide: InstallGuide{Requirements: []Requirement{{Name: evil, InstallCmd: evil}}},
		},
	}}}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	printBundleProfiles(cmd, bundle, bundleFormatUnknown)
	printInstallGuide(cmd, bundle.Profiles)
	if got := bundleOrigin(bundle); strings.ContainsAny(got, "\x1b\a\r") {
		t.Errorf("bundleOrigin() = %q", got)
	}
	if s := out.String(); strings.ContainsAny(s, "\x1b\a\r\u009b") {
		t.Errorf("output carries control characters: %q", s)
	}
}
