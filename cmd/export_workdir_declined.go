package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// declineWorkDirRepoScopeSettings lists the repository-scope settings of each
// work dir and of every repository directly inside it. None of them is
// exported (a work dir records only where projects are); the manifest names
// them so the other machine knows they come back through git, not the bundle.
// The --project directory is skipped: its own collection already lists it.
func declineWorkDirRepoScopeSettings(workDirs []string, projectDir string) []DeclinedAsset {
	// Resolved, so a project or work dir named through a symlink (macOS
	// /tmp is /private/tmp) is still recognised as the same directory.
	real := func(dir string) string {
		abs := absOrEmpty(dir)
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			return r
		}
		return abs
	}
	project := ""
	if strings.TrimSpace(projectDir) != "" {
		project = real(projectDir)
	}
	var out []DeclinedAsset
	seen := map[string]bool{}
	add := func(dir, prefix string) {
		abs := real(dir)
		if abs == "" || seen[abs] || abs == project {
			return
		}
		seen[abs] = true
		for _, d := range declineProjectRepoScopeSettings(abs) {
			d.Path = prefix + d.Path
			out = append(out, d)
		}
	}
	for _, wd := range workDirs {
		wd = strings.TrimSpace(wd)
		if wd == "" {
			continue
		}
		base := filepath.Base(absOrEmpty(wd)) + "/"
		add(wd, base)
		entries, err := os.ReadDir(wd)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			child := filepath.Join(wd, e.Name())
			if _, err := os.Lstat(filepath.Join(child, ".git")); err != nil {
				continue
			}
			add(child, base+e.Name()+"/")
		}
	}
	return out
}
