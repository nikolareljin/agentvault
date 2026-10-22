package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	// GitHub OAuth tokens of the Copilot editor plugins, in
	// ~/.config/github-copilot.
	"hosts.json": true,
	"apps.json":  true,
}

func isCredentialFile(path string) bool {
	// Lower-cased: macOS and Windows file systems ignore case.
	// Trailing dots and spaces too: Windows drops them, so "auth.json." is
	// auth.json there.
	return credentialFileNames[strings.TrimRight(strings.ToLower(filepath.Base(path)), ". ")]
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
	// Windows drops trailing dots from a segment: ".config/fish." is
	// .config/fish there.
	segs := strings.Split(folded, "/")
	for i, seg := range segs {
		if seg != "." && seg != ".." {
			segs[i] = strings.TrimRight(seg, ".")
		}
	}
	trimmed := strings.Join(segs, "/")
	for _, p := range homeStartupPaths {
		p = strings.ToLower(p)
		if trimmed == p || strings.HasPrefix(trimmed, p+"/") {
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
	// Export never carries a tool's memory or session files from the home
	// root, so a bundle that does was not made by export.
	if reason := agentRootRefusal(strings.ToLower(rel)); reason != "" {
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
		// Hook scripts are code and can hold tokens, like any referenced script.
		{root: setupAssetRootProviderClaude, rel: "hooks", dir: true, sensitive: true},
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

func collectExtraProviderAssets(homeDir string, includeSecrets bool) ([]SetupAsset, []string, []DeclinedAsset, error) {
	var assets []SetupAsset
	var warnings []string
	var declined []DeclinedAsset
	for _, spec := range extraProviderSpecs() {
		base := filepath.Join(homeDir, providerRootDirs[spec.root])
		if spec.dir {
			found, warns, err := collectDirFiles(filepath.Join(base, spec.rel),
				setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root,
				filepath.ToSlash(spec.rel), "", spec.sensitive, includeSecrets)
			if err != nil {
				return nil, warnings, declined, err
			}
			assets = append(assets, found...)
			warnings = append(warnings, quietMissingRoots(warns)...)
			continue
		}
		if warn, skip := symlinkedAsset(filepath.Join(base, spec.rel)); skip {
			warnings = append(warnings, warn)
			continue
		}
		if spec.root == setupAssetRootProviderCopilotCLI && spec.rel == "config.json" {
			asset, d, warn, err := collectCopilotConfig(filepath.Join(base, spec.rel), includeSecrets)
			if err != nil {
				return nil, warnings, declined, err
			}
			if warn != "" {
				warnings = append(warnings, warn)
			}
			declined = append(declined, d...)
			if asset != nil {
				assets = append(assets, *asset)
			}
			continue
		}
		asset, _, err := loadSetupAsset(filepath.Join(base, spec.rel),
			setupAssetKindProviderFile, setupAssetOriginProviderHome, spec.root,
			filepath.ToSlash(spec.rel), "", spec.sensitive, includeSecrets, true)
		if err != nil {
			return nil, warnings, declined, err
		}
		if !asset.Missing {
			assets = append(assets, asset)
		}
	}

	userMCP, warn, err := collectClaudeUserMCPServers(homeDir, includeSecrets)
	if err != nil {
		return nil, warnings, declined, err
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	if userMCP != nil {
		assets = append(assets, *userMCP)
	}
	return assets, warnings, declined, nil
}

// copilotLoginKeys are the keys of ~/.copilot/config.json that hold the
// signed-in account and, without a keychain, its tokens. Any key that looks
// like a token is removed too, at any depth.
var copilotLoginKeys = map[string]bool{
	"copilot_tokens": true, "logged_in_users": true, "last_logged_in_user": true,
}

func isLoginKey(k string) bool {
	if copilotLoginKeys[strings.ToLower(k)] {
		return true
	}
	l := strings.ToLower(k)
	// Counts and limits are settings: max_tokens, maxOutputTokens, tokenLimit.
	n := strings.NewReplacer("_", "", "-", "").Replace(l)
	// Only as a count: "minted_token" starts with "min" and is a token.
	if (strings.HasPrefix(n, "max") || strings.HasPrefix(n, "min")) && strings.HasSuffix(n, "tokens") {
		return false
	}
	// A bare "...tokens" suffix is not exempt: access_tokens is a token map.
	for _, suffix := range []string{"tokenlimit", "tokencount", "maxtokens"} {
		if strings.HasSuffix(n, suffix) {
			return false
		}
	}
	for _, w := range []string{"token", "secret", "password", "credential", "oauth"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func stripLoginKeys(v any) (any, int) {
	n := 0
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			if isLoginKey(k) {
				delete(t, k)
				n++
				continue
			}
			var m int
			t[k], m = stripLoginKeys(inner)
			n += m
		}
	case []any:
		for i := range t {
			var m int
			t[i], m = stripLoginKeys(t[i])
			n += m
		}
	}
	return v, n
}

// decodeJSONObject decodes a JSON object keeping numbers as written: a
// float64 round trip would change an ID such as 12345678901234567890.
func decodeJSONObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	return doc, nil
}

// stripCopilotLoginState returns config.json without its login keys and how
// many were removed. Content that is not a JSON object is an error: it is
// never carried or written raw.
func stripCopilotLoginState(raw []byte) ([]byte, int, error) {
	doc, err := decodeJSONObject(raw)
	if err != nil {
		return nil, 0, err
	}
	_, n := stripLoginKeys(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	return out, n, err
}

// collectCopilotConfig carries ~/.copilot/config.json without its login
// state, even with --include-secrets: a copied token is a session someone
// else can use.
func collectCopilotConfig(path string, includeSecrets bool) (*SetupAsset, []DeclinedAsset, string, error) {
	info, err := lstatPinned(path)
	if os.IsNotExist(err) {
		return nil, nil, "", nil
	}
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	shown := "~/.copilot/config.json"
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Sprintf("skipping %s: not a regular file", path), nil
	}
	raw, changed, err := readStattedFile(path, info, maxSetupAssetBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	if changed != "" {
		return nil, nil, fmt.Sprintf("skipping %s: %s", path, changed), nil
	}
	data, n, err := stripCopilotLoginState(raw)
	if err != nil {
		return nil, []DeclinedAsset{{Path: shown, Category: DeclinedByPolicy,
			Reason: "not a JSON object; may hold login tokens, so not carried"}}, "", nil
	}
	var declined []DeclinedAsset
	if n > 0 {
		declined = append(declined, DeclinedAsset{Path: shown, Category: DeclinedByPolicy,
			Reason: fmt.Sprintf("carried in part: %d login key(s) removed; sign in on the other machine", n)})
	}
	asset := SetupAsset{
		Kind:        setupAssetKindProviderFile,
		Origin:      setupAssetOriginProviderHome,
		LogicalRoot: setupAssetRootProviderCopilotCLI,
		LogicalPath: "config.json",
		SourcePath:  path,
		Sensitive:   true,
		SizeBytes:   int64(len(data)),
	}
	if !includeSecrets {
		// Login keys are gone; other secret values are blanked, not the file.
		out, pointers, ok := redactJSONSecrets(data)
		if !ok {
			asset.Redacted = true
			return &asset, declined, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", path), nil
		}
		data = out
		asset.RedactedFields = pointers
		asset.SizeBytes = int64(len(data))
	}
	asset.Content = data
	asset.ContentPresent = true
	asset.SHA256 = hashSetupAssetContent(data)
	return &asset, declined, "", nil
}

// keepLocalCopilotLogin puts this machine's own login keys from the config.json
// at dest back into incoming, so applying a bundle does not sign anyone out.
func keepLocalCopilotLogin(dest string, incoming []byte) []byte {
	existing, err := readExistingFile(dest)
	if err != nil {
		return incoming
	}
	local, err := decodeJSONObject(existing)
	if err != nil {
		return incoming
	}
	doc, err := decodeJSONObject(incoming)
	if err != nil {
		return incoming
	}
	restoreLoginKeys(local, doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return incoming
	}
	return out
}

// restoreLoginKeys copies every login key of local into doc, at the depth
// stripLoginKeys removed it from: a token nested in an object is kept too.
func restoreLoginKeys(local, doc map[string]any) {
	for k, v := range local {
		if isLoginKey(k) {
			doc[k] = v
			continue
		}
		lm, ok1 := v.(map[string]any)
		dm, ok2 := doc[k].(map[string]any)
		if ok1 && ok2 {
			restoreLoginKeys(lm, dm)
		}
	}
}

// symlinkedAsset reports a symlink, or anything else that is not a regular
// file, where a single optional file is expected. loadSetupAsset refuses
// them with an error, which would end the whole export over one dotfiles
// link or a CLAUDE.md linked to AGENTS.md; they are skipped with a warning
// instead.
func symlinkedAsset(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().IsRegular() {
		return "", false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Sprintf("skipping symlink asset %q; copy its target by hand if you want it", path), true
	}
	// A pipe or device would block the export or never end; a directory is
	// not a file to carry.
	return fmt.Sprintf("skipping %q: not a regular file", path), true
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
	info, err := lstatPinned(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Sprintf("skipping %s: not a regular file", path), nil
	}
	// ~/.claude.json grows with project history; the cap is for a file
	// swapped or grown while read, not for its normal size.
	raw, changed, err := readStattedFile(path, info, 64<<20)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	if changed != "" {
		return nil, fmt.Sprintf("skipping %s: %s", path, changed), nil
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
	abs    string // set when taken; the path as named, under home
	real   string // the file read: abs with symlinks resolved
	isDir  bool
	reason string // set when declined
	absent bool
}

// resolveHomeReference decides one reference named by a file in fromDir. It
// may point anywhere under home, through ~, $HOME, an absolute path or a
// relative one, and nowhere else.
func resolveHomeReference(homeDir, fromDir, raw string, aliases []agentRootAlias) homeRefResult {
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
	cleanHome := filepath.Clean(homeDir)
	if !isUnder(cleanHome, abs) {
		if !isUnder(realHome, abs) {
			return homeRefResult{reason: "outside the home directory"}
		}
		// Named through the resolved home (/private/var/... for /var/...):
		// the same file, recorded under the home path the bundle uses.
		abs = filepath.Join(cleanHome, mustRel(realHome, abs))
	}
	if reason := homeReferenceRefusal(mustRel(homeDir, abs)); reason != "" {
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
	if reason := homeReferenceRefusal(mustRel(realHome, real)); reason != "" {
		return homeRefResult{reason: reason}
	}
	// Spelled under the home path, so a directory walked from it yields paths
	// relative to home, and one file has one key however it was named.
	real = filepath.Join(cleanHome, mustRel(realHome, real))
	if reason := agentAliasRefusal(aliases, real); reason != "" {
		return homeRefResult{reason: reason}
	}
	info, err := os.Stat(real)
	if err != nil {
		return homeRefResult{absent: true, reason: "named but not present"}
	}
	if info.IsDir() {
		return homeRefResult{abs: abs, real: real, isDir: true}
	}
	if !info.Mode().IsRegular() {
		return homeRefResult{reason: "not a regular file"}
	}
	if info.Size() > agent.MaxReferenceBytes {
		return homeRefResult{reason: fmt.Sprintf("%d bytes, over the %d byte ceiling", info.Size(), agent.MaxReferenceBytes)}
	}
	return homeRefResult{abs: abs, real: real}
}

// homeReferenceRefusal is homeAssetRefusal plus ~/.claude.json: that file is
// carried as its mcpServers only, and a reference to it would carry the rest
// (the signed-in account, every project's history).
func homeReferenceRefusal(rel string) string {
	folded := strings.ToLower(filepath.ToSlash(filepath.Clean(rel)))
	if folded == ".claude.json" {
		return "carried as its MCP servers only"
	}
	if isCredentialFile(rel) {
		return "a login token file; sign in on the other machine instead"
	}
	if reason := agentRootRefusal(folded); reason != "" {
		return reason
	}
	return homeAssetRefusal(rel)
}

// agentRootAllowed are the parts of each tool's directory a reference may
// reach: the setup the bundle carries anyway, and rule directories. The rest
// is what the tools write while they run (memory, session transcripts,
// archived sessions, history, backups, caches), so it fails closed: a new
// state store a tool adds later is refused without a change here.
var agentRootAllowed = map[string][]string{
	".claude":  {"claude.md", "settings.json", "keybindings.json", "agents", "commands", "output-styles", "hooks", "skills", "rules"},
	".codex":   {"agents.md", "config.toml", "rules", "prompts", "skills"},
	".gemini":  {"gemini.md", "settings.json", "commands"},
	".copilot": {"config.json", "mcp-config.json", "agents"},
	".cursor":  {"mcp.json", "rules"},
	".codeium": {"windsurf/mcp_config.json"},
}

// agentRootRefusal returns why a folded home-relative path inside a tool's
// directory may not be followed, or "". A prose file directly in the tool's
// directory (~/.claude/team-rules.md) is allowed.
func agentRootRefusal(folded string) string {
	// Windows drops trailing dots and spaces: ".claude./todos" is .claude/todos.
	segs := strings.Split(folded, "/")
	for i, seg := range segs {
		segs[i] = strings.TrimRight(seg, ". ")
	}
	folded = strings.Join(segs, "/")
	for root, allowed := range agentRootAllowed {
		if !strings.HasPrefix(folded, root+"/") {
			continue
		}
		sub := strings.TrimPrefix(folded, root+"/")
		if !strings.Contains(sub, "/") && isProseFile(sub) {
			return ""
		}
		for _, p := range allowed {
			if sub == p || strings.HasPrefix(sub, p+"/") {
				return ""
			}
		}
		return "inside " + root + " but not its setup (memory, sessions, history or caches); memory travels only with --include-memory"
	}
	return ""
}

// agentRootAlias is where a tool directory, or one of its entries, really
// lives when it is a symlink: ~/.claude -> ~/dotfiles/claude.
type agentRootAlias struct {
	real    string // spelled under the home path, as resolveHomeReference spells files
	logical string // the home-relative path it stands for, e.g. ".claude/projects"
}

// agentRootAliases resolves each tool directory and every link inside it, at
// any depth. A reference through a target (~/dotfiles/claude/todos, or
// ~/notes/mem with ~/.claude/projects/p/memory linked there) names the same
// files as one through the tool directory and gets the same answer.
func agentRootAliases(homeDir string) []agentRootAlias {
	cleanHome := filepath.Clean(homeDir)
	realHome, err := filepath.EvalSymlinks(homeDir)
	if err != nil {
		realHome = cleanHome
	}
	spell := func(p string) (string, bool) {
		real, err := filepath.EvalSymlinks(p)
		if err != nil || !isUnder(realHome, real) {
			return "", false
		}
		return filepath.Join(cleanHome, mustRel(realHome, real)), true
	}
	var out []agentRootAlias
	for root := range agentRootAllowed {
		dir := filepath.Join(cleanHome, root)
		walkFrom, ok := spell(dir)
		if !ok {
			continue
		}
		if walkFrom != dir {
			out = append(out, agentRootAlias{walkFrom, root})
		}
		// WalkDir does not follow links, so each one is resolved here. Every
		// entry that is not a plain file or directory is tried: a Windows
		// junction is not reported as a symlink.
		_ = filepath.WalkDir(walkFrom, func(p string, d os.DirEntry, err error) error {
			if err != nil || p == walkFrom || d.Type().IsRegular() || d.IsDir() {
				return nil
			}
			if real, ok := spell(p); ok && real != p {
				out = append(out, agentRootAlias{real, root + "/" + filepath.ToSlash(mustRel(walkFrom, p))})
			}
			return nil
		})
	}
	return out
}

// agentAliasRefusal is agentRootRefusal for a path reached through the
// target of a symlinked tool directory or entry.
func agentAliasRefusal(aliases []agentRootAlias, p string) string {
	// Compared in lower case: macOS and Windows match ~/DotFiles/claude to
	// ~/dotfiles/claude. On Linux that can only refuse more.
	p = strings.ToLower(p)
	for _, a := range aliases {
		target := strings.ToLower(a.real)
		if p != target && !isUnder(target, p) {
			continue
		}
		logical := path.Join(a.logical, filepath.ToSlash(mustRel(target, p)))
		if reason := agentRootRefusal(strings.ToLower(logical)); reason != "" {
			return reason
		}
	}
	return ""
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
	// Resolved once: walking the tool directories per reference would cost
	// a full walk of ~/.claude/projects each time.
	aliases := agentRootAliases(homeDir)
	for _, s := range userInstructionSeeds(carried) {
		queue = append(queue, item{s, 0})
	}

	// take carries the file at abs, reading it from real (abs with symlinks
	// resolved, already checked to stay under home).
	take := func(abs, real string, depth int) error {
		abs = filepath.Clean(abs)
		real = filepath.Clean(real)
		if have[abs] || have[real] {
			return nil
		}
		have[abs] = true
		have[real] = true
		rel, err := filepath.Rel(homeDir, abs)
		if err != nil {
			return nil
		}
		// Prose is carried as is. Anything else a reference reaches (a script,
		// a config file) can hold a secret, so it is redacted without
		// --include-secrets, like every other settings file. Both the name and
		// the target count: notes.md can be a symlink to a JSON config.
		shown := "~/" + filepath.ToSlash(rel)
		if reason := referencedFileRefusal(real); reason != "" {
			declined = append(declined, DeclinedAsset{Path: shown, Category: DeclinedByPolicy, Reason: reason})
			return nil
		}
		asset, warn, err := loadSetupAsset(real, setupAssetKindProviderFile, setupAssetOriginProviderHome,
			setupAssetRootProviderHome, filepath.ToSlash(rel), "", !isProseFile(abs) || !isProseFile(real), includeSecrets, true)
		if err != nil {
			// One unreadable file a note names, or one named aux.md, does not
			// end the export.
			declined = append(declined, DeclinedAsset{Path: shown, Category: DeclinedByPolicy, Reason: "named, but not carried: unreadable, or a name import refuses"})
			return nil
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
		refused := func(p string) bool {
			return homeReferenceRefusal(mustRel(homeDir, p)) != "" || agentAliasRefusal(aliases, p) != ""
		}
		return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != dir && (skipReferenceDir(d.Name()) || refused(p)) {
					return filepath.SkipDir
				}
				return nil
			}
			// Links, pipes, sockets and devices are not carried: a pipe would
			// block the read, and a link names a file that was not checked.
			if !d.Type().IsRegular() || refused(p) {
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
			return take(p, p, depth)
		})
	}

	for _, settings := range carried {
		if settings.LogicalPath != "settings.json" || settings.SourcePath == "" ||
			(settings.LogicalRoot != setupAssetRootProviderClaude && settings.LogicalRoot != setupAssetRootProviderGemini) {
			continue
		}
		// Read from disk, not from the asset: without --include-secrets the
		// settings file is redacted, and the scripts it runs are not secrets.
		info, err := lstatPinned(settings.SourcePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		content, changed, err := readStattedFile(settings.SourcePath, info, maxSetupAssetBytes)
		if err != nil || changed != "" {
			continue
		}
		by := "~/" + filepath.ToSlash(mustRel(homeDir, settings.SourcePath))
		for _, p := range settingsCommandPaths(content, homeDir) {
			r := resolveHomeReference(homeDir, homeDir, p, aliases)
			shown := "~/" + filepath.ToSlash(mustRel(homeDir, p))
			switch {
			case r.absent:
				declined = append(declined, DeclinedAsset{Path: shown, Category: DeclinedAbsent,
					Reason: fmt.Sprintf("run by %s and not present", by)})
				continue
			case r.reason != "":
				declined = append(declined, DeclinedAsset{Path: shown, Category: DeclinedByPolicy,
					Reason: fmt.Sprintf("run by %s: %s", by, r.reason)})
				continue
			case r.isDir:
				continue
			}
			if err := take(r.abs, r.real, maxReferenceDepth); err != nil {
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
			r := resolveHomeReference(homeDir, fromDir, raw, aliases)
			switch {
			case r.absent:
				declined = append(declined, DeclinedAsset{Path: raw, Category: DeclinedAbsent,
					Reason: fmt.Sprintf("named by %s and not present", by)})
			case r.reason != "":
				declined = append(declined, DeclinedAsset{Path: raw, Category: DeclinedByPolicy,
					Reason: fmt.Sprintf("named by %s: %s", by, r.reason)})
			case r.isDir:
				if err := takeDir(r.real, by, cur.depth); err != nil {
					return nil, nil, warnings, err
				}
			default:
				if err := take(r.abs, r.real, cur.depth); err != nil {
					return nil, nil, warnings, err
				}
			}
		}
	}
	return assets, dedupeDeclined(declined), warnings, nil
}

// referencedFileRefusal refuses a referenced file that has a second hard
// link, with or without --include-secrets: its other name (.env, .netrc) was
// never checked. Files a tool keeps in its own directory are redacted
// instead, by loadSetupAsset.
func referencedFileRefusal(p string) string {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if agent.HardLinkCount(p, info) > 1 {
		return "has another hard link; its other name cannot be checked"
	}
	return ""
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
		// Any letter case: macOS and Windows match .Claude/settings.json.
		if strings.EqualFold(filepath.ToSlash(p), logical) {
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
		for i := range found {
			// Rules are prose and Gemini commands are TOML prompts. Anything
			// else here (a JSON or YAML file) can hold a token, as at user scope.
			a := &found[i]
			ext := strings.ToLower(filepath.Ext(a.LogicalPath))
			if !includeSecrets && a.ContentPresent && !isProseFile(a.LogicalPath) && ext != ".toml" {
				a.Sensitive, a.Redacted, a.ContentPresent, a.Content, a.SHA256 = true, true, false, nil, ""
				warnings = append(warnings, fmt.Sprintf("sensitive asset %q excluded from export content (use --include-secrets to include it)", a.SourcePath))
			}
		}
		assets = append(assets, found...)
		warnings = append(warnings, warns...)
	}
	for _, rel := range projectInstructionFiles {
		if warn, skip := symlinkedAsset(filepath.Join(projectDir, rel)); skip {
			warnings = append(warnings, warn)
			continue
		}
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
			declined = append(declined, DeclinedAsset{Path: rel, Category: DeclinedRepoScope,
				Reason: "repository-scope settings travel with the repository; listed, not exported"})
			return nil
		}
		// Not prose (a script, a config file): redacted without
		// --include-secrets, as at user scope.
		full := filepath.Join(projectDir, filepath.FromSlash(rel))
		if reason := referencedFileRefusal(full); reason != "" {
			declined = append(declined, DeclinedAsset{Path: rel, Category: DeclinedByPolicy, Reason: reason})
			return nil
		}
		asset, warn, err := loadSetupAsset(full,
			setupAssetKindProjectFile, setupAssetOriginProjectLocal, setupAssetRootProject,
			rel, rel, !isProseFile(rel), includeSecrets, true)
		if err != nil {
			// One unreadable file a note names, or one named aux.md, does not
			// end the export.
			declined = append(declined, DeclinedAsset{Path: rel, Category: DeclinedByPolicy, Reason: "named, but not carried: unreadable, or a name import refuses"})
			return nil
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
					if !d.Type().IsRegular() || agent.SecretBearingReason(mustRel(projectDir, p)) != "" {
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
// and keeps every other key there. A missing or empty file gets those keys
// only: the rest of incoming is never written. A file that is not a JSON
// object is refused rather than overwritten.
func mergeJSONKeysIntoFile(path string, incoming []byte, keys []string) ([]byte, error) {
	existing, err := readExistingFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	doc := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil || doc == nil {
			return nil, fmt.Errorf("%s is not a JSON object; not overwriting it", path)
		}
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
	// The stage outlives one import: refuse what import refuses (device
	// names on Windows) here too, so one such file is skipped, not fatal.
	if _, err := sanitizeAssetRelativePath(rel); err != nil {
		return err.Error()
	}
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
	// .clinerules/hooks/PreToolUse is a script Cline runs.
	if strings.HasPrefix(folded, ".clinerules/hooks/") || strings.Contains(folded, "/.clinerules/hooks/") {
		return "a hooks directory; never written from a bundle"
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
	return notProseReason
}

const notProseReason = "not prose; left staged for review"

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
	d, err := readExistingFile(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false
	}
	if err != nil {
		// Unreadable or over the cap: it exists, so merge must keep it.
		return false, true
	}
	s, err := os.ReadFile(src)
	if err != nil {
		return false, true
	}
	return bytes.Equal(s, d), true
}
