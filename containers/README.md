# perch containers

The container framework. perch treats **containers as first-class**: there is
**one image — `perch-dev`, the dev/test environment** — and every check (Go,
frontend, e2e, lint, vuln, GUI build) runs **inside it**. Identical bits
everywhere → identical results everywhere. Full rationale:
[`docs/superpowers/specs/2026-06-04-perch-container-framework-design.md`](../docs/superpowers/specs/2026-06-04-perch-container-framework-design.md).

## Layout

```
containers/
├── README.md        # this file
├── run.sh           # generic exec: run a command in an image (bind-mount, masks, caches)
└── dev/
    └── Containerfile # the perch-dev image (the dev/test environment)
# future siblings: containers/smoke/  containers/ci/  containers/shared/
```

## The one front door: `make`

You never call `podman` by hand. The `Makefile` is the single entrypoint.

```sh
make image              # build perch-dev (versions sourced from .tool-versions / Makefile)
make test               # Go unit tests, in the container
make test-integration   # Go integration tests (-tags=integration)
make test-front         # frontend typecheck (tsc) + unit (vitest)
make test-e2e           # Playwright (chromium) — the reason this framework exists
make lint vet vulncheck # Go quality gates
make test-all           # everything, each gate in its own container with the right masks
make shell              # interactive shell inside perch-dev
```

The host (Ubuntu 26.04) is newer than anything Playwright 1.60.0 supports
natively, so e2e *must* run in the container — and once one check runs in a
container, all of them do, for consistency.

## How it works

### `CONTAINERIZE` (local ≙ pipeline)

One toggle makes every target work in both worlds:

- **`CONTAINERIZE=1`** (default): the target re-enters the image —
  `run.sh dev make CONTAINERIZE=0 <target>`.
- **`CONTAINERIZE=0`**: the target runs natively (used when we are *already*
  inside the image, or in a pipeline that has already pulled it).

A pipeline is therefore just "run the image, call `make` inside it." No second
test entrypoint, no docker-in-docker.

### Host stays clean (artifact masks)

`run.sh` bind-mounts the repo read-write, then masks paths with **anonymous
volumes** so a container run never mutates the working tree. An anonymous volume
seeds from the *image layer* (toolchain-only → empty), not the host bind-mount:

- **`frontend/node_modules`** — masked always; the container re-runs `npm ci`
  into the empty volume (the host copy is built for the host distro).
- **`frontend/dist`** — masked **only** for frontend-building targets
  (`test-e2e` and `gui-build`), via `PERCH_MASK_DIST=1`. Those run `vite
  build`, which would otherwise clobber the tracked `//go:embed frontend/dist`
  stub. Go targets must **not** mask it, or the embed finds an empty dir and
  fails to compile. Both `test-e2e` and `gui-build` export `PERCH_MASK_DIST=1`
  in the Makefile, so the flag is set automatically.

Named cache volumes (`perch-go-build`, the baked Playwright browsers) persist
across runs for speed.

## WebKit / GUI build

Production GUI builds link **webkit2gtk-4.1** (`-tags "production webkit2_41"`,
set in the `Makefile`): the container base (Ubuntu 24.04 noble) ships only the
4.1 dev package, and the host has it natively too, so host and container link
identically. The image bakes GTK3 + WebKit2GTK-4.1 + `xvfb` so the same image
covers `gui-build` today and a future headless GUI smoke.

### Verify-first base check

To confirm the from-scratch base can link the GUI (cgo + WebKit), the `gui-build`
surface is exercised in the image directly:

```sh
bash containers/run.sh dev make gui-build CONTAINERIZE=0
```

`gui-build` exports `PERCH_MASK_DIST=1` in the Makefile, which keeps the host
`frontend/dist` stub intact while `vite build` writes the masked volume — no
manual prefix needed.

## devcontainer

`.devcontainer/devcontainer.json` reuses this exact image: its
`initializeCommand` runs `make image` on the host, then attaches to
`perch-dev:latest`. Opening the repo in a devcontainer-aware editor yields the
byte-identical environment `make` uses. Inside it, `CONTAINERIZE=0` so targets
run natively (no nested podman).

> **Caveat (devcontainer only):** because targets run with `CONTAINERIZE=0`
> inside the devcontainer, frontend-building targets (`test-e2e`, `gui-build`,
> `test-all`) run `vite build` directly against the mounted workspace — there is
> no `run.sh` dist mask — so they regenerate `frontend/dist`, including the
> tracked `//go:embed` stub `index.html`. Don't commit the regenerated stub
> (same rule as a host `make gui-build`). The host front door (`make …` with the
> default `CONTAINERIZE=1`) always masks `dist` and never touches the stub.

## Not automated here

The manual WebKit + real-agent + D-Bus single-instance smoke
(`docs/superpowers/smoke-checklist.md`) stays human-gated: real `claude`/
`opencode` binaries, a live WebKit window, and cross-process D-Bus are out of
reach of the headless image today. `containers/smoke/` is the designed path to
chip at it later.
