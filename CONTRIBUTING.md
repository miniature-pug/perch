# Contributing to perch

## Toolchain

| Tool | Required version |
|------|-----------------|
| Go directive | `1.25.0` (see `go.mod`) |
| Go toolchain | `go1.26.4` (see `go.mod` `toolchain` directive) |
| tmux | `3.6` (pinned in `.tool-versions`, verified by `perch doctor`) |
| Node.js | `v22.x` (for building the Svelte frontend; checked by `node --version`) |
| npm | bundled with Node v22 |

**GUI system libraries (Linux only)** — install once on a fresh machine:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev
```

The production GUI binary is built with `go build -tags production`, **not** the
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
| `make build` | Build `./bin/perch` with `-tags production` (trimpath, ldflags version stamp) |
| `make install` | Install to `GOBIN` / `~/go/bin` with `-tags production` |
| `make gui-build` | `npm install` + `npm run build` in `frontend/`, then `go build -tags production` |
| `make gui-run` | `gui-build` then launch the binary (needs an X/Wayland display) |
| `make test` | Unit tests (`go test -race -count=1 ./...`) |
| `make test-integration` | Integration tests (`-tags=integration`; requires tmux and git on PATH) |
| `make test-all` | Unit + integration in one pass |
| `make coverage` | Coverage report for `internal/` packages only |
| `make lint` | golangci-lint v2.11.4 (fetched via `go run`, never a floating install) |
| `make fmt` | `gofmt -w` + `goimports -w` (if goimports is present) |
| `make vet` | `go vet ./...` |
| `make vulncheck` | govulncheck v1.3.0 — scan deps for known CVEs |
| `make verify` | Verify every module checksum against `go.sum` |
| `make tidy` | `go mod tidy` then refresh the vendor tree |
| `make vendor` | Refresh the committed `/vendor` tree |
| `make cross` | Build `./bin/perch-linux-amd64` with `-tags production` (Linux-only; cgo+WebKit requires per-target toolchain) |
| `make doctor` | Build then run `perch doctor` (checks runtime deps) |
| `make clean` | Remove `./bin/` |

## GUI dev loop

`wails dev` hot-reload is **not available** — the `wails` CLI cannot build this
repo (root is a library, `main` is at `./cmd/perch`). The GUI dev loop is:

```sh
make gui-run          # rebuild frontend + Go binary, then launch
```

Frontend unit tests run independently of the Go build:

```sh
npm --prefix frontend test
```

For iterating on Go logic without a display, `make test` and `make vet` cover the
non-GUI code paths. The `-tags production` flag is not needed for unit tests.

## §20.1 Runner principle

**Every shell-out in perch goes through `internal/proc.Runner`.** This is a
non-negotiable architectural rule.

- **Production code** uses `proc.ExecRunner{}`, which wraps `os/exec`.
- **Unit tests** use `proc.FakeRunner`. Fake tests assert on `.Calls` (the
  recorded command argv slices) and never spawn a real process. If you write a
  new handler or internal package that runs an external command, inject a
  `proc.Runner` and add a unit test using `FakeRunner`.
- **Integration tests** are tagged `//go:build integration` and use a **private
  tmux socket** (e.g. `/tmp/perch-test-<random>.sock`) so they never touch the
  developer's default tmux server. Never create integration tests that write to
  the real `$TMUX_TMPDIR` socket or assume a live user session.

## Dev safety

**Never run the real `perch` binary or any code path that writes to real `$HOME`
config during development or testing.**

- `perch setup` writes to real `~/.claude/settings.json` and
  `~/.config/opencode/plugins/`. Do not invoke this in tests.
- When a test must exercise config loading, use `t.Setenv("HOME", t.TempDir())`
  and/or `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` to redirect all writes to a
  throwaway directory that is cleaned up automatically.
- The same applies to `XDG_STATE_HOME` / the perch state dir. Test helpers that
  call `state.StateDir()` must redirect it via environment variables before the
  call.

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

  The scope is the affected subsystem (e.g. `gui`, `frontend`, `attach`,
  `state`, `config`, `trust`, `worktree`, `proc`).

- **No co-author trailers.** Do not add `Co-authored-by:` lines to commits.

## Where things live

| Artifact | Location |
|----------|----------|
| Implementation plans | `docs/superpowers/plans/` |
| Architecture diagrams (Mermaid) | `docs/diagrams/` |
| Security ledger | `docs/security-audit.md` |
| Design rationale / internal ADRs | `plan.md` |
| Public architecture overview | `ARCHITECTURE.md` |
