# Routing canary

This example is the reproducible part of the engine capability investigation in
[#166](https://github.com/alibaba/skill-up/issues/166). It answers a narrow
question: **what native evidence does each engine emit when it selects, loads,
or applies a Skill?**

It does not add a `skill_activated` assertion or an engine capability flag. It
only produces raw transcripts that the investigation report
([`docs/design/routing-canary.md`](../../docs/design/routing-canary.md)) reads.

## What it runs

Two Skills are installed per run:

| Skill | Trigger |
| --- | --- |
| `canary-routing` | asks for "the canary routing code word" |
| `literary-translation` | asks to translate French poetry into English |

Three cases probe the routing decision from both sides:

| Case | Request | Expected workspace evidence |
| --- | --- | --- |
| `relevant` | the canary code word | `.canary-relevant` written, `.translation-done` absent |
| `unrelated` | `17 * 23` | neither marker written |
| `competing` | French poetry translation | `.translation-done` written, `.canary-relevant` absent |

Each Skill body writes a marker file before replying. The case expectations
check those files, so a green result means the Skill instructions were applied
to the workspace rather than merely echoed in the final answer. The judge only
checks the reply text.

A marker only measures "routed **and** obeyed". On a request the model can
answer without the Skill (the competing translation case) it may load the right
Skill and then skip the marker instruction, which reads as a routing failure
even though the transcript shows the correct `Skill` call. Run with
`ITERATIONS=3` before reading anything into a single pass; the investigation
report separates routing evidence from application evidence.

## Run it

Build the pinned binary first, then run the canary for the engines you have
installed:

```bash
scripts/routing-canary/run.sh                 # claude_code, codex, opencode
scripts/routing-canary/run.sh claude_code     # one engine
ITERATIONS=3 scripts/routing-canary/run.sh    # repeat for stability
```

The script copies this directory to a temporary location so evaluation
artifacts do not land inside the repository, runs `skill-up run` with
`--no-delete`, and copies the artifacts to `out/<engine>/` in the current
directory. `scripts/routing-canary/summarize.py out/<engine>` prints the
Skill-related events found in each transcript.

`CANARY_MODEL` pins the model for engines that need it:

```bash
CANARY_MODEL=opencode/deepseek-v4.1-flash-free \
  XDG_CONFIG_HOME=/tmp/oc-cfg XDG_DATA_HOME=/tmp/oc-data \
  scripts/routing-canary/run.sh opencode
```

OpenCode reads its user-level `~/.config/opencode/opencode.json` and refuses to
start when that file contains V2 permission rules
(`V2 permissions are not supported by OpenCode V1`). Point `XDG_CONFIG_HOME` and
`XDG_DATA_HOME` at temporary directories (copy `opencode/auth.json` into the
data directory to keep the login) and pin a model, otherwise `opencode run`
fails with `Model is unavailable`. The captured run used
`opencode/deepseek-v4.1-flash-free`.

The script replaces `out/<engine>/` on every run (so an older `iteration-2/`
cannot be read as part of the latest result) and exits nonzero if an engine
failed or produced no artifacts. The summary keeps cases whose transcript is
missing, reporting them as `transcript missing` next to the execution status
from `result.json`. The `skill file reference` column is deliberately weaker
than "read": a directory listing or a failed `cat` also contains `SKILL.md`.

The versions used for the investigation were:

| Engine | Version | Runtime |
| --- | --- | --- |
| `claude_code` | 2.1.317 | `environment.type: none` |
| `codex` | 0.166.0 | `environment.type: none` |
| `opencode` | 1.19.0 | `environment.type: none` |

Record the model each engine used next to the results; routing is a model
behavior and the canary is not meaningful across unknown models.

## Notes

- Keep the two Skill directories flat. `skills[].path` is copied recursively, so
  a Skill directory that contains another `SKILL.md` registers the nested one as
  a second entry with the same declared name.
- Do not name a Skill after what the experiment expects. A Skill named
  `canary-decoy` was visible in the catalog and one run answered that it was a
  decoy rather than a real Skill, so it never applied. The fixture uses neutral
  names (`canary-routing` for the code-word arm is unavoidable, but the other
  arm is `literary-translation`).
- Engines also load their own global Skills (`~/.claude/skills`,
  `~/.agents/skills`, `$CODEX_HOME/skills`, ...). Those entries appear in the
  same selection surface and can capture a canary prompt. See the investigation
  report for the observed leakage and the temporary workarounds.
