package config

import (
	"os"
	"path/filepath"
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
