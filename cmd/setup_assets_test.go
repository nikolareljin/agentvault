package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectSetupAssets_ProjectDiscoveryAndInstructionOverrides(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	mustMkdirAll(t, filepath.Join(projectDir, ".github"))
	mustWriteFile(t, filepath.Join(projectDir, "AGENTS.md"), "agents\n")
	mustWriteFile(t, filepath.Join(projectDir, ".github", "copilot-instructions.md"), "copilot\n")
	mustWriteFile(t, filepath.Join(projectDir, "implement_issue.txt"), "issue workflow\n")

	assets, warnings, err := collectSetupAssets(setupAssetOptions{
		ProjectDir:     projectDir,
		IncludeSecrets: false,
	})
	if err != nil {
		t.Fatalf("collectSetupAssets() error = %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("collectSetupAssets() warnings = none, want aggregated optional asset warnings")
	}
	for _, warningText := range warnings {
		if strings.Contains(warningText, "AIDER") || strings.Contains(warningText, "NANOCLAW") {
			t.Fatalf("collectSetupAssets() warning = %q, want aggregated message rather than per-file noise", warningText)
		}
	}

	if !hasAsset(assets.ProjectFiles, setupAssetKindProjectFile, setupAssetRootProject, "AGENTS.md") {
		t.Fatalf("project files missing AGENTS.md: %#v", assets.ProjectFiles)
	}
	if !hasAsset(assets.ProjectFiles, setupAssetKindProjectFile, setupAssetRootProject, ".github/copilot-instructions.md") {
		t.Fatalf("project files missing copilot instructions")
	}
	if !hasAsset(assets.ProjectFiles, setupAssetKindProjectFile, setupAssetRootProject, "implement_issue.txt") {
		t.Fatalf("project files missing implement_issue.txt")
	}
	if !hasAsset(assets.InstructionOverrides, setupAssetKindInstruction, setupAssetRootProject, "AGENTS.md") {
		t.Fatalf("instruction overrides missing AGENTS.md")
	}
	agentsOverride := findAssetByKind(assets.InstructionOverrides, setupAssetKindInstruction, setupAssetRootProject, "AGENTS.md")
	if agentsOverride == nil || !agentsOverride.ContentPresent || agentsOverride.SizeBytes == 0 || string(agentsOverride.Content) != "agents\n" {
		t.Fatalf("instruction override metadata = %#v, want content metadata copied from project asset", agentsOverride)
	}
}

func TestCollectSetupAssets_ProviderFilesRedactSensitiveContentByDefault(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	mustMkdirAll(t, filepath.Join(homeDir, ".claude"))
	mustMkdirAll(t, filepath.Join(homeDir, ".codex", "rules"))
	mustWriteFile(t, filepath.Join(homeDir, ".claude", "settings.json"), `{"apiKey":"secret"}`)
	mustWriteFile(t, filepath.Join(homeDir, ".claude", "keybindings.json"), `{"up":"k"}`)
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "config.toml"), `model = "gpt"`)
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "rules", "review.md"), "review rules")

	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatalf("collectSetupAssets() error = %v", err)
	}

	settings := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "settings.json")
	if settings == nil {
		t.Fatalf("provider files missing claude settings")
	}
	if !settings.Sensitive || !settings.Redacted || len(settings.Content) != 0 || settings.SHA256 == "" {
		t.Fatalf("claude settings asset = %#v, want sensitive redacted metadata", settings)
	}
	if settings.ContentPresent {
		t.Fatalf("redacted settings asset should not mark content as present: %#v", settings)
	}

	keybindings := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "keybindings.json")
	if keybindings == nil || keybindings.Redacted || string(keybindings.Content) != `{"up":"k"}` {
		t.Fatalf("claude keybindings asset = %#v, want non-redacted content", keybindings)
	}

	rule := findAsset(assets.ProviderFiles, setupAssetRootProviderCodex, "rules/review.md")
	if rule == nil || string(rule.Content) != "review rules" {
		t.Fatalf("codex rule asset = %#v, want content", rule)
	}
}

func TestCollectSetupAssets_IncludeSecretsAndSkills(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	mustMkdirAll(t, filepath.Join(homeDir, ".codex", "skills", "reviewer"))
	mustMkdirAll(t, filepath.Join(projectDir, "skills", "custom"))
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "config.toml"), `token = "secret"`)
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "skills", "reviewer", "SKILL.md"), "home skill")
	mustWriteFile(t, filepath.Join(projectDir, "skills", "custom", "SKILL.md"), "project skill")

	assets, _, err := collectSetupAssets(setupAssetOptions{
		ProjectDir:     projectDir,
		IncludeSecrets: true,
	})
	if err != nil {
		t.Fatalf("collectSetupAssets() error = %v", err)
	}

	configAsset := findAsset(assets.ProviderFiles, setupAssetRootProviderCodex, "config.toml")
	if configAsset == nil || configAsset.Redacted || string(configAsset.Content) != `token = "secret"` {
		t.Fatalf("codex config asset = %#v, want full content", configAsset)
	}
	if !hasAsset(assets.SkillAssets, setupAssetKindSkill, setupAssetRootProviderCodexSkill, "reviewer/SKILL.md") {
		t.Fatalf("skill assets missing home codex skill")
	}
	if !hasAsset(assets.SkillAssets, setupAssetKindSkill, setupAssetRootProject, "skills/custom/SKILL.md") {
		t.Fatalf("skill assets missing project skill")
	}
	projectSkill := findAsset(assets.SkillAssets, setupAssetRootProject, "skills/custom/SKILL.md")
	if projectSkill == nil || !filepath.IsAbs(projectSkill.SourcePath) {
		t.Fatalf("project skill asset = %#v, want absolute source path", projectSkill)
	}
}

func TestStageImportedAssetsAndApplyStagedProjectAssets(t *testing.T) {
	configDir := t.TempDir()
	targetDir := t.TempDir()
	assets := []SetupAsset{
		{
			Kind:                setupAssetKindProjectFile,
			LogicalRoot:         setupAssetRootProject,
			LogicalPath:         "docs/README.md",
			ProjectRelativePath: "docs/README.md",
			SourcePath:          "/tmp/source/docs/README.md",
			ContentPresent:      true,
			Content:             []byte("doc body"),
		},
		{
			Kind:                setupAssetKindSkill,
			LogicalRoot:         setupAssetRootProject,
			LogicalPath:         "skills/review/SKILL.md",
			ProjectRelativePath: "skills/review/SKILL.md",
			SourcePath:          "/tmp/source/skills/review/SKILL.md",
			ContentPresent:      true,
			Content:             []byte("skill body"),
		},
	}

	staged, warnings, err := stageImportedAssets(configDir, assets)
	if err != nil {
		t.Fatalf("stageImportedAssets() error = %v", err)
	}
	if staged != 2 || len(warnings) != 0 {
		t.Fatalf("stageImportedAssets() = (%d, %v), want (2, none)", staged, warnings)
	}

	applied, err := applyStagedProjectAssets(configDir, targetDir)
	if err != nil {
		t.Fatalf("applyStagedProjectAssets() error = %v", err)
	}
	if applied != 2 {
		t.Fatalf("applyStagedProjectAssets() = %d, want 2", applied)
	}
	if data, err := os.ReadFile(filepath.Join(targetDir, "docs", "README.md")); err != nil || string(data) != "doc body" {
		t.Fatalf("staged project file = %q, %v", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(targetDir, "skills", "review", "SKILL.md")); err != nil || string(data) != "skill body" {
		t.Fatalf("staged project skill = %q, %v", string(data), err)
	}
}

func TestApplyProviderAssetsToSystem(t *testing.T) {
	homeDir := t.TempDir()
	assets := []SetupAsset{
		{
			Kind:           setupAssetKindProviderFile,
			LogicalRoot:    setupAssetRootProviderClaude,
			LogicalPath:    "settings.json",
			SourcePath:     "/tmp/source/.claude/settings.json",
			ContentPresent: true,
			Content:        []byte(`{"theme":"dark"}`),
		},
		{
			Kind:           setupAssetKindSkill,
			LogicalRoot:    setupAssetRootProviderCodexSkill,
			LogicalPath:    "review/SKILL.md",
			SourcePath:     "/tmp/source/.codex/skills/review/SKILL.md",
			ContentPresent: true,
			Content:        []byte("skill"),
		},
	}

	applied, warnings, err := applyProviderAssetsToSystem(homeDir, assets)
	if err != nil {
		t.Fatalf("applyProviderAssetsToSystem() error = %v", err)
	}
	if applied != 2 || len(warnings) != 0 {
		t.Fatalf("applyProviderAssetsToSystem() = (%d, %v), want (2, none)", applied, warnings)
	}
	if data, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json")); err != nil || string(data) != `{"theme":"dark"}` {
		t.Fatalf("claude settings = %q, %v", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(homeDir, ".codex", "skills", "review", "SKILL.md")); err != nil || string(data) != "skill" {
		t.Fatalf("codex skill = %q, %v", string(data), err)
	}
}

func TestLoadSetupAsset_MissingOptionalIncludesMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	asset, warningText, err := loadSetupAsset(path, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, "docs/missing.txt", "docs/missing.txt", false, false, true)
	if err != nil {
		t.Fatalf("loadSetupAsset() error = %v", err)
	}
	if warningText == "" || !asset.Missing || asset.LogicalPath != "docs/missing.txt" {
		t.Fatalf("loadSetupAsset() = (%#v, %q), want missing metadata entry", asset, warningText)
	}
	if !strings.Contains(warningText, "root=project_root") || !strings.Contains(warningText, "path=docs/missing.txt") {
		t.Fatalf("loadSetupAsset() warning = %q, want root/path context", warningText)
	}
}

func TestLoadSetupAsset_EmptyFileContentMarkedPresent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "empty.txt")
	mustWriteFile(t, file, "")
	asset, warningText, err := loadSetupAsset(file, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, "empty.txt", "empty.txt", false, false, false)
	if err != nil {
		t.Fatalf("loadSetupAsset() error = %v", err)
	}
	if warningText != "" || !asset.ContentPresent || len(asset.Content) != 0 {
		t.Fatalf("loadSetupAsset() = (%#v, %q), want empty file with present content", asset, warningText)
	}
}

func TestLoadSetupAsset_OversizedReturnsMetadataOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.bin")
	mustWriteFileBytes(t, file, bytes.Repeat([]byte("a"), maxSetupAssetBytes+1))
	asset, warningText, err := loadSetupAsset(file, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, "large.bin", "large.bin", false, false, false)
	if err != nil {
		t.Fatalf("loadSetupAsset() error = %v", err)
	}
	if warningText == "" || asset.ContentPresent || len(asset.Content) != 0 || asset.SizeBytes <= maxSetupAssetBytes {
		t.Fatalf("loadSetupAsset() = (%#v, %q), want metadata-only oversized entry", asset, warningText)
	}
}

func TestCollectDirFiles_SkipsSymlinkAssets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	mustWriteFile(t, target, "secret")
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	assets, warnings, err := collectDirFiles(root, setupAssetKindSkill, setupAssetOriginProjectLocal, setupAssetRootProject, "", "", false, false)
	if err != nil {
		t.Fatalf("collectDirFiles() error = %v", err)
	}
	if hasAsset(assets, setupAssetKindSkill, setupAssetRootProject, "linked.txt") {
		t.Fatalf("collectDirFiles() assets = %#v, want symlink skipped", assets)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "symlink") {
		t.Fatalf("collectDirFiles() warnings = %v, want symlink warning", warnings)
	}
}

func TestLoadSetupAsset_RejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	mustWriteFile(t, target, "secret")
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, _, err := loadSetupAsset(link, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, "linked.txt", "linked.txt", false, false, false)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("loadSetupAsset() err = %v, want symlink rejection", err)
	}
}

func TestCollectDirFiles_SkipsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	mustMkdirAll(t, targetDir)
	link := filepath.Join(root, "linked-root")
	if err := os.Symlink(targetDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	assets, warnings, err := collectDirFiles(link, setupAssetKindSkill, setupAssetOriginProjectLocal, setupAssetRootProject, "", "", false, false)
	if err != nil {
		t.Fatalf("collectDirFiles() error = %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("collectDirFiles() assets = %#v, want symlink root skipped", assets)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "symlink asset root") {
		t.Fatalf("collectDirFiles() warnings = %v, want symlink root warning", warnings)
	}
}

func TestStageImportedAssetsRejectsUnsafePath(t *testing.T) {
	_, _, err := stageImportedAssets(t.TempDir(), []SetupAsset{{
		Kind:           setupAssetKindProjectFile,
		LogicalRoot:    setupAssetRootProject,
		LogicalPath:    "../evil.txt",
		ContentPresent: true,
		Content:        []byte("x"),
	}})
	if err == nil || !strings.Contains(err.Error(), "unsafe") && !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("stageImportedAssets() err = %v, want unsafe path error", err)
	}
}

func TestStageImportedAssetsRejectsUnsafeProviderRoot(t *testing.T) {
	_, _, err := stageImportedAssets(t.TempDir(), []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    "../../.ssh",
		LogicalPath:    "config",
		ContentPresent: true,
		Content:        []byte("x"),
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider asset root") {
		t.Fatalf("stageImportedAssets() err = %v, want unsupported provider root error", err)
	}
}

func TestApplyProviderAssetsToSystemRejectsUnsafePath(t *testing.T) {
	applied, warnings, err := applyProviderAssetsToSystem(t.TempDir(), []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    setupAssetRootProviderClaude,
		LogicalPath:    "../evil",
		ContentPresent: true,
		Content:        []byte("x"),
	}})
	if err != nil {
		t.Fatalf("applyProviderAssetsToSystem() error = %v, want warning-only skip", err)
	}
	if applied != 0 || len(warnings) == 0 {
		t.Fatalf("applyProviderAssetsToSystem() = (%d, %v), want warning-only skip", applied, warnings)
	}
}

func hasAsset(items []SetupAsset, kind string, logicalRoot string, logicalPath string) bool {
	return findAssetByKind(items, kind, logicalRoot, logicalPath) != nil
}

func findAsset(items []SetupAsset, logicalRoot string, logicalPath string) *SetupAsset {
	return findAssetByKind(items, "", logicalRoot, logicalPath)
}

func findAssetByKind(items []SetupAsset, kind string, logicalRoot string, logicalPath string) *SetupAsset {
	for i := range items {
		if kind != "" && items[i].Kind != kind {
			continue
		}
		if items[i].LogicalRoot == logicalRoot && items[i].LogicalPath == logicalPath {
			return &items[i]
		}
	}
	return nil
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustWriteFileBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}
