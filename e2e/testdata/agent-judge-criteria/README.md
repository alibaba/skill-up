# Agent Judge Criteria Testdata

Tests `judge.type: agent_judge` with multiple criteria evaluation.

## eval.yaml Features
- `judge.type: agent_judge`
- Multiple criteria with pass_threshold
- Structured output expectations

## Cases
- `detailed-code-review.yaml`: Tests agent_judge evaluation
  - Evaluates bug identification accuracy
  - Checks for false positive rate
  - Verifies actionable recommendations
  - Validates output structure (Strengths/Issues/Assessment)