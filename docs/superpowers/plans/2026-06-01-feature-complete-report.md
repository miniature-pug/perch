# perch — Feature-Complete Report (2026-06-01, session 2)

Branch `feat/perch-v1`. This session added 28 commits on top of the overnight run
(`983fd70` → `ffaed92`). **Closing gate: ALL GREEN** — build, vet, gofmt, `go test
-race -tags=integration ./...` (all packages), `golangci-lint` 0 issues, `go mod
verify`, `make vulncheck` "No vulnerabilities found", binary **5.3 MB**.

perch is now **feature-complete** for v0.1.0 (pending your manual smoke-run + tag).

## ✅ Shipped this session

### M11-1 — `:` command bar
Vim-style command bar (textinput overlay, separate from the `/` list filter):
`:q`, `:new`, `:attach <query>`, `:proj <name>`, `:setup [--replace]`, `:doctor`,
`:resurrect`, `:help`. Pure `parseCommand` (exhaustively tested) + thin dispatch.
`:attach`/`:proj` reuse the existing Enter activation path. **`:resurrect` is
gated to refuse inside the frame** (its reconcile was audited against normal
topology, not the parked-placeholder frame; ExecProcess-pause would staleify
`displayedPaneID`) — it points the user to startup / a shell.

### M11-3 — auto-offer-resurrect on restart
`bootstrap()` detects (read-only `StrandedCount`, sharing `classify` with
`Reconcile` so it can't drift) whether a tmux-server restart stranded sessions,
and — **before building the frame** (safe: no frame exists yet at restart) —
offers (tty-gated y/N, default N) to restore them. Clean relaunch does NOT offer
(a cleanly-closed pane is a PRUNE, not a RESTORE) — the acceptance test.

### M12 — TUI polish
- **Contrast:** split a readable `colorMuted` (text) from the border-only
  `colorSubtle`; footer/empty-state now legible on dark terminals.
- **`c` collapse-sidebar** toggle (alias to the existing full-preview mode).
- **Help/footer:** Enter reads "open"; `?` documents the `prefix ←/→` refocus
  gesture and `c`/`:`.
- **Modal/help over a dimmed body:** new cell-accurate (`x/ansi`) `composite`
  helper renders confirmations over a dimmed sidebar instead of replacing it —
  the emoji wide-rune offset test is the canary; height invariant preserved.

### M14 — config completion (the big one)
The audit found **9 documented-but-dead config knobs.** Per your ruling
(wire the unambiguous; implement planned; remove unplanned):
- **Wired:** `[theme].accent` → UI accent; `[agents].<name>` → launch binary
  (the global-only security boundary is now real); `[default_session].startup_command`
  → sent to the agent pane after launch; default agent + `[[wildcard]]` path-globs
  → new-session tool resolution (existing-metadata → wildcard → default → `claude`);
  `blacklist` (doublestar `**` globs) → post-discovery UI hide-filter; `sort_order`
  → ordering (live-sessions-first, then frecency).
- **Removed:** `pre_merge` — its parent feature (§7.3 merge) is **explicitly
  optional / v1-slippable per plan.md**; not built at the finish line.
- **Dropped:** the `pinned` sort tier — pinning is no planned feature (only a
  token); unknown sort tokens are now ignored gracefully.
- **Keystone refactor:** the loaded `*config.Config` is now threaded once through
  `tui.Config → loader → Model` (`GlobalCfg`), replacing scattered per-field
  cherry-picking — this both centralizes config flow and unblocked every wiring.
- Added `github.com/bmatcuk/doublestar/v4` v4.10.0 (verified: 2026-01-25, zero
  transitive deps, MIT, vulncheck clean).

### M15 — centralization (your "centralize all hardcoded values" directive)
Behavior-preserving extraction of scattered literals into single-source consts:
- **Protocol identifiers** (top silent-bug risk): `@perch_session` /
  `@perch_pane_status` / `\x1f` were raw literals across 4 packages → now
  `tmux.OptionPerchSession` / `OptionPerchPaneStatus` / `FieldDelim`.
- **Status strings** (`working`/`waiting`/`done`) now use `status.State*`
  everywhere (incl. the claude hook commands); binary names via `model.Tool*`.
- **File/dir names** (`state.json`, `windows`, `trust.json`, `config.toml`,
  `.perch.toml`, `perch-status.ts`, `settings.json`, `__worktrees`, `perch/`
  branch prefix) → named consts in their owning packages.
- **Numeric/layout/color tunables** (frecency factors, pane %, char limit, status
  colors, session-id cap, cleanup delay, scanner buf, row format, max depth) →
  named consts. (Interpretation: centralize into single-source consts; did NOT
  expose new user-config knobs — that would be unrequested feature expansion.)

### M13 — documentation & diagrams
- **5 Mermaid diagrams** (`docs/diagrams/`): architecture, frame-swap, status
  sequence, worktree lifecycle, discovery/state. (Corrected a false
  blacklist-filters-discovery claim found mid-review.)
- **README** fully refreshed for the frame model, command bar, attach, resurrect,
  trust, and the now-complete config; stale claims removed.
- **ARCHITECTURE.md** (new), **CONTRIBUTING.md** (new), **CHANGELOG.md** (new,
  `0.1.0 - Unreleased`), and the one missing godoc package comment (`cmd/perch`).
- An independent accuracy review cross-checked every command/key/verb/config
  field/security claim against code: **zero false claims.**

## 🔬 STILL NEEDS YOU (unchanged from overnight)
A **human smoke-run** at a real terminal — the tty-only attach handshake + agent
reflow on swap can't be verified headlessly. Then **you cut `v0.1.0`** (never
tagged by me).

```
go build -o bin/perch ./cmd/perch
./bin/perch            # tmux frame: sidebar (left) + live main pane (right)
#  ↵ on a session      # agent appears LIVE in the main pane; sidebar stays
#  prefix → / prefix ← # focus into the agent / back to the sidebar
#  :doctor  :attach x  # try the command bar
#  q                   # perch closes; `tmux ls` → your agent sessions SURVIVE
```

## Notes / deferred (by design, flagged for you)
- `pre_merge` + the `perch merge` feature (§7.3) are intentionally **post-v0.1.0**
  (plan.md marks merge optional/slippable). The worktree-remove flow is the v1
  must-have and ships.
- Config values were **centralized** as named consts, not exposed as new
  user-config knobs (centralize ≠ expose). If you want any (e.g. pane width,
  sidebar width) user-configurable, that's a small follow-up.
