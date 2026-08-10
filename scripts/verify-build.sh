#!/usr/bin/env bash
# Checks that make gui-build produces a valid ELF binary.
# Usage: ./scripts/verify-build.sh [BIN_PATH]
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${1:-${REPO_ROOT}/bin/perch}"

echo "==> Running make gui-build …"
make -C "${REPO_ROOT}" gui-build

echo "==> Checking ${BIN} …"
[[ -f "${BIN}" ]] || { echo "ERROR: binary not found at ${BIN}" >&2; exit 1; }

FILE_OUTPUT="$(file "${BIN}")"
echo "    ${FILE_OUTPUT}"
echo "${FILE_OUTPUT}" | grep -q "ELF"       || { echo "ERROR: not ELF" >&2; exit 1; }
echo "${FILE_OUTPUT}" | grep -q "executable" || { echo "ERROR: not executable" >&2; exit 1; }

if ldd "${BIN}" 2>/dev/null | grep -qi tmux; then
  echo "ERROR: binary links tmux (should be removed)" >&2; exit 1
fi

echo "==> PASS: ${BIN} is a valid ELF executable."
