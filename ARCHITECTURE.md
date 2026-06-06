# perch — Architecture Reference

> **Audience:** contributors and maintainers who need to understand how perch
> works at the system level. End-user documentation lives in `README.md`.

---

## Overview

perch is a worktree-native AI-agent **cockpit**: a desktop GUI for running and
supervising AI coding agents (`claude`, `opencode`) across git worktrees. The
default invocation (`perch` or `perch <path>`) launches a **Wails v2 desktop
window**: a Go backend embedded in a WebKit2GTK webview driving a Svelte 5
(runes) SPA. A small set of CLI subcommands (`attach`, `doctor`,
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
  (glob), `BurntSushi/toml` (config)
- **Linux only** — requires WebKit2GTK + GTK3 system libraries.

---

## The Wails GUI Model

### Process model

`perch` runs as a single OS process. The Wails runtime embeds a WebKit2GTK
webview inside a GTK window. The Svelte SPA is compiled into the binary at build
time (`-tags production` embeds the `frontend/dist/` assets). There is no
separate frontend process and no HTTP server for IPC in production.

**Single-instance lock.** `app/options.go` registers a Wails
`SingleInstanceLock` (unique id `com.miniature-pug.perch` — a D-Bus-safe
reverse-DNS string; on Linux Wails folds it into the bus name and only sanitizes
`-`/`.`, not `/`). If a second
`perch` process is launched (including `perch attach <query>`), Wails forwards
`os.Args` to the already-running instance via `OnSecondInstanceLaunch`
(`app.onSecondInstance`), which raises the window (`WindowUnminimise` +
`WindowShow`) and emits the Go→frontend event `"workspace:attach"` with payload
`{query}`. The second process then exits (non-zero on Linux — expected behaviour
of the forwarding path). When no perch instance is running, `perch attach`
simply launches the GUI normally.

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
| `RemoveWorkspace(id)` | Remove a session: for worktree sessions runs `git worktree remove` (errors `ErrWorktreeDirty` on uncommitted changes); for non-worktree sessions drops the record only. Branch never deleted. |
| `ForceRemoveWorkspace(id)` | Force-remove the linked worktree tree (discards uncommitted changes); branch kept. |
| `ListStaleSessions()` | Return worktree sessions unused past `StaleThresholdDays` with per-row dirty/merged state. |
| `CleanupSessions(ids, force)` | Bulk-remove stale sessions: `git worktree remove` + `git branch -d` (or `-D` when force). |
| `HomeShellCwd()` | Return the cwd for the home shell (launch directory or `$HOME`). |
| `OpenShell(paneID, cwd)` | Spawn an auxiliary login-shell pty; `paneID == "shell-home"` bypasses worktree-root containment. |
| `Approve(reqID, decision)` | Resolve a pending `PreToolUse` approval (allow / always / deny) |
| `DiffStat / Hunks / StageHunk / DiscardHunk` | git diff view + staging per worktree |
| `Branches` | git metadata for a repo |
| `ListDir / ReadFile / WriteFile / RevealInFiles / CopyPath` | file-tree operations |
| `GetSettings / SaveSettings / GetLayout / SaveLayout` | persisted UI state |
| `SetWindowFocus(focused)` | Track window focus so OS notifications fire only while unfocused |
| `DiscoverRepos()` | Enumerate git repos under the configured roots for the New Session dialog |

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
Prepare(ctx, workspaceID, cwd, resumeID, model) (launchCmd, err)
Start(ctx)                 // launch the event pump bound to ctx
Events() <-chan Event
Approve(reqID, Decision) error
Capabilities() Caps        // {approvals, attention}
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

Key Go→frontend events:

| Event name | Payload | Trigger |
|------------|---------|---------|
| `agent:event` | `AgentEvent` | Monitor produces a `state`, `approval`, or `question` event |
| `fs:changed` | `{workspaceId, path}` | Per-workspace fsnotify fires (debounced) |
| `notify` | notification record | `dispatchNotify` emits a blocking/ambient/routine notification |
| `pty:data:<paneId>` | `[]int` (byte values) | pty bridge read loop |
| `pty:exit:<paneId>` | `{code}` | pty process exits |
| `workspace:attach` | `{query}` | Second-instance lock → `onSecondInstance`; frontend routes query to workspace selection |

On each `fs:changed` event the frontend calls `DiffStat(worktreePath)` for the
affected workspace and aggregates the per-file counts into a `+N −N` display in
the sidebar row and the status line.

**Sidebar collapse.** The sidebar is collapsible via `Ctrl-b` or a toggle rail
button. Collapsed state is persisted in the layout store under the key
`"sidebar"` (the same `layout.collapsed` map used for the shell drawer), so it
survives across launches.

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
| `internal/config` | Single-layer global TOML config load: reads `config.toml` and exposes `Config{Roots}`. No project overlay; unknown keys are silently dropped. |
| `internal/discover` | Filesystem scanner: walks `roots` for `.git` entries up to `DefaultMaxDepth`, pruning `node_modules`/`vendor`/`.git`. |
| `internal/doctor` | `perch doctor` health check — read-only, all OS calls injected for testability. |
| `internal/git` | git subprocess wrappers behind `proc.Runner`; includes `ValidRef` for ref-name validation, worktree management, and diff/hunk staging. |
| `internal/fs` | Worktree filesystem helpers: directory listing for the file tree and a change watcher. |
| `internal/model` | Shared domain vocabulary (`Tool`, …) — pure data, no I/O. |
| `internal/notify` | Notification tiering (blocking / ambient) for agent lifecycle events. |
| `internal/proc` | `Runner` interface + `ExecRunner` (production) + `FakeRunner` (tests). All shell-outs go through this seam. |
| `frontend/` | Svelte 5 (runes) SPA (Vite build); communicates with Go via Wails bindings and events; renders agent terminals via xterm.js. |

---

## Configuration & Constants

### Go named constants

Every tuning value that was formerly a magic number is now a named
package-level constant (`const`). This covers: pty default cols/rows, read
buffer sizes, file modes, debounce and poll intervals, hook-listener token
size, frecency multipliers, dbus notification addresses, event-name prefixes,
and more. Agent strings (`"claude"`, `"opencode"`) are no longer used as raw
literals; all comparison and dispatch sites use `model.ToolClaude` and
`model.ToolOpencode`.

Two cross-package constants are exported so the owning package is the single
source:

- **`hooklistener.LoopbackHost`** (`"127.0.0.1"`) — the loopback address used
  by `ClaudeMonitor` and `OpencodeMonitor`; exported from `hooklistener` (the
  canonical network owner). `agent` already imports `hooklistener`, so no new
  import edge is introduced.
- **`registry.ConfigDirMode`** (`0o700`) — the config-directory creation mode;
  exported from `registry` (the canonical config-dir owner) and referenced by
  `app`.

### Single XDG config-dir resolver

The XDG config-dir computation (`$XDG_CONFIG_HOME/perch`, falling back to
`~/.config/perch`) is single-sourced:

- `registry.DefaultConfigDir()` is the canonical implementation.
- `config.DefaultGlobalPath()` delegates to it (`filepath.Join(registry.DefaultConfigDir(), "config.toml")`).
- The application-directory name `"perch"` is defined exactly once as the
  unexported constant `registry.appName`.
- The config-directory creation mode (`0o700`) is the exported constant
  `registry.ConfigDirMode`; `app` uses it directly instead of a local copy.

Previously both `internal/config` and `internal/registry` each contained their
own copy of this resolution logic; they now share a single source.

### Frontend constants (`frontend/src/lib/constants.ts`)

All frontend tuning values are centralized in `frontend/src/lib/constants.ts`:

- **Timers** — `AMBIENT_DISMISS_MS`, `ROUTINE_DISMISS_MS`,
  `LAYOUT_SAVE_DEBOUNCE_MS`, `UNDO_REMOVE_DELAY_MS`.
- **Limits** — `CMD_RECENCY_MAX`, `TERMINAL_SCROLLBACK`, `PTY_MAX_DIM`.
- **Layout defaults & resize clamps** — sidebar/shell default sizes and min/max
  bounds.
- **Settings defaults** — default theme, density, font, agent, and model.
  These mirror the Go source of truth in `app/app.go` (`GetSettings`
  absent-file branch). The duplication across the IPC boundary is inherent —
  there is no shared module between Go and the Svelte SPA — so the two sides
  must be kept in sync manually.
- **Option lists** — `THEMES`, `DENSITIES`, `FONTS` arrays (used by
  `SettingsPanel`).
- **Drag MIME types** — `MIME_SESSION = "application/x-perch-session"`, `MIME_TEXT = "application/x-perch-text"`.
- **`@mention` protocol prefix** and **localStorage keys**.

Wails event names (`EVT_AGENT`, `EVT_FS_CHANGED`, `EVT_NOTIFY`,
`EVT_PTY_DATA_PREFIX`, `EVT_PTY_EXIT_PREFIX`, `EVT_WORKSPACE_ATTACH`) are
named `EVT_*` constants in `frontend/src/lib/wails.ts` (the IPC seam), kept
separate from non-IPC tuning values.

The Vite preview port (`4173`) is single-sourced in
`frontend/preview-port.mjs`, which is consumed by `vite.config.ts`,
`playwright.config.ts`, and the e2e spec files.

### CSS design-token additions

`frontend/src/tokens/tokens.css` was extended with:

- `--perch-shadow-float` — shared shadow for floating surfaces (previously
  inlined in 7 components).
- `--perch-scrim` — overlay backdrop colour (previously inlined in 5
  components).
- `--perch-z-*` stacking scale — a complete named z-index ladder
  (`--perch-z-sidebar-rail` through `--perch-z-command-palette`). The previous
  z-index 300/300 collision between the command palette and the undo toast is
  resolved: `--perch-z-undo-toast: 300`, `--perch-z-command-palette: 310`.
- `--perch-fs-shell` / `--perch-lh-shell` — shell font-size and line-height
  tokens; `Terminal.svelte` reads these instead of hardcoding `13px`/`1.5`.
- `--perch-scrollbar-w: 6px` — custom scrollbar width; replaces 6 inline
  `::-webkit-scrollbar { width: 6px }` literals.
- `--perch-scrollbar-radius: 3px` — scrollbar thumb radius; replaces 6 inline
  `-webkit-scrollbar-thumb { border-radius: 3px }` literals.
- `--perch-opacity-disabled: 0.4` — disabled-control opacity policy; replaces 4
  inline `:disabled { opacity: 0.4 }` literals.

`SettingsPanel`'s dead/wrong hex fallbacks were removed; all colour references
now use the token system.

---

## The Runner Seam (§20.1)

Every shell-out in perch — git operations, hook execution — goes through the
`proc.Runner` interface declared in `internal/proc`:

```
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
    RunInDir(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
    RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error)
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
| `worktreePath` | Absolute path to the git worktree (equals `repoPath` for non-worktree sessions). |
| `repoPath` | Absolute path to the source repository root. |
| `worktree` | `true` for an isolated linked-tree session; `false` for a non-worktree (in-repo) session. |
| `agent` | `claude` or `opencode`. |
| `branch` | Worktree branch. |
| `baseRef` | Branch the worktree was created from (used for cleanup merged-check). |
| `title` | Display label. |
| `lastSessionID` | Resume id for the agent (passed to `Monitor.Prepare`). |
| `lastActive` | Timestamp for ordering. |

Settings (`settings.json`, including persisted `AlwaysRules` and
`StaleThresholdDays` — default 30, controls the stale-cleanup banner trigger)
and saved layout (`layout.json`) live in the same config directory.

### Discovery pipeline

1. **Roots** — directories perch scans for git repos (launch cwd by default,
   plus any configured roots).
2. **Scan** — `internal/discover.Scan` walks each root up to the max depth,
   pruning `node_modules`, `vendor`, and `.git`; returns paths containing a
   `.git` entry.

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

`ClaudeMonitor` installs exactly four hooks (`PreToolUse`, `Stop`,
`StopFailure`, `SessionStart`). The non-`PreToolUse` events are translated into
lifecycle `Event`s (`SessionStart` → `running`, `Stop` → `done`, `StopFailure`
→ `errored`) and forwarded the same way. perch is **not** a usage meter: there
is no token or cost metering, no `usage` event kind, and no transcript tailing.
For **opencode**, the equivalent events arrive over the `opencode serve` SSE
stream — `session.status` (busy/idle, and the only default-emitted frame
carrying the `sessionID` used for resume), `session.error` (→ `errored`), and
the question frames below. opencode's experimental `session.next.step.*` frames
are gated behind `OPENCODE_EXPERIMENTAL_EVENT_SYSTEM`, which perch never sets,
so they never fire and are not consumed.

Claude status reporting is automatic — `ClaudeMonitor` writes the per-session hook
config (listener URL + token) into the worktree's `.claude/settings.json` when
the workspace opens. opencode exposes session status natively via its SSE stream
(`opencode serve`), so no plugin file is needed for opencode.

### Event kinds, states, and the attention/notification model

A unified `agent.Event` carries a `Kind` (`state`, `approval`, or `question`)
and, for state-bearing events, a `State`. The full attention state set is six
values:

| State | Meaning |
|-------|---------|
| `running` | The agent is working a turn. |
| `idle` | Steady idle (e.g. reported at connect, or a non-turn idle). |
| `awaiting-approval` | A `PreToolUse` tool call is blocked on the user's Allow/Deny/Always verdict. |
| `awaiting-input` | The agent is asking the **user** a question/choice — distinct from a tool approval. |
| `done` | A turn completed (busy→idle / claude `Stop`); drives the §8 "Turn complete" ambient toast. |
| `errored` | The agent reported a failure. |

**Approval vs. question.** An *approval* (`Kind: "approval"`,
`awaiting-approval`) is a request to act on the system that perch gates behind
the `ApprovalCard` until the user decides. A *question* (`Kind: "question"`,
`awaiting-input`) is the agent asking the user to choose — claude's
`AskUserQuestion` (its `PreToolUse` is **auto-allowed** so the agent renders the
question in its own pane TUI) and opencode's `question.asked`. A question is a
**signal only**: perch renders no question card and sends no reply; the user
answers in the agent's own pane TUI. claude's `ExitPlanMode` stays on the normal
approval path (auto-allowing it would skip the user's plan review). opencode's
`question.replied` resumes `running` and `question.rejected` falls back to a
steady `idle`.

**Sidebar "feels".** Each attention state has a glanceable look in the sidebar
(`Sidebar.svelte`), color + icon + label (never color alone, for WCAG):

| State | Icon | Label | Color / motion |
|-------|------|-------|----------------|
| `awaiting-approval` | ⚠ | needs you | `--perch-warn` amber, fast pulse (1s) |
| `awaiting-input` | ? | asking you | `--perch-info` cyan, slow pulse (1.6s) |
| `done` | ✓ | done | `--perch-ok` green, steady |
| `errored` | ✗ | error | `--perch-err` red, steady |
| `running` | ◐ | running | dim, steady |
| `idle` | ◯ | idle | dim, steady |

Pulses are suppressed under `prefers-reduced-motion`.

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
3. The workspace record is removed from `workspaces.json`.
   - **Worktree session** — `git worktree remove` deletes the linked tree from
     disk. If the tree has uncommitted changes (`ErrWorktreeDirty`), a
     force-confirm is required (`ForceRemoveWorkspace`). The branch is **never**
     deleted by remove — that is the cleanup panel's job.
   - **Non-worktree (in-repo) session** — the record is dropped; the repo root
     and its branch are never touched (no git op).

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

`Always` on an approval persists an `AlwaysRule {agent, tool, pattern, hash}` in
`settings.json`. The `hash` is the SHA-256 of the **full** tool input captured
when the user clicked Always; `pattern` is a truncated copy kept only for display
and is **not** the security boundary. On a later request, perch auto-approves only
when the agent, the tool, and the input **hash** match exactly — a rule with no
hash (or a request with no input hash) never auto-approves. Hashing the full input
means two calls sharing a 4096-byte prefix but differing afterwards cannot collide,
so a rule can never grant more than the exact request the user approved. Rules are
listed and revocable in Settings; a security caveat is surfaced there.

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
| Git ref validation | `git.ValidRef` rejects empty strings, leading `-`, `..` sequences, control characters, and other chars forbidden by `git check-ref-format`. |
| Process group teardown | Closing a pty bridge kills the shell's whole process group, so agent children cannot outlive the workspace. |

---

## Capabilities & Degradation

Each Monitor advertises `Caps {approvals, attention}` via `Capabilities()`. The
frontend reads these and surfaces only the controls the agent supports — an
agent that omits a cap has that surface hidden rather than showing a dead
control. Both `ClaudeMonitor` and `OpencodeMonitor` currently advertise both.

---

## Debug Aids

`perch debug discover` is a hidden subcommand (not advertised in help output)
useful for inspecting the discovery pipeline during development. It is
intentionally omitted from user-facing documentation.
