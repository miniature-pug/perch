#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# containers/run.sh <image> <cmd...>: runs a command inside a perch container.
#
# This is the single definition of how perch runs in a container. Every
# `make` target reuses it. <image> is the short name under containers/, for
# example `dev` maps to perch-dev:latest. Everything after it is the command
# to run.
#
#   bash containers/run.sh dev make CONTAINERIZE=0 test
#   bash containers/run.sh dev bash            # interactive shell
#
# Masks (anonymous volumes) keep the host tree clean. An anonymous volume
# seeds from the IMAGE layer at that path. The image holds only the
# toolchain, so a masked path stays empty unless the run writes to it:
#   - frontend/node_modules : always masked. The host copy is built for the
#     host OS, so the container runs `npm ci` again into the empty volume.
#   - frontend/dist         : masked only when PERCH_MASK_DIST=1, for
#     the frontend-building targets that run `vite build`. Go targets must
#     not mask this path, or `//go:embed frontend/dist` finds an empty
#     directory and the build fails. By default the path stays unmasked: the
#     build reads the committed stub, writes nothing, and the host stays
#     clean.
# ---------------------------------------------------------------------------

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <image> <cmd...>" >&2
  exit 2
fi

image="$1"; shift
# The repo root is the parent of containers/, found without git (a tarball has no .git).
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# An opt-in dist mask, set by test-e2e and gui-build in the Makefile.
# Only the value 1 enables it, so PERCH_MASK_DIST=0 really means "no mask".
mask_dist=""
if [ "${PERCH_MASK_DIST:-0}" = "1" ]; then
  mask_dist="-v /work/frontend/dist"
fi

# Allocate a TTY only when attached to one, so pipelines (no TTY) still work.
tty_flags=""
if [ -t 0 ] && [ -t 1 ]; then
  tty_flags="-it"
fi

# :Z requests an SELinux relabel. On this AppArmor host, the relabel request
# is a harmless no-op: the request touches xattrs only, never file content.
# The go-build cache and the npm download cache persist between runs for
# speed. The npm cache is what lets the per-run `npm ci` into the masked
# node_modules volume skip the network.
exec podman run --rm $tty_flags \
  -v "$ROOT":/work:Z \
  -v /work/frontend/node_modules \
  $mask_dist \
  -v perch-go-build:/root/.cache/go-build \
  -v perch-npm-cache:/root/.npm \
  -w /work \
  "perch-${image}:latest" "$@"
