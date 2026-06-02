# perch GUI Pivot — Design Spec

**Date:** 2026-06-02
**Status:** Approved (design); pending implementation plan
**Supersedes:** the Bubble Tea terminal TUI as the interactive front-end (M0–M17)

---

## 1. Summary

perch pivots from a keyboard-driven terminal TUI (Bubble Tea) to a **Wails v2 desktop GUI** for Linux, while **keeping tmux as the pty/persistence backend** and reusing the existing UI-agnostic Go packages unchanged. The GUI is not a thin reskin: it is a complete, feature-complete product from first release — agent lifecycle, live terminals, git diffs, and worktree management all driven from the GUI.

The terminal TUI (`internal/tui`) and the persistent-frame bootstrap (`internal/frame`) are **deleted**. The CLI subcommands that agents and shell hooks depend on (`perch status set`, `perch doctor`, `perch attach`) **survive** — the `perch` binary remains multi-command, with the default (no-subcommand) invocation launching the GUI.

### Goals
- Feature-complete, bug-free, secure, fast, lightweight GUI on day one — no staged "MVP then parity."
- Mouse **and** keyboard first-class: clicky, snappy, idiot-proof, but exposing advanced usage.
- No new network attack surface: no listening TCP port in production.
- Reuse the proven backend; delete only the UI layer.
- Proper TDD throughout.

### Non-goals
- macOS / Windows support in this iteration (backend kept OS-agnostic so macOS is a later add, not a rewrite).
- A terminal TUI fallback (deleted; not maintained).
- tmux control-mode (`tmux -CC`) protocol client (explicitly deferred; see §5).

---

## 2. Platform & Tech Stack

**Platform:** Linux only. Single OS webview engine (WebKit2GTK, OS-patched), smallest security surface, single CI target.

**Backend (Go):**
| Component | Pin | Notes |
|---|---|---|
| Go | 1.25.0 (toolchain 1.26.2) | per `go.mod` |
| `github.com/wailsapp/wails/v2` | v2.12.0 (2026-03-26) | webview app framework |
| `github.com/creack/pty` | v1.1.24 (2024-10-31) | attach-pty bridge |

**Frontend:**
| Component | Pin | Released |
|---|---|---|
| `svelte` | 5.55.5 | 2026-04-23 |
| `vite` | 8.0.10 | 2026-04-23 |
| `@sveltejs/vite-plugin-svelte` | 7.0.0 | 2026-02-23 |
| `typescript` | 6.0.3 | 2026-04-16 |
| `@xterm/xterm` | 6.0.0 | 2025-12-22 |
| `@xterm/addon-fit` | 0.11.0 | 2025-12-22 |

All pins verified against their registries and are **≥30 days old** as of 2026-06-02. Compatibility verified: vite-plugin-svelte 7.0.0 `peerDependencies` = `vite: "^8.0.0"`, `svelte: "^5.46.4"` — both satisfied. Vite 8.0.10 engine `node: "^20.19.0 || >=22.12.0"` — satisfied by Node v22.15.1.

**Not used:** `@xterm/addon-attach` (a WebSocket addon). Terminal bytes are piped over Wails IPC, not a websocket — xterm.js is fed manually via `write()`.

---

## 3. Architecture

### 3.1 Process & persistence model
- **One Wails process.** The Go backend exposes a small set of *bound methods* to the Svelte frontend. IPC travels over the WebKit2GTK `script-message` channel — **no listening TCP port** in production builds (the dev-only `ws://localhost:34115` reload socket is `//go:build dev` and absent from release builds).
- **tmux on perch's own socket** (`tmux -L <perch-socket>`), never the user's default server.
- **Each agent = one independent, detached tmux session, one pane**, running claude/opencode inside a git worktree. Sessions **persist across GUI restarts** — closing the app does not kill agents.

### 3.2 Rendering bridge — attach-pty + CLI (chosen)
Two concerns, two mechanisms:

1. **Live terminal view (new code):** opening an agent tab spawns `tmux -L <socket> attach -t <session>` inside a creack/pty. The backend reads the pty in **chunked buffers** and emits **batched** byte chunks to the frontend via Wails events; xterm.js renders them with `write()`. Keystrokes from xterm.js flow through a bound method to the pty's stdin. Terminal resize: addon-fit reports cols/rows → bound method → pty winsize (`pty.Setsize`) → tmux client resize. Closing a tab kills **only the attach pty**; the agent session lives. Reopening runs a fresh `attach`, which replays the current screen.

2. **Orchestration (reused code):** session list, create, kill, resume, status — all via the existing `internal/tmux` CLI layer. No control-mode protocol is parsed.

**Why not control mode (`tmux -CC`):** each perch agent is a *single pane*, so control mode's headline benefit (native multi-pane layout) is unused, while its cost (an underdocumented protocol that even Ghostty/WezTerm implement only partially) directly conflicts with the "simple, solid, bug-free" bar. The GUI shell owns all tabs/tiling/chrome; tmux owns only the cell grid inside each tab. Control mode remains a possible future upgrade if in-agent multi-pane is ever wanted.

### 3.3 State sync
The backend polls `list-sessions` / `list-panes` on a short interval (~1s) **and** refreshes immediately after any user action (optimistic UI), emitting a `sessions-changed` event the sidebar subscribes to. Per-session status (running / idle / exited) is derived from the pane-dead flag plus activity. Simple and robust; no control-mode dependency. (tmux hooks are a possible later optimization, not required.)

---

## 4. Code Reuse / Deletion Map

Blast radius confirmed by import analysis: Bubble Tea is confined to `internal/tui`; only `cmd/perch/main.go` (and `main_test.go`) import `internal/tui` / `internal/frame`.

### Keep & reuse (UI-agnostic backend — unchanged)
| Package | Role |
|---|---|
| `internal/model` | Plain domain structs (Project, Tree, Session, Window, Tool). Already UI-agnostic — reused verbatim. |
| `internal/tmux` | tmux subprocess wrappers (launch, connect, cleanup, names). |
| `internal/attach` | Discovery + fuzzy-match of live sessions → tmux target (no pty). Drives the CLI `attach` subcommand and informs GUI session resolution. |
| `internal/agent` | Adapter interface + claude/opencode adapters. |
| `internal/config` | Two-layer TOML (global + per-project `.perch.toml`); security globals are not overridable by `.perch.toml`. |
| `internal/worktree` | Worktree seeding + hook execution. |
| `internal/git` | git subprocess wrappers (for diffs + worktree state). |
| `internal/discover` | Find git repos under a scan root. |
| `internal/state` | JSON state stores + frecency ranking. |
| `internal/resurrect` | Boot-id reconcile: detect tmux server restart, recreate dead sessions. Surfaced in GUI as recovery. |
| `internal/status` | `perch status set` CLI + status state machine (called by agent plugin hooks). |
| `internal/trust` | Records user-approved `.perch.toml` hook files (content-hash gated). |
| `internal/proc` | `Runner` interface + ExecRunner/FakeRunner (testability). |
| `internal/match` | `**`-glob matching for config rules. |
| `internal/doctor` | `perch doctor` health check. |

### Delete
| Package | Reason |
|---|---|
| `internal/tui` | Bubble Tea TUI — replaced by the GUI. Removes all charmbracelet coupling. |
| `internal/frame` | Persistent two-pane tmux frame (sidebar + swap-pane main slot) + placeholder + M17 death-recovery. The GUI is the shell; the frame model is obsolete. |

### New
| Package | Role |
|---|---|
| `app/` (Wails app) | Wails `App` struct, lifecycle, and the bound-method API surface. |
| `internal/pty` | attach-pty bridge: spawn `tmux attach` in a pty, chunked read → events, keystroke + resize handling. |
| `frontend/` | Svelte 5 + Vite SPA: sidebar, tabs, terminals (xterm.js), dialogs, diff view. |

### Rewire
- `cmd/perch/main.go`: drop the `tui` and `frame` imports and the `--sidebar` frame path; default invocation launches the Wails app; retain the `status` / `doctor` / `attach` subcommands. Update `main_test.go` accordingly.

---

## 5. Security (non-negotiable)

- **No production listening port.** IPC is WebKit2GTK `script-message`; assets are served via the `wails://` custom URI scheme; the websocket reload server is dev-build only. (Verified at Wails v2.12.0 source.) The web-server/`-host` route is rejected: a listening port is exposed to DNS-rebinding and Private-Network-Access gaps, and since agents run real shells, any IPC bypass is RCE.
- **The frontend is untrusted for orchestration.** Bound methods accept only **structured, validated** arguments:
  - session identifiers checked against the known-perch-session allowlist (no arbitrary `-t` targets);
  - worktree paths validated to live under the configured project root;
  - **no raw command strings are passed to a shell** — tmux/git are invoked via argv through `internal/proc`, never via a shell interpreter (existing behaviour, preserved).
- **Keystroke → pty input is intentionally arbitrary** — it is a terminal into the user's own agent/shell, conferring no privilege beyond what the user already holds. This boundary is documented, not a vulnerability.
- **`.perch.toml` cannot override security-relevant global config** (existing rule, preserved). Hook execution remains gated by `internal/trust` (content-hash approval).
- **WebKit2GTK is dynamically linked** → engine CVEs are patched via the OS package manager, not bundled. Documented as an operational dependency.
- **Frontend hardening:** a restrictive Content-Security-Policy; no remote URL loading (assets only via `wails://`); no `eval`.
- **Single-user, local-only.** The IPC boundary is the trust boundary; the bound API is kept minimal and fully validated.

---

## 6. UI / UX

### 6.1 Layout
Single window: a live sidebar tree on the left, a tabbed terminal area on the right, a togglable git-diff panel, and a top action/command bar.

```
┌──────────────────────────────────────────────────────────────┐
│  perch            [⌘K palette]            [+ New agent]  [⚙]   │
├──────────────────┬───────────────────────────────────────────┤
│ ▾ perch          │  ◍ feat/x   ◌ fix/y   ◌ spike/z      [+]    │ ← tabs
│   ◍ feat/x  ●run │ ┌───────────────────────────────────────┐  │
│   ◌ fix/y   ○idle│ │                                       │  │
│ ▾ other-repo     │ │   xterm.js  (tmux attach → agent)     │  │
│   ◌ spike/z ✗exit│ │                                       │  │
│                  │ └───────────────────────────────────────┘  │
│ [j/k nav]        │  [ diff ▸ ]  branch feat/x  +42 −7          │
└──────────────────┴───────────────────────────────────────────┘
```

### 6.2 Interaction principles
- **Keyboard-first:** `⌘K` command palette; arrows / `j k` to navigate the sidebar; `Enter` to open/focus an agent; `Ctrl-Tab` / number keys to switch tabs; every action has a shortcut **and** is clickable.
- **Snappy:** Svelte's compiled reactivity (no virtual DOM), xterm.js canvas rendering, batched pty writes, no layout thrash.
- **Idiot-proof but advanced:** safe defaults; destructive actions (kill agent, delete worktree) require confirmation; advanced affordances (new agent on an arbitrary branch, raw terminal access) are one keystroke away.
- **Visual git diff** of the selected worktree, rendered in the diff panel.

---

## 7. Testing (TDD)

- **Go (hermetic):**
  - tmux exercised against a **private `tmux -L <socket>` server created and killed per test** — never the user's default server.
  - `t.Setenv("HOME", t.TempDir())` for any HOME-touching path.
  - **Never run real `claude`/`opencode`** — sessions launched with a fake agent command (e.g. a small script that sleeps/echoes) so no real `$HOME`/auth is touched.
  - Cover: bound-method API (validation + allowlist logic), the `internal/pty` bridge (attach to a test session, assert bytes flow in and keystrokes/resize reach the pty), and orchestration wiring.
- **Frontend:** Vitest component tests for Svelte components; Wails bindings mocked.
- **Integration:** boot the Wails `App` struct headless (no webview) and exercise bound methods against a private tmux server end-to-end.
- **E2E through the real webview:** **out of scope** — WebKit2GTK E2E on Linux is heavy and flaky; coverage is provided by integration + component tests instead. (Explicitly stated, not silently dropped.)
- **Performance:** a pty-throughput benchmark — flood agent output and assert bounded memory and acceptable write latency — because "snappy" is a hard requirement, not an aspiration.

---

## 8. Open items resolved at plan time
- The svelte / vite / vite-plugin-svelte triple is resolved (§2); re-verify pins still satisfy the ≥30-day rule at implementation start if time has passed.
- Exact bound-method API surface (method names + signatures) is enumerated in the implementation plan, derived from §3 and §5.
- Final inventory of retained CLI subcommands (`status`, `doctor`, `attach`) and their flag surface is confirmed during the `cmd/perch` rewire task.

---

## 9. Architecture decision record (why these choices)

| Decision | Choice | Rationale |
|---|---|---|
| Front-end | Wails v2 GUI | Real windows/mouse/tabs/visual diffs; Go backend reuse; no bundled Chromium (vs Electron size); no listening port (vs web server security). |
| Rendering bridge | attach-pty + CLI | Simple, solid, full ANSI fidelity, reuses backend; single-pane agents make control-mode's benefit moot and its cost unjustified. |
| UI framework | Svelte 5 | Compiles to near-vanilla, tiny runtime, smallest reactive dep tree (security), snappy; correctness/maintainability over hand-rolled DOM. |
| Platform | Linux only | One webview engine, smallest surface; backend kept OS-agnostic for a later macOS add. |
| Terminal TUI | Deleted | One UI to perfect; backward-compat is a non-issue; backend packages survive. |
