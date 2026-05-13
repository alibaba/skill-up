# Multi-turn Conversation Testdata

Tests `input.turns` with multiple conversation turns and `post_condition` mechanism.

## eval.yaml Features
- Multiple cases with different turn configurations

## Cases
- `phase-gate-skip-attempt.yaml`: Tests phase gate enforcement with post_condition skip_remaining
  - Turn 1: Normal task start
  - Turn 2: Attempt to skip phase (should be rejected)
  - Turn 3: Complete Research phase
- `multi-turn-refinement.yaml`: Tests iterative refinement across turns
  - Turn 1: Generate quicksort code
  - Turn 2: Request non-recursive version