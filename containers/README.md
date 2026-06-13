[perch](../README.md) / Containers

# The container framework

Perch treats the container as the place where checks run. There is one image,
`perch-dev`, and every check (Go, frontend, end-to-end, lint, vulnerability
scan, and the GUI build) runs inside it. The same bits run everywhere, so the
same results come back everywhere.

## Layout

```
containers/
|-- README.md        this file
|-- run.sh           run a command in an image, with the bind-mount, masks, and caches
`-- dev/
    `-- Containerfile  the perch-dev image
```

## One front door: make

You never call `podman` by hand. The `Makefile` is the single entry point.

```sh
make image              # build perch-dev (versions from .tool-versions)
make test               # Go unit tests, in the container
make test-integration   # Go integration tests (-tags=integration)
make test-front         # frontend typecheck and unit tests
make test-e2e           # Playwright (chromium), the reason this framework exists
make lint vet vulncheck # Go quality gates
make test-all           # everything, each gate in its own container
make shell              # an interactive shell inside perch-dev
```

The image bakes Playwright `1.60.0` and its chromium build, so end-to-end tests
run against a pinned browser regardless of the host. Once one check runs in a
container, the rest do too, for consistency.

## How it works

### CONTAINERIZE

One toggle makes every target work both locally and in a pipeline.

- `CONTAINERIZE=1`, the default, re-enters the image: `run.sh dev make
  CONTAINERIZE=0 <target>`.
- `CONTAINERIZE=0` runs natively, used when you are already inside the image or
  in a pipeline that has already pulled it.

A pipeline is therefore "run the image, call `make` inside it." There is no
second entry point and no docker-in-docker.

### The host stays clean

`run.sh` bind-mounts the repository read-write, then masks paths with anonymous
volumes so a run never mutates your working tree. An anonymous volume seeds from
the image layer, which holds the toolchain only, not from the host bind-mount.

- `frontend/node_modules` is masked always. The container runs `npm ci` into the
  empty volume, because the host copy is built for the host.
- `frontend/dist` is masked only for frontend-building targets, through
  `PERCH_MASK_DIST=1`. Those targets run `vite build`, which would otherwise
  overwrite the committed `//go:embed frontend/dist` stub. Go targets must not
  mask it, or the embed finds an empty directory and fails to compile.
  `test-e2e` sets `PERCH_MASK_DIST=1` itself when `make` dispatches it through
  `run.sh`.

Named cache volumes, the Go build cache and the baked Playwright browser,
persist across runs for speed.

## The GUI build

Production GUI builds link WebKit2GTK 4.1 through `-tags "production
webkit2_41"`, set in the Makefile. The container base, Ubuntu 24.04 (noble),
ships the 4.1 dev package, and so does a recent host, so both link the same way.
The image also bakes GTK3 and `xvfb`, so it covers the GUI build today and a
headless GUI smoke later.

To confirm the base can link the GUI (cgo plus WebKit), exercise the build in
the image:

```sh
PERCH_MASK_DIST=1 bash containers/run.sh dev make gui-build CONTAINERIZE=0
```

`run.sh` reads `PERCH_MASK_DIST` from the host, masks `frontend/dist`, and lets
`vite build` write the throwaway volume, so the committed stub stays intact.
Without the prefix the check still passes, but it regenerates the host stub, so
do not commit it.

## devcontainer

`.devcontainer/devcontainer.json` reuses this image. Its `initializeCommand`
runs `make image` on the host, then attaches to `perch-dev:latest`. Opening the
repository in a devcontainer-aware editor gives the same environment `make` uses.
Inside it, `CONTAINERIZE=0`, so targets run natively without a nested podman.

One caveat: because targets run with `CONTAINERIZE=0` inside the devcontainer,
frontend-building targets run `vite build` against the mounted workspace with no
`run.sh` mask, so they regenerate `frontend/dist`, including the committed stub.
Do not commit the regenerated stub. The default front door, `make` with
`CONTAINERIZE=1`, always masks `dist` and never touches the stub.

## Not automated here

The manual WebKit, real-agent, and D-Bus single-instance smoke stays human-gated:
real `claude` and `opencode` binaries, a live WebKit window, and cross-process
D-Bus are out of reach of the headless image today. The
[smoke checklist](../docs/smoke-checklist.md) covers it.
