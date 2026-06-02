# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - Unreleased

### Added

- **Wails v2 desktop GUI** — `perch` (no arguments) opens a native desktop
  window: a Go backend embedded in a WebKit2GTK webview driving a Svelte 5 SPA.
  The GUI sidebar lists all agent sessions; clicking any session opens a full
  interactive terminal tab (xterm.js). Multiple tabs can be open simultaneously.
  A directory path argument opens the GUI scoped to that root.

- **Attach-pty terminal bridge** — each GUI terminal tab attaches to the
  underlying tmux pane via `creack/pty` (`internal/pty.Bridge`). Raw pty output
  is streamed to xterm.js over Wails events; keystrokes and resize events flow
  back through bound methods. Agents run in detached tmux sessions and remain
  alive when the GUI window is closed.

- **Bound-method API with input validation** — `app.App` exposes the GUI
  operations (`ListSessions`, `OpenTerminal`, `WriteToPty`, `ResizePty`,
  `CloseTerminal`, `KillSession`, `Diff`, `CreateAgent`) over the Wails IPC
  bridge. Every argument crossing the boundary is validated: session IDs against
  a `[A-Za-z0-9_-]` charset allowlist; worktree paths resolved and confined
  under configured `roots`. All tmux/git work goes through argv via
  `internal/proc`, never a shell.

- **No listening port** — all frontend/backend IPC travels over the WebKit2GTK
  script-message channel; assets are served via the `wails://` custom scheme.
  The `ws://localhost:34115` hot-reload socket is `//go:build dev` only and is
  absent from production binaries.

- **~1 s state sync** — a backend poller reads live session state every
  `refresh_ms` (default 1 000 ms) and emits a `sessions-changed` event to the
  frontend when the session set changes. Each mutating action (create, kill)
  also triggers an optimistic refresh for immediate sidebar feedback.

- **`perch attach <query>`** — fuzzy-match and attach to a live agent session
  from outside the GUI. Returns exit 1 on no match, exit 2 on an ambiguous
  match with the candidate list. The raw query string is never passed to tmux.

- **`perch resurrect`** — `resurrect.Reconcile` classifies each recorded
  window as KEEP (pane live, boot-id match), PRUNE (pane gone, same boot), or
  RESTORE (boot-id mismatch → server restarted, re-launch agent).

- **`perch setup [--replace]`** — detects installed AI coding tools (claude,
  opencode) and writes the `perch status set` hook into each tool's config file.
  Additive and idempotent without `--replace`; with `--replace`, stale
  perch-owned blocks are overwritten while all foreign config is preserved.

- **`perch doctor`** — runtime dependency check: tmux version, git, agent
  binaries, and state dir availability. Reports pass/fail per check.

- **Worktree create/remove with file seeding and lifecycle hooks** — GUI
  new-session action creates a linked git worktree. Files listed under
  `[files].copy` or `[files].symlink` in `.perch.toml` are seeded into the new
  tree. `post_create` hooks run after creation; `pre_remove` hooks run before
  removal. Both hook phases are trust-gated.

- **TOFU trust model for `.perch.toml` hooks** — opening a repo whose
  `.perch.toml` defines shell hooks (`post_create`, `pre_remove`) triggers a
  trust modal: `(a)` always / `(o)` once / `(d)` deny. Trust decisions are
  keyed on the resolved config path and its content hash, stored mode 0600 in
  `<state_dir>/trust.json`. The hash is re-verified immediately before exec to
  close the TOCTOU window.

- **Configurable theme accent, agent binary paths, startup command,
  default/wildcard agent selection, blacklist (glob) UI filter, and sort
  order** — all via `~/.config/perch/config.toml` (global) and `.perch.toml`
  (per-project, walked up from the worktree root). Agent binary paths are a
  global-only security boundary and cannot be set in project config.

- **Two-store JSON state + frecency** — `state.json` holds session→worktree
  mappings and per-project frecency scores (`rank`, `last_accessed`). Each live
  tmux window has its own `windows/<pane_key>.json` record (concurrent-safe,
  16 MiB read cap). Boot-id is stamped on every window record for the resurrect
  classifier.

- **Three-tier config discovery** — perch merges configuration from: (1) the
  global config file, (2) the nearest `.perch.toml` walked up from the project
  root, and (3) built-in defaults. Project config cannot override the global
  agent-binary map.

- **Full security hardening** — path traversal guards on worktree operations,
  relative-only `worktree_dir` in project config, git ref validation, state-file
  size caps, `capture-pane` without `-e` (no escape sequences leaked), the
  global-only agent-binary boundary, and the no-listening-port guarantee. See
  `docs/security-audit.md` for the complete ledger.

- **Linux-only build** — requires WebKit2GTK + GTK3 system libraries. Build
  with `make gui-build` (`-tags production` is required to embed the frontend
  assets).

### Notes

- The `pre_merge` config field and the merge feature (§7.3) are intentionally
  deferred past v0.1.0 and have no implementation. The field is parsed but ignored.
