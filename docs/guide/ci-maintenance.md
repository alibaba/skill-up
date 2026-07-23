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

## Manual skill-up version synchronization

The official Action image does not track `latest`. A CLI release and an Action image refresh are two separate operations. Publishing a CLI release alone leaves the existing Action image unchanged.

The image build reads its CLI version from `ARG SKILL_UP_VERSION` in `action/Dockerfile`; the **Runner Image** dispatch `tag` controls only the GHCR tag and does not select the CLI version.

After publishing a new CLI release such as `v0.8.0`, perform the following procedure:

1. Verify that the GitHub Release exists and contains the expected archives and checksum file. The image installer downloads these release assets, so the image cannot be built before they exist.
2. Create a version-synchronization pull request that updates all three defaults to the version without the `v` prefix:
   - `action/Dockerfile`: `ARG SKILL_UP_VERSION=0.8.0`
   - `action.yml`: the `skill-up-version` fallback default
   - `action/main.py`: the `--skill-up-version` fallback default
3. If `install.sh` changed, also update the pinned installer commit and SHA-256 in both `action/Dockerfile` and `action/main.py`. Do not change these pins when the installer content is unchanged.
4. Run CI, CodeQL, Workflow Security, the Dockerfile build review, and the relevant deterministic E2E checks. Merge the synchronization pull request into `main`.
5. Dispatch **Runner Image** from `main`:
   - set `tag` to the intended image tag, normally the CLI release tag such as `v0.8.0`;
   - leave `publish_latest` disabled unless maintainers explicitly need the convenience tag;
   - remember that neither value changes `SKILL_UP_VERSION`.
6. Inspect the build log and require `skill-up --version` to report exactly `0.8.0`. Reject the `release-image` deployment if the version differs.
7. Inspect the immutable build digest, then approve the `release-image` environment. Confirm that the publish job promotes the same digest without rebuilding it.
8. Copy the published digest and create a second pull request that updates the `image: docker://...@sha256:...` reference and its version comment in `action.yml`.
9. Run the composite Action smoke test, **Extended CI**, and **Skill Upper Self-Eval** against that digest. Merge only after the expected engines complete successfully or a documented waiver is reviewed.
10. Verify the merged `action.yml`, GHCR digest, image label, and `skill-up --version` all describe the same release.

The `skill-up-version` Action input does not override the official image because `action/main.py` skips installation when the bundled binary is present. It exists only as a fallback for custom images without skill-up. Do not tell users to select a newer official CLI version through this input.

The current repository does not automatically publish a separate immutable Action release tag after the image digest pull request. Until that process exists:

- `@main` follows the latest merged Action image;
- a post-refresh commit SHA is the stable immutable Action reference;
- an existing CLI release tag must never be moved to include a later digest;
- a CLI release tag may contain the Action image that existed when the tag was created, not necessarily the CLI version named by that tag.

### Runner image release checklist

- [ ] The CLI GitHub Release and checksum file exist.
- [ ] The three skill-up version defaults match.
- [ ] Installer commit and checksum pins match the intended `install.sh` content.
- [ ] The image build log reports the expected `skill-up --version`.
- [ ] Agent CLI versions remain compatible with the new skill-up release.
- [ ] SBOM and provenance generation succeed.
- [ ] Self-Eval consumes the immutable digest, not a tag.
- [ ] `action.yml` is updated to the verified digest.
- [ ] Production `action.yml` does not reference `latest`.
- [ ] The image version, GHCR digest, Action comment, and maintenance record agree.

### Rollback

Do not rebuild or move an old digest. To roll back, create a pull request that restores the last known-good `action.yml` digest, rerun the Action smoke test, and merge through the normal ruleset. If the CLI release itself is faulty, publish a new patch release and repeat the synchronization procedure; never rewrite a published CLI tag.

## Release procedure

1. Ensure the release commit is on `main` and CI is green.
2. Create a signed `v*` semantic-version tag covered by the tag ruleset.
3. Let the unprivileged build job generate the GoReleaser files and upload the one-day release-candidate artifact.
4. Inspect the tag, commit, changelog, and build summary, then approve the `release` environment deployment.
5. Verify that the publish job attested and uploaded the exact release-candidate archives and checksums without rebuilding them.
6. If publication fails, fix forward and create a new version; do not rewrite a published tag.

After the CLI Release succeeds, continue with **Manual skill-up version synchronization**. The CLI release is not considered available through the official GitHub Action until the runner-image digest update has been tested and merged.

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
