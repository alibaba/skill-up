# Retry and Parallelism Testdata

Tests `cases.retry_policy` and `cases.parallelism` configuration.

## eval.yaml Features
- `cases.parallelism: 2`
- `cases.retry_policy.max_retries: 2`
- `cases.retry_policy.retry_on: [timeout, error]`
- Engine: `qoder-cli` with mock-engine (deterministic, no real LLM)

## Cases
- `flaky-success-retry.yaml`: Uses `MOCK_FAIL_COUNT=1` so the mock engine fails on
  the first invocation and succeeds on retry. Verifies that the runner honours
  `retry_policy` by retrying on error and ultimately reporting PASS.
- `second-case-parallel.yaml`: Deterministic success case that runs in parallel
  with the flaky case, verifying that parallelism and retry do not interfere.

## Note
`retry_policy` is currently a **known gap** (see `contract_test.go` gap #6).
Until it is implemented, `flaky-success-retry` will FAIL (first-call error is
not retried). Once the feature lands, this testdata validates the retry path
end-to-end.