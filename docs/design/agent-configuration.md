# Agent configuration semantics

This document records the current `v1alpha1` behavior that future agent
configuration refactors must preserve. It is a compatibility contract, not a
claim that every current precedence rule is ideal.

## Terms

| Term | Meaning |
| --- | --- |
| Engine | The CLI adapter skill-up executes, such as `claude_code`, `codex`, `qodercli`, `qwen_code`, or a custom engine. |
| Protocol | The API protocol selected by the engine adapter. It is not selected by `provider`. |
| Provider | The namespace used to look up model, credential, and endpoint configuration. |
| Model name | The upstream model identifier. It may contain `/` when the upstream service uses an opaque slashed identifier. |
| Requested configuration | Values supplied by eval YAML, CLI flags, environment variables, or the credential file. |
| Applied configuration | Values skill-up passes to the agent CLI after adapter-specific normalization. Local CLI settings may still override them. |
| Observed configuration | Values explicitly reported by the running agent. Missing values are unknown, not inferred from the applied configuration. |
| Local-login delegation | No credential is injected; the agent CLI may use its existing local login state. This does not prove that the login is valid. |

The engine determines the protocol. For example, `provider: dashscope` with a
`codex` engine uses an OpenAI-compatible endpoint, while the same provider with
`claude_code` uses an Anthropic-compatible endpoint.

## Built-in engine behavior

| Engine | Protocol and auth surface | Model behavior | Unsupported or delegated behavior |
| --- | --- | --- | --- |
| `claude_code` | Anthropic-compatible; `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` | Passes an explicit model through to Claude Code | Missing credentials delegate to Claude Code's local login. |
| `codex` | OpenAI-compatible; `OPENAI_API_KEY` and `OPENAI_BASE_URL` | A non-OpenAI provider requires a base URL before skill-up emits a custom Codex provider and model override | Without the required custom-provider endpoint, the model override is omitted and Codex uses local settings. Missing credentials may delegate to local login. |
| `qodercli` | Qoder-managed auth; Global uses `QODER_PERSONAL_ACCESS_TOKEN`, CN uses `QODERCN_PERSONAL_ACCESS_TOKEN` (or the `QODER_CN_ACCESS_TOKEN` input alias), with local-login fallback | Supports `lite`, `efficient`, `auto`, `performance`, and `ultimate` | `engine.kwargs.edition` selects `global` (default) or `cn` and switches the CLI, installer, auth, and session root together. Other model values and `base_url` are ignored. |
| `qwen_code` | OpenAI-compatible; `OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `OPENAI_MODEL` | Passes an explicit model to Qwen Code | Missing credentials may delegate to Qwen OAuth or another existing local login. |
| Custom engine | Defined by `engine.custom` | Receives the configured provider/model values through session input and template variables | Capabilities and auth behavior belong to the custom-engine contract. |

`Agent.Check` only inspects installation or availability. Credential checks are
static and do not make a model request. The first real agent run is therefore
the first validation of a delegated local login.

## Current resolution order

The runner path resolves values in these stages:

1. Load the eval YAML, apply `--engine`, and retain the raw `--model` value.
2. Load `~/.skill-up/credentials.yaml` and provider-scoped environment values.
3. Build one role-aware `ResolvedAgentConfig`. Legacy slash disambiguation is
   performed once at this point instead of mutating and later repairing the
   loaded eval config.
4. Resolve provider-scoped `MODEL`, `API_KEY`, and `BASE_URL` values. A
   provider-scoped model environment variable currently overrides the YAML
   model; provider environment credentials override the credential file.
5. Preserve explicit CLI `--model` and `--api-key` precedence.
6. Apply the selected adapter's declared protocol and capability contract.
   Keep the requested model intact, compute the applied model once, remove
   unsupported kwargs, and emit warnings for ignored or invalid explicit
   settings before case execution.
7. Pass the applied value to the selected adapter, which constructs its
   command and environment without repeating model normalization.

Runner and judge roles use the same resolution flow. Until an explicit judge
engine schema is introduced, the judge inherits the runner engine lifecycle and
kwargs, while resolving its provider/model and credentials as a separate role.
Reports use the resolved runner identity rather than reconstructing it from a
CLI-mutated eval config. `result.json` retains the legacy `engine_name` and
`model_name` fields while also recording credential-free
`requested_configuration`, `applied_configuration`, and optional
`observed_configuration` objects. Per-session results carry the requested and
applied values, capability warnings, and an observed `model` only when the
agent explicitly reports it. An absent observation remains unknown; skill-up
does not probe authentication or spend tokens to infer local CLI state.
An applied provider is recorded only when the adapter actively selects that
provider. If Codex falls back to local provider selection, both the applied
provider and model are empty and credentials resolved for the rejected provider
are not injected into the fallback invocation.

## Model connection boundary

Provider configuration, a resolved model connection, and an agent's native
configuration are related but distinct layers:

1. Provider configuration is static input. It may contain credentials and
   protocol-specific endpoints for a named namespace such as `dashscope`.
2. A resolved model connection selects one `(provider, protocol)` combination,
   preserves the source of each selected value, and records whether auth and
   routing are injected or delegated to agent-local state.
3. The adapter materializes that connection as CLI flags, environment
   variables, or native agent configuration. Native configuration files and
   local login databases remain owned by the agent and may be opaque to
   skill-up.

`ResolvedAgentConfig` currently spans the first two steps and the capability
pass computes what skill-up can apply. A later protocol-aware resolver may
introduce an internal `ResolvedModelConnection`, but it should not be confused
with observed runtime state: it describes the connection selected for the
adapter, not proof of the provider, model, or credential ultimately used by the
agent.

This layering is informed by Harbor's
[`ProviderAccess`, `ModelConnectionSpec`, and `ResolvedModelConnection`](https://github.com/harbor-framework/harbor/blob/71180a2e6fb40626b661c13f261b1d44517ad91a/src/harbor/agents/model_connection.py),
while retaining skill-up's different compatibility constraints. In particular,
skill-up must not infer the provider from an opaque model ID, fan one credential
out to unrelated provider aliases, or treat a resolved connection as an
observation. Explicit empty values must also remain distinguishable from absent
values where an empty value intentionally suppresses inherited configuration.

Native agent configuration support, if added later, belongs after connection
resolution. It must use the same adapter materialization boundary rather than
becoming a second provider/model/credential resolver.

## Legacy slashed model compatibility

`--model provider/name` is a public, historical CLI form and remains supported.
Because `/` is also valid inside an opaque upstream model ID, skill-up uses the
following compatibility behavior:

| Input | Current interpretation |
| --- | --- |
| `--model openai/gpt-4` | `provider=openai`, `name=gpt-4`; `openai` and `anthropic` are always-known framework namespaces. |
| `--model dashscope/qwen3.6-plus` with DashScope credentials or endpoint configured | `provider=dashscope`, `name=qwen3.6-plus`. |
| `--model anthropic_modelscope/deepseek-v4-pro` with no matching provider configuration | `provider=""`, `name=anthropic_modelscope/deepseek-v4-pro`; the full ID is preserved. |
| YAML `model.provider` plus `model.name` | Always treated as an explicit pair, including when local login is the only auth source. |

Provider detection is a disambiguation signal, not an authentication check. A
known provider can still fail authentication during the real agent run.

## Public GitHub Action compatibility

The root GitHub Action exposes separate `engine`, `provider`, and `model`
inputs. Its current translation is part of the public compatibility surface:

- an explicit `codex` engine folds a bare provider and model into the legacy
  `provider/model` CLI value so skill-up constructs Codex custom-provider
  configuration;
- other explicit engines receive the model name without a provider prefix and
  get protocol-specific environment variables;
- an empty engine does not add `--engine`, allowing eval YAML to select the
  adapter, and exports both protocol endpoint variables when a known provider
  is selected;
- absent credentials are not synthesized, preserving agent-local login flows.

These translations are covered by `action/main_test.py`. Any future explicit
`--provider` flag must be additive: tagged Actions and historical
`--model provider/name` commands must remain valid.

## Known gaps for later phases

- Nested provider endpoints are flattened before the adapter protocol is known.
- Provider-scoped `MODEL` currently overrides an explicit YAML model.
- `engine.version`, `engine.entry`, and `engine.model.params` now produce
  warnings when ineffective, but their final implementation/removal is deferred
  to the installation and schema phases.
- The actual installed CLI version is not yet recorded as observed runtime
  configuration.

See [Issue #196](https://github.com/alibaba/skill-up/issues/196) for the staged
cleanup plan.
