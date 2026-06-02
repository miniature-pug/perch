# perch — Architecture Reference

> **Audience:** contributors and maintainers who need to understand how perch
> works at the system level. End-user documentation lives in `README.md`.

---

## Overview

perch is a keyboard-first Go TUI for managing AI coding sessions (`claude`,
`opencode`) across git worktrees inside a tmux server.

Key properties:

- **Single static binary** — no daemon, no server process. Everything is driven
  by invoking tmux sub-commands through the `proc.Runner` seam.
- **Two plain-JSON state stores** — no database dependency (see
  [State & discovery](#state--discovery)).
- **Module:** `github.com/Miniature-Pug/perch`
- **Go directive:** `1.25.0` / **toolchain:** `go1.26.2`
- **Charm stack:** bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0
- **tmux pin:** 3.6 (checked by `perch doctor`)
- **Key deps:** `bmatcuk/doublestar/v4` (glob), `sahilm/fuzzy` (fuzzy match),
  `BurntSushi/toml` (config)

---

## The Persistent Frame Model

The normal launch path (`perch` or `perch <path>`) creates a **persistent tmux
frame** — a dedicated tmux session named `perch` with exactly two panes:

```
┌──────────────┬────────────────────────────────────────┐
│  sidebar     │              main slot                  │
│  (TUI via    │  ← agent session swap-paned in here    │
│  --sidebar)  │  (placeholder: sleep infinity at rest) │
└──────────────┴────────────────────────────────────────┘
         perch frame session
```

Design decisions:

| Aspect | Detail |
|--------|--------|
| **Agent sessions** | Each agent runs in its **own detached tmux session**; the frame session is only the UI shell. |
| **Session switching** | Pressing `↵` on a session in the list runs `swap-pane` to move that agent's home pane into the main slot — no `switch-client`, no re-resume. |
| **Placeholder pane** | `sleep infinity` keeps the main slot occupied when no agent is displayed. One placeholder pane exists for the frame's lifetime. |
| **Quit safety** | `q` swaps the currently displayed agent pane **back to its home session** before `kill-session`-ing the frame, so agents survive a perch exit. |
| **Fallback** | If `frame.Ensure` fails (e.g. name conflict), perch falls back to a direct two-pane TUI without the swap-pane switcher. |
| **Focus model** | Once focus is in the agent's main pane, return to the sidebar with the tmux `prefix ←/→` (or `prefix o`). The sidebar remains visible. |

See `docs/diagrams/frame-swap.mmd` for a flowchart of the swap-pane lifecycle.

### 2-Pane Invariant & Death-Resilience

**Invariant:** the frame window always has exactly two panes — the sidebar and
the main slot. The main slot is occupied by one of:
- the live placeholder (`sleep infinity`) when no agent is displayed,
- a displayed agent's pane (swapped in from its home session), or
- a transient dead pane pending recovery (see below).

**Placeholder exile:** `swap-pane` is a bilateral exchange. When an agent pane
is swapped into the main slot, the placeholder is simultaneously exiled into
the agent's home session. When the agent is swapped back home (on close, quit,
or recovery), the placeholder returns to the main slot under its original pane
id. Only one placeholder exists for the frame's lifetime.

**Agent-exit recovery (M17):** the frame window has `remain-on-exit on` set
during creation and re-stamped defensively on every `reuseFrame` call. When a
displayed agent's process exits, its pane goes dead rather than being destroyed,
preserving the frame's 2-pane topology. perch detects the dead displayed pane
through two paths:

1. **Refresh tick** — every `statusTickMsg` (default every 1000 ms) probes
   `PaneDead` on the displayed pane id via `checkDisplayedDeadCmd`.
2. **On close** — `closeWindowCmd` (triggered by `esc` or quit) probes liveness
   and branches: alive → swap home only; dead → full recovery.

**Recovery sequence (order is critical):**
1. `swap-home` — the live placeholder (exiled in the agent's home session)
   returns to the frame main slot; this simultaneously exiles the dead pane into
   the agent's home window. Killing the dead pane before this step would destroy
   it while it still occupied the frame main slot, stranding the placeholder.
2. `kill-pane` the now-exiled dead pane. Only that one pane is removed — sibling
   agent windows in the same project session survive.
3. `select-pane -L` — focus returns to the sidebar.

After recovery the agent shows idle; pressing `↵` resumes it with `--resume`.

**`reuseFrame` self-heal:** when `Ensure` detects an existing frame session it
repairs structural damage from older binaries before returning:
- 2-pane frame with a dead main pane → `respawn-pane` in place (pane id stable).
- 1-pane frame (only the sidebar remains) → `split-window` to recreate the
  main slot with `placeholderCmd`.

In both repair paths `remain-on-exit on` is also re-stamped, so frames created
before M17 gain the guard on next launch.

`internal/frame.Ensure` is the bootstrap primitive. It stamps the sidebar pane
with the `@perch_frame` tmux option (FD-03 guard) so subsequent calls can
identify an existing frame without ambiguity.

---

## Package Map

See `docs/diagrams/architecture.mmd` for the component dependency graph.

| Package | Responsibility |
|---------|----------------|
| `cmd/perch` | CLI entry-point; dispatches subcommands, wires production dependencies, bootstraps the frame, runs sidebar or direct-TUI fallback. |
| `internal/tui` | Keyboard-first Bubble Tea TUI — two-pane list + preview, swap-pane switching, worktree create/remove, command bar (`:`), trust modals, status glyphs. |
| `internal/frame` | Persistent frame bootstrap (`Ensure`, `SidebarContext`); manages the `@perch_frame` marker, sidebar/placeholder split, and resize. |
| `internal/agent` | `Adapter` interface for AI coding tools; concrete adapters for `claude` and `opencode`. Adapters never panic; partial results degrade gracefully. |
| `internal/attach` | `perch attach <query>`: fuzzy-matches a live/known session name and hands the terminal off to it. `Gather`/`Resolve` are separated for testability. |
| `internal/config` | Two-layer TOML config load: global `config.toml` overlaid by project `.perch.toml`. Houses the **agent-binary security boundary** (see [Security model](#security-model)). |
| `internal/discover` | Filesystem scanner: walks `roots` for `.git` entries up to `DefaultMaxDepth` (8), pruning `DefaultPrune` directories (`node_modules`, `vendor`, `.git`). |
| `internal/doctor` | `perch doctor` health check — read-only, no side effects; all OS calls injected for testability. Checks tmux version, agent binaries, config validity. |
| `internal/git` | git subprocess wrappers behind `proc.Runner`; includes `ValidRef` for ref-name validation before any git worktree operation. |
| `internal/match` | `**`-aware glob matching backed by `doublestar`; used for `blacklist` hide-filtering and `[[wildcard]]` agent assignment. Malformed patterns are skipped, never panic. |
| `internal/model` | Shared domain vocabulary (`Window`, `Session`, `Tool`, …) — pure data, no I/O. |
| `internal/proc` | `Runner` interface + `ExecRunner` (production) + `FakeRunner` (tests). All shell-outs in perch must go through this seam. |
| `internal/resurrect` | Boot-id reconcile engine for `perch resurrect` — KEEP / PRUNE / RESTORE classifier, shared `classify()`, `StrandedCount` detector. |
| `internal/state` | Two plain-JSON stores and frecency ranking (zoxide algorithm). Concurrent-safe per-window files; 16 MiB read cap. |
| `internal/status` | `perch status set` writer; writes `@perch_pane_status` on the target pane. |
| `internal/tmux` | tmux command wrappers behind `proc.Runner`; includes `capture-pane` (preview), `swap-pane`, `send-keys`, and cleanup-script helpers. |
| `internal/trust` | TOFU trust store — records `(config-path → content-hash)` approvals in `trust.json` (mode 0600). |
| `internal/worktree` | File seeding (copy/symlink) and lifecycle-hook execution for freshly created git linked worktrees. Deferred remove dispatches a backgrounded cleanup script. |

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
process. This makes the entire non-TUI surface area unit-testable without a
live tmux server or git repo.

Integration tests (`//go:build integration`) use a **private tmux socket** so
they never touch the user's default tmux server. They are run with
`make test-integration` (requires tmux and git).

Coverage target: **≥80% per package**. `internal/tui` is exempt (Bubble Tea
terminal dependency makes unit coverage impractical).

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
   patterns. Matching paths are **hidden from the TUI list** after discovery;
   they are not excluded from the walk itself.

---

## Status Pipeline

See `docs/diagrams/status-sequence.mmd` for the sequence diagram.

```
Agent hook
  → perch status set <working|waiting|done>
    → tmux set-option @perch_pane_status <state> (on agent pane)
      → TUI statusPoll (every refresh_ms, default 1000 ms)
        → list glyph update
```

Glyphs: `🤖` working / `💬` waiting / `✓` done / `●` live (no status set) / `○` idle (session exists, pane not live).

Selecting a session (swap-in) **auto-clears** `@perch_pane_status` on the
focused pane so the badge resets after the user engages.

The `perch setup [--replace]` command installs the agent hooks (claude /
opencode) that call `perch status set`.

---

## Worktree Lifecycle

See `docs/diagrams/worktree-lifecycle.mmd` for the full flowchart.

### Create (`w`)

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

### Remove (`d`)

1. Confirm modal.
2. `pre_remove` hooks executed (trust-gated, TOCTOU re-hash before exec).
3. `git worktree remove` (force if worktree is dirty).
4. Cleanup script dispatched as a backgrounded tmux `run-shell` (kills the
   window, optionally deletes the branch, removes the tree).
5. Shadow window record removed from `windows/<k>.json`.

---

## Security Model

Full details: [`docs/security-audit.md`](docs/security-audit.md).

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

The shared `classify()` ensures `StrandedCount` (the pre-frame read-only
detector) cannot drift from `Reconcile`'s actual decision logic.

### Bootstrap auto-offer

Before bootstrapping the frame, `cmd/perch` checks `StrandedCount`. If
stranded sessions are detected **and** stdin is a TTY, the user is prompted:

```
perch: N session(s) were stranded by a restart. Restore them? [y/N]
```

Default is `N`; only `y`/`yes` triggers `Reconcile`. Non-TTY stdin (piped /
CI) skips the prompt entirely and never blocks. The offer runs **pre-frame** so
`Reconcile` sees the normal (non-frame) tmux topology.

`perch resurrect` (the subcommand) runs `Reconcile` directly and is also
available as the `:resurrect` command bar verb — though `:resurrect` refuses
when run inside the frame and directs the user to invoke it at the shell or
startup instead.

---

## Configuration Reference

Two config files; both TOML.

**Global:** `$XDG_CONFIG_HOME/perch/config.toml` (default
`~/.config/perch/config.toml`)

| Field | Effect |
|-------|--------|
| `roots` | Directories scanned for git repos. |
| `blacklist` | `**`-glob patterns — matching paths are hidden from the TUI list (post-discovery filter, not a walk prune). |
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
