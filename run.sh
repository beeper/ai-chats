#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

usage() {
  echo "Usage: ./run.sh ai|codex"
}

if [[ $# -ne 1 ]]; then
  usage
  exit 1
fi

bridge="$1"
case "$bridge" in
  ai|codex) ;;
  *)
    usage
    exit 1
    ;;
esac

if ! ./tools/bridges whoami >/dev/null 2>&1; then
  echo "No Beeper login found. Starting login flow..."
  ./tools/bridges login --env "${BEEPER_ENV:-prod}"
fi

exec ./tools/bridges run "$bridge"
