# perch

perch is a worktree-native AI-agent **cockpit** — a desktop GUI for running and
supervising AI coding agents (`claude` and `opencode`) across git worktrees.
`perch` (no arguments) opens a **Wails v2 desktop window**: a Go backend embedded
in a WebKit2GTK webview running a Svelte 5 (runes) SPA. The left sidebar lists
your workspaces; selecting one opens a full interactive terminal (xterm.js)
backed by a **direct pseudo-terminal** that the Go app spawns. Each workspace is
a git worktree off a base branch paired with an agent.

There is **no tmux**, **no daemon**, and **no background server process**.
perch is a single static binary. Frontend ↔ backend communication is Wails
bindings (`window.go.app.App.<Method>`) plus Wails events — not HTTP. The only
local network surface in production is the per-agent hook listener (see
[Security model](#security--trust-model)).

---

## What it is

- A **cockpit** for AI coding agents: one window, many workspaces, each with a
  live terminal, diff view, file tree, and (where the agent supports it) inline
  tool-call approvals.
- **Worktree-native** — every workspace is a real `git worktree` off a base
  branch, so agents work in isolation without disturbing your main checkout.
- **Agent-agnostic via a Monitor seam** — two integrations ship today
  (`claude`, `opencode`); the UI degrades to exactly what each agent supports
  (see [Capabilities](#capabilities--degradation)).
- **Mouse-first, keyboard-accelerated** — the GUI is fully operable with the
  mouse; a vim-style modal layer (NORMAL / TERMINAL / COMMAND) is an
  accelerator, never a requirement.

---

## Requirements

### Runtime

| Tool | Version / note |
|------|----------------|
| WebKit2GTK + GTK3 | system libraries — **Linux only** |
| git | any recent version |

At least one of **claude** or **opencode** must be installed and on `$PATH`.

Install the system libraries on Debian/Ubuntu:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev
```

> **Linux only.** macOS and Windows are not supported because WebKit2GTK is a
> Linux-specific library. A native-toolkit port is a non-goal for v1.

### Build-time (source builds only)

| Tool | Version |
|------|---------|
| Go toolchain | 1.26.4 |
| Node.js | v22 |
| npm | (bundled with Node) |

---

## Build & run

```sh
git clone https://github.com/Miniature-Pug/perch
cd perch
make gui-build
./bin/perch
```

`make gui-build` runs `npm --prefix frontend run build` (builds the Svelte SPA
into `frontend/dist/`) and then `go build -tags production -o bin/perch
./cmd/perch`.

The `-tags production` build tag is **required** — it embeds the compiled
frontend assets into the binary. Without it the app uses a stub that errors at
launch; that is expected.

> The `wails` CLI is **not** used. The repo root is a library package that
> embeds `frontend/dist`; `main` lives in `cmd/perch`. Build with
> `make gui-build`, never `wails build`.

To install to `GOBIN` / `~/go/bin`:

```sh
make install
```

Both targets inject the version string via ldflags
(`-X main.version=$(git describe --tags --always --dirty)`).

Module path: `github.com/Miniature-Pug/perch`

---

## Quick start

```sh
# Open the cockpit; project root = current directory
perch

# Same, but with an explicit project root
perch /path/to/projects

# One-time setup: install status hooks/plugins for detected agents
perch setup
```

`perch` (no arguments) opens the desktop GUI. The sidebar lists your workspaces
from the registry; create a new one to spin up a git worktree plus an agent.
Select a workspace to open its embedded terminal. Each terminal streams output
from a direct pty the Go app spawned for that pane.

---

## Commands

| Command | Purpose |
|---------|---------|
| `perch` | Open the cockpit GUI; project root = cwd |
| `perch <path>` | Open the cockpit GUI; project root = given directory |
| `perch attach <query>` | Fuzzy-match a workspace (by title or worktree path) in the registry and print it; the GUI owns actual focus |
| `perch setup [--replace]` | Detect installed agents; install status hooks/plugins. `--replace` overwrites stale perch-owned entries |
| `perch doctor` | Check runtime dependencies and configuration |
| `perch version` | Print version and build info |

`perch attach` is registry-backed and informational — it locates a workspace in
`workspaces.json`; it does not attach to any background session.

---

## Agent setup

Each agent integration plugs in behind a **Monitor** seam (`internal/agent`).

### claude — hooks

When a claude workspace is opened, its `ClaudeMonitor` writes the hook
configuration (listener URL + bearer token) into `<worktree>/.claude/settings.json`.
The claude agent's hooks then POST tool/lifecycle events back to the listener,
and `PreToolUse` blocks until you approve (see [Security model](#security--trust-model)).

`perch setup` additionally installs the global status hooks into
`~/.claude/settings.json` for agent state reporting.

### opencode — serve + SSE

An opencode workspace launches `opencode serve` and the `OpencodeMonitor`
consumes its Server-Sent-Events stream (`/event`) over an authenticated HTTP
connection to surface lifecycle, token, and approval events.

`perch setup` installs the claude status hooks (`~/.claude/settings.json`).
opencode exposes session status natively via its SSE stream, so no plugin
file is required for opencode.

---

## Capabilities & degradation

Every agent advertises a `Caps` set; the UI surfaces only what the agent
supports.

| Cap | Meaning | claude | opencode |
|-----|---------|:------:|:--------:|
| `approvals` | Inline tool-call approval (Allow / Always / Deny) | ✅ | ✅ |
| `attention` | Lifecycle / attention state (running, idle, awaiting, done, errored) | ✅ | ✅ |
| `tokens` | Token / cost usage reporting | ✅ (tokens only; no cost) | ✅ |

An agent that did not advertise a cap simply has that surface hidden — the
cockpit degrades rather than showing dead controls.

---

## Interaction model

The GUI is **mouse-first**: everything is clickable. A vim-style **modal**
keyboard layer accelerates power use but is never required.

| Mode | Enter | Leave | What it does |
|------|-------|-------|--------------|
| **NORMAL** | default; `Ctrl-\ Ctrl-n` from TERMINAL; click chrome | — | Navigation/command keys drive the UI, not the pty: `j`/`k` move sessions, `1`/`2`/`3` switch Agent/Code/Diff, `\` splits the stage, `/` filters sessions, `⏎` opens the selected session |
| **TERMINAL** | `i`; click a pane | `Ctrl-\ Ctrl-n`; click chrome | All keys pass straight to the focused pane's pty |
| **COMMAND** | `:` or `⌘`/`Ctrl-K` | `Esc`; run a command | Command palette / command line |

`Esc` leaves COMMAND mode — it does **not** leave TERMINAL. Exit a terminal with
the `Ctrl-\ Ctrl-n` chord (or click any chrome). The current mode is always shown
in the status line.

---

## Configuration

The workspace registry is persisted at `~/.config/perch/workspaces.json`
(XDG: `$XDG_CONFIG_HOME/perch/workspaces.json`). Each entry records the
worktree path, agent, branch, title, and last session id. Settings and saved
layout live alongside it (`settings.json`, `layout.json`).

The global `~/.config/perch/config.toml` supplies the allowed project `roots`
(with no config, the launch directory is the sole root) and the `[agents]`
binary paths. Agent-binary paths are a **global-only** setting — see the
security model below.

---

## Security / trust model

### IPC has no listening port

Frontend ↔ backend IPC uses **Wails bindings** (`window.go.app.App.<Method>`)
and Wails events — it does **not** open a TCP or Unix socket. The
`ws://localhost:34115` reload socket is a `//go:build dev` artifact and is never
compiled into a production binary. Every argument crossing the IPC boundary is
validated in `app.App` (workspace-id charset allowlist, worktree-path
containment under the configured roots).

### The agent hook listener (the only local network surface)

Each Claude monitor creates its **own** hook listener bound to `127.0.0.1` on an
**ephemeral** port, protected by a **per-listener random Bearer token**. It
writes the hook config (URL + token) into `<worktree>/.claude/settings.json`.
The Claude agent's hooks POST tool/lifecycle events back to it; `PreToolUse`
**blocks synchronously** until the user approves. The token is compared in
constant time, and the listener is torn down (with its hook entries removed)
when the workspace closes.

This is the entire production local network surface — one short-lived,
loopback-only, token-gated listener per active Claude workspace.

### Global-only agent binary boundary

`[agents]` binary paths and `startup_command` come **only** from the global
config. A project `.perch.toml` has no `[agents]` field — any such key is
silently dropped by the TOML decoder. A malicious repo cannot point perch at an
arbitrary binary.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system-level breakdown.

---

## Non-goals

- No tmux, no daemon, no long-running background process
- No SQLite or embedded database (small JSON stores only)
- No remote, SSH, or multi-machine orchestration
- No CI/CD pipeline integration
- No sandboxing or containers
- No macOS / Windows support (WebKit2GTK is Linux-only)
- No agents beyond claude and opencode (the Monitor seam keeps the door open)

---

## Diagrams

Architecture, status-sequence, worktree-lifecycle, and discovery-state diagrams
live in [`docs/diagrams/`](docs/diagrams/). They render in any Mermaid viewer or
directly in GitHub.
