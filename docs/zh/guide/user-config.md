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

`user` / `project` 层文件不存在或解析失败会降级为 stderr 上的 `warning:`，命令
继续执行。`--config` 显式路径如果缺失或不合法则直接报错。

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
- **`env` 中的 `${VAR}`** 在加载时从当前进程环境展开，密钥不会被写进文件本身。
- **`runtime_kwargs`**：`run` 命令按 `environment.type`（如 `opensandbox`）查找
  默认值。`eval.yaml` 中显式给的值优先；未提供的字段在这里兜底。Key 的形态与
  对应 environment provider 的 kwargs 一致。

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
