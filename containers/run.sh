#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# containers/run.sh <image> <cmd...> — run a command inside a perch container.
#
# The single definition of "how perch runs in a container", reused
# by every `make` target. <image> is the short name under containers/ (e.g.
# `dev` → perch-dev:latest). Everything after it is the command to exec.
#
#   bash containers/run.sh dev make CONTAINERIZE=0 test
#   bash containers/run.sh dev bash            # interactive shell
#
# Masks (anonymous volumes) keep the host tree pristine. An anonymous volume
# seeds from the IMAGE layer at that path — and the image is toolchain-only — so
# a masked path is empty unless the run writes it:
#   - frontend/node_modules : always masked (host copy is built for the host
#     distro; the container re-runs `npm ci` into the empty volume).
#   - frontend/dist         : masked ONLY when PERCH_MASK_DIST is set (the
#     frontend-building targets, which run `vite build`). Go targets must NOT
#     mask it or `//go:embed frontend/dist` finds an empty dir and fails to
#     compile. Default: unmasked → committed stub is read, nothing
#     is written, host stays clean.
# ---------------------------------------------------------------------------

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <image> <cmd...>" >&2
  exit 2
fi

image="$1"; shift
ROOT="$(git rev-parse --show-toplevel)"

# Opt-in dist mask (set by test-e2e / gui-build in the Makefile).
mask_dist=${PERCH_MASK_DIST:+-v /work/frontend/dist}

# Allocate a TTY only when attached to one, so pipelines (no TTY) still work.
tty_flags=""
if [ -t 0 ] && [ -t 1 ]; then
  tty_flags="-it"
fi

# :Z requests an SELinux relabel; on this AppArmor host it is a harmless no-op
# (touches xattrs only, never file content). go-build cache persists for speed.
exec podman run --rm $tty_flags \
  -v "$ROOT":/work:Z \
  -v /work/frontend/node_modules \
  $mask_dist \
  -v perch-go-build:/root/.cache/go-build \
  -w /work \
  "perch-${image}:latest" "$@"
