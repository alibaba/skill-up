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
  < explicit  (--config <path>)
```

| Layer      | Path                                                                                                    | Missing |
| :--------- | :------------------------------------------------------------------------------------------------------ | :------ |
| `embed`    | empty `Config{}` — no vendor defaults baked in                                                          | always  |
| `user`     | `$SKILL_UP_CONFIG`, else `$XDG_CONFIG_HOME/skill-up/config.yaml`, else `~/.config/skill-up/config.yaml` | skipped |
| `explicit` | `--config <path>` passed to any command                                                                 | error   |

A missing `user` file is silently skipped; read or parse errors produce a
`warning:` on stderr. A missing or invalid `--config` path is a hard error.

An automatically discovered `$PWD/.skill-up.yaml` is ignored with a warning.
After reviewing the file, select it with `--config ./.skill-up.yaml` to apply
its defaults. This opt-in covers the whole file, including `runtime_kwargs`.
Selecting a different explicit file does not trust the project file. A project
file selected through `SKILL_UP_CONFIG` is also trusted as the user layer.
Use an absolute path when setting `SKILL_UP_CONFIG` persistently: a relative
path resolves from the working directory and can select a different file in
each repository.

Higher-precedence layers overlay non-zero fields onto lower layers; map fields
(`telemetry.resource_attributes`, `env`, `runtime_kwargs`) are merged key-wise
rather than replaced wholesale.

---

## Schema

```yaml
schema_version: v1alpha1
kind: SkillUpConfig

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
- **`${VAR}` placeholders** in `env` values from the `user` and `explicit`
  layers are expanded from the process environment before defaults are applied.
  Selecting a file grants it access to host environment variables, including
  secrets, so review it before opting in. Unset variables remain literal;
  variables set to an empty string expand to an empty string. Expansion is
  single-pass and does not read other defaults from the same config. Bare
  `$VAR` and shell expressions such as `${VAR:-fallback}` remain literal.
- **`runtime_kwargs`** — defaults for `run` keyed by `environment.type` (e.g.
  `opensandbox`). `eval.yaml` values take precedence; missing keys fall back
  here. Keys mirror the environment provider's kwargs schema.

### Endpoint trust policy

Only trusted user or explicitly selected config files may provide defaults.
An unselected project file cannot override a telemetry endpoint, inject a proxy
or agent base URL through `env`, or redirect an OpenSandbox runtime through
`runtime_kwargs`. This prevents it from redirecting credentials supplied by
trusted config or the process environment, even when its values are literal.

This is a breaking change for projects that relied on automatic `.skill-up.yaml`
loading. Review the file and add `--config ./.skill-up.yaml` to each invocation,
or move trusted defaults to the user-level config. Inspect destinations as well
as credential references before opting in: an explicit file can combine its
endpoint with credentials from user config or the shell.
Trust applies to the selected path, not a saved copy of its contents. Review
changes before reusing that path after a repository update. For stable local
defaults, copy a reviewed file to user config with
`skill-up init --config ./.skill-up.yaml` or keep it outside the repository.

This boundary covers user-config discovery. `skill-up run` treats evaluation
configuration as executable input, whether supplied as a path or discovered
automatically. Its runtime and model endpoints can use credentials from user
config or the shell; its script judges can execute code. Review the evaluation
and its scripts before running it with credentials. This change does not add
endpoint restrictions or sandboxing to evaluation configuration.

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

## Environment Variables

Beyond the YAML config file, skill-up respects certain environment variables
that influence runtime behavior. These are separate from the `env` map in
`config.yaml` (which injects variables into the child process environment).

### `SKILL_UP_BASH`

Override the bash interpreter used by skill-up for `.sh` script judges and
shell commands.

| Aspect       | Detail                                                                 |
| :----------- | :--------------------------------------------------------------------- |
| **Purpose**  | Point skill-up at a specific `bash` binary                             |
| **Applies**  | All platforms — Linux, macOS, and Windows                              |
| **Default**  | Unset; skill-up falls back to `bash` on `PATH` (and on Windows, well-known Git Bash locations) |

**Precedence:** When `SKILL_UP_BASH` is set and points to a valid file,
skill-up uses it unconditionally — it is checked **before** any `PATH` lookup
or well-known-path probing.

**Full-trust requirement:** The path you supply is used as-is without further
sandboxing. You are responsible for ensuring it points to a trustworthy bash
binary.

Example (macOS / Linux):

```bash
export SKILL_UP_BASH=/opt/homebrew/bin/bash
skill-up run ./evals/eval.yaml
```

Example (Windows — Git Bash in a non-standard location):

```powershell
$env:SKILL_UP_BASH = "D:\tools\git\bin\bash.exe"
skill-up run .\evals\eval.yaml
```

On Windows, the WSL shim at `C:\Windows\System32\bash.exe` is rejected even
when specified via `SKILL_UP_BASH`; see [Windows Support](./windows.md#running-sh-script-judges-on-windows) for details.

---

## Examples

### Always export traces to a local collector

`~/.config/skill-up/config.yaml`:

```yaml
schema_version: v1alpha1
kind: SkillUpConfig
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
kind: SkillUpConfig
runtime_kwargs:
  opensandbox:
    base_url: http://sandbox.internal:9090
```

After reviewing it, select the file for this run:

```bash
skill-up run ./evals/eval.yaml --config ./.skill-up.yaml
```

This adds an `opensandbox.base_url` default for this invocation. Without
`--config`, the project file is ignored and user-level defaults still apply.

### One-off override on the command line

```bash
skill-up run ./evals/eval.yaml --config /tmp/debug-config.yaml
```

`--config` overrides user-level defaults for this invocation.
