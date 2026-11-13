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
| `canary-decoy` | asks to translate French poetry into English |

Three cases probe the routing decision from both sides:

| Case | Request | Expected workspace evidence |
| --- | --- | --- |
| `relevant` | the canary code word | `.canary-relevant` written, `.canary-decoy` absent |
| `unrelated` | `17 * 23` | neither marker written |
| `competing` | French poetry translation | `.canary-decoy` written, `.canary-relevant` absent |

Each Skill body writes a marker file before replying. The case expectations
check those files, so a green result means the Skill instructions were followed
rather than merely echoed in the final answer. The judge only checks the reply
text.

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
- Engines also load their own global Skills (`~/.claude/skills`,
  `~/.agents/skills`, `$CODEX_HOME/skills`, ...). Those entries appear in the
  same selection surface and can capture a canary prompt. See the investigation
  report for the observed leakage and the temporary workarounds.
