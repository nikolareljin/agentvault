package cmd

import (
	"strings"
	"testing"
	"time"
)

// Export never writes a file over maxSetupAssetBytes, so import does not
// write one either: a bundle holding one was made by hand.
func TestImportDropsOversizedAssets(t *testing.T) {
	big := SetupAsset{LogicalRoot: "project_root", LogicalPath: "big.md", ContentPresent: true,
		Content: []byte(strings.Repeat("x", maxSetupAssetBytes+1))}
	ok := SetupAsset{LogicalRoot: "project_root", LogicalPath: "ok.md", ContentPresent: true, Content: []byte("ok")}
	setup := SetupBundle{ProjectFiles: []SetupAsset{big, ok}, SkillAssets: []SetupAsset{big}, ProviderFiles: []SetupAsset{ok}}
	report := dropOversizedProfileAssets(&setup)
	if len(setup.ProjectFiles) != 1 || setup.ProjectFiles[0].LogicalPath != "ok.md" || len(setup.SkillAssets) != 0 || len(setup.ProviderFiles) != 1 {
		t.Errorf("kept %+v / %+v", setup.ProjectFiles, setup.SkillAssets)
	}
	if len(report) != 2 || !strings.Contains(report[0], "big.md") {
		t.Errorf("report %q", report)
	}
}

// An instruction override that is not UTF-8 would be stored with its bytes
// replaced; it is left to the staged project file instead.
func TestMergeInstructionSourcesSkipsNonText(t *testing.T) {
	got := mergeInstructionSources(nil, []SetupAsset{
		{LogicalPath: "AGENTS.md", ContentPresent: true, Content: []byte("\xff\xfea\x00")},
		{LogicalPath: "CLAUDE.md", ContentPresent: true, Content: []byte("ok\n")},
	}, time.Now())
	if len(got) != 1 || got[0].Filename != "CLAUDE.md" {
		t.Errorf("merged %+v, want CLAUDE.md only", got)
	}
}
