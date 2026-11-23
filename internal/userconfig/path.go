package userconfig

import (
	"os"
	"path/filepath"
)

// DefaultConfigPath returns the default user config location. Relative XDG
// paths are ignored so discovery and init never resolve them against the repo.
func DefaultConfigPath(home string, getenv func(string) string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, UserConfigDir, UserConfigFile)
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", UserConfigDir, UserConfigFile)
}
