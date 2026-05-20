# User Configuration

skill-up auto-loads a user-config file on every invocation. It is the
recommended place for OpenTelemetry defaults, ambient env vars, and
per-environment `run` kwargs that you don't want to repeat on the command
line or bake into each `eval.yaml`.

This page covers what is loaded, in what order, and how to bootstrap a
config file with `skill-up init`.

---

## Discovery chain

Layers are merged in order of increasing precedence:

```text
embed (empty)
  < user      (~/.config/skill-up/config.yaml, XDG-aware)
  < project   ($PWD/.skill-up.yaml)
  < explicit  (--config <path>)
```

| Layer      | Path                                                                                                       | Missing |
| :--------- | :--------------------------------------------------------------------------------------------------------- | :------ |
| `embed`    | empty `Config{}` — no vendor defaults baked in                                                              | always  |
| `user`     | `$SKILL_EVAL_CONFIG`, else `$XDG_CONFIG_HOME/skill-up/config.yaml`, else `~/.config/skill-up/config.yaml`   | skipped |
| `project`  | `$PWD/.skill-up.yaml`                                                                                       | skipped |
| `explicit` | `--config <path>` passed to any command                                                                     | error   |

A missing or corrupt `user`/`project` file is downgraded to a `warning:` on
stderr — the command keeps running with whatever layers loaded. A missing or
invalid `--config` path is a hard error.

Higher-precedence layers overlay non-zero fields onto lower layers; map fields
(`telemetry.resource_attributes`, `env`, `runtime_kwargs`) are merged key-wise
rather than replaced wholesale.

---

## Schema

```yaml
schema_version: v1alpha1
kind: SkillEvalConfig

telemetry:
  service_name: skill-up                              # OTEL_SERVICE_NAME
  traces_exporter: otlp                               # OTEL_TRACES_EXPORTER
  traces:
    endpoint: http://localhost:4317                   # OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
    protocol: grpc                                    # OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
  resource_attributes:                                # serialized to OTEL_RESOURCE_ATTRIBUTES
    deployment.environment: local
  verbose: false                                      # if true, enables OTEL_LOG_* payload capture

env:                                                  # arbitrary defaults, only-if-unset
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${OTLP_TOKEN}

runtime_kwargs:                                       # per-environment.type defaults for `run`
  opensandbox:
    base_url: http://localhost:8080
```

Semantics:

- **`telemetry` and `env`** — each key maps to an `OTEL_*` (or arbitrary)
  environment variable. Values are only applied **if the corresponding env var
  is not already set**, so a value on the command line or in your shell
  always wins.
- **`${VAR}` placeholders** in `env` values are expanded from the process
  environment at load time, so secrets stay out of the file.
- **`runtime_kwargs`** — defaults for `run` keyed by `environment.type` (e.g.
  `opensandbox`). `eval.yaml` values take precedence; missing keys fall back
  here. Keys mirror the environment provider's kwargs schema.

---

## Bootstrapping with `skill-up init`

`skill-up init` writes a config file to one of two well-known locations:

```bash
skill-up init                            # template -> ~/.config/skill-up/config.yaml
skill-up init --local                    # template -> ./.skill-up.yaml
skill-up init --print                    # template -> stdout
skill-up init --force                    # overwrite an existing target
```

The default template is fully commented-out, so writing it does not change
behavior — it just documents every supported key in one place.

### Seeding from an existing config

Pass `--config <source>` to use an existing YAML file as the **source**.
`init` validates it, then writes its raw bytes (comments and formatting
preserved) to the target chosen by `--local`:

```bash
# Copy a team-shared config to the XDG location
skill-up init --config ./team-config.yaml

# Same content, but as the project-layer file
skill-up init --config ./team-config.yaml --local
```

`--config` and `--local` are not mutually exclusive. The `--config` flag for
`init` is a *source* (read from); for every other subcommand (`run`,
`validate`, ...) it is a *load-path override* and sits at the top of the
discovery chain.

### Inspecting the effective config

The simplest way to see what `run` will pick up is `--print`:

```bash
# Validate and dump the file `run` will see at the explicit layer
skill-up init --config ./team-config.yaml --print
```

---

## Examples

### Always export traces to a local collector

`~/.config/skill-up/config.yaml`:

```yaml
schema_version: v1alpha1
kind: SkillEvalConfig
telemetry:
  service_name: skill-up
  traces_exporter: otlp
  traces:
    endpoint: http://localhost:4317
    protocol: grpc
  resource_attributes:
    deployment.environment: local
```

Now every `skill-up run` exports traces without touching env vars.

### Per-project override

`./.skill-up.yaml` next to your eval suite:

```yaml
schema_version: v1alpha1
kind: SkillEvalConfig
runtime_kwargs:
  opensandbox:
    base_url: http://sandbox.internal:9090
```

This adds an `opensandbox.base_url` default for this project only — your
`~/.config/skill-up/config.yaml` still applies elsewhere.

### One-off override on the command line

```bash
skill-up run ./evals/eval.yaml --config /tmp/debug-config.yaml
```

`--config` overrides both user and project layers for this invocation.
