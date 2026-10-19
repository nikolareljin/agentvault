package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// PluginInventory lists the Claude Code plugins and marketplaces of the
// exporting machine. The plugin files themselves are not carried: Claude
// Code's installed_plugins.json holds absolute cache paths that do not exist
// on the other machine. The inventory says what to install, and import prints
// the commands.
type PluginInventory struct {
	Marketplaces []PluginMarketplace `json:"marketplaces,omitempty"`
	Plugins      []InstalledPlugin   `json:"plugins,omitempty"`
}

// PluginMarketplace is one marketplace and where it comes from.
type PluginMarketplace struct {
	Name string `json:"name"`
	// Source is what `claude plugin marketplace add` takes: owner/repo for
	// GitHub, a URL for git, a path for a local directory.
	Source string `json:"source"`
	Kind   string `json:"kind"`
}

// InstalledPlugin is one installed plugin, as name@marketplace.
type InstalledPlugin struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

func collectClaudePluginInventory(homeDir string) (*PluginInventory, error) {
	dir := filepath.Join(homeDir, ".claude", "plugins")
	inv := &PluginInventory{}

	var markets map[string]struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
			Path   string `json:"path"`
		} `json:"source"`
	}
	if err := readJSONIfPresent(filepath.Join(dir, "known_marketplaces.json"), &markets); err != nil {
		return nil, err
	}
	for name, m := range markets {
		src := m.Source.Repo
		if src == "" {
			src = m.Source.URL
		}
		if src == "" {
			src = m.Source.Path
		}
		inv.Marketplaces = append(inv.Marketplaces, PluginMarketplace{Name: name, Source: src, Kind: m.Source.Source})
	}
	sort.Slice(inv.Marketplaces, func(i, j int) bool { return inv.Marketplaces[i].Name < inv.Marketplaces[j].Name })

	var installed struct {
		Plugins map[string][]struct {
			Scope   string `json:"scope"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := readJSONIfPresent(filepath.Join(dir, "installed_plugins.json"), &installed); err != nil {
		return nil, err
	}
	for id, entries := range installed.Plugins {
		p := InstalledPlugin{ID: id}
		if len(entries) > 0 {
			p.Version, p.Scope = entries[0].Version, entries[0].Scope
		}
		inv.Plugins = append(inv.Plugins, p)
	}
	sort.Slice(inv.Plugins, func(i, j int) bool { return inv.Plugins[i].ID < inv.Plugins[j].ID })

	if len(inv.Marketplaces) == 0 && len(inv.Plugins) == 0 {
		return nil, nil
	}
	return inv, nil
}

// readJSONIfPresent decodes path into v. A missing file leaves v empty; a file
// that does not parse is an error, so a damaged inventory is not exported as
// an empty one.
func readJSONIfPresent(path string, v any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// printPluginInstallCommands prints what to run on this machine to get the
// exporting machine's plugins. It runs nothing: installing a plugin runs its
// code, and that is a decision for the person importing.
func printPluginInstallCommands(out io.Writer, inv *PluginInventory) {
	if inv == nil {
		return
	}
	fmt.Fprintf(out, "  Plugins in bundle: %d marketplace(s), %d plugin(s). To install them:\n",
		len(inv.Marketplaces), len(inv.Plugins))
	for _, m := range inv.Marketplaces {
		if m.Kind == "directory" {
			fmt.Fprintf(out, "    # %s is a local directory on the exporting machine: %s\n", m.Name, m.Source)
			continue
		}
		if m.Source != "" {
			fmt.Fprintf(out, "    claude plugin marketplace add %s\n", m.Source)
		}
	}
	for _, p := range inv.Plugins {
		fmt.Fprintf(out, "    claude plugin install %s\n", p.ID)
	}
}
