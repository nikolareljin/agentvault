package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/nikolareljin/agentvault/internal/safefile"
)

// Codex keeps its marketplaces and the enabled state of each plugin in
// ~/.codex/config.toml, and the plugin files in
// plugins/cache/<marketplace>/<name>/<version>. config.toml alone installs
// nothing: Codex lists a plugin only once `codex plugin add` has filled the
// cache. So, as for Claude, the bundle carries an inventory and import runs
// the CLI. Codex has no plugin scope and no disable command: enabled state
// is written into config.toml.

// codexConfigPlugins is the part of config.toml this file reads.
type codexConfigPlugins struct {
	Marketplaces map[string]struct {
		SourceType  string   `toml:"source_type"`
		Source      string   `toml:"source"`
		Ref         string   `toml:"ref"`
		SparsePaths []string `toml:"sparse_paths"`
	} `toml:"marketplaces"`
	Plugins map[string]struct {
		// Enabled is nil when the table does not say; `codex plugin add`
		// always writes it.
		Enabled *bool `toml:"enabled"`
	} `toml:"plugins"`
}

func readCodexConfigPlugins(home string) (codexConfigPlugins, error) {
	var c codexConfigPlugins
	path := filepath.Join(home, ".codex", "config.toml")
	data, err := safefile.Read(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("reading %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &c); err != nil {
		return c, fmt.Errorf("parsing %s: %w", path, err)
	}
	return c, nil
}

// codexCachedVersion is the version directory of a plugin in the cache, the
// newest when there are several, or "" when it is not installed.
func codexCachedVersion(home, id string) string {
	at := strings.LastIndex(id, "@")
	if at < 0 {
		return ""
	}
	dir := filepath.Join(home, ".codex", "plugins", "cache", id[at+1:], id[:at])
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestAt := "", time.Time{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() {
			continue
		}
		if best == "" || info.ModTime().After(bestAt) {
			best, bestAt = e.Name(), info.ModTime()
		}
	}
	return best
}

// collectCodexPluginInventory lists the Codex marketplaces added to
// config.toml and the plugins it has a table for. The built-in marketplaces
// are on every machine and have no table, so they are not listed.
func collectCodexPluginInventory(homeDir string) (*PluginInventory, error) {
	c, err := readCodexConfigPlugins(homeDir)
	if err != nil {
		return nil, err
	}
	inv := &PluginInventory{}
	for name, m := range c.Marketplaces {
		src := m.Source
		if m.SourceType != "local" {
			src = withoutURLCredentials(src)
		}
		inv.Marketplaces = append(inv.Marketplaces, PluginMarketplace{Name: name, Source: src, Kind: m.SourceType,
			Ref: m.Ref, Sparse: m.SparsePaths})
	}
	sort.Slice(inv.Marketplaces, func(i, j int) bool { return inv.Marketplaces[i].Name < inv.Marketplaces[j].Name })
	for id, p := range c.Plugins {
		on := p.Enabled == nil || *p.Enabled
		inv.Plugins = append(inv.Plugins, InstalledPlugin{ID: id, Version: codexCachedVersion(homeDir, id), Scope: "user", Enabled: &on})
	}
	sort.Slice(inv.Plugins, func(i, j int) bool { return inv.Plugins[i].ID < inv.Plugins[j].ID })
	if len(inv.Marketplaces) == 0 && len(inv.Plugins) == 0 {
		return nil, nil
	}
	return inv, nil
}

// readLocalCodexPlugins is what this machine has. A plugin counts as
// installed only with files in the cache: a table copied in from another
// machine installs nothing.
func readLocalCodexPlugins(home string) (localPlugins, error) {
	l := localPlugins{markets: map[string]string{}, installed: map[string]string{}, enabled: map[string]bool{}}
	c, err := readCodexConfigPlugins(home)
	if err != nil {
		return l, err
	}
	for name, m := range c.Marketplaces {
		l.markets[name] = m.Source
	}
	for id, p := range c.Plugins {
		l.enabled[id] = p.Enabled == nil || *p.Enabled
		if v := codexCachedVersion(home, id); v != "" {
			l.installed[id] = v
		}
	}
	return l, nil
}

// safeCodexRef is a git ref or sparse path import passes as an argument.
var safeCodexRef = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)

// placeCodexMarketplaces rewrites the path of each local marketplace for
// this machine, under the same rule as local-scope Claude projects: only
// under a place this import confirmed. It returns a copy; inv is unchanged.
func placeCodexMarketplaces(inv *PluginInventory, remaps []pathRemap, confirmed []string) *PluginInventory {
	if inv == nil {
		return nil
	}
	out := *inv
	out.Marketplaces = make([]PluginMarketplace, len(inv.Marketplaces))
	for i, m := range inv.Marketplaces {
		m.placed = ""
		if m.Kind == "local" {
			to, _ := remapText(m.Source, remaps)
			if to = filepath.Clean(to); placedUnder(to, confirmed) {
				m.placed = to
			}
		}
		out.Marketplaces[i] = m
	}
	return &out
}

// planCodexPluginImport compares the bundle's Codex plugins with this
// machine, as planPluginImport does for Claude.
func planCodexPluginImport(inv *PluginInventory, local localPlugins) (steps []pluginStep, notes []string) {
	if inv == nil {
		return nil, nil
	}
	for _, m := range inv.Marketplaces {
		have, ok := local.markets[m.Name]
		src := m.Source
		if m.Kind == "local" {
			src = m.placed
		}
		switch {
		case ok && (have == m.Source || have == src):
			notes = append(notes, fmt.Sprintf("marketplace %s: already here", strconv.Quote(m.Name)))
		case ok:
			notes = append(notes, fmt.Sprintf("marketplace %s: kept, source here %s, in bundle %s",
				strconv.Quote(m.Name), strconv.Quote(have), strconv.Quote(m.Source)))
		case !safeMarketplaceName.MatchString(m.Name):
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, not a plain name", strconv.Quote(m.Name)))
		case m.Kind == "local" && src == "":
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, a local directory on the exporting machine (%s) that this import did not place",
				strconv.Quote(m.Name), strconv.Quote(m.Source)))
		case m.Kind != "local" && !safeMarketplaceSource.MatchString(src):
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, source %s is not owner/repo or an https URL",
				strconv.Quote(m.Name), strconv.Quote(m.Source)))
		case m.Kind != "local" && !codexRefsSafe(m):
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, its ref or sparse paths are not plain paths", strconv.Quote(m.Name)))
		default:
			args := []string{"plugin", "marketplace", "add"}
			if m.Kind != "local" {
				if m.Ref != "" {
					args = append(args, "--ref", m.Ref)
				}
				for _, p := range m.Sparse {
					args = append(args, "--sparse", p)
				}
			}
			steps = append(steps, pluginStep{
				what: fmt.Sprintf("add marketplace from %s (named %s)", strconv.Quote(src), strconv.Quote(m.Name)),
				args: append(args, src),
				adds: m.Name,
			})
		}
	}
	listed := map[string]bool{}
	for _, m := range inv.Marketplaces {
		listed[m.Name] = true
	}
	for _, p := range inv.Plugins {
		if !safePluginID.MatchString(p.ID) {
			notes = append(notes, fmt.Sprintf("plugin %s: skipped, not name@marketplace", strconv.Quote(p.ID)))
			continue
		}
		have, installed := local.installed[p.ID]
		if !installed {
			s := pluginStep{
				what: "install plugin " + p.ID,
				args: []string{"plugin", "add", p.ID},
			}
			// A built-in marketplace (openai-curated) has no table and is
			// on every machine; only one the bundle lists can be missing.
			if m := p.ID[strings.LastIndex(p.ID, "@")+1:]; listed[m] {
				s.market = m
			}
			if p.Enabled != nil && !*p.Enabled {
				// codex plugin add enables it, so this comes after.
				s.what += " (disabled, as in the bundle)"
				off := false
				s.setOn = &off
			}
			steps = append(steps, s)
			continue
		}
		same := true
		if on := local.enabled[p.ID]; p.Enabled != nil && on != *p.Enabled {
			same = false
			verb := "enable"
			if !*p.Enabled {
				verb = "disable"
			}
			want := *p.Enabled
			steps = append(steps, pluginStep{
				what:     fmt.Sprintf("%s plugin %s (%s here, %s in bundle)", verb, p.ID, onOff(on), onOff(want)),
				setOn:    &want,
				conflict: true,
			})
		}
		if p.Version != "" && have != p.Version {
			same = false
			// Codex has no update command; the version is the marketplace's.
			notes = append(notes, fmt.Sprintf("plugin %s: version %s here, %s in bundle; kept",
				p.ID, strconv.Quote(have), strconv.Quote(p.Version)))
		}
		if same {
			notes = append(notes, fmt.Sprintf("plugin %s: already here", p.ID))
		}
	}
	return steps, notes
}

func codexRefsSafe(m PluginMarketplace) bool {
	if m.Ref != "" && !safeCodexRef.MatchString(m.Ref) {
		return false
	}
	for _, p := range m.Sparse {
		if !safeCodexRef.MatchString(p) {
			return false
		}
	}
	return true
}

// runCodex runs the codex CLI as runClaude runs claude. Tests replace it.
var runCodex = func(ctx context.Context, dir string, in io.Reader, out io.Writer, args ...string) error {
	path, err := exec.LookPath("codex")
	if err != nil {
		return errors.New("codex is not on PATH")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c := exec.CommandContext(ctx, path, args...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = in, out, out
	return c.Run()
}

// codexPluginTool settles Codex plugins in home.
func codexPluginTool(home string) *pluginTool {
	return &pluginTool{
		cli:   "codex",
		title: "Codex plugins",
		run: func(ctx context.Context, dir string, in io.Reader, out io.Writer, args ...string) error {
			return runCodex(ctx, dir, in, out, args...)
		},
		setEnabled: func(id string, on bool) (string, error) {
			return setCodexPluginEnabled(home, id, on, time.Now())
		},
		manual: func(id string, on bool) string {
			return fmt.Sprintf("set enabled = %t under [plugins.%s] in ~/.codex/config.toml", on, strconv.Quote(id))
		},
	}
}

// setCodexPluginEnabled writes enabled = on into the [plugins."id"] table of
// config.toml, adding the table when there is none, after a backup. The
// result must decode to the same settings apart from that one value, or
// nothing is written. It returns the backup path.
func setCodexPluginEnabled(home, id string, on bool, at time.Time) (string, error) {
	dest := filepath.Join(home, ".codex", "config.toml")
	data, err := safefile.Read(dest)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reading %s: %w", dest, err)
	}
	text := string(data)
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	line := fmt.Sprintf("enabled = %t", on)
	header := "[plugins." + strconv.Quote(id) + "]"
	var b strings.Builder
	inTable, done := false, false
	for _, l := range strings.SplitAfter(text, "\n") {
		if keys, ok := tomlTableKeys(l); ok {
			if inTable && !done {
				b.WriteString(line + nl)
				done = true
			}
			inTable = len(keys) == 2 && keys[0] == "plugins" && keys[1] == id && !strings.HasPrefix(strings.TrimSpace(l), "[[")
			b.WriteString(l)
			continue
		}
		if inTable && !done {
			if k, _, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "enabled" {
				b.WriteString(line + nl)
				done = true
				continue
			}
		}
		b.WriteString(l)
	}
	out := b.String()
	if inTable && !done {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		out += line + nl
		done = true
	}
	if !done {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += nl
		}
		if out != "" {
			out += nl
		}
		out += header + nl + line + nl
	}
	if err := sameCodexApartFromEnabled(data, []byte(out), id, on); err != nil {
		return "", fmt.Errorf("%s: %w", dest, err)
	}
	backup := ""
	if len(data) > 0 {
		if backup, err = writeExclusiveBackup(dest, data, at); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return backup, err
	}
	return backup, writeFileAtomicKeepMode(dest, []byte(out))
}

// sameCodexApartFromEnabled checks an edit of config.toml: after decoding,
// it differs from before only in plugins.<id>.enabled, which is on.
func sameCodexApartFromEnabled(before, after []byte, id string, on bool) error {
	var a, b map[string]any
	if _, err := toml.Decode(string(before), &a); err != nil {
		return errors.New("does not parse, so the plugin's enabled state cannot be set")
	}
	if _, err := toml.Decode(string(after), &b); err != nil {
		return errors.New("could not set the plugin's enabled state (unusual layout)")
	}
	if a == nil {
		a = map[string]any{}
	}
	plugins, _ := a["plugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	table, _ := plugins[id].(map[string]any)
	copied := map[string]any{}
	for k, v := range table {
		copied[k] = v
	}
	copied["enabled"] = on
	plugins[id] = copied
	a["plugins"] = plugins
	if !jsonEqual(a, b) {
		return errors.New("could not set the plugin's enabled state without changing anything else (unusual layout)")
	}
	return nil
}

// keepLocalCodexPluginTables returns the bundle's config.toml with this
// machine's [plugins] and [marketplaces] tables in place of its own: the
// bundle's are settled one by one under --plugins. ok is false when the
// result cannot be built so that it decodes to exactly that; changed is
// whether the bundle's tables differed from this machine's.
func keepLocalCodexPluginTables(content, local []byte) (out []byte, changed, ok bool) {
	var bdoc, ldoc map[string]any
	if _, err := toml.Decode(string(content), &bdoc); err != nil {
		return content, false, true // the whole-file checks refuse it
	}
	if len(bytes.TrimSpace(local)) > 0 {
		if _, err := toml.Decode(string(local), &ldoc); err != nil {
			return content, false, false
		}
	}
	roots := []string{"plugins", "marketplaces"}
	if jsonEqual(pick(bdoc, roots), pick(ldoc, roots)) {
		return content, false, true
	}
	isRoot := func(keys []string) (string, bool) {
		if len(keys) > 0 && (keys[0] == "plugins" || keys[0] == "marketplaces") {
			return "x", true
		}
		return "", false
	}
	stripped := editTOMLTables(string(content), isRoot, nil)
	var kept strings.Builder
	in := false
	for _, l := range strings.SplitAfter(string(local), "\n") {
		if keys, isTable := tomlTableKeys(l); isTable {
			_, in = isRoot(keys)
		}
		if in {
			kept.WriteString(l)
		}
	}
	nl := "\n"
	if strings.Contains(string(content), "\r\n") {
		nl = "\r\n"
	}
	text := stripped
	if k := strings.TrimLeft(kept.String(), "\r\n"); k != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += nl
		}
		if text != "" && !strings.HasSuffix(text, nl+nl) {
			text += nl
		}
		text += k
		if !strings.HasSuffix(text, "\n") {
			text += nl
		}
	}
	var odoc map[string]any
	if _, err := toml.Decode(text, &odoc); err != nil {
		return content, true, false
	}
	want := map[string]any{}
	for k, v := range bdoc {
		want[k] = v
	}
	for _, r := range roots {
		delete(want, r)
		if v, ok := ldoc[r]; ok {
			want[r] = v
		}
	}
	if !jsonEqual(want, odoc) {
		return content, true, false
	}
	return []byte(text), true, true
}

func pick(doc map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	return out
}
