# Credential Package

`internal/credential` is responsible for collecting credential / model information from configuration files, environment variables, and CLI flags into unified agent initialization parameters, providing a consistent input source across different agents.

This document describes the **recommended approach and pipeline constraints**, focusing on:

- How the parameter dict eventually passed to agent initialization is decided
- How runner agent and judge agent configurations are differentiated
- When a provider is configured, how environment variables override global configuration
- How different agents consume these parameters
- What logs should be emitted to avoid the misleading impression that "it looks effective but is actually only loaded globally"

## Goal

Consolidate information scattered across the following sources into agent initialization parameters:

- `engine.model` in the eval config
- The judge agent model setting in the judge config
- CLI overrides such as `--model` and `--api-key`
- The global credential configuration file
- Process environment variables

Before entering `agent.DetectAgent(...)` / judge-agent initialization, produce a parameter dict **per agent kind**:

```go
type AgentInitParams struct {
    Provider string
    Model    string
    APIKey   string
    BaseURL  string
}
```

This dict is the "intermediate decision result" — it does not require every agent to consume all four fields verbatim. Each agent may use them selectively according to its own capabilities.

## Two Pipelines

### 1. Runner Agent

The runner agent is the main agent that executes cases; default sources are:

- `eval.engine.name`
- `eval.engine.model.provider`
- `eval.engine.model.name`
- `eval.engine.model.base_url`
- CLI overrides
- Result returned by the credential resolver

### 2. Judge Agent

The judge agent only exists when `judge.type: agent_judge`.

Its configuration decisions should be handled separately from the runner agent:

- If the judge has independent configuration, prefer the judge's own provider/model/api-key/base-url
- If no independent configuration is provided, reuse the runner agent's final result

It is recommended to treat the judge agent as a separate parameter resolution pipeline rather than "borrowing some fields" from the runner agent's initialization.

## Final Parameter Decisions

For each agent kind (`runner` / `judge`), compute the final values of:

- `provider`
- `model`
- `api_key`
- `base_url`

The recommended priority order follows.

### provider

1. Explicit configuration on the agent role itself
2. If this is a judge agent without independent configuration, reuse the runner agent provider
3. When no provider is set, do not perform provider-scoped credential lookups

The provider is a precondition for environment-variable and credential-file lookups. When it is missing, do not fabricate a provider just to apply the generic logic.

### model

Once `provider` is determined, `model` should follow the same provider-scoped resolution as `api-key` / `base-url`.

1. CLI `--model` overrides the current role's model
2. Explicit configuration on the agent role itself
3. If the current role has a provider, prefer the provider-scoped environment variable, e.g. `${PROVIDER}_MODEL`
4. If this is a judge agent without independent configuration, reuse the runner agent model

`model` consumption is still agent-specific, but the "decision of the final string" should be unified and should no longer bypass provider-scoped env rules on a per-case basis.

### api-key

1. CLI `--api-key` overrides the current role's provider
2. If the current role has a provider, prefer the provider-scoped environment variable
3. If the env var is not set, fall back to the global credential configuration
4. If this is a judge agent without independent configuration, reuse the runner agent's final api-key

Key points:

- `--api-key` has higher priority than environment variables and the credential file
- However, the override target must be the "final provider of the current role" — it must not override only the global default provider
- When the provider is known, prefer reading by provider from environment variables before deciding whether to fall back to the configuration file

### base-url

1. Explicit configuration on the agent role itself
2. If the current role has a provider, prefer the provider-scoped environment variable
3. If the env var is not set, fall back to the global credential configuration
4. If this is a judge agent without independent configuration, reuse the runner agent's final base-url

`base-url` typically does not require an ad-hoc CLI override; if a CLI flag is added in the future, it should outrank env vars and the configuration file, consistent with `api-key`.

## Recommended Resolution Flow

A unified "resolve by role" entry point is recommended, e.g.:

```go
type AgentKind string

const (
    AgentKindRunner AgentKind = "runner"
    AgentKindJudge  AgentKind = "judge"
)

func ResolveAgentInitParams(
    kind AgentKind,
    roleConfig AgentConfigSource,
    fallback *AgentInitParams,
    resolver *Resolver,
    cli CLIOverrides,
) AgentInitParams
```

Where:

- `roleConfig` is the role's own raw configuration
- `fallback` is only used when the judge agent reuses the runner agent's final result
- `resolver` only provides provider-scoped credential lookup
- `cli` only provides ad-hoc overrides

Recommended execution order:

1. Determine the current role's final `provider`
2. Determine the role's own explicit `model` / `base_url`
3. If the provider is non-empty, uniformly read `${PROVIDER}_MODEL` / `${PROVIDER}_API_KEY` / `${PROVIDER}_BASE_URL`
4. If the `api-key` / `base-url` env var is missing, read from the resolver's global credential configuration
5. For the judge agent, fall back to the runner agent's final result for any missing fields
6. Apply CLI overrides last
7. Output the final `AgentInitParams`

Benefits:

- Judge and runner share the same rules, only the input sources differ
- Provider is decided first, avoiding cross-application of the wrong provider's env/config
- CLI overrides are applied last, making behavior the most direct

## Environment Variable Override Rules

Once a role has a determined `provider`, attempt provider-scoped env-var overrides over the global configuration.

Examples:

- `openai` -> `OPENAI_MODEL` / `OPENAI_API_KEY` / `OPENAI_BASE_URL`
- `anthropic` -> `ANTHROPIC_MODEL` / `ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL`
- `qoder` -> `QODER_MODEL` / `QODER_API_KEY` / `QODER_BASE_URL`
- Any other provider -> `<PROVIDER>_MODEL` / `<PROVIDER>_API_KEY` / `<PROVIDER>_BASE_URL`

Rules:

- Env vars only override the configuration corresponding to the current role's final provider
- Do not treat "every env var found during scanning" as "effective configuration for the current role"
- When the provider is empty, skip the `${PROVIDER}_*` lookup and let the agent handle special-case fallbacks itself
- Do not maintain extra special-case env-var names for any provider in the resolver; agent-specific variables like `QODER_PERSONAL_ACCESS_TOKEN` should be consumed by the agent itself
- Logs must distinguish between "which global credentials were discovered" and "which provider's parameter set was actually adopted by the current role"

## Agent Consumption Rules

The unified layer is responsible for producing `AgentInitParams`, but **how to use them is up to each agent's implementation**.

### claude-code

Claude Code in theory supports:

- `model`
- `api-key`
- `base-url`

However, "what the official documentation describes" and "what the current environment empirically prioritizes" must be distinguished.

Official documentation:

- The model can be specified via `claude --model <name>`
- `ANTHROPIC_MODEL` is also supported
- `model` can also be configured in `settings.json`

Black-box experiments on the local machine (Claude Code `2.1.89`) observed:

- `settings.env.ANTHROPIC_MODEL` outranks `ANTHROPIC_MODEL` in the process environment
- `settings.env.ANTHROPIC_MODEL` also outranks the command-line `--model`
- Top-level `settings.model` is in fact overridden by `settings.env.ANTHROPIC_MODEL`

Therefore, in the current environment, if a user's local `settings.env` already pins `ANTHROPIC_MODEL`:

- Injecting the model via env vars from skill-up usually has no effect
- Passing the model via `--model` from skill-up may also have no effect
- Logs should clearly state "the parameter has been passed, but the final behavior may still be governed by local Claude settings.env"

Implementation guidance for claude-code:

- `api-key` / `base-url` may still be passed as explicit initialization parameters
- `model` may continue to be retained as an initialization parameter, but do not assume it will take effect once passed
- For future support of claude-code model configuration, prioritize black-box logging that helps users see whether local settings end up overriding everything

### codex / openai-compatible judge client

These agents typically support:

- `model`
- `api-key`
- `base-url`

So they usually translate the final parameters into:

- Command-line arguments
- The agent's own `Cfg.EnvVars`
- Or fields used to initialize the SDK/client

### qodercli

Qoder CLI's model parameter capabilities differ from other agents; see the official documentation:

[Qoder CLI model flags](https://docs.qoder.com/cli/model#command-line-flag)

Constraints:

- `model` only accepts Qoder-recognized values such as `lite`, `efficient`, `auto`
- `base-url` is ignored by qodercli
- `api-key` does not take effect as a qodercli command-line argument; Global authenticates with `QODER_PERSONAL_ACCESS_TOKEN`, while `engine.kwargs.edition=cn` uses `QODERCN_PERSONAL_ACCESS_TOKEN`
- `QODER_CN_ACCESS_TOKEN` is a skill-up input alias for secret-manager/Keychain injection and is forwarded to qodercn under the official `QODERCN_PERSONAL_ACCESS_TOKEN` name

Therefore for qodercli:

- The final `model` may be parsed and recorded
- The final `api-key` / `base-url` may be parsed; `api-key` should be converted to a runtime env var, while `base-url` should be explicitly ignored
- The actually effective authentication path is env-var injection, not a CLI flag

Do not assume that "the unified layer computed an `api-key`, so every agent will actually use it." qodercli is the canonical counterexample.

## Logging Requirements

Logs must answer two questions:

1. What configuration the current role ultimately used
2. Where that configuration came from

At a minimum, the following three categories should be distinguished.

### 1. Global Discovery Logs

Used to describe what the resolver / env scan found:

```text
[CREDENTIAL_DISCOVERED] provider=anthropic source=env api_key=sk****xx
[CREDENTIAL_DISCOVERED] provider=openai source=file base_url=https://...
```

This category only indicates "global candidate configurations were discovered"; it **does not** mean "the current run is actually using them".

### 2. Role Final Decision Logs

Used to describe what the current agent ultimately adopted:

```text
[AGENT_CONFIG] kind=runner engine=qodercli provider=test model=auto source.model=cli
[AGENT_CONFIG] kind=runner engine=qodercli provider=test auth_env=QODER_PERSONAL_ACCESS_TOKEN source.auth=env
[AGENT_CONFIG] kind=judge engine=openai provider=openai model=gpt-4.1 source.model=judge_config
```

These are the logs users rely on to determine "whether the override took effect."

### 3. Field-Ignored Logs

Used to indicate that a field was parsed but is not consumed by the current agent:

```text
[AGENT_CONFIG] kind=runner engine=qodercli auth_env=QODER_PERSONAL_ACCESS_TOKEN source.auth=resolved
[AGENT_CONFIG] kind=runner engine=qodercli ignored.base_url reason=unsupported_by_agent
```

These logs are particularly important for qodercli; otherwise users may mistakenly believe that `--api-key` will always be used.

## Recommended Logging Principles

- Do not print "globally loaded credentials" as "the provider currently in use"
- Do not print anthropic/openai effective conclusions unrelated to the current role in test/mock provider scenarios
- When a CLI argument is accepted but ignored by the agent, explicitly note "the argument has been parsed but is not consumed by the current agent"
- Logs should include `kind=runner|judge`
- Logs should include `engine=<engine-name>`
- Sensitive values must be masked

## Relation to the Current Implementation

The resolution pipeline described in this document is fully implemented in `agent_init.go`:

- `ResolveRunnerInitParams()` — resolves runner agent parameters from the eval config and CLI overrides
- `ResolveJudgeInitParams()` — resolves judge agent parameters, falling back to runner params when no independent judge config is set
- `resolveAgentInitParams()` — shared resolution logic used by both pipelines

`Resolver.Load()` emits "global discovery" logs (discovered providers from .env and config file). These are distinct from the per-role `[AGENT_CONFIG]` logs emitted by `logResolvedAgentConfig()` in `agent_init.go`, which describe the final resolved parameters and their sources for each agent kind.

## Current Package Responsibilities

It is recommended that `internal/credential` stays within the following responsibilities:

- Loading credentials from configuration files and environment variables
- Providing a provider-scoped query API
- Emitting logs at the "global discovery" level
- Providing baseline data for upstream parameter decisions

It is not recommended to hard-code each agent's consumption differences into the resolver. Behaviors like qodercli's "recognizes only some `model` values, ignores `api-key`/`base-url`" should be decided and logged by the agent itself.
