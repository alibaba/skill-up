# CI Maintenance

This runbook keeps repository settings aligned with the workflow files. Never put secret values, tokens, runner registration tokens, or credentials in this document.

## Trust boundaries

Pull-request code runs on GitHub-hosted runners. Persistent self-hosted runners are reserved for trusted `push`, `merge_group`, `workflow_dispatch`, and reusable-workflow executions.

| Capability | Required labels | Intended use |
| --- | --- | --- |
| Trusted Linux | `self-hosted`, `linux`, `x64`, `trusted` | Trusted integration and model-backed tests |
| Trusted Linux with Docker | `self-hosted`, `linux`, `x64`, `docker`, `trusted` | Trusted container E2E tests |
| Untrusted PR validation | `ubuntu-24.04` or `windows-2025` | Builds, lint, smoke tests, docs, and CodeQL |

Do not add `pull_request_target` to workflows that check out or execute pull-request code. Do not place a self-hosted label on PR jobs. Runner hosts are persistent: patch them regularly, remove unused software and credentials, restrict Docker access, and replace a runner after suspected compromise.

## Stable checks and merge queue

Configure rulesets using the displayed job names below. Job IDs such as `build` are implementation details and must not be entered as required-check names.

| Workflow | Events | Required checks | Optional checks |
| --- | --- | --- | --- |
| CI | `push`, `pull_request`, `merge_group` | `Build & Test`, `E2E Smoke`, `Lint` | — |
| CodeQL | `push`, `pull_request`, `merge_group`, schedule | `Analyze (actions)`, `Analyze (go)`, `Analyze (python)` | — |
| Extended CI | `merge_group`, manual | `E2E (none runtime, Windows)`, `E2E (docker runtime)`, `GoReleaser Check` | `E2E (none runtime)`, `E2E (opensandbox runtime)`, `E2E (docker runtime, full LLM)` |
| Docs | docs-related `push` and `pull_request` | Do not make globally required because path filters can leave it absent | `Build` |
| Workflow Security | workflow-related `push`, `pull_request`, `merge_group` | Keep optional while the initial baseline is triaged | `Zizmor` |

Model-backed checks depend on external services, credentials, quotas, and non-deterministic output, so they remain optional. Promote one to required only after measuring its reliability and ensuring the merge queue can access its environment and secrets.

Every workflow that supplies a required check must listen to `merge_group`; otherwise a merge queue can wait forever for a check that was never created. After renaming a job, update the repository ruleset only after the new check has completed once.

Every workflow must set top-level `permissions: {}` and grant permissions per job. Every `actions/checkout` step must set `persist-credentials: false`. Repository Actions policy should enforce full-length commit SHA references and allow only the action repositories currently used by the workflows.

Zizmor's three `adhoc-packages` findings in Extended CI are accepted low-severity exceptions: those exact-version global CLI installations are the test subjects, not application dependencies. Any High or Medium Zizmor finding must be fixed or explicitly reviewed before merge.

## Secrets and environments

Repository administrators own the following inventory. Record ownership and rotation dates in the organization's secret manager, not secret values here.

| Name | Scope | Consumer | Rotation trigger |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | Repository Actions secret | Extended model-backed E2E and self-evaluation | Provider policy, maintainer departure, or suspected exposure |
| `QODER_ACCESS_TOKEN` | Repository Actions secret | Qoder E2E | Provider policy, maintainer departure, or suspected exposure |

The `release` environment gates GitHub Releases. The `release-image` environment gates GHCR publication. Both should require reviewers, prevent self-review where team size permits, and restrict deployment to their intended tags or branches. Workflows should receive secrets only at the job that needs them.

## Runner image publication

1. Review changes under `action/`, especially the Dockerfile and downloaded binaries.
2. Dispatch **Runner Image**, approve the `release-image` deployment, and wait for build and scan results.
3. Copy the published immutable `sha256:` digest from the workflow summary.
4. In a pull request, update the runner image reference in `action.yml` to that digest.
5. Run the composite-action smoke test and Extended CI before merging.

Never silently move the image tag used by the action. The digest change must remain reviewable and reversible.

## Release procedure

1. Ensure the release commit is on `main` and CI is green.
2. Create a signed `v*` semantic-version tag covered by the tag ruleset.
3. Approve the `release` environment deployment after confirming the tag and changelog.
4. Verify the GitHub Release assets and checksums produced by GoReleaser.
5. If publication fails, fix forward and create a new version; do not rewrite a published tag.

## Artifacts and incidents

E2E artifacts can contain prompts, model responses, file paths, and generated workspaces. Keep retention short, never upload credentials, and inspect or redact artifacts before sharing them outside the maintainer group.

For a suspected secret exposure, disable the affected workflow, rotate the credential at its provider, update the GitHub secret, inspect audit and workflow logs, and invalidate affected artifacts. For a suspected runner compromise, remove the runner from GitHub, rotate credentials reachable from the host, rebuild it from a clean image, and re-register it with a new token.

Review this runbook whenever runner labels, workflow job names, required checks, environments, or secrets change.
