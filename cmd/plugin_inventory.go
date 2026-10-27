package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nikolareljin/agentvault/internal/safefile"
)

// PluginInventory lists the Claude Code plugins and marketplaces of the
// exporting machine. The plugin files themselves are not carried: Claude
// Code's installed_plugins.json holds absolute cache paths that do not exist
// on the other machine. The inventory says what to install, and import
// installs it or prints the commands (see plugin_import.go).
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
	// Scope is "user" when the plugin is installed for the user, else the
	// scope of its first install.
	Scope string `json:"scope,omitempty"`
	// Enabled is its enabledPlugins entry in the user's Claude settings;
	// nil when there is none.
	Enabled *bool `json:"enabled,omitempty"`
	// installs are where it is installed on this machine; they decide what
	// a work dir selection leaves out and never reach the bundle.
	installs []pluginInstall
}

// userScope is whether the plugin is installed for the user. A bundle from
// before Scope was set says nothing, and counts as user.
func (p InstalledPlugin) userScope() bool {
	return p.Scope == "" || p.Scope == "user"
}

type pluginInstall struct {
	scope, projectPath string
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
		src := withoutURLCredentials(m.Source.Repo)
		if src == "" {
			src = withoutURLCredentials(m.Source.URL)
		}
		if src == "" {
			src = withoutURLCredentials(m.Source.Path)
		}
		inv.Marketplaces = append(inv.Marketplaces, PluginMarketplace{Name: name, Source: src, Kind: m.Source.Source})
	}
	sort.Slice(inv.Marketplaces, func(i, j int) bool { return inv.Marketplaces[i].Name < inv.Marketplaces[j].Name })

	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			Version     string `json:"version"`
			ProjectPath string `json:"projectPath"`
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
		for _, e := range entries {
			path := e.ProjectPath
			if e.Scope == "user" {
				path = ""
				p.Version, p.Scope = e.Version, e.Scope
			}
			p.installs = append(p.installs, pluginInstall{scope: e.Scope, projectPath: path})
		}
		inv.Plugins = append(inv.Plugins, p)
	}
	sort.Slice(inv.Plugins, func(i, j int) bool { return inv.Plugins[i].ID < inv.Plugins[j].ID })
	if len(inv.Plugins) > 0 {
		enabled, err := readEnabledPlugins(homeDir)
		if err != nil {
			return nil, err
		}
		for i := range inv.Plugins {
			if on, ok := enabled[inv.Plugins[i].ID]; ok {
				inv.Plugins[i].Enabled = &on
			}
		}
	}

	if len(inv.Marketplaces) == 0 && len(inv.Plugins) == 0 {
		return nil, nil
	}
	return inv, nil
}

// withoutURLCredentials returns a marketplace source with no credentials in
// it, or "" when it cannot tell. A private marketplace added as
// https://token@host/repo would otherwise carry the token into the bundle.
// It fails closed: a URL must be http, https, ssh, git, git+ssh or ssh+git
// with a host, loses its query and fragment, and loses its user when
// userinfoIsCredential says it is one, so ssh://git@host/o/r keeps its
// login. An scp form user@host:path is ssh: it stays as it is when the user
// is a plain login name not starting with "-", and is dropped otherwise.
// owner/repo and local paths pass as they are.
func withoutURLCredentials(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return ""
		}
		userinfo, rest, _, _ := splitURL(raw)
		if u.User != nil && userinfoIsCredential(u.Scheme, userinfo, rest) {
			u.User = nil
		}
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		out := u.String()
		if u.User == nil && strings.Contains(out, "@") {
			return ""
		}
		return out
	}
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return raw
	}
	user, rest := raw[:at], raw[at+1:]
	if !strings.Contains(rest, ":") || strings.HasPrefix(user, "-") || userinfoIsCredential("ssh", user, "@"+rest) {
		return ""
	}
	return raw
}

// readJSONIfPresent decodes path into v. A missing file leaves v empty; a file
// that does not parse is an error, so a damaged inventory is not exported as
// an empty one.
func readJSONIfPresent(path string, v any) error {
	data, err := safefile.Read(path)
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

// The values come from a bundle, and import runs them as arguments or prints
// them for a person to paste into a shell. Only shapes that cannot carry
// shell syntax or start with "-" are used; anything else is reported quoted.
var (
	safeMarketplaceName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	safePluginID          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
	safeMarketplaceSource = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*|https://[A-Za-z0-9][A-Za-z0-9.-]*(/[A-Za-z0-9._~/-]*)?)$`)
)
