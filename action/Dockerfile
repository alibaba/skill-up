# skill-up runner image for the GitHub Action (public github.com / cloud runner).
#
# Bakes the three agent engines + skill-up + a python3 interpreter + the action
# code, so the action itself is a thin Docker container action that just runs.
# Built and pushed to ghcr.io by .github/workflows/build-runner-image.yml.
#
# Everything installed here is from PUBLIC registries (npm / GitHub Releases) —
# no internal Alibaba network is required, so this builds on github.com hosted
# runners with GITHUB_TOKEN. (The internal Aone variant bakes from
# hub.docker.alibaba-inc.com instead; the two images are intentionally separate.)
FROM node:20-bookworm-slim

# Pin the agent CLI versions. Bump these in a PR; the build workflow republishes
# the image. Keep codex aligned with the codexDefaultVersion skill-up expects.
ARG CLAUDE_VERSION=latest
ARG CODEX_VERSION=0.80.0
ARG QODER_VERSION=latest
ARG SKILL_UP_VERSION=latest

# python3 (for main.py) + curl/bash/git/ca-certificates (for skill-up install.sh).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        python3 curl bash git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Agent engines — all public npm packages.
#   claude_code -> @anthropic-ai/claude-code  (CLI binary: claude)
#   codex       -> @openai/codex              (CLI binary: codex)
#   qodercli    -> @qoder-ai/qodercli         (CLI binary: qodercli)
RUN npm install -g \
        "@anthropic-ai/claude-code@${CLAUDE_VERSION}" \
        "@openai/codex@${CODEX_VERSION}" \
        "@qoder-ai/qodercli@${QODER_VERSION}" \
    && claude --version \
    && codex --version \
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
