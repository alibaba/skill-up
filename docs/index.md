---
layout: home

hero:
  name: skill-up
  text: Evaluation framework for Agent Skill developers
  tagline: Declare your eval environment, dependencies, test cases, and grading strategy in YAML — run it locally or in CI.
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
