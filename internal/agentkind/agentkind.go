// Package agentkind holds the canonical set of built-in agent engine names.
//
// It is a tiny leaf package with no internal dependencies so both
// internal/agent (factory dispatch) and internal/config (validation) can
// import the same source of truth without creating an import cycle —
// previously the list was duplicated in both packages with a "keep in sync"
// comment.
package agentkind

// Built-in engine name constants. Aliases (with both - and _) are listed
// because the YAML config and CLI flag historically accept either.
const (
	ClaudeCode      = "claude_code"
	ClaudeCodeAlias = "claude-code"
	Codex           = "codex"
	QoderCLI        = "qodercli"
	QoderAlias      = "qoder"
	QoderCLIAlias   = "qoder-cli"
)

var builtinNames = map[string]struct{}{
	ClaudeCode:      {},
	ClaudeCodeAlias: {},
	Codex:           {},
	QoderCLI:        {},
	QoderAlias:      {},
	QoderCLIAlias:   {},
}

// IsBuiltin reports whether name matches a built-in agent engine.
// A built-in engine ignores any engine.custom block.
func IsBuiltin(name string) bool {
	_, ok := builtinNames[name]
	return ok
}
