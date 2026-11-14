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
`canary-routing` ("the canary routing code word") and `literary-translation`
("translate French poetry or literary prose"). Three cases probe the routing decision:

| Case | Request | Expected workspace evidence |
| --- | --- | --- |
| `relevant` | ask for the canary code word | `.canary-relevant` written, `.translation-done` absent |
| `unrelated` | `17 * 23` | neither marker written |
| `competing` | French poetry translation | `.translation-done` written, `.canary-relevant` absent |

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

Three iterations per engine (`ITERATIONS=3`), each iteration running all three
cases in a fresh workspace:

| Engine | Relevant | Unrelated | Competing | Iterations |
| --- | --- | --- | --- | --- |
| `claude_code` | 3/3 PASS | 3/3 PASS | **1/3 PASS** | 3 |
| `codex` | 3/3 PASS | 3/3 PASS | 3/3 PASS | 3 |
| `opencode` | not run | not run | not run | 0 |

`opencode` could not be measured on this machine: the engine refuses to start
because the user-level `~/.config/opencode/opencode.json` contains V2 permission
rules that OpenCode V1 rejects (`V2 permissions are not supported by OpenCode
V1`). Verifying OpenCode needs an isolated config directory; that is left as the
next measurement.

These numbers are *application* evidence (the marker files), not text
similarity. Two results deserve attention rather than being averaged away:

- The `claude_code` competing case is flaky on application while its routing
  decision is stable: in all three iterations the transcript contains a
  `Skill` call for `translator`, but only iteration 1 also wrote
  `.translation-done`. In iteration 3 the model answered the translation first
  and then explained that it did not follow the marker instruction because the
  instruction looked artificial. Loading the right Skill and obeying it are
  different measurements.
- An earlier fixture named the second Skill `canary-decoy`. With that name the
  competing case failed once out of three runs because the model read the name
  from the catalog and answered that it was "a canary/decoy entry rather than a
  real translation skill", so it translated directly. The fixture now uses a
  neutral `literary-translation` name; the canary result must not leak what it
  expects.

The observability half of the question is the next section.

## Capability matrix

| Engine | Native evidence | Strongest claim it supports | Identity in the evidence |
| --- | --- | --- | --- |
| `claude_code` | Structured `Skill` tool call with a result | **loaded / invoked** — the engine reports launching a named Skill body | Install directory name (`skill`, `translator`), not the frontmatter `name` |
| `codex` | No Skill event; the session starts with an injected `<skills_instructions>` catalog, and activation appears as a command that references the Skill file | **loaded** (weaker) — the command opened `SKILL.md`; no engine-level selection confirmation | Catalog lists the frontmatter `name` plus the install path |
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

Loading is also not the same as obeying. In the neutral-fixture runs the model
called `Skill {"skill": "translator"}` in every competing iteration, but only
one of three wrote the marker and replied with the token the Skill body asked
for. The tool event is still valid routing evidence; the marker is what turns it
into an application claim, and that second step is where the flakiness appeared.

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
- literary-translation: Use when the user asks to translate Frenc (file: r0/translator/SKILL.md)
- canary-routing: Use when the user asks for the canary r (file: r0/skill/SKILL.md)
...
```

Activation is then observable only as an ordinary command call that references
the body:

```json
{"type": "function_call", "name": "exec_command", "arguments": "{\"cmd\": \"cat .../.codex/skills/skill/SKILL.md\"}"}
```

This is weaker than the Claude Code event. The call names the file, and the
engine's result shows the command succeeded, but the summary deliberately does
not infer more than that: the same "SKILL.md in the payload" signal is also
produced by a listing or a failed read. Confirming a read needs the paired tool
result, which is engine specific and out of scope for the schema-agnostic
summary. Nor does this prove the engine's router selected the Skill, or that the
complete body was loaded in one step. For a routing assertion it would have to be combined with
the injected catalog and the behavior markers — which is exactly the kind of
"looks like routing" inference that #166 asks us not to promote to an assertion
prematurely.

## Reading the summary

`scripts/routing-canary/summarize.py` prints one row per case and keeps cases
whose transcript is missing instead of dropping them, so "no transcript" cannot
be mistaken for "no activation":

| Column | Meaning |
| --- | --- |
| `execution` | Case status from the iteration `result.json` (`PASS`/`FAIL`/`ERROR`), independent of transcript availability |
| `graded` | Grading outcome from the case `grading.json` |
| `transcript` | `present` or `missing` |
| `skill tool call` | Tool names in the transcript that look like a Skill tool (e.g. Claude Code's `Skill`) |
| `skill file reference` | A tool-call payload contains `SKILL.md`. A listing or failed read also matches, so this is a reference, not a confirmed read |
| `markers` | Canary marker files referenced by a call |
| `catalog` | The injected `<skills_instructions>` catalog is present |

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
   on the other.** Claude Code's `Skill` call uses `skill`/`translator` (directory
   names); Codex's catalog uses `canary-routing`/`literary-translation`
   (frontmatter names) while pointing at the same directories. A normalized `SkillActivation`
   must not compare these two strings directly.
3. **Nested Skill directories are installed recursively.** A first fixture put
   the second Skill inside the root Skill directory; both Codex and Claude Code then
   registered the second Skill twice (once nested, once flat), because `skills[].path`
   is copied recursively. The fixture in `examples/routing-canary/` keeps the two
   Skills side by side for that reason.
4. **Application compliance is nondeterministic where the task is answerable
   without the Skill.** With three iterations on the final fixture, Claude Code
   routed the competing case correctly 3/3 (a `translator` `Skill` call in every
   transcript) but applied the marker instruction only 1/3; the model translated
   the line itself and said the instruction looked artificial. The relevant case
   (where the code word cannot be guessed) applied 3/3. So a marker-based PASS
   measures routing *and* instruction compliance together, and the second part
   depends on whether the request can be satisfied without loading the Skill.
   Codex applied 3/3 on all cases.
5. **The fixture name leaks the expected answer.** Naming the second Skill
   `canary-decoy` was visible to the model through the catalog; one run answered
   that it was "a canary/decoy entry rather than a real translation skill" and
   skipped it. The fixture now uses `literary-translation`. Any Skill the canary
   expects the model to *use* must not be named like an experiment.

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
4. Keep routing evidence and application evidence as separate assertions. A
   marker file is only decisive for a request the model cannot answer without
   the Skill (the `relevant` case); for a competing case whose task is
   natively answerable it conflates "routed" with "chose to obey".
5. Then derive `SkillActivation`, the capability flag and `skill_activated` from
   the actual event contracts, with unsupported engines reported explicitly
   (not skipped green).
