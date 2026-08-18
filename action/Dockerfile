# skill-up runner image for the GitHub Action (public github.com / cloud runner).
#
# Bakes the agent engines + skill-up + a python3 interpreter + the action
# code, so the action itself is a thin Docker container action that just runs.
# Built and pushed to ghcr.io by .github/workflows/runner-image.yml.
#
# Everything installed here is from PUBLIC registries (npm / GitHub Releases) —
# no internal Alibaba network is required, so this builds on github.com hosted
# runners with GITHUB_TOKEN. (The internal Aone variant bakes from
# hub.docker.alibaba-inc.com instead; the two images are intentionally separate.)
# node:22 — @qwen-code/qwen-code declares engines.node ">=22.0.0"; the older
# node:20 base tripped an engine mismatch at `qwen --version`. 22 also suits
# claude-code / codex and matches skill-up's own Node bootstrap default.
FROM node:26-bookworm-slim@sha256:cd565714d4da3e84bfd341e31448f81d47c6362198f152345297c9c1154e6341

# Pin the agent CLI versions. Bump these in a PR; the build workflow republishes
# the image. Keep codex aligned with the codexDefaultVersion skill-up expects.
ARG CLAUDE_VERSION=2.1.217
ARG CODEX_VERSION=0.80.0
ARG QWEN_CODE_VERSION=0.20.1
ARG QODER_VERSION=1.1.21
ARG QODER_SHA256=f979e907faa7ec5d74759c4cf87f2357d4f82eab35dadf98692a5a43c1055cdb
ARG SKILL_UP_VERSION=0.7.0
ARG SKILL_UP_INSTALL_COMMIT=5ac7ce0467a164d07aacbbf7052bcffda68a446b
ARG SKILL_UP_INSTALL_SHA256=2b7fbea303dc8b6feb09db6f9cc7307fb9263ff6cc4b0c68cdfe9c2897d109ef
ARG VCS_REF=unknown

LABEL org.opencontainers.image.source="https://github.com/alibaba/skill-up" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.version="${SKILL_UP_VERSION}"

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

# qodercli: download the same release validated by extended CI and verify the
# vendor-published SHA-256 before extraction. Do not execute the mutable remote
# installer, which resolves its release from a live channel manifest.
RUN tmp_dir="$(mktemp -d)" \
    && curl -fsSL --retry 3 \
        "https://qoder-ide.oss-accelerate.aliyuncs.com/qodercli/releases/${QODER_VERSION}/qodercli-linux-x64.tar.gz" \
        -o "$tmp_dir/qodercli.tar.gz" \
    && echo "${QODER_SHA256}  $tmp_dir/qodercli.tar.gz" | sha256sum -c - \
    && tar -xzf "$tmp_dir/qodercli.tar.gz" -C /usr/local/bin qodercli \
    && chmod 0755 /usr/local/bin/qodercli \
    && rm -rf "$tmp_dir" \
    && qodercli --version

# skill-up: pin both the installer source commit and installer checksum. The
# installer then downloads the pinned release asset and verifies its published
# checksum before copying the binary.
RUN curl -fsSL --retry 3 \
        "https://raw.githubusercontent.com/alibaba/skill-up/${SKILL_UP_INSTALL_COMMIT}/install.sh" \
        -o /tmp/install-skill-up.sh \
    && echo "${SKILL_UP_INSTALL_SHA256}  /tmp/install-skill-up.sh" | sha256sum -c - \
    && INSTALL_DIR=/usr/local/bin SKILL_UP_VERSION="${SKILL_UP_VERSION}" \
        bash /tmp/install-skill-up.sh \
    && rm -f /tmp/install-skill-up.sh \
    && skill-up --version

# Action code.
COPY entry.sh main.py /action/
RUN chmod +x /action/entry.sh

ENTRYPOINT ["/action/entry.sh"]
