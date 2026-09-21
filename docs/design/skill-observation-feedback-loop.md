# Skill observation feedback loop

Status: implemented for Codex and DeepSeek Harness (`v1alpha1`)

This design introduces compatible observer adapters for current Codex releases and DeepSeek Harness. They capture attributable real-world Skill interactions, review them locally, and turn an approved observation into a candidate regression case.

## Compatibility boundary

The observer plugin targets current Codex CLI and Codex desktop releases that support plugins and lifecycle hooks. It does not target the IDE extension, which does not support plugins.

The DSH adapter is part of `plugins/dsh-skill-up` and targets DSH 0.1.5-rc.2 or newer. Collection is disabled by default and uses committed DSH session events rather than transcript scraping.

The existing skill-up Codex engine remains pinned to 0.80.0 for its custom Chat-compatible model transport. That adapter is evaluation-only and is intentionally unchanged. Observation support does not use transcript parsing or a legacy fallback.

## Architecture

```text
Codex hook payloads
  -> plugin Python runtime (normalization, attribution, redaction)
  -> plugin-local observation v1alpha1
  -> ${PLUGIN_DATA} review store
  -> skill-upper review and explicit approval workflow
  -> plugin MCP candidate case preview/write
  -> optional skill-up validate, then existing run/report workflow

DSH session/event records
  -> plugin adapter (exact attribution, normalization, redaction)
  -> compatible observation v1alpha1
  -> local DSH review store
  -> the same preview and explicit approval workflow
  -> candidate case write plus skill-up validation
  -> isolated baseline/post-change runs and status comparison
```

The observation schema lives in `plugins/skill-up-observer/schemas`. It describes the persisted document without adding a public skill-up CLI or Go package. Host integrations share the schema and normalized fixtures, but do not share lifecycle payloads or runtime implementation language.

## Attribution

An observation records one of these attribution methods:

- `explicit`: the user prompt names a Skill using `$skill-name`.
- `instrumented`: the host calls `mark_skill_invocation` with the responsible Skill.
- `inferred`: reserved in the model for analysis, but rejected at persistence time.

Only explicit and instrumented attribution can produce a stored observation. Generic tool activity is not captured. The Codex `PostToolUse` matcher is limited to the three marker tools bundled with this plugin.

DSH maps an admitted `user/message` with `source.kind = skill-invocation` to `explicit`. A successful `skill` tool call/result pair maps to `instrumented`. Failed Skill loads, generic tool activity, catalog presence, and inferred routing do not create observations.

## Privacy and consent

Installing/enabling the plugin and separately trusting its hook definition is the opt-in boundary. Hooks redact common provider keys, bearer tokens, JWTs, AWS access keys, and secret assignments before writing a draft or final observation. Raw hook payloads and transcript files are not stored.

Storage uses `$CODEX_HOME/plugin-data/skill-up-observer` so Hook and MCP processes resolve the same directory even when only hooks receive `PLUGIN_DATA`; `SKILL_UP_OBSERVER_DATA` is a development/test override. Directories use `0700`; observation and draft files use `0600`. Data remains local and no upload path exists in this implementation.

DSH storage defaults to `$DSH_HOME/plugin-data/skill-up-observer` and can be overridden by the plugin's `observer.dataDir`. DSH collection is a separate explicit configuration switch. It uses the same redaction categories and private file modes.

`UserPromptSubmit` creates a short-lived, redacted draft so later marker tools can attach attribution. `Stop` deletes the draft; the plugin also handles `Interrupt` when the Codex host exposes that hook event. If no explicit or instrumented attribution exists, no final observation is produced.

## Review and mutation boundary

New observations start as `candidate`. The plugin bundles the canonical `skill-upper` Skill to guide capture, listing, inspection, preview, approval, case creation, validation, and any separately requested evolution loop. Hooks and the `skill_up_observer` MCP server remain plugin runtime components rather than a second user-facing Skill. Listing, showing, and case preview are read-only MCP tools. A user must approve a specific observation before the write tool accepts it. Rejection is also explicit and retained as review metadata.

Case conversion:

1. creates a valid `functional_test` case that inherits the suite-level judge;
2. records the observation ID and any feedback in the description;
3. uses exclusive file creation, so an existing file is never overwritten;
4. appends the case path to the block-form `cases.files` list without rewriting unrelated YAML;
5. when `skill-up` is installed, validates the entire eval suite and rolls back both changes on failure.

The generated case is deliberately a candidate: a maintainer must add concrete expectations before using it as a release gate. The observer does not edit the Skill, run evaluations, compare reports, or publish data without a separate request. DSH provides `skill_up_compare` for an explicitly requested baseline/post-change comparison; it reads per-case business statuses from two preserved `result.json` files.

## Follow-up scope

- Add retention controls and configurable redaction policies.
- Consider promoting the observation contract into a public package only after a third host needs it.
