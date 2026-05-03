package agent

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateScopePattern returns an error if the scope/directory_pattern
// combination is invalid. This is the single source of truth for scope
// validation logic; callers may wrap or translate the error for their context.
func ValidateScopePattern(scope, pattern string) error {
	if strings.ContainsRune(scope, 0) || strings.ContainsRune(pattern, 0) {
		return fmt.Errorf("scope and directory_pattern must not contain null bytes")
	}
	switch scope {
	case "", InstructionScopeGlobal, InstructionScopeLocal:
		if pattern != "" {
			return fmt.Errorf("directory_pattern is only valid for directory scope")
		}
	case InstructionScopeDirectory:
		if pattern == "" {
			return fmt.Errorf("directory_pattern is required for directory scope")
		}
		if strings.HasPrefix(pattern, "..") {
			return fmt.Errorf("directory_pattern must not begin with \"..\"")
		}
	default:
		return fmt.Errorf("invalid scope %q; valid: global, directory, local", scope)
	}
	return nil
}

// ValidateInstructionScope returns an error if the scope/directory_pattern
// combination for inst is invalid.
func ValidateInstructionScope(inst InstructionFile) error {
	if strings.ContainsRune(inst.Name, 0) {
		return fmt.Errorf("instruction name must not contain null bytes")
	}
	if err := ValidateScopePattern(inst.Scope, inst.DirectoryPattern); err != nil {
		return fmt.Errorf("instruction %q: %w", inst.Name, err)
	}
	return nil
}

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

// matchesDirectory reports whether pattern applies to workDir or any of its
// ancestors. This allows a directory-scoped instruction to apply anywhere
// inside a matched root (e.g. pattern "/repo" matches "/repo/src/pkg").
//
// When the pattern contains forward slashes, both sides are normalized to
// forward slashes and path.Match is used for consistent cross-platform
// behavior. Separator-free patterns match against the base name of the
// original workDir only (no ancestor walk), so "myrepo" still works without
// full path anchoring.
// Returns false on error or when either argument is empty.
func matchesDirectory(pattern, workDir string) bool {
	if pattern == "" || workDir == "" {
		return false
	}
	hasSep := strings.ContainsRune(pattern, '/') || strings.ContainsRune(pattern, filepath.Separator)
	if hasSep {
		// Walk workDir and each ancestor upward.
		p := filepath.ToSlash(pattern)
		dir := workDir
		for {
			if ok, err := path.Match(p, filepath.ToSlash(dir)); err == nil && ok {
				return true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		return false
	}
	// Separator-free pattern: exact match first, then base-name fallback.
	if ok, err := filepath.Match(pattern, workDir); err == nil && ok {
		return true
	}
	ok, _ := filepath.Match(pattern, filepath.Base(workDir))
	return ok
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
		if ex, ok := existingByKey[InstructionKey(inc)]; ok {
			incScope := inc.Scope
			if incScope == "" {
				incScope = InstructionScopeGlobal
			}
			exScope := ex.Scope
			if exScope == "" {
				exScope = InstructionScopeGlobal
			}
			conflicts = append(conflicts, InstructionConflict{
				Name:             inc.Name,
				IncomingScope:    incScope,
				ExistingScope:    exScope,
				DirectoryPattern: inc.DirectoryPattern,
				ResolutionNote:   "existing kept",
			})
		}
	}
	return conflicts
}
