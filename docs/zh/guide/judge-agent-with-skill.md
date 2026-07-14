# Judge Agent with Skill 功能说明

`judge agent with skill` 指的是：当评测使用 `judge.type: agent_judge` 时，除了用
`judge.criteria` 描述评分维度，还可以通过 `judge.skills` 为评审 Agent 安装专用
Skill。这个 Skill 只服务于评审阶段，用来承载长 rubric、领域规则、反例、证据要求
和输出约束，不会安装给被测的主运行 Agent。

适合使用它的场景：

- 评审标准很长，不适合全部写进 `eval.yaml` 或 case YAML。
- 多个评测或多个 case 需要复用同一套评分规范。
- 被测 Skill 和评分 Skill 需要完全隔离，避免评分规则泄漏给主运行 Agent。
- 你希望沿用 Agent 自己的 Skill 发现和按需加载机制，而不是把大段 rubric 拼进 prompt。
- 报告读者需要看到本次评审使用了哪些 judge Skill，便于复现和审计。

不适合使用它的场景：

- 关键词、文件、退出码或工具调用已经能确定结果；这种情况优先使用 `expect` 或
  `rule_based`。
- 只需要运行本地脚本检查结构化输出；这种情况优先使用 `script` judge。
- 你希望完全不配置 `judge.criteria`，让 Skill 自己生成评分项。本版本仍要求
  `judge.criteria` 明确列出评分维度。

## 核心概念

`skill-up` 现在区分两类 Skill：

| 配置位置 | 安装给谁 | 何时安装 | 作用 |
| --- | --- | --- | --- |
| `skills` | 主运行 Agent | 执行 case 前；benchmark 的 `without_skill` 会跳过 | 被测 Skill 或主运行 Agent 需要的辅助 Skill |
| `judge.skills` | judge Agent | 进入 `agent_judge` 评审前；benchmark 的两组都会安装 | 评审 rubric、领域规则和评分工具 |

这两个配置不会互相继承：

- `judge.skills` 不会安装给主运行 Agent。
- 顶层 `skills` 不会自动安装给 judge Agent。
- benchmark 开启时，`without_skill` 只跳过顶层 `skills`，不会跳过 `judge.skills`。

`judge.skills` 使用 Agent adapter 原生的 Skill 安装能力。`skill-up` 不会读取
`SKILL.md`、`references/` 或 `assets/` 后拼接到 judge prompt；如果某个 Agent
adapter 不能正确安装 Skill，运行会报错，而不是静默降级为 prompt 拼接。

## 推荐目录结构

可以把 judge Skill 放在 `evals/fixtures/` 下，和评测数据一起维护：

```plain
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      api-compatibility.yaml
      security-review.yaml
    fixtures/
      judge-skills/
        api-compatibility-judge/
          SKILL.md
          references/
            compatibility-rubric.md
            negative-examples.md
        security-judge/
          SKILL.md
          references/
            security-rubric.md
```

一个最小 judge Skill 示例：

```markdown
---
name: api-compatibility-judge
description: Use when grading API compatibility, backward compatibility, and migration risk in skill-up agent_judge evaluations.
---

# API Compatibility Judge

You are the authoritative rubric for API compatibility grading.

Before grading, read `references/compatibility-rubric.md`.

When judging an answer:

- Treat breaking public API changes as high severity unless the case explicitly allows them.
- Require concrete evidence from the generated diff, final message, or transcript.
- Do not pass an answer only because it sounds plausible.
- If the answer proposes a migration, verify whether it names affected callers.
```

建议在 judge Skill 的 `description` 中写清触发场景。不同 Agent 的 Skill 发现机制可能
依赖名称、描述或目录索引，描述越准确，judge Agent 越容易在评审时选择它。

## 全局配置示例

在 `evals/eval.yaml` 中配置全局 judge Skill：

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6

skills:
  - source: local_path
    path: .

cases:
  files:
    - evals/cases/api-compatibility.yaml

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skills/api-compatibility-judge
  criteria:
    - "The answer follows the API compatibility rubric in the installed judge Skill."
    - "The grading decision cites concrete evidence from the final answer, transcript, or workspace diff."
    - "The answer does not pass if it misses a breaking public API change."
  pass_threshold: 0.7
  timeout_seconds: 60

report:
  formats: [json, html, junit]
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `judge.type` | 必须是 `agent_judge`。`rule_based` 和 `script` 不支持 `judge.skills`。 |
| `judge.model` | judge Agent 使用的模型。它可以和主运行 Agent 的模型不同。 |
| `judge.skills[].source` | Skill 来源。当前常用值是 `local_path`。 |
| `judge.skills[].path` | judge Skill 路径。相对路径按 Skill 根目录解析，也就是包含 `SKILL.md` 的项目根目录。 |
| `judge.skills[].target` | 可选。覆盖 Agent adapter 的默认安装目标；不确定时建议省略。 |
| `judge.criteria` | 结构化评分维度。即使详细规则在 judge Skill 中，也仍然需要至少一条 criteria。 |
| `judge.pass_threshold` | criteria 通过率阈值，默认 `0.7`。 |
| `judge.timeout_seconds` | 单次 judge 调用的超时限制。`0` 表示不额外设置 judge 级 deadline，但仍受 case 超时约束。 |

## 用例级覆盖

如果某个 case 需要另一套评审 Skill，可以在 case YAML 中声明自己的 `judge`：

```yaml
# evals/cases/security-review.yaml
id: security-review
title: Security review should identify injection risk

input:
  prompt: "Review this patch for security issues."

judge:
  type: agent_judge
  skills:
    - source: local_path
      path: evals/fixtures/judge-skills/security-judge
  criteria:
    - "The answer follows the security judge Skill rubric."
    - "The answer identifies injection risks with concrete file or code evidence."
```

合并规则需要特别注意：

- 如果 case 没有写 `judge.type`，它直接使用 `eval.yaml` 中的全局 `judge`。
- 如果 case 写了 `judge.type`，case 级 `judge` 会被视为一套完整评审策略。
- case 级 `skills`、`criteria`、`success`、`failure`、`script_path` 不会和全局字段合并。
- case 级未设置的 `model`、`pass_threshold`、`timeout_seconds` 会从全局 `judge` 继承。
- `judge.context` 支持在 case 级细粒度覆盖；未设置字段会继续继承全局 context。

因此，如果只想为某个 case 替换 judge Skill，必须同时写上 `judge.type:
agent_judge` 和该 case 需要的 `criteria`。

## 多个 judge Skill

`judge.skills` 可以配置多个 Skill，安装顺序与 YAML 中声明顺序一致：

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skills/common-evidence-rules
    - source: local_path
      path: evals/fixtures/judge-skills/go-api-judge
  criteria:
    - "The answer follows the common evidence rules and the Go API compatibility rubric."
    - "The decision explains any compatibility risk with concrete evidence."
```

使用多个 judge Skill 时，建议让每个 Skill 的职责边界清楚。例如一个 Skill 管证据
要求，另一个 Skill 管领域 rubric。避免多个 Skill 对同一规则给出相互冲突的要求。

## Benchmark 行为

开启 benchmark 后，`skill-up` 会运行两组配置：

```yaml
benchmark:
  enabled: true

skills:
  - source: local_path
    path: .

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skills/api-compatibility-judge
  criteria:
    - "The answer satisfies the judge Skill rubric."
```

执行语义如下：

| benchmark 变体 | 主运行 Agent 是否安装顶层 `skills` | judge Agent 是否安装 `judge.skills` |
| --- | --- | --- |
| `with_skill` | 是 | 是 |
| `without_skill` | 否 | 是 |

这样 benchmark 衡量的是被测 Skill 对主任务结果的影响，而不是评分工具是否存在。

## 评审 prompt 如何使用 Skill

当 `judge.skills` 非空时，`skill-up` 会在 judge prompt 中加入强制使用说明：

- judge Agent 必须把已安装的 judge Skill 作为权威评分依据。
- inline `criteria` 用来定义报告中的评分维度。
- judge Skill 用来定义详细 rubric、约束和证据规则。
- 如果 inline criteria 与 judge Skill 冲突，默认遵循 judge Skill；但 case 级
  criteria 可以表达更具体的验收条件。

这意味着 `judge.criteria` 不应该重复整份 rubric。更好的写法是让 criteria 指向
评分维度，把细则放进 judge Skill：

```yaml
criteria:
  - "The answer satisfies the compatibility requirements defined by api-compatibility-judge."
  - "The answer provides evidence for every pass/fail decision."
  - "The answer does not ignore high-risk breaking changes."
```

## 与 judge.context 配合

`agent_judge` 会把评审材料物化成文件，并在 prompt 里提供材料表。你可以用
`judge.context` 控制给 judge Agent 的材料范围：

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skills/release-note-judge
  context:
    profile: minimal
    attachments:
      - path: evals/fixtures/expected/release-note-requirements.md
        label: release_note_requirements
  criteria:
    - "The release note follows the installed judge Skill rubric."
    - "The answer covers every requirement in release_note_requirements."
```

常见用法：

- 默认 context 适合普通单轮输出评审。
- `minimal` 适合仓库变更很大、transcript 很长、希望 judge 主要依赖显式附件的场景。
- 附件适合提供黄金文件、外部规范、长 diff 摘要或脚本生成的检查结果。

judge Skill 负责教会 judge Agent 如何评审；`judge.context` 负责控制它能看到哪些
评审材料。两者可以一起使用。

## 运行和校验

建议先校验配置：

```bash
skill-up validate evals/eval.yaml
```

常见配置错误会在这里提前暴露：

- `judge.skills is only supported when judge.type is agent_judge`
- `judge.model is required when judge.type is agent_judge`
- `judge.criteria is required when judge.type is agent_judge`
- `judge.skills[0].source is required`
- `judge.skills[0].path is required`

校验通过后运行评测：

```bash
skill-up run evals/eval.yaml --format json --format html --format junit
```

如果要观察安装和评审过程，可以加 verbose：

```bash
skill-up run evals/eval.yaml -v
```

安装失败时，错误信息会包含具体的 `judge.skills[index].path`，例如：

```plain
failed to install judge skill judge.skills[0].path="evals/fixtures/judge-skills/api-compatibility-judge": ...
```

## 报告中的 judge Skill 信息

运行完成后，报告会记录本次评审使用的 judge Skill 元数据。

JSON 报告中，每个 case result 会包含 `judge_skills`：

```json
{
  "case_id": "api-compatibility",
  "status": "pass",
  "judge_skills": [
    {
      "source": "local_path",
      "path": "evals/fixtures/judge-skills/api-compatibility-judge",
      "name": "api-compatibility-judge"
    }
  ]
}
```

JUnit 报告会以 properties 暴露：

```xml
<property name="judge.skills.count" value="1"></property>
<property name="judge.skills.0.source" value="local_path"></property>
<property name="judge.skills.0.path" value="evals/fixtures/judge-skills/api-compatibility-judge"></property>
<property name="judge.skills.0.name" value="api-compatibility-judge"></property>
```

HTML 报告会展示相同的 judge Skill 列表，便于人工审查评审依据。

## 最佳实践

- 优先用 `expect` / `rule_based` 做确定性门槛，只把真正需要语义判断的部分交给
  `agent_judge`。
- `judge.criteria` 保持短而明确，描述评分维度；长规则、例外、反例和证据要求放进
  judge Skill。
- judge Skill 的 `name` 和 `description` 要稳定、具体，方便 Agent 选择和报告审计。
- judge Skill 中尽量写“如何判定”和“必须引用哪些证据”，不要只写抽象价值观。
- 多个 case 共用同一套 rubric 时，把 judge Skill 放在 `evals/fixtures/judge-skills/`
  下统一维护。
- 不确定安装路径时不要设置 `target`，让 Agent adapter 使用默认 Skill 目录。
- 多个 judge Skill 之间避免规则重叠；如果确实有优先级，写在 Skill 文档里。
- 在 CI 中至少保留 JSON 或 JUnit 报告，方便追踪 `judge_skills` 元数据。

## 常见问题

### 可以只写 `judge.skills`，不写 `judge.criteria` 吗？

不可以。本版本仍要求 `agent_judge` 配置 `judge.criteria`。judge Skill 提供详细
rubric，criteria 提供结构化评分维度和报告断言项。

### `judge.skills` 可以用于 `rule_based` 或 `script` 吗？

不可以。`judge.skills` 只支持 `judge.type: agent_judge`。如果确定性规则或脚本已经
能判断结果，应该直接使用 `rule_based` 或 `script`。

### case 里能不能只覆盖 `judge.skills`？

不能只写 `judge.skills`。case 级 judge 配置必须写 `judge.type: agent_judge`，并提供
该 case 自己的 `criteria`。未设置的 `model`、`pass_threshold`、`timeout_seconds`
可以从全局 judge 继承。

### judge Skill 会不会污染被测 Skill？

不会。`judge.skills` 只安装给 judge Agent。顶层 `skills` 才是主运行 Agent 使用的
Skill 配置。

### benchmark 的 `without_skill` 会不会跳过 judge Skill？

不会。`without_skill` 只跳过顶层 `skills`，仍然安装 `judge.skills`。judge Skill 是
评分工具，不是被测能力。

### skill-up 会把 judge Skill 内容拼进 prompt 吗？

不会。`skill-up` 只调用 Agent adapter 的 `InstallSkill`。Skill 的发现、加载和
references 读取由具体 Agent 自己处理。

### 为什么 judge Agent 没有按 Skill 里的规则评分？

先检查以下几点：

- judge Skill 的 `description` 是否准确描述了触发场景。
- `judge.criteria` 是否明确要求遵循该 judge Skill。
- 运行日志里是否出现 judge Skill 安装失败。
- 使用的 Agent adapter 是否支持 Skill 安装和发现。
- judge Skill 内是否把关键 rubric 放在 Agent 能按需读取的位置，例如
  `references/`。

### 什么时候需要设置 `judge.skills[].target`？

只有当你明确知道某个 Agent 需要安装到特定路径时才设置。一般情况下省略 `target`，
让 Agent adapter 选择默认安装目录更稳妥。
