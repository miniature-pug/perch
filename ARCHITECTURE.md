# perch — Architecture Reference

> **Audience:** contributors and maintainers who need to understand how perch
> works at the system level. End-user documentation lives in `README.md`.

---

## Overview

perch is a desktop GUI for managing AI coding sessions (`claude`, `opencode`)
across git worktrees. The default invocation (`perch` or `perch <path>`)
launches a **Wails v2 desktop window**: a Go backend embedded in a WebKit2GTK
webview driving a Svelte 5 SPA. Six CLI subcommands (`setup`, `attach`,
`resurrect`, `status`, `doctor`, `version`) are available for scripting and
hook integration.

Key properties:

- **Single static binary** — no daemon, no server process. Everything is driven
  by invoking tmux sub-commands through the `proc.Runner` seam.
- **Two plain-JSON state stores** — no database dependency (see
  [State & discovery](#state--discovery)).
- **No listening TCP port in production** — IPC between the Svelte frontend
  and the Go backend travels over the WebKit2GTK script-message channel;
  assets are served via the `wails://` custom scheme. The
  `ws://localhost:34115` reload socket is `//go:build dev` only.
- **Module:** `github.com/Miniature-Pug/perch`
- **Go directive:** `1.25.0` / **toolchain:** `go1.26.2`
- **Wails:** v2.12.0 / **Frontend:** Svelte 5 + Vite (in `frontend/`)
- **tmux pin:** 3.6 (checked by `perch doctor`)
- **Key deps:** `bmatcuk/doublestar/v4` (glob), `sahilm/fuzzy` (fuzzy match),
  `BurntSushi/toml` (config), `creack/pty` (attach-pty bridge)
- **Linux only** — requires WebKit2GTK + GTK3 system libraries.

---

## The Wails GUI Model

### Process model

`perch` runs as a single OS process. The Wails runtime embeds a WebKit2GTK
webview inside a GTK window. The Svelte SPA is compiled into the binary at
build time (`-tags production` embeds the `frontend/dist/` assets). There is
no separate frontend process and no HTTP server in production.

```
┌─────────────────────────────────────────────────────────┐
│  perch process                                           │
│  ┌────────────────┐  Wails events + bound methods        │
│  │  Go backend    │ ◄──────────────────────────────────► │
│  │  (app.App)     │         WebKit2GTK webview            │
│  └────────────────┘         (Svelte 5 SPA)               │
│        │                                                  │
│        │ tmux commands via proc.Runner                    │
│        ▼                                                  │
│  tmux server (default socket)                            │
│    agent session A (detached)                            │
│    agent session B (detached)                            │
│    …                                                     │
└─────────────────────────────────────────────────────────┘
```

### Bound-method API

The `app.App` struct is the Wails-bound object. Its exported methods form the
API the Svelte frontend calls over the IPC bridge:

| Method | Purpose |
|--------|---------|
| `ListSessions()` | Return all known sessions with live-status info |
| `OpenTerminal(tabID, sessionID)` | Spawn an attach-pty bridge for a session; start streaming output |
| `WriteToPty(tabID, data)` | Forward keystrokes from the terminal tab to the pty |
| `ResizePty(tabID, cols, rows)` | Propagate terminal resize to the pty |
| `CloseTerminal(tabID)` | Tear down the attach-pty bridge for a tab |
| `KillSession(id)` | Kill the agent's tmux session and remove the shadow record |
| `Diff(worktreePath)` | Return a `git diff` summary for the given worktree |
| `CreateAgent(tool, projectPath, branch)` | Create a new worktree + agent session |

Every argument crossing the IPC boundary is validated inside `app.App`:
session IDs are checked against a `[A-Za-z0-9_-]` charset allowlist;
worktree paths are resolved and verified to lie under the configured `roots`.
All tmux/git work is done via argv through `internal/proc`, never a shell.

### Attach-pty rendering bridge

Opening a terminal tab calls `OpenTerminal`, which spawns `tmux attach-session`
inside a pseudo-terminal via `creack/pty` (`internal/pty.Bridge`). The pty
captures raw byte output and forwards it to the frontend as Wails events;
the Svelte component feeds the bytes to an **xterm.js** terminal. Keystrokes
typed in xterm.js are sent back through `WriteToPty`; terminal resize events
flow through `ResizePty`. This gives every session tab a full interactive
terminal rendered by xterm.js inside the webview.

### State sync

A ~1 s poller calls `ListSessions` internally and emits a `sessions-changed`
event to the frontend whenever the session set changes. The frontend reacts
with an optimistic refresh after each mutating action (create, kill) so the
sidebar stays responsive without waiting for the next poll cycle.

---

## Package Map

See `docs/diagrams/architecture.mmd` for the component dependency graph.

| Package | Responsibility |
|---------|----------------|
| `cmd/perch` | CLI entry-point; dispatches subcommands, wires production dependencies, launches the Wails GUI via `app.Run`. |
| `app/` | Wails `App` struct — bound-method API, input validation (session-id charset allowlist, worktree path containment), attach-pty lifecycle management, ~1 s state-sync poller. |
| `internal/pty` | Attach-pty bridge (`Bridge`): wraps `creack/pty`, runs `tmux attach-session`, batches and forwards pty output as Wails events, routes keystrokes and resize back to the pty. |
| `internal/agent` | `Adapter` interface for AI coding tools; concrete adapters for `claude` and `opencode`. Adapters never panic; partial results degrade gracefully. |
| `internal/attach` | `perch attach <query>`: fuzzy-matches a live/known session name and hands the terminal off to it. `Gather`/`Resolve` are separated for testability. |
| `internal/config` | Two-layer TOML config load: global `config.toml` overlaid by project `.perch.toml`. Houses the **agent-binary security boundary** (see [Security model](#security-model)). |
| `internal/discover` | Filesystem scanner: walks `roots` for `.git` entries up to `DefaultMaxDepth` (8), pruning `DefaultPrune` directories (`node_modules`, `vendor`, `.git`). Returns paths containing a `.git` entry. |
| `internal/doctor` | `perch doctor` health check — read-only, no side effects; all OS calls injected for testability. Checks tmux version, agent binaries, config validity. |
| `internal/git` | git subprocess wrappers behind `proc.Runner`; includes `ValidRef` for ref-name validation before any git worktree operation. |
| `internal/match` | `**`-aware glob matching backed by `doublestar`; used for `blacklist` hide-filtering and `[[wildcard]]` agent assignment. Malformed patterns are skipped, never panic. |
| `internal/model` | Shared domain vocabulary (`Window`, `Session`, `Tool`, …) — pure data, no I/O. |
| `internal/proc` | `Runner` interface + `ExecRunner` (production) + `FakeRunner` (tests). All shell-outs in perch must go through this seam. |
| `internal/resurrect` | Boot-id reconcile engine for `perch resurrect` — KEEP / PRUNE / RESTORE classifier, shared `classify()`, `StrandedCount` detector. |
| `internal/state` | Two plain-JSON stores and frecency ranking (zoxide algorithm). Concurrent-safe per-window files; 16 MiB read cap. |
| `internal/status` | `perch status set` writer; writes `@perch_pane_status` on the target pane. |
| `internal/tmux` | tmux command wrappers behind `proc.Runner`; includes `list-panes`, `new-session`, `send-keys`, and session-management helpers. |
| `internal/trust` | TOFU trust store — records `(config-path → content-hash)` approvals in `trust.json` (mode 0600). |
| `internal/worktree` | File seeding (copy/symlink) and lifecycle-hook execution for freshly created git linked worktrees. Deferred remove dispatches a backgrounded cleanup script. |
| `frontend/` | Svelte 5 SPA (Vite build); communicates with Go via Wails events and bound methods; renders agent terminals via xterm.js. |

---

## The Runner Seam (§20.1)

Every shell-out in perch — tmux commands, git operations, hook execution — goes
through the `proc.Runner` interface declared in `internal/proc`:

```
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}
```

Production code uses `ExecRunner` (wraps `os/exec`). Unit tests inject
`FakeRunner`, which records calls in a `.Calls` slice and never spawns a real
process. This makes the entire non-frontend surface area unit-testable without a
live tmux server or git repo.

Integration tests (`//go:build integration`) use a **private tmux socket** so
they never touch the user's default tmux server. They are run with
`make test-integration` (requires tmux and git).

Coverage target: **≥80% per package**. `frontend/` is exempt (browser-rendered
Svelte components require a headless browser for unit coverage).

---

## State & Discovery

See `docs/diagrams/discovery-state.mmd` for the full data-flow diagram.

### State stores

Both stores live under `$XDG_STATE_HOME/perch/` (fallback
`~/.local/state/perch/`):

| File | Contents |
|------|----------|
| `state.json` | `mappings` (session → worktree choice) + `projects` (frecency `rank`/`last_accessed` per project root). |
| `windows/<k>.json` | One JSON file per live tmux window. Isolated files mean concurrent perch instances never clobber each other on launch or kill. Each record carries a **`boot_id`** (`#{start_time}` of the tmux server at window-creation time). |

Both files are read through `readLimited` (capped at 16 MiB) to prevent
resource exhaustion from malformed or maliciously large files.

### Frecency ranking

Projects are ordered by a zoxide-style frecency score (rank × recency
multiplier). The rank decays (`×0.9`) when total weight exceeds a ceiling.
`sort_order` (default `["running","frecency"]`) is applied after frecency
scoring: rows with a live agent session sort first, then frecency order within
each tier.

### Discovery pipeline

1. **Roots** — directories listed in the global `config.toml` `roots` field.
2. **Scan** — `internal/discover.Scan` walks each root up to depth 8, pruning
   `node_modules`, `vendor`, and `.git`. Returns paths containing a `.git`
   entry.
3. **Frecency sort** — `internal/discover.Projects` (catalog layer) merges scan
   output with frecency stats and returns projects ordered by score.
4. **Blacklist filter** — the `blacklist` config field contains `**`-glob
   patterns. Matching paths are **hidden from the GUI sidebar** after discovery;
   they are not excluded from the walk itself.

---

## Status Pipeline

See `docs/diagrams/status-sequence.mmd` for the sequence diagram.

```
Agent hook
  → perch status set <working|waiting|done>
    → tmux set-option @perch_pane_status <state> (on agent pane)
      → backend poller (every refresh_ms, default 1000 ms)
        → sessions-changed event → GUI sidebar glyph update
```

Glyphs: `🤖` working / `💬` waiting / `✓` done / `●` live (no status set) / `○` idle (session exists, pane not live).

Opening a terminal tab (swap-in equivalent) **auto-clears** `@perch_pane_status`
on the focused pane so the badge resets after the user engages.

The `perch setup [--replace]` command installs the agent hooks (claude /
opencode) that call `perch status set`.

---

## Worktree Lifecycle

See `docs/diagrams/worktree-lifecycle.mmd` for the full flowchart.

### Create

1. Preflight checks (branch name validation via `git.ValidRef`).
2. **Trust gate** — if `.perch.toml` defines `post_create` hooks, a modal
   prompts `(a) trust always / (o) once / (d) deny`. Deny skips hooks; proceed
   continues.
3. `git worktree add` in the configured `worktree_dir`.
4. File seeding — `[files].copy` and `[files].symlink` entries copied/linked
   into the new worktree.
5. `post_create` hooks executed (trust-gated, TOCTOU re-hash before exec).
6. Agent launched in a new tmux session; window record written to
   `windows/<k>.json`.

### Remove

1. Confirm modal.
2. `pre_remove` hooks executed (trust-gated, TOCTOU re-hash before exec).
3. `git worktree remove` (force if worktree is dirty).
4. Cleanup script dispatched as a backgrounded tmux `run-shell` (kills the
   window, optionally deletes the branch, removes the tree).
5. Shadow window record removed from `windows/<k>.json`.

---

## Security Model

Full details: [`docs/security-audit.md`](docs/security-audit.md).

### No listening port

perch opens no TCP or Unix socket in production. The Wails webview and Go
backend communicate over the WebKit2GTK script-message channel; the frontend
is served from the embedded binary via the `wails://` custom scheme. The
`ws://localhost:34115` hot-reload socket is `//go:build dev` only and is
stripped from production builds. This eliminates an entire class of
network-based attack surface.

### Trust (TOFU on `.perch.toml`)

Opening a repo whose `.perch.toml` defines shell hooks (`post_create` or
`pre_remove`) triggers a trust prompt:

- `(a)` trust always — approves this config path + content hash permanently.
- `(o)` once — runs hooks this time, does not persist the approval.
- `(d)` deny — hooks are skipped; the worktree operation continues without them.

Approvals are stored in `<state-dir>/trust.json` (mode 0600), keyed on the
**resolved config path** and the **SHA-256 hash of the file's bytes**. Editing
the file changes the hash, requiring re-approval.

Before each hook execution, perch **re-hashes the file** and compares it to the
approved hash (TOCTOU guard). A mismatch aborts the hooks.

### Global-only agent binary boundary

`[agents].<name>` (absolute binary paths) and
`[default_session].startup_command` live **only** in the global config
(`~/.config/perch/config.toml`). The `projectConfig` struct has no
corresponding fields; unknown TOML keys are silently dropped by
`BurntSushi/toml`. This is a structural guarantee — a malicious `.perch.toml`
cannot influence which binary is executed, and `startup_command` runs without a
trust prompt precisely because it is sourced from the user's own global config.

### Bound-method input validation

Every argument the Svelte frontend sends over the IPC bridge is validated in
`app.App` before any tmux or git operation:

| Constraint | Detail |
|------------|--------|
| Session IDs | Validated against `[A-Za-z0-9_-]` charset allowlist; length-capped. Only sessions perch already knows about are accepted (`liveSession` allowlist check). |
| Worktree paths | Resolved to absolute paths and verified to lie under the configured `roots` (`validateWorktreeUnderRoots`). |

### Other hardening

| Constraint | Detail |
|------------|--------|
| `worktree_dir` in project config | Must be a **relative path** (absolute rejected at load time). |
| Git ref validation | `git.ValidRef` rejects empty strings, leading `-`, `..` sequences, control characters, and other chars forbidden by `git check-ref-format`. |
| State file size cap | `readLimited` caps reads at **16 MiB**; a file exceeding the cap is an error. |
| `capture-pane` flags | Preview uses `capture-pane -p` only — the `-e` (raw escape passthrough) flag is **never passed**, enforced by a locked unit test. |

---

## Resurrect

See `docs/diagrams/discovery-state.mmd` for how `boot_id` flows through the
state stores.

### Boot-id reconcile

Each `windows/<k>.json` record carries the tmux server's `#{start_time}` at
the moment the window was created (`BootID`). After a tmux server restart the
`start_time` changes, so live pane IDs from before the restart are gone.

`resurrect.Reconcile` (and the read-only `StrandedCount`) share a single
`classify(w, livePanes, currentBoot)` function that assigns one of three
actions to each recorded window:

| Action | Condition | Effect |
|--------|-----------|--------|
| **KEEP** | Pane is live **and** `BootID` matches the current server | No-op |
| **PRUNE** | Pane is gone, same boot, home session is still alive | Remove the stale record |
| **RESTORE** | Boot mismatch or server is cold (no current boot) | Re-launch the agent |

The shared `classify()` ensures `StrandedCount` (the pre-launch read-only
detector) cannot drift from `Reconcile`'s actual decision logic.

`perch resurrect` (the subcommand) runs `Reconcile` directly.

---

## Configuration Reference

Two config files; both TOML.

**Global:** `$XDG_CONFIG_HOME/perch/config.toml` (default
`~/.config/perch/config.toml`)

| Field | Effect |
|-------|--------|
| `roots` | Directories scanned for git repos. |
| `blacklist` | `**`-glob patterns — matching paths are hidden from the GUI sidebar (post-discovery filter, not a walk prune). |
| `sort_order` | Priority list for the project selector (e.g. `["running","frecency"]`). |
| `refresh_ms` | Status-poll interval in milliseconds (default 1000). |
| `worktree_dir` | Default worktree parent directory (absolute allowed in global config). |
| `[default_session].agent` | Default AI tool (`claude` or `opencode`). |
| `[default_session].startup_command` | Command sent to the agent via `send-keys` after launch. Global-only (never trust-prompted). |
| `[theme].accent` | UI accent colour. |
| `[agents].<name>` | Absolute binary path for the named tool. **Global-only security boundary.** |

**Project:** `.perch.toml` (walked up from the project root)

| Field | Effect |
|-------|--------|
| `base_branch` | Branch used as the base for new worktrees. |
| `worktree_dir` | Relative-only worktree parent directory. |
| `agent` | Per-project default tool. |
| `post_create` | Shell hooks run after worktree creation (trust-gated). |
| `pre_remove` | Shell hooks run before worktree removal (trust-gated). |
| `[files].copy` / `[files].symlink` | Files seeded into each new worktree. |
| `[[wildcard]]` | Path-glob → agent assignment rules (matched against worktree path). |

**Tool resolution priority for new sessions:**
1. Existing session metadata (tool already known) — always wins.
2. First `[[wildcard]]` rule whose pattern matches the worktree path.
3. `[default_session].agent` / project `agent`.
4. `"claude"` — unconditional final fallback.

---

## Debug Aids

`perch debug discover` and `perch debug tmux` are hidden subcommands (not
advertised in help output) useful for inspecting the discovery pipeline and
tmux state during development. They are intentionally omitted from user-facing
documentation.
