# Git Context Testdata

Tests `context.git.init` and `context.files` configuration.

## eval.yaml Features
- `environment.type: none`
- Git initialization and context files

## Cases
- `check-remote-url.yaml`: Verifies git init configuration
  - Tests `context.git.init: true`
- `review-code-with-context.yaml`: Tests context files
  - Tests `context.files` with source code