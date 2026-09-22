package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// An instruction file is not self-contained. The workspace AGENTS.md that
// prompted this names three workflow templates as required reading; storing it
// without them exports a rule pointing at nothing.
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
// either is not treated as a reference: "see the implement_pr template" is a
// sentence, `implement_pr.txt` is a reference.
var (
	backtickSpan  = regexp.MustCompile("`([^`\n]+)`")
	markdownLink  = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
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

// ResolveReferences classifies every reference found in content against dir.
// It reads nothing recursively; the caller walks the closure so that it can
// keep its own visited set and terminate a cycle.
func ResolveReferences(dir, content string) []Reference {
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
		out = append(out, classifyReference(root, raw))
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

func classifyReference(root, raw string) Reference {
	ref := Reference{Raw: raw}

	clean := strings.TrimPrefix(raw, "./")
	if strings.HasSuffix(clean, "/") {
		ref.Status, ref.Reason = ReferenceSkipped, "names a directory"
		return ref
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "~") {
		ref.Status, ref.Reason = ReferenceRefused, "absolute path"
		return ref
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			ref.Status, ref.Reason = ReferenceRefused, "leaves the directory via .."
			return ref
		}
		if referenceSkipDirs[seg] {
			ref.Status, ref.Reason = ReferenceRefused, "under "+seg
			return ref
		}
	}

	full := filepath.Join(root, clean)
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

	rel, err := filepath.Rel(root, real)
	if err != nil {
		ref.Status, ref.Reason = ReferenceRefused, "cannot be expressed relative to the directory"
		return ref
	}
	ref.Rel = filepath.ToSlash(rel)
	ref.Status = ReferenceTaken
	return ref
}
