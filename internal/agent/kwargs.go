package agent

import (
	"sort"
	"strconv"
	"strings"

	"github.com/alibaba/skill-up/internal/logging"
)

// Recognised engine kwarg keys. Each agent reads only the keys it understands.
const (
	// KwargBypassSandbox asks the agent to skip its own process sandbox
	// (e.g. codex's Landlock wrapper). Useful when the host runtime lacks
	// the kernel features the agent's sandbox depends on.
	KwargBypassSandbox = "bypass_sandbox"
	// KwargMaxJSONLRecordBytes caps one physical JSONL record emitted by an
	// agent. Codex uses it for both its stdout event stream and session file.
	KwargMaxJSONLRecordBytes = "max_jsonl_record_bytes"
	// KwargMaxJSONLOutputBytes caps a complete JSONL artifact before skill-up
	// downloads it from the runtime.
	KwargMaxJSONLOutputBytes = "max_jsonl_output_bytes"
	// KwargEdition selects a branded edition of an otherwise shared CLI.
	// Qoder accepts "global" (default) and "cn".
	KwargEdition = "edition"
)

// knownEngineKwargs is the project-wide set of recognised engine kwarg keys.
// At least one agent in the project must honour or knowingly no-op on each
// key listed here. Keys not in this set are likely typos.
var knownEngineKwargs = map[string]struct{}{
	KwargBypassSandbox:       {},
	KwargMaxJSONLRecordBytes: {},
	KwargMaxJSONLOutputBytes: {},
	KwargEdition:             {},
}

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

// logUnknownEngineKwargs emits a DEBUG line for each key in kwargs that is
// not in knownEngineKwargs. Helps catch typos like "bypas_sandbox" (missing s)
// or "bypass-sandbox" (wrong separator). Per-agent semantics (which agent
// actually honours a key) are documented separately; this only flags keys
// no agent in the project recognises.
func logUnknownEngineKwargs(engineName string, kwargs map[string]string) {
	if len(kwargs) == 0 {
		return
	}
	unknown := make([]string, 0)
	for key := range kwargs {
		if _, ok := knownEngineKwargs[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		logging.Debugf("engine kwarg %q is not a recognised key for agent %q; check spelling or see docs", key, engineName)
	}
}
