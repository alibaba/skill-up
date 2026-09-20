---
name: skill-up-observer
description: Instrument a current Codex Skill interaction with explicit attribution, reproducibility evidence, and user feedback for local observation. Use when the responsible Skill is known with certainty or the user asks to record feedback about the active Skill. For reviewing observations, creating regression cases, or evolving a Skill, use skill-upper.
---

# Skill Up Observer Adapter

Use this adapter only with a current Codex release that supports plugins and
lifecycle hooks. Do not attempt to retrofit it onto the pinned Codex 0.80.0
evaluation adapter.

## During a Skill interaction

1. Attribute the interaction only when the user explicitly references a Skill (for example, `$my-skill`) or when the responsible Skill is otherwise known with certainty.
2. If explicit attribution is absent but certain, call `mark_skill_invocation` once with the Skill name and optional version.
3. Use `attach_skill_evidence` only for references that help reproduce or evaluate the behavior. Prefer paths and short summaries over copied file contents.
4. Use `record_skill_feedback` when the user provides feedback. Never invent sentiment or comments.

The hooks redact common credential shapes before local persistence. Still
avoid sending secrets to the marker tools.

## Hand off review and evolution

For listing or inspecting observations, previewing a candidate, approving or
rejecting it, writing a case, validating the suite, or evolving the target
Skill, use `$skill-upper`. Its observation workflow owns the review and
mutation boundary; this adapter only supplies capture metadata and tools.

If `$skill-upper` is unavailable, explain that the companion Skill is required
for the full review-to-evaluation workflow. Do not bypass its explicit approval
gate or automatically edit, upload, run, commit, or publish anything.
