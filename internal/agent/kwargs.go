package agent

import (
	"strconv"
	"strings"
)

// Recognised engine kwarg keys. Each agent reads only the keys it understands.
const (
	// KwargBypassSandbox asks the agent to skip its own process sandbox
	// (e.g. codex's Landlock wrapper). Useful when the host runtime lacks
	// the kernel features the agent's sandbox depends on.
	KwargBypassSandbox = "bypass_sandbox"
)

// EngineKwargBool reads a boolean engine kwarg. Returns false when the key
// is absent or the value cannot be parsed as a bool (per strconv.ParseBool:
// "1", "t", "T", "true", "TRUE", "True" => true; "0", "f", "false" => false).
func EngineKwargBool(kw map[string]string, key string) bool {
	v, ok := kw[key]
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false
	}
	return b
}
