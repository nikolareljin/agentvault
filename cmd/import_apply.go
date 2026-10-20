package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// conflictPolicy says what import does with a local file that differs from
// the bundle's copy.
type conflictPolicy string

const (
	conflictAsk           conflictPolicy = ""
	conflictBackupReplace conflictPolicy = "backup-replace"
	conflictKeep          conflictPolicy = "keep"
)

func parseConflictPolicy(v string) (conflictPolicy, error) {
	switch conflictPolicy(strings.TrimSpace(strings.ToLower(v))) {
	case conflictBackupReplace:
		return conflictBackupReplace, nil
	case conflictKeep:
		return conflictKeep, nil
	}
	return "", fmt.Errorf("--on-conflict must be backup-replace or keep, not %q", v)
}

// importApplyContext carries the choices of one import run across every file
// it writes: how conflicts are settled, what the user answered, the local
// files as they were before the run, and the backups made.
type importApplyContext struct {
	KeepBundleCommands bool
	// OnConflict is the policy from flags; conflictAsk asks when
	// interactive and replaces with a backup otherwise.
	OnConflict  conflictPolicy
	Interactive bool
	In          *bufio.Reader
	Out         io.Writer
	BundleTime  time.Time
	Now         func() time.Time

	all       conflictPolicy
	originals map[string][]byte
	seen      map[string]bool
	backedUp  map[string]string
	answers   map[string]bool
	Backups   []string
	Broad     []string
}

func newImportApplyContext() *importApplyContext {
	return &importApplyContext{Out: io.Discard, Now: time.Now}
}

// original is dest as it was before this run wrote to it, nil when absent.
func (c *importApplyContext) original(dest string) ([]byte, bool) {
	if c.seen == nil {
		c.seen, c.originals = map[string]bool{}, map[string][]byte{}
	}
	if !c.seen[dest] {
		c.seen[dest] = true
		if data, err := readExistingFile(dest); err == nil {
			c.originals[dest] = data
		}
	}
	data, ok := c.originals[dest]
	return data, ok
}

// backup copies the original of dest to dest.bak-YYYYMMDD-HHMMSS once per
// run. The name is created exclusively and never overwritten; a clash adds
// -1, -2 and so on.
func (c *importApplyContext) backup(dest string) (string, error) {
	if p, ok := c.backedUp[dest]; ok {
		return p, nil
	}
	data, ok := c.original(dest)
	if !ok {
		if _, err := os.Lstat(dest); err == nil {
			return "", fmt.Errorf("%s exists but could not be read, so it cannot be backed up", dest)
		}
		return "", nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	p, err := writeExclusiveBackup(dest, data, now())
	if err != nil {
		return "", err
	}
	if c.backedUp == nil {
		c.backedUp = map[string]string{}
	}
	c.backedUp[dest] = p
	c.Backups = append(c.Backups, p)
	return p, nil
}

func writeExclusiveBackup(dest string, data []byte, at time.Time) (string, error) {
	base := dest + ".bak-" + at.Format("20060102-150405")
	for i := 0; i < 100; i++ {
		p := base
		if i > 0 {
			p = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backing up %s: %w", dest, err)
		}
		_, werr := f.Write(data)
		if werr == nil {
			werr = f.Sync()
		}
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(p)
			return "", fmt.Errorf("backing up %s: %w", dest, werr)
		}
		return p, nil
	}
	return "", fmt.Errorf("backing up %s: no free backup name", dest)
}

// decide settles a conflict on dest: true replaces it (after a backup),
// false keeps the local file.
func (c *importApplyContext) decide(dest string) (bool, error) {
	policy := c.OnConflict
	if policy == conflictAsk {
		policy = c.all
	}
	if policy == conflictAsk && (!c.Interactive || c.In == nil) {
		policy = conflictBackupReplace
	}
	switch policy {
	case conflictKeep:
		return false, nil
	case conflictBackupReplace:
		return true, nil
	}
	local := "unknown"
	if info, err := os.Stat(dest); err == nil {
		local = info.ModTime().Format("2006-01-02 15:04")
	}
	bundle := "unknown"
	if !c.BundleTime.IsZero() {
		bundle = c.BundleTime.Format("2006-01-02 15:04")
	}
	fmt.Fprintf(c.Out, "in %s: local %s (changed %s), bundle (changed %s)\n", filepath.Dir(dest), filepath.Base(dest), local, bundle)
	for {
		fmt.Fprint(c.Out, "[b]ackup + replace / [k]eep existing (ba, ka: same for all remaining) [b]: ")
		line, err := c.In.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(c.Out)
			return true, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "b":
			return true, nil
		case "k":
			return false, nil
		case "ba":
			c.all = conflictBackupReplace
			return true, nil
		case "ka":
			c.all = conflictKeep
			return false, nil
		}
		fmt.Fprintln(c.Out, "  Please answer b, k, ba or ka.")
	}
}

// restoreAsker asks once per changed definition in one file whether this
// machine's secret may go into it. Not interactive: no.
func (c *importApplyContext) restoreAsker(file string) func(owner string, local, bundle any) bool {
	return func(owner string, local, bundle any) bool {
		if !c.Interactive || c.In == nil {
			return false
		}
		key := file + "\x00" + owner
		if v, ok := c.answers[key]; ok {
			return v
		}
		// The key comes from the bundle: quoted, so it cannot carry
		// terminal escapes.
		name := strconv.Quote(owner)
		if owner == "" {
			name = "the file"
		}
		fmt.Fprintf(c.Out, "%s: %s changed since this machine's copy:\n", file, name)
		for _, line := range describeDefinitionChange(local, bundle) {
			fmt.Fprintf(c.Out, "  %s\n", line)
		}
		ok, err := askBool(c.Out, c.In, "restore local secret into changed server", false)
		if err != nil {
			ok = false
		}
		if c.answers == nil {
			c.answers = map[string]bool{}
		}
		c.answers[key] = ok
		return ok
	}
}

// describeDefinitionChange lists the fields that differ, old -> new, with
// every secret-looking value masked: the local side still holds its secrets.
func describeDefinitionChange(local, bundle any) []string {
	lm, _ := maskSecrets(local, false).(map[string]any)
	bm, _ := maskSecrets(bundle, false).(map[string]any)
	keys := map[string]bool{}
	for k := range lm {
		keys[k] = true
	}
	for k := range bm {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		a, _ := json.Marshal(lm[k])
		b, _ := json.Marshal(bm[k])
		if bytes.Equal(a, b) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s -> %s", k, clip(string(a)), clip(string(b))))
	}
	if len(out) == 0 {
		out = append(out, "(secret fields only)")
	}
	return out
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

// maskSecrets copies v with every value under a secret key or container,
// and every string that carries a secret, replaced by "<secret>".
func maskSecrets(v any, under bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			out[k] = maskSecrets(inner, under || isSecretKey(k) || secretContainers[strings.ToLower(k)])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = maskSecrets(inner, under)
		}
		return out
	case string:
		if t != "" && (under || secretString(t)) {
			return "<secret>"
		}
		return t
	case nil:
		return nil
	default:
		if under {
			return "<secret>"
		}
		return t
	}
}

func decodeSettingsObject(data []byte) (map[string]any, bool) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, false
	}
	return doc, true
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

// bundleCommandKeys are top-level settings whose value is a command a tool
// runs on its own: hooks, the status line, credential helpers.
var bundleCommandKeys = []string{"hooks", "statusLine", "apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "otelHeadersHelper"}

// dropBundleCommands keeps every command the bundle adds or changes out of a
// JSON settings file: hooks, statusLine and credential helpers per top-level
// key (hooks per event), and each MCP server, wherever an mcpServers map
// sits, whose command or args differ. Each one is replaced by this machine's
// value, or removed when this machine has none. A value the bundle blanked as
// a secret is left to the secret restore. It returns the names of what it
// replaced.
func dropBundleCommands(content, local []byte) ([]byte, []string) {
	doc, ok := decodeSettingsObject(content)
	if !ok {
		return content, nil
	}
	have, _ := decodeSettingsObject(local)
	if have == nil {
		have = map[string]any{}
	}
	var dropped []string
	put := func(m map[string]any, k string, lv any, lok bool) {
		if lok {
			m[k] = lv
		} else {
			delete(m, k)
		}
	}
	for _, k := range bundleCommandKeys {
		bv, ok := doc[k]
		if !ok || bv == "" {
			continue
		}
		lv, lok := have[k]
		if k == "hooks" {
			bm, bIsMap := bv.(map[string]any)
			lm, _ := lv.(map[string]any)
			if bIsMap {
				for _, ev := range sortedKeys(bm) {
					lev, lok := lm[ev]
					if lok && jsonEqual(bm[ev], lev) {
						continue
					}
					put(bm, ev, lev, lok)
					dropped = append(dropped, "hooks."+ev)
				}
				if _, localHooks := have["hooks"]; len(bm) == 0 && !localHooks {
					delete(doc, "hooks")
				}
				continue
			}
		}
		if lok && jsonEqual(bv, lv) {
			continue
		}
		put(doc, k, lv, lok)
		dropped = append(dropped, k)
	}
	var walk func(b, l any, path string)
	walk = func(b, l any, path string) {
		bm, ok := b.(map[string]any)
		if !ok {
			if ba, ok := b.([]any); ok {
				la, _ := l.([]any)
				for i, inner := range ba {
					var li any
					if i < len(la) {
						li = la[i]
					}
					walk(inner, li, fmt.Sprintf("%s[%d]", path, i))
				}
			}
			return
		}
		lm, _ := l.(map[string]any)
		for _, k := range sortedKeys(bm) {
			name := k
			if path != "" {
				name = path + "." + k
			}
			if k != "mcpServers" {
				walk(bm[k], lm[k], name)
				continue
			}
			servers, ok := bm[k].(map[string]any)
			if !ok {
				continue
			}
			localServers, _ := lm[k].(map[string]any)
			for _, s := range sortedKeys(servers) {
				srv, _ := servers[s].(map[string]any)
				lsrv, lok := localServers[s].(map[string]any)
				if srv == nil {
					continue
				}
				_, hasCmd := srv["command"]
				_, hasArgs := srv["args"]
				if !hasCmd && !hasArgs {
					continue
				}
				// A server that runs a command is the same only when
				// everything but its blanked secrets matches: env or cwd
				// change what the same command does.
				if lok && sameServerApartFromBlanks(srv, lsrv) {
					continue
				}
				if lok {
					servers[s] = lsrv
				} else {
					delete(servers, s)
				}
				dropped = append(dropped, name+"."+s)
			}
		}
	}
	walk(doc, have, "")
	if len(dropped) == 0 {
		return content, nil
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return content, nil
	}
	return out, dropped
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var (
	tomlHeader     = regexp.MustCompile(`^\s*\[`)
	tomlCommandKey = regexp.MustCompile(`(?im)mcp_servers|^\s*["']?notify["']?\s*[.=]|\\[uU]`)
)

// codexCommandSections are the sections of a Codex config.toml that carry
// commands (mcp_servers tables, the notify key), as text. A section is a
// header line and what follows it up to the next one; the root is the text
// before the first header. Any section mentioning mcp_servers or a notify
// key, or holding a \u escape that could spell one, counts.
func codexCommandSections(text string) []string {
	var sections []string
	var cur []string
	flush := func() {
		block := strings.Join(cur, "\n")
		if tomlCommandKey.MatchString(block) {
			sections = append(sections, strings.TrimSpace(block))
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if tomlHeader.MatchString(line) {
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	sort.Strings(sections)
	return sections
}

// codexCommandsChanged is whether the bundle's config.toml has a command
// section this machine's copy does not have, byte for byte.
func codexCommandsChanged(content, local []byte) bool {
	have := map[string]bool{}
	for _, s := range codexCommandSections(string(local)) {
		have[s] = true
	}
	for _, s := range codexCommandSections(string(content)) {
		if !have[s] {
			return true
		}
	}
	return false
}

// broadAllowReasons are commands common in allow rules that can still run
// other programs or reach other machines when allowed with any arguments.
var broadAllowReasons = map[string]string{
	"find":    "find -exec runs any command",
	"git":     "git -c core.pager=... (or core.sshCommand, alias.x=!...) runs any command",
	"make":    "make runs whatever the Makefile in the current directory says",
	"sed":     "GNU sed's e command and w flag run commands and write any file",
	"tar":     "tar --to-command and --checkpoint-action run any command",
	"ssh":     "ssh runs commands on other hosts, and -o ProxyCommand runs one here",
	"rsync":   "rsync -e runs any program",
	"docker":  "docker run -v /:/host gives root on this machine",
	"podman":  "podman run -v /:/host gives the container this machine's files",
	"kubectl": "kubectl exec runs commands in any pod the context can reach",
}

// broadAllows are the allow rules in a Claude settings file that grant one of
// broadAllowReasons' commands with any arguments, as "rule: reason".
func broadAllows(data []byte) []string {
	doc, ok := decodeSettingsObject(data)
	if !ok {
		return nil
	}
	perms, _ := doc["permissions"].(map[string]any)
	allow, _ := perms["allow"].([]any)
	var out []string
	for _, e := range allow {
		s, ok := e.(string)
		if !ok {
			continue
		}
		m := bashRule.FindStringSubmatch(strings.TrimSpace(s))
		if m == nil {
			continue
		}
		inner := m[1]
		if !strings.HasSuffix(inner, ":*") && !strings.HasSuffix(inner, " *") {
			continue
		}
		word := strings.TrimSuffix(strings.TrimSuffix(inner, ":*"), " *")
		if strings.Contains(word, " ") {
			continue
		}
		if reason, ok := broadAllowReasons[commandBase(word)]; ok {
			out = append(out, s+": "+reason)
		}
	}
	return out
}

// printBroadAllowWarning prints one block naming every broad rule kept.
func printBroadAllowWarning(w io.Writer, rules []string) {
	rules = uniqueStrings(rules)
	if len(rules) == 0 {
		return
	}
	fmt.Fprintln(w, "WARNING: these Claude allow rules were imported. They are common, but each lets")
	fmt.Fprintln(w, "a command run with any arguments, and these commands can run other programs:")
	for _, r := range rules {
		fmt.Fprintf(w, "  %s\n", r)
	}
	fmt.Fprintln(w, "Narrow them (for example Bash(git status:*)) if this machine does not need them.")
}

// unionWithLocal writes the bundle's top-level keys over the local file and
// keeps the keys only the local file has. A local-only key that holds a
// secret is kept only when the rest of the file is unchanged, or when ask
// says yes: any other field may say where that secret is sent. removed are
// pointers restore left out; a top-level one is not brought back from the
// local file. It returns the merged document and how many local secrets it
// declined to keep.
func unionWithLocal(content, local []byte, pointers, removed []string, ask func(owner string, local, bundle any) bool) ([]byte, int, bool) {
	doc, ok := decodeSettingsObject(content)
	if !ok {
		return content, 0, false
	}
	have, ok := decodeSettingsObject(local)
	if !ok {
		return content, 0, false
	}
	gone := map[string]bool{}
	for _, p := range removed {
		if parts := splitPointer(p); len(parts) == 1 {
			gone[parts[0]] = true
		}
	}
	merged := map[string]any{}
	for k, v := range have {
		if !gone[k] {
			merged[k] = v
		}
	}
	var localSecrets []string
	for k := range have {
		if _, inBundle := doc[k]; inBundle || gone[k] {
			continue
		}
		if (isSecretKey(k) && !settingLikeKey(k)) || containsSecretShape(have[k]) {
			localSecrets = append(localSecrets, k)
		}
	}
	for k, v := range doc {
		merged[k] = v
	}
	declined := 0
	if len(localSecrets) > 0 {
		ptrs := append([]string(nil), pointers...)
		for _, k := range localSecrets {
			ptrs = append(ptrs, "/"+escapePointer(k))
		}
		if !sameApartFromSecrets(merged, have, "", ptrs) && (ask == nil || !ask("", have, merged)) {
			for _, k := range localSecrets {
				delete(merged, k)
				declined++
			}
		}
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return content, 0, false
	}
	return out, declined, true
}

// sameServerApartFromBlanks compares two MCP server definitions, ignoring
// the env and headers entries the bundle blanked: those are this machine's
// secrets, restored later.
func sameServerApartFromBlanks(bundle, local map[string]any) bool {
	b, l := deepCopyJSON(bundle), deepCopyJSON(local)
	for _, c := range []string{"env", "headers"} {
		bm, _ := bundle[c].(map[string]any)
		for k, v := range bm {
			if v == "" {
				if m, ok := b[c].(map[string]any); ok {
					delete(m, k)
				}
				if m, ok := l[c].(map[string]any); ok {
					delete(m, k)
				}
			}
		}
	}
	return jsonEqual(b, l)
}

// sameFileBytes is equal bytes, or two JSON documents that decode equal: a
// file that only changes layout is not a conflict.
func sameFileBytes(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	da, okA := decodeSettingsObject(a)
	db, okB := decodeSettingsObject(b)
	return okA && okB && jsonEqual(da, db)
}

func deepCopyJSON(m map[string]any) map[string]any {
	data, _ := json.Marshal(m)
	out, _ := decodeSettingsObject(data)
	if out == nil {
		out = map[string]any{}
	}
	return out
}
