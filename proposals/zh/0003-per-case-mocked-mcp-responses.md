---
title: 按用例覆盖 mocked MCP 响应
authors:
  - "kongtang"
creation-date: 2026-07-07
last-updated: 2026-07-07
status: draft
---

# SUP-0003: 按用例覆盖 mocked MCP 响应

语言：[English](../0003-per-case-mocked-mcp-responses.md) | 中文

<!-- toc -->
- [摘要](#摘要)
- [动机](#动机)
  - [目标](#目标)
  - [非目标](#非目标)
- [需求](#需求)
- [提案](#提案)
  - [用户场景速查](#用户场景速查)
  - [Schema 形态](#schema-形态)
  - [合并语义](#合并语义)
  - [运行时行为](#运行时行为)
  - [注意事项/约束/说明](#注意事项约束说明)
  - [风险与缓解措施](#风险与缓解措施)
- [设计细节](#设计细节)
  - [Schema 变更](#schema-变更)
  - [有效 MCP 配置计算](#有效-mcp-配置计算)
  - [Evaluator 改造](#evaluator-改造)
  - [Provisioner 与路径解析](#provisioner-与路径解析)
  - [并行执行隔离](#并行执行隔离)
  - [错误与校验](#错误与校验)
  - [文档与模板](#文档与模板)
- [测试计划](#测试计划)
- [缺点](#缺点)
- [替代方案](#替代方案)
- [所需基础设施](#所需基础设施)
- [升级与迁移策略](#升级与迁移策略)
<!-- /toc -->

## 摘要

当前 skill-up 的 mocked MCP 响应只能在 eval 级别配置。同一个 eval 中的所有 case 会共享同一份 `mcp.servers[].config_ref`，导致开发者无法在保持同一 MCP server 名称和工具名称的前提下，为不同 case 注入不同项目状态、搜索结果或错误响应。

本提案为 case 增加 MCP 覆盖能力：case 可以声明 `mcp.servers`，并按 server `name` 覆盖 eval 级配置。MVP 聚焦 mocked MCP 响应覆盖，保证现有 eval 级 MCP 行为完全兼容，同时让并行执行的 case 使用各自独立的 mock fixture。

## 动机

Issue [#133](https://github.com/alibaba/skill-up/issues/133) 描述了一个常见评测需求：多个 case 都要调用同一个 MCP server，例如 `project-mgmt`，但每个 case 需要看到不同的项目状态、搜索返回或错误响应。

今天的配置只能这样写在 `eval.yaml`：

```yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-mgmt.yaml
```

这会让所有 case 共享 `project-mgmt.yaml` 里的 `tool_responses`。如果开发者想测试“项目打开”“项目关闭”“项目不存在”“工具报错”等分支，只能拆成多个 eval 文件，或者改 server 名称绕过共享配置。这两个办法都会带来噪声：

1. 拆分 eval 会让 benchmark 和报告比较变得分散。
2. 改 server 名称会偏离 Skill 在生产环境中真实使用的 MCP server 名称。
3. 全局 fixture 难以覆盖 MCP 结果驱动的分支逻辑。
4. 并行 case 如果共享可变 mock 状态，容易出现响应串线或测试不稳定。

当前代码中的关键限制是：

- `config.CaseConfig` 没有 MCP 覆盖字段。
- `defaultEvaluator.provisionMCPConfig()` 通过 `sync.Once` 只解析一次 `evalCfg.MCP`。
- `prepareRuntimeForCase()` 为每个 case 创建 runtime 时复用同一份已解析 MCP 配置 clone。
- mocked MCP server 的 Node 脚本由 `internal/mcp` 根据 `tool_responses` 生成，因此不同 case 必须在安装 MCP 前拿到不同的有效 MCP 配置。

### 目标

1. **case 级 mocked MCP 覆盖**：允许 case 为同名 mocked MCP server 指定不同 `config_ref` 或完整 server 配置。
2. **显式合并语义**：按 server `name` 做整项替换；未覆盖的 eval 级 server 原样继承。
3. **路径语义一致**：case 级 `config_ref` 继续相对包含 `SKILL.md` 的 Skill 目录解析，与现有 eval 级 MCP 一致。
4. **并行隔离**：每个 case 使用自己的有效 MCP 配置创建 runtime 和安装 MCP，不允许 mock 响应在 case 间泄漏。
5. **向后兼容**：未声明 case 级 `mcp` 的 eval 保持现有行为和配置格式。
6. **可测试可文档化**：补充单元测试、集成/E2E 覆盖和写作指南示例。

### 非目标

1. **不改变 mocked MCP fixture 文件格式**：`tool_responses`、`{{params.xxx}}` 模板等保持现状。
2. **不引入条件响应 DSL**：本提案不设计按请求参数匹配多个响应分支的复杂 mock 规则。
3. **不要求 real MCP 具备 per-case 覆盖**：MVP 只承诺 mocked MCP 覆盖；real MCP 的 case 级覆盖留给后续扩展。
4. **不改变 Agent Engine 安装协议**：Claude Code、Codex、Qoder CLI、Qwen Code 等 Agent 仍通过现有 `InstallMCP` 接收 runtime MCP 配置。
5. **不跨 case 复用有状态 mock server**：每个 case 的 mocked server 随 case runtime 安装和启动，避免共享状态。

## 需求

### 必须有

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | `CaseConfig` 支持 case 级 MCP 配置 | case YAML 可声明 `mcp.servers`，loader 能正确反序列化 |
| R2 | 同名 server 覆盖 | case 级 `mcp.servers[].name` 与 eval 级 server 同名时，整项替换 eval 级 server |
| R3 | 默认继承 | case 未声明 `mcp` 或未覆盖某个 server 时，继承 eval 级 MCP 配置 |
| R4 | mocked fixture 按 case 生效 | 两个 case 使用同名 server 和同名 tool 时，可以返回不同 fixture 数据 |
| R5 | 并行安全 | `cases.parallelism > 1` 时，不同 case 的 mock 响应不会互相污染 |
| R6 | 向后兼容 | 现有只使用 eval 级 `mcp` 的 eval 不需要修改 |

### 应该有

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| S1 | 清晰错误信息 | case 覆盖不存在名称、缺少 `name`、非 mocked 覆盖等情况给出带 case ID 的错误 |
| S2 | 文档示例 | `docs/guide/writing-evals.md` 与 `docs/zh/guide/writing-evals.md` 展示 per-case mocked MCP 覆盖 |
| S3 | skill template/reference 更新 | `skills/skill-upper` 的模板或参考说明包含 case 级 mocked MCP 用法 |

### 最好有

| ID | 需求 | 验收标准 |
| --- | --- | --- |
| N1 | 报告可追踪 | 调试日志或报告元数据可显示 case 使用的 MCP `config_ref`，但不泄露 secret |
| N2 | 配置去重缓存 | 如果多个 case 的有效 MCP 配置完全相同，可以复用解析结果，但不能共享可变 runtime 状态 |

## 提案

### 用户场景速查

#### 场景 1：同一个项目管理 MCP，不同 case 返回不同项目状态

`eval.yaml` 中声明默认 mocked MCP：

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: codex

mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-default.yaml

cases:
  files:
    - evals/cases/project-open.yaml
    - evals/cases/project-closed.yaml
```

第一个 case 覆盖为“项目打开”：

```yaml
id: project-open
title: 项目打开时应继续执行发布计划

input:
  prompt: Check project status and continue the publish plan.

mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-open.yaml

judge:
  type: rule_based
  success:
    - output_contains:
        any: ["continue", "publish plan"]
```

第二个 case 覆盖为“项目关闭”：

```yaml
id: project-closed
title: 项目关闭时应停止发布计划

input:
  prompt: Check project status and decide whether to continue.

mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-closed.yaml

judge:
  type: rule_based
  success:
    - output_contains:
        any: ["stop", "closed", "cannot continue"]
```

两个 fixture 都定义同一个 tool：

```yaml
tool_responses:
  get_project:
    default:
      id: "p-001"
      status: "OPEN"
```

```yaml
tool_responses:
  get_project:
    default:
      id: "p-001"
      status: "CLOSED"
```

关键点：

- Skill 看到的 MCP server 仍叫 `project-mgmt`。
- Skill 调用的 tool 仍叫 `get_project`。
- 不同 case 只替换 mocked fixture，不需要拆 eval 文件。

#### 场景 2：只覆盖一个 server，其他 server 继续继承

```yaml
# eval.yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-default.yaml
    - name: filesystem
      mode: mocked
```

```yaml
# cases/project-error.yaml
id: project-error
input:
  prompt: Inspect the project and summarize available files.

mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-error.yaml
```

有效 MCP 配置为：

```yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-error.yaml
    - name: filesystem
      mode: mocked
```

### Schema 形态

在 `CaseConfig` 上增加一个可选字段：

```go
type CaseConfig struct {
    ID          string      `yaml:"id"`
    Title       string      `yaml:"title"`
    Description string      `yaml:"description"`
    Tag         string      `yaml:"tag"`
    Input       Input       `yaml:"input"`
    Context     Context     `yaml:"context"`
    Constraints Constraints `yaml:"constraints"`
    Expect      Expect      `yaml:"expect"`
    Judge       JudgeConfig `yaml:"judge,omitempty"`
    MCP         MCPConfig   `yaml:"mcp,omitempty"`
    CollectArtifacts []string `yaml:"collect_artifacts,omitempty"`
}
```

选择 `mcp` 而不是 `mcp_overrides` 的原因：

1. 复用现有 `MCPConfig` / `MCPServer` 结构，减少新概念。
2. 用户可以直接复制 eval 级 `mcp.servers` 片段到 case 中修改。
3. 未来如果要支持 case 级 real MCP 或新增 server，不需要再迁移字段名。

MVP 的校验约束是：case 级 `mcp.servers` 只能声明 `mode: mocked` 的 server。这样可以把本提案的安全和兼容边界收窄到“mock fixture 变化”，避免引入 per-case real MCP 凭据、网络策略和外部服务差异。

### 合并语义

有效 MCP 配置按如下规则计算：

1. 以 eval 级 `mcp.servers` 为基础，保持原有顺序。
2. 对 case 级 `mcp.servers` 逐项处理：
   - 如果 `name` 与 eval 级 server 相同，则用 case 级 server **整项替换** eval 级 server。
   - 如果 `name` 不存在于 eval 级 server，则追加到末尾。
3. 整项替换不是深度合并。case 覆盖同名 server 时，需要写出完整的 `name`、`mode`、`config_ref` 等必要字段。
4. 同一层级内不允许重复 server `name`。
5. 未声明 case 级 `mcp` 的 case 等价于使用 eval 级 MCP 配置。

整项替换比字段级深度合并更容易审查，也避免出现 eval 级 `transport`、`command`、`endpoint` 与 case 级 `config_ref` 意外拼接的隐式行为。

### 运行时行为

Evaluator 在准备每个 case runtime 时计算该 case 的有效 MCP 配置：

```text
caseCfg + evalCfg.MCP
        |
        v
mergeEffectiveMCPConfig(evalMCP, caseMCP)
        |
        v
mcp.Provisioner{SkillDir}.Provision(effectiveMCP)
        |
        v
runtime env + runtime.MCPConfig
        |
        v
ag.InstallMCP(ctx, rt, mcpCfg)
```

与当前实现相比，核心变化是 `provisionMCPConfig()` 不再只用 `sync.Once` 解析一次全局 `evalCfg.MCP`。对于有 case 级覆盖的场景，Provisioner 必须在 case 维度运行，确保 mocked server 脚本嵌入的是该 case 的 fixture。

### 注意事项/约束/说明

1. `config_ref` 仍相对 Skill 目录解析，而不是相对 case 文件所在目录解析。
2. case 级覆盖同名 server 时，server `name` 不变，因此 Skill 不需要修改 MCP 调用逻辑。
3. mocked MCP server 仍由 `internal/mcp` 生成本地 stdio Node 脚本；本提案不改变 MCP 协议层。
4. 如果 case 覆盖的 fixture 文件不存在，错误应指向 case ID、server name 和原始 `config_ref`。
5. 如果多个 case 使用完全相同的 fixture，允许重复解析；这是简单且安全的默认实现。

### 风险与缓解措施

| 风险 | 影响 | 缓解措施 |
| --- | --- | --- |
| 合并语义不清导致误用 | 用户以为字段会深度合并 | 明确采用整项替换，并在文档中给出“需要写完整 server 配置”的示例 |
| case 级 real MCP 引入外部差异 | 凭据、网络、并行稳定性变复杂 | MVP 校验只允许 case 级 mocked MCP |
| 并行 case mock 响应串线 | 评测不稳定或误判 | 每个 case 独立 runtime、独立 `InstallMCP`、不共享 mock server 进程 |
| 全局 `sync.Once` 缓存继续复用旧配置 | case 覆盖不生效 | 将 MCP provision 入口改为 case-aware，必要时仅缓存 eval-only 快路径 |
| 错误信息定位不足 | 用户难以找到坏 fixture | 错误包装包含 `case <id>`、`mcp server <name>`、`config_ref <path>` |

## 设计细节

### Schema 变更

在 `internal/config/schema.go` 中为 `CaseConfig` 增加：

```go
MCP MCPConfig `yaml:"mcp,omitempty"`
```

`MCPConfig` 和 `MCPServer` 结构本身不需要新增字段。这样 case YAML 可以直接复用：

```yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-open.yaml
```

`internal/config/loader_test.go` 需要增加 case 级 `mcp` 的反序列化测试，确认 `CaseConfig.MCP.Servers` 能读取 `name`、`mode`、`config_ref`。

### 有效 MCP 配置计算

新增一个纯函数，建议放在 `internal/config` 或 `internal/evaluator`，用于把 eval 级和 case 级 MCP 合并：

```go
func MergeCaseMCP(evalMCP config.MCPConfig, caseMCP config.MCPConfig) (config.MCPConfig, error)
```

建议规则：

1. 如果 `caseMCP.Servers` 为空，返回 eval 级 MCP 的深拷贝。
2. 构建 `name -> index` 映射，保留 eval 级顺序。
3. 校验 eval 级和 case 级各自不含重复 `name`。
4. 遍历 case 级 servers：
   - `name` 为空时报错。
   - `mode` 必须是 `mocked`。
   - 同名则替换对应 index。
   - 不同名则 append。
5. 返回全新的 `MCPConfig`，避免调用方修改原始配置。

是否允许 case 追加一个 eval 级不存在的 mocked server，可以保留。这样一些 case 能临时注入额外 mocked 工具，而不会强迫所有 case 都安装该 server。

### Evaluator 改造

当前路径：

```go
func (e *defaultEvaluator) prepareRuntimeForCase(...) (runtime.Runtime, error) {
    rtCfg := e.evalCfg.Environment.ToRuntimeConfig()
    mcpCfg, mcpEnv, err := e.provisionMCPConfig()
    ...
    setupCaseEnvironment(..., mcpCfg)
}

func (e *defaultEvaluator) provisionMCPConfig() (...) {
    e.mcpOnce.Do(func() {
        provisioner.Provision(e.evalCfg.MCP)
    })
}
```

建议调整为：

```go
func (e *defaultEvaluator) prepareRuntimeForCase(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    ag agent.Agent,
) (runtime.Runtime, error) {
    rtCfg := e.evalCfg.Environment.ToRuntimeConfig()
    mcpCfg, mcpEnv, err := e.provisionMCPConfigForCase(caseCfg)
    if err != nil {
        return nil, err
    }
    rtCfg.Env = mergeEnvMaps(rtCfg.Env, mcpEnv)
    ...
}
```

`provisionMCPConfigForCase` 负责：

1. 调用 `MergeCaseMCP(e.evalCfg.MCP, caseCfg.MCP)`。
2. 用 `mcp.Provisioner{SkillDir: skillDir}` 解析有效 MCP 配置。
3. 包装错误，包含 case ID。
4. 返回 runtime MCP 配置和需要注入 runtime 的 env。

可以保留 eval-only 快路径缓存：

- 如果所有 case 都没有 `caseCfg.MCP.Servers`，沿用现有 `sync.Once`。
- 如果当前 case 有覆盖，则不使用全局缓存。

但初版实现也可以直接每个 case 都 provision 一次。MCP provision 主要是读 YAML 和生成内联脚本，成本远小于 Agent 运行成本；简单实现更不容易出错。

### Provisioner 与路径解析

`internal/mcp.Provisioner` 已经通过 `SkillDir` 解析 `config_ref`：

```go
func (p Provisioner) resolveConfigRef(configRef string) string {
    if filepath.IsAbs(configRef) || p.SkillDir == "" {
        return filepath.Clean(configRef)
    }
    return filepath.Join(p.SkillDir, configRef)
}
```

本提案不改变这段逻辑。case 级 `config_ref` 与 eval 级 `config_ref` 使用同一语义：

- 相对路径：相对 Skill 目录。
- 绝对路径：清理后直接使用。

这点需要在 `docs/guide/writing-evals.md` 和中文文档中明确，避免用户以为 case 文件旁边的相对路径会生效。

### 并行执行隔离

`prepareRuntimeForCase()` 本来就是按 case 创建 runtime。只要 MCP provision 和 `InstallMCP` 发生在 case 维度，就能自然满足并行隔离：

1. case A 和 case B 各自计算有效 MCP 配置。
2. case A 和 case B 各自生成 mocked MCP Node 脚本。
3. Agent 在各自 runtime 中安装 MCP。
4. mock server 进程由 Agent 在该 runtime/session 内启动，不共享内存。

需要避免的实现是：把 mocked `tool_responses` 解析成全局可变 map，然后让多个 case 的 mock server 引用同一个 map。当前实现把 fixture JSON 嵌入 Node 脚本文本，天然适合 per-case 隔离。

### 错误与校验

`ValidateCaseConfig` 增加 case 级 MCP 校验：

1. `mcp.servers[].name` 必填。
2. `mcp.servers[].mode` 必须是 `mocked`。
3. 同一 case 的 `mcp.servers` 不允许重复 `name`。
4. mocked server 不允许声明 `transport: http`、`endpoint`、`command`、`args`；这些校验可复用 Provisioner 现有错误，也可以在 validator 提前报出。

`MergeCaseMCP` 或 evaluator 包装错误时建议包含：

```text
case project-open: failed to provision MCP servers: failed to load mcp server "project-mgmt" config_ref evals/fixtures/mcp/project-open.yaml: no such file or directory
```

### 文档与模板

需要更新：

1. `docs/guide/writing-evals.md`：MCP 配置章节增加 per-case mocked MCP 覆盖示例。
2. `docs/zh/guide/writing-evals.md`：同步中文说明。
3. `skills/skill-upper/assets/eval.yaml.tmpl` 或相关 reference：提示默认 MCP 写在 eval，case 可覆盖 mocked fixture。
4. 如有 eval schema/reference 文档，补充 `CaseConfig.mcp` 字段。

## 测试计划

### 单元测试

1. `internal/config/loader_test.go`
   - case YAML 中声明 `mcp.servers` 能正确加载。
   - 缺省 `mcp` 时 `CaseConfig.MCP.Servers` 为空。

2. `internal/config/validator_test.go`
   - case 级 mocked MCP 合法。
   - case 级 real MCP 在 MVP 中报错。
   - case 级 server 缺少 `name` 报错。
   - case 级重复 server name 报错。

3. `internal/evaluator` 或 `internal/config` 的 merge 测试
   - 无 case MCP 时完整继承 eval MCP。
   - 同名 server 整项替换，并保持原顺序。
   - 新 server append 到末尾。
   - 替换不会修改原始 eval/case config。

4. `internal/mcp/provisioner_test.go`
   - case 级 `config_ref` 仍相对 SkillDir 解析。
   - 同名 mocked server 使用不同 fixture 时生成的 runtime `Args` 中包含不同 fixture JSON。

5. `internal/evaluator/evaluator_test.go`
   - fake agent 的 `InstallMCP` 记录每个 case 收到的 runtime MCP 配置。
   - 两个 case 同名 server 不同 `config_ref` 时，fake agent 看到不同配置。
   - `cases.parallelism` 大于 1 时配置不串线。

### 集成/E2E 测试

新增一个小型 fixture，例如：

```text
e2e/testdata/mcp-case-overrides/
  SKILL.md
  evals/eval.yaml
  evals/cases/project-open.yaml
  evals/cases/project-closed.yaml
  evals/fixtures/mcp/project-open.yaml
  evals/fixtures/mcp/project-closed.yaml
```

两个 case 都调用 `project-mgmt.get_project`，但 fixture 返回不同 `status`。断言最终输出分别包含 `OPEN` 和 `CLOSED` 或对应行为关键词。

如果真实 Agent E2E 成本较高，可以先添加 build-tag gated E2E；常规单元测试使用 fake agent 覆盖核心合并和安装路径。

### 手动验证

运行：

```bash
make fmt
make verify
make test
go test -tags e2e -v ./e2e -run TestMCP_CaseMockOverrides
```

若未触及 `e2e/` 或真实 Agent 测试资源不可用，至少保证 `make verify` 和 `make test` 通过。

## 缺点

1. 在 case 中重复写完整 `mcp.servers` 项会比 `mcp_overrides.project-mgmt.config_ref` 稍长。
2. MVP 限定 mocked MCP，用户不能立即用同一机制覆盖 real MCP endpoint 或 credentials。
3. 每个 case 都 provision MCP 会多读几次 fixture 文件，不过相对 Agent 执行成本可以忽略。
4. `config_ref` 相对 SkillDir 而不是 case 文件目录，虽然与现有语义一致，但需要文档强调。

## 替代方案

### 替代方案 A：新增 `mcp_overrides`

示例：

```yaml
mcp_overrides:
  project-mgmt:
    config_ref: evals/fixtures/mcp/project-open.yaml
```

优点是写法短，意图聚焦 mock fixture。缺点是会引入第二套 MCP 配置语义，需要定义字段级合并规则，也不利于未来扩展到追加 server 或覆盖更多 server 字段。

### 替代方案 B：在 fixture 文件内部定义多套响应

示例：

```yaml
tool_responses:
  get_project:
    cases:
      project-open:
        status: OPEN
      project-closed:
        status: CLOSED
```

优点是 eval 级 MCP 配置不变。缺点是 mock server 需要知道当前 case ID，MCP fixture 与 eval runner 强耦合，也会让 fixture 文件膨胀。

### 替代方案 C：要求用户拆分 eval 文件

这是当前可行的绕过方案。缺点是重复配置多，报告分散，benchmark 对比困难，不符合 issue #133 中“同一 eval 内覆盖不同 mock 响应”的需求。

### 替代方案 D：自动按 case 文件目录解析 fixture

可以让 case 中的 `config_ref` 相对 case 文件目录解析。优点是局部性强；缺点是与现有 `config_ref` 相对 SkillDir 的规则不一致，会制造两套路径心智模型。本提案不采用。

## 所需基础设施

不需要新增外部服务或第三方依赖。

实现只需要修改现有 Go 代码、测试 fixture、文档和 skill template/reference。mocked MCP 继续依赖当前 Node stdio mock server 生成逻辑。

## 升级与迁移策略

该变更向后兼容：

1. 现有 eval 级 `mcp` 配置继续可用。
2. 现有 case 文件不需要增加字段。
3. 未声明 case 级 `mcp` 时，行为与当前版本一致。
4. 新增能力是可选增强；用户可以逐步把需要变化 fixture 的 case 迁移到 case 级 `mcp`。

文档发布时应明确：

- case 级 MCP 覆盖是 SUP-0003 引入的新能力。
- MVP 支持 mocked MCP 覆盖。
- 如果用户需要 real MCP per-case 覆盖，应另开提案讨论安全、网络和凭据边界。
