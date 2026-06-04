# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - Unreleased

### Added

- **Wails v2 desktop GUI (direct-pty, no tmux)** — `perch` (no arguments) opens
  a native desktop window: a Go backend embedded in a WebKit2GTK webview driving
  a Svelte 5 (runes) SPA. There is no tmux, no daemon, and no background server
  process. Closing the GUI window ends all panes; worktrees and agent
  conversation history persist on disk. A directory path argument opens the
  cockpit scoped to that root.

- **Direct one-pty-per-pane terminal bridge** — `OpenWorkspace` spawns a login
  shell (`$SHELL -l`) inside a pseudo-terminal via `creack/pty`
  (`internal/pty.Bridge`). Raw pty output is forwarded to xterm.js over Wails
  events; keystrokes flow back through `WriteToPty`; resize events through
  `ResizePty`. The agent's launch command is written into the shell so agent and
  shell share one pty. Closing the bridge kills the shell's entire process group.

- **Three-zone cockpit layout** — Sessions sidebar (left), swappable Stage
  (center, Agent / Code / Diff views; `\` to split into an independent second
  session pane), and a pinned shell drawer below. Every divider is
  drag-resizable; every region is collapsible. Layout is persisted to
  `layout.json` and restored on next launch.

- **vim-style modal keymap (NORMAL / TERMINAL / COMMAND)** — mode displayed in
  the status line. NORMAL: `j/k` navigate sessions, `1/2/3` switch Stage views,
  `\` splits, `/` filters, `⏎` opens. TERMINAL: all keystrokes pass to the pty;
  exit with `Ctrl-\ Ctrl-n` or click chrome. COMMAND: command palette / command
  line. Mouse is always primary and never gated by mode.

- **Fuzzy command palette** — `Ctrl-K` / `:` opens a fuzzy command palette
  (prefix-grouped `Agent:` / `Pane:` / `File:` / `View:`; recents first; inline
  keybindings shown).

- **9 themes + density tiers + design-token system** — shipped themes: Gruvbox
  (default), Tokyo Night, Catppuccin, Dracula, Nord, Rosé Pine, One Dark, Perch
  Cyan, Light. Dense / Comfortable / Ultra density tiers. Full design-token
  system (type scale, color, spacing, motion); WCAG AA contrast throughout.

- **Agent adapters: claude and opencode** — the `Monitor` seam
  (`internal/agent`) abstracts the structured side-channel:
  - **claude**: `ClaudeMonitor` provisions a per-workspace hook listener
    (`127.0.0.1`, ephemeral port, per-listener random Bearer token) and writes
    the hook config into `<worktree>/.claude/settings.json`; lifecycle and
    tool-call events arrive via HTTP POST. Session resume via `claude --resume
    <id>`.
  - **opencode**: `OpencodeMonitor` launches `opencode serve` and consumes its
    Server-Sent-Events stream; session resume via `opencode attach --session
    <id>`.
  - Model selection is supported when creating a workspace. Capabilities
    (`approvals`, `attention`, `tokens`) are advertised per-monitor; the UI
    degrades to exactly what each agent supports.

- **Inline tool-call approvals** — a docked `ApprovalCard` (perch chrome, never
  inside the xterm grid) presents **Allow / Deny / Always** for each `PreToolUse`
  event. Approval batching: "Approve all / Deny all" for a queued set. Always
  rules are persisted in `settings.json`, listed and revocable in Settings (a
  security caveat is surfaced in the UI).

- **Notification hub and OS desktop notifications** — three tiers: blocking
  (approval needed / crash) → persistent card + OS notification; ambient (turn
  done) → toast; routine (idle) → hub only. DND mode mutes ambient and routine
  tiers. OS desktop notifications fire for blocking events when the perch window
  is unfocused.

- **CodeMirror 6 editor** — editable save, find/replace, syntax highlighting,
  bracket matching, git gutter (addition and deletion markers), and a
  selection → send-to-agent affordance.

- **Diff view with hunk-granularity staging** — `DiffStat` and `Hunks` surface
  per-file unified diffs; individual hunks can be staged (`StageHunk`) or
  discarded (`DiscardHunk`). Hunks can also be dragged onto an agent pane as
  context.

- **File tree and Preview** — `ListDir` / `ReadFile` / `WriteFile` with
  gitignore-aware filtering; `RevealInFiles` / `CopyPath`. Markdown (DOMPurify-
  sanitised), Mermaid (strict mode), and image Preview.

- **In-app drag-and-drop** — file or hunk → `@mention` onto either agent pane
  (cross-agent supported); session reorder; session → split. No OS drag-out
  (Wails v2 limitation; Copy path / Reveal in Files provided instead).

- **New Session dialog with repo discovery** — scans configured roots for `.git`
  entries; first-run empty-state CTA + templates. Destructive workspace removal
  is guarded by a confirm modal with undo.

- **Bound-method IPC API with input validation** — `app.App` exposes the full
  GUI surface over the Wails IPC bridge: `ListWorkspaces`, `CreateWorkspace`,
  `OpenWorkspace`, `CloseWorkspace`, `RemoveWorkspace`, `WriteToPty`,
  `ResizePty`, `OpenShell`, `Approve`, `DiffStat`, `Hunks`, `StageHunk`,
  `DiscardHunk`, `ListDir`, `ReadFile`, `WriteFile`, `RevealInFiles`, `CopyPath`,
  `Branches`, `Worktrees`, `GetLayout`, `SaveLayout`, `GetSettings`,
  `SaveSettings`, `SetWindowFocus`, `DiscoverRepos`. Every argument crossing
  the boundary is validated: workspace / pane IDs against a `[A-Za-z0-9_-]`
  charset allowlist; worktree paths resolved and confined under configured
  `roots`. All git work goes through argv via `internal/proc`, never a shell.

- **No listening port** — all frontend/backend IPC travels over the WebKit2GTK
  script-message channel; assets are served via the `wails://` custom scheme.
  The only production local network surface is the per-workspace Claude hook
  listener (loopback-only, ephemeral port, random Bearer token). The
  `ws://localhost:34115` hot-reload socket is `//go:build dev` only and is
  absent from production binaries.

- **Workspace registry** — workspaces persisted as a JSON store at
  `~/.config/perch/workspaces.json` (XDG). Each record carries the stable
  workspace ID, worktree path, agent, branch, title, and last session ID for
  resume. Settings (`settings.json`, including `AlwaysRules`) and saved layout
  (`layout.json`) live alongside it.

- **`perch attach <query>`** — focuses the running perch window on the
  workspace that best matches the query (exact `worktreePath`, otherwise
  case-insensitive substring of path/title/branch). Uses a Wails
  `SingleInstanceLock`: the second process forwards its args to the running
  instance, which raises the window and emits `workspace:attach {query}`; the
  second process then exits. If no perch is running, `perch attach` launches
  the GUI normally (the lock is a no-op when nothing holds it). On Linux the
  forwarding process exits non-zero — expected behaviour of the lock mechanism.

- **`perch doctor`** — runtime dependency check: git, agent binaries, state dir
  availability. Reports pass/fail per check.

- **Worktree-per-session** — `CreateWorkspace` derives a linked-worktree path
  from the repository and the slugified branch (validated to stay under the
  configured roots) and runs `git worktree add`. `RemoveWorkspace` closes the
  session's panes and drops the registry record but leaves the worktree on disk,
  so a removal can be undone and the agent's history survives.

- **Global config** — `~/.config/perch/config.toml` supplies the allowed `roots`
  directories; with no config the launch directory is the sole root. Agent
  selection is handled per-workspace via the registry, not via config.

- **Linux-only build** — requires WebKit2GTK + GTK3 system libraries. Build
  with `make gui-build` (`-tags production` embeds the frontend assets).

- **Diffstat counts in sidebar + status line** — each sidebar workspace row and
  the status line show `+N −N` (insertions/deletions) summed from the existing
  `DiffStat` backend call. Counts are refreshed per-workspace on every
  `fs:changed` event so they track agent edits live.

- **Sidebar collapse** — `Ctrl-b` and a toggle rail button collapse/expand the
  sidebar. State is persisted in the layout store under the key `"sidebar"` (the
  same mechanism as the shell drawer) and restored on next launch.

- **`perch attach` single-instance focus** — see the `perch attach` entry above.
  The command is now a thin launcher: a Wails `SingleInstanceLock` forwards
  `os.Args` to the already-running perch window, which raises itself and routes
  the query to workspace selection. When no instance is running it simply opens
  the GUI.

- **opencode workspace: model field hidden** — `NewSessionDialog` hides the
  model input when `agent=opencode` and shows "Selected in the opencode TUI"
  instead, reflecting that `opencode attach` does not accept `--model`/`--agent`
  flags (model selection lives in the opencode TUI itself).

### Removed

- **`perch setup` / global status-hook subsystem** — the vestigial `perch setup`
  command, `InstallStatusHook`, `internal/status` package, and
  `resources/claude-hooks.json` have been deleted. These were TUI-era code that
  wrote to the global `~/.claude/settings.json` to install status hooks invoking
  a nonexistent `perch status set` subcommand. The live claude status path is the
  per-session worktree hooklistener: when a workspace opens, `ClaudeMonitor`
  writes the hook config (listener URL + random Bearer token) into the worktree's
  `.claude/settings.json`. No global setup step is needed or exists.

- **Dead adapter API** — the `Adapter.ListSessions` and `Adapter.ForkInto`
  interface methods (and their claude/opencode implementations, the
  `ErrForkUnsupported` sentinel, the now-orphaned transcript/session-list
  parsing helpers, the `model.Session` type, and the dead `Claude.Home` /
  `Opencode.Dir` struct fields) had zero production callers — vestigial
  session-enumeration/fork surface from the TUI design. Removed. The cockpit
  uses `NewArgs` and `ResumeArgs` only.

### Changed

- **Go magic-number elimination** — every former magic number and hardcoded
  default in the Go codebase is now a named package-level constant: pty
  cols/rows, read-buffer sizes, file-permission modes, debounce and poll
  intervals, hook-listener token size, frecency multipliers, dbus addresses,
  and Wails event-name prefixes. Raw `"claude"`/`"opencode"` agent strings at
  dispatch and comparison sites are replaced with `model.ToolClaude` /
  `model.ToolOpencode`.

- **Single XDG config-dir resolver** — the `$XDG_CONFIG_HOME/perch` resolution
  that was previously duplicated in `internal/config` and `internal/registry`
  is now single-sourced in `registry.DefaultConfigDir()`.
  `config.DefaultGlobalPath()` delegates to it. The app-directory name
  `"perch"` is defined exactly once (`registry.appName`).

- **Frontend constants centralised** — all frontend tuning values (timers,
  limits, layout defaults/clamps, settings defaults, drag MIME types, the
  `@mention` protocol prefix, localStorage keys) moved to
  `frontend/src/lib/constants.ts`. Wails event names are named `EVT_*`
  constants in `wails.ts`.

- **CSS token additions** — `tokens.css` gained `--perch-shadow-float` (was
  inlined in 7 components), `--perch-scrim` (5 components), the full
  `--perch-z-*` stacking scale, `--perch-fs-shell` / `--perch-lh-shell`
  (previously hardcoded in `Terminal.svelte`).

### Fixed

- **z-index 300/300 collision resolved** — the command palette and undo toast
  both used z-index 300. The named `--perch-z-*` stacking scale in
  `tokens.css` sets `--perch-z-undo-toast: 300` and
  `--perch-z-command-palette: 310` so the palette is never occluded by a
  transient toast.

- **SettingsPanel wrong hex fallbacks removed** — dead/incorrect hardcoded hex
  colour values in `SettingsPanel` that were not reachable through the token
  system were removed; all colour references now go through CSS tokens.

- **WCAG AA contrast for secondary text** — `--perch-text-dim` was below the
  4.5:1 ratio when rendered on `--perch-surface` panels (dialogs, notifications)
  in 7 themes. The dim value was raised (hue-preserving) in tokyo-night,
  catppuccin, dracula, nord, rose-pine, one-dark, and perch-cyan; all 9 themes
  now pass AA ≥4.60:1 on both `--perch-bg` and `--perch-surface`.

- **Modal keyboard accessibility** — Settings, Help, Confirm, and New-session
  dialogs now receive focus on open (via the shared `focusOnMount` action in
  `frontend/src/lib/actions.ts`) so Escape (and Enter, where applicable) work
  immediately instead of only after the user tabs into the dialog. Confirm and
  New-session gained explicit Escape handlers.

- **Drag-to-split persistence** — dropping a session onto the stage to open a
  split now persists via `layout.setSplit(true)` instead of a non-saving
  `layout.split = true` assignment, so the split survives a restart.
