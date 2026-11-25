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
  < project   ($PWD/.skill-up.yaml)
  < explicit  (--config <path>)
```

| Layer      | 路径                                                                                            | 文件缺失 |
| :--------- | :---------------------------------------------------------------------------------------------- | :------- |
| `embed`    | 空的 `Config{}`，不内嵌任何厂商默认值                                                           | 永远存在 |
| `user`     | `$SKILL_UP_CONFIG` → `$XDG_CONFIG_HOME/skill-up/config.yaml` → `~/.config/skill-up/config.yaml` | 跳过     |
| `project`  | `$PWD/.skill-up.yaml`                                                                           | 跳过     |
| `explicit` | 任意子命令的 `--config <path>`                                                                  | 报错     |

`user` / `project` 层文件不存在时会跳过；无法读取或解析时会输出警告。
`--config` 显式路径如果缺失或不合法则直接报错，且只能指定一次。只有绝对路径的
`XDG_CONFIG_HOME` 才会生效；否则配置发现和 `init` 的默认写入位置都回退到用户主目录。

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
- **`env` 中的 `${VAR}`** 只在值来自用户层或显式配置层时展开。在应用任何默认值前，
  使用进程环境展开一次。未设置的引用保留原样；自动发现的项目配置值不展开。
- **`runtime_kwargs`**：`run` 命令按 `environment.type`（如 `opensandbox`）查找
  默认值。`eval.yaml` 中显式给的值优先；未提供的字段在这里兜底。Key 的形态与
  对应 environment provider 的 kwargs 一致。

### 凭据信任边界

自动发现的项目配置仍然提供默认值。如果项目默认值可以控制某个导出器的发送目标或
它所信任的对端，由该导出器携带的、通过变量展开生成的 OTLP 请求头不会被应用。检查按
信号进行：`OTEL_EXPORTER_OTLP_TRACES_HEADERS` 只取决于 traces 的生效路由，即 traces
自身的端点、协议和 TLS 选项，以及未被可信的 traces 设置覆盖的通用
`OTEL_EXPORTER_OTLP_*` 选项，因此只设置 metrics 端点的项目不会影响它。通用的
`OTEL_EXPORTER_OTLP_HEADERS` 可能被每个信号使用，只要项目提供了任一路由设置就不会被
应用。项目提供的代理设置和 TLS 根证书设置（`SSL_CERT_FILE`、`SSL_CERT_DIR`）对所有
信号生效。这仅约束新展开的请求头，不改变字面量
请求头、进程环境中已有的请求头或评测执行的处理方式。通过 `--config` 选择文件会
信任其变量展开，请先审阅文件。对于 `init`，`--config` 仅指定拷贝来源，不提供默认值。

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

这样只对当前项目生效，`~/.config/skill-up/config.yaml` 在其他地方依然有效。

### 命令行一次性覆盖

```bash
skill-up run ./evals/eval.yaml --config /tmp/debug-config.yaml
```

`--config` 在本次执行中覆盖 user 和 project 两层。
