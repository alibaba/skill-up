# 从 Anthropic 格式迁移

如果你使用 Anthropic 的 skill-creator 创建了 Skill，你的 Skill 目录下已经有一个 `evals/evals.json` 文件。skill-up 可以直接使用它，无需从零编写配置。

---

## 两种接入方式

### 方式一：直接消费 evals.json（推荐快速接入）

使用 `--auto` 模式，skill-up 会自动检测 `evals/evals.json` 并直接运行，**不生成任何中间文件**：

```bash
# 在 Skill 目录下运行
cd my-skill/
skill-up run --auto

# 或显式指定目录
skill-up run ./my-skill/ --auto

# 用不同的 Engine 运行同一套用例
skill-up run --auto --engine codex
```

**适用场景**：

- 快速在 CI 中跑回归测试
- 用 Codex 等其他 Engine 验证同一套用例
- 保持与 Anthropic 工作流同步——`evals.json` 更新后下次运行自动生效

### 方式二：转换为原生格式（推荐深度定制）

使用 `import` 命令一次性将 `evals.json` 转为 skill-up 的 YAML 格式：

```bash
skill-up import ./evals/evals.json
```

转换后生成：

```plain
evals/
  eval.yaml                # 评测入口配置（需根据实际情况调整）
  cases/
    case-1.yaml            # 每个 eval 条目一个文件
    case-2.yaml
    case-3.yaml
```

转换后你可以：

- 为用例添加 `expect` 门槛检查（确定性验证）
- 使用 `rule_based` 替代纯 LLM 评估
- 配置 MCP 工具验证

**适用场景**：

- 需要 skill-up 独有能力（结构化断言、MCP 工具调用验证等）
- 需要精细调整评估逻辑
- 不再需要与 Anthropic `evals.json` 保持同步

---

## 两种方式对比

|                       | `--auto` 模式                                          | `import` 转换                          |
| :-------------------: | :----------------------------------------------------: | :------------------------------------: |
| **操作**              | 零配置，运行时动态读取                                  | 一次性转换，之后在 YAML 上维护          |
| **同步**              | evals.json 更新自动生效                                 | 转换后两边独立维护                      |
| **定制**              | 仅支持 evals.json 中已有的内容                           | 完全自由定制                            |
| **评估方式**          | 默认使用 agent_judge（因 expectations 是自然语言）       | 可切换为 rule_based 或 script           |
| **典型用户**          | 快速接入、CI 回归                                        | 深度评测、长期维护                      |

> 两种方式不冲突。你可以先用 `--auto` 快速验证，再用 `import` 转换需要深度定制的用例。

---

## evals.json 格式说明

Anthropic 的 `evals.json` 格式如下：

```json
{
  "skill_name": "my-skill",
  "evals": [
    {
      "id": 1,
      "prompt": "帮我创建一个发布计划",
      "expected_output": "应该调用 create_plan 工具",
      "files": [],
      "expectations": [
        "正确调用了 create_plan 工具",
        "参数中包含了计划名称"
      ]
    }
  ]
}
```

转换映射关系：

| evals.json 字段     | skill-up 对应                            |
| :-----------------: | :----------------------------------------: |
| `prompt`            | `input.prompt`                             |
| `expectations`      | `judge.criteria`（agent_judge 评估标准）    |
| `expected_output`   | 用例的 `description`                        |
| `files`             | `context.files`                            |

---

## 推荐的迁移路径

```plain
1. 先用 --auto 跑通 CI
   skill-up run --auto

2. 确认可以正常运行后，import 需要定制的用例
   skill-up import ./evals/evals.json --output ./evals-v2

3. 编辑转换后的 YAML，添加 expect 门槛检查和 rule_based 规则

4. 用原生格式运行
   skill-up run ./evals-v2/eval.yaml
```

最理想的用户旅程：**用 Anthropic skill-creator 创建和迭代 Skill → skill-up `--auto` 快速接入 CI → 需要深度定制时 `import` 转为原生格式**。
