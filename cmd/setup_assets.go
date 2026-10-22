package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/fsname"
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
	// MergeKeys, when set, are the only top-level JSON keys this asset
	// carries. Import merges them into the existing file instead of replacing
	// it, because the rest of that file is the other machine's own state.
	MergeKeys []string `json:"merge_keys,omitempty"`
	// Mode is the source file's permission bits. Import keeps the execute
	// bit, so a hook script still runs on the other machine.
	Mode uint32 `json:"mode,omitempty"`
	// RedactedFields are the JSON Pointers of secret values blanked in
	// Content. Import fills them from the file already on the machine, or
	// leaves them out. The rest of the file travels.
	RedactedFields []string `json:"redacted_fields,omitempty"`
}

// execAllowed is whether an asset may keep its execute bit on import. The bit
// comes from the bundle, so it is honoured only where an agent tool runs
// scripts it was configured to: a tool's own hooks directory. Everything
// else is written without it, whatever the bundle says.
func execAllowed(asset SetupAsset) bool {
	if asset.LogicalRoot == setupAssetRootProviderHome || asset.LogicalRoot == setupAssetRootProject {
		return false
	}
	if _, ok := providerRootDirs[asset.LogicalRoot]; !ok {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(filepath.Clean(asset.LogicalPath)), "hooks/")
}

// assetPerm is base, plus owner execute when the source file was executable.
func assetPerm(asset SetupAsset, base os.FileMode) os.FileMode {
	if os.FileMode(asset.Mode)&0o111 != 0 {
		return base | 0o100 | (base&0o044)>>2
	}
	return base
}

type setupAssetCollection struct {
	ProviderFiles        []SetupAsset `json:"provider_files,omitempty"`
	ProjectFiles         []SetupAsset `json:"project_files,omitempty"`
	InstructionOverrides []SetupAsset `json:"instruction_overrides,omitempty"`
	SkillAssets          []SetupAsset `json:"skill_assets,omitempty"`
	// Declined travels with the bundle so the receiving machine can see what
	// was left behind. A warning printed during export reaches only the person
	// running it.
	Declined []DeclinedAsset `json:"declined,omitempty"`
}

type setupAssetOptions struct {
	ProjectDir     string
	IncludeSecrets bool
	// Project-relative paths of `.local.` files to carry anyway, named one at a
	// time. There is deliberately no flag that takes all of them: these hold
	// per-machine values and some hold credentials, so carrying them is a
	// decision about a specific file, not a mode.
	IncludeLocalFiles []string
	// Agent memory (Claude project memory, Codex memories). Opt-in: it is
	// personal, and carrying it is a choice per export.
	IncludeMemory bool
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

	providerAssets, providerWarnings, providerDeclined, err := collectProviderHomeAssets(homeDir, opts.IncludeSecrets, opts.IncludeLocalFiles)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	extraAssets, extraWarnings, extraDeclined, err := collectExtraProviderAssets(homeDir, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	providerDeclined = append(providerDeclined, extraDeclined...)
	warnings = append(warnings, extraWarnings...)
	if opts.IncludeMemory {
		memoryAssets, memoryWarnings, err := collectMemoryAssets(homeDir, opts.IncludeSecrets)
		if err != nil {
			return setupAssetCollection{}, nil, err
		}
		extraAssets = append(extraAssets, memoryAssets...)
		warnings = append(warnings, memoryWarnings...)
	}
	extraAssets, localWarnings, localDeclined := declineLocalFiles(extraAssets, optedLocalFiles(opts.IncludeLocalFiles))
	warnings = append(warnings, localWarnings...)
	providerDeclined = append(providerDeclined, localDeclined...)
	providerAssets = append(providerAssets, extraAssets...)

	referenced, referencedDeclined, referencedWarnings, err := collectHomeReferencedFiles(homeDir, providerAssets, opts.IncludeSecrets)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	referenced, localWarnings, localDeclined = declineLocalFiles(referenced, optedLocalFiles(opts.IncludeLocalFiles))
	warnings = append(warnings, localWarnings...)
	providerDeclined = append(providerDeclined, localDeclined...)
	providerAssets = append(providerAssets, referenced...)
	providerDeclined = append(providerDeclined, referencedDeclined...)
	warnings = append(warnings, referencedWarnings...)

	assets.ProviderFiles = providerAssets
	assets.Declined = append(assets.Declined, providerDeclined...)
	warnings = append(warnings, providerWarnings...)

	projectAssets, instructionAssets, projectWarnings, projectDeclined, err := collectProjectAssets(projectDir, opts.IncludeSecrets, opts.IncludeLocalFiles)
	if err != nil {
		return setupAssetCollection{}, nil, err
	}
	assets.ProjectFiles = projectAssets
	assets.Declined = append(assets.Declined, projectDeclined...)
	// Two instruction files naming the same missing template is one fact, not
	// two. Without this a bundle lists a path once per file that mentions it.
	assets.Declined = dedupeDeclined(assets.Declined)
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
	if runtime.GOOS != "windows" {
		warnings = append(warnings, windowsNameWarnings(assets)...)
	}
	return assets, warnings, nil
}

func collectProviderHomeAssets(homeDir string, includeSecrets bool, includeLocal []string) ([]SetupAsset, []string, []DeclinedAsset, error) {
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
			// The user-level instruction file. A bundle carried the settings
			// and the skills but not the standing instructions, which is the
			// half that changes behaviour most: a restored machine had the
			// keybindings and none of the rules.
			//
			// Not sensitive: this is instruction text, and marking it so would
			// redact it unless --include-secrets, which restores a machine with
			// its rules blanked out. settings.json is sensitive because it can
			// hold credentials; prose cannot be redacted and still be prose.
			// The 1 MiB asset ceiling still applies, as it does to every asset.
			path:        filepath.Join(homeDir, ".claude", "CLAUDE.md"),
			root:        setupAssetRootProviderClaude,
			logicalPath: "CLAUDE.md",
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
	var declinedAssets []DeclinedAsset
	for _, spec := range specs {
		if warn, skip := symlinkedAsset(spec.path); skip && spec.optional {
			warnings = append(warnings, warn)
			continue
		}
		asset, warn, err := loadSetupAsset(spec.path, setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root, spec.logicalPath, "", spec.sensitive, includeSecrets, spec.optional)
		if err != nil {
			return nil, warnings, declinedAssets, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if !asset.Missing && asset.LogicalPath != "" {
			assets = append(assets, asset)
		}
	}

	copilotConfigDir := filepath.Join(homeDir, ".config", "github-copilot")
	copilotAssets, copilotWarnings, err := collectDirFiles(copilotConfigDir, setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderCopilot, "", "", true, includeSecrets)
	if err != nil {
		return nil, warnings, declinedAssets, err
	}
	assets = append(assets, copilotAssets...)
	warnings = append(warnings, copilotWarnings...)

	// Agent and command definitions are instructions too: an agent definition
	// says how a subagent behaves, and a command is a prompt someone typed once
	// and kept. Both were left on the machine they were written on.
	for _, sub := range []string{"agents", "commands"} {
		subAssets, subWarnings, err := collectDirFiles(
			filepath.Join(homeDir, ".claude", sub),
			setupAssetKindProviderFile, setupAssetOriginProviderHome,
			setupAssetRootProviderClaude, sub, "", false, includeSecrets)
		if err != nil {
			return nil, warnings, declinedAssets, err
		}
		assets = append(assets, subAssets...)
		warnings = append(warnings, subWarnings...)
	}

	// The same rule as directory scope: a .local. file stays behind unless it is
	// named. It was applied to one scope only, so creds.local.md was refused
	// inside a project and carried from the home directory.
	assets, declinedWarnings, declined := declineLocalFiles(assets, optedLocalFiles(includeLocal))
	warnings = append(warnings, declinedWarnings...)
	declinedAssets = append(declinedAssets, declined...)

	codexRulesDir := filepath.Join(homeDir, ".codex", "rules")
	ruleAssets, warningsOut, err := collectDirFiles(codexRulesDir, setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderCodex, "rules", "", false, includeSecrets)
	if err != nil {
		return nil, warnings, declinedAssets, err
	}
	assets = append(assets, ruleAssets...)
	warnings = append(warnings, warningsOut...)
	return assets, warnings, declinedAssets, nil
}

// dedupeDeclined keeps one entry per path and category, in first-seen order.
func dedupeDeclined(in []DeclinedAsset) []DeclinedAsset {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, d := range in {
		key := d.Category + "\x00" + d.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// declineLocalFiles removes the `.local.` files from a collected set unless the
// caller named one, and reports each removal by the same logical path the
// caller would use to opt it in.
//
// The rule is the same at user scope and at directory scope: these hold
// per-machine values and some hold credentials. Applying it to only one scope
// meant an agent definition called creds.local.md was refused inside a project
// and carried from the home directory.
func declineLocalFiles(assets []SetupAsset, opted map[string]bool) ([]SetupAsset, []string, []DeclinedAsset) {
	kept := assets[:0]
	var warnings []string
	var declined []DeclinedAsset
	for _, a := range assets {
		logical := filepath.ToSlash(a.LogicalPath)
		if isLocalSettingsFile(logical) && !opted[logical] {
			reason := "a .local. file is per-machine and may hold credentials; name it with --include-local to carry it"
			warnings = append(warnings, fmt.Sprintf("declined %s: %s", logical, reason))
			declined = append(declined, DeclinedAsset{
				Path:     logical,
				Category: DeclinedByPolicy,
				Reason:   reason,
			})
			continue
		}
		kept = append(kept, a)
	}
	return kept, warnings, declined
}

func optedLocalFiles(includeLocal []string) map[string]bool {
	opted := make(map[string]bool, len(includeLocal))
	for _, p := range includeLocal {
		opted[filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "./"))] = true
	}
	return opted
}

// collectProjectAgentSettings carries a project's own agent configuration:
// .claude/settings.json and any agent or command definitions kept beside it.
//
// A `.local.` file is left behind unless it is named. Those hold per-machine
// values and some hold credentials, so a bundle that carried them quietly
// would be a credential copy nobody asked for. Every one left behind is
// reported by name, because a silent omission and a file that was never there
// read the same on the far end.
func collectProjectAgentSettings(projectDir string, includeSecrets bool, includeLocal []string) ([]SetupAsset, []string, []DeclinedAsset, error) {
	opted := make(map[string]bool, len(includeLocal))
	for _, p := range includeLocal {
		opted[filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "./"))] = true
	}

	var assets []SetupAsset
	var warnings []string

	// settings.json can hold credentials, so it is sensitive and is redacted
	// without --include-secrets, exactly as the user-level one is. Agent and
	// command definitions are prose: redacting them restores a machine with its
	// rules blanked out, which is the same as not carrying them.
	roots := []struct {
		rel       string
		sensitive bool
	}{
		{filepath.Join(".claude"), true},
		{filepath.Join(".claude", "agents"), false},
		{filepath.Join(".claude", "commands"), false},
	}
	for _, root := range roots {
		rel := root.rel
		found, warns, err := collectDirFiles(filepath.Join(projectDir, rel),
			setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject,
			filepath.ToSlash(rel), filepath.ToSlash(rel), root.sensitive, includeSecrets)
		if err != nil {
			return nil, warnings, nil, err
		}
		warnings = append(warnings, warns...)
		for _, a := range found {
			logical := filepath.ToSlash(a.LogicalPath)
			// The agents and commands directories are collected on their own
			// pass; skip them here so a file is not carried twice.
			if rel == ".claude" && (strings.HasPrefix(logical, ".claude/agents/") ||
				strings.HasPrefix(logical, ".claude/commands/")) {
				continue
			}
			// Repository-scope settings are listed, not carried; see
			// projectRepoScopeSettings.
			if isProjectRepoScopeSetting(logical) {
				continue
			}
			assets = append(assets, a)
		}
	}
	assets, declinedWarnings, declined := declineLocalFiles(assets, opted)
	warnings = append(warnings, declinedWarnings...)
	return assets, warnings, declined, nil
}

// isLocalSettingsFile matches the .local. convention wherever it appears in the
// name: settings.local.json, and anything else a tool adopts with the same
// meaning.
func isLocalSettingsFile(logicalPath string) bool {
	return strings.Contains(filepath.Base(logicalPath), ".local.")
}

func collectProjectAssets(projectDir string, includeSecrets bool, includeLocal []string) ([]SetupAsset, []SetupAsset, []string, []DeclinedAsset, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, nil, nil, nil, nil
	}
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("resolving project path: %w", err)
	}

	projectFiles := make([]SetupAsset, 0)
	var projectDeclined []DeclinedAsset
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
			return nil, nil, warnings, projectDeclined, err
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
			return nil, nil, warnings, projectDeclined, err
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

	extras, extraWarnings, err := collectProjectInstructionExtras(projectDir, includeSecrets)
	if err != nil {
		return nil, nil, warnings, projectDeclined, err
	}
	projectFiles = append(projectFiles, extras...)
	warnings = append(warnings, extraWarnings...)

	// What the instruction files name, followed to the files it names in turn,
	// is carried; what cannot be is recorded with the reason. Without the
	// files, a rule pointing at a checklist arrives pointing at nothing.
	closure, closureDeclined, closureWarnings, err := collectProjectReferenceClosure(projectDir, projectFiles, includeSecrets)
	if err != nil {
		return nil, nil, warnings, projectDeclined, err
	}
	// A named .local. file follows the same rule as everywhere else.
	closure, localWarnings, localDeclined := declineLocalFiles(closure, optedLocalFiles(includeLocal))
	warnings = append(warnings, localWarnings...)
	projectDeclined = append(projectDeclined, localDeclined...)
	projectFiles = append(projectFiles, closure...)
	projectDeclined = append(projectDeclined, closureDeclined...)
	warnings = append(warnings, closureWarnings...)
	projectDeclined = append(projectDeclined, declineProjectRepoScopeSettings(projectDir)...)

	// Directory scope: the project's own agent configuration. An agent reads
	// system, user and directory scope; the bundle covered parts of two.
	dirScope, dirWarnings, dirDeclined, err := collectProjectAgentSettings(projectDir, includeSecrets, includeLocal)
	if err != nil {
		return nil, nil, warnings, projectDeclined, err
	}
	projectFiles = append(projectFiles, dirScope...)
	warnings = append(warnings, dirWarnings...)
	projectDeclined = append(projectDeclined, dirDeclined...)
	projectFiles = dedupeAssetsByLogicalPath(projectFiles)

	return projectFiles, instructionOverrides, warnings, projectDeclined, nil
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

// importBackupName matches the names writeExclusiveBackup creates.
var importBackupName = regexp.MustCompile(`\.bak-[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

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
		if !d.Type().IsRegular() {
			// A pipe or socket would block or fail the read, and end the export.
			warnings = append(warnings, fmt.Sprintf("skipping %q under %q: not a regular file", path, root))
			return nil
		}
		if isCredentialFile(path) {
			warnings = append(warnings, fmt.Sprintf("skipping credential file %q; sign in on the other machine instead", path))
			return nil
		}
		// A backup import made of a replaced file is this machine's old
		// copy, secrets and all; it never travels.
		if importBackupName.MatchString(d.Name()) {
			warnings = append(warnings, fmt.Sprintf("skipping import backup %q", path))
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("computing relative path for %q: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		// A .env or a key file inside a rules or agents directory holds a
		// secret like any other: redacted without --include-secrets.
		fileSensitive := sensitive || agent.SecretBearingReason(rel) != ""
		logicalPath := rel
		if logicalPrefix != "" {
			logicalPath = filepath.ToSlash(filepath.Join(logicalPrefix, rel))
		}
		projectRelative := ""
		if projectPrefix != "" {
			projectRelative = filepath.ToSlash(filepath.Join(projectPrefix, rel))
		}
		// A name import would refuse (NOTES~1.MD, aux.md) is left behind
		// here with a warning, not an error that ends the export.
		if _, err := sanitizeAssetRelativePath(logicalPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %q under %q: %v", path, root, err))
			return nil
		}
		asset, warn, err := loadSetupAsset(path, kind, origin, logicalRoot, logicalPath, projectRelative, fileSensitive, includeSecrets, false)
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

// assetPathTail is the end of path that names the asset within its root,
// plus the root directory itself (.aws, .docker). Directories above the root
// are the user's home or a temp dir, and are not checked: on Windows they
// can be short names (C:\Users\RUNNER~1), which would redact every file.
func assetPathTail(path, logicalPath string) string {
	segs := strings.Split(filepath.ToSlash(path), "/")
	n := len(strings.Split(filepath.ToSlash(logicalPath), "/")) + 1
	if n < len(segs) {
		segs = segs[len(segs)-n:]
	}
	return strings.Join(segs, "/")
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

	info, statErr := lstatPinned(path)
	if statErr != nil {
		if os.IsNotExist(statErr) && optional {
			asset.Missing = true
			return asset, fmt.Sprintf("optional asset missing (root=%s, path=%s)", logicalRoot, safeLogicalPath), nil
		}
		return SetupAsset{}, "", fmt.Errorf("reading asset metadata %q: %w", path, statErr)
	}
	asset.SizeBytes = info.Size()
	asset.Mode = uint32(info.Mode().Perm())
	if info.Mode()&os.ModeSymlink != 0 {
		return SetupAsset{}, "", fmt.Errorf("asset %q must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return SetupAsset{}, "", fmt.Errorf("asset %q is not a regular file", path)
	}
	if asset.SizeBytes > maxSetupAssetBytes {
		return asset, fmt.Sprintf("asset %q exceeds %d bytes; exporting metadata only", path, maxSetupAssetBytes), nil
	}
	// A hard link has another name that was never checked: notes.md can be
	// .netrc. Its content is treated as secret.
	hardLinked := agent.HardLinkCount(path, info) > 1
	if hardLinked {
		sensitive = true
		asset.Sensitive = true
	}

	if afterAssetStat != nil {
		afterAssetStat(path)
	}
	data, changed, err := readStattedFile(path, info, maxSetupAssetBytes)
	if err != nil {
		return SetupAsset{}, "", fmt.Errorf("reading asset %q: %w", path, err)
	}
	if changed != "" {
		// Swapped or grown since the checks above: what those checks judged
		// is not what would be read.
		asset.Redacted = true
		return asset, fmt.Sprintf("asset %q %s; not exported", path, changed), nil
	}
	asset.SizeBytes = int64(len(data))
	if hardLinked && !includeSecrets {
		asset.Redacted = true
		return asset, fmt.Sprintf("asset %q has a second hard link; redacted, use --include-secrets to carry", path), nil
	}
	// A credential by name or location (.aws/, .docker/config.json) is
	// redacted whole: its secret may sit under a key no pattern names.
	if sensitive && !includeSecrets && strings.EqualFold(filepath.Ext(path), ".json") &&
		agent.SecretBearingReason(safeLogicalPath) == "" && agent.SecretBearingReason(assetPathTail(path, safeLogicalPath)) == "" {
		if out, pointers, ok := redactJSONSecrets(data); ok {
			asset.Content = out
			asset.ContentPresent = true
			asset.RedactedFields = pointers
			asset.SizeBytes = int64(len(out))
			asset.SHA256 = hashSetupAssetContent(out)
			if len(pointers) == 0 {
				return asset, "", nil
			}
			return asset, fmt.Sprintf("blanked %d secret value(s) in %q (use --include-secrets to carry them)", len(pointers), path), nil
		}
	}
	if sensitive && !includeSecrets {
		asset.Redacted = true
		return asset, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", path), nil
	}
	asset.SHA256 = hashSetupAssetContent(data)
	asset.ContentPresent = true
	asset.Content = data
	return asset, "", nil
}

// lstatPinned is os.Lstat with the file's identity read now. On Windows
// os.SameFile looks a FileInfo's identity up by path on first use, so a
// file swapped after Lstat would compare equal to its replacement.
func lstatPinned(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err == nil {
		_ = os.SameFile(info, info)
	}
	return info, err
}

// afterAssetStat is a test seam: it runs between the checks on a file and
// the read of it.
var afterAssetStat func(path string)

// readStattedFile reads path through one open descriptor, and only when that
// descriptor is still the regular file info described, read to at most max
// bytes. changed says why not, when the file was swapped or grew past max
// after info was taken. The open does not block on a pipe swapped in.
func readStattedFile(path string, info os.FileInfo, max int64) ([]byte, string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	got, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !got.Mode().IsRegular() || !os.SameFile(info, got) {
		return nil, "changed after it was checked", nil
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > max {
		return nil, fmt.Sprintf("grew past %d bytes while read", max), nil
	}
	return data, "", nil
}

// readExistingFile reads a file already on this machine that import merges
// with. Anything but a regular file is an error, so a pipe never blocks it.
func readExistingFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, reason, err := readStattedFile(path, info, maxSetupAssetBytes)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, fmt.Errorf("%s %s", path, reason)
	}
	return data, nil
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
	if len(assets) == 0 {
		return 0, nil, nil
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
		if err := fsname.WriteFile(stageRoot, stagePath, asset.Content, 0700, 0600); err != nil {
			return staged, warnings, fmt.Errorf("writing staged asset %q: %w", stagePath, err)
		}
		// WriteFile leaves an existing file's mode alone, and the umask can
		// narrow a new one; set it so applying from the stage keeps it.
		stagePerm := os.FileMode(0600)
		if execAllowed(asset) {
			stagePerm = assetPerm(asset, stagePerm)
		}
		if err := os.Chmod(stagePath, stagePerm); err != nil {
			return staged, warnings, fmt.Errorf("setting mode of staged asset %q: %w", stagePath, err)
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

// applyStagedProjectAssets writes the staged project files into targetDir,
// through projectAssetWritable. With overwrite false an existing file that
// differs is kept and reported.
func applyStagedProjectAssets(configDir string, targetDir string) (int, error) {
	applied, _, err := applyStagedProjectAssetsWith(configDir, targetDir, true)
	return applied, err
}

func applyStagedProjectAssetsWith(configDir string, targetDir string, overwrite bool) (int, []string, error) {
	return applyStagedProjectAssetsCtx(configDir, targetDir, overwrite, nil)
}

// applyStagedProjectAssetsCtx is applyStagedProjectAssetsWith with conflicts
// settled through ctx, which backs up each file it replaces. With a nil ctx,
// overwrite decides and nothing is backed up.
func applyStagedProjectAssetsCtx(configDir string, targetDir string, overwrite bool, ctx *importApplyContext) (int, []string, error) {
	projectRoot := filepath.Join(configDir, setupImportedAssetsDirName, "project")
	info, err := os.Stat(projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("reading staged project assets: %w", err)
	}
	if !info.IsDir() {
		return 0, nil, nil
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
		return 0, nil, fmt.Errorf("applying staged project assets: %w", err)
	}

	applied := 0
	var skipped []string
	for _, rel := range relPaths {
		if reason := projectAssetRefusal(rel); reason != "" {
			skipped = append(skipped, fmt.Sprintf("%s: %s", rel, reason))
			continue
		}
		srcPath, err := safeJoinUnder(projectRoot, rel)
		if err != nil {
			return applied, skipped, fmt.Errorf("resolving staged asset path: %w", err)
		}
		destPath, err := safeJoinUnder(targetDir, rel)
		if err != nil {
			return applied, skipped, fmt.Errorf("resolving target asset path: %w", err)
		}
		// Before the backup and compare below read it: a short name would
		// read, and back up, the long-named file it aliases.
		if err := fsname.CheckBeforeWrite(targetDir, destPath); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if info, err := os.Lstat(destPath); err == nil && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			skipped = append(skipped, fmt.Sprintf("%s: exists and is not a regular file; kept", rel))
			continue
		}
		same, exists := sameFileContent(srcPath, destPath)
		if !same && (ctx == nil || !ctx.KeepBundleCommands) {
			if data, err := os.ReadFile(srcPath); err == nil && proseRunsCommands(rel, data) {
				skipped = append(skipped, fmt.Sprintf("%s: runs commands (hooks, tools or shell lines) that the bundle adds or changes; left out (use --keep-bundle-commands to take it)", rel))
				continue
			}
		}
		if exists && !same {
			if ctx == nil && !overwrite {
				skipped = append(skipped, fmt.Sprintf("%s: exists and differs; kept (use --strategy replace to overwrite)", rel))
				continue
			}
			if ctx != nil {
				replace, err := ctx.decide(destPath)
				if err != nil {
					return applied, skipped, err
				}
				if !replace {
					skipped = append(skipped, fmt.Sprintf("%s: exists and differs; kept (use --on-conflict backup-replace to replace it)", rel))
					continue
				}
				if _, err := ctx.backup(destPath); err != nil {
					skipped = append(skipped, fmt.Sprintf("%s: exists and differs; kept: %v", rel, err))
					continue
				}
			}
		}
		if err := copyRegularFile(srcPath, targetDir, destPath, 0644); err != nil {
			return applied, skipped, fmt.Errorf("copying staged project asset %q: %w", rel, err)
		}
		applied++
	}
	return applied, skipped, nil
}

// keepInterpreterAllows is set by import --keep-interpreter-allows.
var keepInterpreterAllows bool

// keepBundleCommands is set by import --keep-bundle-commands.
var keepBundleCommands bool

func applyProviderAssetsToSystem(homeDir string, assets []SetupAsset) (int, []string, error) {
	ctx := newImportApplyContext()
	ctx.KeepBundleCommands = keepBundleCommands
	return applyProviderAssetsWith(homeDir, assets, ctx)
}

// applyProviderAssetsWith writes provider files into homeDir. Against the
// file already there it keeps out commands the bundle adds or changes,
// restores secrets, keeps local-only keys of a JSON file, and settles a
// conflict through ctx, backing up what it replaces.
func applyProviderAssetsWith(homeDir string, assets []SetupAsset, ctx *importApplyContext) (int, []string, error) {
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
		if err := ensureNoSymlinkBelow(homeDir, filepath.Dir(dest)); err != nil {
			warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", describeSetupAsset(asset), err))
			continue
		}
		if err := fsname.CheckBeforeWrite(homeDir, dest); err != nil {
			warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", describeSetupAsset(asset), err))
			continue
		}
		if !ctx.DryRun {
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return applied, warnings, fmt.Errorf("creating provider asset directory: %w", err)
			}
		}
		if info, err := os.Lstat(dest); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: destination path is a symlink", describeSetupAsset(asset)))
				continue
			}
			// A pipe there would block the reads below that merge with it.
			if !info.Mode().IsRegular() {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: destination is not a regular file", describeSetupAsset(asset)))
				continue
			}
		} else if !os.IsNotExist(err) {
			return applied, warnings, fmt.Errorf("checking provider asset destination %q: %w", dest, err)
		}
		content := asset.Content
		perm := os.FileMode(0600)
		copilotConfig := asset.LogicalRoot == setupAssetRootProviderCopilotCLI && strings.EqualFold(strings.TrimRight(asset.LogicalPath, ". "), "config.json")
		if copilotConfig {
			// Trailing dots and spaces too: Windows writes "config.json." to config.json.
			// A bundle is not trusted to replace this machine's sign-in.
			stripped, _, err := stripCopilotLoginState(content)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", describeSetupAsset(asset), err))
				continue
			}
			content = stripped
		}
		mergeKeys := asset.MergeKeys
		if asset.LogicalRoot == setupAssetRootProviderHome {
			// Never executable from the home root, and the keys merged into
			// ~/.claude.json are fixed here, not taken from the bundle.
			mergeKeys = nil
			if asset.LogicalPath == ".claude.json" {
				mergeKeys = claudeUserConfigMergeKeys
			}
		}
		original, _ := ctx.original(dest)
		current, _ := readExistingFile(dest)
		desc := describeSetupAsset(asset)
		if !ctx.KeepBundleCommands && (scriptAsset(asset) || proseRunsCommands(asset.LogicalPath, content)) && !bytes.Equal(original, content) {
			// A kept hook command would run whatever script is here.
			warnings = append(warnings, fmt.Sprintf("provider asset %s: left out script (or hooks, tools or shell lines), which the bundle adds or changes; kept this machine's file (use --keep-bundle-commands to take it)", desc))
			continue
		}
		if !ctx.KeepBundleCommands && asset.LogicalRoot == setupAssetRootProviderGemini &&
			strings.EqualFold(strings.TrimRight(asset.LogicalPath, ". "), "trustedFolders.json") && !sameFileBytes(original, content) {
			// Trust lets Gemini act in a folder without asking.
			warnings = append(warnings, fmt.Sprintf("provider asset %s: not applied, the bundle adds or changes trusted folders; kept this machine's file (use --keep-bundle-commands to take it)", desc))
			continue
		}
		codexConfig := asset.LogicalRoot == setupAssetRootProviderCodex && strings.EqualFold(strings.TrimRight(asset.LogicalPath, ". "), "config.toml")
		var mcpRep mcpMergeReport
		codexServersOnly := false
		codexMerged := false
		if codexConfig {
			m, err := ctx.mergeCodexConfig(dest, content, original)
			if err != nil {
				return applied, warnings, err
			}
			if !m.OK && m.Report.changed() {
				names := append(append([]string(nil), m.Report.Added...), m.Report.Replaced...)
				for i, n := range names {
					names[i] = strconv.Quote(n)
				}
				ctx.MCPReport = append(ctx.MCPReport, printable(dest)+": not applied per server, could not edit the [mcp_servers] tables (unusual layout); the whole-file rule decides: "+strings.Join(names, ", "))
			}
			if m.OK {
				content, mcpRep, codexServersOnly, codexMerged = m.Content, m.Report, m.LocalBase, true
				if m.RestDropped {
					warnings = append(warnings, fmt.Sprintf("provider asset %s: left out the bundle's other settings, which add or change notify, model_providers or another command; kept this machine's apart from the MCP servers (use --keep-bundle-commands to take them)", desc))
				}
			}
		}
		if !ctx.KeepBundleCommands {
			if codexConfig {
				if !codexMerged && codexCommandsChanged(content, original) {
					warnings = append(warnings, fmt.Sprintf("provider asset %s: not applied, it adds or changes mcp_servers, notify, model_providers or another command, or does not parse; kept this machine's file (use --keep-bundle-commands to take it)", desc))
					continue
				}
			} else {
				if jsonSettingsAsset(asset) && !bytes.Equal(original, content) {
					if _, ok := decodeSettingsObject(content); !ok {
						// Comments or trailing data: it cannot be checked key
						// by key, and the tool may still read it (Gemini
						// strips comments).
						warnings = append(warnings, fmt.Sprintf("provider asset %s: not applied, it does not parse as a plain JSON object, so its commands cannot be checked; kept this machine's file (use --keep-bundle-commands to take it)", desc))
						continue
					}
					// This machine's file with comments: its servers and keys
					// cannot be compared or kept, so they would be lost.
					if _, ok := decodeSettingsObject(original); !ok && len(bytes.TrimSpace(original)) > 0 {
						warnings = append(warnings, fmt.Sprintf("provider asset %s: not applied, this machine's %s does not parse as a plain JSON object (comments?), so it cannot be merged; kept it", desc, dest))
						continue
					}
				}
				var consent map[string]map[string]bool
				var err error
				content, consent, mcpRep, err = ctx.mergeMCPKeysJSON(dest, content, original, mergeKeys)
				if err != nil {
					return applied, warnings, err
				}
				var dropped []string
				content, dropped = dropBundleCommandsExcept(content, original, consent)
				for _, d := range dropped {
					warnings = append(warnings, fmt.Sprintf("provider asset %s: left out command %s, which the bundle adds or changes; kept this machine's value (use --keep-bundle-commands to take it)", desc, d))
				}
			}
		} else if !codexConfig && jsonSettingsAsset(asset) {
			if _, ok := decodeSettingsObject(original); ok || len(bytes.TrimSpace(original)) == 0 {
				var err error
				content, _, mcpRep, err = ctx.mergeMCPKeysJSON(dest, content, original, mergeKeys)
				if err != nil {
					return applied, warnings, err
				}
			}
		}
		var removed, secrets []string
		declined := 0
		if len(asset.RedactedFields) > 0 {
			res, err := restoreRedactedFieldsWith(content, asset.RedactedFields, original, ctx.restoreAsker(dest))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", desc, err))
				continue
			}
			secretCount := countSecretPointers(content, asset.RedactedFields)
			content = res.Content
			removed = res.Removed
			secrets = res.Secrets
			declined = res.Declined
			if left := secretCount - res.Restored - res.Declined; left > 0 {
				warnings = append(warnings, fmt.Sprintf("provider asset %s: %d secret value(s) were not in the bundle and are not on this machine; set them by hand", desc, left))
			}
		}
		if copilotConfig {
			// After the restore: a redacted pointer naming a login key must
			// not remove this machine's sign-in.
			content = keepLocalCopilotLogin(dest, content)
		}
		// Trailing dots and spaces too: Windows writes "settings.json." to settings.json.
		claudeName := strings.TrimRight(asset.LogicalPath, ". ")
		claudeSettings := asset.LogicalRoot == setupAssetRootProviderClaude &&
			(strings.EqualFold(claudeName, "settings.json") || strings.EqualFold(claudeName, "settings.local.json"))
		if claudeSettings && !keepInterpreterAllows {
			var dropped []string
			content, dropped = dropInterpreterAllows(content)
			for _, d := range dropped {
				warnings = append(warnings, fmt.Sprintf("provider asset %s: left out permission %q, which lets any program run without asking; add it back on this machine if you want it", desc, d))
			}
		}
		if len(mergeKeys) > 0 {
			merged, err := mergeJSONKeysIntoFile(dest, content, mergeKeys)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("provider asset %s cannot be applied: %v", desc, err))
				continue
			}
			// This machine's key and server order; new ones in the bundle's.
			content = orderLikeLocal(merged, current, asset.Content)
		} else if len(current) > 0 {
			// The local file's own keys stay; the bundle's overwrite.
			merged, n, ok := unionWithLocal(content, current, secrets, removed, ctx.restoreAsker(dest))
			if ok {
				content = orderLikeLocal(merged, current, asset.Content)
				declined += n
			}
		}
		if claudeSettings {
			ctx.Broad = append(ctx.Broad, broadAllows(content)...)
		}
		_, statErr := os.Lstat(dest)
		exists := statErr == nil
		if ctx.DryRun {
			if line := formatMCPReport(dest, mcpRep, true); line != "" {
				ctx.MCPReport = append(ctx.MCPReport, line)
			}
			switch {
			case !exists:
				warnings = append(warnings, fmt.Sprintf("provider asset %s: would write %s", desc, dest))
			case sameFileBytes(current, content):
			default:
				warnings = append(warnings, fmt.Sprintf("provider asset %s: %s", desc, ctx.preview(dest)))
			}
			continue
		}
		// Equal JSON in another layout: keep this machine's bytes.
		if exists && sameFileBytes(current, content) {
			content = current
		}
		// Only servers the user already settled differ: no second question.
		serversOnly := mcpRep.changed() && (codexServersOnly || (!codexConfig && sameApartFromMCP(current, content)))
		if exists && !sameFileBytes(current, content) {
			replace := true
			if !serversOnly {
				var err error
				replace, err = ctx.decide(dest)
				if err != nil {
					return applied, warnings, err
				}
			}
			if !replace {
				warnings = append(warnings, fmt.Sprintf("provider asset %s: kept this machine's %s, which differs from the bundle", desc, dest))
				if !mcpRep.empty() {
					ctx.MCPReport = append(ctx.MCPReport, printable(dest)+": file kept, no MCP server changed")
				}
				continue
			}
			if _, err := ctx.backup(dest); err != nil {
				warnings = append(warnings, fmt.Sprintf("provider asset %s: kept this machine's file: %v", desc, err))
				continue
			}
		}
		if declined > 0 {
			where := "the file on this machine"
			if p, ok := ctx.backedUp[dest]; ok {
				where = p
			}
			warnings = append(warnings, fmt.Sprintf("provider asset %s: %d local secret(s) not restored because the definition holding them changed; the old values are in %s", desc, declined, where))
		}
		if execAllowed(asset) {
			perm = assetPerm(asset, perm)
		}
		if err := writeRegularFileAtomic(homeDir, dest, content, perm); err != nil {
			return applied, warnings, fmt.Errorf("writing provider asset %q: %w", dest, err)
		}
		if line := formatMCPReport(dest, mcpRep, false); line != "" {
			ctx.MCPReport = append(ctx.MCPReport, line)
		}
		applied++
	}
	return applied, warnings, nil
}

// jsonSettingsAsset is a provider file read as JSON settings.
func jsonSettingsAsset(asset SetupAsset) bool {
	p := strings.ToLower(strings.TrimRight(asset.LogicalPath, ". "))
	ext := filepath.Ext(p)
	return ext == ".json" || ext == ".jsonc"
}

func providerAssetDestination(homeDir string, asset SetupAsset) (string, error) {
	dir, ok := providerRootDirs[asset.LogicalRoot]
	if !ok {
		return "", fmt.Errorf("unsupported provider asset root %q", asset.LogicalRoot)
	}
	// The path the bundle gives must already be the clean form, so checks
	// made on it hold for the file written: "x/../auth.json" is auth.json.
	rel, err := sanitizeAssetRelativePath(asset.LogicalPath)
	if err != nil {
		return "", err
	}
	if rel != asset.LogicalPath {
		return "", fmt.Errorf("%q: not in clean form (%q); refused", asset.LogicalPath, rel)
	}
	// Export never carries login tokens; a bundle that does is not trusted
	// to replace the ones here.
	if isCredentialFile(rel) {
		return "", fmt.Errorf("%s: a login token file; sign in on this machine instead", rel)
	}
	if asset.LogicalRoot == setupAssetRootProviderHome {
		rel, err := homeAssetWritable(rel)
		if err != nil {
			return "", err
		}
		return safeJoinUnder(homeDir, rel)
	}
	return safeJoinUnder(filepath.Join(homeDir, dir), rel)
}

func providerStageDirectoryName(logicalRoot string) (string, bool) {
	_, ok := providerRootDirs[logicalRoot]
	return logicalRoot, ok
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
	return sanitizeAssetRelativePathFor(runtime.GOOS, path)
}

// sanitizeAssetRelativePathFor applies the rules of goos. Device names are
// refused on Windows only, where opening aux.md opens a device. A short name
// (~N) is a plain name elsewhere; on Windows the writer checks the name it
// actually opened (see fsname.Check).
func sanitizeAssetRelativePathFor(goos string, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty relative path")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("control character in path %q", path)
		}
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
	if goos == "windows" && hasWindowsDeviceName(cleaned) {
		return "", fmt.Errorf("Windows device name is not allowed: %q", path)
	}
	return filepath.ToSlash(cleaned), nil
}

// hasWindowsDeviceName is whether any segment of rel is a Windows device name.
func hasWindowsDeviceName(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if fsname.IsWindowsDeviceName(seg) {
			return true
		}
	}
	return false
}

// windowsNameWarnings names each carried path that Windows cannot write
// (aux.md opens a device there). Export keeps them; import on Windows
// refuses them.
func windowsNameWarnings(c setupAssetCollection) []string {
	var out []string
	for _, list := range [][]SetupAsset{c.ProviderFiles, c.ProjectFiles, c.InstructionOverrides, c.SkillAssets} {
		for _, a := range list {
			p := a.ProjectRelativePath
			if p == "" {
				p = a.LogicalPath
			}
			if hasWindowsDeviceName(p) {
				out = append(out, fmt.Sprintf("%s: a Windows device name; import on Windows will refuse it", describeSetupAsset(a)))
			}
		}
	}
	return out
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

// beforeFinalRename is a test hook run between the name checks and the
// rename that publishes the file.
var beforeFinalRename func(destPath string)

// finalRename moves tmpPath to destPath. On Windows a destination that was
// absent at the check is created only, never replaced: an 8.3 alias that
// appeared since (SETTIN~1.JSO for settings.json) makes the rename fail
// instead of overwriting the long-named file. A destination that existed
// was checked to be its own long name, so replacing it is safe.
func finalRename(tmpPath, destPath string, existed bool) error {
	if beforeFinalRename != nil {
		beforeFinalRename(destPath)
	}
	if runtime.GOOS == "windows" && !existed {
		return fsname.RenameNoReplace(tmpPath, destPath)
	}
	return replaceRegularFile(tmpPath, destPath)
}

// writeRegularFileAtomic writes destPath, refusing symlinks in the path below
// root. root and its ancestors are trusted (macOS /var -> /private/var).
func writeRegularFileAtomic(root string, destPath string, content []byte, perm os.FileMode) error {
	destDir := filepath.Dir(destPath)
	if err := ensureNoSymlinkBelow(root, destDir); err != nil {
		return err
	}
	// Seen before the name check, so a file that appears after it (an 8.3
	// alias) is never replaced: see finalRename.
	_, statErr := os.Lstat(destPath)
	existed := statErr == nil
	if err := fsname.CheckBeforeWrite(root, destPath); err != nil {
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
	if err := finalRename(tmpPath, destPath, existed); err != nil {
		return err
	}
	tmpPath = ""
	return fsname.CheckAfterWrite(root, destPath)
}

func copyRegularFile(srcPath string, root string, destPath string, perm os.FileMode) error {
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
	if err := ensureNoSymlinkBelow(root, destDir); err != nil {
		return err
	}
	// Seen before the name check, so a file that appears after it (an 8.3
	// alias) is never replaced: see finalRename.
	_, statErr := os.Lstat(destPath)
	existed := statErr == nil
	if err := fsname.CheckBeforeWrite(root, destPath); err != nil {
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
	if err := finalRename(tmpPath, destPath, existed); err != nil {
		return err
	}
	tmpPath = ""
	return fsname.CheckAfterWrite(root, destPath)
}
