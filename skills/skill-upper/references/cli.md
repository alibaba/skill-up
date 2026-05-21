# skill-up CLI 命令参考

skill-up 提供以下命令，覆盖评测的完整生命周期：校验、运行、查看用例、生成报告和格式迁移。另含用户配置初始化与调试子命令。

---

## skill-up run

运行评测用例，生成评估报告。

```bash
skill-up run [path] [flags]
```

### 参数

| 参数   | 说明                                                             |
| ------ | ---------------------------------------------------------------- |
| `path` | `eval.yaml` 的路径。省略时默认在当前目录下查找 `evals/eval.yaml` |

### Flags

| Flag                  | 默认               | 说明                                                                                                                                                                              |
| --------------------- | ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--auto`              | `false`            | 自动检测 `evals/` 目录，支持直接消费 Anthropic `evals.json`                                                                                                                       |
| `--include-case-name` | —                  | 只运行匹配的用例（glob，可多次）                                                                                                                                                  |
| `--exclude-case-name` | —                  | 排除匹配的用例（glob，可多次）                                                                                                                                                    |
| `--format`            | —                  | 附加报告格式：`junit` / `html`（可多次）。`result.json` 始终写入；`--format junit` 生成 `report.xml`；`--format html` 生成 `report.html`；`--format json` 对 `result.json` 为冗余 |
| `--output-dir`        | eval.yaml 同级目录 | 报告和产物的输出目录                                                                                                                                                              |
| `--iteration`         | `0`（auto）        | 总运行次数。`0` = 自动：在最后一个已有 `iteration-N/` 之后追加一轮；正整数 = 显式运行 N 轮，产物写入 `iteration-1/` … `iteration-N/`                                              |
| `--engine`            | 配置中的值         | 覆盖 Engine 名称                                                                                                                                                                  |
| `--model`             | 配置中的值         | 覆盖模型（格式：`provider/name`）                                                                                                                                                 |
| `--parallelism`       | 配置中的值         | 覆盖 `cases.parallelism`，临时调整用例并行数，取值 1–256                                                                                                                          |
| `--api-key`           | —                  | 传入 API Key（优先级高于环境变量）                                                                                                                                                |
| `-v, --verbose`       | `0`                | 日志详细程度：`info` 默认；`-v` 为 `debug`；`-vv` / `--verbose=2` 为 `trace`                                                                                                      |

### 退出码

- `0` — 所有用例通过
- `1` — 有用例失败或执行出错

可作为 CI 门禁。

### 典型用法

```bash
skill-up run ./evals/eval.yaml
skill-up run ./evals/eval.yaml --include-case-name "basic-*"
skill-up run ./evals/eval.yaml --exclude-case-name "*-old"
skill-up run ./evals/eval.yaml --engine codex --model openai/gpt-4
skill-up run ./evals/eval.yaml --parallelism 4
skill-up run ./evals/eval.yaml --format html --format junit
skill-up run ./evals/eval.yaml --iteration 3
skill-up run ./evals/eval.yaml
skill-up run --auto
skill-up run ./my-skill/ --auto --engine codex
```

### OTLP Trace（可选）

设置标准 OpenTelemetry 环境变量后，`skill-up run` 可经 OTLP 上报 trace；verbose 日志可带 `trace_id` / `span_id`。也可用 `skill-up init` 生成的用户配置填充默认 OTEL 相关环境变量。详见上游文档「User config」与「CLI 命令参考」中的 OTLP 小节。

---

## skill-up validate

校验 `eval.yaml` 及引用的 case 文件。

```bash
skill-up validate [path to eval.yaml]
```

成功示例：

```plain
✓ eval.yaml is valid (loaded 3 case(s))
```

---

## skill-up list-cases

列出配置中的全部用例。

```bash
skill-up list-cases [path to eval.yaml]
```

---

## skill-up report

从已有 `result.json` 重新生成报告，不重跑评测。

```bash
skill-up report <path to result.json> [flags]
```

| Flag           | 默认             | 说明                                |
| -------------- | ---------------- | ----------------------------------- |
| `--format`     | `json`           | `json` / `junit` / `html`（可多次） |
| `--output-dir` | result.json 同级 | 输出目录                            |

```bash
skill-up report result.json --format html
skill-up report result.json --format json --format junit --format html --output-dir ./reports
```

---

## skill-up import

将 Anthropic `evals.json` 转为 skill-up 原生 YAML。

```bash
skill-up import <evals.json> [flags]
```

| Flag       | 默认            | 说明     |
| ---------- | --------------- | -------- |
| `--output` | evals.json 同级 | 输出目录 |

`import` 与 `run --auto` 的区别：`import` 是一次性格式转换；`run --auto` 运行时直接读 `evals.json`，不落 YAML。

---

## skill-up init

将**用户配置模板**写到磁盘（Telemetry、默认 `runtime_kwargs` 等），与评测 `eval.yaml` 不同。

```bash
skill-up init [flags]
```

| Flag              | 说明                                             |
| ----------------- | ------------------------------------------------ |
| `--local`         | 写入 `$PWD/.skill-up.yaml`（与 `--config` 互斥） |
| `--print`         | 打印模板到 stdout，不写文件                      |
| `--force`         | 覆盖已存在文件                                   |
| `--config <path>` | 显式目标路径（需与 `--local` 二选一）            |

默认路径：`$XDG_CONFIG_HOME/skill-up/config.yaml` 或 `~/.config/skill-up/config.yaml`。发现链中还支持环境变量 `SKILL_UP_CONFIG` 指向用户配置文件。

---

## skill-up debug

调试内部模块（仅开发 / 排错常用）：

```bash
skill-up debug judge <input.json>
skill-up debug report <input.json>
```

---

## 产物目录结构

```plain
<skill-name>-workspace/
  iteration-1/
    result.json
    benchmark.json
    report.html          # 若生成
    <case-id>/
      with_skill/
        outputs/
        grading.json
      without_skill/     # 仅 benchmark.enabled=true 时可能有
        outputs/
        grading.json
```

### grading.json（Anthropic 兼容子集）

工作区内的 `grading.json` 通常只含 `expectations` 与 `summary`。完整状态见 `result.json` 的 `case_results[].grading`（`status`、`turns_executed`、`assertion_results` 等）。

### benchmark.json

启用基线对比后会有 `without_skill` 与 `delta` 等字段。
