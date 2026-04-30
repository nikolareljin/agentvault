package agent

import "path/filepath"

// InstructionConflict reports a scope collision detected during import.
type InstructionConflict struct {
	Name           string
	IncomingScope  string
	ExistingScope  string
	ResolutionNote string
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

// matchesDirectory reports whether pattern matches workDir using filepath.Match.
// Returns false on error or when either argument is empty.
func matchesDirectory(pattern, workDir string) bool {
	if pattern == "" || workDir == "" {
		return false
	}
	matched, err := filepath.Match(pattern, workDir)
	if err != nil {
		return false
	}
	return matched
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
		if existing, ok := best[inst.Name]; !ok || rank > existing.rank {
			best[inst.Name] = candidate{inst: inst, rank: rank}
		}
	}

	result := make([]InstructionFile, 0, len(best))
	for _, c := range best {
		result = append(result, c.inst)
	}
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
				Name:           inc.Name,
				IncomingScope:  incScope,
				ExistingScope:  incScope,
				ResolutionNote: "existing wins (use --merge to update)",
			})
		}
	}
	return conflicts
}
