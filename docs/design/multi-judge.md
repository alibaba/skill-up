# Independent multi-judge evaluation

Status: implemented in PR #290 for the `none` runtime, with internal extension
interfaces. Numerical scores, configurable policies, validator bundles, and
additional runtime adapters are future work.

Related issue: [#246](https://github.com/alibaba/skill-up/issues/246).
Usage: [Writing evals](../guide/writing-evals.md#multiple-independent-judges).

## Problem and goals

A generated change may need deterministic validation and semantic review.
Running the evaluated agent twice changes the input being evaluated. Asking an
Agent judge to run a script also leaves script execution and interpretation under
that agent's control: it does not inherit the framework's script exit-code verdict,
timeout handling, or independent attribution.

Run the evaluated agent once, then preserve an independent verdict for each named
judge. A script failure must remain visible even if semantic review passes. Extra
Agent criteria must not silently increase that judge's weight in the case verdict.

## Configuration and compatibility

The snippet uses the OpenCode/DashScope engine and endpoint configuration from
the [code-stats example](../../examples/code-stats/README.md). An Agent judge
inherits that engine and endpoint and requires an explicit model.

```yaml
judges:
  - id: functional
    type: script
    script_path: evals/scripts/check.sh
  - id: semantic
    type: agent_judge
    model: dashscope/qwen3.8-max
    criteria:
      - The change addresses the request without unrelated modifications.
    pass_threshold: 1.0
```

Each member has a unique ID. Eval-level lists supply defaults; a case-level list
replaces the entire list. A case-level singular `judge` also replaces an inherited
list. A document cannot contain both `judge` and `judges`. Legacy singular configs
retain their execution and reporting behavior. `expect` is a separate hard gate.
No public aggregation-policy configuration is introduced by this implementation.

## Execution and isolation

1. Resolve and validate the judge plan; reject runtimes lacking snapshot capability
   before invoking the evaluated agent.
2. Execute the evaluated agent and run `expect` gates. A failed gate skips all
   members and produces a failed case with gate evidence.
3. Capture the completed workspace once.
4. Run each member sequentially in its own fork of that snapshot, using the same
   agent transcript and execution metadata. A completed member's FAIL or ERROR
   does not prevent later members from running. Case cancellation skips pending
   members; member deadlines are bounded by the case deadline.
5. Preserve member outcomes, aggregate the case, and clean up forks and snapshot.

List order is execution order, not a dependency graph. Member output is not passed
to another member. Judges can execute additional review-agent sessions; the
original evaluated-agent execution is not repeated.

`runtime.JudgeSnapshotProvider` is an optional capability alongside `Runtime`.
It captures a `runtime.JudgeSnapshot`, which provides `Fork` and `Close`. A fork
must be ready to use, isolated from sibling forks, the snapshot, and the original
workspace; the caller owns its cleanup. A future runtime implements this contract
without requiring concrete-type dispatch in the evaluator.

Only `NoneRuntime` implements it today. It copies the full workspace to temporary
storage and creates a new copy for each member. Internal relative symlinks are
preserved, as are regular-file/directory modification times and permission bits
(the workspace root stays private with mode 0700). Absolute or escaping links
and special files are rejected. Copying
observes cancellation. This isolates workspace file changes, not host processes,
network access, credentials, or effects on external services. Snapshot failure
produces ERROR with skipped members. Temporary snapshots are deleted after use;
this is not a durable replay or regrading feature.

## Verdict and result contracts

`judge.OutcomeAggregator` separates policy from execution. Its `Strategy` names
the policy and `Aggregate` combines leaf outcomes into compatibility grading.
`DefaultOutcomeAggregator` selects `AllRequiredAggregator`; the evaluator records
that strategy and the report uses it rather than inventing its own policy name.
A gate failure retains its own case status instead of aggregating skipped members.

The current policy is `all_required`:

- PASS: every executed member passes.
- FAIL: at least one member fails and no member has ERROR or SKIP.
- ERROR: at least one member errors or is skipped after judging begins.

Each member contributes one assertion to compatibility grading. Leaf criteria
remain in that member's result, including permitted failed criteria when an Agent
judge meets its `pass_threshold`. Script exit 0 means PASS; nonzero means FAIL;
execution failures and strict multi-judge timeouts mean ERROR.

`evaluation.json` version 1 is the canonical grouped artifact: `gates`,
`judge_results`, and `aggregation` (strategy and status). Members retain ID, type,
status, result/evidence, errors, skip reason, duration, and artifact references.
`grading.json` is a compatibility projection; its pass rate is the fraction of
passing member verdicts, not an arbitrary numerical quality score. Reports retain
the group structure. Existing benchmark summaries remain based on case verdicts.

## Comparison with Harbor

Reference: Harbor main at
[`58789aad577f432a4590209a8fd901f8c894f382`](https://github.com/harbor-framework/harbor/tree/58789aad577f432a4590209a8fd901f8c894f382).
This comparison is based on source inspection, not runtime experiments.

| Layer | Harbor | skill-up in this PR |
| --- | --- | --- |
| Evaluation output | Named numerical rewards | Named judge verdicts and evidence |
| Default script protocol | `reward.json` or `reward.txt` | Exit code and diagnostic output |
| Attribution | Reward map itself contains numerical values | Each judge has execution status and artifacts |
| Aggregation | Metrics across trial rewards; separate multi-step policy | `all_required` within one case |
| Custom implementation | Imported verifier class | Built-in judge factory; scripts as external validators |
| Environment | Abstract environment; shared or separate verifier | Optional snapshot capability; `none` adapter |
| Supporting files | Tests directory upload or image-owned tests | Script entry-path reuse; no validator bundle contract |

Harbor's [default verifier](https://github.com/harbor-framework/harbor/blob/58789aad577f432a4590209a8fd901f8c894f382/src/harbor/verifier/verifier.py)
reads numerical rewards after script execution. Multiple reward keys do not by
themselves represent independently executed judges with individual deadlines and
errors. Its [verifier factory](https://github.com/harbor-framework/harbor/blob/58789aad577f432a4590209a8fd901f8c894f382/src/harbor/verifier/factory.py)
also accepts custom verifier classes.

Harbor's [metric aggregation](https://github.com/harbor-framework/harbor/blob/58789aad577f432a4590209a8fd901f8c894f382/src/harbor/metrics/base.py)
aggregates across trial reward maps, treating missing maps or keys as zero. Its
[multi-step aggregation](https://github.com/harbor-framework/harbor/blob/58789aad577f432a4590209a8fd901f8c894f382/src/harbor/trial/trial.py#L790)
supports per-key mean or final-step reward; steps without verifier results are
excluded from the mean denominator. These policies are distinct from independent
judges over one agent execution. A separate verifier environment receives collected
artifacts rather than an automatic copy of the complete agent workspace.

## Future extension boundaries

The following are proposals, not supported YAML or result fields:

1. **Numerical judge scores.** Add optional named finite scores alongside verdicts
   and evidence, with explicit scale and direction. Keep execution errors distinct
   from legitimate zero scores and keep assertion pass rates unchanged. Specify
   result-version compatibility before extending serialized artifacts.
2. **Case policies.** Select an `OutcomeAggregator` through a validated configuration
   contract when concrete use cases require weights, thresholds, or required versus
   optional judges. Define ERROR, SKIP, missing-score and denominator semantics;
   never infer weights from the number of leaf criteria. Update report and benchmark
   consumers with the policy, not just the evaluator.
3. **Benchmark metrics.** Aggregate numerical dimensions across cases and repeated
   runs separately from case acceptance. Define missing-value and failure policies
   explicitly rather than copying Harbor's zero substitution implicitly.
4. **Runtime adapters and declared inputs.** Implement snapshot/fork for another
   runtime only with evidence that sibling judges cannot alter each other's inputs.
   Artifact-only verification can be a separate input contract. Durable regrading
   additionally needs retained inputs, configuration and validator identities.
5. **Reusable validator bundles.** Extend script-path reuse with supporting modules,
   data, explicit parameters, dependency/environment requirements, and versioned
   JSON input/output. Retain exit-code compatibility. Directory packaging and
   installation/readback must be verified together.
6. **External judge adapters.** Prefer a versioned process protocol when an external
   implementation is needed; adding a Go interface does not create a public plugin
   ABI. The current judge and runtime types remain internal.

## Validation boundaries

Tests cover independent workspace forks, one evaluated-agent execution, gate
short-circuiting, timeout/error handling, unsupported runtimes, grouped reports,
and compatibility grading. A runtime wrapper exercising the snapshot capability
proves dispatch does not require a concrete `NoneRuntime`. Aggregation tests cover
status precedence and preservation of member weight.

Docker/OpenSandbox multi-judge execution, numerical scoring, weighted policies,
validator bundles, and durable replay are not validated or claimed by this PR.

### Live Qwen verification

On 2026-09-30, the existing [code-stats example](../../examples/code-stats/README.md)
was run through OpenCode 1.14.24 with `dashscope/qwen3.8-max`, using DashScope's
OpenAI-compatible endpoint. These were real model calls, in addition to the
mocked-agent regression tests above. The model identity is the requested and
applied client configuration; the provider's serving implementation was not
independently audited.

The selected `analyze-directory` case reuses the existing `check-stats.sh` without
modifying it and adds an independent semantic judge. The fixture contains three
specified files, totaling 20 lines and 350 bytes. Tool configuration and installed
Skill files are excluded from that scope.

| Run | Expect gates | Format script | Semantic Agent | Aggregate |
| --- | --- | --- | --- | --- |
| With Skill | PASS | PASS | PASS | PASS |
| Without Skill baseline | FAIL | SKIP | SKIP | FAIL |
| Controlled script failure, with Skill | PASS | FAIL | PASS | FAIL |

The positive run reported the expected file, line and byte counts. The baseline
failed the exact `Total Files: 3` and `Total Lines: 20` strings; this is a formatting
gate result, not evidence that the model cannot calculate those values. Gate
failure correctly skipped both judges.

For the controlled negative run, a temporary copy of the example disabled the
baseline and changed only the script's final success path to write a
`.judge-isolation-probe` file and exit 1. An extra semantic criterion required the
probe to be absent. The Agent judge's recorded directory checks found no probe in
its workspace; it continued and passed, while the script retained FAIL and
`all_required` returned FAIL. This verifies workspace mutation isolation and
failure attribution with a real Agent judge. It does not claim host or network
isolation.

Both runs preserved `evaluation.json`, compatibility `grading.json`, agent and
judge transcripts, and the semantic context manifest. Included context paths were
resolved against the archived outputs and read back successfully. JSON, HTML and
JUnit reports were generated for the positive run.

To repeat the positive run, inject `DASHSCOPE_API_KEY` securely, then run from the
repository root:

```bash
make build
./bin/skill-up validate examples/code-stats/evals/eval.yaml
./bin/skill-up run examples/code-stats/evals/eval.yaml \
  --include-case-name analyze-directory --iteration 1 --parallelism 1 \
  --output-dir /tmp/code-stats-qwen-positive \
  --event-log /tmp/code-stats-qwen-positive-events.jsonl \
  --format json --format html --format junit
```

To reproduce the negative test, copy `examples/code-stats` to a scratch directory,
set `benchmark.enabled: false`, replace the final `exit 0` in the copied
`check-stats.sh` with the following, and add the probe-absence criterion to the
copied case's semantic judge:

```bash
printf 'script-only mutation' > .judge-isolation-probe
echo 'CONTROLLED_FAILURE: functional judge deliberately exits 1'
exit 1
```

Run the copied eval with the same case selector and a separate output directory.
Expect process exit 1, script FAIL, semantic PASS and aggregate FAIL. Inspect the
semantic transcript for the actual directory check rather than relying only on
its verdict. Keep this injection out of the normal example.

Both scenarios were rerun after fixing snapshot modification-time preservation,
with the same verdicts and successful archived-material readback. A regression
test additionally preserves source/output freshness and directory modification
times across capture and independent forks.

Only this selected case and the controlled negative variant were run against the
real model. The other three code-stats cases and the six-case skill-upper suite
were configuration-validated but were not run against Qwen in this verification.
The Harbor comparison remains a source comparison, not a live Harbor experiment.
