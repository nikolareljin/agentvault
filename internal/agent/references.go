package agent

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// An instruction file is not self-contained: it can name checklists, templates
// or scripts as required reading, and storing it without them exports a rule
// pointing at nothing.
//
// Finding those names is the easy half. The hard half is what NOT to take, and
// it is decided here rather than discovered in production: nothing absolute,
// nothing reached through `..`, nothing whose real path leaves the directory
// being pulled, nothing over a size ceiling, nothing under a directory that is
// never anybody's instructions.

// MaxReferenceBytes is the ceiling for a followed reference. An instruction
// file's companion is a template or a script; a hundred-megabyte asset that
// happens to be named in prose is not, and an export is not the place to find
// that out.
const MaxReferenceBytes = 256 * 1024

// Directories never followed into. A reference into one is refused by name
// rather than silently dropped, so a document pointing at build output says so.
var referenceSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".venv":        true,
	"__pycache__":  true,
}

// Backticked spans and markdown link targets. Prose naming a file without
// either is not treated as a reference: "see the review checklist" is a
// sentence, `docs/checklist.md` is a reference.
var (
	backtickSpan = regexp.MustCompile("`([^`\n]+)`")
	markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	// Claude Code's import syntax: "@path/to/file" at the start of a line or
	// after whitespace. Requiring that boundary keeps user@host out.
	atImport      = regexp.MustCompile(`(?m)(?:^|\s)@(~?[A-Za-z0-9_./-]+)`)
	fileExtension = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,7}$`)
	globChars     = regexp.MustCompile(`[*?\[\]{}]`)
	// A version, not a path: everything before the last dot is digits. Catches
	// 8.0.x, which has an alphabetic extension and is not a file. 1.25 and
	// 4.1.11 are already excluded by requiring the extension to start with a
	// letter; this is the case that slipped through.
	versionLike = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*\.[A-Za-z][A-Za-z0-9]*$`)
)

// ReferenceStatus is why a reference was taken or refused.
type ReferenceStatus string

const (
	ReferenceTaken    ReferenceStatus = "taken"
	ReferenceRefused  ReferenceStatus = "refused"
	ReferenceNotFound ReferenceStatus = "not found"
	// Named, but not a file reference at all: a directory, or a token written
	// with a trailing slash. Measured against a real instruction file, treating
	// these as refusals buried the one true refusal under eleven directory
	// mentions, so they are reported only when asked for.
	ReferenceSkipped ReferenceStatus = "skipped"
)

// Reference is one path named by an instruction file, and what became of it.
type Reference struct {
	// Path as written in the document.
	Raw string
	// Path relative to the pulled directory, set when taken.
	Rel string
	// Status and, when it is not taken, the reason in a form worth printing.
	Status ReferenceStatus
	Reason string
}

// FindReferences returns the distinct path-like tokens an instruction file
// names, in the order they first appear. It decides nothing about them.
func FindReferences(content string) []string {
	var out []string
	seen := map[string]bool{}

	add := func(tok string) {
		tok = strings.TrimSpace(tok)
		if tok == "" || seen[tok] || !looksLikePath(tok) {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}

	for _, m := range backtickSpan.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, m := range markdownLink.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, m := range atImport.FindAllStringSubmatch(content, -1) {
		add(strings.TrimRight(m[1], ".,;:"))
	}
	return out
}

// looksLikePath keeps the obvious file references and drops the things that
// share their shape: commands, URLs, version numbers, globs, flags.
func looksLikePath(tok string) bool {
	if strings.ContainsAny(tok, " \t") {
		return false
	}
	if strings.Contains(tok, "://") || strings.HasPrefix(tok, "#") ||
		strings.HasPrefix(tok, "mailto:") || strings.HasPrefix(tok, "-") {
		return false
	}
	if globChars.MatchString(tok) {
		return false
	}
	if versionLike.MatchString(tok) {
		return false
	}
	// A path has either a separator or an alphabetic extension. The extension
	// must start with a letter so that 1.25, 0.32.0 and 8.0.x are not paths.
	return strings.Contains(tok, "/") || fileExtension.MatchString(tok)
}

// ResolveReferences classifies every reference in content, resolved relative to
// baseRel: the directory of the file that named them, itself relative to dir.
// A script in scripts/ naming `helpers.sh` means scripts/helpers.sh, and naming
// `../AGENTS.md` means the root file, not an escape. Resolving everything
// against the root instead reported the first as missing and refused the second.
//
// It reads nothing recursively; the caller walks the closure so that it can
// keep its own visited set and terminate a cycle.
func ResolveReferences(dir, baseRel, content string) []Reference {
	root, err := filepath.Abs(dir)
	if err != nil {
		root = dir
	}
	// The directory's own real path, so a symlinked root does not make every
	// reference look like an escape.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}

	var out []Reference
	for _, raw := range FindReferences(content) {
		out = append(out, classifyReference(root, baseRel, raw))
	}
	// One entry per file taken, whatever the spelling: `AGENTS.md` and
	// `./AGENTS.md` are the same reference and were reported twice.
	seenRel := map[string]bool{}
	deduped := out[:0]
	for _, r := range out {
		if r.Status == ReferenceTaken {
			if seenRel[r.Rel] {
				continue
			}
			seenRel[r.Rel] = true
		}
		deduped = append(deduped, r)
	}
	out = deduped
	sort.SliceStable(out, func(i, j int) bool { return out[i].Raw < out[j].Raw })
	return out
}

// rootedReason returns why p is refused when it names a location independent
// of the instruction directory, or "" when it does not. filepath.IsAbs misses
// "/etc/passwd", `\x` and "C:x" on Windows, where they are rooted or
// drive-relative; the reason says so, since "a:b" is not obviously absolute.
func rootedReason(goos, p string) string {
	if filepath.IsAbs(p) {
		return "absolute path"
	}
	if isWindowsRooted(goos, p) {
		return "rooted or drive-qualified path on Windows"
	}
	return ""
}

// isWindowsRooted applies the Windows rules on goos "windows" only, by hand
// rather than with filepath.VolumeName, so the rules are tested on any OS.
// Covers "/x", `\x`, UNC and `\\?\` prefixes (both start with a slash),
// and drive-qualified "C:", "C:x", "c:/x".
func isWindowsRooted(goos, p string) bool {
	if goos != "windows" {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) >= 2 && p[1] == ':' {
		c := p[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return false
}

func classifyReference(root, baseRel, raw string) Reference {
	ref := Reference{Raw: raw}

	clean := strings.TrimPrefix(raw, "./")
	if strings.HasSuffix(clean, "/") {
		ref.Status, ref.Reason = ReferenceSkipped, "names a directory"
		return ref
	}
	if reason := rootedReason(runtime.GOOS, clean); reason != "" {
		ref.Status, ref.Reason = ReferenceRefused, reason
		return ref
	}
	if strings.HasPrefix(clean, "~") {
		ref.Status, ref.Reason = ReferenceRefused, "absolute path"
		return ref
	}

	// Lexical containment first, so a reference that escapes is refused by name
	// whether or not it exists. Checking only the resolved path would report a
	// non-existent ../secret as merely missing.
	full := filepath.Join(root, baseRel, clean)
	lexical, err := filepath.Rel(root, full)
	if err != nil || lexical == ".." || strings.HasPrefix(lexical, ".."+string(os.PathSeparator)) {
		ref.Status, ref.Reason = ReferenceRefused, "leaves the directory"
		return ref
	}
	for _, seg := range strings.Split(filepath.ToSlash(lexical), "/") {
		if referenceSkipDirs[seg] {
			ref.Status, ref.Reason = ReferenceRefused, "under "+seg
			return ref
		}
	}
	if reason := SecretBearingReason(lexical); reason != "" {
		ref.Status, ref.Reason = ReferenceRefused, reason
		return ref
	}

	info, err := os.Lstat(full)
	if err != nil {
		ref.Status, ref.Reason = ReferenceNotFound, "named but not present"
		return ref
	}

	// A symlink is followed only while it stays inside. EvalSymlinks also
	// resolves any symlinked parent, which is the case a prefix check alone
	// would miss.
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		ref.Status, ref.Reason = ReferenceNotFound, "named but not present"
		return ref
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		ref.Status, ref.Reason = ReferenceRefused, "resolves outside the directory"
		return ref
	}

	if info, err = os.Stat(full); err != nil {
		ref.Status, ref.Reason = ReferenceNotFound, "named but not present"
		return ref
	}
	if info.IsDir() {
		ref.Status, ref.Reason = ReferenceSkipped, "names a directory"
		return ref
	}
	if !info.Mode().IsRegular() {
		ref.Status, ref.Reason = ReferenceRefused, "not a regular file"
		return ref
	}
	if info.Size() > MaxReferenceBytes {
		ref.Status = ReferenceRefused
		ref.Reason = fmt.Sprintf("%d bytes, over the %d byte ceiling", info.Size(), MaxReferenceBytes)
		return ref
	}
	// A second hard link is another name for the same file, and that name
	// was never checked: notes.md can be .env.
	if HardLinkCount(full, info) > 1 {
		ref.Status, ref.Reason = ReferenceRefused, "has another hard link; its other name cannot be checked"
		return ref
	}

	rel, err := filepath.Rel(root, real)
	if err != nil {
		ref.Status, ref.Reason = ReferenceRefused, "cannot be expressed relative to the directory"
		return ref
	}
	// Again on the resolved name: a symlink called notes.md can point at .env.
	if reason := SecretBearingReason(rel); reason != "" {
		ref.Status, ref.Reason = ReferenceRefused, reason
		return ref
	}
	ref.Rel = filepath.ToSlash(rel)
	ref.Status = ReferenceTaken
	return ref
}

// Files that hold credentials by convention. Instruction files mention them
// ("copy `.env.example` to `.env`"), and following that mention would put the
// secrets themselves in a bundle or the vault.
var (
	secretBaseNames = map[string]bool{
		".env": true, ".netrc": true, ".npmrc": true, ".pypirc": true, ".pgpass": true,
		".git-credentials": true, "id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
		".credentials.json": true, "auth.json": true, "oauth_creds.json": true, "google_accounts.json": true,
		"credentials": true, "credentials.json": true, "hosts.yml": true,
	}
	secretExtensions = map[string]bool{
		".pem": true, ".key": true, ".p12": true, ".pfx": true, ".keystore": true,
		".jks": true, ".kdbx": true, ".ppk": true,
	}
	secretDirs = map[string]bool{
		".ssh": true, ".gnupg": true, ".aws": true, ".kube": true, ".docker": true, ".azure": true,
	}
	shortName = regexp.MustCompile(`~[0-9]`)
	// .env variants that are templates by name, not secrets.
	envTemplates = map[string]bool{
		".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true,
	}
)

// SecretBearingReason returns why rel names a credential file, or "" when it
// does not. rel is slash- or OS-separated and relative.
func SecretBearingReason(rel string) string {
	slashed := filepath.ToSlash(rel)
	segs := strings.Split(slashed, "/")
	for i, seg := range segs {
		// Windows reads ".env::$DATA" and ".env:x" as .env: a name ends at
		// the first colon there.
		if c := strings.IndexByte(seg, ':'); c >= 0 {
			segs[i] = seg[:c]
		}
		// A Windows short name (ENV~1, CREDEN~1.JSO) is another name for a
		// file whose long name cannot be checked from here.
		if shortName.MatchString(seg) {
			return "a Windows short name; its long name cannot be checked"
		}
	}
	slashed = strings.Join(segs, "/")
	for _, seg := range segs {
		// Windows drops trailing dots and spaces: ".aws." is .aws there.
		if secretDirs[strings.TrimRight(strings.ToLower(seg), ". ")] {
			return "under " + seg + ", which holds credentials"
		}
	}
	base := path.Base(slashed)
	// Trailing dots and spaces are dropped by Windows: ".env." is .env there.
	lower := strings.TrimRight(strings.ToLower(base), ". ")
	if envTemplates[lower] {
		return ""
	}
	if secretBaseNames[lower] || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".env") {
		return "a credential file by name; never followed"
	}
	if secretExtensions[strings.ToLower(path.Ext(lower))] {
		return "a key or certificate file; never followed"
	}
	return ""
}
