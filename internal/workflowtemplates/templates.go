package workflowtemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/nikolareljin/agentvault/internal/config"
)

const (
	DefaultSchemaVersion = "1"
	TemplatesDirName     = "templates"
	metadataFileName     = "metadata.json"
	RepoLocalVersion     = "repo-local"
	CustomVersion        = "custom"
)

// A template file is plain text (<name>.txt) or Markdown (<name>.md); with both,
// the .txt one wins. A custom template is any other <name>.txt or <name>.md a user adds to config storage. It is
// listed, shown, exported and imported like a built-in; agentvault ships none.
var customFilenamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}\.(txt|md)$`)

// reservedStem reports a Windows device name (con.txt opens the console there).
func reservedStem(filename string) bool {
	stem := strings.TrimSuffix(strings.TrimSuffix(filename, ".txt"), ".md")
	switch stem {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '0' && stem[3] <= '9'
}

// customKey returns the key for a custom template file, or false when the name
// is not one (wrong shape, or a built-in's key).
func customKey(filename string) (string, bool) {
	if !customFilenamePattern.MatchString(filename) || reservedStem(filename) {
		return "", false
	}
	key := normalizeTemplateName(filename)
	if _, builtin := findDefaultByKey(key); builtin {
		return "", false
	}
	return key, true
}

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

// Description is the front matter "description:" of a Markdown template, if any.
func (t ResolvedTemplate) Description() string {
	if !t.isMarkdown() {
		return ""
	}
	desc, _ := splitFrontMatter(t.Content)
	// A repository-local file is untrusted: drop control characters, so a
	// description cannot send terminal escape sequences to `templates list`.
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, desc)
}

// Body is the template without its front matter: what an agent is sent. A .txt
// template is sent whole, even when it starts with "---".
func (t ResolvedTemplate) Body() string {
	if !t.isMarkdown() {
		return t.Content
	}
	_, body := splitFrontMatter(t.Content)
	return body
}

func (t ResolvedTemplate) isMarkdown() bool {
	return strings.HasSuffix(strings.ToLower(t.Filename), ".md")
}

// splitFrontMatter separates a leading "---" YAML block from the text and
// returns its description value. Text without one comes back unchanged.
func splitFrontMatter(content string) (string, string) {
	text := strings.TrimLeft(content, "\ufeff")
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", content
	}
	lines := strings.SplitAfter(text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		desc := ""
		for _, line := range lines[1:i] {
			key, value, ok := strings.Cut(line, ":")
			if ok && strings.TrimSpace(key) == "description" {
				desc = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
		return desc, strings.TrimLeft(strings.Join(lines[i+1:], ""), "\r\n")
	}
	return "", content
}

// readTxtOrMD reads dir/<stem>.txt, else dir/<stem>.md. With both present the
// .txt one wins and a warning names the .md one. With contained set, a file that
// resolves outside dir (a symlink, or a symlinked parent) is refused: a cloned
// repository must not be able to pull ~/.aws/credentials into a prompt.
func readTxtOrMD(dir, txtName string, contained bool) (content string, name string, ok bool, warnings []string) {
	read := readTemplateFile
	if contained {
		read = func(path string) (string, bool, string) { return readContainedTemplateFile(dir, path) }
	}
	mdName := otherExtension(txtName)
	content, ok, warn := read(filepath.Join(dir, txtName))
	if warn != "" {
		warnings = append(warnings, warn)
	}
	if ok {
		if _, err := os.Stat(filepath.Join(dir, mdName)); err == nil {
			warnings = append(warnings, fmt.Sprintf("both %q and %q exist in %s; using %q", txtName, mdName, dir, txtName))
		}
		return content, txtName, true, warnings
	}
	content, ok, warn = read(filepath.Join(dir, mdName))
	if warn != "" {
		warnings = append(warnings, warn)
	}
	return content, mdName, ok, warnings
}

type metadataFile struct {
	SchemaVersion string               `json:"schema_version"`
	UpdatedAt     time.Time            `json:"updated_at,omitempty"`
	Versions      map[string]string    `json:"versions,omitempty"`
	Updated       map[string]time.Time `json:"updated,omitempty"`
	Filenames     map[string]string    `json:"filenames,omitempty"`
}

// The built-in templates are short and generic on purpose: a starting point a
// user edits, replaces per repository, or swaps for their own (docs/templates.md).
const builtinImplementIssueTemplate = `Implement Issue

Inputs: repository path, issue reference, base branch (default: the
repository's default branch).

Steps:
1. Read the issue. Confirm the scope and acceptance criteria; ask when they are unclear.
2. Create a branch from the base branch. Default name: release/X.Y.Z with the next
   semantic version. Use the repository's own naming instead when it has one, or
   when asked.
3. Make the change, scoped to the issue. Comment only where the logic is not obvious.
4. Add or update tests for the new behaviour, including edge cases.
5. Update the docs and the changelog when a user sees a difference.
6. Run the repository's build, lint and tests. Fix what fails.
7. Commit with a clear message, push, and open a pull request that references the issue.

Output: branch, files changed, test results, commit, pull request URL.
`

const builtinImplementPRTemplate = `Fix Pull Request Review

Inputs: repository path, pull request reference.

Steps:
1. Read every unresolved review thread and comment. List what each one asks for.
2. Fix each confirmed issue. Where you disagree, reply with the reason instead.
3. Run the repository's build, lint and tests. Fix what fails.
4. Commit and push to the pull request branch.
5. Reply to or resolve each thread you addressed.
6. Look over the whole change once more, then request review again.

Output: threads addressed, files changed, test results, commit, pull request URL.
`

const builtinAddIssueTemplate = `Add TODO Item

Inputs: repository path, TODO file (default: TODO.txt), and for each item a
title, description, implementation details and acceptance criteria.

TODO.txt: items sit between [TODO] and [/TODO] markers, fields in this order:

ID: <next 3-digit id>
Title: <title>
Description: <description>
Implementation Details:
- <detail>
Acceptance Criteria:
- <criterion>

TODO.md: one "## <title>" section per item. The first non-blank line under
the heading is "ID: <id>", then the description as a paragraph, then "Implementation
Details:" and "Acceptance Criteria:", each followed by a "- " list.

Steps:
1. Read the file and find the highest ID. Never renumber existing items.
2. Give each new item the next ID.
3. Refuse an item without implementation details or acceptance criteria.
4. Add new items at the end (before [/TODO] in TODO.txt). Change nothing else.

Output: file path, IDs assigned, items added.
`

var defaultSpecs = []TemplateAsset{
	{
		Key:      "implement_issue",
		Filename: "implement_issue.txt",
		Version:  "builtin-3.0",
		Content:  builtinImplementIssueTemplate,
	},
	{
		Key:      "implement_pr",
		Filename: "implement_pr.txt",
		Version:  "builtin-3.0",
		Content:  builtinImplementPRTemplate,
	},
	{
		Key:      "add_issue",
		Filename: "add_issue.txt",
		Version:  "builtin-3.0",
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
			content, name, ok, warns := readTxtOrMD(repoDir, spec.Filename, true)
			warnings = append(warnings, warns...)
			if ok {
				resolved = append(resolved, ResolvedTemplate{
					TemplateAsset: TemplateAsset{
						Key:       spec.Key,
						Filename:  name,
						Version:   RepoLocalVersion,
						UpdatedAt: time.Time{},
						Content:   content,
					},
					Source: "repo-local",
					Path:   filepath.Join(repoDir, name),
				})
				continue
			}
		}

		if asset, ok := byKey[spec.Key]; ok && strings.TrimSpace(asset.Content) != "" {
			// A stored copy always wins, including one written by an older
			// release. Without this warning that is silent and permanent:
			// RefreshConfigTemplates never overwrites an existing file unless
			// --force is passed, so a machine keeps a superseded template
			// indefinitely while reporting nothing.
			if stored := strings.TrimSpace(asset.Version); stored != "" &&
				strings.HasPrefix(stored, "builtin-") && stored != spec.Version {
				warnings = append(warnings, fmt.Sprintf(
					"template %q in config storage is %s; the built-in is %s. "+
						"Run `agentvault templates refresh --force` to replace it, "+
						"or edit it in place to keep your own version.",
					asset.Filename, stored, spec.Version))
			}
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

	for _, asset := range assets {
		if _, builtin := findDefaultByKey(asset.Key); builtin {
			continue
		}
		if repoDir != "" {
			txtName := asset.Filename
			if strings.HasSuffix(txtName, ".md") {
				txtName = otherExtension(txtName)
			}
			content, name, ok, warns := readTxtOrMD(repoDir, txtName, true)
			warnings = append(warnings, warns...)
			if ok {
				asset.Filename, asset.Version, asset.UpdatedAt, asset.Content = name, RepoLocalVersion, time.Time{}, content
				resolved = append(resolved, ResolvedTemplate{TemplateAsset: asset, Source: "repo-local", Path: filepath.Join(repoDir, name)})
				continue
			}
		}
		resolved = append(resolved, ResolvedTemplate{
			TemplateAsset: asset,
			Source:        "config",
			Path:          filepath.Join(configTemplatesDir(configDir), asset.Filename),
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
		for _, asset := range byKey {
			if _, builtin := findDefaultByKey(asset.Key); !builtin {
				merged = append(merged, asset)
			}
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
			key, ok := customKey(asset.Filename)
			if !ok || key != asset.Key {
				warnings = append(warnings, fmt.Sprintf("skipped template %q: a custom template is <name>.txt or <name>.md, lowercase letters, digits, - and _", asset.Key))
				continue
			}
			defaultSpec = TemplateAsset{Key: key, Filename: asset.Filename}
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
		if !customFilenamePattern.MatchString(filename) || reservedStem(filename) {
			if !supported {
				warnings = append(warnings, fmt.Sprintf("skipped template %q: %q is not <name>.txt or <name>.md", asset.Key, filename))
				continue
			}
			// Older releases stored a built-in under any safe name; keep the
			// content under the canonical one rather than fail the import.
			warnings = append(warnings, fmt.Sprintf("template %q: stored %q as %q", asset.Key, filename, defaultSpec.Filename))
			filename = defaultSpec.Filename
		}
		if existingKey, exists := seenFilenames[filename]; exists {
			return 0, nil, fmt.Errorf("duplicate template filename %q for keys %q and %q", filename, existingKey, asset.Key)
		}
		seenFilenames[filename] = asset.Key
		if strings.TrimSpace(asset.Content) == "" {
			warnings = append(warnings, fmt.Sprintf("skipped empty template %q", filename))
			continue
		}
		if len(asset.Content) > MaxTemplateBytes {
			warnings = append(warnings, fmt.Sprintf("skipped template %q: %d bytes, over the %d byte ceiling", filename, len(asset.Content), MaxTemplateBytes))
			continue
		}
		// A renamed built-in must not overwrite a file that is another template's.
		if normalizeTemplateName(filename) != asset.Key && meta.Filenames[asset.Key] != filename {
			if _, err := os.Lstat(filepath.Join(configTemplatesDir(configDir), filename)); err == nil {
				warnings = append(warnings, fmt.Sprintf("skipped template %q: %q already holds another template", asset.Key, filename))
				continue
			}
		}
		if err := os.WriteFile(filepath.Join(configTemplatesDir(configDir), filename), []byte(asset.Content), 0600); err != nil {
			return 0, nil, fmt.Errorf("writing template %s: %w", filename, err)
		}
		// The imported file replaces this key's template. A stored copy under the
		// other extension would otherwise shadow it (.txt wins) or warn forever.
		// Only the same template's other copy: a renamed file can share a stem
		// with another template, which must survive the import.
		if sibling := otherExtension(filename); sibling != "" && normalizeTemplateName(sibling) == asset.Key {
			if _, claimed := seenFilenames[sibling]; !claimed {
				if err := os.Remove(filepath.Join(configTemplatesDir(configDir), sibling)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return 0, nil, fmt.Errorf("removing replaced template %s: %w", sibling, err)
				}
			}
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
		if force && filename == spec.Filename {
			// Overwrite the copy in effect: a .txt written beside a lone .md
			// would shadow it and warn on every run.
			mdName := otherExtension(spec.Filename)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				if _, err := os.Stat(filepath.Join(templatesDir, mdName)); err == nil {
					filename, path = mdName, filepath.Join(templatesDir, mdName)
				}
			}
		}
		if !force {
			// A Markdown copy of a built-in is the user's version: keep it, and do
			// not write a .txt beside it, which would win over it.
			if filename == spec.Filename {
				mdName := otherExtension(spec.Filename)
				if content, ok, _ := readTemplateFile(filepath.Join(templatesDir, mdName)); ok && strings.TrimSpace(content) != "" {
					if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
						if _, ok := meta.Versions[spec.Key]; !ok {
							meta.Versions[spec.Key] = CustomVersion
							dirty = true
						}
						if meta.Filenames[spec.Key] != mdName {
							meta.Filenames[spec.Key] = mdName
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
				} else if safeFilename != otherExtension(spec.Filename) {
					filename = safeFilename
					usedMetadataFilename = safeFilename != spec.Filename
				}
			}
		}
		selectedFilename := filename
		path := filepath.Join(configTemplatesDir(configDir), selectedFilename)
		content, ok, warn := readTemplateFile(path)
		if !usedMetadataFilename {
			// The canonical name, so <key>.md stands in for a missing <key>.txt.
			var name string
			var warns []string
			content, name, ok, warns = readTxtOrMD(configTemplatesDir(configDir), filename, false)
			selectedFilename, path, warn = name, filepath.Join(configTemplatesDir(configDir), name), ""
			warnings = append(warnings, warns...)
		}
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

	// A file a built-in's metadata points at belongs to that built-in, whether or
	// not it was read above.
	for _, spec := range defaultSpecs {
		seenFilenames[spec.Filename] = spec.Key
		seenFilenames[otherExtension(spec.Filename)] = spec.Key
		if meta.Filenames != nil {
			if mfn := strings.TrimSpace(meta.Filenames[spec.Key]); mfn != "" {
				seenFilenames[mfn] = spec.Key
			}
		}
	}
	entries, err := os.ReadDir(configTemplatesDir(configDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		warnings = append(warnings, fmt.Sprintf("cannot list custom templates (%v)", err))
	}
	customAt := map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		if name == metadataFileName {
			continue
		}
		// Follow a symlink, as the built-in names do: a dotfiles checkout often
		// links its templates in.
		if info, err := os.Stat(filepath.Join(configTemplatesDir(configDir), name)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, taken := seenFilenames[name]; taken {
			continue
		}
		key, ok := customKey(name)
		if !ok {
			continue
		}
		content, ok, warn := readTemplateFile(filepath.Join(configTemplatesDir(configDir), name))
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if !ok {
			continue
		}
		asset := TemplateAsset{Key: key, Filename: name, Version: CustomVersion, Content: content}
		if v := strings.TrimSpace(meta.Versions[key]); v != "" {
			asset.Version = v
		}
		asset.UpdatedAt = meta.Updated[key]
		if i, dup := customAt[key]; dup {
			txt, md := name, assets[i].Filename
			if strings.HasSuffix(md, ".txt") {
				txt, md = md, name
			}
			warnings = append(warnings, fmt.Sprintf("both %q and %q exist in %s; using %q", txt, md, configTemplatesDir(configDir), txt))
			if strings.HasSuffix(name, ".txt") {
				assets[i] = asset
			}
			continue
		}
		customAt[key] = len(assets)
		assets = append(assets, asset)
	}

	return assets, dedupeWarnings(warnings), nil
}

// otherExtension returns <stem>.md for <stem>.txt and the reverse, else "".
func otherExtension(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".txt"):
		return strings.TrimSuffix(filename, ".txt") + ".md"
	case strings.HasSuffix(filename, ".md"):
		return strings.TrimSuffix(filename, ".md") + ".txt"
	}
	return ""
}

// MaxTemplateBytes is the ceiling for one template file or imported template.
const MaxTemplateBytes = 256 * 1024

// readContainedTemplateFile reads path only when its real path stays inside root.
func readContainedTemplateFile(root, path string) (string, bool, string) {
	if _, err := os.Lstat(path); err != nil {
		return readTemplateFile(path)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, fmt.Sprintf("cannot resolve %q; skipping (%v)", root, err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", path, err)
	}
	if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, fmt.Sprintf("template %q resolves outside %s; skipping", path, root)
	}
	return readTemplateFile(path)
}

func readTemplateFile(path string) (string, bool, string) {
	// Stat before opening: a FIFO would block the open, a device never ends.
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, ""
		}
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Sprintf("template %q is not a regular file; skipping", path)
	}
	if info.Size() > MaxTemplateBytes {
		return "", false, fmt.Sprintf("template %q is %d bytes, over the %d byte ceiling; skipping", path, info.Size(), MaxTemplateBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxTemplateBytes+1))
	if err != nil {
		return "", false, fmt.Sprintf("cannot read template %q; skipping (%v)", path, err)
	}
	if len(data) > MaxTemplateBytes {
		return "", false, fmt.Sprintf("template %q is over the %d byte ceiling; skipping", path, MaxTemplateBytes)
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
	n = strings.TrimSuffix(strings.TrimSuffix(n, ".txt"), ".md")
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
