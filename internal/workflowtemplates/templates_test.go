package workflowtemplates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadResolvedPrecedence(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()

	written, err := RefreshConfigTemplates(cfgDir, false)
	if err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	if len(written) == 0 {
		t.Fatalf("RefreshConfigTemplates() wrote no templates")
	}

	repoOverride := "repo override content\n"
	if err := os.WriteFile(filepath.Join(repoDir, "implement_issue.txt"), []byte(repoOverride), 0644); err != nil {
		t.Fatalf("WriteFile(repo override): %v", err)
	}
	configOverride := "config override content\n"
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "implement_pr.txt"), []byte(configOverride), 0644); err != nil {
		t.Fatalf("WriteFile(config override): %v", err)
	}

	resolved, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if len(resolved) != len(defaultSpecs) {
		t.Fatalf("LoadResolved() len = %d, want %d", len(resolved), len(defaultSpecs))
	}

	byFilename := map[string]ResolvedTemplate{}
	for _, item := range resolved {
		byFilename[item.Filename] = item
	}

	gotIssue := byFilename["implement_issue.txt"]
	if gotIssue.Source != "repo-local" {
		t.Fatalf("implement_issue source = %q, want repo-local", gotIssue.Source)
	}
	if gotIssue.Version != RepoLocalVersion {
		t.Fatalf("implement_issue version = %q, want %q", gotIssue.Version, RepoLocalVersion)
	}
	if strings.TrimSpace(gotIssue.Content) != strings.TrimSpace(repoOverride) {
		t.Fatalf("implement_issue content mismatch")
	}

	gotPR := byFilename["implement_pr.txt"]
	if gotPR.Source != "config" {
		t.Fatalf("implement_pr source = %q, want config", gotPR.Source)
	}
	if strings.TrimSpace(gotPR.Content) != strings.TrimSpace(configOverride) {
		t.Fatalf("implement_pr content mismatch")
	}

	gotAdd := byFilename["add_issue.txt"]
	if gotAdd.Source != "config" {
		t.Fatalf("add_issue source = %q, want config", gotAdd.Source)
	}
	if strings.TrimSpace(gotAdd.Content) == "" {
		t.Fatalf("add_issue content should not be empty")
	}
	if len(warnings) != 0 {
		t.Fatalf("LoadResolved() warnings = %v, want none", warnings)
	}
}

func TestLoadResolvedFallbackWarnings(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt"), []byte("\n"), 0644); err != nil {
		t.Fatalf("WriteFile(empty): %v", err)
	}

	resolved, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if len(resolved) != len(defaultSpecs) {
		t.Fatalf("resolved len = %d, want %d", len(resolved), len(defaultSpecs))
	}
	if len(warnings) == 0 {
		t.Fatalf("expected fallback warnings, got none")
	}
	for _, item := range resolved {
		if item.Source == "config" {
			continue
		}
		if item.Source != "built-in" {
			t.Fatalf("unexpected source %q", item.Source)
		}
	}
}

func TestExportImportRoundTripPreservesTemplateMetadata(t *testing.T) {
	srcCfg := t.TempDir()
	dstCfg := t.TempDir()

	if _, err := RefreshConfigTemplates(srcCfg, true); err != nil {
		t.Fatalf("RefreshConfigTemplates(src): %v", err)
	}

	customContent := "custom workflow body\n"
	customVersion := "custom-2026.03.08"
	if err := os.WriteFile(filepath.Join(srcCfg, TemplatesDirName, "implement_issue.txt"), []byte(customContent), 0644); err != nil {
		t.Fatalf("WriteFile(custom): %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcCfg, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "versions": {"implement_issue": "`+customVersion+`"},
  "updated": {"implement_issue": "2026-03-08T12:00:00Z"},
  "filenames": {"implement_issue": "implement_issue.txt"}
}`), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	bundle, warnings, err := ExportBundle(srcCfg)
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("ExportBundle() warnings = %v, want none", warnings)
	}
	if len(bundle.Assets) == 0 {
		t.Fatalf("bundle assets empty")
	}

	imported, importWarnings, err := ImportBundle(dstCfg, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if imported == 0 {
		t.Fatalf("ImportBundle() imported count = 0, want > 0")
	}
	if len(importWarnings) != 0 {
		t.Fatalf("ImportBundle() warnings = %v, want none", importWarnings)
	}

	dstBundle, _, err := ExportBundle(dstCfg)
	if err != nil {
		t.Fatalf("ExportBundle(dst) error = %v", err)
	}
	var found bool
	for _, asset := range dstBundle.Assets {
		if asset.Key != "implement_issue" {
			continue
		}
		found = true
		if strings.TrimSpace(asset.Content) != strings.TrimSpace(customContent) {
			t.Fatalf("content mismatch after round trip")
		}
		if asset.Version != customVersion {
			t.Fatalf("version = %q, want %q", asset.Version, customVersion)
		}
		if asset.UpdatedAt.IsZero() {
			t.Fatalf("updated_at should be preserved/non-zero")
		}
	}
	if !found {
		t.Fatalf("implement_issue not found after round trip")
	}
}

func TestImportBundleSkipsEmptyAssets(t *testing.T) {
	cfgDir := t.TempDir()
	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "implement_issue.txt", Version: "v1", Content: "valid\n"},
			{Key: "implement_pr", Filename: "implement_pr.txt", Version: "v1", Content: "\n"},
			{Key: "custom_template", Filename: "custom_template.txt", Version: "v1", Content: "custom\n"},
		},
	}
	imported, warnings, err := ImportBundle(cfgDir, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if imported != 2 {
		t.Fatalf("ImportBundle() imported count = %d, want 2 (implement_issue and the custom one)", imported)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for empty asset")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt")); err != nil {
		t.Fatalf("expected implement_issue.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, TemplatesDirName, "implement_pr.txt")); !os.IsNotExist(err) {
		t.Fatalf("implement_pr.txt should not exist, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, TemplatesDirName, "custom_template.txt")); err != nil {
		t.Fatalf("expected custom_template.txt: %v", err)
	}
}

func TestImportBundleNoOpDoesNotWriteMetadata(t *testing.T) {
	cfgDir := t.TempDir()
	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "implement_issue.txt", Version: "v1", Content: "\n"},
			{Key: "not a name", Filename: "Not A Name.txt", Version: "v1", Content: "x\n"},
		},
	}

	imported, warnings, err := ImportBundle(cfgDir, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if imported != 0 {
		t.Fatalf("ImportBundle() imported count = %d, want 0", imported)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warnings for no-op import")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, TemplatesDirName, metadataFileName)); !os.IsNotExist(err) {
		t.Fatalf("metadata.json should not exist after no-op import, err=%v", err)
	}
}

func TestFindTemplateFilenameAcceptsFilenameInput(t *testing.T) {
	got, ok := FindTemplateFilename("implement_issue.txt")
	if !ok {
		t.Fatalf("FindTemplateFilename() should resolve canonical filename input")
	}
	if got != "implement_issue.txt" {
		t.Fatalf("FindTemplateFilename() = %q, want implement_issue.txt", got)
	}
}

func TestImportBundleRejectsUnsafeFilename(t *testing.T) {
	cfgDir := t.TempDir()
	testCases := []struct {
		name     string
		filename string
	}{
		{name: "path-traversal", filename: "../escape.txt"},
		{name: "reserved-metadata", filename: "metadata.json"},
		{name: "reserved-metadata-dot-prefix", filename: "./metadata.json"},
		{name: "reserved-metadata-collapsed", filename: "foo/../metadata.json"},
		{name: "windows-volume", filename: "C:escape.txt"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := Bundle{
				SchemaVersion: DefaultSchemaVersion,
				ExportedAt:    time.Now().UTC(),
				Assets: []TemplateAsset{
					{Key: "implement_issue", Filename: tc.filename, Version: "v1", Content: "x\n"},
				},
			}
			_, _, err := ImportBundle(cfgDir, bundle)
			if err == nil {
				t.Fatalf("ImportBundle() expected unsafe filename error for %q", tc.filename)
			}
			if !strings.Contains(err.Error(), "invalid filename") {
				t.Fatalf("ImportBundle() err = %v, want invalid filename", err)
			}
		})
	}
}

func TestImportBundleRejectsUnsupportedSchemaVersion(t *testing.T) {
	cfgDir := t.TempDir()
	bundle := Bundle{
		SchemaVersion: "99",
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "implement_issue.txt", Version: "v1", Content: "x\n"},
		},
	}
	_, _, err := ImportBundle(cfgDir, bundle)
	if err == nil {
		t.Fatalf("ImportBundle() expected schema version error")
	}
	if !strings.Contains(err.Error(), "unsupported template bundle schema version") {
		t.Fatalf("ImportBundle() err = %v, want schema version error", err)
	}
}

func TestImportBundleRejectsDuplicateKeys(t *testing.T) {
	cfgDir := t.TempDir()
	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "implement_issue.txt", Version: "v1", Content: "first\n"},
			{Key: "implement_issue", Filename: "implement_issue-copy.txt", Version: "v2", Content: "second\n"},
		},
	}

	_, _, err := ImportBundle(cfgDir, bundle)
	if err == nil {
		t.Fatalf("ImportBundle() expected duplicate key error")
	}
	if !strings.Contains(err.Error(), "duplicate template key") {
		t.Fatalf("ImportBundle() err = %v, want duplicate template key", err)
	}
}

func TestImportBundleRepairsInvalidExistingMetadata(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}
	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "implement_issue.txt", Version: "v2", Content: "fixed content\n"},
		},
	}

	imported, warnings, err := ImportBundle(cfgDir, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if imported != 1 {
		t.Fatalf("ImportBundle() imported count = %d, want 1", imported)
	}
	var hasRepairWarning bool
	for _, warningText := range warnings {
		if strings.Contains(warningText, "existing template metadata invalid; resetting metadata defaults") {
			hasRepairWarning = true
		}
	}
	if !hasRepairWarning {
		t.Fatalf("expected metadata repair warning, got %v", warnings)
	}
	meta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata() error = %v", err)
	}
	if meta.Versions["implement_issue"] != "v2" {
		t.Fatalf("metadata version = %q, want v2", meta.Versions["implement_issue"])
	}
}

func TestRefreshConfigTemplatesErrorsOnInvalidMetadata(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	_, err := RefreshConfigTemplates(cfgDir, false)
	if err == nil {
		t.Fatalf("RefreshConfigTemplates() expected metadata error")
	}
	if !strings.Contains(err.Error(), "reading template metadata") {
		t.Fatalf("RefreshConfigTemplates() err = %v, want metadata read context", err)
	}
}

func TestLoadResolvedUsesSanitizedMetadataFilename(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}

	customName := "custom-pr-template.txt"
	customContent := "custom pr template\n"
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, customName), []byte(customContent), 0644); err != nil {
		t.Fatalf("WriteFile(custom template): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_pr": "`+customName+`"}
}`), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	resolved, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	var matched *ResolvedTemplate
	for i := range resolved {
		if resolved[i].Key == "implement_pr" {
			matched = &resolved[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("implement_pr template not resolved")
	}
	if matched.Filename != customName {
		t.Fatalf("Filename = %q, want %q", matched.Filename, customName)
	}
	wantPath := filepath.Join(cfgDir, TemplatesDirName, customName)
	if matched.Path != wantPath {
		t.Fatalf("Path = %q, want %q", matched.Path, wantPath)
	}
	if strings.TrimSpace(matched.Content) != strings.TrimSpace(customContent) {
		t.Fatalf("content mismatch")
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestLoadResolvedIgnoresUnsafeMetadataFilename(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_issue": "../leak.txt"}
}`), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	resolved, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	var issue ResolvedTemplate
	for _, item := range resolved {
		if item.Key == "implement_issue" {
			issue = item
			break
		}
	}
	if issue.Filename != "implement_issue.txt" {
		t.Fatalf("Filename = %q, want canonical fallback", issue.Filename)
	}
	if issue.Path != filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt") {
		t.Fatalf("Path = %q, want canonical path", issue.Path)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warning for unsafe metadata filename")
	}
}

func TestLoadResolvedWarnsOnMissingMetadataSelectedFilename(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	if err := os.Remove(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt")); err != nil {
		t.Fatalf("Remove(canonical template): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_issue": "custom-issue-template.txt"}
}`), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	_, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}

	var hasMissingMetadataWarning bool
	for _, warningText := range warnings {
		if strings.Contains(warningText, `custom-issue-template.txt" referenced by metadata for "implement_issue" is missing; falling back to built-in default template`) {
			hasMissingMetadataWarning = true
		}
	}
	if !hasMissingMetadataWarning {
		t.Fatalf("expected missing metadata-selected filename warning, got %v", warnings)
	}
}

func TestLoadResolvedFallsBackToCanonicalConfigFileWhenMetadataFileMissing(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	canonicalContent := "canonical issue body\n"
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt"), []byte(canonicalContent), 0600); err != nil {
		t.Fatalf("WriteFile(canonical): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_issue": "custom-issue-template.txt"}
}`), 0644); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	resolved, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	var issue *ResolvedTemplate
	for i := range resolved {
		if resolved[i].Key == "implement_issue" {
			issue = &resolved[i]
			break
		}
	}
	if issue == nil {
		t.Fatalf("implement_issue template not resolved")
	}
	if issue.Filename != "implement_issue.txt" {
		t.Fatalf("Filename = %q, want canonical fallback", issue.Filename)
	}
	if strings.TrimSpace(issue.Content) != strings.TrimSpace(canonicalContent) {
		t.Fatalf("content mismatch after canonical fallback")
	}
	var hasMissingMetadataWarning bool
	for _, warningText := range warnings {
		if strings.Contains(warningText, `custom-issue-template.txt" referenced by metadata for "implement_issue" is missing; falling back to canonical config template "implement_issue.txt"`) {
			hasMissingMetadataWarning = true
		}
	}
	if !hasMissingMetadataWarning {
		t.Fatalf("expected missing metadata-selected filename warning, got %v", warnings)
	}
}

func TestLoadResolvedDoesNotDuplicateFallbackWarning(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt"), []byte("\n"), 0600); err != nil {
		t.Fatalf("WriteFile(empty): %v", err)
	}

	_, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	emptyCount := 0
	missingCount := 0
	for _, warningText := range warnings {
		if strings.Contains(warningText, `implement_issue.txt" is empty; skipping`) {
			emptyCount++
		}
		if strings.Contains(warningText, `template "implement_issue.txt" missing from config storage; using built-in default`) {
			missingCount++
		}
	}
	if emptyCount != 1 {
		t.Fatalf("empty warning count = %d, want 1", emptyCount)
	}
	if missingCount != 0 {
		t.Fatalf("missing warning count = %d, want 0 when specific warning exists", missingCount)
	}
}

func TestLoadResolvedKeepsFallbackWarningAfterUnsafeMetadataWarning(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	// Remove canonical file so built-in fallback is required.
	if err := os.Remove(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt")); err != nil {
		t.Fatalf("Remove(canonical template): %v", err)
	}
	// Add unsafe metadata filename to trigger metadata warning for same key.
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_issue": "../unsafe.txt"}
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	_, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}

	var hasUnsafeMetaWarning bool
	var hasFallbackMissingWarning bool
	for _, warningText := range warnings {
		if strings.Contains(warningText, `ignoring unsafe metadata filename for "implement_issue"`) {
			hasUnsafeMetaWarning = true
		}
		if strings.Contains(warningText, `template "implement_issue.txt" missing from config storage; using built-in default`) {
			hasFallbackMissingWarning = true
		}
	}
	if !hasUnsafeMetaWarning {
		t.Fatalf("expected unsafe metadata warning")
	}
	if !hasFallbackMissingWarning {
		t.Fatalf("expected missing fallback warning even when unsafe metadata warning exists")
	}
}

func TestExportBundleIncludesMissingDefaults(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "implement_issue.txt"), []byte("issue-only\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, warnings, err := ExportBundle(cfgDir)
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	byKey := make(map[string]TemplateAsset, len(bundle.Assets))
	for _, asset := range bundle.Assets {
		byKey[asset.Key] = asset
	}
	for _, key := range []string{"implement_issue", "implement_pr", "add_issue"} {
		if _, ok := byKey[key]; !ok {
			t.Fatalf("missing key %q in exported bundle", key)
		}
	}
	if strings.TrimSpace(byKey["implement_issue"].Content) != "issue-only" {
		t.Fatalf("implement_issue content mismatch")
	}
	foundMissingDefaultWarning := false
	for _, warningText := range warnings {
		if strings.Contains(warningText, "exporting built-in default") {
			foundMissingDefaultWarning = true
			break
		}
	}
	if !foundMissingDefaultWarning {
		t.Fatalf("expected warning about exporting built-in default")
	}
}

func TestExportBundleAvoidsDuplicateResolvedFilenames(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "shared.txt"), []byte("shared body\n"), 0600); err != nil {
		t.Fatalf("WriteFile(shared): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {
    "implement_issue": "shared.txt",
    "implement_pr": "shared.txt"
  }
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	bundle, warnings, err := ExportBundle(cfgDir)
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	seen := make(map[string]string, len(bundle.Assets))
	for _, asset := range bundle.Assets {
		if existingKey, exists := seen[asset.Filename]; exists {
			t.Fatalf("duplicate exported filename %q for keys %q and %q", asset.Filename, existingKey, asset.Key)
		}
		seen[asset.Filename] = asset.Key
	}
	var hasConflictWarning bool
	for _, warningText := range warnings {
		if strings.Contains(warningText, `conflicts with`) {
			hasConflictWarning = true
		}
	}
	if !hasConflictWarning {
		t.Fatalf("expected duplicate filename conflict warning, got %v", warnings)
	}
}

func TestLoadResolvedDoesNotAddGenericMissingWarningAfterConflictWarning(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, "shared.txt"), []byte("shared body\n"), 0600); err != nil {
		t.Fatalf("WriteFile(shared): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "filenames": {
    "implement_issue": "shared.txt",
    "implement_pr": "shared.txt"
  }
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	_, warnings, err := LoadResolved(cfgDir, repoDir)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	conflictCount := 0
	missingCount := 0
	for _, warningText := range warnings {
		if strings.Contains(warningText, `template "shared.txt" for "implement_pr" conflicts with "implement_issue"; falling back to built-in default`) {
			conflictCount++
		}
		if strings.Contains(warningText, `template "implement_pr.txt" missing from config storage; using built-in default`) {
			missingCount++
		}
	}
	if conflictCount != 1 {
		t.Fatalf("conflict warning count = %d, want 1", conflictCount)
	}
	if missingCount != 0 {
		t.Fatalf("missing warning count = %d, want 0 when conflict warning exists", missingCount)
	}
}

func TestRefreshConfigTemplatesBackfillsUpdatedMetadata(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates(force=true) error = %v", err)
	}
	// Remove updated metadata while keeping template files in place.
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "versions": {
    "implement_issue": "builtin-2.0",
    "implement_pr": "builtin-2.0",
    "add_issue": "builtin-2.1"
  },
  "filenames": {
    "implement_issue": "implement_issue.txt",
    "implement_pr": "implement_pr.txt",
    "add_issue": "add_issue.txt"
  }
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}
	if _, err := RefreshConfigTemplates(cfgDir, false); err != nil {
		t.Fatalf("RefreshConfigTemplates(force=false) error = %v", err)
	}
	meta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata() error = %v", err)
	}
	for _, key := range []string{"implement_issue", "implement_pr", "add_issue"} {
		ts := meta.Updated[key]
		if ts.IsZero() {
			t.Fatalf("meta.Updated[%q] should be backfilled", key)
		}
	}
}

func TestBuiltInAddIssueDescribesBothTodoForms(t *testing.T) {
	spec, _ := findDefaultByKey("add_issue")
	for _, want := range []string{"[TODO]", "[/TODO]", "ID: <next 3-digit id>", "TODO.md", "## <title>", "first line under the heading", "description as a paragraph"} {
		if !strings.Contains(spec.Content, want) {
			t.Errorf("add_issue template missing %q", want)
		}
	}
}

func TestBuiltInTemplatesStateInputsStepsAndOutput(t *testing.T) {
	for _, spec := range defaultSpecs {
		for _, want := range []string{"Inputs:", "Steps:", "Output:"} {
			if !strings.Contains(spec.Content, want) {
				t.Errorf("%s template missing %q", spec.Key, want)
			}
		}
	}
}

func TestRefreshConfigTemplatesPreservesMetadataSelectedFilenameWithoutForce(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, TemplatesDirName), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	customName := "custom-issue-template.txt"
	customContent := "custom issue body\n"
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, customName), []byte(customContent), 0600); err != nil {
		t.Fatalf("WriteFile(custom): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, TemplatesDirName, metadataFileName), []byte(`{
  "schema_version": "1",
  "versions": {"implement_issue": "custom-v1"},
  "updated": {"implement_issue": "2026-03-09T00:00:00Z"},
  "filenames": {"implement_issue": "`+customName+`"}
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	written, err := RefreshConfigTemplates(cfgDir, false)
	if err != nil {
		t.Fatalf("RefreshConfigTemplates(force=false) error = %v", err)
	}
	foundCustomRewrite := false
	for _, asset := range written {
		if asset.Key == "implement_issue" {
			foundCustomRewrite = true
		}
	}
	if foundCustomRewrite {
		t.Fatalf("RefreshConfigTemplates(force=false) should not rewrite existing metadata-selected file")
	}

	meta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata() error = %v", err)
	}
	if meta.Filenames["implement_issue"] != customName {
		t.Fatalf("metadata filename = %q, want %q", meta.Filenames["implement_issue"], customName)
	}
	content, err := os.ReadFile(filepath.Join(cfgDir, TemplatesDirName, customName))
	if err != nil {
		t.Fatalf("ReadFile(custom): %v", err)
	}
	if string(content) != customContent {
		t.Fatalf("custom content changed unexpectedly")
	}
}

func TestRefreshConfigTemplatesDoesNotRewriteMetadataWhenUnchanged(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates(force=true) error = %v", err)
	}
	beforeMeta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata(before) error = %v", err)
	}
	if beforeMeta.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero UpdatedAt after initial refresh")
	}

	time.Sleep(10 * time.Millisecond)

	written, err := RefreshConfigTemplates(cfgDir, false)
	if err != nil {
		t.Fatalf("RefreshConfigTemplates(force=false) error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("RefreshConfigTemplates(force=false) wrote %d templates, want 0", len(written))
	}

	afterMeta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata(after) error = %v", err)
	}
	if !afterMeta.UpdatedAt.Equal(beforeMeta.UpdatedAt) {
		t.Fatalf("UpdatedAt changed unexpectedly: %v -> %v", beforeMeta.UpdatedAt, afterMeta.UpdatedAt)
	}
}

func TestImportBundlePreservesMetadataForUntouchedKeys(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}
	beforeMeta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata(before) error = %v", err)
	}
	originalPRFilename := beforeMeta.Filenames["implement_pr"]
	originalPRVersion := beforeMeta.Versions["implement_pr"]
	originalPRUpdated := beforeMeta.Updated["implement_pr"]
	if originalPRFilename == "" || originalPRVersion == "" || originalPRUpdated.IsZero() {
		t.Fatalf("expected initialized metadata for implement_pr")
	}

	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{
				Key:      "implement_issue",
				Filename: "implement_issue.txt",
				Version:  "imported-v2",
				Content:  "updated issue body\n",
			},
		},
	}
	imported, warnings, err := ImportBundle(cfgDir, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
	}
	if imported != 1 {
		t.Fatalf("ImportBundle() imported count = %d, want 1", imported)
	}
	if len(warnings) != 0 {
		t.Fatalf("ImportBundle() warnings = %v, want none", warnings)
	}

	afterMeta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata(after) error = %v", err)
	}
	if afterMeta.Filenames["implement_pr"] != originalPRFilename {
		t.Fatalf("implement_pr filename changed unexpectedly: %q -> %q", originalPRFilename, afterMeta.Filenames["implement_pr"])
	}
	if afterMeta.Versions["implement_pr"] != originalPRVersion {
		t.Fatalf("implement_pr version changed unexpectedly: %q -> %q", originalPRVersion, afterMeta.Versions["implement_pr"])
	}
	if !afterMeta.Updated["implement_pr"].Equal(originalPRUpdated) {
		t.Fatalf("implement_pr updated timestamp changed unexpectedly: %v -> %v", originalPRUpdated, afterMeta.Updated["implement_pr"])
	}
}

func TestImportBundleRejectsDuplicateResolvedFilenames(t *testing.T) {
	cfgDir := t.TempDir()
	bundle := Bundle{
		SchemaVersion: DefaultSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Assets: []TemplateAsset{
			{Key: "implement_issue", Filename: "shared.txt", Version: "v1", Content: "issue\n"},
			{Key: "implement_pr", Filename: "shared.txt", Version: "v1", Content: "pr\n"},
		},
	}
	_, _, err := ImportBundle(cfgDir, bundle)
	if err == nil {
		t.Fatalf("ImportBundle() expected duplicate filename error")
	}
	if !strings.Contains(err.Error(), "duplicate template filename") {
		t.Fatalf("ImportBundle() err = %v, want duplicate template filename", err)
	}
}

// A stored template written by an older release keeps winning forever, because
// RefreshConfigTemplates never overwrites an existing file without --force. That
// is correct - it must not clobber someone's edits - but it must not be silent
// either, which is how one machine went on using a superseded template while
// reporting nothing. Both directions are asserted: a warning when the stored
// version is an older built-in, and none when it matches.
func TestLoadResolvedWarnsWhenStoredTemplateIsAnOlderBuiltin(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, false); err != nil {
		t.Fatalf("RefreshConfigTemplates() error = %v", err)
	}

	spec, ok := findDefaultByKey("implement_pr")
	if !ok {
		t.Fatalf("findDefaultByKey(implement_pr) not found")
	}

	// Sound before broken: as written by this release, nothing is stale.
	_, warnings, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "refresh --force") {
			t.Fatalf("fresh config storage reported a stale template: %q", w)
		}
	}

	// Now age the stored copy the way an older release left it.
	meta, err := readMetadata(cfgDir)
	if err != nil {
		t.Fatalf("readMetadata() error = %v", err)
	}
	meta.Versions["implement_pr"] = "builtin-2.0"
	if err := writeMetadata(cfgDir, meta); err != nil {
		t.Fatalf("writeMetadata() error = %v", err)
	}

	_, warnings, err = LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "implement_pr.txt") &&
			strings.Contains(w, "builtin-2.0") &&
			strings.Contains(w, spec.Version) &&
			strings.Contains(w, "refresh --force") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no staleness warning for a superseded stored template; got %v", warnings)
	}
}

func TestCustomTemplateIsListedExportedAndImported(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release-notes.txt"), []byte("write the notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Not template names: ignored, never an error.
	for _, name := range []string{"Notes.txt", "notes.json", "a b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	resolved, _, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	var custom *ResolvedTemplate
	for i := range resolved {
		if resolved[i].Filename == "release-notes.txt" {
			custom = &resolved[i]
		}
		if resolved[i].Filename == "Notes.txt" || resolved[i].Filename == "notes.json" || resolved[i].Filename == "a b.txt" {
			t.Errorf("%s is not a template name but was listed", resolved[i].Filename)
		}
	}
	if custom == nil || custom.Key != "release_notes" || custom.Source != "config" || custom.Version != CustomVersion {
		t.Fatalf("custom template = %+v, want key release_notes from config", custom)
	}

	bundle, _, err := ExportBundle(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if _, _, err := ImportBundle(other, bundle); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(other, TemplatesDirName, "release-notes.txt"))
	if err != nil || strings.TrimSpace(string(got)) != "write the notes" {
		t.Fatalf("custom template did not travel: %q, %v", got, err)
	}
}

func TestRepoLocalFileOverridesCustomTemplate(t *testing.T) {
	cfgDir := t.TempDir()
	repo := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "triage.txt"), []byte("stored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "triage.txt"), []byte("repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := LoadResolved(cfgDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resolved {
		if r.Key == "triage" {
			if r.Source != "repo-local" || strings.TrimSpace(r.Content) != "repo" {
				t.Fatalf("triage = %+v, want the repo-local file", r)
			}
			return
		}
	}
	t.Fatal("triage was not listed")
}

func writeTemplate(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findResolved(t *testing.T, resolved []ResolvedTemplate, key string) ResolvedTemplate {
	t.Helper()
	var found []ResolvedTemplate
	for _, r := range resolved {
		if r.Key == key {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d templates with key %q, want 1: %+v", len(found), key, found)
	}
	return found[0]
}

func hasWarning(warnings []string, parts ...string) bool {
	for _, w := range warnings {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(w, p)
		}
		if all {
			return true
		}
	}
	return false
}

func TestMarkdownCustomTemplateFrontMatter(t *testing.T) {
	cfgDir := t.TempDir()
	writeTemplate(t, filepath.Join(cfgDir, TemplatesDirName), "triage.md",
		"---\ndescription: \"Sort new issues\"\n---\n\n# Triage\n1. Label it.\n")
	resolved, _, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	r := findResolved(t, resolved, "triage")
	if r.Description() != "Sort new issues" {
		t.Errorf("description = %q", r.Description())
	}
	if strings.Contains(r.Body(), "description:") || !strings.HasPrefix(r.Body(), "# Triage") {
		t.Errorf("body kept the front matter: %q", r.Body())
	}
	if !strings.Contains(r.Content, "description:") {
		t.Error("stored content should stay the whole file, so export round-trips it")
	}
}

func TestTxtWinsOverMarkdownWithAWarning(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	writeTemplate(t, dir, "triage.md", "markdown\n")
	writeTemplate(t, dir, "triage.txt", "plain\n")
	resolved, warnings, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(t, resolved, "triage"); r.Filename != "triage.txt" {
		t.Errorf("using %s, want triage.txt", r.Filename)
	}
	if !hasWarning(warnings, "triage.txt", "triage.md") {
		t.Errorf("no warning naming both files: %v", warnings)
	}

	repo := t.TempDir()
	writeTemplate(t, repo, "implement_pr.md", "repo markdown\n")
	writeTemplate(t, repo, "implement_pr.txt", "repo plain\n")
	resolved, warnings, err = LoadResolved(cfgDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(t, resolved, "implement_pr"); r.Filename != "implement_pr.txt" || r.Source != "repo-local" {
		t.Errorf("implement_pr = %s from %s, want the repo's .txt", r.Filename, r.Source)
	}
	if !hasWarning(warnings, "implement_pr.txt", "implement_pr.md") {
		t.Errorf("no warning naming both repo files: %v", warnings)
	}
}

func TestBuiltInKeyFromMarkdownFile(t *testing.T) {
	cfgDir := t.TempDir()
	writeTemplate(t, filepath.Join(cfgDir, TemplatesDirName), "implement_issue.md", "# Our issue flow\n")
	repo := t.TempDir()
	writeTemplate(t, repo, "implement_pr.md", "# Our review flow\n")
	resolved, _, err := LoadResolved(cfgDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(t, resolved, "implement_issue"); r.Source != "config" || r.Filename != "implement_issue.md" {
		t.Errorf("implement_issue = %s from %s, want config implement_issue.md", r.Filename, r.Source)
	}
	if r := findResolved(t, resolved, "implement_pr"); r.Source != "repo-local" || r.Filename != "implement_pr.md" {
		t.Errorf("implement_pr = %s from %s, want repo-local implement_pr.md", r.Filename, r.Source)
	}
}

func TestRefreshKeepsAMarkdownCopyOfABuiltIn(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	writeTemplate(t, dir, "implement_issue.md", "# our flow\n")
	if _, err := RefreshConfigTemplates(cfgDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "implement_issue.txt")); !os.IsNotExist(err) {
		t.Fatalf("refresh wrote implement_issue.txt beside the user's .md (err=%v)", err)
	}
	if _, err := RefreshConfigTemplates(cfgDir, false); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(t, resolved, "implement_issue"); r.Filename != "implement_issue.md" || strings.TrimSpace(r.Content) != "# our flow" {
		t.Fatalf("implement_issue = %s %q, want the user's .md", r.Filename, r.Content)
	}
}

func TestStoredMarkdownBuiltInStillLetsATxtWin(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	// What an import of a bundle holding implement_pr.md leaves behind.
	bundle := Bundle{SchemaVersion: DefaultSchemaVersion, Assets: []TemplateAsset{
		{Key: "implement_pr", Filename: "implement_pr.md", Version: "v1", Content: "# md\n"},
	}}
	if _, _, err := ImportBundle(cfgDir, bundle); err != nil {
		t.Fatal(err)
	}
	writeTemplate(t, dir, "implement_pr.txt", "plain\n")
	resolved, warnings, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(t, resolved, "implement_pr"); r.Filename != "implement_pr.txt" {
		t.Fatalf("using %s, want implement_pr.txt", r.Filename)
	}
	if !hasWarning(warnings, "implement_pr.txt", "implement_pr.md") {
		t.Errorf("no warning naming both files: %v", warnings)
	}
}

func TestSplitFrontMatterEdgeCases(t *testing.T) {
	cases := []struct {
		name, in, desc, body string
	}{
		{"none", "# Title\n", "", "# Title\n"},
		{"crlf", "---\r\ndescription: Win\r\n---\r\n# T\r\n", "Win", "# T\r\n"},
		{"bom", "\ufeff---\ndescription: B\n---\nbody\n", "B", "body\n"},
		{"unclosed", "---\ndescription: x\nbody\n", "", "---\ndescription: x\nbody\n"},
		{"rule not front matter", "text\n---\nmore\n", "", "text\n---\nmore\n"},
		{"colon in value", "---\ndescription: 'a: b'\n---\nx\n", "a: b", "x\n"},
	}
	for _, c := range cases {
		desc, body := splitFrontMatter(c.in)
		if desc != c.desc || body != c.body {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, desc, body, c.desc, c.body)
		}
	}
}

func TestSymlinkedCustomTemplateIsListed(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(target, []byte("notes body\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "notes.md")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resolved {
		if r.Key == "folder" {
			t.Fatalf("directory folder.txt listed as a template")
		}
		if r.Key == "notes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("symlinked notes.md not listed: %+v", resolved)
	}
}

func TestRefreshForceOverwritesALoneMarkdownCopy(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "implement_pr.md"), []byte("mine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshConfigTemplates(cfgDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "implement_pr.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refresh --force wrote implement_pr.txt beside the .md copy (err=%v)", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "implement_pr.md"))
	if string(got) != builtinImplementPRTemplate {
		t.Fatalf("implement_pr.md not overwritten with the built-in: %q", got)
	}
	_, warnings, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "both") {
			t.Fatalf("unexpected warning: %s", w)
		}
	}
}

func TestImportReplacesTheOtherExtensionCopy(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := RefreshConfigTemplates(cfgDir, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfgDir, TemplatesDirName)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("old notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{SchemaVersion: DefaultSchemaVersion, Assets: []TemplateAsset{
		{Key: "implement_pr", Filename: "implement_pr.md", Version: CustomVersion, Content: "imported pr\n"},
		{Key: "notes", Filename: "notes.md", Version: CustomVersion, Content: "imported notes\n"},
	}}
	if _, _, err := ImportBundle(cfgDir, bundle); err != nil {
		t.Fatal(err)
	}
	resolved, warnings, err := LoadResolved(cfgDir, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"implement_pr": "imported pr\n", "notes": "imported notes\n"}
	for _, r := range resolved {
		if w, ok := want[r.Key]; ok && r.Content != w {
			t.Fatalf("%s resolved to %q from %s, want the imported copy", r.Key, r.Content, r.Filename)
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestImportNeverRemovesAnotherTemplatesFile(t *testing.T) {
	cfgDir := t.TempDir()
	dir := filepath.Join(cfgDir, TemplatesDirName)
	writeTemplate(t, dir, "my-flow.md", "my own custom template\n")
	// A built-in stored under a renamed file whose stem matches the custom one.
	bundle := Bundle{SchemaVersion: DefaultSchemaVersion, Assets: []TemplateAsset{
		{Key: "implement_issue", Filename: "my-flow.txt", Version: "v1", Content: "issue flow\n"},
	}}
	if _, _, err := ImportBundle(cfgDir, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-flow.md")); err != nil {
		t.Fatalf("import removed an unrelated template: %v", err)
	}
}
