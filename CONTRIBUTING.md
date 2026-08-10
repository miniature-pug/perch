[perch](README.md) / Contributing

# Contributing to perch

## Toolchain

| Tool | Version |
|------|---------|
| Go language floor | `1.25.0` (the `go` directive in `go.mod`) |
| Go toolchain | `go1.26.5` (the `toolchain` directive in `go.mod`, pinned in `.tool-versions`) |
| Node.js | `22.22.3` (pinned in `.tool-versions`), with the bundled npm |
| git | any recent version |

Install the GUI system libraries once, on Linux:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

`libwebkit2gtk-4.1-dev` pulls in `libsoup-3.0-dev`. WebKit2GTK 4.0 is end of
life, so perch links 4.1. The container base and recent Debian and Ubuntu
releases both provide the 4.1 dev package, so the host and the container link
the same way.

Build the production GUI binary with `go build -tags "production webkit2_41"`.
The `webkit2_41` tag links WebKit2GTK 4.1. The `production` tag selects the Wails
production runtime, which omits the dev reload server. Perch embeds the frontend
from `frontend/dist/` regardless of tags, so build it first with `make
gui-build`. This repository does not use the `wails` CLI. The CLI cannot build
this repository, because `main` lives at `./cmd/perch` while the root package is
a library.

All Go builds use vendored dependencies. The Makefile and the container set
`GOFLAGS=-mod=vendor`, so every `go` command reads the committed `vendor/` tree
and needs no network after cloning.

## Make workflow

Run targets from the repository root.

| Target | What it does |
|--------|--------------|
| `make image` | Build the `perch-dev` container image, the environment every check runs in |
| `make shell` | Open an interactive shell inside `perch-dev` |
| `make build` | Build `./bin/perch` with the production tags; embeds the committed frontend stub without rebuilding it |
| `make install` | Install to `GOBIN` with the production tags |
| `make run` | `build`, then run `./bin/perch` |
| `make gui-build` | `npm ci`, build the frontend, then build the binary with the production tags |
| `make gui-install` | `gui-build`, then install the launcher icon, without launching (run `./bin/perch` yourself) |
| `make gui-run` | `gui-build`, install the desktop entry, then launch the binary |
| `make desktop` | Install a user `.desktop` entry and icon so GNOME and Wayland show the app icon |
| `make test` | Go unit tests, with the race detector |
| `make test-integration` | Go integration tests (`-tags=integration`); needs git |
| `make test-front` | Frontend `npm audit` (production deps, fails on high or critical), typecheck, and unit tests |
| `make test-e2e` | Playwright (chromium) end-to-end tests |
| `make test-all` | The full gate: test, test-integration, test-front, lint, vet, vulncheck, test-e2e |
| `make coverage` | Per-package coverage for `internal/` |
| `make lint` | golangci-lint `v2.11.4`, the version baked into the image |
| `make vet` | `go vet ./...` |
| `make vulncheck` | govulncheck `v1.3.0`, baked into the image |
| `make verify` | `go mod verify` against `go.sum` |
| `make verify-all` | The quality gates only: vet, lint, vulncheck, test-front |
| `make fmt` | `gofmt -w` and `goimports -w` |
| `make tidy` | `go mod tidy`, then refresh the vendor tree |
| `make vendor` | Refresh the `vendor/` tree |
| `make cross` | Cross-build `./bin/perch-linux-amd64` |
| `make doctor` | Build, then run `perch doctor` |
| `make clean` | Remove `./bin/` |

### Checks run in the container

The test workflow is container-first. There is one image, `perch-dev`.
`make image` builds it from `containers/dev/Containerfile`. By default every
check re-enters that image and runs there, so a check produces the same result
on any host. The image bakes a pinned Playwright and its browser, the pinned
`golangci-lint`, and `govulncheck`, so checks never fetch tools at runtime.

The `CONTAINERIZE` variable is the toggle, and it defaults to `1`. Set
`CONTAINERIZE=0` to run a target natively. This is what happens inside the image
and in a pipeline, where re-entry would be redundant. A container run never
changes your working tree. Anonymous volumes mask `frontend/node_modules`, and
`frontend/dist` for frontend-building targets, so the committed
`//go:embed frontend/dist` stub is never overwritten. The
[container framework](containers/README.md) covers the model in full.

## GUI dev loop

There is no `wails dev` hot reload, because the `wails` CLI cannot build this
layout. The loop is:

```sh
make gui-run          # rebuild the frontend and binary, then launch
```

`make test-front` runs `npm audit` over the production dependencies, then the
frontend typecheck and unit tests, in the container. The audit fails the gate on
any high or critical advisory, so a vulnerable shipped dependency cannot drift in
unnoticed. This check is the frontend counterpart to `govulncheck` on the Go side.
With a local Node toolchain, `npm --prefix frontend test` works for quick
iteration, but `make test-front` is the canonical path. For Go logic without a
display, `make test` and `make vet` cover the non-GUI code. Unit tests do not
need the production build tags.

## The Runner principle

Every shell-out in perch goes through `internal/proc.Runner`. This is a firm
rule.

- Production code uses `proc.ExecRunner`, which wraps `os/exec`.
- Unit tests use `proc.FakeRunner`. They assert on `.Calls`, the recorded argv,
  and never spawn a process. A new handler or package that runs an external
  command takes a `proc.Runner` and gets a `FakeRunner` test.
- Integration tests, tagged `//go:build integration`, exercise real worktree,
  pty, and git behavior against throwaway repositories and temp directories.
  They never touch your real `$HOME` config.

## Test safety

Never run a code path that writes to your real `$HOME` config during development
or testing.

- A test that exercises config loading sets `t.Setenv("HOME", t.TempDir())` and
  `t.Setenv("XDG_CONFIG_HOME", t.TempDir())`, so writes land in a throwaway
  directory.
- The same applies to the perch config directory, which honors
  `XDG_CONFIG_HOME`. Redirect it before any load so the registry, settings, and
  layout writes are sandboxed.

## Coverage

The project targets at least 80% statement coverage per package. Run `make
coverage` for the `internal/` totals. A new package below the target blocks the
review gate.

## Branches and commits

- Never commit to `main` or `master`. Work on a feature branch and open a pull
  request.
- Follow Conventional Commits, the style used throughout the history:

  ```
  feat(scope): short imperative description
  fix(scope): short imperative description
  docs(scope): short imperative description
  ```

  The scope is the affected subsystem, such as `gui`, `frontend`, `agent`,
  `pty`, `hooklistener`, `registry`, or `config`.
- Do not add `Co-authored-by` trailers.

## Where things live

| Topic | Location |
|-------|----------|
| Usage guide | [docs/usage.md](docs/usage.md) |
| Architecture | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Backend package map | [internal/README.md](internal/README.md) |
| Frontend map | [frontend/README.md](frontend/README.md) |
| Container framework | [containers/README.md](containers/README.md) |
| Architecture diagrams | [docs/diagrams/](docs/diagrams/README.md) |
| Pre-release smoke checklist | [docs/smoke-checklist.md](docs/smoke-checklist.md) |
