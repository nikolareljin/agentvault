package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A settings file that can hold a token used to be redacted whole without
// --include-secrets, so the other machine got no settings.json at all: no
// hooks, no permissions, no plugins. Field-level redaction blanks only the
// values that look like secrets, records where they were, and on import
// fills them back from the file already on that machine, or leaves them out.

// secretKeyParts mark a JSON key whose value is a secret.
var secretKeyParts = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "api-key",
	"authorization", "bearer", "credential", "private_key", "privatekey",
	"access_key", "accesskey", "session_key", "client_secret", "cookie",
}

// secretKeys are short keys that are secrets by themselves.
// sig and signature are signed-URL secrets (Azure SAS ?sig=).
var secretKeys = map[string]bool{"key": true, "auth": true, "pat": true, "pass": true, "pin": true,
	"sig": true, "signature": true}

// secretContainers hold values that are commonly secrets: an MCP server's env
// and an HTTP server's headers (Codex: http_headers). Every string under them
// is blanked.
var secretContainers = map[string]bool{"env": true, "headers": true, "http_headers": true}

func isSecretKey(k string) bool {
	lower := strings.ToLower(k)
	if secretKeys[lower] {
		return true
	}
	for _, p := range secretKeyParts {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// settingLikeKey is a count or limit whose name contains a secret word:
// maxTokens, tokenLimit, passwordMinLength.
func settingLikeKey(k string) bool {
	lower := strings.ToLower(k)
	for _, suf := range []string{"tokens", "count", "limit", "length", "len", "size", "ttl", "timeout"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

// redactJSONSecrets blanks every secret-looking value in a JSON object and
// returns the new document and the JSON Pointers (RFC 6901) of the blanked
// values. ok is false when data is not a JSON object; the caller then
// redacts the whole file as before.
func redactJSONSecrets(data []byte) (out []byte, pointers []string, ok bool) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, nil, false
	}
	var walk func(v any, ptr string, under, numUnder bool) any
	walk = func(v any, ptr string, under, numUnder bool) any {
		switch t := v.(type) {
		case map[string]any:
			for k, inner := range t {
				t[k] = walk(inner, ptr+"/"+escapePointer(k), under || isSecretKey(k) || secretContainers[strings.ToLower(k)],
					numUnder || (isSecretKey(k) && !settingLikeKey(k)) || secretContainers[strings.ToLower(k)])
			}
			return t
		case []any:
			for i, inner := range t {
				t[i] = walk(inner, ptr+"/"+strconv.Itoa(i), under, numUnder)
			}
			return t
		case json.Number:
			// A number under a key such as maxTokens is a setting; under
			// password or pin it is a secret.
			if numUnder {
				pointers = append(pointers, ptr)
				return ""
			}
			return t
		case string:
			if under {
				pointers = append(pointers, ptr)
				return ""
			}
			return t
		default:
			return t
		}
	}
	walk(doc, "", false, false)
	// A secret can sit under a key that says nothing: an args list holding
	// "--token", "x", or a URL with credentials in it. Field-level redaction
	// cannot place those, so such a file falls back to whole-file redaction.
	if containsSecretShape(doc) {
		return nil, nil, false
	}
	sort.Strings(pointers)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, false
	}
	return out, pointers, true
}

// restoreRedactedFields fills each redacted pointer in incoming from the same
// pointer in existing, the file already on this machine. A pointer existing
// does not have is removed, so a tool never reads an empty string as its
// token. A secret whose definition changed is not restored.
func restoreRedactedFields(incoming []byte, pointers []string, existing []byte) ([]byte, int, error) {
	res, err := restoreRedactedFieldsWith(incoming, pointers, existing, nil)
	return res.Content, res.Restored, err
}

// restoreResult is what restoreRedactedFieldsWith did.
type restoreResult struct {
	Content  []byte
	Restored int
	// Declined counts local secrets not restored because the definition
	// that holds them changed and ask did not say yes.
	Declined int
	// Removed are the secret pointers left out of Content.
	Removed []string
	// Secrets are the listed pointers that named a blanked secret; any
	// other listed pointer is part of the definition.
	Secrets []string
}

// restoreRedactedFieldsWith is restoreRedactedFields with a question for a
// secret whose definition changed: ask gets the owner pointer ("" for the
// file itself) and the local and bundle definitions, and a yes restores it.
// Every answer is collected before the document changes.
func restoreRedactedFieldsWith(incoming []byte, pointers []string, existing []byte, ask func(owner string, local, bundle any) bool) (restoreResult, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(incoming))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return restoreResult{}, fmt.Errorf("bundle content is not a JSON object: %w", err)
	}
	var have map[string]any
	if len(bytes.TrimSpace(existing)) > 0 {
		d := json.NewDecoder(bytes.NewReader(existing))
		d.UseNumber()
		if err := d.Decode(&have); err != nil {
			have = nil
		}
	}
	// The pointer list comes from the bundle. Only a pointer that names a
	// blanked value under a secret key counts as a secret; any other listed
	// field is part of the definition and must match.
	secrets := secretPointers(doc, pointers)
	res := restoreResult{Secrets: secrets}
	// Decide every restore against the document as the bundle has it, then
	// change it. A listed field that is not a secret is left as it is, so it
	// still has to match.
	restore := map[string]any{}
	answers := map[string]bool{}
	for _, p := range secrets {
		v, ok := pointerGet(have, p)
		if !ok {
			continue
		}
		same, owner := restorable(doc, have, p, secrets)
		if same {
			restore[p] = v
			continue
		}
		if owner == nil {
			continue
		}
		yes, asked := answers[*owner]
		if !asked {
			in, _ := pointerGetAny(doc, *owner)
			ex, _ := pointerGetAny(have, *owner)
			yes = ask != nil && ask(*owner, ex, in)
			answers[*owner] = yes
		}
		if yes {
			restore[p] = v
		} else {
			res.Declined++
		}
	}
	// Deepest first, so removing an array element does not shift a sibling
	// pointer that is still to be handled.
	sorted := append([]string(nil), secrets...)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	for _, p := range sorted {
		if v, ok := restore[p]; ok {
			pointerSet(doc, p, v)
			continue
		}
		pointerDelete(doc, p)
		res.Removed = append(res.Removed, p)
	}
	res.Restored = len(restore)
	out, err := json.MarshalIndent(doc, "", "  ")
	res.Content = out
	return res, err
}

// secretPointers are the listed pointers that name a blanked value under a
// secret key. Other listed fields are part of the definition.
func secretPointers(doc map[string]any, pointers []string) []string {
	var secrets []string
	for _, p := range pointers {
		if v, ok := pointerGet(doc, p); ok && v == "" && secretPointer(p) {
			secrets = append(secrets, p)
		}
	}
	return secrets
}

// countSecretPointers is how many listed pointers restore would treat as
// secrets in content, so a tampered list does not inflate the warning.
func countSecretPointers(content []byte, pointers []string) int {
	var doc map[string]any
	if json.Unmarshal(content, &doc) != nil {
		return 0
	}
	return len(secretPointers(doc, pointers))
}

func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

func splitPointer(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

func pointerGet(doc map[string]any, p string) (any, bool) {
	var cur any = doc
	for _, part := range splitPointer(p) {
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

// pointerParent returns the container holding the last segment of p.
func pointerParent(doc map[string]any, p string) (any, string) {
	parts := splitPointer(p)
	var cur any = doc
	for _, part := range parts[:len(parts)-1] {
		switch t := cur.(type) {
		case map[string]any:
			cur = t[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(t) {
				return nil, ""
			}
			cur = t[i]
		default:
			return nil, ""
		}
	}
	return cur, parts[len(parts)-1]
}

func pointerSet(doc map[string]any, p string, v any) {
	parent, last := pointerParent(doc, p)
	switch t := parent.(type) {
	case map[string]any:
		t[last] = v
	case []any:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(t) {
			t[i] = v
		}
	}
}

// pointerDelete removes a map key. An array element is left as "" rather
// than removed, so the positions of its siblings do not change.
func pointerDelete(doc map[string]any, p string) {
	parent, last := pointerParent(doc, p)
	if m, ok := parent.(map[string]any); ok {
		delete(m, last)
	}
}

// pointerBlank removes a map key or sets an array element to "", the way
// export left it.
func pointerBlank(doc map[string]any, p string) {
	parent, last := pointerParent(doc, p)
	switch t := parent.(type) {
	case map[string]any:
		delete(t, last)
	case []any:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(t) {
			t[i] = ""
		}
	}
}

// secretPointer is a pointer with a secret key or container on its path.
func secretPointer(p string) bool {
	for _, part := range splitPointer(p) {
		if isSecretKey(part) || secretContainers[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

var (
	// Credentials in a URL, a credential query parameter, or a well-known
	// token prefix.
	secretValueShapes = regexp.MustCompile(`(?i)(://[^/\s:@]+:[^/\s@]+@|\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|\bgithub_pat_|\bsk-[A-Za-z0-9_-]{16,}|\bxox[abprs]-|\bAKIA[0-9A-Z]{16}\b|\bAIza[0-9A-Za-z_-]{30,}|\bglpat-|\bbearer\s+[A-Za-z0-9._~+/=-]{8,}|\bbasic\s+[A-Za-z0-9+/=]{8,}|\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.)`)
	// A query or fragment parameter: ?client_secret=x, #access_token=x.
	// Its name decides, by the key rules.
	queryParam = regexp.MustCompile(`[?&#]([^=&#\s]+)=[^&#\s]`)
	// A flag whose next argument is a secret.
	secretFlag = regexp.MustCompile(`(?i)^--?(` + secretFlagNames + `)(=.+)?$`)
	// The same flag with its value inside one string: a command line.
	secretFlagInline = regexp.MustCompile(`(?i)(^|\s)--?(` + secretFlagNames + `)(=|\s+)[^-\s]`)
	// An HTTP header passed as an argument: "Authorization: Bearer x".
	headerArg = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*:\s*\S`)
	// An environment assignment passed as an argument or inside a command
	// line: docker run -e GITHUB_TOKEN=x.
	// A header passed the same way: X-Api-Key=x.
	envAssign = regexp.MustCompile(`(?:^|\s)([A-Za-z_][A-Za-z0-9_-]*)=\S`)
)

const secretFlagNames = `[a-z]*[-_]?(token|secret|password|api[-_]?key)|key|pat|pass|passwd|auth|bearer|access[-_]?key|private[-_]?key|client[-_]?secret|credentials?|cookie` // #nosec G101 -- a regexp of flag names, not a credential

// secretString is a string value that carries a secret in its text.
func secretString(s string) bool {
	if s == "" {
		return false
	}
	if secretValueShapes.MatchString(s) || secretFlagInline.MatchString(s) {
		return true
	}
	for _, u := range urlInString.FindAllString(s, -1) {
		if urlCarriesCredential(u) {
			return true
		}
	}
	for _, q := range queryParam.FindAllStringSubmatch(s, -1) {
		if isSecretKey(q[1]) {
			return true
		}
	}
	for _, a := range envAssign.FindAllStringSubmatch(s, -1) {
		if isSecretKey(a[1]) && !settingLikeKey(a[1]) {
			return true
		}
	}
	m := headerArg.FindStringSubmatch(s)
	return m != nil && isSecretKey(m[1])
}

// urlInString finds each URL in a string: a scheme, "://" and what follows
// up to whitespace or a quote.
var urlInString = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

// urlCarriesCredential reports a URL with userinfo (a user name alone, as
// in https://token@host, counts) or a query or fragment parameter whose
// decoded name is a secret key. It parses with net/url, so ports, IPv6
// hosts and percent-encoding do not hide one. A URL that does not parse
// counts as carrying one.
func urlCarriesCredential(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	if u.User != nil {
		return true
	}
	return secretParams(u.RawQuery) || secretParams(u.Fragment)
}

// secretParams reports a query-shaped string with a secret parameter name,
// judged decoded where it decodes and as written where it does not.
func secretParams(q string) bool {
	for _, part := range strings.FieldsFunc(q, func(r rune) bool { return r == '&' || r == ';' }) {
		name, _, _ := strings.Cut(part, "=")
		if isSecretKey(name) {
			return true
		}
		if dec, err := url.QueryUnescape(name); err == nil && isSecretKey(dec) {
			return true
		}
	}
	return false
}

// stripURLSecrets is raw without its userinfo and without query and
// fragment parameters whose name is a secret key, so the rest of the URL
// stays usable. ok is false when raw is not a URL that parses; the caller
// then leaves the whole value out.
func stripURLSecrets(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.User = nil
	strip := func(q string) (string, bool) {
		if q == "" {
			return "", true
		}
		vals, err := url.ParseQuery(q)
		if err != nil {
			return "", false
		}
		for k := range vals {
			if isSecretKey(k) {
				delete(vals, k)
			}
		}
		return vals.Encode(), true
	}
	var ok bool
	if u.RawQuery, ok = strip(u.RawQuery); !ok {
		return "", false
	}
	frag, ok := strip(u.Fragment)
	if !ok {
		return "", false
	}
	u.Fragment, u.RawFragment = frag, ""
	if secretString(u.String()) {
		return "", false
	}
	return u.String(), true
}

// containsSecretShape reports a value that looks like a secret anywhere in
// doc that the key-based pass left in place.
func containsSecretShape(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for _, inner := range t {
			if containsSecretShape(inner) {
				return true
			}
		}
	case []any:
		for i, inner := range t {
			s, ok := inner.(string)
			if !ok {
				if containsSecretShape(inner) {
					return true
				}
				continue
			}
			if m := secretFlag.FindStringSubmatch(s); m != nil {
				if m[2] != "" && m[2] != "=" {
					return true
				}
				if i+1 < len(t) {
					if next, ok := t[i+1].(string); ok && next != "" {
						return true
					}
				}
			}
			if secretString(s) {
				return true
			}
		}
	case string:
		return secretString(t)
	}
	return false
}

// restorable decides whether this machine's secret at p may go back into the
// incoming document without asking. The pointer must name a blanked secret
// ("" in incoming, under a secret key, env or headers), and the object
// holding it must be the same object on both sides apart from its secrets: a
// bundle that keeps an MCP server's name but points its URL elsewhere must
// not receive the local token for it without a yes. owner is that object's
// pointer ("" for the whole file), or nil when p is not a secret at all.
func restorable(doc, have map[string]any, p string, pointers []string) (bool, *string) {
	if v, ok := pointerGet(doc, p); !ok || v != "" {
		return false, nil
	}
	if !secretPointer(p) {
		return false, nil
	}
	parts := splitPointer(p)
	// The nearest object that is not itself a secret container: for
	// /mcpServers/x/headers/Authorization that is /mcpServers/x.
	cut := len(parts) - 1
	for cut > 0 && (isSecretKey(parts[cut-1]) || secretContainers[strings.ToLower(parts[cut-1])]) {
		cut--
	}
	if cut == 0 {
		if len(parts) == 1 {
			// A top-level secret (apiKeyHelper) belongs to the tool that
			// reads the file, on both machines: any field may say where the
			// secret is sent, so the definition is the whole file.
			owner := ""
			return sameApartFromSecrets(doc, have, "", pointers), &owner
		}
		// settings.json's own env: the rest of env must match too, or a
		// bundle that sets ANTHROPIC_BASE_URL elsewhere receives the key.
		cut = 1
	}
	escaped := make([]string, cut)
	for i, part := range parts[:cut] {
		escaped[i] = escapePointer(part)
	}
	owner := "/" + strings.Join(escaped, "/")
	in, _ := pointerGetAny(doc, owner)
	ex, _ := pointerGetAny(have, owner)
	return sameApartFromSecrets(in, ex, owner, pointers), &owner
}

func pointerGetAny(doc map[string]any, p string) (any, bool) {
	if p == "" {
		return doc, true
	}
	return pointerGet(doc, p)
}

// sameApartFromSecrets compares two values with every redacted pointer under
// prefix ignored.
func sameApartFromSecrets(a, b any, prefix string, pointers []string) bool {
	strip := func(v any) any {
		data, _ := json.Marshal(v)
		var cp any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		_ = dec.Decode(&cp)
		m, ok := cp.(map[string]any)
		if !ok {
			return cp
		}
		for _, p := range pointers {
			if strings.HasPrefix(p, prefix+"/") {
				pointerBlank(m, strings.TrimPrefix(p, prefix))
			}
		}
		return m
	}
	ja, _ := json.Marshal(strip(a))
	jb, _ := json.Marshal(strip(b))
	return bytes.Equal(ja, jb)
}

// interpreters run any program they are given. A Claude allow rule that
// lets one run with any arguments is a blanket grant.
var interpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "env": true,
	"eval": true, "exec": true, "sudo": true, "doas": true, "xargs": true, "node": true, "deno": true,
	"bun": true, "npx": true, "perl": true, "ruby": true, "php": true, "lua": true, "osascript": true,
	"pwsh": true, "powershell": true, "cmd": true, "busybox": true, "nohup": true, "timeout": true,
	"source": true, "builtin": true, "awk": true, "gawk": true, "mawk": true, "nawk": true, "bunx": true,
	"uvx": true, "pipx": true, "expect": true, "tclsh": true, "unshare": true, "nsenter": true,
	"systemd-run": true, "pkexec": true, "py": true, "ipython": true, "tcsh": true, "csh": true, "ash": true,
	"mksh": true, "yash": true, "nu": true, "xonsh": true, "elvish": true, "ts-node": true, "tsx": true,
	"rscript": true, "wsl": true, "run0": true, "setpriv": true, "parallel": true, "pnpx": true,
	"r": true, "julia": true, "wish": true, "toybox": true, "pypy": true, "pypy3": true, "irb": true,
	"pry": true, "guile": true, "racket": true, "elixir": true, "iex": true, "erl": true, "escript": true,
	"groovy": true, "jshell": true, "swift": true, "rlwrap": true, "scala": true, "kotlin": true,
	"runghc": true, "runhaskell": true, "ghci": true, "sbcl": true, "clojure": true, "clj": true,
}

var bashRule = regexp.MustCompile(`^Bash(?:\((.*)\))?$`)

// isInterpreterAllow is a Bash rule that may let any program run without
// asking. It fails closed: a rule is kept only when it is a plain command
// line (literal words of safe characters, single spaces, an optional
// trailing ":*" or " *") whose command is not an interpreter, wrapper or
// shell keyword, with no exec-style flag and no interpreter named in any
// word. Everything else, bare Bash and Bash(*) included, is a blanket grant.
func isInterpreterAllow(rule string) bool {
	m := bashRule.FindStringSubmatch(strings.TrimSpace(rule))
	if m == nil {
		return false
	}
	inner := m[1]
	if strings.HasSuffix(inner, ":*") {
		inner = strings.TrimSuffix(inner, ":*")
	} else if strings.HasSuffix(inner, " *") {
		inner = strings.TrimSuffix(inner, " *")
	}
	if inner == "" {
		return true
	}
	words := strings.Split(inner, " ")
	for _, w := range words {
		if !plainWord.MatchString(w) {
			return true
		}
		// Case-sensitive: git -C, grep -E and tar -C are not exec flags.
		switch w {
		case "-c", "-e", "--eval", "-exec", "-execdir", "-ok", "-okdir", "--command":
			return true
		}
		base := commandBase(w)
		if interpreters[base] || commandWrappers[base] || strings.HasPrefix(base, "python") {
			return true
		}
	}
	first := strings.ToLower(words[0])
	// At command position "." and ".." run a file in the shell
	// (. ./env.sh); as arguments (find ., git add .) they are paths.
	if strings.HasPrefix(first, ".") && !strings.HasPrefix(first, "./") && !strings.HasPrefix(first, "../") {
		return true
	}
	if len(words) > 1 && packageRunners[commandBase(words[0])+" "+strings.ToLower(words[1])] {
		return true
	}
	return shellKeywords[first]
}

// packageRunners download and run a package: the first two words.
var packageRunners = map[string]bool{"npm exec": true, "pnpm dlx": true, "pnpm exec": true, "yarn dlx": true,
	"yarn exec": true, "bun x": true, "pipx run": true, "uv run": true, "npm x": true, "pnpm x": true,
	"go run": true, "cargo run": true, "dotnet run": true, "uv tool": true, "deno run": true}

// plainWord is a literal argument: no shell metacharacter, quote, glob,
// backslash or env assignment.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9._/@%+,-]+$`)

// shellKeywords cannot be the command of a plain rule.
var shellKeywords = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true,
	"while": true, "until": true, "time": true, "fi": true, "done": true, "case": true, "for": true,
	"select": true, "function": true, "coproc": true}

// commandBase is a word's program name: no "cmd:*" suffix (a drive letter,
// C:\..., stays), no directory, no .exe, lower case.
func commandBase(w string) string {
	if i := strings.LastIndex(w, ":"); i > 1 {
		w = w[:i]
	}
	base := strings.ToLower(path.Base(strings.ReplaceAll(w, `\`, "/")))
	return strings.TrimSuffix(base, ".exe")
}

// commandWrappers run another command given as an argument.
var commandWrappers = map[string]bool{
	"command": true, "nice": true, "time": true, "stdbuf": true, "ionice": true, "setsid": true,
	"chrt": true, "taskset": true, "watch": true, "flock": true, "unbuffer": true, "script": true,
	"strace": true, "ltrace": true, "chroot": true, "runuser": true, "su": true, "start": true,
	"sg": true, "firejail": true, "xvfb-run": true, "gnome-terminal": true, "konsole": true, "xterm": true,
	"tmux": true, "screen": true,
}

// dropInterpreterAllows removes interpreter-wildcard entries from
// permissions.allow in a Claude settings document and returns what it
// removed. A document that is not JSON is returned unchanged.
func dropInterpreterAllows(data []byte) ([]byte, []string) {
	var doc map[string]any
	// A byte order mark would make the decode fail and the file pass whole.
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return data, nil
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		return data, nil
	}
	allow, _ := perms["allow"].([]any)
	var kept []any
	var dropped []string
	for _, e := range allow {
		if s, ok := e.(string); ok && isInterpreterAllow(s) {
			dropped = append(dropped, s)
			continue
		}
		kept = append(kept, e)
	}
	// bypassPermissions skips every prompt; the same grant in one word.
	if mode, ok := perms["defaultMode"].(string); ok && strings.EqualFold(mode, "bypassPermissions") {
		delete(perms, "defaultMode")
		dropped = append(dropped, "defaultMode: "+mode)
	}
	if len(dropped) == 0 {
		return data, nil
	}
	if kept == nil {
		kept = []any{}
	}
	if allow != nil {
		perms["allow"] = kept
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return data, nil
	}
	return out, dropped
}
