# Contributing to perch

## Toolchain

| Tool | Required version |
|------|-----------------|
| Go directive | `1.25.0` (see `go.mod`) |
| Go toolchain | `go1.26.4` (see `go.mod` `toolchain` directive) |
| git | any recent version (worktree + diff operations) |
| Node.js | `22.22.3` (pinned in `.tool-versions`; for building the Svelte frontend) |
| npm | bundled with Node v22 |

**GUI system libraries (Linux only)** — install once on a fresh machine:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

`libwebkit2gtk-4.1-dev` pulls in `libsoup-3.0-dev` transitively. WebKit2GTK 4.0
is EOL; the container base ships only 4.1, and Ubuntu 26.04 hosts `4.1-dev`
natively, so host and container link identically.

The production GUI binary is built with `go build -tags "production webkit2_41"`
(the `webkit2_41` tag selects the **webkit2gtk-4.1** link), **not** the
`wails` CLI. The `wails` CLI is incompatible with this repo's layout (`main`
lives at `./cmd/perch`; the root package is a library). The `wails` CLI is
therefore not required and should not be used to build or run the app.

All Go builds are **vendored and hermetic**. The `GOFLAGS=-mod=vendor` environment
variable is set in the Makefile so every `go` invocation reads from the committed
`/vendor` tree — no network access is required after cloning.

## Make workflow

Run targets from the repo root. All targets respect the vendored build.

| Target | What it does |
|--------|-------------|
| `make image` | Build the `perch-dev` container image (`containers/dev/Containerfile`) — the dev/test environment every check runs in |
| `make shell` | Open an interactive shell inside the `perch-dev` image |
| `make build` | Build `./bin/perch` with `-tags "production webkit2_41"` (trimpath, ldflags version stamp; links webkit2gtk-4.1) |
| `make install` | Install to `GOBIN` / `~/go/bin` with `-tags "production webkit2_41"` |
| `make run` | `build` then run `./bin/perch` (needs an X/Wayland display for the GUI) |
| `make gui-build` | `npm --prefix frontend ci` + `npm --prefix frontend run build`, then `go build -tags "production webkit2_41"` |
| `make gui-run` | `gui-build` then launch the binary (needs an X/Wayland display) |
| `make test` | Unit tests (`go test -race -count=1 ./...`) — in the `perch-dev` container |
| `make test-integration` | Integration tests (`-tags=integration`; requires git on PATH) — in the `perch-dev` container |
| `make test-front` | Frontend typecheck (`tsc`) + unit tests (`vitest`) — in the `perch-dev` container |
| `make test-e2e` | Playwright chromium e2e — in the `perch-dev` container (the host distro is too new to run Playwright 1.60.0 natively) |
| `make test-all` | The everything-gate: `test test-integration test-front lint vet vulncheck test-e2e`, each in its own container with the correct artifact masks |
| `make coverage` | Coverage report for `internal/` packages only |
| `make lint` | golangci-lint v2.11.4 — runs the prebaked pinned binary inside the `perch-dev` image |
| `make fmt` | `gofmt -w` + `goimports -w` (if goimports is present) |
| `make vet` | `go vet ./...` — in the `perch-dev` container |
| `make vulncheck` | govulncheck v1.3.0 — prebaked pinned binary inside the `perch-dev` image; scans deps for known CVEs |
| `make verify` | Verify every module checksum against `go.sum` |
| `make verify-all` | Quality gates only: `vet lint vulncheck test-front` (vet + lint + govulncheck + tsc + frontend unit; no integration/e2e) — in the `perch-dev` container |
| `make tidy` | `go mod tidy` then refresh the vendor tree |
| `make vendor` | Refresh the committed `/vendor` tree |
| `make cross` | Build `./bin/perch-linux-amd64` with `-tags "production webkit2_41"` (Linux-only; cgo+WebKit requires per-target toolchain) |
| `make doctor` | Build then run `perch doctor` (checks runtime deps) |
| `make clean` | Remove `./bin/` |

### Testing runs in containers

The dev/test workflow is **container-first**. There is one image, `perch-dev`
(`containers/dev/Containerfile`); build it with `make image`. By default every
check — `test`, `test-integration`, `test-front`, `lint`, `vet`, `vulncheck`,
`test-e2e` — re-enters that image and runs there. The reason is e2e: the host
(Ubuntu 26.04) is too new for Playwright 1.60.0 to run natively, so the e2e
suite runs in `perch-dev`, and for consistency every other gate runs there too.
`lint` and `vulncheck` run prebaked pinned binaries baked into the image rather
than fetching tools at runtime.

The `CONTAINERIZE` variable is the toggle (default `1` = re-enter the image).
Set `CONTAINERIZE=0` to run a target natively — that is what happens **inside**
the image and in a pipeline, where re-entry would be redundant. The generic
container exec is `containers/run.sh`.

Container runs never mutate your working tree: `frontend/node_modules` and (for
frontend-building targets) `frontend/dist` are masked with anonymous volumes, so
the tracked `//go:embed frontend/dist` stub is never clobbered. The
`.devcontainer/devcontainer.json` reuses the same `perch-dev` image.

See [`containers/README.md`](containers/README.md) and the design rationale in
[`docs/superpowers/specs/2026-06-04-perch-container-framework-design.md`](docs/superpowers/specs/2026-06-04-perch-container-framework-design.md).

## GUI dev loop

`wails dev` hot-reload is **not available** — the `wails` CLI cannot build this
repo (root is a library, `main` is at `./cmd/perch`). The GUI dev loop is:

```sh
make gui-run          # rebuild frontend + Go binary, then launch
```

Frontend tests (typecheck + unit) run in the `perch-dev` container via:

```sh
make test-front
```

If you have a local Node toolchain set up, `npm --prefix frontend test` still
works for quick local iteration, but `make test-front` is the canonical path.

For iterating on Go logic without a display, `make test` and `make vet` cover the
non-GUI code paths. The `production`/`webkit2_41` build tags are not needed for
unit tests.

## §20.1 Runner principle

**Every shell-out in perch goes through `internal/proc.Runner`.** This is a
non-negotiable architectural rule.

- **Production code** uses `proc.ExecRunner{}`, which wraps `os/exec`.
- **Unit tests** use `proc.FakeRunner`. Fake tests assert on `.Calls` (the
  recorded command argv slices) and never spawn a real process. If you write a
  new handler or internal package that runs an external command, inject a
  `proc.Runner` and add a unit test using `FakeRunner`.
- **Integration tests** are tagged `//go:build integration` and exercise real
  worktree, pty, and git behaviour against **throwaway git repos and temp
  directories** created per test. They never touch the developer's real `$HOME`
  config or working repos.

## Dev safety

**Never run the real `perch` binary or any code path that writes to real `$HOME`
config during development or testing.**

- When a test must exercise config loading, use `t.Setenv("HOME", t.TempDir())`
  and/or `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` to redirect all writes to a
  throwaway directory that is cleaned up automatically.
- The same applies to the perch config dir (`registry.DefaultConfigDir()`,
  which honours `XDG_CONFIG_HOME`). Test helpers must redirect it via
  `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` before any load so the registry,
  settings, and layout writes land in a throwaway directory.

## Coverage

The project targets **≥ 80% statement coverage per package**.

Run `make coverage` to see per-package totals for `internal/`. A new package
below 80% will block the review gate.

## Branch and commit conventions

- **Never commit directly to `main` or `master`.** All changes go through a
  feature branch and a pull request.
- **Conventional Commits** — follow the style used throughout the repo's history:

  ```
  feat(scope): short imperative description
  fix(scope): short imperative description
  refactor(scope): short imperative description
  docs(scope): short imperative description
  test(scope): short imperative description
  ```

  The scope is the affected subsystem (e.g. `gui`, `frontend`, `agent`,
  `pty`, `hooklistener`, `registry`, `config`, `worktree`, `proc`).

- **No co-author trailers.** Do not add `Co-authored-by:` lines to commits.

## Where things live

| Artifact | Location |
|----------|----------|
| Implementation plans | `docs/superpowers/plans/` |
| Architecture diagrams (Mermaid) | `docs/diagrams/` |
| Public architecture overview | `ARCHITECTURE.md` |
