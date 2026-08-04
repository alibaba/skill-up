# CLI 命令参考

skill-up 提供以下命令，覆盖评测的完整生命周期：校验、运行、查看用例、生成报告和格式迁移。

---

## skill-up run

运行评测用例，生成评估报告。

```bash
skill-up run [path] [flags]
```

### 参数

| 参数   | 说明                                                                |
| :----: | :-----------------------------------------------------------------: |
| `path` | `eval.yaml` 的路径。省略时默认在当前目录下查找 `evals/eval.yaml`     |

### Flags

| Flag                  | 默认值                       | 说明                                                                                                                                                     |
| :-------------------: | :--------------------------: | :------------------------------------------------------------------------------------------------------------------------------------------------------: |
| `--auto`              | `false`                      | 自动检测 `evals/` 目录，支持直接消费 Anthropic `evals.json`                                                                                                 |
| `--include-case-name` | —                            | 只运行匹配的用例（支持 glob，可多次指定）                                                                                                                  |
| `--exclude-case-name` | —                            | 排除匹配的用例（支持 glob，可多次指定）                                                                                                                    |
| `--format`            | —                            | 附加报告格式：`junit` / `html`（可多次指定）。`result.json` 始终写入；`--format junit` 生成 `report.xml`，`--format html` 生成 `report.html`；`--format json` 为空操作 |
| `--output-dir`        | 与 skill 目录同级的 `<skill-name>-workspace/` | 报告和产物的输出目录                                                                                                                       |
| `--iteration`         | `0`（auto）                  | 重复运行已选用例，用于稳定性/flaky 采样。`0` 表示在最新 `iteration-N/` 后自动追加一轮，但不汇总历史结果；正整数 `N` 表示运行 N 次采样，产物写入 `iteration-1/` 到 `iteration-N/`；当 `N > 1` 时，终端摘要只覆盖本次命令执行的采样 |
| `--engine`            | 配置文件中的值                | 覆盖 Engine 名称                                                                                                                                          |
| `--model`             | 配置文件中的值                | 覆盖模型（格式：`provider/name`）                                                                                                                          |
| `--parallelism`       | 配置文件中的值                | 覆盖 `cases.parallelism`，用于临时调整用例并行数，取值范围为 1 到 256                                                                                       |
| `--baseline`          | 配置文件中的值                | 为本次运行覆盖 `benchmark.enabled` 为 `true`                                                                                                                |
| `--api-key`           | —                            | 传入 API Key（优先级高于环境变量）                                                                                                                          |
| `-v, --verbose`       | `0`                          | 增加日志详细程度。默认输出 `info`；`-v`/`--verbose`/`--verbose=true` 输出 `debug`；`-vv`/`--verbose=2` 输出 `trace`；`--verbose=false` 关闭附加详细日志        |

### 示例

```bash
# 运行所有用例
skill-up run ./evals/eval.yaml

# 只运行匹配的用例
skill-up run ./evals/eval.yaml --include-case-name "basic-*"

# 排除特定用例
skill-up run ./evals/eval.yaml --exclude-case-name "*-old" --exclude-case-name "*-deprecated"

# 指定 Engine 和模型
skill-up run ./evals/eval.yaml --engine codex --model openai/gpt-4

# 临时覆盖用例并行数
skill-up run ./evals/eval.yaml --parallelism 4

# 启用基线对比
skill-up run ./evals/eval.yaml --baseline

# 生成多种格式报告
skill-up run ./evals/eval.yaml --format json --format html --format junit

# 本次命令连续运行 3 次采样，产物分别写入 iteration-1/ 到 iteration-3/，终端打印简单 flaky 摘要
skill-up run ./evals/eval.yaml --iteration 3

# 自动检测模式（直接消费 Anthropic evals.json）
skill-up run --auto
skill-up run --auto --engine codex
skill-up run ./my-skill/ --auto
```

### 退出码

| 退出码 | 含义                  |
| :----: | :-------------------: |
| `0`    | 所有用例通过           |
| `1`    | 有用例失败或执行出错    |

> CI 集成时可直接根据退出码判断评测是否通过。

### OTLP Trace 上报

默认不启用 telemetry。设置标准 OpenTelemetry 环境变量后，`skill-up run` 会通过 OTLP 上报运行链路，并在 verbose slog 日志中附带 `trace_id` / `span_id`：

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_METRICS_EXPORTER=otlp
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=local,service.namespace=skill-up
skill-up run ./evals/eval.yaml -v
```

也可以使用 trace 专用配置覆盖通用配置，例如 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`、`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`。目前支持 `grpc` 和 `http/protobuf`。

`OTEL_METRICS_EXPORTER=otlp` 会开启低基数指标上报，包括 run/case/runtime exec 的计数与耗时；也支持 `OTEL_METRICS_EXPORTER=console` 用于本地调试。`OTEL_RESOURCE_ATTRIBUTES` 会作为 resource 属性一起上报。需要显式关闭 metrics 时可设置 `OTEL_METRICS_EXPORTER=none`。

---

## skill-up validate

校验评测配置文件是否合法。建议在运行前先执行校验。

```bash
skill-up validate [path to eval.yaml]
```

### 示例

```bash
skill-up validate
skill-up validate ./evals/eval.yaml
```

成功输出：

```plain
✓ eval.yaml is valid (loaded 3 case(s))
```

校验会检查：

- `eval.yaml` 和所有引用的 case 文件是否存在且格式正确
- 必填字段是否完整
- 字段值是否合法

---

## skill-up list-cases

列出评测配置中的所有用例，方便快速查看。

```bash
skill-up list-cases [path to eval.yaml]
```

### 示例

```bash
skill-up list-cases
skill-up list-cases ./evals/eval.yaml
```

输出示例：

```plain
ID                    Title                           Tag              Prompt
basic-success         Agent should find null bug      functional_test  Review the current diff and report ...
edge-case-empty       Handle empty input gracefully   functional_test  Review an empty repository with no ...
regression-001        Fix: no longer misreports       functional_test  Review the payment processor code ...
```

---

## skill-up report

从已有的评测结果文件重新生成报告，无需重新运行评测。

```bash
skill-up report <path to result.json> [flags]
```

### Flags

| Flag           | 默认值                  | 说明                                                |
| :------------: | :---------------------: | :-------------------------------------------------: |
| `--format`     | `json`                  | 报告格式：`json` / `junit` / `html`（可多次指定）    |
| `--output-dir` | report.json 同级目录    | 报告输出目录（不存在时自动创建）                     |

### 示例

```bash
# 从结果生成 HTML 报告
skill-up report result.json --format html

# 同时生成多种格式
skill-up report result.json --format json --format junit --format html

# 指定输出目录
skill-up report result.json --format html --output-dir ./reports
```

### 报告格式说明

| 格式    | 文件          | 用途                                                                |
| :-----: | :-----------: | :-----------------------------------------------------------------: |
| `json`  | `report.json` | 机器可读的结构化数据，可被其他工具消费                                |
| `junit` | `report.xml`  | JUnit XML 格式，可被 CI 系统（Jenkins、GitHub Actions 等）解析        |
| `html`  | `report.html` | 人类可读的可视化报告，用浏览器打开查看                                |

---

## skill-up init

写出一份 skill-up 用户配置文件。完整的加载顺序与字段说明见 [用户配置](./user-config)。

```bash
skill-up init [flags]
```

### Flags

| Flag       | 默认值       | 说明                                                                                            |
| :--------: | :----------: | :---------------------------------------------------------------------------------------------: |
| `--local`  | `false`      | 写到 `$PWD/.skill-up.yaml` 而不是 XDG 路径                                                       |
| `--config` | —            | **读取源**文件（先校验，再原样拷贝，注释保留）。未提供时写出带注释的模板                          |
| `--print`  | `false`      | 输出到 stdout，而不是写盘                                                                        |
| `--force`  | `false`      | 覆盖已存在的目标文件                                                                             |

> 注意：对 `init` 子命令，`--config` 是**读取源**；对其他子命令（`run`、`validate` …），`--config` 是加载链最顶层的覆盖路径。

### 示例

```bash
# 写出带注释的模板
skill-up init                                  # -> ~/.config/skill-up/config.yaml
skill-up init --local                          # -> ./.skill-up.yaml
skill-up init --print                          # -> stdout

# 从已有配置初始化（先校验，注释完整保留）
skill-up init --config ./team-config.yaml          # -> ~/.config/skill-up/config.yaml
skill-up init --config ./team-config.yaml --local  # -> ./.skill-up.yaml
skill-up init --config ./team-config.yaml --print  # 校验后输出到 stdout

# 覆盖
skill-up init --local --force
```

---

## skill-up import

将 Anthropic `evals.json` 格式的用例一次性转换为 skill-up 的原生 YAML 格式。

```bash
skill-up import <path to evals.json> [flags]
```

### Flags

| Flag       | 默认值                  | 说明      |
| :--------: | :---------------------: | :-------: |
| `--output` | evals.json 同级目录     | 输出目录   |

### 示例

```bash
# 转换 Anthropic 格式到当前目录
skill-up import ./evals/evals.json

# 指定输出目录
skill-up import ./evals/evals.json --output ./new-evals
```

转换后会生成：

- `eval.yaml` — 评测入口配置（带默认值，需根据实际情况调整）
- `cases/*.yaml` — 每个 eval 条目转换为一个用例文件

> `import` 和 `--auto` 的区别：`import` 是一次性格式转换，之后在 YAML 文件上维护；`run --auto` 是运行时直接消费 `evals.json`，不生成中间文件。详见 [从 Anthropic 格式迁移](./migration)。

---

## 产物目录结构

评测运行后会在输出目录生成以下结构：

```plain
<skill-name>-workspace/
  iteration-1/                    # 第一轮迭代
    benchmark.json                # 汇总统计
    <case-id>/
      with_skill/                 # Skill 安装后的执行结果
        outputs/                  # Agent 生成的文件
          workspace/              # collect_artifacts 命中的文件（保留相对路径）
        grading.json              # 评估结果
      without_skill/              # 基线对比结果（仅 benchmark.enabled=true 时）
        outputs/
          workspace/
        grading.json
```

`outputs/workspace/` 子目录仅在配置了 `collect_artifacts` 时出现，详见[编写评测 → 采集 workspace 产物](writing-evals.md#采集-workspace-产物collect_artifacts)。

### grading.json 格式

每个用例的评估结果（Anthropic 兼容格式）：

```json
{
  "expectations": [
    {
      "text": "输出包含 null 关键词",
      "passed": true,
      "evidence": "final_message 中包含 'null pointer at line 42'"
    }
  ],
  "summary": {
    "passed": 1,
    "failed": 0,
    "total": 1,
    "pass_rate": 1.0
  }
}
```

> **注意**：工作区目录下的 `grading.json` 采用 Anthropic 兼容格式，仅包含 `expectations` 和 `summary` 两个顶层字段。完整的评估状态（`status`、`turns_executed`、`turns_total`、`assertion_results`）保存在 `result.json` 的 `case_results[].grading` 中。

`result.json` 中的 `grading` 对象包含完整的评估状态：

- **PASS** — 所有断言通过
- **FAIL** — 至少一条断言失败
- **ERROR** — 执行异常（超时、引擎崩溃等）

### benchmark.json 格式

汇总所有用例的统计数据：

```json
{
  "run_summary": {
    "with_skill": {
      "pass_rate": { "mean": 0.83 },
      "time_seconds": { "mean": 45.0 },
      "tokens": { "mean": 3800 }
    },
    "without_skill": null,
    "delta": null
  }
}
```
