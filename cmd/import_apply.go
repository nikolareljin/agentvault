package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nikolareljin/agentvault/internal/agent"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/nikolareljin/agentvault/internal/textutil"
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
	// DryRun reports what would be written, replaced and left out, and
	// writes nothing: no file, no backup, no directory.
	DryRun bool
	// OnConflict is the policy from flags; conflictAsk asks when
	// interactive and replaces with a backup otherwise.
	OnConflict  conflictPolicy
	Interactive bool
	In          *bufio.Reader
	Out         io.Writer
	BundleTime  time.Time
	Now         func() time.Time

	// MCPServers is --mcp-servers; nil when not given.
	MCPServers *mcpSelection
	// MCPReport is one line per file whose MCP servers were merged.
	MCPReport []string

	all       conflictPolicy
	mcpAll    conflictPolicy
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

// preview says what a run would do with a conflict on dest, without asking.
func (c *importApplyContext) preview(dest string) string {
	policy := c.OnConflict
	if policy == conflictAsk {
		policy = c.all
	}
	if policy == conflictAsk && (!c.Interactive || c.In == nil) {
		policy = conflictBackupReplace
	}
	switch policy {
	case conflictKeep:
		return "would keep this machine's " + dest + ", which differs from the bundle"
	case conflictBackupReplace:
		return "would back up and replace " + dest
	}
	return "would ask whether to back up and replace " + dest
}

// restoreAsker asks once per changed definition in one file whether this
// machine's secret may go into it. Not interactive: no.
func (c *importApplyContext) restoreAsker(file string) func(owner string, local, bundle any) bool {
	return func(owner string, local, bundle any) bool {
		if !c.Interactive || c.In == nil || c.DryRun {
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
	// Anything after the object (a second object, a comment) is a file
	// the agent tool may read differently, or not at all.
	if _, err := dec.Token(); err != io.EOF {
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
	"otelHeadersHelper", "enabledPlugins", "extraKnownMarketplaces", "plugins", "marketplaces", "fileSuggestion",
	"trusted_folders"}

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

// routingEnv are environment variables that change where requests go or
// which certificates are trusted: a bundle that sets one receives this
// machine's API key or apiKeyHelper output.
var routingEnv = map[string]bool{
	"NODE_EXTRA_CA_CERTS": true, "NODE_TLS_REJECT_UNAUTHORIZED": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"REQUESTS_CA_BUNDLE": true, "CURL_CA_BUNDLE": true, "ANTHROPIC_CUSTOM_HEADERS": true, "OTEL_EXPORTER_OTLP_HEADERS": true,
	// Provider selection: the request goes to another cloud account.
	"CLAUDE_CODE_USE_BEDROCK": true, "CLAUDE_CODE_USE_VERTEX": true, "CLAUDE_CODE_USE_FOUNDRY": true,
	"ANTHROPIC_FOUNDRY_RESOURCE": true,
}

// gatedEnv is an env entry the bundle may not add or change: one that loads
// code, or one that sends requests (and the key with them) elsewhere.
func gatedEnv(name string) bool {
	u := strings.ToUpper(name)
	// Contains, not suffix: AWS_ENDPOINT_URL_BEDROCK_RUNTIME.
	if codeLoadingEnv[u] || routingEnv[u] || strings.Contains(u, "PROXY") || strings.Contains(u, "ENDPOINT") ||
		strings.Contains(u, "_URL") || strings.Contains(u, "BASE_URL") || strings.Contains(u, "_API_BASE") {
		return true
	}
	return strings.HasSuffix(u, "_HOST")
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
//   - each code-loading or request-routing variable in any env map outside
//     an MCP server;
//   - any other top-level key under which a command field differs.
//
// A value the bundle blanked as a secret is left to the secret restore. It
// returns the names of what it replaced.
func dropBundleCommands(content, local []byte) ([]byte, []string) {
	return dropBundleCommandsExcept(content, local, nil)
}

// dropBundleCommandsExcept is dropBundleCommands, except that the servers in
// consent (top-level key -> server names) are the user's picks and pass.
func dropBundleCommandsExcept(content, local []byte, consent map[string]map[string]bool) ([]byte, []string) {
	doc, ok := decodeSettingsObject(content)
	if !ok {
		return content, nil
	}
	have, _ := decodeSettingsObject(local)
	if have == nil {
		have = map[string]any{}
	}
	var dropped []string
	// keptBlank: an export blank replaced by this machine's value.
	keptBlank := false
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
	// Folder trust lets a tool act in a directory without asking. The
	// bundle may not change it, including by leaving it out, by replacing
	// security with a non-object, or through the legacy top-level keys.
	for _, k := range []string{"folderTrust", "folderTrustFeature"} {
		bv, bok := doc[k]
		lv, lok := have[k]
		if bok != lok || (bok && !jsonEqual(bv, lv)) {
			put(doc, k, lv, lok)
			dropped = append(dropped, k)
		}
	}
	{
		bsec, bIsMap := doc["security"].(map[string]any)
		bv, bok := bsec["folderTrust"]
		lsec, _ := have["security"].(map[string]any)
		lv, lok := lsec["folderTrust"]
		// A non-object security with nothing local to keep holds no trust.
		if (bok != lok || (bok && !jsonEqual(bv, lv))) && (bIsMap || lok) {
			if !bIsMap {
				bsec = map[string]any{}
				doc["security"] = bsec
			}
			put(bsec, "folderTrust", lv, lok)
			dropped = append(dropped, "security.folderTrust")
		}
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
			case k == "mcpServers" || k == "mcp_servers" || k == "servers" || k == "context_servers":
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
					if consent[name][s] {
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
					if !gatedEnv(v) {
						continue
					}
					lv, lok := localEnv[v]
					if lok && jsonEqual(env[v], lv) {
						continue
					}
					blank := env[v] == ""
					put(env, v, lv, lok)
					// A blank is what export left of a secret: nothing to name.
					if blank {
						keptBlank = true
					} else {
						dropped = append(dropped, name+"."+v)
					}
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
		bk, lk := doc[k], lv
		for p, picked := range consent {
			// The picked servers were consented to one by one above.
			parts := strings.Split(p, ".")
			if parts[0] == k && len(picked) > 0 {
				bk, lk = withoutServersAt(bk, parts[1:], picked), withoutServersAt(lk, parts[1:], picked)
			}
		}
		if commandFieldsDiffer(bk, lk) {
			put(doc, k, lv, lok)
			dropped = append(dropped, k)
		}
	}
	if len(dropped) == 0 && !keptBlank {
		return content, nil
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return content, nil
	}
	return out, dropped
}

// withoutServersAt is v with the named servers left out of the server map
// at rest below it.
func withoutServersAt(v any, rest []string, names map[string]bool) any {
	if len(rest) == 0 {
		return withoutServers(v, names)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, inner := range m {
		out[k] = inner
	}
	out[rest[0]] = withoutServersAt(m[rest[0]], rest[1:], names)
	return out
}

// withoutServers is a server map with the named servers left out.
func withoutServers(v any, names map[string]bool) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, inner := range m {
		if !names[k] {
			out[k] = inner
		}
	}
	return out
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
var codexCommandKeys = []string{"mcp_servers", "notify", "model_providers", "shell_environment_policy",
	"projects", "approval_policy", "sandbox_mode", "sandbox_workspace_write"}

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
	// Where requests go, and the login token with them (chatgpt_base_url,
	// an otel endpoint), and hooks.
	l := strings.ToLower(k)
	return l == "hooks" || l == "otel" || strings.HasSuffix(l, "url") || strings.HasSuffix(l, "endpoint")
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

// proseExtensions are file types an agent tool reads rather than runs.
// JSON and TOML settings are checked key by key instead.
var proseExtensions = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".txt": true,
	".rst": true, ".json": true, ".jsonc": true, ".toml": true}

// proseRunsCommands is whether a prose file can carry something an agent
// runs, so a bundle's new or changed copy needs consent like a script: any
// TOML command file (Gemini runs "!{cmd}", and TOML escapes hide it from a
// byte check), frontmatter with any key outside inertFrontmatterKeys (hooks,
// tools, mcpServers, permissionMode and the rest), and a shell line
// ("!`cmd`", "!{cmd}").
func proseRunsCommands(name string, content []byte) bool {
	slashed := strings.TrimPrefix(agent.NormalizePathForMatch(name), "./")
	ext := path.Ext(slashed)
	switch ext {
	case ".toml":
		// Only Codex config.toml is checked key by key elsewhere.
		return slashed != "config.toml"
	case ".md", ".markdown", ".mdx", ".mdc", ".txt", ".prompt":
	default:
		return false
	}
	if agent.TextProblem(content) != "" {
		// UTF-16 hides "---" and "!`" from the checks below behind NUL
		// bytes, and a tool may still decode it. Not text: ask.
		return true
	}
	if bytes.Contains(content, []byte("!`")) || bytes.Contains(content, []byte("!{")) {
		return true
	}
	trimmed := bytes.TrimLeft(content, "\ufeff \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return false
	}
	return !inertFrontmatter(trimmed)
}

func inertFrontmatterLines(block []string) bool {
	for _, l := range block {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok || k != strings.TrimSpace(k) || !inertFrontmatterKeys[strings.ToLower(k)] {
			return false
		}
		v = strings.TrimSpace(v)
		if v != "" && strings.ContainsAny(v[:1], "{[&*!|>%@`\"'") {
			return false
		}
	}
	return true
}

// inertFrontmatterKeys name a file, describe it or say when it applies; none
// runs anything or grants a tool. Every skill needs name and description, so
// holding all frontmatter held every skill.
var inertFrontmatterKeys = map[string]bool{
	"name": true, "description": true, "version": true, "license": true, "author": true,
	"when_to_use": true, "argument-hint": true, "color": true, "model": true,
	"globs": true, "alwaysapply": true,
}

// inertFrontmatter is whether content (starting with "---") has a closed
// frontmatter block that parses as one YAML mapping of inertFrontmatterKeys
// to plain scalars. Anything else (another key, a nested value, a tag, an
// alias, a duplicate key, no closing line) is not inert.
func inertFrontmatter(content []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if strings.TrimRight(lines[0], " \t") != "---" {
		return false
	}
	// Tools end frontmatter at "---" only. A "..." line would end it for
	// YAML and hide the keys after it from this check, so it is not inert.
	end := -1
	for i := 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		if l == "..." {
			return false
		}
		if l == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return false
	}
	block := lines[1:end]
	for _, l := range block {
		// A lone CR, NEL or Unicode line break is a new line to some readers
		// and not to others; a key could hide behind it. Tabs aside, no
		// control character belongs in frontmatter.
		for _, r := range l {
			if r == '\u0085' || r == '\u2028' || r == '\u2029' || r == utf8.RuneError || (r < 0x20 && r != '\t') || r == 0x7f {
				return false
			}
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(block, "\n")), &doc); err != nil {
		// Not YAML; tools still read "description: Review a PR: read it."
		// line by line. Inert only if every line is an inert key at column
		// 0 and a value that opens no YAML structure.
		return inertFrontmatterLines(block)
	}
	if doc.Kind == 0 {
		return true // empty frontmatter
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return false
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || !inertFrontmatterKeys[strings.ToLower(k.Value)] {
			return false
		}
		if v.Kind != yaml.ScalarNode || v.Anchor != "" {
			return false
		}
		switch v.Tag {
		case "!!str", "!!int", "!!float", "!!bool", "!!null":
		default:
			return false
		}
	}
	return true
}

// scriptAsset is a provider file that may run as a program: anything in a
// hooks directory, and anything that is not prose by extension (an
// extensionless file can be a script with a #! line, and a kept command can
// name any path).
func scriptAsset(asset SetupAsset) bool {
	if execAllowed(asset) {
		return true
	}
	p := agent.NormalizePathForMatch(asset.LogicalPath)
	if strings.HasPrefix(p, "hooks/") || strings.Contains(p, "/hooks/") {
		return true
	}
	return !proseExtensions[path.Ext(p)]
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
	return textutil.Printable(s)
}
