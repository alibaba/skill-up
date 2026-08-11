# Judge 类型选型与写法（skill-up）

skill-up 的评估分两层：`expect`（零成本门槛） + `judge`（质量评估）。expect 不过 judge 就跳过，所以能用 expect 先过滤的尽量用 expect。

每个用例只能选**一种** judge 类型。

## 选型决策树

1. 有明确关键词 / 文件 / 退出码 / 工具调用可判定？→ **rule_based**
2. 有结构化输出，想写自定义脚本检查？→ **script**
3. 以上都不行，要 LLM 做语义判断？→ **agent_judge**（最贵，慎用）

## rule_based — 确定性规则

```yaml
judge:
  type: rule_based
  success:
    - output_contains:
        all: ["bug", "null"]
        any: ["建议修复", "推荐更改"]
        not: ["LGTM"]
    - output_matches:
        all: ["(?m)^## Status$", "(?m)^## Evidence$"]
        any: ["(?i)pass", "(?i)success"]
        not: ["(?i)api[_-]?key\\s*="]
    - exit_code: 0
    - tool_called:
        name: "github::create_pull_request"
        args:
          title: "Fix null check"
  failure:
    - output_contains:
        any: ["无需修改", "代码正确"]
```

评估逻辑：`failure` 优先，任一条命中立即 FAIL；否则所有 `success` 必须满足才 PASS。

**支持的匹配器**：

- `output_contains`
- `output_matches`（Go regexp，支持 `all` / `any` / `not`）
- `exit_code`
- `tool_called`
- `files_exist` / `files_not_exist`

## agent_judge — LLM 评审

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-rubric
      include: [SKILL.md, "references/**"]
      exclude: ["references/drafts/**"]
  criteria:
    - "输出中识别了真实存在的 bug，并符合 judge-rubric 中的评分细则"
    - "没有将正确代码误报为 bug"
    - "建议具有可操作性，不是泛泛而谈"
  pass_threshold: 0.7
```

**注意事项**：

- 会额外消耗 token，慢且贵
- criteria 尽量具体、可验证
- 能拆出确定性条件时先用 `rule_based` / `expect` 挡一道
- 需要长 Rubric、领域规则、复用评分规范时，把它们放进 `judge.skills`
- `judge.skills` 只会安装给 judge agent，不会污染被测 run agent；安装依赖具体 Agent adapter 的 Skill 支持，不会回退为 prompt 拼接
- `judge.skills[].include` / `exclude` 与顶层 `skills` 语义一致：相对 `path` 的 doublestar glob，且 exclude 优先

## script — 自定义脚本

```yaml
judge:
  type: script
  script_path: evals/fixtures/scripts/check-quality.sh
  timeout_seconds: 30
```

**脚本约定**：

- 退出码 `0` = PASS，非 `0` = FAIL
- 工作目录是用例工作区根目录
- 环境变量：`$EVAL_FINAL_MESSAGE`、`$EVAL_EXIT_CODE`、`$EVAL_TRANSCRIPT_PATH`（若可用）

## 成本提醒

| judge | 相对成本 | 何时选 |
| --- | --- | --- |
| expect | 0 | 任何时候先挡一道 |
| rule_based | 极低 | 默认首选 |
| script | 低（取决于脚本） | 灵活自定义 |
| agent_judge | 高 | 真的需要语义理解 |
