# 用户配置

每次执行 `skill-up`，框架都会自动加载一份用户配置（user-config）。它适合存放
OpenTelemetry 默认值、共享的环境变量、以及不想在命令行或每个 `eval.yaml` 里
重复声明的 `run` 默认参数。

本页说明加载顺序、Schema，以及如何用 `skill-up init` 落地这个文件。

---

## 加载顺序

按优先级从低到高依次合并：

```text
embed (空)
  < user      (~/.config/skill-up/config.yaml，遵循 XDG)
  < explicit  (--config <path>)
```

| Layer      | 路径                                                                                            | 文件缺失 |
| :--------- | :---------------------------------------------------------------------------------------------- | :------- |
| `embed`    | 空的 `Config{}`，不内嵌任何厂商默认值                                                           | 永远存在 |
| `user`     | `$SKILL_UP_CONFIG` → `$XDG_CONFIG_HOME/skill-up/config.yaml` → `~/.config/skill-up/config.yaml` | 跳过     |
| `explicit` | 任意子命令的 `--config <path>`                                                                  | 报错     |

`user` 文件不存在时静默跳过，读取或解析失败时在 stderr 上输出 `warning:`。
`--config` 显式路径如果缺失或不合法则直接报错。

自动发现的 `$PWD/.skill-up.yaml` 会被忽略并输出警告。审阅文件后，使用
`--config ./.skill-up.yaml` 显式加载。这项授权覆盖整个文件，包括 `runtime_kwargs`。
指定其他显式文件不会授权项目文件。通过 `SKILL_UP_CONFIG` 选择的项目文件也会作为
可信的用户层加载。
长期设置 `SKILL_UP_CONFIG` 时应使用绝对路径：相对路径从当前工作目录解析，
在不同仓库中可能选择不同文件。

高优先级层只**覆盖非零字段**；map 类字段（`telemetry.resource_attributes`、
`env`、`runtime_kwargs`）按 key 逐项合并，而不是整体替换。

---

## Schema

```yaml
schema_version: v1alpha1
kind: SkillUpConfig

telemetry:
  service_name: skill-up                              # OTEL_SERVICE_NAME
  traces_exporter: otlp                               # OTEL_TRACES_EXPORTER
  traces:
    endpoint: http://localhost:4317                   # OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
    protocol: grpc                                    # OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
  resource_attributes:                                # 序列化为 OTEL_RESOURCE_ATTRIBUTES
    deployment.environment: local
  verbose: false                                      # 为 true 时开启 OTEL_LOG_* 详细载荷

env:                                                  # 任意环境变量，only-if-unset
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${OTLP_TOKEN}

runtime_kwargs:                                       # 按 environment.type 提供 run 的默认 kwargs
  opensandbox:
    base_url: http://localhost:8080
```

语义：

- **`telemetry` 与 `env`**：每个 key 对应一个 `OTEL_*` 或自定义环境变量，**仅在
  对应环境变量未被设置时才会注入**——命令行或 shell 中已有的值优先。
- **`env` 中的 `${VAR}`** 仅在值来自 `user` 或 `explicit` 层时，才在应用默认值之前
  从当前进程环境展开。选择文件会授权其读取宿主环境变量（包括密钥），因此应先审阅。
  未设置的变量保留原文，
  已设置为空字符串的变量展开为空字符串。只展开一次，不读取同一配置中的其他默认值。
  `$VAR` 和 `${VAR:-fallback}` 等 shell 表达式保留原文。
- **`runtime_kwargs`**：`run` 命令按 `environment.type`（如 `opensandbox`）查找
  默认值。`eval.yaml` 中显式给的值优先；未提供的字段在这里兜底。Key 的形态与
  对应 environment provider 的 kwargs 一致。

### 端点信任策略

只有可信的用户配置或显式选择的配置文件才能提供默认值。未选择的项目文件不能覆盖
遥测端点、通过 `env` 注入代理或 agent 基础 URL，也不能通过 `runtime_kwargs`
重定向 OpenSandbox。即使项目值是字面量，也无法通过这些默认值将可信配置或进程
环境中的凭据发送到项目指定的目标。

这是对自动加载 `.skill-up.yaml` 行为的不兼容变更。审阅文件后，为每次调用添加
`--config ./.skill-up.yaml`，或将可信默认值移到用户配置中。授权前应同时检查目标
地址和凭据引用：显式配置的端点可以与用户配置或 shell 中的凭据组合使用。
信任针对所选路径，而非已审阅内容的快照。仓库更新后，再次使用该路径前应审阅变更。
如需稳定的本地默认值，可用 `skill-up init --config ./.skill-up.yaml` 将已审阅的文件
复制到用户配置，或将其保存在仓库之外。

此边界仅针对用户配置发现。`skill-up run` 将评测配置视为可执行输入，无论路径是
显式提供还是自动发现。其运行时和模型端点可使用用户配置或 shell 中的凭据，
脚本裁判也可执行代码。携带凭据运行前，应审阅评测及其脚本。本次变更不会为
评测配置增加端点限制或沙箱保护。

---

## 用 `skill-up init` 初始化

`skill-up init` 会把配置文件写到两个标准位置之一：

```bash
skill-up init                            # 模板 -> ~/.config/skill-up/config.yaml
skill-up init --local                    # 模板 -> ./.skill-up.yaml
skill-up init --print                    # 模板 -> stdout
skill-up init --force                    # 覆盖已存在的目标
```

默认模板里所有可选字段都是注释掉的——直接写入不会改变任何行为，它的价值是把
所有支持的字段集中在一份带注释的文件里。

### 从已有配置初始化

`--config <source>` 指定一个已存在的 YAML 作为**源文件**：`init` 会先把它当作
skill-up 配置进行校验，然后将原始字节（注释、格式都保留）写到由 `--local`
决定的目标位置：

```bash
# 把团队共享的配置拷到 XDG 路径
skill-up init --config ./team-config.yaml

# 同样的内容，作为项目级配置
skill-up init --config ./team-config.yaml --local
```

`--config` 和 `--local` 可以同时使用。对 `init` 子命令，`--config` 是**读取
源**；而对其他子命令（`run` / `validate` 等），`--config` 是加载链最顶层的
**覆盖路径**。

### 查看会被加载的内容

最简单的方式是 `--print`：

```bash
# 校验并把 run 实际看到的 explicit 层内容输出到 stdout
skill-up init --config ./team-config.yaml --print
```

---

## 环境变量

除了 YAML 配置文件外，skill-up 还支持若干影响运行时行为的环境变量。这些变量
与 `config.yaml` 中的 `env` 字典（用于向子进程注入环境变量）是分开的。

### `SKILL_UP_BASH`

覆盖 skill-up 用于 `.sh` script judge 和 shell 命令的 bash 解释器。

| 属性         | 说明                                                                   |
| :----------- | :--------------------------------------------------------------------- |
| **用途**     | 让 skill-up 使用指定的 `bash` 可执行文件                               |
| **适用平台** | 所有平台 —— Linux、macOS、Windows                                      |
| **默认值**   | 未设置；回退到 `PATH` 上的 `bash`（Windows 上还会探测常见 Git Bash 路径） |

**优先级：** 设置 `SKILL_UP_BASH` 且其值指向有效文件时，skill-up 会无条件使用它
—— 该变量的检查**先于**任何 `PATH` 查找或知名路径探测。

**完全信任要求：** 你提供的路径会被原样使用，不经过额外沙箱。你有责任确保它指向
一个可信的 bash 可执行文件。

示例（macOS / Linux）：

```bash
export SKILL_UP_BASH=/opt/homebrew/bin/bash
skill-up run ./evals/eval.yaml
```

示例（Windows —— Git Bash 安装在非标准位置）：

```powershell
$env:SKILL_UP_BASH = "D:\tools\git\bin\bash.exe"
skill-up run .\evals\eval.yaml
```

在 Windows 上，即使通过 `SKILL_UP_BASH` 显式指定 `C:\Windows\System32\bash.exe`
（WSL shim）也会被拒绝；详见 [Windows 支持](./windows.md#在-windows-上运行-sh-script-judge)。

---

## 示例

### 默认导出 trace 到本地 collector

`~/.config/skill-up/config.yaml`：

```yaml
schema_version: v1alpha1
kind: SkillUpConfig
telemetry:
  service_name: skill-up
  traces_exporter: otlp
  traces:
    endpoint: http://localhost:4317
    protocol: grpc
  resource_attributes:
    deployment.environment: local
```

之后每次 `skill-up run` 都会自动导出 trace，不用再手动设置环境变量。

### 项目级覆盖

在评测目录旁放一份 `./.skill-up.yaml`：

```yaml
schema_version: v1alpha1
kind: SkillUpConfig
runtime_kwargs:
  opensandbox:
    base_url: http://sandbox.internal:9090
```

审阅后，为本次运行显式选择文件：

```bash
skill-up run ./evals/eval.yaml --config ./.skill-up.yaml
```

这会为本次调用提供 `opensandbox.base_url` 默认值。未传 `--config` 时，项目文件会被
忽略，用户级默认值仍生效。

### 命令行一次性覆盖

```bash
skill-up run ./evals/eval.yaml --config /tmp/debug-config.yaml
```

`--config` 在本次执行中覆盖用户级默认值。
