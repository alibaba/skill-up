# 安装 / 升级 / 排错

这里区分两个独立组件：

- `skill-up`：运行评测的 CLI 二进制。
- observation host plugin：可选的 Codex 或 DSH 插件，用于采集和审核真实
  Skill 使用记录。普通评测不需要安装插件。

`skill-up` 以预编译单二进制发布在 [GitHub Releases](https://github.com/alibaba/skill-up/releases)，无运行时依赖（不需要 Go、Python、Node 等即可使用官方安装脚本）。

> **平台**：仅支持 **macOS / Linux**，暂不支持 Windows。

## 官方安装脚本（macOS / Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

脚本行为概要：

- 识别 OS（darwin / linux）与架构（amd64 / arm64）
- 从 GitHub Releases 下载对应压缩包与校验文件
- 默认安装到 `~/.local/bin/skill-up`
- 可用 `sha256sum` / `shasum` 校验（若本机有相应工具）

### 版本与安装目录

```bash
# 固定版本（可为 vX.Y.Z 或 X.Y.Z，脚本会规范化为带 v 的 tag）
export SKILL_UP_VERSION=v0.1.0
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

# 自定义目录
export INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

## 验证安装

```bash
skill-up --version
skill-up --help
```

## 安装 observation host plugin（可选）

只有在用户要求使用 observation capture/review 时才安装。仓库当前没有接入
公开 marketplace 或 npm；两个自包含安装包都来自
[GitHub Releases](https://github.com/alibaba/skill-up/releases)。以下示例用
`0.13.0`，实际使用时替换为目标 release 版本。

### Codex Observer

要求：支持插件与 lifecycle hooks 的当前 Codex CLI 或 ChatGPT 桌面端，以及
`python3`。Codex IDE extension 不支持该插件。

Codex 的本地插件通过 marketplace 发现，不能直接对 tarball 执行安装。下载并
解压到一个独立的本地 marketplace：

```bash
export SKILL_UP_VERSION=0.13.0
export SKILL_UP_MARKETPLACE="$HOME/.local/share/skill-up-marketplace"

mkdir -p "$SKILL_UP_MARKETPLACE/plugins" "$SKILL_UP_MARKETPLACE/.agents/plugins"
curl -fL \
  "https://github.com/alibaba/skill-up/releases/download/v${SKILL_UP_VERSION}/skill-up-observer_${SKILL_UP_VERSION}.tar.gz" \
  -o /tmp/skill-up-observer.tar.gz
tar -xzf /tmp/skill-up-observer.tar.gz -C "$SKILL_UP_MARKETPLACE/plugins"
```

在 `$SKILL_UP_MARKETPLACE/.agents/plugins/marketplace.json` 写入：

```json
{
  "name": "skill-up-local",
  "interface": {
    "displayName": "Skill Up Local"
  },
  "plugins": [
    {
      "name": "skill-up-observer",
      "source": {
        "source": "local",
        "path": "./plugins/skill-up-observer"
      },
      "policy": {
        "installation": "AVAILABLE",
        "authentication": "ON_INSTALL"
      },
      "category": "Productivity"
    }
  ]
}
```

注册并检查 marketplace：

```bash
codex plugin marketplace add "$SKILL_UP_MARKETPLACE"
codex plugin marketplace list
```

然后重启 ChatGPT 桌面端，在 Plugins Directory 中选择 **Skill Up Local**，安装
**skill-up-observer**，并在提示时审核、信任其 hooks。安装后新建对话验证
`skill-upper` 以及 `mark_skill_invocation`、`record_skill_feedback` 等工具可用。

### DeepSeek Harness（DSH）

要求：`dsh` 0.1.5-rc.2 或更新版本、`pnpm`，以及 PATH 中可用的 `skill-up`。

```bash
export SKILL_UP_VERSION=0.13.0
curl -fL \
  "https://github.com/alibaba/skill-up/releases/download/v${SKILL_UP_VERSION}/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz" \
  -o "/tmp/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz"
dsh plugin --profile web add "/tmp/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz"
dsh --profile web --dump-config
```

安装本身不会同意采集。需要 observation workflow 时，在该 profile 的
`cordis.patch.yml` 中将插件配置显式设为：

```yaml
- id: skill-up
  name: '@alibaba/dsh-skill-up'
  config:
    skillUpBin: skill-up
    observer:
      enabled: true
```

重新启动该 DSH profile，再确认 observation tools 可用。若只需要运行评测，
保持 `observer.enabled: false` 即可。

## 升级

再次执行安装脚本即可覆盖旧二进制（可先设 `SKILL_UP_VERSION` 锁定版本）。

## 排错

### `command not found: skill-up`

通常是 `~/.local/bin` 不在 `PATH`。

**macOS（zsh）**：

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Linux（bash）**：

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### 下载失败 / 网络受限

- 配置代理：`export HTTPS_PROXY=http://your-proxy:port`
- 或从 [Releases](https://github.com/alibaba/skill-up/releases) 手动下载对应 `skill-up_*_*.tar.gz` 与 `checksums.txt`，解压后将二进制放到 PATH 内并 `chmod +x`

### macOS "无法验证开发者"

```bash
xattr -d com.apple.quarantine "$(which skill-up)"
```

或在「系统设置 → 隐私与安全性」中允许。

### 从源码构建

已与仓库 schema 一致，适合开发：

```bash
make build
```
