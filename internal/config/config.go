package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	AppName    = "agentvault"
	VaultFile  = "vault.enc"
	ConfigFile = "config.json"
)

// Dir returns the AgentVault config directory, respecting XDG_CONFIG_HOME.
func Dir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", AppName)
}

// VaultPath returns the full path to the encrypted vault file.
func VaultPath() string {
	return filepath.Join(Dir(), VaultFile)
}

// ConfigPath returns the full path to the config file.
func ConfigPath() string {
	return filepath.Join(Dir(), ConfigFile)
}

// TemplatesDir returns the template storage directory.
func TemplatesDir() string {
	return filepath.Join(Dir(), "templates")
}

// ClaudeDesktopConfigPath returns where Claude Desktop reads its MCP config on
// this OS. It fails rather than return a relative path, which would put the
// file in the working directory when HOME (or USERPROFILE and APPDATA) is unset.
func ClaudeDesktopConfigPath() (string, error) {
	home, _ := os.UserHomeDir()
	p := claudeDesktopConfigPathFor(runtime.GOOS, home, os.Getenv("APPDATA"))
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("cannot locate Claude Desktop config (got %q): set HOME (USERPROFILE or APPDATA on Windows) or pass an output file", p)
	}
	return p, nil
}

// claudeDesktopConfigPathFor is ClaudeDesktopConfigPath with its inputs
// passed in, so every OS can be tested on one. Linux has no official Claude
// Desktop; the path there is the one this tool has always written.
func claudeDesktopConfigPathFor(goos, home, appData string) string {
	const file = "claude_desktop_config.json"
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", file)
	case "windows":
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Claude", file)
	default:
		return filepath.Join(home, ".config", "claude", file)
	}
}
