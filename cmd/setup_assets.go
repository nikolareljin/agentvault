package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	setupImportedAssetsDirName        = "imported-assets"
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
}

func collectSetupAssets(opts setupAssetOptions) (setupAssetCollection, []string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return setupAssetCollection{}, nil, fmt.Errorf("resolving user home for setup asset export: %w", err)
	}

	var assets setupAssetCollection
	var warnings []string
	projectDir, err := normalizeOptionalDir(opts.ProjectDir)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}

	providerAssets, providerWarnings, err := collectProviderHomeAssets(homeDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.ProviderFiles = providerAssets
	warnings = append(warnings, providerWarnings...)

	projectAssets, instructionAssets, projectWarnings, err := collectProjectAssets(projectDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.ProjectFiles = projectAssets
	assets.InstructionOverrides = instructionAssets
	warnings = append(warnings, projectWarnings...)

	skillAssets, skillWarnings, err := collectSkillAssets(homeDir, projectDir, opts.IncludeSecrets)
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
	missingInstructionFiles := 0
	for _, filename := range sortedInstructionFilenames() {
		absPath := filepath.Join(projectDir, filepath.FromSlash(filename))
		asset, warn, err := loadSetupAsset(absPath, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, filepath.ToSlash(filename), filepath.ToSlash(filename), false, includeSecrets, true)
		if err != nil {
			return nil, nil, warnings, err
		}
		if warn != "" {
			missingInstructionFiles++
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
	if missingInstructionFiles > 0 {
		warnings = append(warnings, fmt.Sprintf("project export skipped %d missing optional instruction file(s)", missingInstructionFiles))
	}

	missingWorkflowFiles := 0
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
			missingWorkflowFiles++
		}
		if asset.LogicalPath == "" {
			continue
		}
		if _, exists := seen[asset.SourcePath]; exists {
			continue
		}
		projectFiles = append(projectFiles, asset)
	}
	if missingWorkflowFiles > 0 {
		warnings = append(warnings, fmt.Sprintf("project export skipped %d missing optional workflow template file(s)", missingWorkflowFiles))
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
			return SetupAsset{}, "optional asset missing", nil
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

func normalizeOptionalDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving directory %q: %w", path, err)
	}
	return abs, nil
}

func instructionNameForAsset(asset SetupAsset) string {
	needle := filepath.ToSlash(asset.ProjectRelativePath)
	if needle == "" {
		needle = filepath.ToSlash(asset.LogicalPath)
	}
	for name, filename := range agent.WellKnownInstructions {
		if filepath.ToSlash(filename) == needle {
			return name
		}
	}
	base := filepath.Base(needle)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func stageImportedAssets(configDir string, assets []SetupAsset) (int, []string, error) {
	stageRoot := filepath.Join(configDir, setupImportedAssetsDirName)
	if err := os.RemoveAll(stageRoot); err != nil && !os.IsNotExist(err) {
		return 0, nil, fmt.Errorf("resetting staged imported assets: %w", err)
	}

	staged := 0
	var warnings []string
	for _, asset := range assets {
		if asset.Redacted || len(asset.Content) == 0 {
			if asset.Redacted {
				warnings = append(warnings, fmt.Sprintf("staging skipped redacted asset %q", asset.SourcePath))
			}
			continue
		}
		stagePath := stagedAssetPath(stageRoot, asset)
		if stagePath == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(stagePath), 0700); err != nil {
			return staged, warnings, fmt.Errorf("creating staged asset directory: %w", err)
		}
		if err := os.WriteFile(stagePath, asset.Content, 0600); err != nil {
			return staged, warnings, fmt.Errorf("writing staged asset %q: %w", stagePath, err)
		}
		staged++
	}
	return staged, warnings, nil
}

func stagedAssetPath(stageRoot string, asset SetupAsset) string {
	switch asset.Kind {
	case setupAssetKindProviderFile:
		return filepath.Join(stageRoot, "provider", asset.LogicalRoot, filepath.FromSlash(asset.LogicalPath))
	case setupAssetKindProjectFile:
		rel := asset.ProjectRelativePath
		if rel == "" {
			rel = asset.LogicalPath
		}
		return filepath.Join(stageRoot, "project", filepath.FromSlash(rel))
	case setupAssetKindSkill:
		switch asset.LogicalRoot {
		case setupAssetRootProject:
			rel := asset.ProjectRelativePath
			if rel == "" {
				rel = asset.LogicalPath
			}
			return filepath.Join(stageRoot, "project", filepath.FromSlash(rel))
		default:
			return filepath.Join(stageRoot, "provider", asset.LogicalRoot, filepath.FromSlash(asset.LogicalPath))
		}
	default:
		return ""
	}
}

func applyStagedProjectAssets(configDir string, targetDir string) (int, error) {
	projectRoot := filepath.Join(configDir, setupImportedAssetsDirName, "project")
	info, err := os.Stat(projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading staged project assets: %w", err)
	}
	if !info.IsDir() {
		return 0, nil
	}

	relPaths := make([]string, 0)
	err = filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged project asset %q must not be a symlink", path)
		}
		rel, err := safeSubpath(projectRoot, path)
		if err != nil {
			return err
		}
		relPaths = append(relPaths, rel)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("applying staged project assets: %w", err)
	}

	applied := 0
	for _, rel := range relPaths {
		srcPath, err := safeJoinUnder(projectRoot, rel)
		if err != nil {
			return applied, fmt.Errorf("resolving staged asset path: %w", err)
		}
		destPath, err := safeJoinUnder(targetDir, rel)
		if err != nil {
			return applied, fmt.Errorf("resolving target asset path: %w", err)
		}
		if err := copyRegularFile(srcPath, destPath, 0644); err != nil {
			return applied, fmt.Errorf("copying staged project asset %q: %w", rel, err)
		}
		applied++
	}
	return applied, nil
}

func applyProviderAssetsToSystem(homeDir string, assets []SetupAsset) (int, []string, error) {
	applied := 0
	var warnings []string
	for _, asset := range assets {
		if asset.Redacted || len(asset.Content) == 0 {
			if asset.Redacted {
				warnings = append(warnings, fmt.Sprintf("provider asset %q is redacted; cannot apply without re-importing with --include-secrets", asset.SourcePath))
			}
			continue
		}
		dest, ok := providerAssetDestination(homeDir, asset)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("provider asset root %q is not supported for apply; staged only", asset.LogicalRoot))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return applied, warnings, fmt.Errorf("creating provider asset directory: %w", err)
		}
		if err := os.WriteFile(dest, asset.Content, 0600); err != nil {
			return applied, warnings, fmt.Errorf("writing provider asset %q: %w", dest, err)
		}
		applied++
	}
	return applied, warnings, nil
}

func providerAssetDestination(homeDir string, asset SetupAsset) (string, bool) {
	switch asset.LogicalRoot {
	case setupAssetRootProviderClaude:
		return filepath.Join(homeDir, ".claude", filepath.FromSlash(asset.LogicalPath)), true
	case setupAssetRootProviderCodex:
		return filepath.Join(homeDir, ".codex", filepath.FromSlash(asset.LogicalPath)), true
	case setupAssetRootProviderClaudeSkill:
		return filepath.Join(homeDir, ".claude", "skills", filepath.FromSlash(asset.LogicalPath)), true
	case setupAssetRootProviderCodexSkill:
		return filepath.Join(homeDir, ".codex", "skills", filepath.FromSlash(asset.LogicalPath)), true
	default:
		return "", false
	}
}

func safeSubpath(root string, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return "", fmt.Errorf("empty relative path for %q", path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q escapes root %q", path, root)
	}
	return rel, nil
}

func safeJoinUnder(root string, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return "", fmt.Errorf("empty relative path")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	return filepath.Join(root, rel), nil
}

func copyRegularFile(srcPath string, destPath string, perm os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file")
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		return err
	}
	return nil
}
