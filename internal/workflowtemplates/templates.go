package workflowtemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/config"
)

const (
	DefaultSchemaVersion = "1"
	TemplatesDirName     = "templates"
	metadataFileName     = "metadata.json"
	RepoLocalVersion     = "repo-local"
)

// TemplateAsset stores one workflow template body with metadata.
type TemplateAsset struct {
	Key       string    `json:"key"`
	Filename  string    `json:"filename"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Content   string    `json:"content"`
}

// Bundle is embedded in setup export/import payloads.
type Bundle struct {
	SchemaVersion string          `json:"schema_version"`
	ExportedAt    time.Time       `json:"exported_at"`
	Assets        []TemplateAsset `json:"assets"`
}

// ResolvedTemplate describes one effective template with its source.
type ResolvedTemplate struct {
	TemplateAsset
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

type metadataFile struct {
	SchemaVersion string               `json:"schema_version"`
	UpdatedAt     time.Time            `json:"updated_at,omitempty"`
	Versions      map[string]string    `json:"versions,omitempty"`
	Updated       map[string]time.Time `json:"updated,omitempty"`
	Filenames     map[string]string    `json:"filenames,omitempty"`
}

const builtinImplementIssueTemplate = `Implement Issue Template

Metadata:
- Template Version: 2.0
- Template Type: Issue Implementation Workflow
- Mode: Modular and Extendable

Inputs:
- Repository path
- Issue reference (number/url/title)
- Base branch (default: main or master, override allowed)
- Release strategy (major/minor/patch, or auto)
- Optional policy overrides

Config Defaults (Override Per Team/Repo):
- DEFAULT_BASE_BRANCH=main|master
- DEFAULT_RELEASE_PREFIX=release/
- DEFAULT_CHANGE_DOCS=true
- DEFAULT_RUN_TESTS=true
- DEFAULT_OPEN_PR=true

Core Workflow (Default Module Set):
1. Intake Module:
- Read issue title and description.
- Confirm scope and acceptance criteria from the issue.

2. Release Module:
- Classify change as major/minor/patch.
- Create ` + "`release/X.Y.Z`" + ` from configured base branch using next semver.
- Extension: teams can swap this with custom branching policy.

3. Implementation Module:
- Modify codebase according to issue scope.
- Add concise code comments where important logic changes are introduced.
- Create/update files as needed.

4. Documentation Module:
- Update docs/CHANGELOG/README where needed.
- Keep comments concise and clean; avoid unnecessary special characters.
- Extension: teams can define doc update policy per repo.

5. Validation Module:
- Run project test/build/lint workflow before commit.
- Extension: team-specific quality gates can be inserted here.

6. Delivery Module:
- Commit with appropriate message.
- Push branch.
- Open PR referencing resolved issue.
- If PR creation fails, output PR create URL.

Output Contract:
- Branch name created
- Files changed summary
- Tests/build/lint results
- Commit hash
- PR URL (or fallback create URL)

Extension Points:
- Pre-Intake Hook: custom issue triage rules
- Pre-Commit Hook: formatting, security scans, policy checks
- Post-Push Hook: reviewer assignment, label automation, CI checks
- If Copilot is requested as reviewer, request review-only and do not ask Copilot to implement/fix code via PR comments.
- Post-PR Hook: comment templating and release note generation

Customization Notes:
- Repositories can override any ` + "`Config Defaults`" + ` value.
- Teams can add/remove modules, but should preserve ordered checkpoints.
- Keep this template as a base profile and create derived profiles per repo as needed.

Profile Example:
- Profile Name: legacy-master-flow
- DEFAULT_BASE_BRANCH=master
- DEFAULT_RELEASE_PREFIX=release/
- DEFAULT_CHANGE_DOCS=true
- DEFAULT_RUN_TESTS=true
- DEFAULT_OPEN_PR=true
- Extra Hook: Pre-Commit Hook runs ` + "`make lint && make test`" + ` before commit.
`

const builtinImplementPRTemplate = `Fix PR Template

Metadata:
- Template Version: 2.0
- Template Type: PR Review Remediation Workflow
- Mode: Modular and Extendable

Inputs:
- Repository path
- PR reference (number/url)
- Base branch context (main or master, inferred or configured)
- Target branch name
- Optional reviewer re-request policy
- Optional policy overrides

Config Defaults (Override Per Team/Repo):
- DEFAULT_UPDATE_DOCS_IF_NEEDED=true
- DEFAULT_RUN_TESTS=true
- DEFAULT_RESOLVE_THREADS=true
- DEFAULT_REREQUEST_REVIEW=true

Core Workflow (Default Module Set):
1. Review Intake Module:
- Read all PR review issues and conversations.
- Build a fix checklist grouped by thread/comment.

2. Remediation Module:
- Modify code for all confirmed review issues.
- Add concise comments where important changes are introduced.
- Create/update files as needed.

3. Documentation Module:
- Update docs/CHANGELOG/README only when required by the code changes.
- Keep comments concise and clean; avoid unnecessary special characters.

4. Validation Module:
- Run project test/build/lint workflow before commit.
- Extension: allow repo-specific CI dry-run or smoke checks.

5. Delivery Module:
- Commit with appropriate message.
- Push code changes.

6. PR Hygiene Module:
- Resolve all fixed conversations/threads.
- Re-request review from same reviewer when configured.
- Re-request review from Copilot on the processed PR after fixes are pushed, but as review-only.
- If Copilot was not reviewing the PR previously, explicitly request a new Copilot review-only request.
- Do not ask Copilot to implement/fix code; do not post prompt-like comments that can trigger code-writing behavior.
- Prefer GitHub reviewer re-request APIs/UI over chat/comment triggers.
- If re-request fails, report which PR requires re-request and provide PR URL.

Output Contract:
- Addressed thread list
- Files changed summary
- Tests/build/lint results
- Commit hash
- Updated PR URL
- Re-review status

Extension Points:
- Pre-Remediation Hook: map comments to owners/components
- Pre-Commit Hook: enforce style/security/release policies
- Post-Push Hook: auto-resolve eligible threads
- Post-Hygiene Hook: auto-comment with fix summary

Customization Notes:
- Teams can change module order if dependencies are preserved.
- Config defaults can be overridden by repo profile.
- Keep a shared default and add derived templates per workflow maturity.

Profile Example:
- Profile Name: strict-main-flow
- BASE_BRANCH=main
- DEFAULT_UPDATE_DOCS_IF_NEEDED=true
- DEFAULT_RUN_TESTS=true
- DEFAULT_RESOLVE_THREADS=true
- DEFAULT_REREQUEST_REVIEW=true
- Extra Hook: Post-Push Hook posts a checklist summary comment on the PR.
`

var builtinAddIssueTemplate = `Add TODO Item Template Instructions

Metadata:
- Template Version: 2.1
- Template Type: TODO Entry Authoring Workflow
- Mode: Modular and Extendable
- Compatible Format: git-lantern TODO structure

Purpose:
- Generate one or more TODO entries using a deterministic, append-only process.
- Produce output compatible with TODO files that use [TODO] ... [/TODO] markers.

Input Contract:
- Target repository path
- Target TODO file path (default: <repo>/TODO.txt)
- Number of items to create
- For each item:
  - Title
  - Description
  - Intent
  - Implementation Details (required)
  - Acceptance Criteria list (required)

Config Defaults (Override Per Team/Repo):
- DEFAULT_TODO_FILENAME=TODO.txt
- DEFAULT_ID_WIDTH=3
- DEFAULT_APPEND_BEFORE_CLOSING_MARKER=true
- DEFAULT_REQUIRE_IMPLEMENTATION_DETAILS=true
- DEFAULT_REQUIRE_ACCEPTANCE_CRITERIA=true

Core Workflow (Default Module Set):
1. Discovery Module:
- Read target TODO file.
- Validate markers [TODO] and [/TODO].
- Detect highest existing ID.

2. Allocation Module:
- Allocate sequential IDs for all new items.
- Preserve existing IDs without renumbering.

3. Composition Module:
- Build each entry in canonical field order:
  1. ID
  2. Title
  3. Description
  4. Intent
  5. Implementation Details
  6. Acceptance Criteria

4. Validation Module:
- Reject missing required fields.
- Enforce single blank line between items.
- Enforce - bullets for Implementation Details and Acceptance Criteria.
- Reject malformed insertion if markers are missing.

5. Write Module:
- Insert new items before [/TODO].
- Keep all existing content unchanged outside insertion block.

Output Contract:
- Updated TODO file path
- IDs assigned
- Inserted item count
- Validation results
- Append-only TODO entries in canonical field order

Fill-In Entry Template (Canonical):

ID: <NEXT_ID_3_DIGITS>
Title: <TITLE>
Description: <DESCRIPTION>
Intent: <INTENT>
Implementation Details:
- <DETAIL_1>
- <DETAIL_2>
Acceptance Criteria:
- <CRITERION_1>
- <CRITERION_2>

Reusable Implementation Template Bodies:
- implement_issue.txt embedded checklist:
` + "\n\n" + builtinImplementIssueTemplate + "\n\n" + `- implement_pr.txt embedded checklist:
` + "\n\n" + builtinImplementPRTemplate + "\n\n" + `Usage Notes For Reusable Modules:
- When the TODO item is issue-implementation focused, embed the full implement_issue.txt checklist body into the item's Implementation Details section.
- When the TODO item is PR-remediation focused, embed the full implement_pr.txt checklist body into the item's Implementation Details section.
- For multi-item runs, apply deterministic sequential IDs and keep each item self-contained.

Extension Points:
- Pre-Validation Hook: apply custom schema checks per repository.
- Pre-Write Hook: enforce label/tag conventions in titles.
- Post-Write Hook: auto-open related issue/PR links.

Customization Notes:
- Keep this file as base profile and create team-specific variants.
- Override only config defaults and hook logic when possible.
- Preserve canonical output field order for compatibility.

Profile Example:
- Profile Name: multi-repo-standard
- DEFAULT_TODO_FILENAME=TODO.txt
- DEFAULT_ID_WIDTH=3
- DEFAULT_APPEND_BEFORE_CLOSING_MARKER=true
- DEFAULT_REQUIRE_IMPLEMENTATION_DETAILS=true
- DEFAULT_REQUIRE_ACCEPTANCE_CRITERIA=true
- Repo Policy Note: apply same template for repositories using either main or master base branch naming.
`

var defaultSpecs = []TemplateAsset{
	{
		Key:      "implement_issue",
		Filename: "implement_issue.txt",
		Version:  "builtin-2.0",
		Content:  builtinImplementIssueTemplate,
	},
	{
		Key:      "implement_pr",
		Filename: "implement_pr.txt",
		Version:  "builtin-2.0",
		Content:  builtinImplementPRTemplate,
	},
	{
		Key:      "add_issue",
		Filename: "add_issue.txt",
		Version:  "builtin-2.1",
		Content:  builtinAddIssueTemplate,
	},
}

// SupportedKeys returns sorted known template keys.
func SupportedKeys() []string {
	keys := make([]string, 0, len(defaultSpecs))
	for _, spec := range defaultSpecs {
		keys = append(keys, spec.Key)
	}
	sort.Strings(keys)
	return keys
}

// FindTemplateFilename resolves a key or filename into the canonical filename.
func FindTemplateFilename(name string) (string, bool) {
	norm := normalizeTemplateName(name)
	for _, spec := range defaultSpecs {
		if norm == spec.Key || norm == normalizeTemplateName(spec.Filename) {
			return spec.Filename, true
		}
	}
	return "", false
}

// FindTemplateKey resolves a key or filename into the canonical key.
func FindTemplateKey(name string) (string, bool) {
	norm := normalizeTemplateName(name)
	for _, spec := range defaultSpecs {
		if norm == spec.Key || norm == normalizeTemplateName(spec.Filename) {
			return spec.Key, true
		}
	}
	return "", false
}

// LoadResolved loads effective templates with precedence repo-local > config > built-in.
func LoadResolved(configDir string, repoDir string) ([]ResolvedTemplate, []string, error) {
	assets, warnings, err := loadConfigAssets(configDir)
	if err != nil {
		return nil, nil, err
	}
	byKey := make(map[string]TemplateAsset, len(assets))
	for _, a := range assets {
		byKey[a.Key] = a
	}

	resolved := make([]ResolvedTemplate, 0, len(defaultSpecs))
	for _, spec := range defaultSpecs {
		if repoDir != "" {
			repoPath := filepath.Join(repoDir, spec.Filename)
			if content, ok, warn := readTemplateFile(repoPath); ok {
				resolved = append(resolved, ResolvedTemplate{
					TemplateAsset: TemplateAsset{
						Key:       spec.Key,
						Filename:  spec.Filename,
						Version:   RepoLocalVersion,
						UpdatedAt: time.Time{},
						Content:   content,
					},
					Source: "repo-local",
					Path:   repoPath,
				})
				continue
			} else if warn != "" {
				warnings = append(warnings, warn)
			}
		}

		if asset, ok := byKey[spec.Key]; ok && strings.TrimSpace(asset.Content) != "" {
			resolved = append(resolved, ResolvedTemplate{
				TemplateAsset: asset,
				Source:        "config",
				Path:          filepath.Join(configTemplatesDir(configDir), asset.Filename),
			})
			continue
		}

		if !hasSpecificTemplateWarning(warnings, spec.Key, spec.Filename) {
			warnings = append(warnings, fmt.Sprintf("template %q missing from config storage; using built-in default", spec.Filename))
		}
		resolved = append(resolved, ResolvedTemplate{
			TemplateAsset: spec,
			Source:        "built-in",
		})
	}

	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Filename < resolved[j].Filename })
	return resolved, dedupeWarnings(warnings), nil
}

// ExportBundle creates setup-export payload from config storage and defaults.
func ExportBundle(configDir string) (Bundle, []string, error) {
	assets, warnings, err := loadConfigAssets(configDir)
	if err != nil {
		return Bundle{}, nil, err
	}
	if len(assets) == 0 {
		assets = cloneDefaults()
		warnings = append(warnings, "no workflow templates found in config storage; exporting built-in defaults")
	} else {
		byKey := make(map[string]TemplateAsset, len(assets))
		for _, asset := range assets {
			byKey[asset.Key] = asset
		}
		merged := make([]TemplateAsset, 0, len(defaultSpecs)+len(assets))
		for _, spec := range defaultSpecs {
			if asset, ok := byKey[spec.Key]; ok && strings.TrimSpace(asset.Content) != "" {
				merged = append(merged, asset)
				delete(byKey, spec.Key)
				continue
			}
			if !hasSpecificTemplateWarning(warnings, spec.Key, spec.Filename) {
				warnings = append(warnings, fmt.Sprintf("template %q missing from config storage; exporting built-in default", spec.Filename))
			}
			merged = append(merged, spec)
		}
		assets = merged
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Filename < assets[j].Filename })
	return Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets:        assets,
	}, dedupeWarnings(warnings), nil
}

// ImportBundle writes template assets into config storage and returns imported asset count.
func ImportBundle(configDir string, bundle Bundle) (int, []string, error) {
	if bundle.SchemaVersion != "" && bundle.SchemaVersion != DefaultSchemaVersion {
		return 0, nil, fmt.Errorf("unsupported template bundle schema version %q", bundle.SchemaVersion)
	}
	if len(bundle.Assets) == 0 {
		return 0, []string{"bundle did not include workflow templates; nothing imported"}, nil
	}
	if err := os.MkdirAll(configTemplatesDir(configDir), 0700); err != nil {
		return 0, nil, fmt.Errorf("creating templates directory: %w", err)
	}
	warnings := make([]string, 0)
	meta, metaErr := readMetadata(configDir)
	metadataNeedsRepair := metaErr != nil && !errors.Is(metaErr, os.ErrNotExist)
	if metaErr != nil {
		if !errors.Is(metaErr, os.ErrNotExist) {
			// Allow bundle import to repair corrupted metadata by resetting to defaults.
			warnings = append(warnings, fmt.Sprintf("existing template metadata invalid; resetting metadata defaults (%v)", metaErr))
		}
		meta = metadataFile{
			SchemaVersion: DefaultSchemaVersion,
		}
	}
	if meta.SchemaVersion == "" {
		meta.SchemaVersion = DefaultSchemaVersion
	}
	if meta.Versions == nil {
		meta.Versions = make(map[string]string)
	}
	if meta.Updated == nil {
		meta.Updated = make(map[string]time.Time)
	}
	if meta.Filenames == nil {
		meta.Filenames = make(map[string]string)
	}
	importedCount := 0
	seenKeys := make(map[string]struct{})
	seenFilenames := make(map[string]string)
	for _, asset := range bundle.Assets {
		asset.Key = normalizeTemplateName(asset.Key)
		if asset.Key == "" {
			warnings = append(warnings, "skipped template with empty key")
			continue
		}
		defaultSpec, supported := findDefaultByKey(asset.Key)
		if !supported {
			warnings = append(warnings, fmt.Sprintf("skipped unsupported template key %q", asset.Key))
			continue
		}
		if _, exists := seenKeys[asset.Key]; exists {
			return 0, nil, fmt.Errorf("duplicate template key %q in bundle", asset.Key)
		}
		seenKeys[asset.Key] = struct{}{}
		filename := asset.Filename
		if filename == "" {
			filename = defaultSpec.Filename
		}
		filename, err := sanitizeTemplateFilename(filename)
		if err != nil {
			return 0, nil, fmt.Errorf("invalid filename for template key %q: %w", asset.Key, err)
		}
		if existingKey, exists := seenFilenames[filename]; exists {
			return 0, nil, fmt.Errorf("duplicate template filename %q for keys %q and %q", filename, existingKey, asset.Key)
		}
		seenFilenames[filename] = asset.Key
		if strings.TrimSpace(asset.Content) == "" {
			warnings = append(warnings, fmt.Sprintf("skipped empty template %q", filename))
			continue
		}
		if err := os.WriteFile(filepath.Join(configTemplatesDir(configDir), filename), []byte(asset.Content), 0600); err != nil {
			return 0, nil, fmt.Errorf("writing template %s: %w", filename, err)
		}
		meta.Versions[asset.Key] = firstNonEmpty(asset.Version, "imported")
		meta.Updated[asset.Key] = nonZeroTime(asset.UpdatedAt, time.Now().UTC())
		meta.Filenames[asset.Key] = filename
		importedCount++
	}
	if importedCount == 0 && !metadataNeedsRepair {
		return 0, dedupeWarnings(warnings), nil
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := writeMetadata(configDir, meta); err != nil {
		return 0, nil, err
	}
	return importedCount, dedupeWarnings(warnings), nil
}

// RefreshConfigTemplates ensures config storage contains default templates and metadata.
func RefreshConfigTemplates(configDir string, force bool) ([]TemplateAsset, error) {
	templatesDir := configTemplatesDir(configDir)
	if err := os.MkdirAll(templatesDir, 0700); err != nil {
		return nil, fmt.Errorf("creating templates directory: %w", err)
	}
	now := time.Now().UTC()
	dirty := false
	meta, metaErr := readMetadata(configDir)
	if metaErr != nil {
		if !errors.Is(metaErr, os.ErrNotExist) {
			return nil, fmt.Errorf("reading template metadata: %w", metaErr)
		}
		meta = metadataFile{}
		dirty = true
	}
	if meta.SchemaVersion == "" {
		meta.SchemaVersion = DefaultSchemaVersion
		dirty = true
	}
	if meta.Versions == nil {
		meta.Versions = make(map[string]string)
		dirty = true
	}
	if meta.Updated == nil {
		meta.Updated = make(map[string]time.Time)
		dirty = true
	}
	if meta.Filenames == nil {
		meta.Filenames = make(map[string]string)
		dirty = true
	}

	written := make([]TemplateAsset, 0, len(defaultSpecs))
	for _, spec := range defaultSpecs {
		filename := spec.Filename
		if meta.Filenames != nil {
			if mfn := strings.TrimSpace(meta.Filenames[spec.Key]); mfn != "" {
				safeFilename, err := sanitizeTemplateFilename(mfn)
				if err == nil {
					filename = safeFilename
				}
			}
		}
		path := filepath.Join(templatesDir, filename)
		if !force {
			if content, ok, _ := readTemplateFile(path); ok && strings.TrimSpace(content) != "" {
				if _, ok := meta.Versions[spec.Key]; !ok {
					meta.Versions[spec.Key] = spec.Version
					dirty = true
				}
				if _, ok := meta.Filenames[spec.Key]; !ok {
					meta.Filenames[spec.Key] = filename
					dirty = true
				}
				if _, ok := meta.Updated[spec.Key]; !ok {
					meta.Updated[spec.Key] = now
					dirty = true
				}
				continue
			}
			if filename != spec.Filename {
				canonicalPath := filepath.Join(templatesDir, spec.Filename)
				if content, ok, _ := readTemplateFile(canonicalPath); ok && strings.TrimSpace(content) != "" {
					if _, ok := meta.Versions[spec.Key]; !ok {
						meta.Versions[spec.Key] = spec.Version
						dirty = true
					}
					if meta.Filenames[spec.Key] != spec.Filename {
						meta.Filenames[spec.Key] = spec.Filename
						dirty = true
					}
					if _, ok := meta.Updated[spec.Key]; !ok {
						meta.Updated[spec.Key] = now
						dirty = true
					}
					continue
				}
			}
		}
		if err := os.WriteFile(path, []byte(spec.Content), 0600); err != nil {
			return nil, fmt.Errorf("writing template %s: %w", filename, err)
		}
		meta.Versions[spec.Key] = spec.Version
		meta.Updated[spec.Key] = now
		meta.Filenames[spec.Key] = filename
		dirty = true
		written = append(written, TemplateAsset{
			Key:       spec.Key,
			Filename:  filename,
			Version:   spec.Version,
			UpdatedAt: now,
			Content:   spec.Content,
		})
	}
	if !dirty {
		return written, nil
	}
	meta.UpdatedAt = now
	if err := writeMetadata(configDir, meta); err != nil {
		return nil, err
	}
	return written, nil
}

func loadConfigAssets(configDir string) ([]TemplateAsset, []string, error) {
	meta, metaErr := readMetadata(configDir)
	warnings := make([]string, 0)
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		warnings = append(warnings, fmt.Sprintf("template metadata is invalid; using safe fallbacks (%v)", metaErr))
	}

	assets := make([]TemplateAsset, 0, len(defaultSpecs))
	seenFilenames := make(map[string]string, len(defaultSpecs))
	for _, spec := range defaultSpecs {
		filename := spec.Filename
		usedMetadataFilename := false
		if meta.Filenames != nil {
			if mfn := strings.TrimSpace(meta.Filenames[spec.Key]); mfn != "" {
				safeFilename, err := sanitizeTemplateFilename(mfn)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("ignoring unsafe metadata filename for %q: %v", spec.Key, err))
				} else {
					filename = safeFilename
					usedMetadataFilename = safeFilename != spec.Filename
				}
			}
		}
		selectedFilename := filename
		path := filepath.Join(configTemplatesDir(configDir), selectedFilename)
		content, ok, warn := readTemplateFile(path)
		if !ok {
			fallbackTarget := "built-in default template"
			if warn != "" {
				warnings = append(warnings, warn)
			}
			if usedMetadataFilename {
				canonicalPath := filepath.Join(configTemplatesDir(configDir), spec.Filename)
				canonicalContent, canonicalOK, canonicalWarn := readTemplateFile(canonicalPath)
				if canonicalOK {
					content = canonicalContent
					ok = true
					selectedFilename = spec.Filename
					fallbackTarget = fmt.Sprintf("canonical config template %q", spec.Filename)
				} else if canonicalWarn != "" {
					warnings = append(warnings, canonicalWarn)
				}
				if warn == "" {
					warnings = append(warnings, fmt.Sprintf("template %q referenced by metadata for %q is missing; falling back to %s", path, spec.Key, fallbackTarget))
				}
			}
		}
		if !ok {
			continue
		}
		asset := TemplateAsset{
			Key:       spec.Key,
			Filename:  selectedFilename,
			Version:   spec.Version,
			UpdatedAt: time.Time{},
			Content:   content,
		}
		if meta.Versions != nil {
			if v := strings.TrimSpace(meta.Versions[spec.Key]); v != "" {
				asset.Version = v
			}
		}
		if meta.Updated != nil {
			asset.UpdatedAt = meta.Updated[spec.Key]
		}
		if existingKey, exists := seenFilenames[asset.Filename]; exists {
			warnings = append(warnings, fmt.Sprintf("template %q for %q conflicts with %q; falling back to built-in default", asset.Filename, spec.Key, existingKey))
			continue
		}
		seenFilenames[asset.Filename] = spec.Key
		assets = append(assets, asset)
	}

	return assets, dedupeWarnings(warnings), nil
}

func readTemplateFile(path string) (string, bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, ""
		}
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", path, err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", false, fmt.Sprintf("template %q is empty; skipping", path)
	}
	return string(data), true, ""
}

func readMetadata(configDir string) (metadataFile, error) {
	path := filepath.Join(configTemplatesDir(configDir), metadataFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return metadataFile{}, err
	}
	var meta metadataFile
	if err := json.Unmarshal(data, &meta); err != nil {
		return metadataFile{}, fmt.Errorf("decoding metadata: %w", err)
	}
	if meta.SchemaVersion == "" {
		meta.SchemaVersion = DefaultSchemaVersion
	}
	if meta.SchemaVersion != DefaultSchemaVersion {
		return metadataFile{}, fmt.Errorf("unsupported metadata schema version %q", meta.SchemaVersion)
	}
	return meta, nil
}

func writeMetadata(configDir string, meta metadataFile) error {
	if meta.SchemaVersion == "" {
		meta.SchemaVersion = DefaultSchemaVersion
	}
	if err := os.MkdirAll(configTemplatesDir(configDir), 0700); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configTemplatesDir(configDir), metadataFileName), data, 0600); err != nil {
		return fmt.Errorf("writing metadata: %w", err)
	}
	return nil
}

func cloneDefaults() []TemplateAsset {
	cloned := make([]TemplateAsset, len(defaultSpecs))
	copy(cloned, defaultSpecs)
	for i := range cloned {
		cloned[i].UpdatedAt = time.Time{}
	}
	return cloned
}

func findDefaultByKey(key string) (TemplateAsset, bool) {
	for _, spec := range defaultSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return TemplateAsset{}, false
}

func normalizeTemplateName(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	n = strings.TrimSuffix(n, ".txt")
	n = strings.ReplaceAll(n, "-", "_")
	return n
}

func sanitizeTemplateFilename(filename string) (string, error) {
	name := strings.TrimSpace(filename)
	if name == "" {
		return "", errors.New("empty filename")
	}
	cleaned := filepath.Clean(name)
	if filepath.IsAbs(name) || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", filename)
	}
	if filepath.VolumeName(name) != "" || filepath.VolumeName(cleaned) != "" || strings.Contains(cleaned, ":") {
		return "", fmt.Errorf("volume-qualified paths are not allowed: %q", filename)
	}
	if cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("invalid path: %q", filename)
	}
	if filepath.Base(cleaned) != cleaned {
		return "", fmt.Errorf("path separators are not allowed: %q", filename)
	}
	if strings.Contains(cleaned, "/") || strings.Contains(cleaned, `\`) {
		return "", fmt.Errorf("path separators are not allowed: %q", filename)
	}
	if strings.EqualFold(cleaned, metadataFileName) {
		return "", fmt.Errorf("reserved filename is not allowed: %q", filename)
	}
	return cleaned, nil
}

func dedupeWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(warnings))
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

func hasSpecificTemplateWarning(warnings []string, key, filename string) bool {
	if len(warnings) == 0 {
		return false
	}
	quotedFilename := fmt.Sprintf("%q", filename)
	unusableIndicators := []string{
		"empty",
		"cannot read",
		"can't read",
		"failed to read",
		"unreadable",
		"conflict",
		"conflicts",
		"corrupt",
		"invalid",
		"malformed",
		"missing",
	}
	for _, warningText := range warnings {
		if !(strings.Contains(warningText, quotedFilename) || strings.Contains(warningText, filename) || strings.Contains(warningText, key)) {
			continue
		}
		lowered := strings.ToLower(warningText)
		for _, indicator := range unusableIndicators {
			if strings.Contains(lowered, indicator) {
				return true
			}
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nonZeroTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func configTemplatesDir(configDir string) string {
	if strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, TemplatesDirName)
	}
	return config.TemplatesDir()
}
