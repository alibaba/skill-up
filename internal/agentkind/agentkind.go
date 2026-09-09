// Package agentkind holds the canonical set of built-in agent engine names.
//
// It is a tiny leaf package with no internal dependencies so both
// internal/agent (factory dispatch) and internal/config (validation) can
// import the same source of truth without creating an import cycle —
// previously the list was duplicated in both packages with a "keep in sync"
// comment.
package agentkind

import (
	"regexp"
	"strings"
)

// Built-in engine name constants. Aliases (with both - and _) are listed
// because the YAML config and CLI flag historically accept either.
const (
	ClaudeCode      = "claude_code"
	ClaudeCodeAlias = "claude-code"
	Codex           = "codex"
	QoderCLI        = "qodercli"
	QoderAlias      = "qoder"
	QoderCLIAlias   = "qoder-cli"
	QwenCode        = "qwen_code"
	QwenCodeAlias   = "qwen-code"
	QwenAlias       = "qwen"
)

var builtinNames = map[string]struct{}{
	ClaudeCode:      {},
	ClaudeCodeAlias: {},
	Codex:           {},
	QoderCLI:        {},
	QoderAlias:      {},
	QoderCLIAlias:   {},
	QwenCode:        {},
	QwenCodeAlias:   {},
	QwenAlias:       {},
}

const semverIdentifier = `(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)`

var exactVersionRegexp = regexp.MustCompile(
	`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)` +
		`(?:-` + semverIdentifier + `(?:\.` + semverIdentifier + `)*)?` +
		`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

// IsBuiltin reports whether name matches a built-in agent engine.
// A built-in engine ignores any engine.custom block.
func IsBuiltin(name string) bool {
	_, ok := builtinNames[name]
	return ok
}

// SupportsVersion reports whether the built-in adapter can select and enforce
// an engine version.
func SupportsVersion(name string) bool {
	switch name {
	case ClaudeCode, ClaudeCodeAlias, Codex, QwenCode, QwenCodeAlias, QwenAlias:
		return true
	default:
		return false
	}
}

// IsExactVersion reports whether value is one complete semantic-version token.
func IsExactVersion(value string) bool {
	return exactVersionRegexp.MatchString(strings.TrimSpace(value))
}
