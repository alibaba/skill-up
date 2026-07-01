# skill-up runner image for the GitHub Action (public github.com / cloud runner).
#
# Bakes the agent engines + skill-up + a python3 interpreter + the action
# code, so the action itself is a thin Docker container action that just runs.
# Built and pushed to ghcr.io by .github/workflows/build-runner-image.yml.
#
# Everything installed here is from PUBLIC registries (npm / GitHub Releases) —
# no internal Alibaba network is required, so this builds on github.com hosted
# runners with GITHUB_TOKEN. (The internal Aone variant bakes from
# hub.docker.alibaba-inc.com instead; the two images are intentionally separate.)
# node:22 — @qwen-code/qwen-code declares engines.node ">=22.0.0"; the older
# node:20 base tripped an engine mismatch at `qwen --version`. 22 also suits
# claude-code / codex and matches skill-up's own Node bootstrap default.
FROM node:22-bookworm-slim

# Pin the agent CLI versions. Bump these in a PR; the build workflow republishes
# the image. Keep codex aligned with the codexDefaultVersion skill-up expects.
ARG CLAUDE_VERSION=latest
ARG CODEX_VERSION=0.80.0
ARG QWEN_CODE_VERSION=latest
ARG SKILL_UP_VERSION=latest

# python3 (for main.py) + curl/bash/git/ca-certificates/unzip (installers).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        python3 curl bash git ca-certificates unzip \
    && rm -rf /var/lib/apt/lists/*

# Agent engines.
#   claude_code -> @anthropic-ai/claude-code (npm, CLI binary: claude)
#   codex       -> @openai/codex             (npm, CLI binary: codex)
#   qwen_code   -> @qwen-code/qwen-code       (npm, CLI binary: qwen)
RUN npm install -g \
        "@anthropic-ai/claude-code@${CLAUDE_VERSION}" \
        "@openai/codex@${CODEX_VERSION}" \
        "@qwen-code/qwen-code@${QWEN_CODE_VERSION}" \
    && claude --version \
    && codex --version \
    && qwen --version

#   qodercli -> official installer (qoder.com/install), NOT npm: the npm
#   @qoder-ai/qodercli package is a different release line (1.x) whose flags
#   don't match skill-up's adapter (`--permission-mode=bypass_permissions -p`,
#   built against the installer's 0.x line) — it no-ops silently. The installer
#   drops the binary under $HOME/.qoder and symlinks $HOME/.local/bin; symlink
#   it into /usr/local/bin because $HOME differs at Actions runtime
#   (/github/home, not /root).
RUN curl -fsSL https://qoder.com/install | bash \
    && ln -sf /root/.local/bin/qodercli /usr/local/bin/qodercli \
    && qodercli --version

# skill-up — public GitHub Releases via the official installer.
RUN INSTALL_DIR=/usr/local/bin \
    SKILL_UP_VERSION="${SKILL_UP_VERSION}" \
    bash -c 'if [ "$SKILL_UP_VERSION" = "latest" ]; then unset SKILL_UP_VERSION; fi; \
             curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash' \
    && skill-up --version

# Action code.
COPY entry.sh main.py /action/
RUN chmod +x /action/entry.sh

ENTRYPOINT ["/action/entry.sh"]
