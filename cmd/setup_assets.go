package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	setupAssetOriginProviderHome      = "provider-home"
	setupAssetOriginProjectLocal      = "project-local"
	setupAssetRootProject             = "project_root"
	setupAssetRootProviderClaude      = "provider_claude"
	setupAssetRootProviderCodex       = "provider_codex"
	setupAssetRootProviderCopilot     = "provider_copilot"
	setupAssetRootProviderClaudeSkill = "provider_claude_skill"
	setupAssetRootProviderCodexSkill  = "provider_codex_skill"
	setupImportedAssetsDirName        = "imported-assets"
	maxSetupAssetBytes                = 1 << 20
)

var errUnsupportedProviderAssetRoot = errors.New("unsupported provider asset root")

var renameFile = os.Rename
var removeFile = os.Remove

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
	ContentPresent      bool   `json:"content_present,omitempty"`
	SizeBytes           int64  `json:"size_bytes,omitempty"`
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

	copilotConfigDir := filepath.Join(homeDir, ".config", "github-copilot")
	copilotAssets, copilotWarnings, err := collectDirFiles(copilotConfigDir, setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderCopilot, "", "", true, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	assets = append(assets, copilotAssets...)
	warnings = append(warnings, copilotWarnings...)

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
	var instructionWarnings []string
	var workflowWarnings []string

	seen := make(map[string]struct{})
	missingInstructionFiles := 0
	for _, filename := range sortedInstructionFilenames() {
		absPath := filepath.Join(projectDir, filepath.FromSlash(filename))
		asset, warn, err := loadSetupAsset(absPath, setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject, filepath.ToSlash(filename), filepath.ToSlash(filename), false, includeSecrets, true)
		if err != nil {
			return nil, nil, warnings, err
		}
		if asset.Missing {
			missingInstructionFiles++
			continue
		} else if warn != "" {
			instructionWarnings = append(instructionWarnings, warn)
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
			ContentPresent:      asset.ContentPresent,
			SizeBytes:           asset.SizeBytes,
			Content:             asset.Content,
			SHA256:              asset.SHA256,
		})
	}
	if missingInstructionFiles > 0 {
		warnings = append(warnings, fmt.Sprintf("project export skipped %d missing optional instruction file(s)", missingInstructionFiles))
	}
	warnings = append(warnings, instructionWarnings...)

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
		if asset.Missing {
			missingWorkflowFiles++
			continue
		} else if warn != "" {
			workflowWarnings = append(workflowWarnings, warn)
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
	warnings = append(warnings, workflowWarnings...)

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
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{fmt.Sprintf("optional asset root %q not found; skipping", root)}, nil
		}
		return nil, nil, fmt.Errorf("reading asset root %q: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, []string{fmt.Sprintf("skipping symlink asset root %q; not following symlinks", root)}, nil
	}
	if !info.IsDir() {
		return nil, []string{fmt.Sprintf("asset root %q is not a directory; skipping", root)}, nil
	}

	var assets []SetupAsset
	var warnings []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking %q: %w", root, walkErr)
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf("skipping symlink asset %q under %q", path, root))
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
		asset, warn, err := loadSetupAsset(path, kind, origin, logicalRoot, logicalPath, projectRelative, sensitive, includeSecrets, false)
		if err != nil {
			return err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		assets = append(assets, asset)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return assets, warnings, nil
}

func loadSetupAsset(path string, kind string, origin string, logicalRoot string, logicalPath string, projectRelativePath string, sensitive bool, includeSecrets bool, optional bool) (SetupAsset, string, error) {
	safeLogicalPath, err := sanitizeAssetRelativePath(logicalPath)
	if err != nil {
		return SetupAsset{}, "", fmt.Errorf("sanitizing logical path %q: %w", logicalPath, err)
	}
	safeProjectRelativePath := ""
	if strings.TrimSpace(projectRelativePath) != "" {
		safeProjectRelativePath, err = sanitizeAssetRelativePath(projectRelativePath)
		if err != nil {
			return SetupAsset{}, "", fmt.Errorf("sanitizing project-relative path %q: %w", projectRelativePath, err)
		}
	}

	asset := SetupAsset{
		Kind:                kind,
		Origin:              origin,
		LogicalRoot:         logicalRoot,
		LogicalPath:         safeLogicalPath,
		ProjectRelativePath: safeProjectRelativePath,
		SourcePath:          path,
		Sensitive:           sensitive,
	}

	info, statErr := os.Lstat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) && optional {
			asset.Missing = true
			return asset, fmt.Sprintf("optional asset missing (root=%s, path=%s)", logicalRoot, safeLogicalPath), nil
		}
		return SetupAsset{}, "", fmt.Errorf("reading asset metadata %q: %w", path, statErr)
	}
	asset.SizeBytes = info.Size()
	if info.Mode()&os.ModeSymlink != 0 {
		return SetupAsset{}, "", fmt.Errorf("asset %q must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return SetupAsset{}, "", fmt.Errorf("asset %q is not a regular file", path)
	}
	if asset.SizeBytes > maxSetupAssetBytes {
		return asset, fmt.Sprintf("asset %q exceeds %d bytes; exporting metadata only", path, maxSetupAssetBytes), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return SetupAsset{}, "", fmt.Errorf("reading asset %q: %w", path, err)
	}
	asset.SizeBytes = int64(len(data))
	if sensitive && !includeSecrets {
		asset.Redacted = true
		return asset, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", path), nil
	}
	asset.SHA256 = hashSetupAssetContent(data)
	asset.ContentPresent = true
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
		if asset.Redacted || asset.Missing || !asset.ContentPresent {
			if asset.Redacted {
				warnings = append(warnings, fmt.Sprintf("staging skipped redacted asset %s", describeSetupAsset(asset)))
			}
			if !asset.Redacted && !asset.Missing && !asset.ContentPresent {
				warnings = append(warnings, fmt.Sprintf("staging skipped asset %s because bundle omitted file content", describeSetupAsset(asset)))
			}
			continue
		}
		stagePath, err := stagedAssetPath(stageRoot, asset)
		if err != nil {
			if errors.Is(err, errUnsupportedProviderAssetRoot) {
				warnings = append(warnings, fmt.Sprintf("staging skipped asset %s because %v", describeSetupAsset(asset), err))
				continue
			}
			return staged, warnings, fmt.Errorf("resolving staged asset path: %w", err)
		}
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

func describeSetupAsset(asset SetupAsset) string {
	path := asset.ProjectRelativePath
	if path == "" {
		path = asset.LogicalPath
	}
	if path == "" {
		path = "<unknown>"
	}
	return fmt.Sprintf("(root=%s path=%s)", asset.LogicalRoot, path)
}

func stagedAssetPath(stageRoot string, asset SetupAsset) (string, error) {
	switch asset.Kind {
	case setupAssetKindProviderFile:
		providerStageRoot, ok := providerStageDirectoryName(asset.LogicalRoot)
		if !ok {
			return "", fmt.Errorf("%w %q", errUnsupportedProviderAssetRoot, asset.LogicalRoot)
		}
		rel, err := sanitizeAssetRelativePath(asset.LogicalPath)
		if err != nil {
			return "", err
		}
		return safeJoinUnder(filepath.Join(stageRoot, "provider", providerStageRoot), rel)
	case setupAssetKindProjectFile:
		rel := asset.ProjectRelativePath
		if rel == "" {
			rel = asset.LogicalPath
		}
		rel, err := sanitizeAssetRelativePath(rel)
		if err != nil {
			return "", err
		}
		return safeJoinUnder(filepath.Join(stageRoot, "project"), rel)
	case setupAssetKindSkill:
		switch asset.LogicalRoot {
		case setupAssetRootProject:
			rel := asset.ProjectRelativePath
			if rel == "" {
				rel = asset.LogicalPath
			}
			rel, err := sanitizeAssetRelativePath(rel)
			if err != nil {
				return "", err
			}
			return safeJoinUnder(filepath.Join(stageRoot, "project"), rel)
		default:
			providerStageRoot, ok := providerStageDirectoryName(asset.LogicalRoot)
			if !ok {
				return "", fmt.Errorf("%w %q", errUnsupportedProviderAssetRoot, asset.LogicalRoot)
			}
			rel, err := sanitizeAssetRelativePath(asset.LogicalPath)
			if err != nil {
				return "", err
			}
			return safeJoinUnder(filepath.Join(stageRoot, "provider", providerStageRoot), rel)
		}
	default:
		return "", nil
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
		if asset.Redacted || asset.Missing || !asset.ContentPresent {
			if asset.Redacted {
				warnings = append(warnings, fmt.Sprintf("provider asset %s is redacted; cannot apply without re-importing with --include-secrets", describeSetupAsset(asset)))
			}
			if !asset.Redacted && !asset.Missing && !asset.ContentPresent {
				warnings = append(warnings, fmt.Sprintf("provider asset %s omitted file content in bundle; cannot apply", describeSetupAsset(asset)))
			}
			continue
		}
		dest, err := providerAssetDestination(homeDir, asset)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", describeSetupAsset(asset), err))
			continue
		}
		if err := ensureNoSymlinkPath(filepath.Dir(dest)); err != nil {
			warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", describeSetupAsset(asset), err))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return applied, warnings, fmt.Errorf("creating provider asset directory: %w", err)
		}
		if info, err := os.Lstat(dest); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: destination path is a symlink", describeSetupAsset(asset)))
				continue
			}
		} else if !os.IsNotExist(err) {
			return applied, warnings, fmt.Errorf("checking provider asset destination %q: %w", dest, err)
		}
		if err := writeRegularFileAtomic(dest, asset.Content, 0600); err != nil {
			return applied, warnings, fmt.Errorf("writing provider asset %q: %w", dest, err)
		}
		applied++
	}
	return applied, warnings, nil
}

func providerAssetDestination(homeDir string, asset SetupAsset) (string, error) {
	switch asset.LogicalRoot {
	case setupAssetRootProviderClaude:
		return safeJoinUnder(filepath.Join(homeDir, ".claude"), asset.LogicalPath)
	case setupAssetRootProviderCodex:
		return safeJoinUnder(filepath.Join(homeDir, ".codex"), asset.LogicalPath)
	case setupAssetRootProviderCopilot:
		return safeJoinUnder(filepath.Join(homeDir, ".config", "github-copilot"), asset.LogicalPath)
	case setupAssetRootProviderClaudeSkill:
		return safeJoinUnder(filepath.Join(homeDir, ".claude", "skills"), asset.LogicalPath)
	case setupAssetRootProviderCodexSkill:
		return safeJoinUnder(filepath.Join(homeDir, ".codex", "skills"), asset.LogicalPath)
	default:
		return "", fmt.Errorf("unsupported provider asset root %q", asset.LogicalRoot)
	}
}

func providerStageDirectoryName(logicalRoot string) (string, bool) {
	switch logicalRoot {
	case setupAssetRootProviderClaude:
		return setupAssetRootProviderClaude, true
	case setupAssetRootProviderCodex:
		return setupAssetRootProviderCodex, true
	case setupAssetRootProviderCopilot:
		return setupAssetRootProviderCopilot, true
	case setupAssetRootProviderClaudeSkill:
		return setupAssetRootProviderClaudeSkill, true
	case setupAssetRootProviderCodexSkill:
		return setupAssetRootProviderCodexSkill, true
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
	rel, err := sanitizeAssetRelativePath(rel)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	joined := filepath.Join(root, rel)
	relToRoot, err := filepath.Rel(root, joined)
	if err != nil {
		return "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	return joined, nil
}

func sanitizeAssetRelativePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty relative path")
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(path) || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute path is not allowed: %q", path)
	}
	if filepath.VolumeName(path) != "" || filepath.VolumeName(cleaned) != "" || strings.Contains(cleaned, ":") {
		return "", fmt.Errorf("volume-qualified path is not allowed: %q", path)
	}
	if cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("invalid relative path: %q", path)
	}
	if strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal is not allowed: %q", path)
	}
	return filepath.ToSlash(cleaned), nil
}

func ensureNoSymlinkPath(path string) error {
	if path == "" {
		return nil
	}
	cleaned := filepath.Clean(path)
	sep := string(filepath.Separator)
	volume := filepath.VolumeName(cleaned)

	var parts []string
	var current string

	if filepath.IsAbs(cleaned) {
		if volume != "" {
			current = volume + sep
			rest := strings.TrimPrefix(cleaned[len(volume):], sep)
			if rest == "" {
				return nil
			}
			parts = strings.Split(rest, sep)
		} else {
			current = sep
			rest := strings.TrimPrefix(cleaned, sep)
			if rest == "" {
				return nil
			}
			parts = strings.Split(rest, sep)
		}
	} else {
		parts = strings.Split(cleaned, sep)
		if len(parts) == 0 {
			return nil
		}
		current = "."
	}

	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination directory contains a symlink: %q", current)
		}
	}
	return nil
}

func replaceRegularFile(tmpPath string, destPath string) error {
	err := renameFile(tmpPath, destPath)
	if err == nil {
		return nil
	}

	if runtime.GOOS != "windows" {
		return err
	}

	backupPath := destPath + ".bak"
	_ = removeFile(backupPath)
	if backupErr := renameFile(destPath, backupPath); backupErr != nil {
		if os.IsNotExist(backupErr) {
			return err
		}
		return backupErr
	}
	if retryErr := renameFile(tmpPath, destPath); retryErr != nil {
		if restoreErr := renameFile(backupPath, destPath); restoreErr != nil && !os.IsNotExist(restoreErr) {
			return fmt.Errorf("renaming replacement failed: %v; restoring original failed: %v", retryErr, restoreErr)
		}
		return retryErr
	}
	_ = removeFile(backupPath)
	return nil
}

func writeRegularFileAtomic(destPath string, content []byte, perm os.FileMode) error {
	destDir := filepath.Dir(destPath)
	if err := ensureNoSymlinkPath(destDir); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(destPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination path is a symlink and is not allowed: %q", destPath)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tmpFile, err := os.CreateTemp(destDir, ".asset-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmpFile.Write(content); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := replaceRegularFile(tmpPath, destPath); err != nil {
		return err
	}
	tmpPath = ""
	return nil
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

	destDir := filepath.Dir(destPath)
	if err := ensureNoSymlinkPath(destDir); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(destPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination path is a symlink and is not allowed: %q", destPath)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tmpFile, err := os.CreateTemp(destDir, ".asset-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(perm); err != nil {
		return err
	}
	if _, err := io.Copy(tmpFile, src); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := replaceRegularFile(tmpPath, destPath); err != nil {
		return err
	}
	tmpPath = ""
	return nil
}
