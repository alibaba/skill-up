package runtime

import (
	"path/filepath"
	"strings"
)

// IsolatedHome reports whether this runtime redirects personal agent state.
func (r *NoneRuntime) IsolatedHome() bool { return r.cfg.AgentHome == AgentHomeTemporary }

func (r *NoneRuntime) isolatedEnvironment(env []string) []string {
	home := r.runDir
	owned := map[string]string{
		"HOME":              home,
		"USERPROFILE":       home,
		"HOMEDRIVE":         filepath.VolumeName(home),
		"HOMEPATH":          strings.TrimPrefix(home, filepath.VolumeName(home)),
		"CODEX_HOME":        home,
		"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"),
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"XDG_DATA_HOME":     filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME":    filepath.Join(home, ".cache"),
		"XDG_STATE_HOME":    filepath.Join(home, ".local", "state"),
		"APPDATA":           filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":      filepath.Join(home, "AppData", "Local"),
		// Non-interactive shells must not source a personal startup file.
		"BASH_ENV": "",
		"ENV":      "",
	}
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, reserved := owned[strings.ToUpper(key)]; !reserved {
			result = append(result, entry)
		}
	}
	for key, value := range owned {
		result = append(result, key+"="+value)
	}
	return result
}
