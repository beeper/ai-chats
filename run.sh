#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

usage() {
  echo "Usage: ./run.sh"
}

if [[ $# -ne 0 ]]; then
  usage
  exit 1
fi

exec go run ./cmd/ai
