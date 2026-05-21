package credential

import (
	"os"
	"strings"

	"github.com/alibaba/skill-up/internal/logging"
)

// HasProvider reports whether r has any configuration path for the named
// provider — a resolver entry, or one of `<PROVIDER>_API_KEY`,
// `<PROVIDER>_BASE_URL`, `<PROVIDER>_PERSONAL_ACCESS_TOKEN` in the process
// env. The PAT env is the canonical Qoder credential (see
// EnvQoderPersonalAccessToken / QoderCLIAgent.CheckCredentials), so probing
// it makes qoder-only-via-PAT setups count as configured.
//
// Framework default providers ("anthropic", "openai") are reported as
// configured unconditionally — their upstream APIs only accept bare model
// identifiers (no `provider/name` form), so a slashed `--model X/Y` with
// X ∈ {anthropic, openai} can only mean "use bare Y on the X namespace",
// never "the literal `X/Y` is the model id". The underlying agent CLIs
// (claude, codex) also carry their own persistent login state that the
// resolver/env probe can't see; treating them as configured avoids
// collapsing splits when users rely on that login state alone.
//
// Safe on a nil receiver: only env probing applies, which is useful for
// callers that need a provider check before any resolver has been loaded.
//
// Used to disambiguate the two valid interpretations of a slashed
// `--model provider/name` input — see ResolveModelRef.
func (r *Resolver) HasProvider(name string) bool {
	if name == "" {
		return false
	}
	switch strings.ToLower(name) {
	case "anthropic", "openai":
		return true
	}
	if r != nil {
		if _, ok := r.Get(name); ok {
			return true
		}
	}
	if _, _, ok := lookupProviderEnv(name, valueAPIKey); ok {
		return true
	}
	if _, _, ok := lookupProviderEnv(name, valueBaseURL); ok {
		return true
	}
	if v := os.Getenv(strings.ToUpper(name) + "_PERSONAL_ACCESS_TOKEN"); v != "" {
		return true
	}
	return false
}

// ResolveModelRef interprets a model identifier that may carry a
// `provider/` prefix and returns the canonical (provider, name) pair the
// CLI should store on `evalCfg.Engine.Model`.
//
// The split happens iff the prefix is a configured provider (per
// HasProvider). Otherwise the slashed string is treated as an opaque model
// identifier the upstream API expects verbatim — typical for internal
// anthropic-proxy gateways that register models under
// `anthropic_modelscope/deepseek-v4-pro` keys.
//
// Two interpretations this disambiguates:
//
//   - Configured provider → credential namespace usage
//     (`provider: dashscope, name: claude-sonnet-4-6` uses dashscope's
//     credentials but talks to a bare Anthropic model id).
//   - Unconfigured provider → literal model identifier
//     (`anthropic_modelscope/deepseek-v4-pro` registered as-is on an
//     internal proxy).
//
// Emits a debug log on the collapse path so operators can see the
// disambiguation choice in trace output. The split path stays silent —
// that's the common, expected case.
//
// raw == "" returns ("", "").
//
// Engine-awareness: not currently required — every supported engine
// agrees that "stored config exists" is the right disambiguation signal.
// If a future engine needs a different policy (e.g. codex's
// runProviderConfig wanting a wider notion of configured), extend the
// signature with an engine parameter; until then keeping it engine-free
// avoids leaking agent concerns into credential.
func ResolveModelRef(raw string, resolver *Resolver) (provider, name string) {
	if raw == "" {
		return "", ""
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", raw
	}
	if resolver.HasProvider(parts[0]) {
		return parts[0], parts[1]
	}
	upper := strings.ToUpper(parts[0])
	logging.Debugf(
		"ResolveModelRef: treating %q as opaque model id; provider %q has no resolver entry / %s_API_KEY / %s_BASE_URL / %s_PERSONAL_ACCESS_TOKEN env",
		raw, parts[0], upper, upper, upper,
	)
	return "", raw
}
