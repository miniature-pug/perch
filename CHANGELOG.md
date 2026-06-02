# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - Unreleased

### Added

- **Persistent-frame TUI** — `perch` bootstraps a dedicated `perch` tmux session
  with a sidebar pane (the TUI) and a live main pane. Selecting an agent session
  swap-panes it into the main slot; quitting swaps the agent home before killing
  the frame so agent sessions survive. Falls back to a direct two-pane TUI when
  `frame.Ensure` fails.

- **`:` command bar** — press `:` inside the TUI to open an inline command prompt.
  Supported verbs: `q`/`quit`, `new`, `attach <query>`, `proj`/`project <name>`,
  `setup [--replace]`, `doctor`, `resurrect`, `help`/`h`/`?`. `:resurrect` is
  intentionally blocked inside the frame (the user is directed to the startup
  auto-offer or shell instead).

- **`perch attach <query>`** — fuzzy-match and attach to a live agent session from
  outside the TUI. Returns exit 1 on no match, exit 2 on an ambiguous match with
  the candidate list. The raw query string is never passed to tmux.

- **`perch resurrect` + restart auto-offer** — `resurrect.Reconcile` classifies
  each recorded window as KEEP (pane live, boot-id match), PRUNE (pane gone,
  same boot), or RESTORE (boot-id mismatch → server restarted, re-launch agent).
  At bootstrap, if stranded sessions are detected, perch offers an interactive
  prompt (tty-gated, default N) before the frame is created.

- **`perch setup [--replace]`** — detects installed AI coding tools (claude,
  opencode) and writes the `perch status set` hook into each tool's config file.
  Additive and idempotent without `--replace`; with `--replace`, stale
  perch-owned blocks are overwritten while all foreign config is preserved.

- **`perch doctor`** — runtime dependency check: tmux version, git, agent binaries,
  and state dir availability. Reports pass/fail per check.

- **Worktree create/remove with file seeding and lifecycle hooks** — press `w` to
  create a linked git worktree. Files listed under `[files].copy` or
  `[files].symlink` in `.perch.toml` are seeded into the new tree.
  `post_create` hooks run after creation; `pre_remove` hooks run before removal.
  Both hook phases are trust-gated.

- **TOFU trust model for `.perch.toml` hooks** — opening a repo whose `.perch.toml`
  defines shell hooks (`post_create`, `pre_remove`, `pre_merge`) triggers a trust
  modal: `(a)` always / `(o)` once / `(d)` deny. Trust decisions are keyed on the
  resolved config path and its content hash, stored mode 0600 in
  `<state_dir>/trust.json`. The hash is re-verified immediately before exec to
  close the TOCTOU window.

- **Configurable theme accent, agent binary paths, startup command, default/wildcard
  agent selection, blacklist (glob) UI filter, and sort order** — all via
  `~/.config/perch/config.toml` (global) and `.perch.toml` (per-project, walked
  up from the worktree root). Agent binary paths are a global-only security
  boundary and cannot be set in project config.

- **Two-store JSON state + frecency** — `state.json` holds session→worktree
  mappings and per-project frecency scores (`rank`, `last_accessed`). Each live
  tmux window has its own `windows/<pane_key>.json` record (concurrent-safe,
  16 MiB read cap). Boot-id is stamped on every window record for the resurrect
  classifier.

- **Three-tier config discovery** — perch merges configuration from: (1) the global
  config file, (2) the nearest `.perch.toml` walked up from the project root, and
  (3) built-in defaults. Project config cannot override the global agent-binary
  map.

- **Full security hardening** — path traversal guards on worktree operations,
  relative-only `worktree_dir` in project config, git ref validation, state-file
  size caps, `capture-pane` without `-e` (no escape sequences leaked), and
  the global-only agent-binary boundary. See `docs/security-audit.md` for the
  complete ledger.

### Notes

- The `pre_merge` config field and the merge feature (§7.3) are intentionally
  deferred past v0.1.0 and have no implementation. The field is parsed but ignored.
