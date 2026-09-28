# Install, upgrade, and troubleshoot

There are two separate components:

- `skill-up`: the CLI binary that runs evaluations.
- An observation host plugin: an optional Codex or DSH integration for capturing and reviewing real Skill usage. Ordinary evaluations do not require a plugin.

`skill-up` is distributed as a prebuilt standalone binary through [GitHub Releases](https://github.com/alibaba/skill-up/releases). The official installer does not require Go, Python, or Node at runtime.

> **Platforms:** macOS and Linux only. Windows is not currently supported.

## Official installer (macOS and Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

The script detects the OS (`darwin` or `linux`) and architecture (`amd64` or `arm64`), downloads the matching release archive and checksums, installs to `~/.local/bin/skill-up` by default, and verifies the checksum when `sha256sum` or `shasum` is available.

### Version and installation directory

```bash
# Pin a version (vX.Y.Z or X.Y.Z; the script normalizes the tag).
export SKILL_UP_VERSION=v0.1.0
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

# Choose a different directory.
export INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

## Verify the installation

```bash
skill-up --version
skill-up --help
```

## Install an observation host plugin (optional)

Install a plugin only when the user requests observation capture or review. The repository does not currently publish these packages through a public marketplace or npm. Both self-contained archives come from [GitHub Releases](https://github.com/alibaba/skill-up/releases). The examples use `0.13.0`; replace it with the desired release version.

### Codex (`codex-skill-up`)

Requires a current Codex CLI or ChatGPT desktop app with plugin and lifecycle hook support, plus `python3`. The Codex IDE extension does not support this plugin.

If the old local plugin named `skill-up-observer` was installed from a checkout, uninstall or disable it in the Plugins Directory before installing `codex-skill-up` so both sets of hooks do not run. The existing `$CODEX_HOME/plugin-data/skill-up-observer` directory remains intact.

Codex discovers local plugins through a marketplace; it cannot install the tarball directly. Download and extract it into a separate local marketplace:

```bash
export SKILL_UP_VERSION=0.13.0
export SKILL_UP_MARKETPLACE="$HOME/.local/share/skill-up-marketplace"

mkdir -p "$SKILL_UP_MARKETPLACE/plugins" "$SKILL_UP_MARKETPLACE/.agents/plugins"
curl -fL \
  "https://github.com/alibaba/skill-up/releases/download/v${SKILL_UP_VERSION}/codex-skill-up_${SKILL_UP_VERSION}.tar.gz" \
  -o /tmp/codex-skill-up.tar.gz
tar -xzf /tmp/codex-skill-up.tar.gz -C "$SKILL_UP_MARKETPLACE/plugins"
```

Create `$SKILL_UP_MARKETPLACE/.agents/plugins/marketplace.json`:

```json
{
  "name": "skill-up-local",
  "interface": {
    "displayName": "Skill Up Local"
  },
  "plugins": [
    {
      "name": "codex-skill-up",
      "source": {
        "source": "local",
        "path": "./plugins/codex-skill-up"
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

Register and inspect the marketplace:

```bash
codex plugin marketplace add "$SKILL_UP_MARKETPLACE"
codex plugin marketplace list
```

Restart the ChatGPT desktop app, select **Skill Up Local** in the Plugins Directory, install **codex-skill-up**, and review and trust its hooks when prompted. In a new conversation, verify that `skill-upper` and tools such as `mark_skill_invocation` and `record_skill_feedback` are available.

### DeepSeek Harness (DSH)

Requires `dsh` 0.1.5-rc.2 or later, `pnpm`, and `skill-up` on `PATH`.

```bash
export SKILL_UP_VERSION=0.13.0
curl -fL \
  "https://github.com/alibaba/skill-up/releases/download/v${SKILL_UP_VERSION}/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz" \
  -o "/tmp/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz"
dsh plugin --profile web add "/tmp/alibaba-dsh-skill-up-${SKILL_UP_VERSION}.tgz"
dsh --profile web --dump-config
```

Installation alone does not opt in to capture. For the observation workflow, explicitly configure the plugin in the profile's `cordis.patch.yml`:

```yaml
- id: skill-up
  name: '@alibaba/dsh-skill-up'
  config:
    skillUpBin: skill-up
    observer:
      enabled: true
```

Restart that DSH profile and verify the observation tools. If only evaluation runs are needed, leave `observer.enabled: false`.

## Upgrade

Run the installer again to replace the old binary. Set `SKILL_UP_VERSION` first to pin a release.

## Troubleshooting

### `command not found: skill-up`

Usually `~/.local/bin` is missing from `PATH`.

**macOS (zsh):**

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Linux (bash):**

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### Download fails or network is restricted

- Configure a proxy: `export HTTPS_PROXY=http://your-proxy:port`.
- Or download the matching `skill-up_*_*.tar.gz` and `checksums.txt` from [Releases](https://github.com/alibaba/skill-up/releases), extract the binary into a directory on `PATH`, and run `chmod +x` on it.

### macOS says it cannot verify the developer

```bash
xattr -d com.apple.quarantine "$(which skill-up)"
```

Alternatively, allow it in System Settings → Privacy & Security.

### Build from source

For development against the repository's current schema:

```bash
make build
```
