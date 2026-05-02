package agent

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// InstructionConflict reports a scope collision detected during import.
type InstructionConflict struct {
	Name             string
	IncomingScope    string
	ExistingScope    string
	DirectoryPattern string
	ResolutionNote   string
}

// InstructionKey returns the composite identity key for an instruction.
// Unique identity is Name + Scope + DirectoryPattern, so a global and a
// directory-scoped instruction with the same name can coexist.
func InstructionKey(inst InstructionFile) string {
	scope := inst.Scope
	if scope == "" {
		scope = InstructionScopeGlobal
	}
	return inst.Name + "\x00" + scope + "\x00" + inst.DirectoryPattern
}

// scopeRank returns the precedence level for a scope string.
// Higher rank wins when multiple instructions share the same Name.
func scopeRank(scope string) int {
	switch scope {
	case InstructionScopeLocal:
		return 3
	case InstructionScopeDirectory:
		return 2
	default: // "global" or ""
		return 1
	}
}

// matchesDirectory reports whether pattern matches workDir.
// Tries an exact filepath.Match first. When the pattern contains forward
// slashes (common in exported patterns), both sides are normalized to forward
// slashes via filepath.ToSlash so matching works correctly on Windows.
// For patterns without any path separator, falls back to matching the base
// name so "projectname" works without full anchoring.
// Returns false on error or when either argument is empty.
func matchesDirectory(pattern, workDir string) bool {
	if pattern == "" || workDir == "" {
		return false
	}
	// When the pattern contains forward slashes, normalize both to slashes and
	// use path.Match (which always treats '/' as separator) for consistent
	// cross-platform behavior. Otherwise use filepath.Match.
	if strings.ContainsRune(pattern, '/') {
		p := filepath.ToSlash(pattern)
		w := filepath.ToSlash(workDir)
		if ok, err := path.Match(p, w); err == nil && ok {
			return true
		}
	} else {
		if ok, err := filepath.Match(pattern, workDir); err == nil && ok {
			return true
		}
	}
	// Separator-free patterns match against the base name.
	if !strings.ContainsRune(pattern, filepath.Separator) && !strings.ContainsRune(pattern, '/') {
		ok, _ := filepath.Match(pattern, filepath.Base(workDir))
		return ok
	}
	return false
}

// ResolveEffectiveInstructions merges a flat instruction list using scope precedence:
// local > directory > global. For each unique Name, the highest-rank applicable
// instruction wins. Directory-scoped instructions only apply when their pattern
// matches workDir; an empty workDir disables all directory-scope matching.
func ResolveEffectiveInstructions(instructions []InstructionFile, workDir string) []InstructionFile {
	type candidate struct {
		inst InstructionFile
		rank int
	}
	best := make(map[string]candidate)

	for _, inst := range instructions {
		scope := inst.Scope
		if scope == "" {
			scope = InstructionScopeGlobal
		}

		// Directory-scoped instructions must match the working directory.
		if scope == InstructionScopeDirectory && !matchesDirectory(inst.DirectoryPattern, workDir) {
			continue
		}

		rank := scopeRank(scope)
		if existing, ok := best[inst.Name]; !ok || rank > existing.rank ||
			(rank == existing.rank && len(inst.DirectoryPattern) > len(existing.inst.DirectoryPattern)) ||
			(rank == existing.rank && inst.DirectoryPattern < existing.inst.DirectoryPattern) {
			best[inst.Name] = candidate{inst: inst, rank: rank}
		}
	}

	result := make([]InstructionFile, 0, len(best))
	for _, c := range best {
		result = append(result, c.inst)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// CheckInstructionConflicts compares incoming instructions against existing ones
// using the composite key (Name + Scope + DirectoryPattern). Same composite key
// is a conflict (existing wins). Different scopes for the same name coexist.
func CheckInstructionConflicts(existing, incoming []InstructionFile) []InstructionConflict {
	existingByKey := make(map[string]InstructionFile, len(existing))
	for _, e := range existing {
		existingByKey[InstructionKey(e)] = e
	}

	var conflicts []InstructionConflict
	for _, inc := range incoming {
		if _, ok := existingByKey[InstructionKey(inc)]; ok {
			incScope := inc.Scope
			if incScope == "" {
				incScope = InstructionScopeGlobal
			}
			conflicts = append(conflicts, InstructionConflict{
				Name:             inc.Name,
				IncomingScope:    incScope,
				ExistingScope:    incScope,
				DirectoryPattern: inc.DirectoryPattern,
				ResolutionNote:   "existing kept",
			})
		}
	}
	return conflicts
}
