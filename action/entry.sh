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

set -e

for c in python3 python /usr/bin/python3 /usr/local/bin/python3; do
  if command -v "$c" >/dev/null 2>&1 || [ -x "$c" ]; then
    exec "$c" "$DIR/main.py" "$@"
  fi
done

echo "[entry] no python interpreter found" >&2
exit 1
