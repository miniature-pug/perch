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

# The production build must link WebKitGTK 4.1 (the webkit2_41 tag). A build
# without the tag, or against the end-of-life 4.0, fails here instead of at
# the first launch.
# Capture ldd first: under pipefail, grep -q exiting early can SIGPIPE ldd and
# fail the pipeline on a good binary.
LDD_OUTPUT="$(ldd "${BIN}" 2>/dev/null || true)"
if ! grep -q 'libwebkit2gtk-4\.1' <<<"${LDD_OUTPUT}"; then
  echo "ERROR: binary does not link libwebkit2gtk-4.1" >&2; exit 1
fi

echo "==> PASS: ${BIN} is a valid ELF executable."
