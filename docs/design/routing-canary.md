# Routing canary: what Skill activation evidence do engines actually expose?

This document is the first milestone of
[#166](https://github.com/alibaba/skill-up/issues/166): an executable, repeatable
investigation into whether an engine exposes native evidence that it selected,
loaded, or applied a Skill.

It deliberately does **not** introduce `SkillActivation`, an engine capability
flag, or a `skill_activated` assertion. Those should be derived from the evidence
below, in a second change, once we agree on what each engine's events mean.

The canary itself lives in [`examples/routing-canary/`](../../examples/routing-canary/README.md)
and is run with `scripts/routing-canary/run.sh`.

## Method

Two Skills with non-overlapping triggers are installed for every case:
`canary-routing` ("the canary routing code word") and `canary-decoy` ("translate
French poetry"). Three cases probe the routing decision:

| Case | Request | Expected workspace evidence |
| --- | --- | --- |
| `relevant` | ask for the canary code word | `.canary-relevant` written, `.canary-decoy` absent |
| `unrelated` | `17 * 23` | neither marker written |
| `competing` | French poetry translation | `.canary-decoy` written, `.canary-relevant` absent |

Each Skill body instructs the model to write a marker file before replying, and
the case expectations check those files. A green case therefore means the Skill
instructions were applied to the workspace, not just that the final answer
resembled the expected text. The judge only checks the reply.

The response is not the evidence we are looking for. The transcripts are: the
canary records which events each engine writes when its router/loader touches a
Skill.

Pinned environment for the runs below:

| Engine | Version | Runtime | Model |
| --- | --- | --- | --- |
| `claude_code` | 2.1.317 | `environment.type: none` | engine default (MiniMax-compatible endpoint) |
| `codex` | 0.166.0 | `environment.type: none` | engine default (`deepseek/deepseek-v4.1-flash`) |
| `opencode` | 1.19.0 | not verified | — |

The model is not part of the canary contract: routing is a model behavior, so a
canary result is only comparable at a fixed engine version **and** model. The
versions above were the ones installed on the machine that ran the investigation;
the model is reported because it affects the outcome, not because the canary
pins it.

## Results

| Engine | Relevant | Unrelated | Competing | Runs |
| --- | --- | --- | --- | --- |
| `claude_code` | PASS | PASS | PASS | 2 |
| `codex` | PASS | PASS | PASS | 2 |
| `opencode` | not run | not run | not run | 0 |

`opencode` could not be measured on this machine: the engine refuses to start
because the user-level `~/.config/opencode/opencode.json` contains V2 permission
rules that OpenCode V1 rejects (`V2 permissions are not supported by OpenCode
V1`). Verifying OpenCode needs an isolated config directory; that is left as the
next measurement.

Both engines pass by *application* evidence (the marker files), not by text
similarity. That is the outcome half of the question. The observability half is
the next section.

## Capability matrix

| Engine | Native evidence | Strongest claim it supports | Identity in the evidence |
| --- | --- | --- | --- |
| `claude_code` | Structured `Skill` tool call with a result | **loaded / invoked** — the engine reports launching a named Skill body | Install directory name (`skill`, `decoy`), not the frontmatter `name` |
| `codex` | No Skill event; the session starts with an injected `<skills_instructions>` catalog, and activation appears as an explicit file read | **loaded** (weaker) — the model opened `SKILL.md`; no engine-level selection confirmation | Catalog lists the frontmatter `name` plus the install path |
| `opencode` | Unknown | — | — |

### claude_code

The transcript contains a real tool call and its result:

```json
{"type": "tool_use", "name": "Skill", "input": {"skill": "skill", "args": "canary routing code word"}}
{"type": "tool_result", "content": "Launching skill: skill"}
```

That is engine-native: the name comes from the tool call, not from the model's
prose, and the paired result confirms the engine acted on it. In an early run
with a different fixture layout, the model first tried a name that was not
installed, and the transcript recorded the failed attempt before the successful
one — so the event stream also exposes mistakes, which is valuable for negative
cases.

Two gaps remain:

- The claim is "the engine launched this Skill body", not "the model followed
  it". Application is only shown by the marker files.
- The name in the event is the **install directory name**, while `SKILL.md`
  declares a different `name` (`canary-routing`). A normalized assertion would
  need a mapping from install identity to declared identity; that is the same
  problem addressed in #367/#393/#400.

### codex

No Skill tool call exists. Instead the session begins with a developer message
that contains the installed Skill catalog:

```text
<skills_instructions>
### Skill roots
- `r0` = <workspace>/.codex/skills
- `r1` = /Users/<user>/.agents/skills
- `r2` = <CODEX_HOME>/skills/.system
### Available skills
- canary-decoy: Use when the user asks to translate Frenc (file: r0/decoy/SKILL.md)
- canary-routing: Use when the user asks for the canary r (file: r0/skill/SKILL.md)
...
```

Activation is then observable only as an ordinary command call that reads the
body:

```json
{"type": "function_call", "name": "exec_command", "arguments": "{\"cmd\": \"cat .../.codex/skills/skill/SKILL.md\"}"}
```

This is weaker than the Claude Code event. It proves the file was opened; it does
not prove the engine's router selected the Skill, nor that the complete body was
loaded in one step. For a routing assertion it would have to be combined with
the injected catalog and the behavior markers — which is exactly the kind of
"looks like routing" inference that #166 asks us not to promote to an assertion
prematurely.

## Confounders found while measuring

These are observations about the environment, not canary failures. Each one
matters if this becomes a CI signal.

1. **Engine-global Skill roots leak into the evaluation.** Codex injected the
   user's global roots into the catalog in the same session: `r1 =
   ~/.agents/skills` (with 100+ unrelated Skills), plus `.system` skills from
   `CODEX_HOME`. On the first run the user's own `gstack/canary` Skill was in
   that catalog, and the canary prompt is about a "canary". Claude Code
   transcripts likewise show shell exploration of `~/.claude/skills`. A routing
   canary must be run with engine skill roots quarantined, or its result is
   polluted. `CODEX_HOME` isolates part of Codex's roots but does not remove
   `~/.agents/skills` (observed).
2. **Install identity is the directory name on one engine and the declared name
   on the other.** Claude Code's `Skill` call uses `skill`/`decoy` (directory
   names); Codex's catalog uses `canary-routing`/`canary-decoy` (frontmatter
   names) while pointing at the same directories. A normalized `SkillActivation`
   must not compare these two strings directly.
3. **Nested Skill directories are installed recursively.** A first fixture put
   the decoy inside the root Skill directory; both Codex and Claude Code then
   registered the decoy twice (once nested, once flat), because `skills[].path`
   is copied recursively. The fixture in `examples/routing-canary/` keeps the two
   Skills side by side for that reason.
4. **Routing is nondeterministic at the margin.** The installed-name retry in the
   early Claude Code run is one example; the unrelated and competing cases are
   there to catch the other direction (no activation on a non-match, and the
   right Skill when two are present). Repeated runs (`ITERATIONS=3`) are
   supported but have not been recorded yet.

## What this does not show

- It does not show "applied" for any engine; only the workspace markers do, and
  they are behavior, not activation evidence.
- It does not compare with-Skill and without-Skill outcomes, so it cannot
  separate "routed and followed" from "would have done it anyway".
- It does not cover OpenCode, or Windows, or sandboxed runtimes.

## Recommendation for the next change

1. Treat the Claude Code `Skill` tool call as the only engine-native activation
   evidence observed so far, and only at the **invoked/loaded** level. Pair it
   with behavior assertions before calling anything "applied".
2. Treat Codex as **not exposing activation evidence** for now. Parsing a
   `SKILL.md` read as activation would encode the weak inference #166 warns
   about; a capability flag should say unsupported rather than silently pass.
3. Measure OpenCode in an isolated config directory before designing anything
   for it.
4. Then derive `SkillActivation`, the capability flag and `skill_activated` from
   the actual event contracts, with unsupported engines reported explicitly
   (not skipped green).
