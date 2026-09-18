# Skill Up Observer Codex plugin

This self-contained plugin captures explicitly attributed Skill interactions through Codex hooks and stores normalized observations locally. Its bundled MCP server and review Skill support explicit approval before the plugin creates a candidate regression case.

The root `plugin.json` and `mcp.json` follow the current portable Agent Plugin format. `.codex-plugin/plugin.json` and `.mcp.json` are retained as Codex compatibility fallbacks.

## Requirements

- A current Codex CLI or Codex desktop release with plugin and lifecycle-hook support. The IDE extension does not support plugins.
- Python 3 available as `python3`.
- Explicit trust for the plugin's hook definition in Codex.

The pinned Codex 0.80.0 binary used by skill-up's evaluation adapter is intentionally out of scope. It remains available for custom-model evaluation and does not load this plugin.

## Data and consent

Enabling the plugin and trusting its hooks opts in to local capture. Pending drafts and finalized observations are stored in Codex's plugin-specific writable `${PLUGIN_DATA}` directory. `SKILL_UP_OBSERVER_DATA` is available as a development/test override. Directories use mode `0700` and observation files use `0600`.

Common API-key, bearer-token, JWT, and secret-assignment shapes are redacted before persistence. Nothing is uploaded. Unattributed turns are discarded at `Stop` or `Interrupt`, and inferred attribution is rejected by the observation contract.

## Review workflow

Ask Codex to list or show observations, preview a candidate case, and approve or reject a specific observation. The bundled Skill uses the plugin's MCP tools for these operations.

Preview is read-only. Writing requires an approved observation, never overwrites an existing case, and updates `evals/eval.yaml`. If `skill-up` is available on `PATH`, the plugin runs `skill-up validate` and rolls back both files when validation fails; capture and review do not depend on skill-up.
