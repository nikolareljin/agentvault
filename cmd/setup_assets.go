package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/workflowtemplates"
)

const (
	setupAssetKindProviderFile        = "provider_file"
	setupAssetKindProjectFile         = "project_file"
	setupAssetKindInstruction         = "instruction_override"
	setupAssetKindSkill               = "skill_asset"
	setupAssetOriginVaultManaged      = "vault-managed"
	setupAssetOriginProviderHome      = "provider-home"
	setupAssetOriginProjectLocal      = "project-local"
	setupAssetOriginGenerated         = "generated"
	setupAssetOriginDetected          = "detected"
	setupAssetRootHome                = "home"
	setupAssetRootConfig              = "config"
	setupAssetRootProject             = "project_root"
	setupAssetRootProviderClaude      = "provider_claude"
	setupAssetRootProviderCodex       = "provider_codex"
	setupAssetRootProviderCopilot     = "provider_copilot"
	setupAssetRootProviderClaudeSkill = "provider_claude_skill"
	setupAssetRootProviderCodexSkill  = "provider_codex_skill"
)

// SetupAsset stores one portable file asset captured during setup export.
type SetupAsset struct {
	Kind                string `json:"kind"`
	Origin              string `json:"origin"`
	LogicalRoot         string `json:"logical_root"`
	LogicalPath         string `json:"logical_path"`
	ProjectRelativePath string `json:"project_relative_path,omitempty"`
	SourcePath          string `json:"source_path,omitempty"`
	Sensitive           bool   `json:"sensitive,omitempty"`
	Missing             bool   `json:"missing,omitempty"`
	Redacted            bool   `json:"redacted,omitempty"`
	Content             []byte `json:"content,omitempty"`
	SHA256              string `json:"sha256,omitempty"`
}

type setupAssetCollection struct {
	ProviderFiles        []SetupAsset `json:"provider_files,omitempty"`
	ProjectFiles         []SetupAsset `json:"project_files,omitempty"`
	InstructionOverrides []SetupAsset `json:"instruction_overrides,omitempty"`
	SkillAssets          []SetupAsset `json:"skill_assets,omitempty"`
}

type setupAssetOptions struct {
	ProjectDir     string
	IncludeSecrets bool
	ConfigDir      string
}

func collectSetupAssets(opts setupAssetOptions) (setupAssetCollection, []string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return setupAssetCollection{}, nil, fmt.Errorf("resolving user home for setup asset export: %w", err)
	}

	var assets setupAssetCollection
	var warnings []string

	providerAssets, providerWarnings, err := collectProviderHomeAssets(homeDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.ProviderFiles = providerAssets
	warnings = append(warnings, providerWarnings...)

	projectAssets, instructionAssets, projectWarnings, err := collectProjectAssets(opts.ProjectDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.ProjectFiles = projectAssets
	assets.InstructionOverrides = instructionAssets
	warnings = append(warnings, projectWarnings...)

	skillAssets, skillWarnings, err := collectSkillAssets(homeDir, opts.ProjectDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.SkillAssets = skillAssets
	warnings = append(warnings, skillWarnings...)

	sortSetupAssets(assets.ProviderFiles)
	sortSetupAssets(assets.ProjectFiles)
	sortSetupAssets(assets.InstructionOverrides)
	sortSetupAssets(assets.SkillAssets)
	return assets, warnings, nil
}

func collectProviderHomeAssets(homeDir string, includeSecrets bool) ([]SetupAsset, []string, error) {
	specs := []struct {
		path        string
		root        string
		logicalPath string
		sensitive   bool
		optional    bool
	}{
		{
			path:        filepath.Join(homeDir, ".claude", "settings.json"),
			root:        setupAssetRootProviderClaude,
			logicalPath: "settings.json",
			sensitive:   true,
			optional:    true,
		},
		{
			path:        filepath.Join(homeDir, ".claude", "keybindings.json"),
			root:        setupAssetRootProviderClaude,
			logicalPath: "keybindings.json",
			sensitive:   false,
			optional:    true,
		},
		{
			path:        filepath.Join(homeDir, ".codex", "config.toml"),
			root:        setupAssetRootProviderCodex,
			logicalPath: "config.toml",
			sensitive:   true,
			optional:    true,
		},
	}

	assets := make([]SetupAsset, 0, len(specs))
	var warnings []string
	for _, spec := range specs {
		asset, warn, err := loadSetupAsset(spec.path, setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root, spec.logicalPath, "", spec.sensitive, includeSecrets, spec.optional)
		if err != nil {
			return nil, warnings, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if asset.LogicalPath != "" {
			assets = append(assets, asset)
		}
	}

	codexRulesDir := filepath.Join(homeDir, ".codex", "rules")
	ruleAssets, warningsOut, err := collectDirFiles(codexRulesDir, setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderCodex, "rules", "", false, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	assets = append(assets, ruleAssets...)
	warnings = append(warnings, warningsOut...)
	return assets, warnings, nil
}

func collectProjectAssets(projectDir string, includeSecrets bool) ([]SetupAsset, []SetupAsset, []string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, nil, nil, nil
	}
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving project path: %w", err)
	}

	projectFiles := make([]SetupAsset, 0)
	instructionOverrides := make([]SetupAsset, 0)
	var warnings []string

	seen := make(map[string]struct{})
	for _, filename := range sortedInstructionFilenames() {
		absPath := filepath.Join(projectDir, filepath.FromSlash(filename))
		asset, warn, err := loadSetupAsset(absPath, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, filepath.ToSlash(filename), filepath.ToSlash(filename), false, includeSecrets, true)
		if err != nil {
			return nil, nil, warnings, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if asset.LogicalPath == "" {
			continue
		}
		projectFiles = append(projectFiles, asset)
		seen[asset.SourcePath] = struct{}{}
		instructionOverrides = append(instructionOverrides, SetupAsset{
			Kind:                setupAssetKindInstruction,
			Origin:              setupAssetOriginProjectLocal,
			LogicalRoot:         setupAssetRootProject,
			LogicalPath:         asset.LogicalPath,
			ProjectRelativePath: asset.ProjectRelativePath,
			SourcePath:          asset.SourcePath,
			Sensitive:           asset.Sensitive,
			Missing:             asset.Missing,
			Redacted:            asset.Redacted,
			Content:             asset.Content,
			SHA256:              asset.SHA256,
		})
	}

	for _, key := range workflowtemplates.SupportedKeys() {
		filename, ok := workflowtemplates.FindTemplateFilename(key)
		if !ok {
			continue
		}
		absPath := filepath.Join(projectDir, filename)
		asset, warn, err := loadSetupAsset(absPath, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, filepath.ToSlash(filename), filepath.ToSlash(filename), false, includeSecrets, true)
		if err != nil {
			return nil, nil, warnings, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if asset.LogicalPath == "" {
			continue
		}
		if _, exists := seen[asset.SourcePath]; exists {
			continue
		}
		projectFiles = append(projectFiles, asset)
	}

	return projectFiles, instructionOverrides, warnings, nil
}

func collectSkillAssets(homeDir string, projectDir string, includeSecrets bool) ([]SetupAsset, []string, error) {
	var assets []SetupAsset
	var warnings []string

	claudeSkills, claudeWarnings, err := collectDirFiles(filepath.Join(homeDir, ".claude", "skills"), setupAssetKindSkill, setupAssetOriginProviderHome, setupAssetRootProviderClaudeSkill, "", "", false, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	assets = append(assets, claudeSkills...)
	warnings = append(warnings, claudeWarnings...)

	codexSkills, codexWarnings, err := collectDirFiles(filepath.Join(homeDir, ".codex", "skills"), setupAssetKindSkill, setupAssetOriginProviderHome, setupAssetRootProviderCodexSkill, "", "", false, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	assets = append(assets, codexSkills...)
	warnings = append(warnings, codexWarnings...)

	if strings.TrimSpace(projectDir) != "" {
		projectSkills, projectWarnings, err := collectDirFiles(filepath.Join(projectDir, "skills"), setupAssetKindSkill, setupAssetOriginProjectLocal, setupAssetRootProject, "skills", "skills", false, includeSecrets)
		if err != nil {
			return nil, warnings, err
		}
		assets = append(assets, projectSkills...)
		warnings = append(warnings, projectWarnings...)
	}

	return assets, warnings, nil
}

func collectDirFiles(root string, kind string, origin string, logicalRoot string, logicalPrefix string, projectPrefix string, sensitive bool, includeSecrets bool) ([]SetupAsset, []string, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{fmt.Sprintf("optional asset root %q not found; skipping", root)}, nil
		}
		return nil, nil, fmt.Errorf("reading asset root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, []string{fmt.Sprintf("asset root %q is not a directory; skipping", root)}, nil
	}

	var assets []SetupAsset
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking %q: %w", root, walkErr)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("computing relative path for %q: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		logicalPath := rel
		if logicalPrefix != "" {
			logicalPath = filepath.ToSlash(filepath.Join(logicalPrefix, rel))
		}
		projectRelative := ""
		if projectPrefix != "" {
			projectRelative = filepath.ToSlash(filepath.Join(projectPrefix, rel))
		}
		asset, _, err := loadSetupAsset(path, kind, origin, logicalRoot, logicalPath, projectRelative, sensitive, includeSecrets, false)
		if err != nil {
			return err
		}
		assets = append(assets, asset)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return assets, nil, nil
}

func loadSetupAsset(path string, kind string, origin string, logicalRoot string, logicalPath string, projectRelativePath string, sensitive bool, includeSecrets bool, optional bool) (SetupAsset, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && optional {
			return SetupAsset{}, fmt.Sprintf("optional asset %q not found; skipping", path), nil
		}
		return SetupAsset{}, "", fmt.Errorf("reading asset %q: %w", path, err)
	}
	asset := SetupAsset{
		Kind:                kind,
		Origin:              origin,
		LogicalRoot:         logicalRoot,
		LogicalPath:         filepath.ToSlash(logicalPath),
		ProjectRelativePath: filepath.ToSlash(projectRelativePath),
		SourcePath:          path,
		Sensitive:           sensitive,
		SHA256:              hashSetupAssetContent(data),
	}
	if sensitive && !includeSecrets {
		asset.Redacted = true
		return asset, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", path), nil
	}
	asset.Content = data
	return asset, "", nil
}

func hashSetupAssetContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortSetupAssets(items []SetupAsset) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].LogicalRoot != items[j].LogicalRoot {
			return items[i].LogicalRoot < items[j].LogicalRoot
		}
		return items[i].LogicalPath < items[j].LogicalPath
	})
}

func sortedInstructionFilenames() []string {
	seen := make(map[string]struct{}, len(agent.WellKnownInstructions))
	filenames := make([]string, 0, len(agent.WellKnownInstructions))
	for _, filename := range agent.WellKnownInstructions {
		if _, ok := seen[filename]; ok {
			continue
		}
		seen[filename] = struct{}{}
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	return filenames
}
