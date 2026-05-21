# Skill-up Enhancement Proposals

Use this directory to draft, review, and store enhancement proposals before they
undergo broader discussion.

> [!NOTE]
> The proposal process and template structure is inspired by
> [Tekton Enhancement Proposals (TEPs)](https://github.com/tektoncd/community/tree/main/teps).

> [!IMPORTANT]
> **When is a proposal required?**
>
> Use the proposal process for changes that:
> - Introduce new features or major enhancements to skill-up
> - Modify the evaluation pipeline, Agent interface, or Judge behavior
> - Affect the configuration schema or CLI contract
> - Add new Agent Engine integrations
>
> Small bug fixes, documentation updates, and minor refactors can be submitted
> directly as Pull Requests without a proposal.

## Getting started

1. Run the init script to create a new proposal:

   ```bash
   proposals/init-proposal.sh "Proposal Title"
   ```

   This copies the template, fills in metadata, and creates a sequentially
   numbered `0001-proposal-title.md` draft.

2. Fill in each section from the template (`Summary`, `Motivation`, …).
3. Once ready, submit the resulting file in a PR for community review.

**Available options:**

```bash
proposals/init-proposal.sh --help
proposals/init-proposal.sh --status provisional --author "@username" "My Feature"
```

## Template

The template used for new proposals lives at `proposals/proposal-template.md.template`
and mirrors the standard enhancement proposal structure while capturing the key
sections needed for skill-up planning. Each generated file starts with YAML
front matter followed by the title and TOC:

```yaml
---
title: My First Proposal
authors:
  - "@your-github-handle"
creation-date: 2025-12-21
last-updated: 2025-12-21
status: draft
---

# Proposal-0001: My First Proposal

<!-- toc -->
- [Summary](#summary)
...
<!-- /toc -->
```

This YAML front matter renders as a table on GitHub and keeps the proposal
metadata (status, authors, dates) visible at the top of the document.

## Status lifecycle

| Status | Description |
|--------|-------------|
| `draft` | Work in progress; not yet under formal review. |
| `provisional` | Maintainers agree with the direction; design details still pending. |
| `implementable` | Design approved and compliance checks passed; ready for implementation. |
| `implementing` | Code is being merged and changes are being integrated. |
| `implemented` | Feature has reached stable status with complete documentation. |
| `withdrawn` | Author has withdrawn the proposal. |
| `rejected` | Maintainers have declined the proposal. |
