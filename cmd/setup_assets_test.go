package cmd

import (
	"os"
	"path/filepath"
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
		t.Fatalf("collectSetupAssets() warnings = none, want optional asset warnings for absent roots")
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

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}
