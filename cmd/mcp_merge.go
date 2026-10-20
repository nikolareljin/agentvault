package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// mcpServerKeys are the top-level keys that hold a map of MCP servers in a
// JSON settings file: mcpServers (Claude, Gemini, Copilot, Cursor,
// Windsurf) and servers (VS Code style).
var mcpServerKeys = []string{"mcpServers", "servers"}

// errMCPAborted is returned when the user answers q at the server list.
var errMCPAborted = errors.New("import aborted at the MCP server list")

// mcpChooser settles the servers mergeMCPServers cannot settle alone.
type mcpChooser interface {
	// pickNew returns the bundle-only servers to add.
	pickNew(names []string, bundle map[string]any) (map[string]bool, error)
	// replaceChanged is whether the bundle's definition of a server both
	// sides have, and that differs, replaces this machine's.
	replaceChanged(name string, local, bundle any) (bool, error)
}

// mcpMergeReport says what happened to each server that was not the same on
// both sides.
type mcpMergeReport struct {
	Added, Replaced, KeptLocal, LeftOut []string
	// Ask lists the servers a dry run would ask about.
	Ask []string
}

func (r mcpMergeReport) empty() bool {
	return len(r.Added)+len(r.Replaced)+len(r.KeptLocal)+len(r.LeftOut)+len(r.Ask) == 0
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
			fresh = append(fresh, name)
			continue
		}
		bm, bIsMap := bv.(map[string]any)
		lm, lIsMap := lv.(map[string]any)
		if (bIsMap && lIsMap && sameServerApartFromBlanks(bm, lm)) || jsonEqual(bv, lv) {
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

// askReplaceServer asks about one changed server. End of input keeps this
// machine's definition.
func (c *importApplyContext) askReplaceServer(file, name string, local, bundle any) (bool, error) {
	fmt.Fprintf(c.Out, "%s: MCP server %s differs from this machine's:\n", printable(file), strconv.Quote(name))
	for _, line := range describeDefinitionChange(local, bundle) {
		fmt.Fprintf(c.Out, "  %s\n", line)
	}
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
// accepts, q aborts. End of input accepts the selection so far.
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
			return picked, nil
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

// describeMCPServer is one line naming a server, its type and what it runs
// or reaches. Every part comes from the bundle, so all of it is made
// printable.
func describeMCPServer(name string, v any) string {
	m, _ := v.(map[string]any)
	typ, _ := m["type"].(string)
	target := ""
	if u, ok := m["url"].(string); ok && u != "" {
		target = u
		if typ == "" {
			typ = "http"
		}
	} else if u, ok := m["httpUrl"].(string); ok && u != "" {
		target = u
		if typ == "" {
			typ = "http"
		}
	} else if cmd, ok := m["command"].(string); ok {
		target = cmd
		if args, ok := m["args"].([]any); ok {
			for _, a := range args {
				target += " " + fmt.Sprint(a)
			}
		}
		if typ == "" {
			typ = "stdio"
		}
	}
	if typ == "" {
		typ = "unknown"
	}
	return fmt.Sprintf("%s (%s) %s", printable(name), printable(typ), printable(clip(target)))
}

// mergeMCPKeysJSON merges the server maps of a JSON settings file one
// server at a time, against this machine's copy. It returns the new
// content, the servers taken from the bundle per key, and the report.
func (c *importApplyContext) mergeMCPKeysJSON(dest string, content, local []byte) ([]byte, map[string]map[string]bool, mcpMergeReport, error) {
	var rep mcpMergeReport
	doc, ok := decodeSettingsObject(content)
	if !ok {
		return content, nil, rep, nil
	}
	have, _ := decodeSettingsObject(local)
	consent := map[string]map[string]bool{}
	touched := false
	for _, key := range mcpServerKeys {
		bs, bok := doc[key].(map[string]any)
		ls, lok := have[key].(map[string]any)
		if !bok {
			if _, present := doc[key]; !present && lok && len(ls) > 0 {
				// The bundle has none: this machine's servers stay.
				doc[key] = ls
				touched = true
			}
			continue
		}
		if !lok {
			ls = map[string]any{}
		}
		ch := &ctxChooser{c: c, file: dest}
		merged, r, err := mergeMCPServers(ls, bs, ch)
		if err != nil {
			return content, nil, rep, err
		}
		r.Ask = ch.ask
		doc[key] = merged
		touched = true
		consent[key] = r.consent()
		rep.Added = append(rep.Added, r.Added...)
		rep.Replaced = append(rep.Replaced, r.Replaced...)
		rep.KeptLocal = append(rep.KeptLocal, r.KeptLocal...)
		rep.LeftOut = append(rep.LeftOut, r.LeftOut...)
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
	for _, k := range mcpServerKeys {
		delete(da, k)
		delete(db, k)
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
		add("would ask about", rep.Ask)
	} else {
		add("added", rep.Added)
		add("replaced", rep.Replaced)
		add("kept local", rep.KeptLocal)
		add("left out", rep.LeftOut)
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
	var b, pending strings.Builder
	skipping := false
	written := map[string]bool{}
	for _, line := range strings.SplitAfter(text, "\n") {
		if keys, ok := tomlTableKeys(line); ok {
			now := len(keys) >= 2 && keys[0] == "mcp_servers" && drop[keys[1]]
			if skipping && !now {
				keep := pending.String()
				if strings.HasSuffix(b.String(), "\n\n") || b.Len() == 0 {
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
					if !strings.HasSuffix(t, "\n\n") {
						pending.WriteString("\n")
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
	out := editCodexServerTables(base, drop, swap)
	for _, t := range add {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" && !strings.HasSuffix(out, "\n\n") {
			out += "\n"
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
	ch := &ctxChooser{c: c, file: dest}
	merged, rep, err := mergeMCPServers(lservers, bservers, ch)
	if err != nil {
		return none, err
	}
	rep.Ask = ch.ask
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
