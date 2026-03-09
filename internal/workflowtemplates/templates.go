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
	SchemaVersion string                     `json:"schema_version"`
	UpdatedAt     time.Time                  `json:"updated_at,omitempty"`
	Versions      map[string]string          `json:"versions,omitempty"`
	Updated       map[string]time.Time       `json:"updated,omitempty"`
	Filenames     map[string]string          `json:"filenames,omitempty"`
	Extra         map[string]json.RawMessage `json:"-"`
}

var defaultSpecs = []TemplateAsset{
	{
		Key:      "implement_issue",
		Filename: "implement_issue.txt",
		Version:  "builtin-1.0",
		Content: `Implement Issue Template

Metadata:
- Template Version: 1.0
- Template Type: Issue Workflow

Inputs:
- Repository path
- Issue reference
- Base branch

Workflow:
1. Review issue scope and acceptance criteria.
2. Create a release branch from the selected base branch.
3. Implement code changes.
4. Update tests and documentation.
5. Run validation commands.
6. Commit, push, and open PR that references the issue.

Notes:
- Keep changes scoped to the issue.
- Prefer clear commit messages and explicit test results.
`,
	},
	{
		Key:      "implement_pr",
		Filename: "implement_pr.txt",
		Version:  "builtin-1.0",
		Content: `Implement PR Review Template

Metadata:
- Template Version: 1.0
- Template Type: PR Review Fix Workflow

Inputs:
- Repository path
- PR reference

Workflow:
1. Gather unresolved review comments.
2. Implement code fixes aligned with reviewer feedback.
3. Add or update tests for regressions.
4. Run lint/test/build checks.
5. Commit, push, and update PR with concise summary.
6. Re-request review only (do not request automated code fixing).
`,
	},
	{
		Key:      "add_issue",
		Filename: "add_issue.txt",
		Version:  "builtin-1.0",
		Content: `Add Issue Template

Metadata:
- Template Version: 1.0
- Template Type: Issue Creation Workflow

Inputs:
- Repository path
- Feature or bug description

Workflow:
1. Define problem statement and impact.
2. Add acceptance criteria.
3. Add implementation notes and validation approach.
4. Create issue with a clear, actionable title.
`,
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
		if norm == spec.Key || norm == spec.Filename {
			return spec.Filename, true
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
						Version:   "repo-local",
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
				Path:          filepath.Join(configTemplatesDir(configDir), spec.Filename),
			})
			continue
		}

		warnings = append(warnings, fmt.Sprintf("template %q missing from config storage; using built-in default", spec.Filename))
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
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Filename < assets[j].Filename })
	return Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets:        assets,
	}, dedupeWarnings(warnings), nil
}

// ImportBundle writes template assets into config storage.
func ImportBundle(configDir string, bundle Bundle) ([]string, error) {
	if len(bundle.Assets) == 0 {
		return []string{"bundle did not include workflow templates; nothing imported"}, nil
	}
	if err := os.MkdirAll(configTemplatesDir(configDir), 0755); err != nil {
		return nil, fmt.Errorf("creating templates directory: %w", err)
	}
	meta := metadataFile{
		SchemaVersion: DefaultSchemaVersion,
		UpdatedAt:     time.Now().UTC(),
		Versions:      make(map[string]string),
		Updated:       make(map[string]time.Time),
		Filenames:     make(map[string]string),
	}
	warnings := make([]string, 0)
	for _, asset := range bundle.Assets {
		asset.Key = normalizeTemplateName(asset.Key)
		if asset.Key == "" {
			warnings = append(warnings, "skipped template with empty key")
			continue
		}
		filename := asset.Filename
		if filename == "" {
			if def, ok := findDefaultByKey(asset.Key); ok {
				filename = def.Filename
			} else {
				filename = asset.Key + ".txt"
			}
		}
		if strings.TrimSpace(asset.Content) == "" {
			warnings = append(warnings, fmt.Sprintf("skipped empty template %q", filename))
			continue
		}
		if err := os.WriteFile(filepath.Join(configTemplatesDir(configDir), filename), []byte(asset.Content), 0644); err != nil {
			return nil, fmt.Errorf("writing template %s: %w", filename, err)
		}
		meta.Versions[asset.Key] = firstNonEmpty(asset.Version, "imported")
		meta.Updated[asset.Key] = nonZeroTime(asset.UpdatedAt, time.Now().UTC())
		meta.Filenames[asset.Key] = filename
	}
	if err := writeMetadata(configDir, meta); err != nil {
		return nil, err
	}
	return dedupeWarnings(warnings), nil
}

// RefreshConfigTemplates ensures config storage contains default templates and metadata.
func RefreshConfigTemplates(configDir string, force bool) ([]TemplateAsset, error) {
	templatesDir := configTemplatesDir(configDir)
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		return nil, fmt.Errorf("creating templates directory: %w", err)
	}
	now := time.Now().UTC()
	meta, _ := readMetadata(configDir)
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

	written := make([]TemplateAsset, 0, len(defaultSpecs))
	for _, spec := range defaultSpecs {
		path := filepath.Join(templatesDir, spec.Filename)
		if !force {
			if content, ok, _ := readTemplateFile(path); ok && strings.TrimSpace(content) != "" {
				if _, ok := meta.Versions[spec.Key]; !ok {
					meta.Versions[spec.Key] = spec.Version
				}
				if _, ok := meta.Filenames[spec.Key]; !ok {
					meta.Filenames[spec.Key] = spec.Filename
				}
				continue
			}
		}
		if err := os.WriteFile(path, []byte(spec.Content), 0644); err != nil {
			return nil, fmt.Errorf("writing template %s: %w", spec.Filename, err)
		}
		meta.Versions[spec.Key] = spec.Version
		meta.Updated[spec.Key] = now
		meta.Filenames[spec.Key] = spec.Filename
		written = append(written, spec)
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
	for _, spec := range defaultSpecs {
		filename := spec.Filename
		if meta.Filenames != nil {
			if mfn := strings.TrimSpace(meta.Filenames[spec.Key]); mfn != "" {
				filename = mfn
			}
		}
		path := filepath.Join(configTemplatesDir(configDir), filename)
		content, ok, warn := readTemplateFile(path)
		if !ok {
			if warn != "" {
				warnings = append(warnings, warn)
			}
			continue
		}
		asset := TemplateAsset{
			Key:       spec.Key,
			Filename:  filename,
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
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", filepath.Base(path), err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", false, fmt.Sprintf("template %q is empty; skipping", filepath.Base(path))
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
	return meta, nil
}

func writeMetadata(configDir string, meta metadataFile) error {
	if err := os.MkdirAll(configTemplatesDir(configDir), 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configTemplatesDir(configDir), metadataFileName), data, 0644); err != nil {
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
