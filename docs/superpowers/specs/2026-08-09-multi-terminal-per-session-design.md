# Multi-terminal per session: design

Status: Design. Drives implementation on `feat/perch-v1`.

## Goal

A VSCode-like integrated-terminal experience for each session's shell drawer:
multiple shell terminals per session, arranged as **tabs**, optionally **split
side-by-side**, **click a tab to switch**, a **+** to add one, and an **×** that
closes a terminal. Closing the last one immediately spawns a fresh replacement, so
the drawer is never empty ("closes the current one and replaces it with a new one").

Scope is the per-session shell drawer (`.shell-drawer-zone`). The home shell
(`shell-home`) stays a single terminal, unchanged. It is a landing shell with no
session context, and multi-terminal there adds no value.

## Ground truth this builds on

- The Go bridge registry `bridges map[paneID]*Bridge` (`app/app.go`) is already a
  flat namespace with no per-workspace count limit. `OpenShell`, `WriteToPty`,
  `ResizePty` are all paneID-generic. N shells per session already work at that
  layer given distinct paneIDs.
- Three sites assume one shell per session by deriving the workspaceID from
  `TrimPrefix(paneID, "shell-")`: `OpenShell` (env overlay), `env.go` ReloadAgentEnv,
  and `CloseWorkspace`'s single `"shell-"+id` reap.
- `validateSessionID` hard-limits paneIDs to `[A-Za-z0-9_-]`, ≤128 chars. Colons
  are forbidden and load-bearing as delimiters elsewhere. UUID workspace ids are
  hex + hyphens, never underscores.
- Frontend keeps every terminal mounted and hidden via `display`; `visible` is what
  triggers a Terminal's re-fit on show. The flex height chain
  (`zone → panel → body → .terminal`, each `flex:1; min-height:0`) is load-bearing:
  break it and FitAddon clips the cursor.
- `Terminal.onDestroy` does NOT close the pty; closing a pty is an explicit backend
  call. So pty spawn/close must be tied to the shell LIST (add/remove), not to xterm
  mount/unmount.
- The agent split already models "two panes" as `activeId` + `splitId` + an
  `active ≠ split` invariant. The shell split reuses that exact shape.

## Paneid scheme (backward compatible)

- First/default shell of a session keeps `shell-<uuid>`, unchanged. Existing
  sessions and the three TrimPrefix sites keep working for the default shell.
- Additional shells: `shell-<uuid>_<n>` (n = 1, 2, …), `_` chosen because the UUID
  never contains `_`, so the workspaceID is recoverable by cutting on the first `_`.
- Helper `workspaceIDForShellPane(paneID) string`: `strings.TrimPrefix(paneID,
  "shell-")` then `strings.Cut(rest, "_")` → the part before `_`. Replaces the three
  ad-hoc TrimPrefix sites so any shell pane maps back to its workspace.

## Backend changes (Go)

1. `workspaceIDForShellPane(paneID)` helper; route `OpenShell` env-overlay lookup
   and `env.go` ReloadAgentEnv through it.
2. `CloseShell(paneID string) error`: a bound Wails method. It calls
   `validateSessionID`, looks up the bridge, calls `br.Close()`, and deletes the
   entry from `bridges`. It mirrors the shell half of `CloseWorkspace`. It is
   idempotent: an unknown paneID is a no-op, not an error.
3. `CloseWorkspace` reaps ALL of a session's shells: close the agent `pane-<id>`
   plus every bridge whose key `== "shell-"+id` or `HasPrefix("shell-"+id+"_")`.
   (UUID fixed format means this never matches another session's shells.)
4. `wails.ts`: bind `closeShell(paneId)`.

## Frontend changes

### State (per-run, NOT persisted)

Ptys die on app restart, so the shell LIST is in-memory only (like `openIds` /
`termEpoch`). Drawer height (`shellH`) and collapse stay persisted as today.

In `App.svelte`, per-run `$state`:
- `shellsFor: Record<wsId, ShellPane[]>` where `ShellPane = { id, title }`.
- `activeShellFor: Record<wsId, string>`: the primary/left shell.
- `shellSplitFor: Record<wsId, string | null>`: the right shell when split, else
  null (split is ON iff non-null). Invariant `activeShellFor !== shellSplitFor`.

Pure, unit-tested helpers in `lib/shellPanes.ts`:
- `nextShellId(wsId, existing): string`: `shell-<wsId>` if none, else the smallest
  unused `shell-<wsId>_<n>` (n ≥ 1).
- `defaultTitle(index): string`: for example `"shell"`, `"shell 2"`, …
- transition helpers for close-never-empty and the split invariant, so the tricky
  logic is testable without a DOM.

### Side effects (App owns pty lifecycle)

- Ensure default shell on session open (`openSession`): if `shellsFor[id]` is empty,
  mint `shell-<id>`, `openShell(id, cwd)`, set it active. Replaces the current
  spawn-on-ShellDrawer-mount.
- `addShell(wsId)`: mint id, `openShell(id, cwd)`, push, set active.
- `closeShell(wsId, id)`: `closeShell(id)` (backend), remove from list. If the list
  is now empty → immediately `addShell` (never empty). Else set active to an adjacent
  shell. If it was the split partner (or split drops below 2 shells), clear split.
- `selectShell(wsId, id)`: set active; if it equals the split partner, swap so the
  invariant holds (mirror the agent `active ≠ split` effect).
- `toggleShellSplit(wsId)`: null → pick a partner (a shell ≠ active, creating one if
  only one exists) and set it; non-null → clear.
- Workspace removal / `CloseWorkspace` frontend paths clear the three maps for the id.

### Components

- New `ShellPanel.svelte` (presentational) for the per-session drawer: a tab strip
  (`flex-shrink:0`) with a tab per shell (title, per-tab ×), a **+**, a **split**
  toggle, the **env→agent reload** button (operating on the active shell's paneId),
  and the collapse toggle; then a body (`flex:1; min-height:0`) that renders every
  shell as a kept-alive `<Terminal>` cell. Tabs mode shows only the active cell;
  split mode shows active (left) + partner (right) side by side (`flex-direction:row`,
  each cell `flex:1; min-width:0`). `visible` per cell toggles so the shown cell
  re-fits. Callbacks: `onSelectShell / onNewShell / onCloseShell / onToggleSplit /
  onToggleCollapse / onReloadEnv`.
- `ShellDrawer.svelte` stays as the single-terminal home-shell wrapper, unchanged.
- App's `.shell-drawer-zone` loop renders `<ShellPanel>` per mounted session with the
  state + callbacks; the home zone keeps `<ShellDrawer>`.

## × behavior

Per-tab ×: closes that shell (backend `closeShell` + list removal). Closing the last
remaining shell immediately spawns a fresh one, so the drawer is never empty. This
is the "closes the current one and replaces it with a new one" behavior the request
calls for. Closing a non-last tab activates an adjacent tab.

## Risks / constraints honored

- Keying stays within `[A-Za-z0-9_-]`; `_` delimiter, no colons.
- No pty leak: `CloseWorkspace` reaps all of a session's shells; `closeShell` is the
  explicit teardown (xterm unmount alone never closes a pty).
- New-shell ids are always fresh/unique so `putBridge` never SIGKILLs a live shell.
- Tab switch toggles `visible` (not just display) so the shown terminal re-fits.
- Tab strip / split gutter are `flex-shrink:0` and preserve the `flex:1; min-height:0`
  chain down to `.terminal`; split cells add `min-width:0`.
- Split reuses the proven `active` + `splitId` model; independent shell nodes mean no
  single-node portal tug-of-war (each cell mounts its own Terminal).

## Tests

- Go: `CloseShell` closes+removes a bridge and is idempotent; `CloseWorkspace` reaps
  all `shell-<id>[_n]` bridges; `workspaceIDForShellPane` for `shell-<uuid>`,
  `shell-<uuid>_2`, and `shell-home`.
- Frontend unit: `nextShellId` (first + gap reuse), close-never-empty transition,
  split invariant (`active ≠ split`, clear on drop below 2).
- Frontend integration (App.test.ts): +, tab click switches visible cell, ×-on-last
  spawns a replacement (`closeShell` then `openShell` both called), split shows two
  cells, closing to below 2 clears split.
- Manual smoke: real WebKit. Tab switch reflows the shown grid to the drawer height;
  split shows two live shells; × never leaves an empty drawer.

## Out of scope

- Persisting the shell list/layout across app restarts (ptys die anyway).
- Multi-terminal for the home shell.
- Renaming tabs / drag-reorder (could follow later; not requested).
