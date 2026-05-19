# 安装 / 升级 / 排错

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

