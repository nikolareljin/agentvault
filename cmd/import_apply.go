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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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
	fmt.Fprintf(c.Out, "in %s: local %s (changed %s), bundle (changed %s)\n", printable(filepath.Dir(dest)), printable(filepath.Base(dest)), local, bundle)
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
		fmt.Fprintf(c.Out, "%s: %s changed since this machine's copy:\n", printable(file), name)
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
		out = append(out, fmt.Sprintf("%s: %s -> %s", printable(k), clip(string(a)), clip(string(b))))
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

// bundleCommandKeys are top-level settings that run code or load it: hooks,
// the status line, credential helpers, plugins and their marketplaces.
var bundleCommandKeys = []string{"hooks", "statusLine", "apiKeyHelper", "awsAuthRefresh", "awsCredentialExport",
	"otelHeadersHelper", "enabledPlugins", "extraKnownMarketplaces", "plugins", "marketplaces", "fileSuggestion"}

// codeLoadingEnv are environment variables that make another program load
// or run code.
var codeLoadingEnv = map[string]bool{
	"NODE_OPTIONS": true, "NODE_PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true, "DYLD_FRAMEWORK_PATH": true, "PYTHONSTARTUP": true,
	"PYTHONPATH": true, "PYTHONHOME": true, "BASH_ENV": true, "ENV": true, "ZDOTDIR": true, "PROMPT_COMMAND": true,
	"PERL5OPT": true, "PERL5LIB": true, "RUBYOPT": true, "RUBYLIB": true, "JAVA_TOOL_OPTIONS": true,
	"_JAVA_OPTIONS": true, "JDK_JAVA_OPTIONS": true, "GIT_SSH_COMMAND": true, "GIT_SSH": true, "GIT_EXEC_PATH": true,
	"GIT_PAGER": true, "PAGER": true, "EDITOR": true, "VISUAL": true, "SHELL": true, "PATH": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "NPM_CONFIG_PREFIX": true, "CLAUDE_CODE_SHELL_PREFIX": true,
}

// commandField is a key whose value is a program to run: command, args, and
// any *Command or *Helper (Gemini's tools.discoveryCommand, headersHelper).
func commandField(k string) bool {
	l := strings.ToLower(k)
	return l == "command" || l == "args" || l == "cmd" || strings.HasSuffix(l, "command") || strings.HasSuffix(l, "helper")
}

// dropBundleCommands keeps every command the bundle adds or changes out of a
// JSON settings file, and puts this machine's value back (or nothing):
//   - each MCP server, wherever an mcpServers map sits, that is new or
//     differs in anything but its blanked secrets, whatever its type;
//   - each hooks event, and the other bundleCommandKeys whole;
//   - each code-loading variable in any env map outside an MCP server;
//   - any other top-level key under which a command field differs.
//
// A value the bundle blanked as a secret is left to the secret restore. It
// returns the names of what it replaced.
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
			switch {
			case k == "mcpServers" || k == "mcp_servers" || k == "servers":
				servers, ok := bm[k].(map[string]any)
				if !ok {
					// Not a map of servers: the whole value is this
					// machine's, or nothing.
					if lv, lok := lm[k]; !lok || !jsonEqual(bm[k], lv) {
						put(bm, k, lv, lok)
						dropped = append(dropped, name)
					}
					continue
				}
				localServers, _ := lm[k].(map[string]any)
				for _, s := range sortedKeys(servers) {
					srv, _ := servers[s].(map[string]any)
					lsrv, lok := localServers[s].(map[string]any)
					if srv != nil && lok && sameServerApartFromBlanks(srv, lsrv) {
						continue
					}
					if lok {
						servers[s] = lsrv
					} else {
						delete(servers, s)
					}
					dropped = append(dropped, name+"."+s)
				}
			case strings.EqualFold(k, "env"):
				env, ok := bm[k].(map[string]any)
				if !ok {
					continue
				}
				localEnv, _ := lm[k].(map[string]any)
				for _, v := range sortedKeys(env) {
					if !codeLoadingEnv[strings.ToUpper(v)] {
						continue
					}
					lv, lok := localEnv[v]
					if lok && jsonEqual(env[v], lv) {
						continue
					}
					put(env, v, lv, lok)
					dropped = append(dropped, name+"."+v)
				}
			default:
				walk(bm[k], lm[k], name)
			}
		}
	}
	walk(doc, have, "")
	// Anything else: a top-level key under which a command field is new or
	// differs is this machine's.
	for _, k := range sortedKeys(doc) {
		lv, lok := have[k]
		if lok && jsonEqual(doc[k], lv) {
			continue
		}
		if commandFieldsDiffer(doc[k], lv) {
			put(doc, k, lv, lok)
			dropped = append(dropped, k)
		}
	}
	if len(dropped) == 0 {
		return content, nil
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return content, nil
	}
	return out, dropped
}

// commandFieldsDiffer is whether b holds a command field, at any depth,
// that l does not hold with the same value at the same place.
func commandFieldsDiffer(b, l any) bool {
	switch t := b.(type) {
	case map[string]any:
		lm, _ := l.(map[string]any)
		for k, v := range t {
			if commandField(k) && v != "" {
				if lv, ok := lm[k]; !ok || !jsonEqual(v, lv) {
					return true
				}
				continue
			}
			if commandFieldsDiffer(v, lm[k]) {
				return true
			}
		}
	case []any:
		la, _ := l.([]any)
		for i, v := range t {
			var lv any
			if i < len(la) {
				lv = la[i]
			}
			if commandFieldsDiffer(v, lv) {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// codexCommandKeys are Codex config.toml keys that run programs or choose
// where requests go; at the top level and under each profile.
var codexCommandKeys = []string{"mcp_servers", "notify", "model_providers", "shell_environment_policy"}

// codexCommandView is the part of a decoded Codex config that runs or loads
// code: codexCommandKeys at the top level and under profiles, and any
// command field anywhere else, by path.
func codexCommandView(doc map[string]any) map[string]any {
	view := map[string]any{}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, inner := range m {
			p := path + "/" + escapePointer(k)
			if commandField(k) || codexCommandKey(k) {
				view[p] = inner
				continue
			}
			walk(inner, p)
		}
	}
	walk(doc, "")
	return view
}

func codexCommandKey(k string) bool {
	for _, c := range codexCommandKeys {
		if k == c {
			return true
		}
	}
	return false
}

// codexCommandsChanged is whether the bundle's config.toml runs or loads
// anything this machine's copy does not, compared after decoding, so key
// spelling (dotted, quoted, escaped, inline tables) does not matter. A file
// that does not parse counts as changed.
func codexCommandsChanged(content, local []byte) bool {
	var in map[string]any
	if _, err := toml.Decode(string(content), &in); err != nil {
		return true
	}
	var have map[string]any
	if len(bytes.TrimSpace(local)) > 0 {
		if _, err := toml.Decode(string(local), &have); err != nil {
			have = nil
		}
	}
	bv, lv := codexCommandView(in), codexCommandView(have)
	for p, v := range bv {
		if l, ok := lv[p]; !ok || !jsonEqual(v, l) {
			return true
		}
	}
	return false
}

// scriptAsset is a provider file that runs as a program: one in a hooks
// directory or one with a script extension. (The execute bit is kept only
// in a hooks directory, so it adds nothing here.)
func scriptAsset(asset SetupAsset) bool {
	if execAllowed(asset) {
		return true
	}
	p := strings.ToLower(strings.ReplaceAll(asset.LogicalPath, `\`, "/"))
	if strings.HasPrefix(p, "hooks/") || strings.Contains(p, "/hooks/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".sh", ".bash", ".zsh", ".fish", ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".php",
		".ps1", ".psm1", ".bat", ".cmd", ".exe", ".lua", ".jar":
		return true
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
		fmt.Fprintf(w, "  %s\n", printable(r))
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

// printable replaces control characters, which a name from the bundle could
// use to rewrite the terminal around a question, with '?'.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}
