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

| Layer      | Path                                                                                                    | Missing |
| :--------- | :------------------------------------------------------------------------------------------------------ | :------ |
| `embed`    | empty `Config{}` — no vendor defaults baked in                                                          | always  |
| `user`     | `$SKILL_UP_CONFIG`, else `$XDG_CONFIG_HOME/skill-up/config.yaml`, else `~/.config/skill-up/config.yaml` | skipped |
| `project`  | `$PWD/.skill-up.yaml`                                                                                   | skipped |
| `explicit` | `--config <path>` passed to any command                                                                 | error   |

Missing `user` and `project` files are skipped. Optional files that cannot be read or parsed produce a warning. A missing or invalid `--config` path is a hard error. Specify `--config` at most once. The XDG path is used only when `XDG_CONFIG_HOME` is absolute; otherwise discovery and the default `init` write target fall back to the home directory.

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
- **`${VAR}` placeholders** expand only in `env` values supplied by the user or explicit layer, in one pass against the process environment before any defaults are applied. Unset references remain literal, and automatically discovered project values are never expanded.
- **`runtime_kwargs`** — defaults for `run` keyed by `environment.type` (e.g.
  `opensandbox`). `eval.yaml` values take precedence; missing keys fall back
  here. Keys mirror the environment provider's kwargs schema.

### Credential boundary

Automatically discovered project files still supply defaults. A newly expanded OTLP header is withheld when project defaults can control where the exporter that carries it sends data, or which peer that exporter accepts. The check is per signal: `OTEL_EXPORTER_OTLP_TRACES_HEADERS` follows the traces route, meaning the traces endpoint, protocol and TLS options, then the shared `OTEL_EXPORTER_OTLP_*` options that no trusted traces setting overrides. A project that only sets a metrics endpoint therefore does not withhold it. The shared `OTEL_EXPORTER_OTLP_HEADERS` can reach every signal, so any project route setting withholds it. Project proxy settings and TLS root settings (`SSL_CERT_FILE`, `SSL_CERT_DIR`) count for every signal. This protection applies only to headers produced by expansion; it does not change the treatment of literal headers, headers already in the process environment, or evaluation execution. Review any config before selecting it with `--config`, which trusts the file for expansion. For `init`, `--config` is only a copy source and is not used to supply defaults.

---

A trusted per-signal HTTP(S) endpoint must parse as an absolute URL with a host before it can override a project-supplied shared endpoint for this check. A signal-specific certificate or key path cannot establish that a shared project TLS setting has been displaced. Exporters read those files at initialization, possibly in a child process, and can fall back to the shared setting if loading fails. In that combination the expanded header is withheld. These rules apply equally to trusted config and process environment overrides.

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

This adds an `opensandbox.base_url` default for this project only — your
`~/.config/skill-up/config.yaml` still applies elsewhere.

### One-off override on the command line

```bash
skill-up run ./evals/eval.yaml --config /tmp/debug-config.yaml
```

`--config` overrides both user and project layers for this invocation.
