#!/usr/bin/env bash

set -euo pipefail

gh_version="2.97.0"
gh_linux_amd64_sha256="a2c9b8497e1f85b1ad0dfcb78b5a622e098801b8e461e459e88e1ee12f018112"

: "${RUNNER_TEMP:?RUNNER_TEMP must be set}"
: "${GITHUB_PATH:?GITHUB_PATH must be set}"

archive="gh_${gh_version}_linux_amd64.tar.gz"
archive_path="${RUNNER_TEMP}/${archive}"

curl --fail --location --retry 3 --show-error --silent \
  "https://github.com/cli/cli/releases/download/v${gh_version}/${archive}" \
  --output "$archive_path"
printf '%s  %s\n' "$gh_linux_amd64_sha256" "$archive_path" | sha256sum --check
tar -xzf "$archive_path" -C "$RUNNER_TEMP"

gh_bin_dir="${RUNNER_TEMP}/gh_${gh_version}_linux_amd64/bin"
"$gh_bin_dir/gh" --version
echo "$gh_bin_dir" >> "$GITHUB_PATH"
