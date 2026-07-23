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
| Extended CI | `merge_group`, manual | `Extended CI Summary` | Its component jobs are aggregated by the summary |
| Model E2E | manual | Do not make required | `E2E (none runtime, live models)`, `E2E (OpenSandbox, live model)`, `E2E (Docker runtime, live model)` |
| Docs | docs-related `push` and `pull_request` | Do not make globally required because path filters can leave it absent | `Build` |
| Workflow Security | workflow-related `push`, `pull_request`, `merge_group` | Keep optional while the initial baseline is triaged | `Zizmor` |

Model-backed checks depend on external services, credentials, quotas, and non-deterministic output, so `Model E2E` is manual and optional. Its dispatch input can run all suites or one runtime. Promote one to required only after measuring its reliability and ensuring the merge queue can access its environment and secrets.

Every workflow that supplies a required check must listen to `merge_group`; otherwise a merge queue can wait forever for a check that was never created. After renaming a job, update the repository ruleset only after the new check has completed once.

Every workflow must set top-level `permissions: {}` and grant permissions per job. Every `actions/checkout` step must set `persist-credentials: false`. Repository Actions policy should enforce full-length commit SHA references and allow only the action repositories currently used by the workflows.

The release workflow also requires `actions/download-artifact` and `actions/attest`. Add both official repositories to a restricted Actions allowlist before the first tag release; keeping their workflow references pinned to full commit SHAs is still mandatory.

Zizmor's three `adhoc-packages` findings in Model E2E are accepted low-severity exceptions: those exact-version global CLI installations are the test subjects, not application dependencies. Any High or Medium Zizmor finding must be fixed or explicitly reviewed before merge.

## Secrets and environments

Repository administrators own the following inventory. Record ownership and rotation dates in the organization's secret manager, not secret values here.

| Name | Scope | Consumer | Rotation trigger |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | Repository Actions secret | Model E2E and self-evaluation | Provider policy, maintainer departure, or suspected exposure |
| `QODER_ACCESS_TOKEN` | Repository Actions secret | Qoder E2E | Provider policy, maintainer departure, or suspected exposure |

The `release` environment gates the job that publishes already-built GoReleaser artifacts. The `release-image` environment gates promotion of an already-built image digest to user-facing GHCR tags. Compilation happens before approval with read-only source access; approval never triggers a rebuild. Both environments should require reviewers, prevent self-review where team size permits, and restrict deployment to their intended tags or branches. Workflows should receive secrets only at the job that needs them.

## Runner image publication

1. Review changes under `action/`, especially the Dockerfile and downloaded binaries.
2. Dispatch **Runner Image** and inspect the build job's immutable digest.
3. Approve the `release-image` deployment. The publish job promotes that exact digest to the requested tag without rebuilding it.
4. Copy the published immutable `sha256:` digest from the workflow summary.
5. In a pull request, update the runner image reference in `action.yml` to that digest.
6. Run the composite-action smoke test and Extended CI before merging.

The build job pushes a run-scoped `build-<run-id>-<attempt>` staging tag so the protected job can promote the exact registry object. Periodically remove unreferenced staging versions according to the package retention policy. Never silently move the image tag used by the action. The digest change must remain reviewable and reversible. Reusable callers consume the workflow's digest output, not the mutable tag.

## Release procedure

1. Ensure the release commit is on `main` and CI is green.
2. Create a signed `v*` semantic-version tag covered by the tag ruleset.
3. Let the unprivileged build job generate the GoReleaser files and upload the one-day release-candidate artifact.
4. Inspect the tag, commit, changelog, and build summary, then approve the `release` environment deployment.
5. Verify that the publish job attested and uploaded the exact release-candidate archives and checksums without rebuilding them.
6. If publication fails, fix forward and create a new version; do not rewrite a published tag.

## Cache policy

- Go module caches are enabled only for deterministic validation jobs. Pull-request caches are treated as untrusted performance hints: jobs must still verify and compile all inputs, and they receive no secrets or write token.
- GoReleaser snapshot and production builds disable the Actions cache so release output does not consume cache entries writable by other jobs.
- Runner-image builds do not use the GitHub Actions cache backend; a cache service outage must not fail a publication after registry writes begin.
- Caches are disposable and must never carry reports, release files, credentials, or state required by a later job. Use uploaded artifacts for job-to-job transfer and pin retention explicitly.
- When changing a cache key, dependency manager, lockfile, or trust boundary, review both restore sources and write permissions before enabling it.

## Artifacts and incidents

E2E artifacts can contain prompts, model responses, file paths, and generated workspaces. Keep retention short, never upload credentials, and inspect or redact artifacts before sharing them outside the maintainer group.

For a suspected secret exposure, disable the affected workflow, rotate the credential at its provider, update the GitHub secret, inspect audit and workflow logs, and invalidate affected artifacts. For a suspected runner compromise, remove the runner from GitHub, rotate credentials reachable from the host, rebuild it from a clean image, and re-register it with a new token.

Review this runbook whenever runner labels, workflow job names, required checks, environments, or secrets change.
