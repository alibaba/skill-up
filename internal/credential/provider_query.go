package credential

import (
	"os"
	"slices"
	"strings"

	"github.com/alibaba/skill-up/internal/logging"
)

// frameworkDefaultProviders are the engine-native providers whose upstream
// APIs accept only bare model identifiers (no `provider/name` form). A
// slashed `--model X/Y` with X in this set can only mean "use bare Y on
// the X namespace" — never "the literal `X/Y` is the model id". The
// underlying agent CLIs (claude, codex) also carry persistent login state
// that the resolver/env probe can't see, so we treat these as configured
// even when no env or resolver entry is present.
//
// Kept as a package-private slice (rather than inlined in HasProvider) so
// that adding a new engine-native provider is a one-line change and the
// "why this set is hardcoded" rationale lives next to the data.
var frameworkDefaultProviders = []string{"anthropic", "openai"}

func isFrameworkDefaultProvider(name string) bool {
	return slices.Contains(frameworkDefaultProviders, strings.ToLower(name))
}

// HasProvider reports whether r should treat `name` as a configured
// provider for the purposes of `provider/name` model-ref disambiguation
// (see ResolveModelRef). A provider is configured when any of the
// following holds:
//
//   - it appears in frameworkDefaultProviders (engine-native provider
//     with persistent login state the resolver can't see);
//   - r has a resolver entry for it;
//   - `<PROVIDER>_API_KEY` or `<PROVIDER>_BASE_URL` is set in the process
//     env — the generic env-credential convention used across providers;
//   - `name == "qoder"` and EnvQoderPersonalAccessToken is set. The PAT
//     env is Qoder-specific (see QoderCLIAgent.CheckCredentials); there
//     is no general `<PROVIDER>_PERSONAL_ACCESS_TOKEN` convention, so
//     this branch is hard-coded rather than templated on `name`.
//
// Safe on a nil receiver: only the framework-default and env probes
// apply, which is useful for callers that need a provider check before
// any resolver has been loaded.
//
// Caveat — dual semantics: `true` means "treat the prefix as a namespace
// for split", not "credentials definitely exist for this provider".
// Framework defaults short-circuit this without any credential check; a
// later auth step may still fail. Callers needing strict credential
// presence should use Resolver.Get + lookupProviderEnv directly.
func (r *Resolver) HasProvider(name string) bool {
	if name == "" {
		return false
	}
	if isFrameworkDefaultProvider(name) {
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
	if strings.EqualFold(name, "qoder") {
		for _, envName := range []string{
			EnvQoderPersonalAccessToken,
			EnvQoderCNPersonalAccessToken,
			EnvQoderCNAccessToken,
		} {
			if os.Getenv(envName) != "" {
				return true
			}
		}
	}
	return false
}

// ResolveModelRef interprets a model identifier that may carry a
// `provider/` prefix and returns the canonical (provider, name) pair the
// CLI should store on `evalCfg.Engine.Model`.
//
// The split happens iff the prefix is a configured provider per
// Resolver.HasProvider. Otherwise the slashed string is treated as an
// opaque model identifier the upstream API expects verbatim — typical
// for internal anthropic-proxy gateways that register models under keys
// like `anthropic_modelscope/deepseek-v4-pro`.
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
// CLI-time signals (`--api-key`, `engine.model.base_url`) deliberately
// do NOT influence this decision. They were once wired in as "implicit
// provider configured" hints, but that forced a split on flows like
// `--api-key K --model anthropic_modelscope/deepseek-v4-pro` and broke
// literal-id pass-through. Users who really mean namespace semantics
// should configure the provider via env / credentials.yaml; the
// HasProvider probe then preserves the split.
//
// Emits a debug log on the collapse path so operators can see the
// disambiguation choice in trace output. The split path stays silent —
// that's the common, expected case.
//
// raw == "" returns ("", "").
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
		"ResolveModelRef: treating %q as opaque model id; provider %q has no resolver entry / %s_API_KEY / %s_BASE_URL env (and is not a framework default or qoder PAT setup)",
		raw, parts[0], upper, upper,
	)
	return "", raw
}
