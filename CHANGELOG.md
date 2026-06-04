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

- **`perch setup [--replace]`** — detects installed AI coding tools (claude,
  opencode) and installs agent hooks/plugins: `~/.claude/settings.json` (claude
  status hooks) and `~/.config/opencode/plugins/perch-status.ts` (opencode
  plugin). Additive and idempotent without `--replace`; with `--replace`, stale
  perch-owned blocks are overwritten while all foreign config is preserved.

- **`perch attach <query>`** — registry-backed informational lookup: fuzzy-
  matches a workspace by title or worktree path and prints the result. The GUI
  owns actual focus; this command does not attach to any background session.
  Returns exit 1 on no match, exit 2 on an ambiguous match with the candidate
  list.

- **`perch doctor`** — runtime dependency check: git, agent binaries, state dir
  availability. Reports pass/fail per check.

- **Worktree-per-session** — `CreateWorkspace` derives a linked-worktree path
  from the repository and the slugified branch (validated to stay under the
  configured roots) and runs `git worktree add`. `RemoveWorkspace` closes the
  session's panes and drops the registry record but leaves the worktree on disk,
  so a removal can be undone and the agent's history survives.

- **Global config + agent-binary security boundary** — `~/.config/perch/config.toml`
  supplies the allowed project `roots`; with no config the launch directory is the
  sole root. Agent binary paths are a global-only setting — a project `.perch.toml`
  has no `[agents]` field and cannot influence which binary is executed.

- **Linux-only build** — requires WebKit2GTK + GTK3 system libraries. Build
  with `make gui-build` (`-tags production` embeds the frontend assets).
