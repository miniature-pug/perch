# perch — Architecture Reference

> **Audience:** contributors and maintainers who need to understand how perch
> works at the system level. End-user documentation lives in `README.md`.

---

## Overview

perch is a worktree-native AI-agent **cockpit**: a desktop GUI for running and
supervising AI coding agents (`claude`, `opencode`) across git worktrees. The
default invocation (`perch` or `perch <path>`) launches a **Wails v2 desktop
window**: a Go backend embedded in a WebKit2GTK webview driving a Svelte 5
(runes) SPA. A small set of CLI subcommands (`setup`, `attach`, `doctor`,
`version`) supports scripting and agent-hook integration.

Key properties:

- **Single static binary** — no tmux, no daemon, no server process.
- **One direct pty per pane** — each terminal is backed by a pseudo-terminal
  (`creack/pty`) the Go app spawns with a login shell. No multiplexer.
- **No listening TCP port for IPC** — the Svelte frontend calls the Go backend
  over Wails bindings (`window.go.app.App.<Method>`) and receives Wails events.
  The only production local network surface is the per-agent hook listener (see
  [Security model](#security-model)).
- **Plain-JSON workspace registry** — no database dependency (see
  [Workspace registry](#workspace-registry--discovery)).
- **Module:** `github.com/Miniature-Pug/perch`
- **Go toolchain:** `go1.26.4`
- **Wails:** v2 / **Frontend:** Svelte 5 (runes) + Vite (in `frontend/`)
- **Key deps:** `creack/pty` (direct pty bridge), `bmatcuk/doublestar/v4`
  (glob), `sahilm/fuzzy` (fuzzy match), `BurntSushi/toml` (config)
- **Linux only** — requires WebKit2GTK + GTK3 system libraries.

---

## The Wails GUI Model

### Process model

`perch` runs as a single OS process. The Wails runtime embeds a WebKit2GTK
webview inside a GTK window. The Svelte SPA is compiled into the binary at build
time (`-tags production` embeds the `frontend/dist/` assets). There is no
separate frontend process and no HTTP server for IPC in production.

```
┌─────────────────────────────────────────────────────────┐
│  perch process                                           │
│  ┌────────────────┐  Wails bindings + events             │
│  │  Go backend    │ ◄──────────────────────────────────► │
│  │  (app.App)     │         WebKit2GTK webview            │
│  └────────────────┘         (Svelte 5 SPA)               │
│        │                                                  │
│        ├── Bridge ──► login shell in a direct pty (per pane)
│        ├── ClaudeMonitor ──► hooklistener (127.0.0.1:ephemeral)
│        └── OpencodeMonitor ──► opencode serve + SSE        │
└─────────────────────────────────────────────────────────┘
```

The three downstream seams are:

- **App → Bridge → Shell** — `OpenWorkspace` spawns a direct pty running a login
  shell in the worktree; raw bytes stream to the frontend as Wails events and
  keystrokes flow back via `WriteToPty`.
- **App → ClaudeMonitor → hooklistener** — for claude workspaces, lifecycle and
  tool-call events arrive over the per-workspace hook listener.
- **App → OpencodeMonitor → serve/SSE** — for opencode workspaces, events
  arrive over the agent's `opencode serve` Server-Sent-Events stream.

### Bound-method API

The `app.App` struct is the Wails-bound object. Its exported methods form the
API the Svelte frontend calls over the IPC bridge:

| Method | Purpose |
|--------|---------|
| `ListWorkspaces()` | Return all known workspaces with live state + caps |
| `CreateWorkspace(agent, repoPath, branch, model)` | Create a new git worktree + register an agent workspace |
| `OpenWorkspace(id)` | Spawn a direct pty + start the agent's Monitor; begin streaming |
| `WriteToPty(paneID, data)` | Forward keystrokes from the terminal tab to the pty |
| `ResizePty(paneID, cols, rows)` | Propagate terminal resize to the pty |
| `CloseWorkspace(id)` | Tear down the pty bridge + Monitor for a workspace |
| `RemoveWorkspace(id)` | Remove the worktree + registry entry |
| `OpenShell(paneID, cwd)` | Spawn an auxiliary login-shell pty |
| `Approve(reqID, decision)` | Resolve a pending `PreToolUse` approval (allow / always / deny) |
| `DiffStat / Hunks / StageHunk / DiscardHunk` | git diff view + staging per worktree |
| `Branches / Worktrees` | git metadata for a repo |
| `ListDir / ReadFile / WriteFile / RevealInFiles / CopyPath` | file-tree operations |
| `GetSettings / SaveSettings / GetLayout / SaveLayout` | persisted UI state |

Every argument crossing the IPC boundary is validated inside `app.App`:
workspace / pane IDs are checked against a `[A-Za-z0-9_-]` charset allowlist;
worktree paths are resolved and verified to lie under the configured `roots`.
All git work is done via argv through `internal/proc`, never a shell.

### Direct-pty rendering bridge

Opening a workspace calls `OpenWorkspace`, which spawns a **login shell inside a
pseudo-terminal** via `creack/pty` (`internal/pty.Bridge`) in the worktree
directory. The pty captures raw byte output and forwards it to the frontend as
Wails events; the Svelte component feeds the bytes to an **xterm.js** terminal.
Keystrokes typed in xterm.js are sent back through `WriteToPty`; resize events
flow through `ResizePty`. The agent's launch command (produced by the Monitor's
`Prepare`) is written into the shell so the agent starts inside the same pty.
Closing the bridge kills the whole process group, so the shell's children die
with it.

### Agent Monitor seam

Each agent integration implements the `agent.Monitor` interface
(`internal/agent/monitor.go`):

```
Prepare(ctx, workspaceID, cwd, resumeID) (launchCmd, err)
Start(ctx)                 // launch the event pump bound to ctx
Events() <-chan Event
Approve(reqID, Decision) error
Capabilities() Caps        // {approvals, attention, tokens}
Teardown() error
CurrentState() State
LastApprovalTool() string
```

`agent.NewMonitor(tool, adapter)` returns `ClaudeMonitor` or `OpencodeMonitor`.
`OpenWorkspace` calls `Prepare` (which provisions the listener / serve command),
`Start` (which begins translating agent events into the unified `Event` channel),
and forwards each event to the frontend as an `agent:event` Wails event after
stamping the workspace id and a routable approval `reqID`.

### State sync

`app.App` forwards Monitor events to the frontend in real time and emits
`fs:changed` events from a per-workspace filesystem watcher (debounced). The
frontend reacts to mutating actions (create / open / close / remove) optimistically
so the sidebar stays responsive.

---

## Package Map

See `docs/diagrams/architecture.mmd` for the component dependency graph.

| Package | Responsibility |
|---------|----------------|
| `cmd/perch` | CLI entry-point; dispatches subcommands, wires production dependencies, launches the Wails GUI via `app.Run`. |
| `app/` | Wails `App` struct — bound-method API, input validation (id charset allowlist, worktree path containment), pty + Monitor lifecycle, event forwarding, fs-watcher debounce. |
| `internal/pty` | Direct pty bridge (`Bridge`): wraps `creack/pty`, runs a login shell, forwards pty output as Wails events, routes keystrokes and resize back to the pty. **No tmux.** |
| `internal/agent` | `Monitor` seam + `Adapter` interface; concrete `ClaudeMonitor` (hook listener) and `OpencodeMonitor` (serve + SSE), plus per-tool adapters for `claude`/`opencode`. |
| `internal/hooklistener` | Per-workspace loopback HTTP listener (`127.0.0.1:0`) protected by a random Bearer token; receives Claude hook POSTs and blocks `PreToolUse` until `Decide()`. |
| `internal/registry` | Workspace registry persisted at `~/.config/perch/workspaces.json` (XDG). `Store` with `Load`/`List`/`Get`/`Upsert`/`Remove`. |
| `internal/config` | Two-layer TOML config load: global `config.toml` overlaid by project `.perch.toml`. Houses the **agent-binary security boundary** (see [Security model](#security-model)). |
| `internal/discover` | Filesystem scanner: walks `roots` for `.git` entries up to `DefaultMaxDepth`, pruning `node_modules`/`vendor`/`.git`. |
| `internal/doctor` | `perch doctor` health check — read-only, all OS calls injected for testability. |
| `internal/git` | git subprocess wrappers behind `proc.Runner`; includes `ValidRef` for ref-name validation, worktree management, and diff/hunk staging. |
| `internal/fs` | Worktree filesystem helpers: directory listing for the file tree and a change watcher. |
| `internal/match` | `**`-aware glob matching backed by `doublestar`; used for blacklist filtering and `[[wildcard]]` agent assignment. |
| `internal/model` | Shared domain vocabulary (`Tool`, …) — pure data, no I/O. |
| `internal/notify` | Notification tiering (blocking / ambient) for agent lifecycle events. |
| `internal/proc` | `Runner` interface + `ExecRunner` (production) + `FakeRunner` (tests). All shell-outs go through this seam. |
| `internal/status` | Status-hook helper used by `perch setup` for agent state reporting. |
| `internal/worktree` | File seeding (copy/symlink) and lifecycle-hook (`post_create`/`pre_remove`) helpers. **Not currently wired into the cockpit** — `CreateWorkspace` creates worktrees via `internal/git` directly; these helpers have no caller. |
| `frontend/` | Svelte 5 (runes) SPA (Vite build); communicates with Go via Wails bindings and events; renders agent terminals via xterm.js. |

---

## The Runner Seam (§20.1)

Every shell-out in perch — git operations, hook execution — goes through the
`proc.Runner` interface declared in `internal/proc`:

```
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}
```

Production code uses `ExecRunner` (wraps `os/exec`). Unit tests inject
`FakeRunner`, which records calls in a `.Calls` slice and never spawns a real
process. This makes the entire non-frontend surface area unit-testable without a
live git repo.

Integration tests (`//go:build integration`) exercise real worktree and pty
behaviour against throwaway git repos and temp directories. They are run with
`make test-integration` (requires git).

Coverage target: **≥80% per package**. `frontend/` is exempt (browser-rendered
Svelte components require a headless browser for unit coverage).

---

## Workspace Registry & Discovery

See `docs/diagrams/discovery-state.mmd` for the full data-flow diagram.

### The registry

Workspaces are persisted as a single JSON store at
`~/.config/perch/workspaces.json` (XDG: `$XDG_CONFIG_HOME/perch`). Each
`Workspace` record carries:

| Field | Contents |
|-------|----------|
| `id` | Stable workspace identifier (charset-validated). |
| `worktreePath` | Absolute path to the git worktree. |
| `agent` | `claude` or `opencode`. |
| `branch` | Worktree branch. |
| `title` | Display label. |
| `lastSessionID` | Resume id for the agent (passed to `Monitor.Prepare`). |
| `lastActive` | Timestamp for ordering. |

Settings (`settings.json`, including persisted `AlwaysRules`) and saved layout
(`layout.json`) live in the same config directory.

### Discovery pipeline

1. **Roots** — directories perch scans for git repos (launch cwd by default,
   plus any configured roots).
2. **Scan** — `internal/discover.Scan` walks each root up to the max depth,
   pruning `node_modules`, `vendor`, and `.git`; returns paths containing a
   `.git` entry.
3. **Blacklist filter** — `**`-glob patterns hide matching project/tree paths
   from the GUI after discovery (a post-walk filter, not a prune).

---

## Status / Approval Pipeline

See `docs/diagrams/status-sequence.mmd` for the sequence diagram.

For **claude**, the flow is the hook-listener approval loop:

```
claude agent
  → hooklistener (PreToolUse POST, blocks)
    → app.App emits "agent:event" (approval)
      → Svelte ApprovalCard
        → user clicks Allow / Always / Deny
          → App.Approve → Monitor.Approve → hooklistener.Decide
            → claude agent continues (response unblocks the POST)
              → App emits state-update event → frontend re-renders
```

Non-`PreToolUse` hook events (`SessionStart`, `Stop`, `StopFailure`,
`Notification`) are translated by `ClaudeMonitor` into lifecycle `Event`s
(`running` / `idle` / `errored`) and forwarded the same way; token usage is
read from the transcript. For **opencode**, the equivalent events arrive over
the `opencode serve` SSE stream.

`perch setup [--replace]` installs the agent status hooks
(`~/.claude/settings.json`). opencode exposes session status natively via
its SSE stream (`opencode serve`), so no plugin file is needed for opencode.

---

## Worktree Lifecycle

See `docs/diagrams/worktree-lifecycle.mmd` for the full flowchart.

### Create

1. Preflight checks: `repoPath` resolved under a configured root, branch name
   validated via `git.ValidRef`, agent name known (`claude` / `opencode`).
2. The linked-worktree path is derived from the repository and the slugified
   branch, then re-checked to confirm it stays under the configured roots.
3. `git worktree add` (an already-existing branch is tolerated).
4. Workspace recorded in `workspaces.json`. When opened, a direct pty is
   spawned and the agent's Monitor is started.

### Remove

1. Confirm modal, then a deferred removal with an undo window.
2. The pty bridge and Monitor are torn down (Monitor `Teardown` removes the
   per-workspace hook entries from `.claude/settings.json` and closes the
   listener).
3. The workspace record is removed from `workspaces.json`. The worktree
   directory is **left on disk** — so the removal can be undone and the agent's
   conversation history survives.

---

## Security Model

### IPC has no listening port

Frontend ↔ backend IPC uses Wails bindings (`window.go.app.App.<Method>`) and
Wails events; it opens **no TCP or Unix socket**. The `ws://localhost:34115`
hot-reload socket is `//go:build dev` only and is stripped from production
builds.

### Agent hook listener (the only production local network surface)

Each Claude monitor creates its **own** hook listener bound to **`127.0.0.1`**
on an **ephemeral** port, protected by a **per-listener random Bearer token**
(32 random bytes, hex-encoded). It writes the hook config (URL + token) into
`<worktree>/.claude/settings.json`. The Claude agent's hooks POST tool/lifecycle
events back to it; **`PreToolUse` blocks synchronously until the user approves.**

Hardening details:

| Constraint | Detail |
|------------|--------|
| Bind address | `127.0.0.1:0` — loopback only, never a routable interface. |
| Port | Ephemeral (kernel-assigned per listener). |
| Auth | Random per-listener Bearer token, compared in **constant time** (`subtle.ConstantTimeCompare`). |
| Blocking approval | `PreToolUse` POSTs block in the handler until `Decide()` supplies a verdict; client-disconnect / shutdown cancels cleanly. |
| Lifetime | One listener per active Claude workspace; `Teardown` closes it and strips its hook entries from `settings.json`. |

This is the entire production local network surface.

### Always-allow approval rules

`Always` on an approval persists an `AlwaysRule {agent, tool, pattern}` in
`settings.json`. On a later request, perch auto-approves only when the agent,
tool, and tool **input match exactly** (byte-for-byte — never a glob), so a rule
can never grant more than the request the user approved. Rules are listed and
revocable in Settings; a security caveat is surfaced there.

### Global-only agent binary boundary

`[agents].<name>` (absolute binary paths) and
`[default_session].startup_command` live **only** in the global config
(`~/.config/perch/config.toml`). The project-config struct has no corresponding
fields; unknown TOML keys are silently dropped by `BurntSushi/toml`. This is a
structural guarantee — a malicious `.perch.toml` cannot influence which binary
is executed.

### Bound-method input validation

Every argument the Svelte frontend sends over the IPC bridge is validated in
`app.App` before any pty or git operation:

| Constraint | Detail |
|------------|--------|
| Workspace / pane IDs | Validated against `[A-Za-z0-9_-]` charset allowlist; only known workspaces are accepted. |
| Worktree paths | Resolved to absolute paths and verified to lie under the configured `roots`. |

### Other hardening

| Constraint | Detail |
|------------|--------|
| `worktree_dir` in project config | Must be a **relative path** (absolute rejected at load time). |
| Git ref validation | `git.ValidRef` rejects empty strings, leading `-`, `..` sequences, control characters, and other chars forbidden by `git check-ref-format`. |
| Process group teardown | Closing a pty bridge kills the shell's whole process group, so agent children cannot outlive the workspace. |

---

## Capabilities & Degradation

Each Monitor advertises `Caps {approvals, attention, tokens}` via
`Capabilities()`. The frontend reads these and surfaces only the controls the
agent supports — an agent that omits a cap has that surface hidden rather than
showing a dead control. Both `ClaudeMonitor` and `OpencodeMonitor` currently
advertise all three.

---

## Debug Aids

`perch debug discover` is a hidden subcommand (not advertised in help output)
useful for inspecting the discovery pipeline during development. It is
intentionally omitted from user-facing documentation.
