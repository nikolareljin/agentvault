package cmd

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/gitid"
	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

// captureBundleLocations records the export's project, the Codex trusted
// projects and any --workdir by work dir and git identity, merged with what
// the vault already knows. It returns nil when there is nothing to record.
func captureBundleLocations(cmd *cobra.Command, v *vault.Vault, bundle SetupBundle, opts setupCollectOptions) *locations.Set {
	var projects []string
	if bundle.SourceProject != "" {
		projects = append(projects, bundle.SourceProject)
	}
	if c := bundle.ProviderConfigs.Codex; c != nil {
		var trusted []string
		for p := range c.TrustedProjects {
			trusted = append(trusted, p)
		}
		sort.Strings(trusted)
		projects = append(projects, trusted...)
	}
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	captured := locations.Capture(ctx, bundle.SourceHome, projects, opts.WorkDirs, nil)
	merged := locations.Merge(v.Locations(), captured)
	if merged.Empty() {
		return nil
	}
	// Aliases are this vault's history of other machines' paths; the
	// bundle carries where each project is now.
	for i := range merged.Projects {
		merged.Projects[i].Aliases = nil
	}
	// Work dir mappings are this machine's choices, not the bundle's.
	merged.WorkDirMaps = nil
	merged.ConfirmedHidden = nil
	if merged.Empty() {
		return nil
	}
	return &merged
}

// recordProjectLocation adds dir to the vault's known projects, by identity.
func recordProjectLocation(v *vault.Vault, dir string) error {
	captured := locations.Capture(context.Background(), exportSourceHome(), []string{dir}, nil, nil)
	if captured.Empty() || captured.Projects[0].GitRemote == "" && len(captured.Projects[0].RootCommits) == 0 {
		return nil
	}
	return v.SetLocations(locations.Merge(v.Locations(), captured))
}

// aliasDirectoryPatterns makes directory-scoped instructions written for
// another checkout of the same repository apply to dir. The vault knows the
// repository's other paths (aliases); a pattern naming one of them, or a path
// below it, is rewritten to dir before matching.
func aliasDirectoryPatterns(insts []agent.InstructionFile, known locations.Set, dir string, identify locations.Identifier) []agent.InstructionFile {
	if known.Empty() {
		return insts
	}
	if identify == nil {
		identify = func(ctx context.Context, d string) (gitid.Repo, error) {
			return gitid.Identify(ctx, d, skipGitRunner)
		}
	}
	r, err := identify(context.Background(), dir)
	if err != nil || r.Remote == "" && len(r.RootCommits) == 0 {
		return insts
	}
	p, ok := known.FindByIdentity(r.Remote, r.RootCommits)
	if !ok {
		return insts
	}
	here := filepath.ToSlash(r.Root)
	var olds []string
	for _, a := range append([]string{p.Abs}, p.Aliases...) {
		if a = strings.TrimRight(filepath.ToSlash(a), "/"); a != "" && a != here {
			olds = append(olds, a)
		}
	}
	out := make([]agent.InstructionFile, len(insts))
	for i, inst := range insts {
		if inst.Scope == agent.InstructionScopeDirectory {
			pat := agent.NormalizeDirectoryPattern(inst.DirectoryPattern)
			for _, old := range olds {
				if pat == old || strings.HasPrefix(pat, old+"/") {
					inst.DirectoryPattern = here + pat[len(old):]
					break
				}
			}
		}
		out[i] = inst
	}
	return out
}
