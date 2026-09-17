# `@alibaba/dsh-skill-up`

Experimental DeepSeek Harness (DSH) plugin for testing and iteratively improving
existing Agent Skills with `skill-up` and the bundled `skill-upper` Skill.

The intended workflow starts from a real new input or scenario:

1. Add or refine a regression case for the existing Skill.
2. Run a baseline before changing the Skill.
3. Improve the Skill from observed failures.
4. Rerun the same case and inspect per-case status in `result.json`.
5. Preserve the before/after reports as evidence.

The plugin grounds each improvement in gaps revealed by the supplied scenario
and verifies case status instead of treating process exit alone as proof.

## Install from a checkout

Prerequisites:

- `dsh` 0.1.5-rc.2 or newer
- `skill-up` available on `PATH`
- `pnpm` available on `PATH` for `dsh plugin`

From the repository root:

```bash
cd plugins/dsh-skill-up
npm install --no-package-lock
npm pack
dsh plugin --profile web add ./alibaba-dsh-skill-up-0.1.0-alpha.1.tgz
dsh --profile web --dump-config
```

Packing first materializes the bundled `skill-upper` files and tests the package.
The config dump should then contain the `skill-up` row from this bundle. The
package is not published to npm yet; after publication, installation becomes:

```bash
dsh plugin --profile web add @alibaba/dsh-skill-up
```

## Capabilities

- Bundles the repository's canonical `skill-upper` Skill at package time.
- `skill_up_validate` validates a workspace-relative evaluation suite.
- `skill_up_run` starts an evaluation through DSH's background-job service.
- Each run gets an isolated report directory under `.skill-up-workspace/`, so
  concurrent jobs and before/after evidence cannot overwrite one another.
- `skill_up_summary` reads the per-case statuses in an existing `result.json`.
- Evaluation arguments are passed as an argv array, not interpolated into a shell.
- Eval and result paths are confined to the DSH workspace, including symlink resolution.

Use the existing `job_output`, `job_list`, and `job_kill` tools to observe or
cancel a run started by `skill_up_run`.

## Configuration

The bundle inserts the plugin with safe defaults. Override the row in the
profile's `cordis.patch.yml` when needed:

```yaml
- id: skill-up
  name: '@alibaba/dsh-skill-up'
  config:
    skillUpBin: skill-up
    credentialEnv:
      - OPENAI_API_KEY
      - ANTHROPIC_API_KEY
```

`credentialEnv` is empty by default. DSH deliberately scrubs credential-shaped
environment variables from subprocesses; listing a name explicitly opts that
value into the `skill-up` child without logging it. Prefer skill-up's credential
file or an already authenticated Agent CLI when possible.

`skill-up run` may execute Agent CLIs, judge scripts, and other code declared by
the selected eval suite. Only run suites you trust. The plugin confines selected
input and report paths to the workspace but does not turn an untrusted eval suite
into trusted code.
