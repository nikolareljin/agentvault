package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// mcpServerPaths are where a JSON settings file holds a map of MCP
// servers: mcpServers (Claude, Gemini, Copilot, Cursor, Windsurf), servers
// and mcp.servers (VS Code style), context_servers (Zed).
var mcpServerPaths = [][]string{{"mcpServers"}, {"servers"}, {"mcp", "servers"}, {"context_servers"}}

// getPath is the value at path in doc, and whether it is there.
func getPath(doc map[string]any, path []string) (any, bool) {
	var cur any = doc
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath sets the value at path, creating missing objects on the way. It
// does nothing when a value on the way is not an object.
func setPath(doc map[string]any, path []string, v any) bool {
	m := doc
	for _, k := range path[:len(path)-1] {
		next, ok := m[k]
		if !ok {
			nm := map[string]any{}
			m[k] = nm
			m = nm
			continue
		}
		if m, ok = next.(map[string]any); !ok {
			return false
		}
	}
	m[path[len(path)-1]] = v
	return true
}

func deletePath(doc map[string]any, path []string) {
	parent := doc
	if len(path) > 1 {
		p, _ := getPath(doc, path[:len(path)-1])
		parent, _ = p.(map[string]any)
	}
	if parent != nil {
		delete(parent, path[len(path)-1])
	}
}

// errMCPAborted is returned when the user answers q at the server list.
var errMCPAborted = errors.New("import aborted at the MCP server list")

// mcpChooser settles the servers mergeMCPServers cannot settle alone.
type mcpChooser interface {
	// pickNew returns the bundle-only servers to add.
	pickNew(names []string, bundle map[string]any) (map[string]bool, error)
	// replaceChanged is whether the bundle's definition of a server both
	// sides have, and that differs, replaces this machine's.
	replaceChanged(name string, local, bundle any) (bool, error)
	// refuse is whether a server may not be offered at all.
	refuse(name string, bundle any) bool
}

// mcpMergeReport says what happened to each server that was not the same on
// both sides.
type mcpMergeReport struct {
	Added, Replaced, KeptLocal, LeftOut []string
	// EnvLeftOut are "server.VAR" variables, and "server.key" fields such
	// as envFile or headersHelper, of taken servers left out.
	EnvLeftOut []string
	// Refused are servers not offered: their command, args or URL hold a
	// secret-shaped value that would be hidden in the question.
	Refused []string
	// EnvRefused are servers not taken: they read this machine's
	// environment variables and that was not approved.
	EnvRefused []string
	// Ask lists the servers a dry run would ask about.
	Ask []string
}

func (r mcpMergeReport) empty() bool {
	return len(r.Added)+len(r.Replaced)+len(r.KeptLocal)+len(r.LeftOut)+len(r.Ask)+len(r.EnvLeftOut)+len(r.Refused)+len(r.EnvRefused) == 0
}

// changed is whether the merge takes anything from the bundle.
func (r mcpMergeReport) changed() bool { return len(r.Added)+len(r.Replaced) > 0 }

// consent is the set of servers the user took from the bundle: their
// commands are not gated.
func (r mcpMergeReport) consent() map[string]bool {
	out := map[string]bool{}
	for _, n := range append(append([]string(nil), r.Added...), r.Replaced...) {
		out[n] = true
	}
	return out
}

// mergeMCPServers merges two server maps one server at a time:
//   - only here: kept;
//   - on both sides and the same apart from the secrets the bundle blanked:
//     this machine's definition, secrets included;
//   - on both sides and different: the bundle's when ch says so, else this
//     machine's;
//   - only in the bundle: added when ch picks it, else left out.
func mergeMCPServers(local, bundle map[string]any, ch mcpChooser) (map[string]any, mcpMergeReport, error) {
	merged := map[string]any{}
	var rep mcpMergeReport
	for name, v := range local {
		merged[name] = v
	}
	var fresh []string
	for _, name := range sortedKeys(bundle) {
		bv := bundle[name]
		lv, ok := local[name]
		if !ok {
			if ch.refuse(name, bv) {
				rep.LeftOut = append(rep.LeftOut, name)
				rep.Refused = append(rep.Refused, name)
				continue
			}
			fresh = append(fresh, name)
			continue
		}
		bm, bIsMap := bv.(map[string]any)
		lm, lIsMap := lv.(map[string]any)
		if (bIsMap && lIsMap && sameServerApartFromBlanks(bm, lm)) || jsonEqual(bv, lv) {
			continue
		}
		if ch.refuse(name, bv) {
			rep.KeptLocal = append(rep.KeptLocal, name)
			rep.Refused = append(rep.Refused, name)
			continue
		}
		replace, err := ch.replaceChanged(name, lv, bv)
		if err != nil {
			return nil, rep, err
		}
		if replace {
			merged[name] = bv
			rep.Replaced = append(rep.Replaced, name)
		} else {
			rep.KeptLocal = append(rep.KeptLocal, name)
		}
	}
	if len(fresh) > 0 {
		picked, err := ch.pickNew(fresh, bundle)
		if err != nil {
			return nil, rep, err
		}
		for _, name := range fresh {
			if picked[name] {
				merged[name] = bundle[name]
				rep.Added = append(rep.Added, name)
			} else {
				rep.LeftOut = append(rep.LeftOut, name)
			}
		}
	}
	return merged, rep, nil
}

// mcpSelection is --mcp-servers: all, none, or a list of names.
type mcpSelection struct {
	all   bool
	names map[string]bool
	// seen are the server names the bundle had, in any file.
	seen map[string]bool
}

// note records a bundle server name, for unknown().
func (s *mcpSelection) note(name string) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[name] = true
}

// unknown are the names given that no bundle server had.
func (s *mcpSelection) unknown() []string {
	if s == nil {
		return nil
	}
	var out []string
	for n := range s.names {
		if !s.seen[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func parseMCPSelection(v string) (*mcpSelection, error) {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "all":
		return &mcpSelection{all: true}, nil
	case "none":
		return &mcpSelection{names: map[string]bool{}}, nil
	case "":
		return nil, errors.New("--mcp-servers needs all, none or a comma-separated list of server names")
	}
	sel := &mcpSelection{names: map[string]bool{}}
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			sel.names[n] = true
		}
	}
	if len(sel.names) == 0 {
		return nil, errors.New("--mcp-servers needs all, none or a comma-separated list of server names")
	}
	return sel, nil
}

func (s *mcpSelection) has(name string) bool {
	return s.all || s.names[name]
}

// ctxChooser is the import's chooser for one file: --mcp-servers when
// given, else --keep-bundle-commands (everything; --on-conflict keep still
// keeps changed servers), else the terminal, else nothing is taken.
type ctxChooser struct {
	c    *importApplyContext
	file string
	ask  []string
	// zed: the servers are Zed context_servers.
	zed bool
}

func (ch *ctxChooser) refuse(_ string, bundle any) bool {
	return !ch.c.KeepBundleCommands && !offerableServer(bundle, ch.zed)
}

// zedServerFields are the extra fields of a Zed context_servers entry:
// source ("custom" or "extension") and the extension's settings.
var zedServerFields = map[string]bool{"source": true, "settings": true}

// mcpServerFields are the fields a server definition from a bundle may have
// to be offered: Claude, Gemini, Copilot, Cursor and Windsurf (type,
// command, args, url, httpUrl, env, headers, timeout, disabled,
// description) and Codex (enabled, startup_timeout_sec, tool_timeout_sec,
// http_headers, bearer_token_env_var, env_http_headers, env_vars). Any
// other field (serverUrl, envFile, cwd, headersHelper, ...) may run
// something or send something this check does not know, so such a server
// is not offered without --keep-bundle-commands.
var mcpServerFields = map[string]bool{
	"type": true, "command": true, "args": true, "url": true, "httpUrl": true, "env": true, "headers": true,
	"timeout": true, "disabled": true, "description": true,
	"enabled": true, "startup_timeout_sec": true, "tool_timeout_sec": true, "http_headers": true,
	"bearer_token_env_var": true, "env_http_headers": true, "env_vars": true,
}

// mcpSendFields are where a server runs or sends to: a secret-shaped value
// in one is never shown for consent, so the server is not offered.
var mcpSendFields = []string{"command", "args", "url", "httpUrl", "headers", "http_headers"}

// offerableServer is whether a bundle server may be shown for consent:
// only known fields, and no secret-shaped value where it runs or sends, or
// in a gated env variable.
func offerableServer(v any, zed bool) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k := range m {
		if !mcpServerFields[k] && !(zed && zedServerFields[k]) {
			return false
		}
	}
	if zed && anySecretString(m["settings"]) {
		return false
	}
	for _, k := range mcpSendFields {
		if anySecretString(m[k]) {
			return false
		}
	}
	locs, inArray := mcpEnvLocations(m)
	if inArray {
		return false
	}
	for _, loc := range locs {
		ev, _ := getPath(m, loc)
		env, _ := ev.(map[string]any)
		for k, v := range env {
			if gatedEnv(k) && anySecretString(v) {
				return false
			}
		}
	}
	return true
}

func anySecretString(v any) bool {
	switch t := v.(type) {
	case string:
		return secretString(t)
	case []any:
		for _, x := range t {
			if anySecretString(x) {
				return true
			}
		}
	case map[string]any:
		for _, x := range t {
			if anySecretString(x) {
				return true
			}
		}
	}
	return false
}

// envRefPattern finds variable names in $VAR, ${VAR}, ${env:VAR},
// ${VAR:-x}, $env:VAR and %VAR% references.
var envRefPattern = regexp.MustCompile(`\$\{?(?:env:)?([A-Za-z_][A-Za-z0-9_]*)|%([A-Za-z_][A-Za-z0-9_]*)%`)

// envRefMarker is any construct that may expand a variable: a '$' anywhere,
// or a %...% pair. Fail closed: a string with one counts as reading this
// machine's environment even when no name can be parsed from it.
var envRefMarker = regexp.MustCompile(`\$|%[^%\s]+%`)

// mcpEnvRefs are the names of this machine's environment variables a
// server reads into its definition: references in any string, and the
// Codex fields that name a variable (bearer_token_env_var, env_vars,
// env_http_headers values).
func mcpEnvRefs(v any) []string {
	seen := map[string]bool{}
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch t := v.(type) {
		case string:
			if envRefMarker.MatchString(t) {
				found := false
				for _, m := range envRefPattern.FindAllStringSubmatch(t, -1) {
					seen[m[1]+m[2]] = true
					found = true
				}
				if !found {
					seen["contains $ or %...% that may expand a variable: "+t] = true
				}
			}
			if strings.HasSuffix(key, "_env_var") || key == "env_vars" || key == "env_http_headers" {
				seen[t] = true
			}
		case []any:
			for _, x := range t {
				walk(key, x)
			}
		case map[string]any:
			for k, x := range t {
				if key == "env_http_headers" {
					walk(key, x)
					continue
				}
				walk(k, x)
			}
		}
	}
	walk("", v)
	out := make([]string, 0, len(seen))
	for k := range seen {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (ch *ctxChooser) pickNew(names []string, bundle map[string]any) (map[string]bool, error) {
	c := ch.c
	out := map[string]bool{}
	switch {
	case c.MCPServers != nil:
		for _, n := range names {
			out[n] = c.MCPServers.has(n)
		}
	case c.KeepBundleCommands:
		for _, n := range names {
			out[n] = true
		}
	case c.Interactive && c.In != nil && c.DryRun:
		ch.ask = append(ch.ask, names...)
	case c.Interactive && c.In != nil:
		return pickMCPServers(c.Out, c.In, ch.file, names, bundle)
	}
	return out, nil
}

func (ch *ctxChooser) replaceChanged(name string, local, bundle any) (bool, error) {
	c := ch.c
	switch {
	case c.MCPServers != nil:
		return c.MCPServers.has(name), nil
	case c.OnConflict == conflictKeep || c.mcpAll == conflictKeep:
		return false, nil
	case c.KeepBundleCommands || c.mcpAll == conflictBackupReplace:
		return true, nil
	case c.Interactive && c.In != nil && c.DryRun:
		ch.ask = append(ch.ask, name)
		return false, nil
	case c.Interactive && c.In != nil:
		return c.askReplaceServer(ch.file, name, local, bundle)
	}
	return false, nil
}

func (c *importApplyContext) noteBundleServers(servers map[string]any) {
	if c.MCPServers == nil {
		return
	}
	for n := range servers {
		c.MCPServers.note(n)
	}
}

// askReplaceServer asks about one changed server. End of input keeps this
// machine's definition.
func (c *importApplyContext) askReplaceServer(file, name string, local, bundle any) (bool, error) {
	fmt.Fprintf(c.Out, "%s: MCP server %s differs from this machine's:\n", printable(file), strconv.Quote(name))
	for _, line := range describeDefinitionChange(local, bundle) {
		fmt.Fprintf(c.Out, "  %s\n", line)
	}
	fmt.Fprintf(c.Out, "  bundle's definition: %s\n", describeMCPServer(name, bundle))
	for {
		fmt.Fprint(c.Out, "[b]ackup + replace / [k]eep this machine's (ba, ka: same for all remaining servers) [k]: ")
		line, err := c.In.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(c.Out)
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "k":
			return false, nil
		case "b":
			return true, nil
		case "ba":
			c.mcpAll = conflictBackupReplace
			return true, nil
		case "ka":
			c.mcpAll = conflictKeep
			return false, nil
		}
		fmt.Fprintln(c.Out, "  Please answer b, k, ba or ka.")
	}
}

// pickMCPServers shows the bundle-only servers as a numbered list. None is
// selected at first; a row number toggles it, a selects all, an empty line
// accepts, q aborts. End of input picks nothing: only an accepted list is
// consent.
func pickMCPServers(out io.Writer, in interface{ ReadString(byte) (string, error) }, file string, names []string, bundle map[string]any) (map[string]bool, error) {
	picked := map[string]bool{}
	for {
		fmt.Fprintf(out, "MCP servers in the bundle that %s does not have:\n", printable(file))
		for i, n := range names {
			mark := " "
			if picked[n] {
				mark = "x"
			}
			fmt.Fprintf(out, "  [%s] %d. %s\n", mark, i+1, describeMCPServer(n, bundle[n]))
		}
		fmt.Fprint(out, "Enter a row number to select or clear it, a for all, empty line to accept, q to abort: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(out)
			return map[string]bool{}, nil
		}
		line = strings.ToLower(strings.TrimSpace(line))
		switch line {
		case "":
			return picked, nil
		case "q", "quit":
			return nil, errMCPAborted
		case "a", "all":
			for _, n := range names {
				picked[n] = true
			}
			continue
		}
		i, err := strconv.Atoi(line)
		if err != nil || i < 1 || i > len(names) {
			fmt.Fprintf(out, "No row %q.\n", printable(line))
			continue
		}
		picked[names[i-1]] = !picked[names[i-1]]
	}
}

// describeMCPServer names a server and prints every field of it verbatim:
// nothing is masked, since a masked value could hide what runs. A server
// with a secret-shaped value where it runs or sends is never offered, so
// never described. Every part comes from the bundle, so all of it is made
// printable.
func describeMCPServer(name string, v any) string {
	m, _ := v.(map[string]any)
	typ, _ := m["type"].(string)
	if typ == "" {
		typ = "stdio"
		if m["url"] != nil || m["httpUrl"] != nil {
			typ = "http"
		}
	}
	lines := []string{fmt.Sprintf("%s (%s)", printable(name), printable(typ))}
	for _, k := range sortedKeys(m) {
		if k != "type" {
			lines = append(lines, "      "+printable(k)+": "+showValue(m[k]))
		}
	}
	for _, c := range settingsCommands(m) {
		lines = append(lines, "      "+printable(c[0])+": "+c[1])
	}
	if refs := mcpEnvRefs(m); len(refs) > 0 {
		lines = append(lines, "      reads this machine's variables: "+printable(strings.Join(refs, ", ")))
	}
	return strings.Join(lines, "\n")
}

// showValue is a value as JSON (a string quoted), made printable.
func showValue(val any) string {
	if t, ok := val.(string); ok {
		return printable(strconv.Quote(t))
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(val)
	return printable(strings.TrimSuffix(b.String(), "\n"))
}

// gateTaken checks the servers taken from the bundle for what a pick does
// not consent to, unless --keep-bundle-commands:
//   - a server that reads this machine's environment variables ($VAR,
//     bearer_token_env_var, ...) is taken only after a separate yes that
//     lists each variable; without a terminal it is not taken;
//   - code-loading and request-routing env variables (gatedEnv) are kept
//     only when this machine has the same value or after a separate yes;
//     otherwise they are left out and named in rep.EnvLeftOut.
func (c *importApplyContext) gateTaken(file string, merged, local map[string]any, rep *mcpMergeReport) error {
	if c.KeepBundleCommands {
		return nil
	}
	asked := c.Interactive && c.In != nil && !c.DryRun
	untake := func(name string) {
		if lv, ok := local[name]; ok {
			merged[name] = lv
			rep.Replaced = removeString(rep.Replaced, name)
			rep.KeptLocal = append(rep.KeptLocal, name)
		} else {
			delete(merged, name)
			rep.Added = removeString(rep.Added, name)
			rep.LeftOut = append(rep.LeftOut, name)
		}
	}
	taken := append(append([]string(nil), rep.Added...), rep.Replaced...)
	sort.Strings(taken)
	for _, name := range taken {
		refs := mcpEnvRefs(merged[name])
		// Taken servers are new or changed: a changed URL or header could
		// send the same variables elsewhere, so this is always asked.
		if len(refs) == 0 {
			continue
		}
		yes := false
		if asked {
			fmt.Fprintf(c.Out, "%s: MCP server %s reads these variables of this machine, and may send them where it connects:\n  %s\n",
				printable(file), strconv.Quote(name), printable(strings.Join(refs, ", ")))
			var err error
			if yes, err = askBool(c.Out, c.In, "take this server", false); err != nil {
				return err
			}
		}
		if !yes {
			untake(name)
			rep.EnvRefused = append(rep.EnvRefused, name)
		}
	}
	taken = append(append([]string(nil), rep.Added...), rep.Replaced...)
	sort.Strings(taken)
	for _, name := range taken {
		srv, _ := merged[name].(map[string]any)
		locs, _ := mcpEnvLocations(srv)
		for _, loc := range locs {
			if err := c.gateEnvAt(file, name, loc, merged, local, rep, asked); err != nil {
				return err
			}
		}
	}
	return nil
}

// mcpEnvLocations are where a server keeps its environment: env, Zed's
// command.env, and every env object at any depth under Zed's settings.
// inArray is true when an env sits under an array there, where it cannot
// be gated by path: such a server is not offered.
func mcpEnvLocations(srv map[string]any) (locs [][]string, inArray bool) {
	locs = [][]string{{"env"}, {"command", "env"}}
	var walk func(v any, path []string, underArray bool)
	walk = func(v any, path []string, underArray bool) {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				p := append(append([]string(nil), path...), k)
				if strings.EqualFold(k, "env") {
					if underArray {
						inArray = true
					} else if _, ok := t[k].(map[string]any); ok {
						locs = append(locs, p)
					}
				}
				walk(t[k], p, underArray)
			}
		case []any:
			for _, x := range t {
				walk(x, path, true)
			}
		}
	}
	walk(srv["settings"], []string{"settings"}, false)
	return locs, inArray
}

// settingsCommands are the command fields at any depth under Zed's
// settings, as "settings.a.command" -> value.
func settingsCommands(srv map[string]any) [][2]string {
	var out [][2]string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				p := path + "." + k
				if commandField(k) {
					out = append(out, [2]string{p, showValue(t[k])})
					continue
				}
				walk(t[k], p)
			}
		case []any:
			for i, x := range t {
				walk(x, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(srv["settings"], "settings")
	return out
}

// gateEnvAt asks about, or leaves out, the gated variables of one env map
// of a taken server.
func (c *importApplyContext) gateEnvAt(file, name string, loc []string, merged, local map[string]any, rep *mcpMergeReport, asked bool) error {
	srv, ok := merged[name].(map[string]any)
	if !ok {
		return nil
	}
	ev, _ := getPath(srv, loc)
	env, _ := ev.(map[string]any)
	lsrv, _ := local[name].(map[string]any)
	lev, _ := getPath(lsrv, loc)
	lenv, _ := lev.(map[string]any)
	var gated []string
	for _, k := range sortedKeys(env) {
		if gatedEnv(k) {
			if lv, ok := lenv[k]; !ok || !jsonEqual(lv, env[k]) {
				gated = append(gated, k)
			}
		}
	}
	if len(gated) == 0 {
		return nil
	}
	label := strings.Join(loc, ".")
	yes := false
	if asked {
		fmt.Fprintf(c.Out, "%s: MCP server %s sets variables that load code or send requests elsewhere:\n", printable(file), strconv.Quote(name))
		for _, k := range gated {
			fmt.Fprintf(c.Out, "  %s %s = %s\n", label, printable(k), showValue(env[k]))
		}
		var err error
		if yes, err = askBool(c.Out, c.In, "keep these", false); err != nil {
			return err
		}
	}
	if yes {
		return nil
	}
	// A deep copy: the maps may be the bundle's own.
	ns := deepCopyJSON(srv)
	ne := map[string]any{}
	for k, v := range env {
		ne[k] = v
	}
	for _, k := range gated {
		if lv, ok := lenv[k]; ok {
			ne[k] = lv
		} else {
			delete(ne, k)
		}
		rep.EnvLeftOut = append(rep.EnvLeftOut, name+"."+k)
	}
	setPath(ns, loc, ne)
	merged[name] = ns
	return nil
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// mergeMCPKeysJSON merges the server maps of a JSON settings file one
// server at a time, against this machine's copy. It returns the new
// content, the servers taken from the bundle per key, and the report.
func (c *importApplyContext) mergeMCPKeysJSON(dest string, content, local []byte, only []string) ([]byte, map[string]map[string]bool, mcpMergeReport, error) {
	var rep mcpMergeReport
	doc, ok := decodeSettingsObject(content)
	if !ok {
		return content, nil, rep, nil
	}
	have, _ := decodeSettingsObject(local)
	consent := map[string]map[string]bool{}
	touched := false
	for _, path := range mcpServerPaths {
		key := strings.Join(path, ".")
		if len(only) > 0 && !containsString(only, key) {
			// Only these keys of the file are written (~/.claude.json).
			continue
		}
		bv, bpresent := getPath(doc, path)
		lv, _ := getPath(have, path)
		bs, bok := bv.(map[string]any)
		ls, lok := lv.(map[string]any)
		if !bok {
			if !bpresent && lok && len(ls) > 0 && setPath(doc, path, ls) {
				// The bundle has none: this machine's servers stay.
				touched = true
			}
			continue
		}
		if !lok {
			ls = map[string]any{}
		}
		c.noteBundleServers(bs)
		ch := &ctxChooser{c: c, file: dest, zed: key == "context_servers"}
		merged, r, err := mergeMCPServers(ls, bs, ch)
		if err != nil {
			return content, nil, rep, err
		}
		r.Ask = ch.ask
		if err := c.gateTaken(dest, merged, ls, &r); err != nil {
			return content, nil, rep, err
		}
		rep.EnvLeftOut = append(rep.EnvLeftOut, r.EnvLeftOut...)
		rep.EnvRefused = append(rep.EnvRefused, r.EnvRefused...)
		setPath(doc, path, merged)
		touched = true
		consent[key] = r.consent()
		rep.Added = append(rep.Added, r.Added...)
		rep.Replaced = append(rep.Replaced, r.Replaced...)
		rep.KeptLocal = append(rep.KeptLocal, r.KeptLocal...)
		rep.LeftOut = append(rep.LeftOut, r.LeftOut...)
		rep.Refused = append(rep.Refused, r.Refused...)
		rep.Ask = append(rep.Ask, r.Ask...)
	}
	if !touched {
		return content, nil, rep, nil
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return content, nil, rep, nil
	}
	return out, consent, rep, nil
}

// sameApartFromMCP is whether two JSON documents are equal once their
// server maps are left out.
func sameApartFromMCP(a, b []byte) bool {
	da, okA := decodeSettingsObject(a)
	db, okB := decodeSettingsObject(b)
	if !okA || !okB {
		return false
	}
	for _, p := range mcpServerPaths {
		deletePath(da, p)
		deletePath(db, p)
	}
	return jsonEqual(da, db)
}

// formatMCPReport is the one line for a file, or "".
func formatMCPReport(file string, rep mcpMergeReport, dry bool) string {
	if rep.empty() {
		return ""
	}
	var parts []string
	add := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		q := make([]string, len(names))
		for i, n := range names {
			q[i] = strconv.Quote(n)
		}
		parts = append(parts, label+" "+strings.Join(q, ", "))
	}
	if dry {
		add("would add", rep.Added)
		add("would replace", rep.Replaced)
		add("would keep local", rep.KeptLocal)
		add("would leave out", rep.LeftOut)
		add("would leave out variables", rep.EnvLeftOut)
		add("would not offer, an unknown field or a secret-shaped value (review the bundle)", rep.Refused)
		add("would not take, reads this machine's variables", rep.EnvRefused)
		add("would ask about", rep.Ask)
	} else {
		add("added", rep.Added)
		add("replaced", rep.Replaced)
		add("kept local", rep.KeptLocal)
		add("left out", rep.LeftOut)
		add("left out variables", rep.EnvLeftOut)
		add("not offered, an unknown field or a secret-shaped value (review the bundle)", rep.Refused)
		add("not taken, reads this machine's variables (approve on a terminal or use --keep-bundle-commands)", rep.EnvRefused)
	}
	return printable(file) + ": " + strings.Join(parts, "; ")
}

// --- Codex config.toml ---

// validCodexServerName is a server name import will write as a TOML table
// name: no quotes, backslashes, brackets or control characters. Dots and
// spaces are written quoted.
func validCodexServerName(name string) bool {
	if name == "" || len(name) > 200 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '[' || r == ']' || r == '\'' {
			return false
		}
	}
	return true
}

// tomlTableKeys parses the key path of a table header line such as
// [mcp_servers."a.b".env] or [[x.y]]. ok is false for anything else.
func tomlTableKeys(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	double := strings.HasPrefix(s, "[[")
	if double {
		s = s[2:]
	} else {
		s = s[1:]
	}
	var keys []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return nil, false
		}
		var key string
		switch s[0] {
		case '"':
			end := 1
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return nil, false
			}
			k, err := strconv.Unquote(s[:end+1])
			if err != nil {
				return nil, false
			}
			key, s = k, s[end+1:]
		case '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return nil, false
			}
			key, s = s[1:end+1], s[end+2:]
		default:
			end := 0
			for end < len(s) && (s[end] == '_' || s[end] == '-' || s[end] >= 'a' && s[end] <= 'z' ||
				s[end] >= 'A' && s[end] <= 'Z' || s[end] >= '0' && s[end] <= '9') {
				end++
			}
			if end == 0 {
				return nil, false
			}
			key, s = s[:end], s[end:]
		}
		keys = append(keys, key)
		s = strings.TrimLeft(s, " \t")
		if strings.HasPrefix(s, ".") {
			s = s[1:]
			continue
		}
		closing := "]"
		if double {
			closing = "]]"
		}
		if !strings.HasPrefix(s, closing) {
			return nil, false
		}
		rest := strings.TrimSpace(s[len(closing):])
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return nil, false
		}
		return keys, true
	}
}

// editCodexServerTables drops the [mcp_servers.<name>] tables, and their
// sub-tables, of the servers in drop from a config.toml text, and writes
// swap[name] where the first of them was. Comment and blank lines just
// before the next kept table belong to it and stay; every other line stays
// as it is.
func editCodexServerTables(text string, drop map[string]bool, swap map[string]string) string {
	if len(drop) == 0 {
		return text
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	var b, pending strings.Builder
	skipping := false
	written := map[string]bool{}
	for _, line := range strings.SplitAfter(text, "\n") {
		if keys, ok := tomlTableKeys(line); ok {
			now := len(keys) >= 2 && keys[0] == "mcp_servers" && drop[keys[1]]
			if skipping && !now {
				keep := pending.String()
				if strings.HasSuffix(b.String(), nl+nl) || b.Len() == 0 {
					// No second blank line where a table was taken out.
					keep = strings.TrimLeft(keep, "\r\n")
				}
				b.WriteString(keep)
			}
			pending.Reset()
			skipping = now
			if now && !written[keys[1]] {
				written[keys[1]] = true
				if t, ok := swap[keys[1]]; ok {
					b.WriteString(t)
					if !strings.HasSuffix(t, nl+nl) {
						pending.WriteString(nl)
					}
				}
			}
		}
		if !skipping {
			b.WriteString(line)
			continue
		}
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			pending.WriteString(line)
		} else {
			pending.Reset()
		}
	}
	return b.String()
}

// encodeCodexServer writes one server as [mcp_servers.<name>] tables.
func encodeCodexServer(name string, v any) (string, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(map[string]any{"mcp_servers": map[string]any{name: v}}); err != nil {
		return "", err
	}
	// The encoder opens with an empty [mcp_servers] table; a file may only
	// define it once, so it is left out.
	s := buf.String()
	s = strings.TrimPrefix(s, "[mcp_servers]\n")
	return strings.TrimLeft(s, "\n"), nil
}

// mergeCodexServers merges the [mcp_servers] of a Codex config.toml one
// server at a time. base is the text the result is built from (this
// machine's file, or the bundle's); its other lines are kept. ok is false
// when the result cannot be built so that it parses to exactly the merged
// servers and base's other settings: the caller then falls back to the
// whole-file rule.
func mergeCodexServerText(base string, baseServers, merged map[string]any) (string, bool) {
	drop := map[string]bool{}
	swap := map[string]string{}
	var add []string
	for _, name := range sortedKeys(merged) {
		if bv, ok := baseServers[name]; ok && jsonEqual(bv, merged[name]) {
			continue
		}
		if !validCodexServerName(name) {
			return "", false
		}
		t, err := encodeCodexServer(name, merged[name])
		if err != nil {
			return "", false
		}
		if _, ok := baseServers[name]; ok {
			drop[name], swap[name] = true, t
		} else {
			add = append(add, t)
		}
	}
	for name := range baseServers {
		if _, ok := merged[name]; !ok {
			if !validCodexServerName(name) {
				return "", false
			}
			drop[name] = true
		}
	}
	if strings.Contains(base, "\r\n") {
		// Inserted tables use the file's line ending.
		for k, t := range swap {
			swap[k] = strings.ReplaceAll(t, "\n", "\r\n")
		}
		for i, t := range add {
			add[i] = strings.ReplaceAll(t, "\n", "\r\n")
		}
	}
	out := editCodexServerTables(base, drop, swap)
	nl := "\n"
	if strings.Contains(base, "\r\n") {
		nl = "\r\n"
	}
	for _, t := range add {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		if out != "" && !strings.HasSuffix(out, nl+nl) {
			out += nl
		}
		out += t
	}
	// Check the text, built by line, against what it must say.
	var got, want map[string]any
	if _, err := toml.Decode(out, &got); err != nil {
		return "", false
	}
	if _, err := toml.Decode(base, &want); err != nil {
		return "", false
	}
	gs, _ := got["mcp_servers"].(map[string]any)
	if len(gs) != len(merged) || (len(merged) > 0 && !jsonEqual(gs, merged)) {
		return "", false
	}
	delete(got, "mcp_servers")
	delete(want, "mcp_servers")
	if !jsonEqual(got, want) {
		return "", false
	}
	return out, true
}

// codexDecode is a config.toml decoded, or nil, and its servers.
func codexDecode(text []byte) (map[string]any, map[string]any, bool) {
	var doc map[string]any
	if _, err := toml.Decode(string(text), &doc); err != nil {
		return nil, nil, false
	}
	if doc == nil {
		doc = map[string]any{}
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	if _, present := doc["mcp_servers"]; present && servers == nil {
		return nil, nil, false
	}
	return doc, servers, true
}

// codexMerge is the outcome of mergeCodexConfig.
type codexMerge struct {
	Content []byte
	// OK is false when the servers could not be merged per server: the
	// whole-file rule applies to Content, which is the bundle's.
	OK bool
	// LocalBase: the result is this machine's file with servers edited.
	LocalBase bool
	// RestDropped: the bundle changed a command outside mcp_servers, which
	// was not taken.
	RestDropped bool
	Report      mcpMergeReport
}

// mergeCodexConfig settles the MCP servers of a Codex config.toml per
// server.
func (c *importApplyContext) mergeCodexConfig(dest string, content, local []byte) (codexMerge, error) {
	none := codexMerge{Content: content}
	bdoc, bservers, ok := codexDecode(content)
	if !ok {
		return none, nil
	}
	ldoc, lservers, ok := codexDecode(local)
	if !ok {
		return none, nil
	}
	if len(bservers) == 0 && len(lservers) == 0 {
		return none, nil
	}
	if lservers == nil {
		lservers = map[string]any{}
	}
	if bservers == nil {
		bservers = map[string]any{}
	}
	c.noteBundleServers(bservers)
	ch := &ctxChooser{c: c, file: dest}
	merged, rep, err := mergeMCPServers(lservers, bservers, ch)
	if err != nil {
		return none, err
	}
	rep.Ask = ch.ask
	if err := c.gateTaken(dest, merged, lservers, &rep); err != nil {
		return none, err
	}
	// Build on this machine's text when the rest is the same, or when the
	// rest changes a command that is not taken: then only servers change.
	restSame := jsonEqualWithout(bdoc, ldoc, "mcp_servers")
	restDropped := !restSame && !c.KeepBundleCommands && codexCommandsChangedApartFromServers(bdoc, ldoc)
	useLocal := restSame || restDropped
	base, baseServers := string(content), bservers
	if useLocal {
		base, baseServers = string(local), lservers
	}
	out, ok := mergeCodexServerText(base, baseServers, merged)
	if !ok {
		none.Report = rep
		return none, nil
	}
	return codexMerge{Content: []byte(out), OK: true, LocalBase: useLocal, RestDropped: restDropped, Report: rep}, nil
}

func jsonEqualWithout(a, b map[string]any, key string) bool {
	ca, cb := map[string]any{}, map[string]any{}
	for k, v := range a {
		if k != key {
			ca[k] = v
		}
	}
	for k, v := range b {
		if k != key {
			cb[k] = v
		}
	}
	return jsonEqual(ca, cb)
}

// codexCommandsChangedApartFromServers is codexCommandsChanged for decoded
// documents, with the top-level mcp_servers left to the per-server merge.
func codexCommandsChangedApartFromServers(bundle, local map[string]any) bool {
	bv, lv := codexCommandView(bundle), codexCommandView(local)
	delete(bv, "/mcp_servers")
	delete(lv, "/mcp_servers")
	for p, v := range bv {
		if l, ok := lv[p]; !ok || !jsonEqual(v, l) {
			return true
		}
	}
	return false
}
