package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// pluginPolicy is what import does with the bundle's Claude Code plugins.
type pluginPolicy string

const (
	// pluginsAsk asks per marketplace and plugin. Needs a terminal.
	pluginsAsk pluginPolicy = "ask"
	// pluginsPrint runs nothing and prints the commands.
	pluginsPrint pluginPolicy = "print"
	// pluginsMissing adds what is missing and keeps this machine's side of
	// every conflict.
	pluginsMissing pluginPolicy = "missing"
	// pluginsBundle adds what is missing and takes the bundle's side of
	// every conflict.
	pluginsBundle pluginPolicy = "bundle"
)

func parsePluginPolicy(v string) (pluginPolicy, error) {
	switch p := pluginPolicy(v); p {
	case pluginsAsk, pluginsPrint, pluginsMissing, pluginsBundle:
		return p, nil
	}
	return "", fmt.Errorf("--plugins %q: want ask, print, missing or bundle", v)
}

// resolvePluginPolicy is the policy for this run: print for a dry run, else
// the flag, else ask on a terminal and print without one. Installing a
// plugin runs its code, so nothing is installed without a person saying so
// here or by the flag.
func resolvePluginPolicy(flag string, interactive, dryRun bool) (pluginPolicy, error) {
	if dryRun {
		if flag != "" {
			if _, err := parsePluginPolicy(flag); err != nil {
				return "", err
			}
		}
		return pluginsPrint, nil
	}
	if flag == "" {
		if interactive {
			return pluginsAsk, nil
		}
		return pluginsPrint, nil
	}
	p, err := parsePluginPolicy(flag)
	if err != nil {
		return "", err
	}
	if p == pluginsAsk && !interactive {
		return "", errors.New("--plugins ask needs a terminal; use missing, bundle or print")
	}
	return p, nil
}

// localPlugins is what this machine has, read from the same files export
// reads. Only user-scope installs count: the bundle's plugins are installed
// for the user.
type localPlugins struct {
	markets   map[string]string   // name -> source
	installed map[string]string   // id -> version
	enabled   map[string]bool     // explicit enabledPlugins entries only
	localAt   map[string][]string // id -> project dirs of local installs
}

func readLocalPlugins(home string) (localPlugins, error) {
	l := localPlugins{markets: map[string]string{}, installed: map[string]string{}, enabled: map[string]bool{}, localAt: map[string][]string{}}
	inv, err := collectClaudePluginInventory(home)
	if err != nil {
		return l, err
	}
	if inv != nil {
		for _, m := range inv.Marketplaces {
			l.markets[m.Name] = m.Source
		}
		for _, p := range inv.Plugins {
			if p.userScope() {
				l.installed[p.ID] = p.Version
			}
			l.localAt[p.ID] = p.LocalProjects
		}
	}
	enabled, err := readEnabledPlugins(home)
	if err != nil {
		return l, err
	}
	l.enabled = enabled
	return l, nil
}

// readEnabledPlugins is the enabledPlugins map of the user's Claude settings.
func readEnabledPlugins(home string) (map[string]bool, error) {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := readJSONIfPresent(filepath.Join(home, ".claude", "settings.json"), &settings); err != nil {
		return nil, err
	}
	if settings.EnabledPlugins == nil {
		return map[string]bool{}, nil
	}
	return settings.EnabledPlugins, nil
}

// pluginStep is one change import can make. A conflict step changes
// something this machine already has; the others add what it lacks.
type pluginStep struct {
	what     string // shown to the person: "add marketplace x (o/r)"
	args     []string
	conflict bool
	// market is the marketplace a plugin install needs; adds is the one a
	// marketplace step adds.
	market, adds string
	// then runs after args succeeded, for a fresh install the bundle has
	// disabled.
	then []string
	// dir is where claude runs: the project of a local-scope install.
	dir string
}

// planPluginImport compares the bundle with this machine. It returns the
// steps that would change something and the lines for what it leaves as it
// is, so the report covers every marketplace and plugin.
func planPluginImport(inv *PluginInventory, local localPlugins) (steps []pluginStep, notes []string) {
	if inv == nil {
		return nil, nil
	}
	for _, m := range inv.Marketplaces {
		have, ok := local.markets[m.Name]
		switch {
		case ok && have == m.Source:
			notes = append(notes, fmt.Sprintf("marketplace %s: already here", strconv.Quote(m.Name)))
		case ok:
			notes = append(notes, fmt.Sprintf("marketplace %s: kept, source here %s, in bundle %s",
				strconv.Quote(m.Name), strconv.Quote(have), strconv.Quote(m.Source)))
		case !safeMarketplaceName.MatchString(m.Name):
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, not a plain name", strconv.Quote(m.Name)))
		case m.Kind == "directory":
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, a local directory on the exporting machine (%s)",
				strconv.Quote(m.Name), strconv.Quote(m.Source)))
		case !safeMarketplaceSource.MatchString(m.Source):
			notes = append(notes, fmt.Sprintf("marketplace %s: not added, source %s is not owner/repo or an https URL",
				strconv.Quote(m.Name), strconv.Quote(m.Source)))
		default:
			steps = append(steps, pluginStep{
				what: fmt.Sprintf("add marketplace from %s (named %s)", strconv.Quote(m.Source), strconv.Quote(m.Name)),
				args: []string{"plugin", "marketplace", "add", m.Source},
				adds: m.Name,
			})
		}
	}
	for _, p := range inv.Plugins {
		if !safePluginID.MatchString(p.ID) {
			notes = append(notes, fmt.Sprintf("plugin %s: skipped, not name@marketplace", strconv.Quote(p.ID)))
			continue
		}
		steps, notes = planLocalInstalls(p, local, steps, notes)
		if !p.userScope() {
			switch {
			case len(p.LocalProjects) > 0:
			case p.Scope == "project":
				notes = append(notes, fmt.Sprintf("plugin %s: not installed, project scope: the repository's .claude/settings.json carries it", p.ID))
			default:
				// A bundle from before LocalProjects names no project.
				notes = append(notes, fmt.Sprintf("plugin %s: not installed, installed for projects only (scope %s)", p.ID, p.Scope))
			}
			continue
		}
		have, installed := local.installed[p.ID]
		if !installed {
			s := pluginStep{
				what:   "install plugin " + p.ID,
				args:   []string{"plugin", "install", p.ID},
				market: p.ID[strings.LastIndex(p.ID, "@")+1:],
			}
			if p.Enabled != nil && !*p.Enabled {
				s.what += " (disabled, as in the bundle)"
				s.then = []string{"plugin", "disable", "--scope", "user", p.ID}
			}
			steps = append(steps, s)
			continue
		}
		same := true
		// No entry here means disabled, as `claude plugin list` reports it:
		// only the bundle enabling it differs.
		on, set := local.enabled[p.ID]
		if p.Enabled != nil && on != *p.Enabled {
			same = false
			verb := "enable"
			if !*p.Enabled {
				verb = "disable"
			}
			here := onOff(on)
			if !set {
				here = "not set"
			}
			steps = append(steps, pluginStep{
				what:     fmt.Sprintf("%s plugin %s (%s here, %s in bundle)", verb, p.ID, here, onOff(*p.Enabled)),
				args:     []string{"plugin", verb, "--scope", "user", p.ID},
				conflict: true,
			})
		}
		if p.Version != "" && have != "" && have != p.Version {
			same = false
			steps = append(steps, pluginStep{
				what: fmt.Sprintf("update plugin %s to the marketplace's latest (%s here, %s in bundle)",
					p.ID, strconv.Quote(have), strconv.Quote(p.Version)),
				args:     []string{"plugin", "update", p.ID},
				conflict: true,
			})
		}
		if same {
			notes = append(notes, fmt.Sprintf("plugin %s: already here", p.ID))
		}
	}
	return steps, notes
}

// planLocalInstalls adds a local-scope install for each of the plugin's
// projects this import placed, and a line for each it did not.
func planLocalInstalls(p InstalledPlugin, local localPlugins, steps []pluginStep, notes []string) ([]pluginStep, []string) {
	for _, dir := range p.unplaced {
		notes = append(notes, fmt.Sprintf("plugin %s for project %s: not installed, the project was not placed by this import",
			p.ID, strconv.Quote(dir)))
	}
	for _, dir := range p.placed {
		here := false
		for _, d := range local.localAt[p.ID] {
			here = here || samePath(d, dir)
		}
		if here {
			notes = append(notes, fmt.Sprintf("plugin %s for project %s: already here", p.ID, strconv.Quote(dir)))
			continue
		}
		s := pluginStep{
			what:   fmt.Sprintf("install plugin %s for project %s (local scope)", p.ID, strconv.Quote(dir)),
			args:   []string{"plugin", "install", "--scope", "local", p.ID},
			market: p.ID[strings.LastIndex(p.ID, "@")+1:],
			dir:    dir,
		}
		for _, off := range p.placedOff {
			if samePath(off, dir) {
				s.what = fmt.Sprintf("install plugin %s for project %s (local scope, disabled, as in the bundle)", p.ID, strconv.Quote(dir))
				s.then = []string{"plugin", "disable", "--scope", "local", p.ID}
			}
		}
		steps = append(steps, s)
	}
	return steps, notes
}

// placePluginProjects rewrites each local-scope project of inv for this
// machine. A project is placed only under a place this import confirmed:
// an accepted match, --map, a work dir mapping or --project-root. The home
// rewrite alone is not one, so a bundle cannot pick a directory to write
// settings.local.json into. It returns a copy; inv is unchanged.
func placePluginProjects(inv *PluginInventory, remaps []pathRemap, confirmed []string) *PluginInventory {
	if inv == nil {
		return nil
	}
	out := *inv
	out.Plugins = make([]InstalledPlugin, len(inv.Plugins))
	for i, p := range inv.Plugins {
		p.placed, p.unplaced, p.placedOff = nil, nil, nil
		for _, dir := range p.LocalProjects {
			to, _ := remapText(dir, remaps)
			to = filepath.Clean(to)
			if placedUnder(to, confirmed) {
				p.placed = append(p.placed, to)
				for _, off := range p.LocalDisabled {
					if off == dir {
						p.placedOff = append(p.placedOff, to)
					}
				}
			} else {
				p.unplaced = append(p.unplaced, dir)
			}
		}
		out.Plugins[i] = p
	}
	return &out
}

// placedUnder is whether dir is an existing directory under one of roots,
// with links resolved on both sides: a link inside a project must not
// carry the write out of it.
func placedUnder(dir string, roots []string) bool {
	if !filepath.IsAbs(dir) || printable(dir) != dir {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		if rr, err := filepath.EvalSymlinks(r); err == nil && pathWithin(rr, real) {
			return true
		}
	}
	return false
}

func onOff(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

// runClaude runs the claude CLI with args and no shell, in dir when it is
// set. in is nil, or the terminal when a person is answering, so claude can
// ask its own questions. Tests replace it.
var runClaude = func(ctx context.Context, dir string, in io.Reader, out io.Writer, args ...string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("claude is not on PATH")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c := exec.CommandContext(ctx, path, args...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = in, out, out
	return c.Run()
}

type pluginImportOptions struct {
	Policy pluginPolicy
	DryRun bool
	In     *bufio.Reader
	// Terminal is handed to claude under ask; nil otherwise.
	Terminal io.Reader
	Out      io.Writer // the report
	Err      io.Writer // prompts and the claude CLI's own output
}

// importPlugins settles the bundle's plugins with this machine and reports
// every decision. It returns the number of steps that failed.
func importPlugins(ctx context.Context, inv *PluginInventory, local localPlugins, o pluginImportOptions) (int, error) {
	steps, notes := planPluginImport(inv, local)
	if len(steps) == 0 && len(notes) == 0 {
		return 0, nil
	}
	fmt.Fprintln(o.Out, "\nPlugins:")
	for _, n := range notes {
		fmt.Fprintf(o.Out, "  %s\n", printable(n))
	}
	if o.Policy == pluginsPrint || o.DryRun {
		if len(steps) > 0 {
			fmt.Fprintln(o.Out, "  To apply the bundle's plugins here, run:")
		}
		for _, s := range steps {
			for _, args := range [][]string{s.args, s.then} {
				switch {
				case args == nil:
				case s.dir != "":
					fmt.Fprintf(o.Out, "    (cd %s && claude %s)\n", shellQuote(s.dir), strings.Join(args, " "))
				default:
					fmt.Fprintf(o.Out, "    claude %s\n", strings.Join(args, " "))
				}
			}
		}
		return 0, nil
	}
	ready := map[string]bool{}
	for name := range local.markets {
		ready[name] = true
	}
	failed := 0
	for _, s := range steps {
		if s.market != "" && !ready[s.market] {
			fmt.Fprintf(o.Out, "  not done: %s: marketplace %s is not on this machine\n", printable(s.what), strconv.Quote(s.market))
			continue
		}
		yes, err := o.decide(s)
		if err != nil {
			return failed, err
		}
		if !yes {
			fmt.Fprintf(o.Out, "  kept: %s not done\n", printable(s.what))
			continue
		}
		var term io.Reader
		if o.Policy == pluginsAsk {
			term = o.Terminal
		}
		if err := runClaude(ctx, s.dir, term, o.Err, s.args...); err != nil {
			failed++
			fmt.Fprintf(o.Out, "  failed: %s: %v\n", printable(s.what), err)
			continue
		}
		if s.then != nil {
			if err := runClaude(ctx, s.dir, term, o.Err, s.then...); err != nil {
				failed++
				fmt.Fprintf(o.Out, "  failed: %s: installed, but not disabled: %v\n", printable(s.what), err)
				continue
			}
		}
		if s.adds != "" {
			ready[s.adds] = true
		}
		fmt.Fprintf(o.Out, "  done: %s\n", printable(s.what))
	}
	return failed, nil
}

// decide is yes or no for one step under the policy.
func (o pluginImportOptions) decide(s pluginStep) (bool, error) {
	switch o.Policy {
	case pluginsBundle:
		return true, nil
	case pluginsMissing:
		return !s.conflict, nil
	}
	note := ""
	// Only disabling stops code; every other step can start or change it.
	if s.args[1] != "disable" {
		note = ", it can run code on this machine"
	}
	fmt.Fprintf(o.Err, "  %s%s? [y/N]: ", printable(s.what), note)
	line, err := o.In.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading plugin answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// mergePluginInventories joins the inventories of the imported profiles.
// The first profile that lists a marketplace or plugin wins.
func mergePluginInventories(invs []*PluginInventory) *PluginInventory {
	out := &PluginInventory{}
	markets, plugins := map[string]bool{}, map[string]int{}
	for _, inv := range invs {
		if inv == nil {
			continue
		}
		for _, m := range inv.Marketplaces {
			if !markets[m.Name] {
				markets[m.Name] = true
				out.Marketplaces = append(out.Marketplaces, m)
			}
		}
		for _, p := range inv.Plugins {
			if at, ok := plugins[p.ID]; ok {
				// Local installs from every profile count.
				q := &out.Plugins[at]
				q.placed = appendNewPaths(q.placed, p.placed)
				q.unplaced = appendNewPaths(q.unplaced, p.unplaced)
				q.placedOff = appendNewPaths(q.placedOff, p.placedOff)
				continue
			}
			plugins[p.ID] = len(out.Plugins)
			out.Plugins = append(out.Plugins, p)
		}
	}
	if len(out.Marketplaces) == 0 && len(out.Plugins) == 0 {
		return nil
	}
	return out
}

func appendNewPaths(have, add []string) []string {
	for _, a := range add {
		dup := false
		for _, h := range have {
			dup = dup || samePath(h, a)
		}
		if !dup {
			have = append(have, a)
		}
	}
	return have
}
