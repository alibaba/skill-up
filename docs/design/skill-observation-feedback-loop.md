# Skill observation feedback loop

Status: initial implementation (`v1alpha1`)

This design introduces a self-contained observer plugin for current Codex releases. It is the first stage of issue #255: capture attributable real-world Skill interactions, review them locally, and turn an approved observation into a candidate regression case.

## Compatibility boundary

The observer plugin targets current Codex CLI and Codex desktop releases that support plugins and lifecycle hooks. It does not target the IDE extension, which does not support plugins.

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
```

The observation schema lives in `plugins/skill-up-observer/schemas`. It describes the plugin's persisted document without adding a public skill-up CLI or Go package. Future host integrations may reuse the document shape, but do not need to share Codex hook payloads or the plugin runtime.

## Attribution

An observation records one of these attribution methods:

- `explicit`: the user prompt names a Skill using `$skill-name`.
- `instrumented`: the host calls `mark_skill_invocation` with the responsible Skill.
- `inferred`: reserved in the model for analysis, but rejected at persistence time.

Only explicit and instrumented attribution can produce a stored observation. Generic tool activity is not captured. The Codex `PostToolUse` matcher is limited to the three marker tools bundled with this plugin.

## Privacy and consent

Installing/enabling the plugin and separately trusting its hook definition is the opt-in boundary. Hooks redact common provider keys, bearer tokens, JWTs, AWS access keys, and secret assignments before writing a draft or final observation. Raw hook payloads and transcript files are not stored.

Storage uses Codex's plugin-specific `${PLUGIN_DATA}` directory; `SKILL_UP_OBSERVER_DATA` is a development/test override. Directories use `0700`; observation and draft files use `0600`. Data remains local and no upload path exists in this implementation.

`UserPromptSubmit` creates a short-lived, redacted draft so later marker tools can attach attribution. `Stop` deletes the draft; the plugin also handles `Interrupt` when the Codex host exposes that hook event. If no explicit or instrumented attribution exists, no final observation is produced.

## Review and mutation boundary

New observations start as `candidate`. The canonical `skill-upper` Skill guides listing, inspection, preview, approval, case creation, validation, and any separately requested evolution loop. The plugin-local Skill remains a thin Codex capture adapter. Listing, showing, and case preview are read-only MCP tools. A user must approve a specific observation before the write tool accepts it. Rejection is also explicit and retained as review metadata.

Case conversion:

1. creates a valid `functional_test` case that inherits the suite-level judge;
2. records the observation ID and any feedback in the description;
3. uses exclusive file creation, so an existing file is never overwritten;
4. appends the case path to the block-form `cases.files` list without rewriting unrelated YAML;
5. when `skill-up` is installed, validates the entire eval suite and rolls back both changes on failure.

The generated case is deliberately a candidate: a maintainer must add concrete expectations before using it as a release gate. The observer does not edit the Skill, run evaluations, compare reports, or publish data without a separate request.

## Follow-up scope for issue #255

- Add a DeepSeek Harness adapter after its plugin bundle lands, with equivalent normalized fixtures.
- Add retention controls and configurable redaction policies.
- Link approved cases to baseline and post-change reports.
- Produce comparison summaries without weakening existing report semantics.
