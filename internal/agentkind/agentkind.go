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

// Wire protocols spoken by built-in engines and model-provider namespaces.
// They decide whether an engine can drive a given judge model: an
// Anthropic-protocol model needs an Anthropic-protocol engine and vice
// versa.
const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
)

// engineProtocols maps each built-in engine (including aliases) to the wire
// protocol it speaks. claude_code and qodercli are Anthropic-protocol agents
// (qodercli is Claude-compatible); codex speaks the OpenAI protocol.
var engineProtocols = map[string]string{
	ClaudeCode:      ProtocolAnthropic,
	ClaudeCodeAlias: ProtocolAnthropic,
	QoderCLI:        ProtocolAnthropic,
	QoderAlias:      ProtocolAnthropic,
	QoderCLIAlias:   ProtocolAnthropic,
	Codex:           ProtocolOpenAI,
}

// providerProtocols maps a model-provider namespace (the prefix in
// `provider/model`) to its wire protocol.
var providerProtocols = map[string]string{
	ProtocolAnthropic: ProtocolAnthropic,
	ProtocolOpenAI:    ProtocolOpenAI,
}

// canonicalEngineForProtocol is the default built-in engine for each
// protocol — used to pick a judge engine when the case engine cannot speak
// the judge model's protocol.
var canonicalEngineForProtocol = map[string]string{
	ProtocolAnthropic: ClaudeCode,
	ProtocolOpenAI:    Codex,
}

// ProtocolForEngine returns the wire protocol spoken by a built-in engine.
// The second return value is false for unknown (e.g. custom) engines.
func ProtocolForEngine(engine string) (string, bool) {
	p, ok := engineProtocols[engine]
	return p, ok
}

// ProtocolForProvider returns the wire protocol implied by a model-provider
// namespace. The second return value is false for unknown providers.
func ProtocolForProvider(provider string) (string, bool) {
	p, ok := providerProtocols[provider]
	return p, ok
}

// EngineForProvider returns the canonical built-in engine that speaks the
// protocol implied by the given model provider (anthropic/* → claude_code,
// openai/* → codex). The second return value is false for unknown providers.
func EngineForProvider(provider string) (string, bool) {
	protocol, ok := providerProtocols[provider]
	if !ok {
		return "", false
	}
	engine, ok := canonicalEngineForProtocol[protocol]
	return engine, ok
}
