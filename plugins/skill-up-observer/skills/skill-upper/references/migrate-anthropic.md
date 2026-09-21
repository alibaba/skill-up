# 从 Anthropic evals.json 迁移（skill-up）

若使用 Anthropic skill-creator 生成过 Skill，目录下可能有 `evals/evals.json`。skill-up 可以直接消费它，也可一次性转成原生 YAML。

## 方式一：`--auto` 直接跑（零配置）

```bash
cd my-skill/
skill-up run --auto
skill-up run ./my-skill/ --auto
skill-up run --auto --engine codex
```

**适用**：快速 CI 回归、多 Engine 验证、与 Anthropic evals 源文件保持同步。

**限制**：

- 不支持多轮对话
- `expectations` 往往映射为 `agent_judge.criteria`，消耗评审 token
- 不便在 `--auto` 路径上叠加复杂 `opensandbox` / MCP / 强 `expect` 门槛（需转 YAML 后编辑）

## 方式二：`import` 转原生 yaml（深度定制）

```bash
skill-up import ./evals/evals.json
skill-up import ./evals/evals.json --output ./evals-v2
```

生成 `eval.yaml` + `cases/*.yaml` 后，你可以：

- 添加 `expect` 确定性门槛
- 使用 `rule_based` / `script` 替代或补充 LLM 评判
- 配置多轮 `turns`
- 配置 `environment.type: opensandbox`、MCP 等

## 对比

| | `--auto` | `import` |
| --- | --- | --- |
| 操作 | 运行时读取 | 一次性落盘 YAML |
| 同步 | evals.json 更新即生效 | 之后独立维护 YAML |
| 定制 | 受限于 JSON | 完全可控 |

> 可先用 `--auto` 跑通，再对重试用例 `import` 手改。

## evals.json 映射（摘要）

| evals.json | skill-up YAML |
| --- | --- |
| `prompt` | `input.prompt` |
| `expectations` | 默认 `judge.agent_judge.criteria` |
| `expected_output` | 常为 `description` |
| `files` | `context.files` 等 |

## 推荐路径

1. `skill-up run --auto` 验证可走通  
2. `skill-up import ./evals/evals.json --output ./evals-native`  
3. 编辑 YAML：补 `expect`、`rule_based`、`opensandbox` / MCP  
4. `skill-up run ./evals-native/eval.yaml`

Ideal：Anthropic 迭代 Skill → skill-up `--auto` 进 CI → 深度场景转 YAML 长期维护。
