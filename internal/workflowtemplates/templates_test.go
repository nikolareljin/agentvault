package workflowtemplates

import (
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

	importWarnings, err := ImportBundle(dstCfg, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
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
		},
	}
	warnings, err := ImportBundle(cfgDir, bundle)
	if err != nil {
		t.Fatalf("ImportBundle() error = %v", err)
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
			_, err := ImportBundle(cfgDir, bundle)
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
	_, err := ImportBundle(cfgDir, bundle)
	if err == nil {
		t.Fatalf("ImportBundle() expected schema version error")
	}
	if !strings.Contains(err.Error(), "unsupported template bundle schema version") {
		t.Fatalf("ImportBundle() err = %v, want schema version error", err)
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
