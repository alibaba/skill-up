package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/alibaba/skill-up/internal/customengine"
)

// sensitiveEnvNamePattern matches environment variable names that look like
// credentials. Such values must not be rendered into a command line (where
// runtimes record them on exec spans and in failure logs); they belong in
// engine.custom.env. Word boundaries avoid false positives like MONKEY_PATH.
var sensitiveEnvNamePattern = regexp.MustCompile(
	`(?i)(^|_)(API_?KEY|ACCESS_?KEY|KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTHORIZATION)(_|$)`,
)

func isSensitiveEnvName(name string) bool {
	return sensitiveEnvNamePattern.MatchString(name)
}

// secretLiteralPatterns matches common credential shapes (vendor key prefixes,
// long opaque hex/base64-ish tokens, JWTs). Used to reject ${X:-<literal>}
// defaults that bake a secret straight into the command line even when the
// outer variable name itself does not look secret-like.
var secretLiteralPatterns = []*regexp.Regexp{
	// Vendor prefixes (Anthropic sk-ant-, OpenAI sk-, GitHub ghp_/gho_/ghu_/
	// ghs_/ghr_, Google AIza, Slack xox[abp]-, AWS AKIA/ASIA) — case-sensitive
	// on purpose: lowercase prefixes match real keys, uppercase ones AWS IDs.
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])sk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])AIza[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])xox[abposr]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:AKIA|ASIA)[A-Z0-9]{16}`),
	// JWT: three base64url segments separated by dots.
	regexp.MustCompile(`(?:^|[^A-Za-z0-9_])ey[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
}

// rejectIfSecretLiteral returns a typed error when strict-mode resolution
// would otherwise splice a credential-shaped literal into a command line.
// fromDefault distinguishes the ${X:-...} default branch from the live
// environment-variable branch so the message can name the actual source.
//
//revive:disable-next-line:flag-parameter
func rejectIfSecretLiteral(name, value string, rejectSecrets, fromDefault bool) error {
	if !rejectSecrets || !looksLikeSecret(value) {
		return nil
	}
	if fromDefault {
		return fmt.Errorf(
			"${%s:-...} default value looks like a credential; pass secrets via engine.custom.env, not as inline command-line defaults",
			name,
		)
	}
	return fmt.Errorf(
		"environment variable %q resolves to a value that looks like a credential; reference it from engine.custom.env instead of inline in a command line",
		name,
	)
}

// looksLikeSecret applies the secretLiteralPatterns above to a default-value
// literal. It is intentionally conservative — only well-known credential
// shapes trigger — so legitimate default values (paths, model names, URLs)
// pass through. The check exists to plug the gap where ${INNOCUOUS_NAME:-
// sk-real-secret} would otherwise smuggle credentials into the command line.
func looksLikeSecret(s string) bool {
	if s == "" {
		return false
	}
	for _, re := range secretLiteralPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// isSensitiveTemplateVar reports whether a built-in template variable may carry
// a credential and so must not be rendered into a command line: ${api_key}; a
// ${kwargs.<key>} whose key name looks secret-like; or an aggregate variable
// (${kwargs}, ${kwargs_json}, ${session_input}, ${session_input_json}) that
// embeds the whole kwargs map and could therefore contain secret-like keys.
func isSensitiveTemplateVar(name string) bool {
	switch name {
	case "api_key", "kwargs", "kwargs_json", "session_input", "session_input_json":
		return true
	}
	if key, ok := strings.CutPrefix(name, "kwargs."); ok {
		// kwarg keys may use hyphens or camelCase (e.g. "api-key", "apiKey",
		// "bearerToken"); normalize before the underscore-bounded check.
		return isSensitiveEnvName(normalizeKeyForSensitiveCheck(key))
	}
	return false
}

// normalizeKeyForSensitiveCheck converts a kwarg key into UPPER_SNAKE_CASE so
// the sensitive-name pattern recognizes alternative naming conventions —
// hyphenated ("api-key"), dotted ("api.key"), camelCase ("apiKey",
// "bearerToken"), and other non-alphanumeric separators.
func normalizeKeyForSensitiveCheck(key string) string {
	var sep strings.Builder
	sep.Grow(len(key))
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sep.WriteRune(r)
		} else {
			sep.WriteByte('_')
		}
	}
	normalized := sep.String()
	var b strings.Builder
	b.Grow(len(normalized) + 4)
	prevLower := false
	for _, r := range normalized {
		if prevLower && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(r)
		prevLower = unicode.IsLower(r)
	}
	return strings.ToUpper(b.String())
}

// builtinTemplateVars is the set of run-time template variable names provided
// by skill-up. References to these are left intact during config-time env
// resolution and resolved later when a custom engine runs a case.
var builtinTemplateVars = map[string]struct{}{
	"workspace":          {},
	"prompt":             {},
	"messages":           {},
	"messages_json":      {},
	"session_input":      {},
	"session_input_json": {},
	"input_file":         {},
	"output_file":        {},
	"model":              {},
	"model_provider":     {},
	"model_name":         {},
	"api_key":            {},
	"case_id":            {},
	"variant":            {},
	"max_turns":          {},
	"timeout_seconds":    {},
	"kwargs":             {},
	"kwargs_json":        {},
}

// IsBuiltinTemplateVar reports whether name is a skill-up-provided template
// variable (including any kwargs.<key> reference). Such names are resolved at
// run time, not at config-load time.
func IsBuiltinTemplateVar(name string) bool {
	if strings.HasPrefix(name, "kwargs.") {
		return true
	}
	_, ok := builtinTemplateVars[name]
	return ok
}

// ResolveCustomEngineConfig runs environment-variable resolution and engine
// validation for the current engine.Name. The loader intentionally defers
// this: the final engine name is only known after CLI overrides (--engine),
// so callers invoke this once that name is settled. It is a no-op for built-in
// engines, which ignore any engine.custom block.
func ResolveCustomEngineConfig(cfg *EvalConfig) error {
	return ResolveCustomEngineConfigWithOptions(cfg, ResolveCustomEngineOptions{})
}

// ResolveCustomEngineOptions controls which raw engine fields are relevant to
// the current invocation.
type ResolveCustomEngineOptions struct {
	// SkipModelIdentity leaves engine.model.provider/name untouched when an
	// explicit CLI model has already superseded those YAML fields. BaseURL and
	// Params remain active and are still resolved.
	SkipModelIdentity bool
}

// ResolveCustomEngineConfigWithOptions resolves and validates the active
// custom-engine configuration while allowing superseded YAML model fields to
// be excluded from environment expansion.
func ResolveCustomEngineConfigWithOptions(cfg *EvalConfig, opts ResolveCustomEngineOptions) error {
	if err := resolveCustomEngineEnv(cfg, opts); err != nil {
		return err
	}
	if errs := validateEngine(cfg.Engine); len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// resolveCustomEngineEnv resolves ${VAR} environment-variable references inside
// the custom engine config tree. The engine.custom subtree is only walked
// when a custom engine is actually active; engine.model is resolved
// unconditionally so that --engine overrides from a custom engine back to a
// built-in still produce a fully-resolved provider/name/base_url (credential
// resolution keys off the resolved provider, and an unresolved
// `${MODEL_PROVIDER:-...}` literal would silently break auth). Built-in
// template variables are left intact for run-time resolution.
func resolveCustomEngineEnv(cfg *EvalConfig, opts ResolveCustomEngineOptions) error {
	// engine.model resolution must run regardless of whether engine.custom
	// is active, so a config that used both a custom block and a templated
	// model still has its model fields resolved after a --engine override
	// drops the custom block from the runtime path.
	modelErrs := resolveModelEnv(&cfg.Engine.Model, opts)

	custom := cfg.Engine.Custom
	if custom == nil || IsBuiltinEngineName(cfg.Engine.Name) {
		return aggregateConfigErrors(modelErrs)
	}

	errs := modelErrs
	errs = append(errs, resolveScalarEnv("transport", &custom.Transport)...)
	errs = append(errs, resolveScalarEnv("response_format", &custom.ResponseFormat)...)
	errs = append(errs, resolveStringMapEnv("env", custom.Env)...)
	// kwargs values can be expanded into a command line via ${kwargs.<key>},
	// and per the design kwargs are not for sensitive values; resolve strictly.
	errs = append(errs, resolveStringMapEnvStrict("kwargs", custom.Kwargs)...)
	// Only the active transport block is resolved, so stale ${VAR} refs in an
	// inactive block (e.g. a leftover custom.http while transport: local) do
	// not fail an otherwise runnable config.
	switch custom.Transport {
	case customTransportLocal:
		if custom.Local != nil {
			errs = append(errs, resolveLocalEnv(custom.Local)...)
		}
	case customTransportHTTP:
		if custom.HTTP != nil {
			errs = append(errs, resolveHTTPEnv(custom.HTTP)...)
		}
	}

	return aggregateConfigErrors(errs)
}

// resolveScalarEnv resolves a single string field, leaving secret-like
// references untouched (env / non-command-line use).
func resolveScalarEnv(field string, target *string) []string {
	return resolveScalarEnvWith(field, target, resolveEnvRefs)
}

// resolveScalarEnvStrict resolves a single string field, rejecting secret-like
// references (used for fields that become a command line, e.g. local.command).
func resolveScalarEnvStrict(field string, target *string) []string {
	return resolveScalarEnvWith(field, target, resolveEnvRefsStrict)
}

func resolveScalarEnvWith(field string, target *string, resolveFn func(string) (string, error)) []string {
	v, err := resolveFn(*target)
	if err != nil {
		return []string{fmt.Sprintf("engine.custom.%s: %s", field, err)}
	}
	*target = v
	return nil
}

// resolveStringMapEnv resolves every value of a string map in place, leaving
// secret-like references untouched (used for env and http.headers, which
// legitimately hold credentials).
func resolveStringMapEnv(field string, m map[string]string) []string {
	return resolveStringMapEnvWith(field, m, resolveEnvRefs)
}

// resolveStringMapEnvStrict resolves a string map's values strictly, rejecting
// secret-like references (used for kwargs, whose entries can be expanded into
// command lines via ${kwargs.<key>}; per the design, kwargs are not for
// sensitive values).
func resolveStringMapEnvStrict(field string, m map[string]string) []string {
	return resolveStringMapEnvWith(field, m, resolveEnvRefsStrict)
}

func resolveStringMapEnvWith(field string, m map[string]string, resolveFn func(string) (string, error)) []string {
	var errs []string
	for k, v := range m {
		rv, err := resolveFn(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("engine.custom.%s.%s: %s", field, k, err))
			continue
		}
		m[k] = rv
	}
	return errs
}

// resolveLocalEnv resolves the engine.custom.local fields. command, args, cwd
// and the I/O paths all become (or are logged as part of) a command line, so
// they reject secret-like references.
func resolveLocalEnv(l *customengine.LocalConfig) []string {
	var errs []string
	errs = append(errs, resolveScalarEnvStrict("local.command", &l.Command)...)
	errs = append(errs, resolveScalarEnvStrict("local.cwd", &l.Cwd)...)
	errs = append(errs, resolveScalarEnvStrict("local.input_file", &l.InputFile)...)
	errs = append(errs, resolveScalarEnvStrict("local.output_file", &l.OutputFile)...)
	for i := range l.Args {
		rv, err := resolveEnvRefsStrict(l.Args[i])
		if err != nil {
			errs = append(errs, fmt.Sprintf("engine.custom.local.args[%d]: %s", i, err))
			continue
		}
		l.Args[i] = rv
	}
	return errs
}

// resolveHTTPEnv resolves the engine.custom.http fields.
func resolveHTTPEnv(h *customengine.HTTPConfig) []string {
	var errs []string
	errs = append(errs, resolveScalarEnv("http.url", &h.URL)...)
	errs = append(errs, resolveScalarEnv("http.method", &h.Method)...)
	errs = append(errs, resolveStringMapEnv("http.headers", h.Headers)...)
	for i := range h.Files {
		rv, err := resolveEnvRefs(h.Files[i].Path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("engine.custom.http.files[%d].path: %s", i, err))
			continue
		}
		h.Files[i].Path = rv
	}
	if rb, err := resolveEnvRefsInAny(h.RequestBody); err != nil {
		errs = append(errs, fmt.Sprintf("engine.custom.http.request_body: %s", err))
	} else {
		// RequestBody is any (map, sequence, or scalar), so write the resolved
		// value back unconditionally — a scalar like ${session_input} would be
		// dropped by a map-only type assertion.
		h.RequestBody = rb
	}
	return errs
}

// resolveModelEnv resolves env references in engine.model string values.
func resolveModelEnv(model *ModelConfig, opts ResolveCustomEngineOptions) []string {
	var errs []string
	if !opts.SkipModelIdentity {
		if v, err := resolveEnvRefs(model.Provider); err != nil {
			errs = append(errs, fmt.Sprintf("engine.model.provider: %s", err))
		} else {
			model.Provider = v
		}
		if v, err := resolveEnvRefs(model.Name); err != nil {
			errs = append(errs, fmt.Sprintf("engine.model.name: %s", err))
		} else {
			model.Name = v
		}
	}
	if v, err := resolveEnvRefs(model.BaseURL); err != nil {
		errs = append(errs, fmt.Sprintf("engine.model.base_url: %s", err))
	} else {
		model.BaseURL = v
	}
	for k, v := range model.Params {
		rv, err := resolveEnvRefs(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("engine.model.params.%s: %s", k, err))
			continue
		}
		model.Params[k] = rv
	}
	return errs
}

func aggregateConfigErrors(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("custom engine config errors:\n  - %s", strings.Join(errs, "\n  - "))
}

// resolveEnvRefsInAny resolves env references inside an arbitrary YAML value,
// recursing through maps and slices and resolving string leaves.
func resolveEnvRefsInAny(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return resolveEnvRefs(t)
	case map[string]any:
		for k, val := range t {
			rv, err := resolveEnvRefsInAny(val)
			if err != nil {
				return nil, err
			}
			t[k] = rv
		}
		return t, nil
	case []any:
		for i, val := range t {
			rv, err := resolveEnvRefsInAny(val)
			if err != nil {
				return nil, err
			}
			t[i] = rv
		}
		return t, nil
	default:
		return v, nil
	}
}

// resolveEnvRefs replaces ${VAR}, ${VAR:-default} and ${VAR?message} env
// references in s. References to built-in template variables are left intact.
func resolveEnvRefs(s string) (string, error) {
	return resolveEnvRefsWith(s, false)
}

// resolveEnvRefsStrict behaves like resolveEnvRefs but rejects references to
// secret-like environment variables. It is used for fields that become a
// command line (local.command / local.args), keeping credentials out of
// process listings and exec traces.
//
// The resolver iterates: if the produced value itself embeds further ${...}
// references (a wrapper env var whose value is "${CUSTOM_AGENT_TOKEN}"), the
// next pass re-checks them, so a non-sensitive wrapper cannot smuggle a
// sensitive name through to run-time rendering. Iteration stops at a fixed
// point or at maxStrictExpansionDepth to bound pathological cycles.
func resolveEnvRefsStrict(s string) (string, error) {
	const maxStrictExpansionDepth = 10
	for range maxStrictExpansionDepth {
		resolved, err := resolveEnvRefsWith(s, true)
		if err != nil {
			return "", err
		}
		if resolved == s || !strings.Contains(resolved, "${") {
			return resolved, nil
		}
		s = resolved
	}
	return "", errors.New("strict env resolution exceeded maximum depth (possible reference cycle)")
}

// resolveEnvRefsWith is the strictness-parameterized impl behind
// resolveEnvRefs and resolveEnvRefsStrict; the bool is the strict-mode toggle
// kept here as a private implementation detail.
//
//revive:disable-next-line:flag-parameter
func resolveEnvRefsWith(s string, rejectSecrets bool) (string, error) {
	if s == "" {
		return s, nil
	}
	if !strings.Contains(s, "${") {
		// No env reference to expand. In strict mode the raw literal still
		// needs to be checked — `args: ["--token", "sk-ant-..."]` would
		// otherwise hand the credential straight to process.command tracing.
		if rejectSecrets && looksLikeSecret(s) {
			return "", errors.New(
				"value looks like a credential literal; pass secrets via engine.custom.env instead of inline command-line literals",
			)
		}
		return s, nil
	}

	var b strings.Builder
	for i := 0; i < len(s); {
		open := strings.Index(s[i:], "${")
		if open < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+open])
		start := i + open
		closeIdx := strings.IndexByte(s[start:], '}')
		if closeIdx < 0 {
			// Unterminated reference: keep the remainder verbatim.
			b.WriteString(s[start:])
			break
		}
		inner := s[start+2 : start+closeIdx]
		value, leaveIntact, err := resolveEnvToken(inner, rejectSecrets)
		if err != nil {
			return "", err
		}
		if leaveIntact {
			b.WriteString(s[start : start+closeIdx+1])
		} else {
			b.WriteString(value)
		}
		i = start + closeIdx + 1
	}
	out := b.String()
	// Even after expansion, a mixed input like `Bearer ${TOKEN_PREFIX}sk-ant-real-key`
	// can resolve to a string whose suffix is a credential literal. Run the
	// final check so strict-mode callers get a consistent guarantee:
	// no credential-shaped literal reaches a command line.
	if rejectSecrets && looksLikeSecret(out) {
		return "", errors.New(
			"resolved value looks like a credential literal; pass secrets via engine.custom.env instead of inline command-line literals",
		)
	}
	return out, nil
}

// resolveEnvToken parses one ${VAR}-style placeholder and returns the
// resolved value (or a flag asking the caller to keep the placeholder
// literally). The bool is the same strict-mode toggle threaded from
// resolveEnvRefsWith.
//
//revive:disable-next-line:flag-parameter
func resolveEnvToken(inner string, rejectSecrets bool) (value string, leaveIntact bool, err error) {
	tok := customengine.ParseTemplateToken(inner)

	if IsBuiltinTemplateVar(tok.Name) {
		if rejectSecrets && isSensitiveTemplateVar(tok.Name) {
			return "", false, fmt.Errorf(
				"secret-like template variable ${%s} must not be referenced in a command line; pass credentials via engine.custom.env instead",
				tok.Name,
			)
		}
		return "", true, nil
	}

	if rejectSecrets && isSensitiveEnvName(tok.Name) {
		return "", false, fmt.Errorf(
			"secret-like environment variable %q must not be referenced in a command line; pass credentials via engine.custom.env instead",
			tok.Name,
		)
	}

	if v := os.Getenv(tok.Name); v != "" {
		if err := rejectIfSecretLiteral(tok.Name, v, rejectSecrets, false); err != nil {
			return "", false, err
		}
		return v, false, nil
	}
	if tok.HasDefault {
		if err := rejectIfSecretLiteral(tok.Name, tok.Default, rejectSecrets, true); err != nil {
			return "", false, err
		}
		return tok.Default, false, nil
	}
	if tok.HasErrForm {
		return "", false, tok.RequiredErr()
	}
	return "", false, fmt.Errorf("environment variable %s is required but not set", tok.Name)
}
