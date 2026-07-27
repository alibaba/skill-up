#!/usr/bin/env bash
# Docker container action entrypoint.
#
# The image bakes a python3 interpreter plus main.py, so this bootstrap is thin:
# locate a python interpreter and exec main.py, passing the action inputs through
# verbatim as CLI flags (assembled in action.yml's runs.args).

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for p in /usr/local/bin /usr/bin "$HOME/.local/bin"; do
  case ":$PATH:" in
    *":$p:"*) ;;
    *) [ -d "$p" ] && PATH="$p:$PATH" ;;
  esac
done
export PATH

set -euo pipefail

restore_workspace_permissions() {
  local workspace="${GITHUB_WORKSPACE:-}"
  [ -n "$workspace" ] || return 0

  local report_dir="$workspace/skill-up-workspace"
  [ -e "$report_dir" ] || return 0

  # Docker actions run as root and write into the host-mounted workspace.
  # Hand generated reports back to the runner user so the next checkout can
  # clean the workspace on self-hosted runners.
  local owner
  if ! owner="$(stat -c '%u:%g' "$workspace")"; then
    echo "[entry] failed to determine workspace ownership" >&2
    return 1
  fi
  if ! chown -R "$owner" "$report_dir"; then
    echo "[entry] failed to restore report ownership" >&2
    return 1
  fi
  if ! chmod -R u+rwX,go+rX "$report_dir"; then
    echo "[entry] failed to restore report permissions" >&2
    return 1
  fi
}

cleanup() {
  local status=$?
  if ! restore_workspace_permissions && [ "$status" -eq 0 ]; then
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT

for c in python3 python /usr/bin/python3 /usr/local/bin/python3; do
  if command -v "$c" >/dev/null 2>&1 || [ -x "$c" ]; then
    "$c" "$DIR/main.py" "$@"
    exit 0
  fi
done

echo "[entry] no python interpreter found" >&2
exit 1
