package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// Every agent tool keeps its user-level setup in its own home directory. This
// file covers the ones setup_assets.go did not: Gemini, the Copilot CLI,
// Cursor and Windsurf MCP configs, Claude's user MCP servers and output
// styles, Codex's global AGENTS.md and prompts, opt-in memory, and every file
// those instruction and settings files name.

const (
	setupAssetRootProviderHome       = "provider_home"
	setupAssetRootProviderGemini     = "provider_gemini"
	setupAssetRootProviderCopilotCLI = "provider_copilot_cli"
	setupAssetRootProviderCursor     = "provider_cursor"
	setupAssetRootProviderWindsurf   = "provider_windsurf"

	// A followed reference is walked this many hops from the file that started
	// it. Documentation that names documentation does not need more, and a
	// generated tree that links itself must stop somewhere.
	maxReferenceDepth = 10
	// A reference to a directory carries at most this many files. A rules
	// directory has a handful; a reference that reaches hundreds named a tree.
	maxDirReferenceFiles = 200
)

// providerRootDirs maps each provider asset root to its directory under the
// user's home. Export reads from it and import writes back to it, so the two
// cannot disagree about where a root lives.
var providerRootDirs = map[string]string{
	setupAssetRootProviderClaude:      ".claude",
	setupAssetRootProviderCodex:       ".codex",
	setupAssetRootProviderCopilot:     filepath.Join(".config", "github-copilot"),
	setupAssetRootProviderClaudeSkill: filepath.Join(".claude", "skills"),
	setupAssetRootProviderCodexSkill:  filepath.Join(".codex", "skills"),
	setupAssetRootProviderGemini:      ".gemini",
	setupAssetRootProviderCopilotCLI:  ".copilot",
	setupAssetRootProviderCursor:      ".cursor",
	setupAssetRootProviderWindsurf:    filepath.Join(".codeium", "windsurf"),
	setupAssetRootProviderHome:        "",
}

// credentialFileNames are login tokens, not settings. They are never collected
// from any directory, with or without --include-secrets: the other machine
// signs in for itself, and a copied token is a session someone else can use.
var credentialFileNames = map[string]bool{
	".credentials.json":    true, // Claude Code
	"auth.json":            true, // Codex
	"oauth_creds.json":     true, // Gemini CLI
	"google_accounts.json": true, // Gemini CLI
}

func isCredentialFile(path string) bool {
	return credentialFileNames[filepath.Base(path)]
}

// homeStartupPaths run code at login or shell start, or grant access. The home
// root is otherwise an open write target, so a bundle from someone else could
// plant one of these; they are never written from a bundle, and never
// collected into one.
var homeStartupPaths = []string{
	".bashrc", ".bash_profile", ".bash_login", ".bash_logout", ".profile",
	".zshrc", ".zshenv", ".zprofile", ".zlogin", ".zlogout",
	".config/fish", ".config/autostart", ".config/systemd", ".config/environment.d",
	"Library/LaunchAgents", ".local/share/applications", ".xprofile", ".xinitrc",
	".pam_environment", ".gitconfig", ".config/git",
}

// homeAssetRefusal returns why a home-relative path may not be carried or
// written, or "". Only .claude.json may sit at the home root: every other
// dotfile there is shell or login state.
func homeAssetRefusal(rel string) string {
	slashed := filepath.ToSlash(filepath.Clean(rel))
	for _, seg := range strings.Split(slashed, "/") {
		if seg != strings.TrimSpace(seg) {
			return "a path segment with leading or trailing whitespace"
		}
	}
	// macOS and Windows home directories are case-insensitive: .BASHRC is
	// .bashrc there.
	folded := strings.ToLower(slashed)
	if reason := agent.SecretBearingReason(folded); reason != "" {
		return reason
	}
	for _, p := range homeStartupPaths {
		p = strings.ToLower(p)
		if folded == p || strings.HasPrefix(folded, p+"/") {
			return "a login or shell startup path; never written from a bundle"
		}
	}
	if !strings.Contains(folded, "/") && strings.HasPrefix(folded, ".") && folded != ".claude.json" {
		return "a dotfile at the home root; never written from a bundle"
	}
	return ""
}

// homeAssetWritable decides whether import writes a home-root asset, and
// returns its cleaned relative path. The home root is open-ended, so it is an
// allowlist: ~/.claude.json (merged) and prose. A referenced script or config
// file is staged for review and never written: placed on PATH or named by a
// hook, a file from a bundle runs code.
func homeAssetWritable(logicalPath string) (string, error) {
	rel, err := sanitizeAssetRelativePath(logicalPath)
	if err != nil {
		return "", err
	}
	// The path the bundle gives must already be the clean form. Any
	// difference (" .claude.json", "./x", "a//b") is a second spelling, and
	// checks made on one spelling must hold for the file written.
	if rel != logicalPath {
		return "", fmt.Errorf("%q: not in clean form (%q); refused", logicalPath, rel)
	}
	if reason := homeAssetRefusal(rel); reason != "" {
		return "", fmt.Errorf("%s: %s", rel, reason)
	}
	if rel != ".claude.json" && !isProseFile(rel) {
		return "", fmt.Errorf("%s: not prose; staged for review, copy it by hand if you want it", rel)
	}
	return rel, nil
}

type providerFileSpec struct {
	root      string
	rel       string // relative to the root's directory
	sensitive bool
	dir       bool
}

// extraProviderSpecs are the user-level files and directories added on top of
// collectProviderHomeAssets. Settings files that can hold tokens are
// sensitive; prose and command definitions are not.
func extraProviderSpecs() []providerFileSpec {
	return []providerFileSpec{
		{root: setupAssetRootProviderClaude, rel: "settings.local.json", sensitive: true},
		{root: setupAssetRootProviderClaude, rel: "output-styles", dir: true},
		{root: setupAssetRootProviderClaude, rel: "hooks", dir: true},
		{root: setupAssetRootProviderCodex, rel: "AGENTS.md"},
		{root: setupAssetRootProviderCodex, rel: "prompts", dir: true},
		{root: setupAssetRootProviderGemini, rel: "GEMINI.md"},
		{root: setupAssetRootProviderGemini, rel: "settings.json", sensitive: true},
		{root: setupAssetRootProviderGemini, rel: "commands", dir: true},
		{root: setupAssetRootProviderCopilotCLI, rel: "config.json", sensitive: true},
		{root: setupAssetRootProviderCopilotCLI, rel: "mcp-config.json", sensitive: true},
		{root: setupAssetRootProviderCopilotCLI, rel: "agents", dir: true},
		{root: setupAssetRootProviderCursor, rel: "mcp.json", sensitive: true},
		{root: setupAssetRootProviderWindsurf, rel: "mcp_config.json", sensitive: true},
	}
}

func collectExtraProviderAssets(homeDir string, includeSecrets bool) ([]SetupAsset, []string, error) {
	var assets []SetupAsset
	var warnings []string
	for _, spec := range extraProviderSpecs() {
		base := filepath.Join(homeDir, providerRootDirs[spec.root])
		if spec.dir {
			found, warns, err := collectDirFiles(filepath.Join(base, spec.rel),
				setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root,
				filepath.ToSlash(spec.rel), "", spec.sensitive, includeSecrets)
			if err != nil {
				return nil, warnings, err
			}
			assets = append(assets, found...)
			warnings = append(warnings, quietMissingRoots(warns)...)
			continue
		}
		asset, _, err := loadSetupAsset(filepath.Join(base, spec.rel),
			setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root,
			filepath.ToSlash(spec.rel), "", spec.sensitive, includeSecrets, true)
		if err != nil {
			return nil, warnings, err
		}
		if !asset.Missing {
			assets = append(assets, asset)
		}
	}

	userMCP, warn, err := collectClaudeUserMCPServers(homeDir, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	if userMCP != nil {
		assets = append(assets, *userMCP)
	}
	return assets, warnings, nil
}

// quietMissingRoots drops the "not found" note for optional directories. Most
// machines have one or two of these tools, and a warning per absent tool
// buries the warnings that matter.
func quietMissingRoots(warnings []string) []string {
	out := warnings[:0]
	for _, w := range warnings {
		if strings.HasPrefix(w, "optional asset root ") && strings.HasSuffix(w, " not found; skipping") {
			continue
		}
		out = append(out, w)
	}
	return out
}

// claudeUserConfigMergeKeys are the keys of ~/.claude.json that are setup.
// The rest of that file is per-machine state: the signed-in account, per-
// project trust and history, caches. Import merges these keys into the
// file on the other machine and leaves everything else in it alone.
var claudeUserConfigMergeKeys = []string{"mcpServers"}

// collectClaudeUserMCPServers carries the user-scope MCP servers from
// ~/.claude.json, and nothing else from that file.
func collectClaudeUserMCPServers(homeDir string, includeSecrets bool) (*SetupAsset, string, error) {
	path := filepath.Join(homeDir, ".claude.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Sprintf("skipping %s: not a regular file", path), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Sprintf("skipping %s: not a JSON object", path), nil
	}
	kept := map[string]json.RawMessage{}
	for _, k := range claudeUserConfigMergeKeys {
		if v, ok := doc[k]; ok {
			kept[k] = v
		}
	}
	if len(kept) == 0 {
		return nil, "", nil
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return nil, "", err
	}
	asset := SetupAsset{
		Kind:        setupAssetKindProviderFile,
		Origin:      setupAssetOriginProviderHome,
		LogicalRoot: setupAssetRootProviderHome,
		LogicalPath: ".claude.json",
		SourcePath:  path,
		// MCP server entries carry env and headers, which hold tokens.
		Sensitive: true,
		SizeBytes: int64(len(data)),
		MergeKeys: claudeUserConfigMergeKeys,
	}
	if !includeSecrets {
		out, pointers, ok := redactJSONSecrets(data)
		if !ok {
			asset.Redacted = true
			return &asset, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", path), nil
		}
		data = out
		asset.RedactedFields = pointers
		asset.SizeBytes = int64(len(data))
	}
	asset.Content = data
	asset.ContentPresent = true
	asset.SHA256 = hashSetupAssetContent(data)
	return &asset, "", nil
}

// collectMemoryAssets carries what agents have written down about the user and
// their projects: Claude's per-project memory and Codex's memories. Opt-in,
// because memory is personal and the person exporting may not want it on the
// other machine.
func collectMemoryAssets(homeDir string, includeSecrets bool) ([]SetupAsset, []string, error) {
	var assets []SetupAsset
	var warnings []string
	claudeProjects := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(claudeProjects)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("reading %s: %w", claudeProjects, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		prefix := filepath.ToSlash(filepath.Join("projects", e.Name(), "memory"))
		found, warns, err := collectDirFiles(filepath.Join(claudeProjects, e.Name(), "memory"),
			setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderClaude,
			prefix, "", false, includeSecrets)
		if err != nil {
			return nil, warnings, err
		}
		assets = append(assets, found...)
		warnings = append(warnings, quietMissingRoots(warns)...)
	}
	found, warns, err := collectDirFiles(filepath.Join(homeDir, ".codex", "memories"),
		setupAssetKindProviderFile, setupAssetOriginProviderHome, setupAssetRootProviderCodex,
		"memories", "", false, includeSecrets)
	if err != nil {
		return nil, warnings, err
	}
	assets = append(assets, found...)
	warnings = append(warnings, quietMissingRoots(warns)...)
	return assets, warnings, nil
}

// --- files named by user-level instructions and settings -----------------

// userInstructionSeeds are the user-level instruction files whose references
// are followed. A CLAUDE.md that says "@~/.claude/team-rules.md" arrives
// without team-rules.md otherwise.
func userInstructionSeeds(assets []SetupAsset) []SetupAsset {
	var seeds []SetupAsset
	for _, a := range assets {
		if !a.ContentPresent {
			continue
		}
		switch {
		case a.LogicalRoot == setupAssetRootProviderClaude && (a.LogicalPath == "CLAUDE.md" ||
			strings.HasPrefix(a.LogicalPath, "agents/") || strings.HasPrefix(a.LogicalPath, "commands/") ||
			strings.HasPrefix(a.LogicalPath, "output-styles/")):
			seeds = append(seeds, a)
		case a.LogicalRoot == setupAssetRootProviderCodex && a.LogicalPath == "AGENTS.md":
			seeds = append(seeds, a)
		case a.LogicalRoot == setupAssetRootProviderGemini && a.LogicalPath == "GEMINI.md":
			seeds = append(seeds, a)
		}
	}
	return seeds
}

// settingsCommandPaths returns the files a settings.json runs: hook commands
// and the status line. Each word of a command that names a path under the
// home directory counts; the rest of the command is not a file.
func settingsCommandPaths(content []byte, homeDir string) []string {
	var doc map[string]any
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var commands []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, inner := range t {
				if s, ok := inner.(string); ok && k == "command" {
					commands = append(commands, s)
					continue
				}
				walk(inner)
			}
		case []any:
			for _, inner := range t {
				walk(inner)
			}
		}
	}
	walk(doc["hooks"])
	walk(doc["statusLine"])

	seen := map[string]bool{}
	var out []string
	for _, c := range commands {
		for _, word := range strings.Fields(c) {
			word = strings.Trim(word, `"'`)
			p, ok := expandHomePath(word, homeDir)
			if !ok || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// expandHomePath turns ~/x, $HOME/x, ${HOME}/x and <home>/x into an absolute
// path under home. Anything else is not a home path.
func expandHomePath(word, homeDir string) (string, bool) {
	slashed := filepath.ToSlash(word)
	for _, prefix := range []string{"~/", "$HOME/", "${HOME}/"} {
		if strings.HasPrefix(slashed, prefix) {
			return filepath.Join(homeDir, filepath.FromSlash(strings.TrimPrefix(slashed, prefix))), true
		}
	}
	home := filepath.Clean(homeDir)
	if filepath.IsAbs(word) {
		clean := filepath.Clean(word)
		if strings.HasPrefix(clean, home+string(filepath.Separator)) {
			return clean, true
		}
	}
	return "", false
}

// homeRefResult is what became of one path named by a user-level file.
type homeRefResult struct {
	abs    string // set when taken
	isDir  bool
	reason string // set when declined
	absent bool
}

// resolveHomeReference decides one reference named by a file in fromDir. It
// may point anywhere under home, through ~, $HOME, an absolute path or a
// relative one, and nowhere else.
func resolveHomeReference(homeDir, fromDir, raw string) homeRefResult {
	clean := strings.TrimPrefix(raw, "./")
	var abs string
	if p, ok := expandHomePath(clean, homeDir); ok {
		abs = p
	} else if isRootedPath(clean) || strings.HasPrefix(clean, "~") {
		return homeRefResult{reason: "outside the home directory"}
	} else {
		abs = filepath.Join(fromDir, filepath.FromSlash(clean))
	}
	realHome, err := filepath.EvalSymlinks(homeDir)
	if err != nil {
		realHome = filepath.Clean(homeDir)
	}
	if !isUnder(filepath.Clean(homeDir), abs) && !isUnder(realHome, abs) {
		return homeRefResult{reason: "outside the home directory"}
	}
	if reason := homeAssetRefusal(mustRel(homeDir, abs)); reason != "" {
		return homeRefResult{reason: reason}
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return homeRefResult{absent: true, reason: "named but not present"}
	}
	if !isUnder(realHome, real) {
		return homeRefResult{reason: "resolves outside the home directory"}
	}
	// Again on the resolved path: a symlink named notes.md can point at .netrc.
	if reason := homeAssetRefusal(mustRel(realHome, real)); reason != "" {
		return homeRefResult{reason: reason}
	}
	info, err := os.Stat(real)
	if err != nil {
		return homeRefResult{absent: true, reason: "named but not present"}
	}
	if info.IsDir() {
		return homeRefResult{abs: abs, isDir: true}
	}
	if !info.Mode().IsRegular() {
		return homeRefResult{reason: "not a regular file"}
	}
	if info.Size() > agent.MaxReferenceBytes {
		return homeRefResult{reason: fmt.Sprintf("%d bytes, over the %d byte ceiling", info.Size(), agent.MaxReferenceBytes)}
	}
	return homeRefResult{abs: abs}
}

// isRootedPath is an absolute path on any OS. On Windows "/etc/hosts" has no
// drive and filepath.IsAbs says no, but it names the root of the current
// drive, not a path relative to the file that wrote it.
func isRootedPath(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && ((p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z')
}

func isUnder(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// collectHomeReferencedFiles follows what user-level instruction files name,
// and what the settings files run, to files under the home directory. A file
// already carried under its tool's own root is not carried twice.
func collectHomeReferencedFiles(homeDir string, carried []SetupAsset, includeSecrets bool) ([]SetupAsset, []DeclinedAsset, []string, error) {
	have := map[string]bool{}
	for _, a := range carried {
		if a.SourcePath != "" {
			have[filepath.Clean(a.SourcePath)] = true
		}
	}
	var assets []SetupAsset
	var declined []DeclinedAsset
	var warnings []string

	type item struct {
		asset SetupAsset
		depth int
	}
	var queue []item
	for _, s := range userInstructionSeeds(carried) {
		queue = append(queue, item{s, 0})
	}

	take := func(abs string, depth int) error {
		abs = filepath.Clean(abs)
		if have[abs] {
			return nil
		}
		have[abs] = true
		rel, err := filepath.Rel(homeDir, abs)
		if err != nil {
			return nil
		}
		// Prose is carried as is. Anything else a reference reaches (a script,
		// a config file) can hold a secret, so it is redacted without
		// --include-secrets, like every other settings file.
		asset, warn, err := loadSetupAsset(abs, setupAssetKindProviderFile, setupAssetOriginProviderHome,
			setupAssetRootProviderHome, filepath.ToSlash(rel), "", !isProseFile(abs), includeSecrets, true)
		if err != nil {
			return err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if asset.Missing {
			return nil
		}
		assets = append(assets, asset)
		if asset.ContentPresent && depth < maxReferenceDepth && isTextReference(abs) {
			queue = append(queue, item{asset, depth + 1})
		}
		return nil
	}

	takeDir := func(dir, by string, depth int) error {
		n := 0
		return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != dir && skipReferenceDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 || homeAssetRefusal(mustRel(homeDir, p)) != "" {
				return nil
			}
			if n >= maxDirReferenceFiles {
				rel, _ := filepath.Rel(homeDir, dir)
				declined = append(declined, DeclinedAsset{
					Path:     "~/" + filepath.ToSlash(rel),
					Category: DeclinedByPolicy,
					Reason:   fmt.Sprintf("named by %s; more than %d files, the rest left behind", by, maxDirReferenceFiles),
				})
				return filepath.SkipAll
			}
			info, err := d.Info()
			if err != nil || info.Size() > agent.MaxReferenceBytes {
				return nil
			}
			n++
			return take(p, depth)
		})
	}

	for _, settings := range carried {
		if settings.LogicalPath != "settings.json" || settings.SourcePath == "" ||
			(settings.LogicalRoot != setupAssetRootProviderClaude && settings.LogicalRoot != setupAssetRootProviderGemini) {
			continue
		}
		// Read from disk, not from the asset: without --include-secrets the
		// settings file is redacted, and the scripts it runs are not secrets.
		content, err := os.ReadFile(settings.SourcePath)
		if err != nil {
			continue
		}
		for _, p := range settingsCommandPaths(content, homeDir) {
			r := resolveHomeReference(homeDir, homeDir, p)
			if r.abs == "" || r.isDir {
				continue
			}
			if err := take(r.abs, maxReferenceDepth); err != nil {
				return nil, nil, warnings, err
			}
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		fromDir := filepath.Dir(cur.asset.SourcePath)
		by := "~/" + filepath.ToSlash(mustRel(homeDir, cur.asset.SourcePath))
		for _, raw := range agent.FindReferences(string(cur.asset.Content)) {
			r := resolveHomeReference(homeDir, fromDir, raw)
			switch {
			case r.absent:
				declined = append(declined, DeclinedAsset{Path: raw, Category: DeclinedAbsent,
					Reason: fmt.Sprintf("named by %s and not present", by)})
			case r.reason != "":
				declined = append(declined, DeclinedAsset{Path: raw, Category: DeclinedByPolicy,
					Reason: fmt.Sprintf("named by %s: %s", by, r.reason)})
			case r.isDir:
				if err := takeDir(r.abs, by, cur.depth); err != nil {
					return nil, nil, warnings, err
				}
			default:
				if err := take(r.abs, cur.depth); err != nil {
					return nil, nil, warnings, err
				}
			}
		}
	}
	return assets, dedupeDeclined(declined), warnings, nil
}

func mustRel(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return rel
}

func skipReferenceDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "target", ".venv", "__pycache__":
		return true
	}
	return false
}

// isProseFile is whether a file is documentation rather than code or config.
func isProseFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".mdc", ".markdown", ".txt", ".rst":
		return true
	}
	return false
}

// isTextReference is whether a carried file is worth reading for further
// references. Markdown and plain text name other files; scripts and data do
// not in a form worth following.
func isTextReference(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".mdc", ".txt", ".markdown":
		return true
	}
	return filepath.Ext(p) == ""
}

// --- project scope -------------------------------------------------------

// projectInstructionDirs are per-tool rule and command directories inside a
// project. They are instructions, not settings, so they are carried.
var projectInstructionDirs = []string{
	filepath.Join(".github", "instructions"),
	filepath.Join(".github", "prompts"),
	filepath.Join(".cursor", "rules"),
	filepath.Join(".windsurf", "rules"),
	filepath.Join(".gemini", "commands"),
	".clinerules",
}

// projectInstructionFiles are single instruction files with no vault name.
var projectInstructionFiles = []string{
	"CONVENTIONS.md",
}

// projectRepoScopeSettings are each tool's repository-scope settings. They
// belong to the repository and travel with it through git, so an export
// lists them and does not carry them: carrying them would overwrite the
// repository's own copy on the other machine with this machine's.
var projectRepoScopeSettings = []string{
	filepath.Join(".claude", "settings.json"),
	".mcp.json",
	filepath.Join(".gemini", "settings.json"),
	filepath.Join(".codex", "config.toml"),
	filepath.Join(".cursor", "mcp.json"),
	filepath.Join(".vscode", "mcp.json"),
}

func isProjectRepoScopeSetting(logical string) bool {
	for _, p := range projectRepoScopeSettings {
		if filepath.ToSlash(p) == logical {
			return true
		}
	}
	return false
}

func declineProjectRepoScopeSettings(projectDir string) []DeclinedAsset {
	var out []DeclinedAsset
	for _, rel := range projectRepoScopeSettings {
		info, err := os.Lstat(filepath.Join(projectDir, rel))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, DeclinedAsset{
			Path:     filepath.ToSlash(rel),
			Category: DeclinedRepoScope,
			Reason:   "repository-scope settings travel with the repository; listed, not exported",
		})
	}
	return out
}

func collectProjectInstructionExtras(projectDir string, includeSecrets bool) ([]SetupAsset, []string, error) {
	var assets []SetupAsset
	var warnings []string
	for _, rel := range projectInstructionDirs {
		full := filepath.Join(projectDir, rel)
		info, err := os.Lstat(full)
		if err != nil {
			continue
		}
		slashed := filepath.ToSlash(rel)
		if info.Mode().IsRegular() {
			asset, warn, err := loadSetupAsset(full, setupAssetKindProjectFile, setupAssetOriginProjectLocal,
				setupAssetRootProject, slashed, slashed, false, includeSecrets, true)
			if err != nil {
				return nil, warnings, err
			}
			if warn != "" {
				warnings = append(warnings, warn)
			}
			assets = append(assets, asset)
			continue
		}
		found, warns, err := collectDirFiles(full, setupAssetKindProjectFile, setupAssetOriginProjectLocal,
			setupAssetRootProject, slashed, slashed, false, includeSecrets)
		if err != nil {
			return nil, warnings, err
		}
		assets = append(assets, found...)
		warnings = append(warnings, warns...)
	}
	for _, rel := range projectInstructionFiles {
		asset, _, err := loadSetupAsset(filepath.Join(projectDir, rel), setupAssetKindProjectFile,
			setupAssetOriginProjectLocal, setupAssetRootProject, rel, rel, false, includeSecrets, true)
		if err != nil {
			return nil, warnings, err
		}
		if !asset.Missing {
			assets = append(assets, asset)
		}
	}
	return assets, warnings, nil
}

// collectProjectReferenceClosure carries every file the project's instruction
// files name, and every file those name, to maxReferenceDepth hops. A named
// directory carries its files. What cannot be taken is recorded with the
// reason, so the other machine can tell a refusal from an absence.
func collectProjectReferenceClosure(projectDir string, seeds []SetupAsset, includeSecrets bool) ([]SetupAsset, []DeclinedAsset, []string, error) {
	have := map[string]bool{}
	for _, s := range seeds {
		have[filepath.ToSlash(s.LogicalPath)] = true
	}
	type item struct {
		asset SetupAsset
		depth int
	}
	var queue []item
	for _, s := range seeds {
		if s.ContentPresent {
			queue = append(queue, item{s, 0})
		}
	}
	var assets []SetupAsset
	var declined []DeclinedAsset
	var warnings []string

	take := func(rel string, depth int) error {
		if have[rel] {
			return nil
		}
		have[rel] = true
		if isProjectRepoScopeSetting(rel) {
			return nil
		}
		asset, warn, err := loadSetupAsset(filepath.Join(projectDir, filepath.FromSlash(rel)),
			setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject,
			rel, rel, false, includeSecrets, true)
		if err != nil {
			return err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if asset.Missing {
			return nil
		}
		assets = append(assets, asset)
		if asset.ContentPresent && depth < maxReferenceDepth && isTextReference(rel) {
			queue = append(queue, item{asset, depth + 1})
		}
		return nil
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		base := filepath.Dir(filepath.FromSlash(cur.asset.LogicalPath))
		for _, ref := range agent.ResolveReferences(projectDir, base, string(cur.asset.Content)) {
			switch ref.Status {
			case agent.ReferenceTaken:
				if err := take(ref.Rel, cur.depth); err != nil {
					return nil, nil, warnings, err
				}
			case agent.ReferenceNotFound:
				declined = append(declined, DeclinedAsset{Path: ref.Raw, Category: DeclinedAbsent,
					Reason: fmt.Sprintf("named by %s and not present", cur.asset.LogicalPath)})
			case agent.ReferenceRefused:
				declined = append(declined, DeclinedAsset{Path: ref.Raw, Category: DeclinedByPolicy,
					Reason: fmt.Sprintf("named by %s: %s", cur.asset.LogicalPath, ref.Reason)})
			case agent.ReferenceSkipped:
				rel, ok := projectDirReference(projectDir, base, ref.Raw)
				if !ok {
					continue
				}
				n := 0
				err := filepath.WalkDir(filepath.Join(projectDir, filepath.FromSlash(rel)), func(p string, d os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if d.IsDir() {
						if skipReferenceDir(d.Name()) {
							return filepath.SkipDir
						}
						return nil
					}
					if d.Type()&os.ModeSymlink != 0 || agent.SecretBearingReason(mustRel(projectDir, p)) != "" {
						return nil
					}
					if n >= maxDirReferenceFiles {
						declined = append(declined, DeclinedAsset{Path: rel, Category: DeclinedByPolicy,
							Reason: fmt.Sprintf("named by %s; more than %d files, the rest left behind", cur.asset.LogicalPath, maxDirReferenceFiles)})
						return filepath.SkipAll
					}
					info, err := d.Info()
					if err != nil || info.Size() > agent.MaxReferenceBytes {
						return nil
					}
					fileRel, err := filepath.Rel(projectDir, p)
					if err != nil {
						return nil
					}
					n++
					return take(filepath.ToSlash(fileRel), cur.depth)
				})
				if err != nil {
					return nil, nil, warnings, err
				}
			}
		}
	}
	return assets, declined, warnings, nil
}

// projectDirReference returns a directory reference relative to the project
// when it names a real directory inside it, outside the skipped trees.
func projectDirReference(projectDir, base, raw string) (string, bool) {
	clean := strings.TrimSuffix(strings.TrimPrefix(raw, "./"), "/")
	if clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "~") {
		return "", false
	}
	root, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", false
	}
	full := filepath.Join(projectDir, base, filepath.FromSlash(clean))
	real, err := filepath.EvalSymlinks(full)
	if err != nil || !isUnder(root, real) {
		return "", false
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", false
	}
	rel, err := filepath.Rel(root, real)
	if err != nil {
		return "", false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if skipReferenceDir(seg) {
			return "", false
		}
	}
	return filepath.ToSlash(rel), true
}

// dedupeAssetsByLogicalPath keeps the first asset per root and path. Two
// collectors can reach one file: a reference from AGENTS.md and the walk of
// .claude/ both take .claude/agents/reviewer.md.
func dedupeAssetsByLogicalPath(in []SetupAsset) []SetupAsset {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, a := range in {
		key := a.LogicalRoot + "\x00" + filepath.ToSlash(a.LogicalPath)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}

// mergeJSONKeysIntoFile sets keys from incoming into the JSON object at path
// and keeps every other key there. A missing or empty file takes incoming as
// is; a file that is not a JSON object is refused rather than overwritten.
func mergeJSONKeysIntoFile(path string, incoming []byte, keys []string) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) || (err == nil && len(strings.TrimSpace(string(existing))) == 0) {
		return incoming, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object; not overwriting it", path)
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &in); err != nil {
		return nil, fmt.Errorf("bundle content for %s is not a JSON object", path)
	}
	for _, k := range keys {
		if v, ok := in[k]; ok {
			doc[k] = v
		}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// projectAssetRefusal returns why a staged project file is not written into a
// project, or "". Written: prose anywhere, and the files and directories
// agent tools read instructions from. Never written: anything that runs code
// or configures the repository (.git, CI workflows, git hook directories,
// repository settings), credential files, and other non-prose files, which
// stay staged for review.
func projectAssetRefusal(rel string) string {
	slashed := filepath.ToSlash(filepath.Clean(rel))
	folded := strings.ToLower(slashed)
	for _, seg := range strings.Split(slashed, "/") {
		if seg != strings.TrimSpace(seg) {
			return "a path segment with leading or trailing whitespace"
		}
		// Windows drops trailing dots (.git. is .git) and reads a colon as
		// an alternate data stream.
		if strings.HasSuffix(seg, ".") && seg != "." && seg != ".." || strings.Contains(seg, ":") {
			return "a path segment Windows would read as another name"
		}
		switch strings.ToLower(seg) {
		case ".git", ".githooks", ".husky":
			return "runs code or configures git; never written from a bundle"
		}
	}
	if strings.HasPrefix(folded, ".github/workflows/") || strings.HasPrefix(folded, ".github/actions/") {
		return "a CI definition; never written from a bundle"
	}
	if isProjectRepoScopeSetting(slashed) || (strings.HasPrefix(folded, ".claude/") && strings.HasSuffix(folded, "/settings.json")) {
		return "repository-scope settings; never written from a bundle"
	}
	if reason := agent.SecretBearingReason(folded); reason != "" {
		return reason
	}
	if isProseFile(slashed) || isProjectInstructionPath(slashed) {
		return ""
	}
	return "not prose; left staged for review"
}

// plainTextRuleFiles are instruction files with no extension that hold rule
// text and nothing a tool executes.
var plainTextRuleFiles = map[string]bool{".cursorrules": true, ".windsurfrules": true, ".clinerules": true}

// isProjectInstructionPath is a non-prose file agent tools read as
// instructions.
//
// Prose is already allowed by the caller. This adds only what is text a
// tool reads and never runs: the extensionless rule files, and Gemini's
// TOML commands. A script under skills/ or a config such as
// .aider.conf.yml (its lint-cmd runs) stays staged.
func isProjectInstructionPath(slashed string) bool {
	if plainTextRuleFiles[slashed] {
		return true
	}
	if strings.HasPrefix(slashed, ".clinerules/") && !strings.Contains(path.Base(slashed), ".") {
		return true
	}
	return strings.HasPrefix(slashed, ".gemini/commands/") && strings.EqualFold(path.Ext(slashed), ".toml")
}

// sameFileContent reports whether dest exists and, if so, whether it holds
// the same bytes as src.
func sameFileContent(src, dest string) (same bool, exists bool) {
	d, err := os.ReadFile(dest)
	if err != nil {
		return false, false
	}
	s, err := os.ReadFile(src)
	if err != nil {
		return false, true
	}
	return bytes.Equal(s, d), true
}
