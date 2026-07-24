---
layout: home

hero:
  name: skill-up
  text: Evaluate and evolve Agent Skills
  tagline: Measure Skill quality, then turn failures into automatic eval fixes and the next iteration.
  image:
    src: /logo.png
    alt: skill-up
  actions:
    - theme: brand
      text: Get Started
      link: /guide/getting-started
    - theme: alt
      text: Writing Evals
      link: /guide/writing-evals
    - theme: alt
      text: View on GitHub
      link: https://github.com/alibaba/skill-up

features:
  - title: Eval-to-Evolution with skill-upper
    details: Create evals through natural conversation, then let skill-upper diagnose failures, repair or expand cases, and rerun skill-up until the suite evolves.
    link: /guide/getting-started#eval-to-evolution-with-skill-upper
    linkText: Learn more
  - title: Declarative Eval Config
    details: Define evaluation environment, engine, model, and cases through YAML (eval.yaml + cases/*.yaml).
    link: /guide/writing-evals
    linkText: Configuration reference
  - title: Multi-Engine Support
    details: Works with Qoder CLI, Claude Code, and Codex as Agent Engines.
  - title: Flexible Judging
    details: Supports rule_based, script, and agent_judge evaluation strategies.
    link: /guide/writing-evals#grading-strategies
    linkText: Grading strategies
  - title: Structured Reports
    details: Outputs Anthropic-compatible grading.json, benchmark.json, plus result.json, JUnit XML, and HTML reports.
    link: /guide/cli-reference#output-layout
    linkText: Output layout
  - title: Anthropic Compatible
    details: Import evals.json via skill-up import, or auto-detect with --auto.
    link: /guide/migration
    linkText: Migration guide
  - title: CI-Ready
    details: Designed for local development and continuous integration pipelines.
    link: /guide/cli-reference#exit-codes
    linkText: Exit codes
---

## Overview

skill-up combines **evaluation** and **evolution** for Agent Skills. It makes quality measurable through declarative YAML evals, isolated multi-engine runs, flexible judges, and structured reports. Then skill-upper turns failures into progress: it can automatically repair or expand the eval suite and rerun the loop with you.

The same workflow runs locally or in CI, supports Anthropic `evals.json` imports, and emits JSON, JUnit, and HTML reports.

![How skill-up evaluates and evolves Agent Skills through automatic eval repair and iteration](/skill-up-overview.png)
