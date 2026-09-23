# From observed Skill use to a verified improvement

This example uses `code-stats` on a small project with three owned files (`README.md`, `main.go`, and `util.go`) and one dependency file (`node_modules/demo-lib/index.js`). A source code summary should count **three files** and report only `.go` and `.md` extensions. The screenshots show real DSH conversations in an isolated local project, with sidebar history excluded.

## 1. Invoke the Skill

User: “Use code-stats to summarize the files and extensions in ./project.”

With the original Skill, DSH counts **four files**. The extension table includes `.js: 1`; the scan trace identifies the extra file as `node_modules/demo-lib/index.js`.

![The request and the incorrect four-file result](../../docs/public/dsh-code-stats-before.jpg)

## 2. Review the captured observation

User: “Use the plugin's observation tools to review the code-stats run that counted four files. Check whether all counted files belong to project source, and summarize any accuracy issue with evidence. Don't edit anything.”

The plugin automatically captured the successful `skill` tool invocation, the request, and the final answer after the preceding turn. No feedback had been attached to that observation. DSH calls `list_skill_observations` and `get_skill_observation`, reads the four-file observation, checks the project files, and identifies the dependency file being counted as project code. It explains the accuracy issue without changing the Skill.

![DSH reading the captured Skill observation](../../docs/public/dsh-code-stats-observe-tools.jpg)

![DSH independently identifying the dependency-counting problem](../../docs/public/dsh-code-stats-observe.jpg)

## 3. Confirm the observation with feedback

User: “Add negative feedback to the code-stats run that counted four files: it included a dependency file. Don't change the Skill yet.”

The plugin attaches the feedback to the original four-file observation. The observation remains a `candidate`; collecting feedback does not edit the Skill or write a regression case.

![The feedback request and recorded feedback](../../docs/public/dsh-code-stats-feedback.jpg)

## 4. Improve and retry

User: “Update code-stats to ignore node_modules by default, then rerun the count for ./project.”

After the isolated demo Skill is updated, the same project returns **three files**, with no `.js` extension. The repository's [`code-stats/SKILL.md`](../../examples/code-stats/SKILL.md) also excludes `.git/`, `dist/`, and `build/` by default; users can explicitly request those directories.

![The improvement request and corrected three-file result](../../docs/public/dsh-code-stats-after.jpg)

## 5. Verify with the same regression case

User: “Compare the code-stats regression results in baseline-run and postchange-run.”

The hand-authored [`exclude-dependencies.yaml`](../../examples/code-stats/evals/cases/exclude-dependencies.yaml) creates the three owned files and one dependency file under `./project`. It requires a total of three files, `.go: 2`, `.md: 1`, and no `.js` row. With the same case and engine, the original Skill **fails** with four files; the updated Skill **passes** with three. `skill_up_compare` reads the preserved reports and confirms the case sets match.

![The comparison request and the case changing from FAIL to PASS](../../docs/public/dsh-code-stats-compare.jpg)

Observation capture is automatic; summarization starts when a user asks DSH to review observations. The plugin does not proactively send improvement suggestions. Feedback should identify the relevant run: an ambiguous reference attached to the analysis turn during a separate probe. Each evaluation phase ran once, so the results establish this run's outcome rather than model reliability. Another report-linking probe triggered an unrequested model approval call; approval enforcement is not demonstrated here. The regression case was written and reviewed by a maintainer, not automatically generated from the candidate.
