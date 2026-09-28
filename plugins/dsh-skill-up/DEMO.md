# From observed Skill use to a verified improvement

This example uses `code-stats` on a small project with three owned files (`README.md`, `main.go`, and `util.go`) and one dependency file (`node_modules/demo-lib/index.js`). A source code summary should count **three files** and report only `.go` and `.md` extensions. The screenshots show DSH conversations in an isolated local project, with sidebar history excluded. The feedback capture below was also verified in one resumed DSH ACP session.

## 1. Invoke the Skill

User: “Use code-stats to summarize the files and extensions in ./project.”

With the original Skill, DSH counts **four files**. The extension table includes `.js: 1`; the scan trace identifies the extra file as `node_modules/demo-lib/index.js`.

![The request and the incorrect four-file result](../../docs/public/dsh-code-stats-before.jpg)

## 2. Give natural feedback

User: “That count includes node_modules. Please treat dependency files separately; do not change the Skill yet.”

The opt-in observer links this next user turn to the completed `code-stats` observation in the same session. The user does not need to supply an observation ID or ask DSH to record feedback. The stored `followups` entry retains the user's words as an **unclassified candidate**, rather than inventing a sentiment or assuming every follow-up is feedback. In the resumed-session run, the record was:

```json
{
  "id": "obs_ecda987921b963d8bca091bc",
  "skill": "code-stats",
  "turn_id": "1",
  "followups": [{ "turn_id": "2", "text": "That count includes node_modules. Please treat dependency files separately; do not change the Skill yet." }]
}
```

## 3. Review collected feedback later

User: “Review the collected code-stats feedback. Summarize the issue and suggest a Skill change with evidence. Do not edit files.”

DSH calls `collect_skill_feedback`, which returns the original request and answer together with the linked follow-up. It cites observation `obs_ecda987921b963d8bca091bc`, identifies `node_modules/demo-lib/index.js` in the four-file result, and suggests excluding dependency directories from the default source count. This is one observed issue, not evidence of a recurring pattern. Collection and review do not edit the Skill or write a regression case. A scheduler can send this review request later; scheduling is external to the plugin.

```text
Observation: 4 files, including node_modules/demo-lib/index.js
Follow-up:   "That count includes node_modules..."
Suggestion:  Exclude dependency directories from the default source count.
```

## 4. Improve and retry

User: “Update code-stats to ignore node_modules by default, then rerun the count for ./project.”

After the isolated demo Skill is updated, the same project returns **three files**, with no `.js` extension. The repository's [`code-stats/SKILL.md`](../../examples/code-stats/SKILL.md) also excludes `.git/`, `dist/`, and `build/` by default; users can explicitly request those directories.

![The improvement request and corrected three-file result](../../docs/public/dsh-code-stats-after.jpg)

## 5. Verify with the same regression case

User: “Compare the code-stats regression results in baseline-run and postchange-run.”

The hand-authored [`exclude-dependencies.yaml`](../../examples/code-stats/evals/cases/exclude-dependencies.yaml) creates the three owned files and one dependency file under `./project`. It requires a total of three files, `.go: 2`, `.md: 1`, and no `.js` row. With the same case and engine, the original Skill **fails** with four files; the updated Skill **passes** with three. `skill_up_compare` reads the preserved reports and confirms the case sets match.

![The comparison request and the case changing from FAIL to PASS](../../docs/public/dsh-code-stats-compare.jpg)

Observation and immediate same-session follow-up capture are automatic when the observer is enabled; summarization starts when a user or external scheduler asks DSH to review collected feedback. The plugin does not proactively send suggestions. Follow-ups are candidates and must be checked for relevance. Each evaluation phase ran once, so the results establish this run's outcome rather than model reliability. The regression case was written and reviewed by a maintainer, not automatically generated from the observation.
