package agent

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ScopePatternErrorKind identifies a specific scope/directory_pattern validation failure.
type ScopePatternErrorKind string

const (
	ScopePatternErrorNullByte          ScopePatternErrorKind = "null_byte"
	ScopePatternErrorDirectoryOnly     ScopePatternErrorKind = "directory_only"
	ScopePatternErrorDirectoryRequired ScopePatternErrorKind = "directory_required"
	ScopePatternErrorParentTraversal   ScopePatternErrorKind = "parent_traversal"
	ScopePatternErrorInvalidGlob       ScopePatternErrorKind = "invalid_glob"
	ScopePatternErrorInvalidScope      ScopePatternErrorKind = "invalid_scope"
)

// ScopePatternError reports a structured scope/directory_pattern validation failure.
type ScopePatternError struct {
	Kind    ScopePatternErrorKind
	Scope   string
	Pattern string
	Err     error
}

func (e *ScopePatternError) Error() string {
	switch e.Kind {
	case ScopePatternErrorNullByte:
		return "scope and directory_pattern must not contain null bytes"
	case ScopePatternErrorDirectoryOnly:
		return "directory_pattern is only valid for directory scope"
	case ScopePatternErrorDirectoryRequired:
		return "directory_pattern is required for directory scope"
	case ScopePatternErrorParentTraversal:
		return "directory_pattern must not begin with \"..\""
	case ScopePatternErrorInvalidGlob:
		return fmt.Sprintf("directory_pattern has invalid glob syntax: %v", e.Err)
	case ScopePatternErrorInvalidScope:
		return fmt.Sprintf("invalid scope %q; valid: global, directory, local", e.Scope)
	default:
		return "invalid scope/directory_pattern"
	}
}

func (e *ScopePatternError) Unwrap() error {
	return e.Err
}

// ValidateScopePattern returns an error if the scope/directory_pattern
// combination is invalid. This is the single source of truth for scope
// validation logic; callers may wrap or translate the error for their context.
func ValidateScopePattern(scope, pattern string) error {
	if strings.ContainsRune(scope, 0) || strings.ContainsRune(pattern, 0) {
		return &ScopePatternError{Kind: ScopePatternErrorNullByte, Scope: scope, Pattern: pattern}
	}
	switch scope {
	case "", InstructionScopeGlobal, InstructionScopeLocal:
		if pattern != "" {
			return &ScopePatternError{Kind: ScopePatternErrorDirectoryOnly, Scope: scope, Pattern: pattern}
		}
	case InstructionScopeDirectory:
		if pattern == "" {
			return &ScopePatternError{Kind: ScopePatternErrorDirectoryRequired, Scope: scope, Pattern: pattern}
		}
		normalizedPattern := NormalizeDirectoryPattern(pattern)
		if normalizedPattern == ".." || strings.HasPrefix(normalizedPattern, "../") {
			return &ScopePatternError{Kind: ScopePatternErrorParentTraversal, Scope: scope, Pattern: pattern}
		}
		if _, err := path.Match(normalizedPattern, ""); err != nil {
			return &ScopePatternError{Kind: ScopePatternErrorInvalidGlob, Scope: scope, Pattern: pattern, Err: err}
		}
	default:
		return &ScopePatternError{Kind: ScopePatternErrorInvalidScope, Scope: scope, Pattern: pattern}
	}
	return nil
}

// NormalizeDirectoryPattern returns a stable slash form for directory-scope
// identity and matching, regardless of the platform or CLI input style.
func NormalizeDirectoryPattern(pattern string) string {
	return strings.ReplaceAll(filepath.ToSlash(pattern), "\\", "/")
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
	if err := ValidateInstructionFilename(inst.Filename); err != nil {
		return fmt.Errorf("instruction %q: %w", inst.Name, err)
	}
	return nil
}

// InstructionFilenameForms names what ValidateInstructionFilename accepts,
// for error messages.
const InstructionFilenameForms = "an instruction file (AGENTS.md, CLAUDE.md, GEMINI.md, codex.md, " +
	".github/copilot-instructions.md, .cursorrules, .windsurfrules, .clinerules, .aider.conf.yml, " +
	"or .md, .mdc, .markdown, .txt, .rst) in the project, in a plain directory or under " +
	".github/instructions, .github/prompts, .cursor/rules, .windsurf/rules, .clinerules, " +
	".claude/agents, .claude/commands, .claude/skills or .gemini/commands (also .toml there); " +
	"any file in a skill directory (.claude/skills/NAME/...); " +
	"or a file an instruction refers to, in a plain directory"

// instructionDirs are the dot directories an instruction may sit under, at
// any depth. Any other dot directory belongs to a tool's settings.
var instructionDirs = [][]string{
	{".github", "instructions"},
	{".github", "prompts"},
	{".cursor", "rules"},
	{".windsurf", "rules"},
	{".clinerules"},
	{".claude", "agents"},
	{".claude", "commands"},
	{".claude", "skills"},
	{".gemini", "commands"},
}

// instructionDotFiles are rule files without a prose extension that tools
// read as instructions only.
var instructionDotFiles = map[string]bool{
	".cursorrules": true, ".windsurfrules": true, ".clinerules": true,
}

// autoLoadedFiles are files a tool loads and runs on its own when it
// enters or opens the directory, at any depth. Never an instruction.
var autoLoadedFiles = map[string]bool{
	"mise.toml": true, "mise.local.toml": true, "cmakelists.txt": true, "cmakepresets.json": true,
	"cmakeuserpresets.json": true, "devcontainer.json": true, "directory.build.props": true,
	"directory.build.targets": true, "directory.build.rsp": true, "directory.packages.props": true,
}

// InstructionFileRunsCommands is whether an instruction written to name can
// run code: .aider.conf.yml (aider runs its lint-cmd and test-cmd) and the
// scripts and other files instructions refer to. An import takes a bundle's
// copy of these only with consent.
func InstructionFileRunsCommands(name string) bool {
	n, err := normalizeInstructionFilename(name)
	if err != nil {
		return false
	}
	runs, reason := classifyInstructionTarget(n)
	return reason == "" && runs
}

// NormalizePathForMatch returns name as Windows resolves it, for matching
// only: slash separated, lower case, each segment without trailing dots and
// spaces. Every path check uses it so the checks never diverge.
func NormalizePathForMatch(name string) string {
	segs := strings.Split(strings.ReplaceAll(name, "\\", "/"), "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			continue
		}
		segs[i] = strings.ToLower(strings.TrimRight(seg, ". "))
	}
	return strings.Join(segs, "/")
}

// normalizeInstructionFilename returns name as Windows would resolve it:
// slash separated, lower case, each segment without trailing dots and spaces.
func normalizeInstructionFilename(name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("filename must not contain null bytes")
	}
	slashed := strings.ReplaceAll(name, "\\", "/")
	// A colon is a drive or an NTFS alternate data stream ("a.md:x").
	if path.IsAbs(slashed) || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.Contains(slashed, ":") {
		return "", fmt.Errorf("filename %q must be relative", name)
	}
	for _, seg := range strings.Split(slashed, "/") {
		if t := strings.TrimRight(seg, ". "); t == "" && seg != "" && seg != "." && seg != ".." {
			// "... " or ".. " can read as a parent directory on Windows.
			return "", fmt.Errorf("filename %q has a segment of dots and spaces", name)
		}
	}
	cleaned := path.Clean(NormalizePathForMatch(slashed))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("filename %q escapes the target directory", name)
	}
	return cleaned, nil
}

// ValidateInstructionFilename rejects filenames that would be written outside
// the target directory on push or apply, or that name a tool's settings,
// hooks or git files rather than an instruction. Empty is allowed (not yet
// placed).
func ValidateInstructionFilename(name string) error {
	if name == "" {
		return nil
	}
	cleaned, err := normalizeInstructionFilename(name)
	if err != nil {
		return err
	}
	if _, reason := classifyInstructionTarget(cleaned); reason != "" {
		return fmt.Errorf("filename %q: %s; allowed: %s", name, reason, InstructionFilenameForms)
	}
	return nil
}

// classifyInstructionTarget checks rel, a normalized name. reason is why it
// is refused; runs is whether an allowed name can run code. Instructions
// are allowed in plain directories and known instruction directories only:
// a name such as .git/config, pkg/.envrc or sub/.vscode/tasks.json would
// turn content into configuration that runs commands.
func classifyInstructionTarget(rel string) (runs bool, reason string) {
	segs := strings.Split(rel, "/")
	for _, seg := range segs {
		switch seg {
		case ".git", ".githooks", ".husky":
			return false, "runs code or configures git; refused"
		}
		// An 8.3 short name such as GIT~1 or CLAUDE~1 can name a dot directory
		// on Windows.
		if i := strings.IndexByte(seg, '~'); i >= 0 && i+1 < len(seg) && seg[i+1] >= '0' && seg[i+1] <= '9' {
			return false, "an 8.3 short name; refused"
		}
	}
	if rel == ".aider.conf.yml" {
		return true, ""
	}
	dirs, base := segs[:len(segs)-1], segs[len(segs)-1]
	inToolDir, inGeminiCommands := false, false
	inSkill := false
	for i := 0; i < len(dirs); {
		if !strings.HasPrefix(dirs[i], ".") {
			i++
			continue
		}
		if inSkill {
			return false, "under a dot directory in a skill; refused"
		}
		n := matchInstructionDir(dirs[i:])
		if n == 0 && dirs[i] == ".github" && i == len(dirs)-1 && base == "copilot-instructions.md" {
			// Directly under .github only Copilot's own instruction file.
			i++
			continue
		}
		if n == 0 {
			return false, "under a tool's settings directory; refused"
		}
		if n == 2 && dirs[i] == ".claude" && dirs[i+1] == "skills" && i+2 < len(dirs) {
			// A skill is a directory: the scripts, references and assets it
			// bundles are part of it. Its own dot directories are not.
			inSkill = true
			i += n
			continue
		}
		inToolDir = true
		inGeminiCommands = dirs[i] == ".gemini"
		i += n
	}
	if instructionDotFiles[base] && !inSkill {
		return false, ""
	}
	if autoLoadedFiles[base] {
		return false, "a file a tool loads and runs on its own; refused"
	}
	if inSkill && !strings.HasPrefix(base, ".") {
		// Anything not prose can run.
		return skillFileRuns(base), ""
	}
	switch path.Ext(base) {
	case ".md", ".mdc", ".markdown", ".txt", ".rst":
		return false, ""
	case ".toml":
		if inGeminiCommands {
			// A Gemini command; import gates its "!{cmd}" lines.
			return false, ""
		}
	}
	if inToolDir {
		// .github/workflows/ci.yml runs in CI; only prose is carried there.
		return false, "not prose under a tool's directory; refused"
	}
	if strings.HasPrefix(base, ".") {
		// Dot files configure tools (.envrc, .npmrc, .tool-versions); only
		// samples such as .env.example are carried.
		for _, suf := range []string{".example", ".sample", ".template"} {
			if strings.HasSuffix(base, suf) {
				return true, ""
			}
		}
		return false, "a tool's settings file; refused"
	}
	return true, ""
}

// skillFileRuns is whether a file inside a skill directory can run code:
// anything but prose. A SKILL.md with frontmatter is gated by its content.
func skillFileRuns(base string) bool {
	switch path.Ext(base) {
	case ".md", ".mdc", ".markdown", ".txt", ".rst":
		return false
	}
	return true
}

// matchInstructionDir is how many leading segments of dirs form a known
// instruction directory, or 0.
func matchInstructionDir(dirs []string) int {
	for _, d := range instructionDirs {
		if len(dirs) < len(d) {
			continue
		}
		ok := true
		for j := range d {
			if dirs[j] != d[j] {
				ok = false
				break
			}
		}
		if ok {
			return len(d)
		}
	}
	return 0
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
// directory-scoped instruction with the same name can coexist. A referenced
// file adds its SourceProject, so two projects' notes/x.md are two entries.
func InstructionKey(inst InstructionFile) string {
	scope := inst.Scope
	if scope == "" {
		scope = InstructionScopeGlobal
	}
	pattern := inst.DirectoryPattern
	if scope == InstructionScopeDirectory {
		pattern = NormalizeDirectoryPattern(pattern)
	}
	key := inst.Name + "\x00" + scope + "\x00" + pattern
	if inst.SourceProject != "" {
		key += "\x00" + inst.SourceProject
	}
	return key
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
// behavior. Absolute patterns match against workDir and its ancestors.
// Relative patterns with separators match against suffixes of workDir and its
// ancestors, so "repo/*" can match "/home/user/repo/src". Separator-free
// patterns match against the base name of the original workDir only (no
// ancestor walk), so "example-repo" still works without full path anchoring.
// Returns false on error or when either argument is empty.
func matchesDirectory(pattern, workDir string) bool {
	if pattern == "" || workDir == "" {
		return false
	}
	hasSep := strings.ContainsRune(pattern, '/') || strings.ContainsRune(pattern, filepath.Separator)
	if hasSep {
		// Walk workDir and each ancestor upward.
		p := NormalizeDirectoryPattern(pattern)
		dir := workDir
		for {
			dirSlash := filepath.ToSlash(dir)
			if ok, err := path.Match(p, dirSlash); err == nil && ok {
				return true
			}
			if !path.IsAbs(p) && matchesDirectorySuffix(p, strings.TrimPrefix(dirSlash, "/")) {
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

func matchesDirectorySuffix(pattern, workDir string) bool {
	for candidate := workDir; candidate != ""; {
		if ok, err := path.Match(pattern, candidate); err == nil && ok {
			return true
		}
		idx := strings.IndexRune(candidate, '/')
		if idx < 0 {
			break
		}
		candidate = candidate[idx+1:]
	}
	return false
}

// ResolveEffectiveInstructions merges a flat instruction list using scope precedence:
// local > directory > global. For each unique Name, the highest-rank applicable
// instruction wins. Directory-scoped instructions only apply when their pattern
// matches workDir; an empty workDir disables all directory-scope matching.
// Each project's copy of a referenced file is its own entry.
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
		id := inst.Name + "\x00" + inst.SourceProject
		if existing, ok := best[id]; !ok || rank > existing.rank ||
			(rank == existing.rank && len(inst.DirectoryPattern) > len(existing.inst.DirectoryPattern)) ||
			(rank == existing.rank &&
				len(inst.DirectoryPattern) == len(existing.inst.DirectoryPattern) &&
				inst.DirectoryPattern < existing.inst.DirectoryPattern) {
			best[id] = candidate{inst: inst, rank: rank}
		}
	}

	result := make([]InstructionFile, 0, len(best))
	for _, c := range best {
		result = append(result, c.inst)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].SourceProject < result[j].SourceProject
	})
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
