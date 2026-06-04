# perch — Agent Cockpit Design

**Status:** Approved design, ready for implementation planning.
**Date:** 2026-06-02
**Supersedes:** `2026-06-02-perch-gui-pivot-design.md` and its plan (the tmux-backed GUI pivot). This document replaces the tmux architecture with a direct-pty model and an agent-state side-channel. There is **no released version and no backward-compatibility requirement** — this describes a single, feature-complete product.

---

## 1. Vision

perch is a **worktree-native cockpit for agent-assisted coding**: a keyboard-first Linux desktop GUI that runs multiple AI coding agents (Claude Code, opencode) in real terminals across git worktrees, and gives you everything a terminal can't — desktop notifications when an agent needs you, a visual diff of what each one changed, inline approve/deny of tool calls, and just enough editor that you don't reach for VS Code.

**Identity in one line:** an agent cockpit that happens to let you edit, *not* an editor that happens to run agents.

**The differentiator** (confirmed by competitive survey): worktree-awareness. Cursor, Zed, Warp, Cockpit, Claudia all have multi-session sidebars and approvals; almost none are worktree-native. That is the gap perch owns.

### Principles (non-negotiable)
- **Feature-complete, perfection-first.** No phased cuts; the product ships whole, thoroughly tested, polished.
- **The agent is the intelligence; perch is the cockpit.** perch never reimplements code intelligence (LSP, refactor, debug). It orchestrates, observes, and gives a thin editor.
- **Keyboard-first, mouse-equal.** vim modal control for power; the mouse always works for everyone.
- **Honest about the platform.** Where Wails/WebKitGTK can't do something, we use a real fallback and say so.

---

## 2. Non-Goals

- **Not a VS Code clone.** No LSP client, no go-to-definition, no debugger (DAP), no problems panel from language servers, no extension API, no minimap. Code intelligence is the agent's job. (Diagnostics that *do* appear come from parsing the agent's own test/build output, not a language server.)
- **Not multi-window.** Wails v2 is single-window (verified: multi-window is a Wails v3 feature, and v3 is alpha — rejected for stability). "Tear-off to a separate OS window" is satisfied by **in-app split panes**.
- **No native drag-OUT to other apps.** Wails v2 exposes no webview drag-out (verified). Replaced by **Copy path / Reveal in Files / Export**.
- **No backward compatibility** with the previous Bubble Tea TUI — it is deleted.

---

## 3. Platform & Build (verified facts)

- **Stack:** Go backend + WebKit2GTK webview + Svelte 5 SPA, via **Wails v2 (v2.12.0)**.
- **Build requires `-tags production`** (the no-tag/`!dev !production` build compiles a stub `CreateApp` that errors). Production build: `npm --prefix frontend run build && go build -tags production -o build/bin/perch ./cmd/perch`. The `wails` CLI is **not** used (repo root is a library package `perch` embedding `frontend/dist` and `.tool-versions`; `main` lives in `cmd/perch`). This is already wired into `make gui-build`.
- **No listening TCP port for IPC** — Wails uses the WebKit `script-message` bridge and the `wails://` asset scheme in production. The IPC namespace is `window.go.app.App.<Method>`.
- **The one local network surface** is the **Claude hook callback listener** (see §6.2): a Go HTTP server bound to `127.0.0.1` on an ephemeral port, protected by a per-process bearer token, used only for `claude` hooks to post events and fetch approval decisions. Never bound to a non-loopback host.
- **Verified Wails v2 / WebKitGTK constraints:**
  - In-page HTML5 drag-and-drop: **works**.
  - OS file/folder **drop-in**: works via `OnFileDrop`, but Linux bug [#3686](https://github.com/wailsapp/wails/issues/3686) lets WebKitGTK hijack the drop and replace the UI; mitigation (`DisableWebViewDrop: true` + `preventDefault` on `dragover`/`drop`) is a **validation spike**.
  - Drag-**out**: unsupported → fallback above.
  - Multi-window: unsupported → splits.

---

## 4. Deletions

Remove entirely (no compat shims): `internal/tui`, `internal/frame`, `internal/tmux`, the tmux **attach-pty bridge**, and all charmbracelet/Bubble Tea dependencies. `cmd/perch` keeps `setup`, `doctor`, `version`, and gains the GUI default; `attach`/`resurrect`/`status` are re-evaluated against the new model (see §6.5).

---

## 5. Backend Architecture (Go)

### 5.1 Direct PTY layer (`internal/pty`)
One **direct pty per pane** (no tmux). A pane spawns the user's **interactive login shell** (`$SHELL -l`, fallback `/bin/bash`) via `creack/pty` in the worktree directory, inheriting the environment (dotfiles, prompt, aliases, `$PATH`, completions come free). The agent (`claude` / `opencode …`) is launched **inside that shell**, not as a bare exec. Bridge responsibilities:
- `Spawn(cwd, argv, winsize)` → pty master + process handle.
- Output pump: pty bytes → batched `[]int` chunks (≤16 KiB) → Wails `EventsEmit` → xterm.js. (`[]int` because Go base64-encodes `[]byte` in JSON.)
- `Write([]byte)` → pty stdin (JSON number-array unmarshals into `[]byte`).
- `Resize(cols, rows)`.
- `Close()` → kill process group + `Wait` (reap) + close fd, double-close guarded.

### 5.2 Session lifecycle & workspace registry
- **GUI close → panes die.** Panes are not persisted as processes (no daemon, no tmux). This is intentional and sufficient: worktrees persist on disk, and both agents persist their own conversation and resume it.
- **Workspace registry** (`internal/registry`): a small on-disk store (JSON under perch's config dir, e.g. `~/.config/perch/workspaces.json`) mapping each known workspace → `{ worktreePath, agent, lastSessionID, title, lastActive }`. On launch, the sidebar is populated from the registry (cold open shows known workspaces even with no live processes).
- **Open / resume:** opening a workspace relaunches `$SHELL` + agent with the agent's **resume flag** bound to `lastSessionID`; the harness restores the conversation. (Claude: `--resume <id>` / `--continue`; opencode: `--session <ses_…>` / `--continue`.)

### 5.3 Git & diff (`internal/git`)
perch computes diffs itself (no agent needed): `DiffStat` (per-file `+/−`), per-file unified hunks, and **stage / discard at hunk granularity** (apply/reverse patch against the worktree/index). Also: branch/worktree enumeration for the New Session dialog, and `+142 −37`-style status-line/sidebar counts.

### 5.4 Filesystem (`internal/fs`)
`ListDir` (gitignore-aware tree), `ReadFile`, `WriteFile` (editor save), an external-change watcher (fsnotify) so the editor/tree reflect agent edits live, plus `RevealInFiles` and `CopyPath`.

---

## 6. Agent Integration — the spine (`internal/agent`)

The state-bearing features (attention, notifications, approvals, token/cost) cannot come from the pty byte stream (that's pixels, not state). They come from an **agent-specific structured side-channel**, abstracted behind one interface. **Claude is the primary target.**

### 6.1 `AgentAdapter` interface
```
type AgentAdapter interface {
    // Launch the agent inside a shell pty in cwd; resumeID "" = fresh.
    Spawn(ctx, cwd string, resumeID string) (Session, error)
    // Structured state stream for this session.
    Events() <-chan AgentEvent      // running | idle/awaiting-input | awaiting-approval | tool-start/end | done | errored | usage{tokens,cost}
    // Resolve a pending tool-approval (drives GUI Allow/Deny/Always).
    Approve(reqID string, decision Decision) error
    Capabilities() Caps             // which features this agent supports (UI degrades per-agent)
    ResumeID(Session) string        // persisted to the registry
}
```
A registry selects the adapter by agent type. `Caps` lets the UI light up only what an agent supports.

### 6.2 `ClaudeAdapter` (hooks + transcript)
- **Spawn:** `claude` (resume via `--resume <id>` / `--continue`) inside the shell pane.
- **Hooks side-channel:** perch writes a **project-scoped hook config** (`.claude/settings.json` under the worktree, or a perch-managed settings dir) registering:
  - `PreToolUse` → calls perch's localhost listener; perch shows the Allow/Deny/Always card and returns `permissionDecision: allow|deny` (this **replaces** Claude's in-TUI prompt). Marked a **validation spike** — documented but not officially exampled.
  - `Stop` → "turn done, your turn" (drives idle/attention). `StopFailure` → errored (carries `error_type`).
  - `SessionStart` → captures `session_id` for transcript lookup + registry.
- **Listener:** Go HTTP server on `127.0.0.1`, ephemeral port, per-process bearer token injected into the hook command/`http` hook config. Localhost-only.
- **Transcript tail:** tail `~/.claude/projects/<slug>/<session-id>.jsonl` for message/tool history and (spike) token/cost. Slug-derivation and token presence are **validation spikes**.
- **Capabilities:** approvals ✓, attention ✓ (via Stop), tokens ⚠ (spike).

### 6.3 `OpencodeAdapter` (serve + SSE + REST)
- **Spawn / launch topology:** perch self-assigns a free loopback port `P` + random password `PW`, then the pane runs roughly `export OPENCODE_SERVER_PASSWORD=PW; opencode serve --port P --hostname 127.0.0.1 & <poll until listening>; exec opencode attach http://127.0.0.1:P [--session <id>]`, so the user still sees the real TUI while perch consumes the server's stream. `opencode attach` takes the URL as an explicit positional (there is **no** `$OPENCODE_URL` env var), reads the password from `OPENCODE_SERVER_PASSWORD`, and accepts `--session` for resume but **not** `--model`/`--agent` (model selection stays in the opencode TUI — a documented deviation).
- **Events:** subscribe to **SSE `GET /event`** behind **HTTP Basic auth** (`Authorization: Basic base64("opencode:"+PW)`). Wire frames are a nested envelope `data: {"id":…,"type":…,"properties":{…}}` carrying `session.next.step.started/ended/failed` (with **token+cost** in `properties.tokens`/`properties.cost`), `permission.asked`/`permission.replied`, text/shell events, heartbeat. Reconnect by re-opening the stream.
- **Approvals:** `permission.asked` (in) → GUI card → `POST /permission/:id/reply` with body `{reply: once|always|reject}` (Basic auth) (out).
- **Resume/registry:** `opencode session list --format json`; resume `--session <id>` / `--continue`.
- **Capabilities:** approvals ✓, attention ✓, tokens ✓ (native). opencode is the *easier* integration; Claude is still primary because it's the daily driver.

### 6.4 State → UI mapping
`AgentEvent`s drive: sidebar status icon (`◐ running` / `◯ idle` / `⚠ needs you` / `✓ done` / `✗ error`), the notification tiers (§8), the approval card, and the token/cost meter in the status line.

### 6.5 CLI surface
`cmd/perch`: default → launch GUI; keep `setup`, `doctor`, `version`. `attach` becomes "focus/raise an existing perch workspace" (no separate process to attach to); `resurrect`/`status` fold into the registry + GUI.

---

## 7. Frontend Architecture (Svelte 5 + xterm.js + CodeMirror 6)

### 7.1 Component tree
`App` → `MenuBar` (Session/Worktree/View/Agent/Help + notification bell), `Sidebar/Sessions`, `Stage` (view-switcher **Agent / Code / Diff** + split manager), `Terminal` (xterm.js + fit), `Editor` (CodeMirror 6), `DiffView` (hunks + stage/discard), `FileTree` (+ action menu), `ShellDrawer` (pinned, its own pty), `CommandPalette`, `NotificationHub`, `ApprovalCard` (docked chrome, **not** inside the terminal grid), `NewSessionDialog`, `ConfirmDialog`, `Preview` (markdown/mermaid/image), `ThemeProvider`. Svelte 5 runes + callback props (no `createEventDispatcher`).

### 7.2 Layout model
Three zones: **left** Sessions · **center** swappable Stage (Agent/Code/Diff, full-width each; `\` to split) + **pinned** shell drawer below · the file tree rides with the Code view. Every divider drag-resizable, every region collapsible, **layout persisted** to disk and restored (no auto-redistribute on focus).

### 7.3 Editor (deliberately thin, but real)
CodeMirror 6 (≈100 KB, vs Monaco's MBs), **editable**: open/edit/save, find/replace, syntax highlighting, bracket matching, **git gutter** (perch supplies changed-line ranges). **No LSP / completion / go-to-def** — by design. Editor↔agent: select → **@mention / Send to agent** (same plumbing as drag-drop).

### 7.4 Design tokens (from the research, locked)
- **Type:** sans (Geist; IBM Plex / Inter selectable) for chrome, mono (Geist Mono) for terminal/code/paths. Scale: code **14px/1.55**, shell **13/1.5**, body **13–14**, status/caption **12**, section labels **11/600/0.06em uppercase**, headings 20/24. Never mono mid-sentence.
- **Color/contrast:** WCAG 2.2 AA — text ≥ 4.5:1, borders/icons/focus-ring ≥ 3:1. **Status = color + icon + label**, never color alone. Off-white on off-black (no pure `#fff/#000`); lighter weights on dark (halation).
- **Spacing:** 8pt grid; default **Dense** tier (selectable Comfortable/Ultra).
- **Depth:** borders, not shadows; shadow only for floating popovers/modals.
- **Motion:** 100–150ms, `cubic-bezier(.4,0,.2,1)`, nothing > 250ms; typing/selection instant (Doherty < 400ms).
- **Icons:** Lucide/Phosphor, 16px chrome, ~1.5–2px stroke; icon-only only for universal glyphs (+ `aria-label`).

### 7.5 Themes
9 shipped, **Gruvbox default**: Gruvbox, Tokyo Night, Catppuccin, Dracula, Nord, Rosé Pine, One Dark, Perch Cyan, Light. Themes vary palette only; structure/type/spacing constant. `View ▸ Theme`; persisted.

### 7.6 Vim modal model + mouse
Modes shown in status line: **NORMAL** (keys drive perch: `j/k` sessions, `gt/gT` tabs, `1/2/3` view, `gd` diff, `ge` editor, `` ^` `` shell, `\` split, `/` filter, `:` command, `⏎` open, `i` enter terminal), **TERMINAL** (keys pass to the agent/shell; leave via `Ctrl-\ Ctrl-n` *or click out*), **COMMAND** (`:`/palette open). Mouse is always primary and never gated by mode. Full keymap table is an appendix in the plan.

### 7.7 Other surfaces
- **Command palette** (`⌘/Ctrl-K`): fuzzy, prefix-grouped (`Agent:`/`Pane:`/`File:`/`View:`), recents first, inline keybindings.
- **Drag-drop:** in-app set (file/diff/selection → @mention; cross-agent; hunk → fix; session → split; reorder) + OS file/folder **drop-in** (spike per #3686); drag-out → Copy path/Reveal.
- **New Session dialog:** pick worktree/branch + agent + model → direct-pty spawn.
- **First-run/empty:** one **New Session** CTA + a few templates; no wizard.
- **Preview:** markdown, mermaid, images.

---

## 8. Notifications & Approvals

- **Three tiers:** blocking (approval needed / crash) → persistent inline card + OS notification; ambient (turn done) → toast 5–7s; routine (idle/external change) → hub only.
- **Notification hub** (bell + badge): filterable (approvals/errors/done), mark-read, **DND** (mutes tiers 2–3).
- **OS notifications** when the window is unfocused/backgrounded (Linux desktop notification via the appropriate Go/dbus path; validated on the target).
- **Approval card:** docked perch chrome adjacent to the agent pane (never interleaved in the xterm grid). **Allow / Deny / Always**; **batching** (queue with "Approve all / Deny all"); **Always** rules persisted and manageable in settings, with a security caveat surfaced.
- **Confirmations:** destructive (kill session, remove worktree) → modal **+ undo**; routine actions never confirmed.

---

## 9. Security

- Wails production build opens no IPC port; the only local surface is the **localhost-bound, token-authed** Claude hook listener.
- `validateSessionID` (byte charset `[A-Za-z0-9_-]`, 1–128), `validateWorktreeUnderRoots` (abs+clean+EvalSymlinks, trailing-sep prefix), `containedUnderRoots` for derived paths — all retained.
- Agent session targets come from matched registry entries; raw user-supplied ids never reach argv.
- Hook config is written scoped to the workspace and removed on teardown; `Always`-allow rules are explicit, listed, revocable.
- Tests never run a real `claude`/`opencode`, never write the real `$HOME` (use `t.Setenv("HOME", t.TempDir())` + a fake agent), and never touch a shared server.

---

## 10. Testing Strategy

- **Go:** unit + `-race`; integration with a **fake agent** binary (scripted to emit hook calls / SSE events / pty output) and temp `$HOME`; **adapter contract tests** against recorded Claude hook payloads and recorded opencode SSE fixtures; git/diff tests on real temp repos; registry round-trip.
- **Frontend:** Vitest + `@testing-library/svelte` (component behavior, keymap/mode, palette, approval batching, layout persistence); xterm/CodeMirror mount/unmount leak guards.
- **End-to-end smoke:** production build launches, fake agent drives a full loop (spawn → tool approval via GUI → diff → resume).
- **Validation spikes (gated, early in the plan — de-risk, don't defer):**
  1. Claude `PreToolUse` GUI approval interception end-to-end.
  2. Claude transcript token/cost availability + project-slug derivation.
  3. opencode `serve` + SSE consumption + REST approval reply.
  4. Wails `OnFileDrop` Linux #3686 workaround.
  5. Linux OS desktop notification path.
- **Quality gates:** `go vet`, `golangci-lint`, `govulncheck`, `tsc`, all green; production GUI ELF builds.

---

## 11. Package / File Layout

```
cmd/perch/            # entry: GUI default + setup/doctor/version
app/                  # Wails App: bound methods, options (no-port Run), startup/shutdown
internal/pty/         # direct-pty bridge
internal/agent/       # AgentAdapter + claude/ + opencode/ + fake/ (tests)
internal/registry/    # workspace registry
internal/git/         # diff/stat/hunk stage-discard, worktree enum
internal/fs/          # listdir/read/write/watch/reveal
internal/notify/      # OS notifications
internal/hooklistener/# localhost token-authed Claude hook server
internal/doctor/      # (existing) imports perch.ToolVersions
frontend/src/lib/     # Svelte components (see §7.1)
frontend/src/tokens/  # design tokens + themes
```

---

## 12. Open Risks (tracked, not deferred)

All five §10 spikes are documented-but-unverified Claude/Wails behaviors. Each is a **gated early task**: if a spike fails, we adapt the mechanism (e.g., Claude token/cost via an alternate source) — but the *feature* stays in scope. opencode integration carries low risk (fully documented server/SSE). The single accepted limitation is **no separate OS windows** (Wails v2), met by splits.
