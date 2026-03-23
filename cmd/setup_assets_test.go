package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
)

func setTestHomeDir(t *testing.T, homeDir string) {
	t.Helper()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(homeDir)
		pathPart := strings.TrimPrefix(homeDir, volume)
		if pathPart == "" {
			pathPart = string(filepath.Separator)
		}
		t.Setenv("HOMEDRIVE", volume)
		t.Setenv("HOMEPATH", pathPart)
	}
}

func TestCollectSetupAssets_ProjectDiscoveryAndInstructionOverrides(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	setTestHomeDir(t, homeDir)

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
	setTestHomeDir(t, homeDir)

	mustMkdirAll(t, filepath.Join(homeDir, ".claude"))
	mustMkdirAll(t, filepath.Join(homeDir, ".codex", "rules"))
	mustMkdirAll(t, filepath.Join(homeDir, ".config", "github-copilot"))
	mustWriteFile(t, filepath.Join(homeDir, ".claude", "settings.json"), `{"apiKey":"secret"}`)
	mustWriteFile(t, filepath.Join(homeDir, ".claude", "keybindings.json"), `{"up":"k"}`)
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "config.toml"), `model = "gpt"`)
	mustWriteFile(t, filepath.Join(homeDir, ".codex", "rules", "review.md"), "review rules")
	mustWriteFile(t, filepath.Join(homeDir, ".config", "github-copilot", "hosts.json"), `{"github.com":{"oauth_token":"secret"}}`)

	assets, _, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatalf("collectSetupAssets() error = %v", err)
	}

	settings := findAsset(assets.ProviderFiles, setupAssetRootProviderClaude, "settings.json")
	if settings == nil {
		t.Fatalf("provider files missing claude settings")
	}
	if !settings.Sensitive || !settings.Redacted || len(settings.Content) != 0 || settings.SHA256 != "" {
		t.Fatalf("claude settings asset = %#v, want sensitive redacted metadata", settings)
	}

	copilotHosts := findAsset(assets.ProviderFiles, setupAssetRootProviderCopilot, "hosts.json")
	if copilotHosts == nil {
		t.Fatalf("provider files missing copilot config")
	}
	if !copilotHosts.Sensitive || !copilotHosts.Redacted || len(copilotHosts.Content) != 0 || copilotHosts.SHA256 != "" {
		t.Fatalf("copilot config asset = %#v, want sensitive redacted metadata", copilotHosts)
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

func TestCollectSetupAssets_SkipsMissingOptionalProviderFilesFromManifest(t *testing.T) {
	homeDir := t.TempDir()
	setTestHomeDir(t, homeDir)

	mustMkdirAll(t, filepath.Join(homeDir, ".claude"))

	assets, warnings, err := collectSetupAssets(setupAssetOptions{})
	if err != nil {
		t.Fatalf("collectSetupAssets() error = %v", err)
	}

	for _, asset := range assets.ProviderFiles {
		if asset.Missing {
			t.Fatalf("collectSetupAssets() provider file manifest contains missing entry: %#v", asset)
		}
	}

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "optional asset missing") {
		t.Fatalf("collectSetupAssets() warnings = %v, want provider missing-asset warning", warnings)
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
	if configAsset == nil || configAsset.Redacted || string(configAsset.Content) != `token = "secret"` || configAsset.SHA256 == "" {
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

func TestCollectDirFiles_SurfacesLoadSetupAssetWarnings(t *testing.T) {
	root := t.TempDir()
	mustWriteFileBytes(t, filepath.Join(root, "oversized.txt"), bytes.Repeat([]byte("a"), maxSetupAssetBytes+1))

	assets, warnings, err := collectDirFiles(root, setupAssetKindSkill, setupAssetOriginProjectLocal, setupAssetRootProject, "", "", false, false)
	if err != nil {
		t.Fatalf("collectDirFiles() error = %v", err)
	}
	if !hasAsset(assets, setupAssetKindSkill, setupAssetRootProject, "oversized.txt") {
		t.Fatalf("collectDirFiles() assets = %#v, want oversized asset metadata entry", assets)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "oversized.txt") || !strings.Contains(joined, "metadata only") {
		t.Fatalf("collectDirFiles() warnings = %v, want oversized warning surfaced", warnings)
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

func TestStageImportedAssetsWarnsOnUnsupportedProviderRoot(t *testing.T) {
	staged, warnings, err := stageImportedAssets(t.TempDir(), []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    "../../.ssh",
		LogicalPath:    "config",
		ContentPresent: true,
		Content:        []byte("x"),
	}})
	if err != nil {
		t.Fatalf("stageImportedAssets() error = %v, want warning-only skip", err)
	}
	joined := strings.Join(warnings, "\n")
	if staged != 0 || !strings.Contains(joined, "unsupported provider asset root") {
		t.Fatalf("stageImportedAssets() = (%d, %v), want unsupported-root warning", staged, warnings)
	}
	stagePath, err := stagedAssetPath(t.TempDir(), SetupAsset{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    "../../.ssh",
		LogicalPath:    "config",
		ContentPresent: true,
		Content:        []byte("x"),
	})
	if stagePath != "" || !errors.Is(err, errUnsupportedProviderAssetRoot) {
		t.Fatalf("stagedAssetPath() = (%q, %v), want unsupported-provider-root sentinel", stagePath, err)
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

func TestCollectProjectAssets_DoesNotCountOversizedWarningsAsMissing(t *testing.T) {
	baselineDir := t.TempDir()
	_, _, baselineWarnings, err := collectProjectAssets(baselineDir, false)
	if err != nil {
		t.Fatalf("collectProjectAssets() baseline error = %v", err)
	}

	projectDir := t.TempDir()
	mustWriteFileBytes(t, filepath.Join(projectDir, "AGENTS.md"), bytes.Repeat([]byte("a"), maxSetupAssetBytes+1))

	_, _, warnings, err := collectProjectAssets(projectDir, false)
	if err != nil {
		t.Fatalf("collectProjectAssets() error = %v", err)
	}
	if len(baselineWarnings) == 0 || len(warnings) == 0 {
		t.Fatalf("collectProjectAssets() warnings = %v baseline = %v, want missing-file warnings", warnings, baselineWarnings)
	}
	baselineFields := strings.Fields(baselineWarnings[0])
	fields := strings.Fields(warnings[0])
	if len(baselineFields) < 4 || len(fields) < 4 {
		t.Fatalf("collectProjectAssets() warnings = %v baseline = %v, want parseable missing-file warnings", warnings, baselineWarnings)
	}
	baselineMissing, err := strconv.Atoi(baselineFields[3])
	if err != nil {
		t.Fatalf("collectProjectAssets() baseline warning = %q, want numeric missing count: %v", baselineWarnings[0], err)
	}
	missing, err := strconv.Atoi(fields[3])
	if err != nil {
		t.Fatalf("collectProjectAssets() warning = %q, want numeric missing count: %v", warnings[0], err)
	}
	if baselineMissing-missing != 1 {
		t.Fatalf("collectProjectAssets() warnings = %v baseline = %v, want oversized instruction file to reduce the missing count by one", warnings, baselineWarnings)
	}
}

func TestApplyProviderAssetsToSystemWarnsOnUnsafePath(t *testing.T) {
	applied, warnings, err := applyProviderAssetsToSystem(t.TempDir(), []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    setupAssetRootProviderClaude,
		LogicalPath:    "../evil",
		SourcePath:     "/tmp/source/.claude/evil",
		ContentPresent: true,
		Content:        []byte("x"),
	}})
	if err != nil {
		t.Fatalf("applyProviderAssetsToSystem() error = %v, want warning-only skip", err)
	}
	joined := strings.Join(warnings, "\n")
	if applied != 0 || len(warnings) == 0 || !strings.Contains(joined, "path traversal") {
		t.Fatalf("applyProviderAssetsToSystem() = (%d, %v), want path-traversal warning", applied, warnings)
	}
	if strings.Contains(joined, "unsupported") {
		t.Fatalf("applyProviderAssetsToSystem() warnings = %v, want real error instead of unsupported-root warning", warnings)
	}
}

func TestCollectProjectAssets_SurfacesOversizedInstructionWarnings(t *testing.T) {
	projectDir := t.TempDir()
	mustWriteFileBytes(t, filepath.Join(projectDir, "AGENTS.md"), bytes.Repeat([]byte("a"), maxSetupAssetBytes+1))

	_, _, warnings, err := collectProjectAssets(projectDir, false)
	if err != nil {
		t.Fatalf("collectProjectAssets() error = %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "AGENTS.md") || !strings.Contains(joined, "metadata only") {
		t.Fatalf("collectProjectAssets() warnings = %v, want oversized instruction warning", warnings)
	}
}

func TestCollectProjectAssets_SkipsMissingOptionalFilesFromManifests(t *testing.T) {
	projectDir := t.TempDir()

	projectFiles, instructionOverrides, warnings, err := collectProjectAssets(projectDir, false)
	if err != nil {
		t.Fatalf("collectProjectAssets() error = %v", err)
	}

	if len(projectFiles) != 0 {
		t.Fatalf("collectProjectAssets() projectFiles = %v, want no missing optional files in manifest", projectFiles)
	}
	if len(instructionOverrides) != 0 {
		t.Fatalf("collectProjectAssets() instructionOverrides = %v, want no missing optional files in manifest", instructionOverrides)
	}

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "missing optional instruction file(s)") {
		t.Fatalf("collectProjectAssets() warnings = %v, want aggregated missing instruction warning", warnings)
	}
	if !strings.Contains(joined, "missing optional workflow template file(s)") {
		t.Fatalf("collectProjectAssets() warnings = %v, want aggregated missing workflow warning", warnings)
	}
}

func TestCollectProjectAssets_SurfacesOversizedWorkflowWarnings(t *testing.T) {
	projectDir := t.TempDir()
	keys := workflowtemplates.SupportedKeys()
	if len(keys) == 0 {
		t.Fatal("workflowtemplates.SupportedKeys() = none, want at least one template")
	}
	filename, ok := workflowtemplates.FindTemplateFilename(keys[0])
	if !ok {
		t.Fatalf("FindTemplateFilename(%q) = false, want filename", keys[0])
	}
	mustWriteFileBytes(t, filepath.Join(projectDir, filename), bytes.Repeat([]byte("a"), maxSetupAssetBytes+1))

	_, _, warnings, err := collectProjectAssets(projectDir, false)
	if err != nil {
		t.Fatalf("collectProjectAssets() error = %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, filepath.ToSlash(filename)) || !strings.Contains(joined, "metadata only") {
		t.Fatalf("collectProjectAssets() warnings = %v, want oversized workflow warning", warnings)
	}
}

func TestApplyProviderAssetsToSystemRejectsSymlinkProviderDirectory(t *testing.T) {
	homeDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(homeDir, ".config"))
	outsideDir := t.TempDir()
	link := filepath.Join(homeDir, ".config", "github-copilot")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	applied, warnings, err := applyProviderAssetsToSystem(homeDir, []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    setupAssetRootProviderCopilot,
		LogicalPath:    "hosts.json",
		ContentPresent: true,
		Content:        []byte(`{"github.com":{"oauth_token":"secret"}}`),
	}})
	if err != nil {
		t.Fatalf("applyProviderAssetsToSystem() error = %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if applied != 0 || !strings.Contains(joined, "destination directory contains a symlink") {
		t.Fatalf("applyProviderAssetsToSystem() = (%d, %v), want provider-dir symlink warning", applied, warnings)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "hosts.json")); !os.IsNotExist(err) {
		t.Fatalf("copilot provider symlink target should remain untouched, got err=%v", err)
	}
}

func TestStageImportedAssetsWarningsUseLogicalPaths(t *testing.T) {
	_, warnings, err := stageImportedAssets(t.TempDir(), []SetupAsset{{
		Kind:                setupAssetKindProjectFile,
		LogicalRoot:         setupAssetRootProject,
		LogicalPath:         "docs/README.md",
		ProjectRelativePath: "docs/README.md",
		SourcePath:          "/tmp/exporter-machine/docs/README.md",
		ContentPresent:      false,
	}})
	if err != nil {
		t.Fatalf("stageImportedAssets() error = %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "root=project_root") || !strings.Contains(joined, "path=docs/README.md") {
		t.Fatalf("stageImportedAssets() warnings = %v, want logical asset identifier", warnings)
	}
	if strings.Contains(joined, "/tmp/exporter-machine/") {
		t.Fatalf("stageImportedAssets() warnings = %v, want source path omitted", warnings)
	}
}

func TestApplyProviderAssetsToSystemRejectsSymlinkDestination(t *testing.T) {
	homeDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(homeDir, ".claude"))
	target := filepath.Join(homeDir, "target.txt")
	mustWriteFile(t, target, "target")
	link := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	applied, warnings, err := applyProviderAssetsToSystem(homeDir, []SetupAsset{{
		Kind:           setupAssetKindProviderFile,
		LogicalRoot:    setupAssetRootProviderClaude,
		LogicalPath:    "settings.json",
		ContentPresent: true,
		Content:        []byte(`{"theme":"dark"}`),
	}})
	if err != nil {
		t.Fatalf("applyProviderAssetsToSystem() error = %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if applied != 0 || !strings.Contains(joined, "destination path is a symlink") {
		t.Fatalf("applyProviderAssetsToSystem() = (%d, %v), want symlink warning", applied, warnings)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "target" {
		t.Fatalf("symlink target = %q, %v, want untouched target", string(data), err)
	}
}

func TestWriteRegularFileAtomicOverwritesExistingFile(t *testing.T) {
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "settings.json")
	mustWriteFile(t, destPath, `old`)

	if err := writeRegularFileAtomic(destPath, []byte(`new`), 0600); err != nil {
		t.Fatalf("writeRegularFileAtomic() error = %v", err)
	}
	if data, err := os.ReadFile(destPath); err != nil || string(data) != "new" {
		t.Fatalf("destination = %q, %v, want overwritten file", string(data), err)
	}
}

func TestReplaceRegularFileDoesNotRemoveDestinationOnNonExistRenameError(t *testing.T) {
	origRename := renameFile
	origRemove := removeFile
	t.Cleanup(func() {
		renameFile = origRename
		removeFile = origRemove
	})

	renameCalls := 0
	removeCalls := 0
	renameFile = func(_ string, _ string) error {
		renameCalls++
		return os.ErrPermission
	}
	removeFile = func(_ string) error {
		removeCalls++
		return nil
	}

	err := replaceRegularFile("/tmp/source", "/tmp/dest")
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("replaceRegularFile() err = %v, want permission error", err)
	}
	if renameCalls != 1 {
		t.Fatalf("renameFile calls = %d, want 1", renameCalls)
	}
	if removeCalls != 0 {
		t.Fatalf("removeFile calls = %d, want 0", removeCalls)
	}
}

func TestCopyRegularFileOverwritesExistingFile(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "source.txt")
	mustWriteFile(t, srcPath, "source")
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "dest.txt")
	mustWriteFile(t, destPath, "old")

	err := copyRegularFile(srcPath, destPath, 0644)
	if err != nil {
		t.Fatalf("copyRegularFile() error = %v", err)
	}
	if data, err := os.ReadFile(destPath); err != nil || string(data) != "source" {
		t.Fatalf("destination = %q, %v, want overwritten file", string(data), err)
	}
}

func TestCopyRegularFileRejectsSymlinkDestination(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "source.txt")
	mustWriteFile(t, srcPath, "source")
	destDir := t.TempDir()
	target := filepath.Join(destDir, "target.txt")
	mustWriteFile(t, target, "target")
	link := filepath.Join(destDir, "dest.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := copyRegularFile(srcPath, link, 0644)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyRegularFile() err = %v, want symlink destination error", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "target" {
		t.Fatalf("symlink target = %q, %v, want untouched target", string(data), err)
	}
}

func TestEnsureNoSymlinkPathRejectsRelativeFirstComponent(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(tmpDir, "docs")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err = ensureNoSymlinkPath(filepath.Join("docs", "nested", "file.txt"))
	if err == nil || !strings.Contains(err.Error(), "destination directory contains a symlink") {
		t.Fatalf("ensureNoSymlinkPath() err = %v, want first relative component symlink error", err)
	}
}

func TestCopyRegularFileRejectsSymlinkDestinationDirectory(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "source.txt")
	mustWriteFile(t, srcPath, "source")
	rootDir := t.TempDir()
	outsideDir := t.TempDir()
	link := filepath.Join(rootDir, "linked-dir")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := copyRegularFile(srcPath, filepath.Join(link, "dest.txt"), 0644)
	if err == nil || !strings.Contains(err.Error(), "destination directory contains a symlink") {
		t.Fatalf("copyRegularFile() err = %v, want symlink directory error", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "dest.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink target directory should remain untouched, got err=%v", err)
	}
}
