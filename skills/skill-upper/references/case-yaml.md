# case.yaml 字段参考（skill-up）

每个 `evals/cases/*.yaml` 是一个评测用例。用例 ID = 文件名（去掉 `.yaml`）。语义与 skill-up 内置 schema 一致。

## 单轮用例骨架

```yaml
id: find-null-bug
title: 应该识别出空指针 bug
description: 验证 Skill 能在代码审查中发现 null 解引用问题

input:
  prompt: |
    Review the current diff and report findings.

context:
  repo_fixture: fixtures/repos/null-check-bug
  git:
    init: true
    checkout: main
    apply_diff: fixtures/diffs/null-check.patch

constraints:
  timeout_seconds: 180
  max_turns: 8

expect:
  must_contain: ["null", "bug"]
  must_not_contain: ["LGTM"]
  exit_code: 0

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["null", "bug"]
    - exit_code: 0
```

## 多轮对话

```yaml
input:
  turns:
    - role: user
      content: "sdd_bootstrap: task=实现用户登录功能"
      post_condition:
        must_contain_any: ["Research", "分析"]
        on_fail: skip_remaining      # 或 fail
      capture:
        - variable: phase
          pattern: "(?P<value>Research|Implementation)"
    - role: user
      content: "跳过 Research，直接帮我写代码"
```

`post_condition`：每轮结束后检查输出。`on_fail: skip_remaining` 标为 SKIP，`fail` 直接 FAIL 整个用例。

`capture`：从响应中捕获值，用于后续轮次的 `{{variable}}` 模板替换。
  • 提取器：`pattern`（正则）或 `jsonpath`，必须且仅指定一个
  • 优先使用 `(?P<value>...)` 命名组
  • 未匹配/空值 → 用例进入 ERROR 状态
  • 作用域仅限当前用例执行

### 按轮次 Judge 断言

```yaml
judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 2
        contains_any: ["必须完成", "不能跳过", "Research"]
    - turn_response_not_contains:
        turn: 2
        not_contains: ["LGTM"]
    - tool_called_in_turn:
        turn: 1
        name: write_file
    - tool_not_called_in_turn:
        turn: 2
        name: delete_file
```

仅 `status=completed` 的轮次可被断言；引用不存在或未完成的轮次会导致断言失败。

## context — 初始化工作区

```yaml
context:
  repo_fixture: fixtures/repos/my-project
  git:
    init: true
    checkout: feature-branch
    apply_diff: fixtures/diffs/my.patch
    remotes:
      - name: origin
        url: https://github.com/user/repo
  files:
    "src/main.py": |
      def hello():
          print("Hello World")
    "config.json": '{"debug": true}'
```

## expect — 零成本门槛检查

```yaml
expect:
  must_contain:
    - "review"
    - "bug"
  must_not_contain:
    - "LGTM"
    - "error"
  exit_code: 0
  files_exist:
    - "review.md"
    - "output.json"
  files_not_exist:
    - "temp.log"
  file_contains:
    - path: "review.md"
      content: "security"
  golden_file: "expected.txt"
```

expect 不通过时，judge 会被跳过。用它来快速过滤明显不合格的输出，节省 token。

**与 `cases.defaults.expect` 合并：**

- 如果 `eval.yaml` 的 `cases.defaults.expect` 定义了默认检查（如 `exit_code: 0`, `must_not_contain: ["TODO"]`），每个用例的 expect 会与之合并
- 切片字段（`must_contain`, `must_not_contain`, `files_exist`, `files_not_exist`, `file_contains`）追加去重；标量字段（`exit_code`, `golden_file`）由用例覆盖
- 用例未设 expect 时，直接使用默认值

## 常见写法

- **纯文本路由 Skill**：`expect.must_contain` + `judge.rule_based.output_contains`
- **MCP 工具校验**：`judge.rule_based.success[tool_called]`
- **语义质量评估**：`judge.agent_judge.criteria`
- **复杂结构化断言**：`judge.script`，在脚本里读 `$EVAL_TRANSCRIPT_PATH` 等自由判断
