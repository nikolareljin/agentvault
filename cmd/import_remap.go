package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// A bundle's files name paths on the machine they came from: a hook command
// is /home/alex/bin/guard.sh, an MCP server is /home/alex/.local/bin/x, Claude
// keeps memory under projects/-home-alex-Projects. On a machine where home is
// /Users/alex every one of those points at nothing. Import rewrites them.

func exportSourceHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}

func absOrEmpty(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// pathRemap rewrites one path prefix.
type pathRemap struct {
	From, To string
}

// buildPathRemaps returns the rewrites for one import: each --map OLD=NEW in
// the order given, then the bundle's project root to projectRoot, then the
// exporting home to this home. More specific prefixes go first, so a project
// under the old home maps to its own new place, not to the home rewrite.
func buildPathRemaps(sourceHome, targetHome, sourceProject, projectRoot string, maps []string) ([]pathRemap, error) {
	var out []pathRemap
	for _, m := range maps {
		from, to, ok := strings.Cut(m, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("--map %q: want OLD=NEW", m)
		}
		from, to = strings.TrimRight(from, `/\`), strings.TrimRight(to, `/\`)
		// A relative OLD (src=lib) would rewrite that word in any prose.
		if !remappableRoot(from) {
			return nil, fmt.Errorf("--map %q: OLD must be an absolute path below the filesystem root", m)
		}
		out = append(out, pathRemap{From: from, To: to})
	}
	// The source paths come from the bundle: one that names / or a relative
	// path would rewrite text that is not this user's paths.
	if remappableRoot(sourceProject) && projectRoot != "" && sourceProject != projectRoot {
		out = append(out, pathRemap{From: sourceProject, To: projectRoot})
	}
	if remappableRoot(sourceHome) && targetHome != "" && sourceHome != targetHome {
		out = append(out, pathRemap{From: sourceHome, To: targetHome})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].From) > len(out[j].From) })
	return out, nil
}

// remappableRoot is an absolute path, POSIX or Windows, that is not a
// filesystem root by itself.
func remappableRoot(p string) bool {
	switch {
	case len(p) > 1 && p[0] == '/' && strings.Trim(p, "/") != "":
		return true
	case len(p) > 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && strings.Trim(p[3:], `/\`) != "":
		return true
	}
	return false
}

// pathChar is what may continue a path segment. A match on /home/alex must
// not rewrite /home/alexandra.
const pathChar = `A-Za-z0-9._-`

// pathPunct is the punctuation in pathChar.
const pathPunct = "._-"

// remapText rewrites every occurrence of each From that starts and ends at a
// segment boundary, in one pass, so a rewritten path is never rewritten
// again by a later rule. A Windows From matches without regard to case and
// also in its JSON-escaped spelling. When From and To use different
// separators, the rest of each rewritten path is converted too, so
// C:\Users\a\bin\x becomes /home/b/bin/x, not /home/b\bin\x. In a JSON
// document a backslash in To is written escaped. It returns the new text and
// the number of rewrites.
func remapText(text string, remaps []pathRemap) (string, int) {
	type rule struct {
		from, to  string
		esc, fold bool
		fromWin   bool
		toWin     bool
	}
	isJSON := json.Valid([]byte(text))
	var rules []rule
	for _, r := range remaps {
		fromWin, toWin := windowsStylePath(r.From), windowsStylePath(r.To)
		if fromWin {
			rules = append(rules, rule{r.From, r.To, false, true, true, toWin})
			rules = append(rules, rule{strings.ReplaceAll(r.From, `\`, `\\`), r.To, true, true, true, toWin})
		} else {
			rules = append(rules, rule{r.From, r.To, isJSON, false, false, toWin})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].from) > len(rules[j].from) })
	var b strings.Builder
	total := 0
	for i := 0; i < len(text); {
		matched := false
		if i == 0 || !isPathByte(text[i-1], true) {
			for _, r := range rules {
				end := i + len(r.from)
				if r.from == "" || end > len(text) || (end < len(text) && isPathByte(text[end], false)) {
					continue
				}
				if seg := text[i:end]; seg != r.from && !(r.fold && strings.EqualFold(seg, r.from)) {
					continue
				}
				to := r.to
				if r.esc {
					to = strings.ReplaceAll(to, `\`, `\\`)
				}
				b.WriteString(to)
				i = end
				if r.fromWin != r.toWin {
					i = convertPathTail(&b, text, i, r.fromWin, r.esc)
				}
				total++
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String(), total
}

// windowsStylePath is a path with a drive letter or a backslash.
func windowsStylePath(p string) bool {
	return strings.Contains(p, `\`) || (len(p) >= 2 && p[1] == ':')
}

// convertPathTail copies the rest of a rewritten path from text[i:] into b
// with its separators switched to the other style, and returns where the path
// ends: whitespace, a quote, or a delimiter. In esc text (JSON) a Windows
// separator is \\ and any other backslash is an escape that ends the path.
func convertPathTail(b *strings.Builder, text string, i int, fromWin, esc bool) int {
	for i < len(text) {
		c := text[i]
		if strings.IndexByte(" \t\r\n\"'`,;:)]}<>|", c) >= 0 {
			return i
		}
		switch {
		case fromWin && c == '\\' && esc:
			if i+1 >= len(text) || text[i+1] != '\\' {
				return i
			}
			b.WriteByte('/')
			i += 2
		case fromWin && c == '\\':
			b.WriteByte('/')
			i++
		case !fromWin && c == '/':
			if esc {
				b.WriteString(`\\`)
			} else {
				b.WriteByte('\\')
			}
			i++
		case !fromWin && c == '\\':
			// An escape (\n) or a literal backslash: not part of the path.
			return i
		default:
			b.WriteByte(c)
			i++
		}
	}
	return i
}

// isPathByte is whether c continues a path segment. Before a match a
// separator also counts: /srv/home/alex is not /home/alex.
func isPathByte(c byte, before bool) bool {
	if strings.IndexByte(pathPunct, c) >= 0 || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
		return true
	}
	return before && (c == '/' || c == '\\')
}

// claudeProjectKey is how Claude Code names a project's directory under
// ~/.claude/projects: the absolute path with every other character replaced
// by "-".
func claudeProjectKey(path string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(filepath.ToSlash(path), "-")
}

// remapLogicalPath renames Claude's per-project directories, which encode the
// project's absolute path in their name.
func remapLogicalPath(asset SetupAsset, remaps []pathRemap) string {
	if asset.LogicalRoot != setupAssetRootProviderClaude || !strings.HasPrefix(asset.LogicalPath, "projects/") {
		return asset.LogicalPath
	}
	rest := strings.TrimPrefix(asset.LogicalPath, "projects/")
	key, tail, _ := strings.Cut(rest, "/")
	for _, r := range remaps {
		from := claudeProjectKey(r.From)
		if key == from || strings.HasPrefix(key, from+"-") {
			return "projects/" + claudeProjectKey(r.To) + key[len(from):] + "/" + tail
		}
	}
	return asset.LogicalPath
}

// foreignHomePath finds a home-directory path that no rewrite covered.
// A Windows path may sit in JSON with its backslashes doubled.
var foreignHomePath = regexp.MustCompile(`(/home/[` + pathChar + `]+|/Users/[` + pathChar + `]+|(?i:[A-Za-z]:\\{1,2}Users\\{1,2})[` + pathChar + `]+)`)

// remapAssets applies the rewrites to every asset with text content, and
// reports what changed and what still names another machine's home.
func remapAssets(assets []SetupAsset, remaps []pathRemap, targetHome string) ([]SetupAsset, []string) {
	var report []string
	out := make([]SetupAsset, len(assets))
	for i, a := range assets {
		if newPath := remapLogicalPath(a, remaps); newPath != a.LogicalPath {
			report = append(report, fmt.Sprintf("renamed %q -> %q", a.LogicalPath, newPath))
			a.LogicalPath = newPath
		}
		if a.ContentPresent && isRemappableText(a.Content) && len(remaps) > 0 {
			text, n := remapText(string(a.Content), remaps)
			if n > 0 {
				a.Content = []byte(text)
				a.SizeBytes = int64(len(a.Content))
				a.SHA256 = hashSetupAssetContent(a.Content)
				report = append(report, fmt.Sprintf("rewrote %d path(s) in %q", n, describeSetupAsset(a)))
			}
		}
		if a.ContentPresent && isRemappableText(a.Content) {
			for _, m := range uniqueStrings(foreignHomePath.FindAllString(string(a.Content), -1)) {
				if isTargetHome(m, targetHome) {
					continue
				}
				report = append(report, fmt.Sprintf("warning: %q still names %s; pass --map %s=<path here>", describeSetupAsset(a), m, m))
			}
		}
		out[i] = a
	}
	return out, report
}

// isTargetHome is whether m, a home path found in a file, is this machine's
// home or a prefix of it. A Windows path may be JSON-escaped and in any case.
func isTargetHome(m, targetHome string) bool {
	if targetHome == "" {
		return false
	}
	if windowsStylePath(m) {
		m = strings.ToLower(strings.ReplaceAll(m, `\\`, `\`))
		targetHome = strings.ToLower(targetHome)
	}
	return m == targetHome || strings.HasPrefix(targetHome, m+"/") || strings.HasPrefix(targetHome, m+`\`)
}

// isRemappableText is UTF-8 without NUL bytes: a binary file is not rewritten.
func isRemappableText(b []byte) bool {
	return utf8.Valid(b) && !strings.ContainsRune(string(b), 0)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
