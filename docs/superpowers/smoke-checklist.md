# perch Cockpit — Manual Smoke Checklist

Run before any release tag. All items must pass. This checklist covers the
cross-process / GUI behaviors that headless tests cannot reach (real WebKit,
real pty, real desktop notifications, a real agent).

> NOTE: On a headless/VM display you may need `WEBKIT_DISABLE_COMPOSITING_MODE=1 bin/perch`.

> ALSO RUN (not in this checklist, but required pre-release): the Playwright e2e
> suite — `make test-e2e` — which runs containerized in the `perch-dev` image
> (61 passing on the noble base; the host distro is too new to run Playwright
> 1.60.0 natively). It runs headless against `npm run preview` (no WebKit/agent
> needed) and includes the CSS-token bundle guard (`theme-tokens.spec.ts`,
> `styled.spec.ts`) that catches token/stylesheet regressions in the real Vite
> bundle. The Go headless full-loop test also requires its build tag:
> `go test -tags integration ./app/`.

## Environment
- [ ] Linux with WebKit2GTK + GTK3 installed
- [ ] `claude` or `opencode` installed and authenticated
- [ ] `bash scripts/verify-build.sh` exited 0 (valid ELF at `bin/perch`)

## Launch
- [ ] `bin/perch` opens a GUI window; no crash in the terminal (this exercises
      the webkit2gtk-4.1 link — the production binary links 4.1 via the
      `webkit2_41` build tag; 4.0 is EOL)
- [ ] Sidebar renders; NORMAL mode visible in the status line

## Workspace creation (claude)
- [ ] Command palette (`Ctrl-K` or `:`) → New Session → dialog appears
- [ ] Pick a git repo, branch, agent `claude`
- [ ] Create → workspace appears in the sidebar with a running status

## Terminal pane — CRITICAL (the one path no automated test covers)
- [ ] Click the workspace → a Terminal pane with a live shell appears
- [ ] **Agent output actually appears in the pane** — type a command / start the agent and confirm bytes render. (This is the cross-process pty→WebKit→xterm wire — bug-1. Headless tests lock the event-name agreement (`pty:data:pane-<id>`), but only this step proves the real round-trip.)
- [ ] `i` → TERMINAL mode; keystrokes pass through to the agent
- [ ] `Ctrl-\ Ctrl-n` → back to NORMAL mode
- [ ] Clicking outside the terminal returns to NORMAL (if click-out is wired)

## Shell drawer — CRITICAL (separate pty, bug-1b)
- [ ] The pinned shell drawer at the bottom shows a **live shell**; type a
      command and confirm it runs and output renders. This is a SEPARATE pty
      from the agent pane, keyed `shell-<id>`. (A prior bug keyed it
      `<id>:shell`; the `:` was rejected by `validateSessionID`, so the drawer
      silently never connected to a pty — only this step proves the real shell
      pty round-trip. The new `OpenShell` bad-pane-id negative test guards the
      charset, but not the live wire.)

## Tool-call approval (hook listener)
- [ ] Ask the agent to write a file
- [ ] ApprovalCard appears in the chrome (not inside the terminal grid)
- [ ] Card shows the tool name and an input summary
- [ ] Click Allow → the agent continues; the file is written on disk
- [ ] Sidebar status returns to idle/done
- [ ] (Keyboard) Command palette / approval is reachable and operable by keyboard

## Agent question (AskUserQuestion / question.asked) — CRITICAL (signal, not card)
A question is an attention SIGNAL, not an approval: the user answers it inside the
agent's own pane TUI, and perch must NOT pop an approval card or block the agent.
- [ ] (claude) Ask the agent something that makes it use `AskUserQuestion` (e.g. a
      prompt that triggers a multiple-choice). Confirm: the sidebar row shows the
      QUESTION feel — a `?` / "asking you", cyan (`--perch-info`), slow pulse — that
      is VISUALLY DISTINCT from the approval feel (`⚠` "needs you", amber, fast
      pulse); the question is answered IN the agent's pane TUI; perch does NOT pop an
      approval card for it and does NOT block the agent waiting on one.
- [ ] (opencode) Same, via opencode's `question.asked`: the row shows the cyan
      slow-pulse "asking you" feel, the question is answered in the attach TUI, and
      perch surfaces no approval card and never replies on the question endpoint.
- [ ] After answering, the feel clears: replying resumes **running**; rejecting/
      cancelling falls back to a steady **idle** (no spurious "Turn complete" toast).

> NOTE: headless/mock tests lock the CONTRACT — the claude PreToolUse `tool_name`
> branch + internal auto-allow (the agent is never blocked), and the opencode
> `question.asked`/`replied`/`rejected` translation — but they CANNOT prove a real
> `claude` actually emits an `AskUserQuestion` PreToolUse, nor that a real
> `opencode` emits `question.asked`. Only this manual step proves the real
> round-trip. (Same honest gate as bug-1b's terminal pane and bug-2's resume.)

## Workspace + turn loop (opencode) — only mock-covered, verify on a real binary
The entire opencode side-channel is validated by httptest mocks against a
source-read of the v1.15.12 contract; there is no automated real-binary test.
Verify each of these against a real `opencode`:
- [ ] New Session with agent `opencode` → the pane runs `opencode serve` then `opencode attach`; the TUI appears and accepts input (the serve+attach launch incantation actually submits and connects)
- [ ] While the agent works, the sidebar shows **running**; when the turn ends it shows **done** (busy→idle ⇒ StateDone) — and an ambient "Turn complete" notification fires
- [ ] Opening the workspace does NOT fire a spurious "Turn complete" toast before any turn runs (idle-at-connect stays steady, not done)
- [ ] A tool call surfaces an ApprovalCard; Allow lets it proceed
- [ ] "Always allow" a specific tool input → the *same* input auto-approves next time, but a *different* input of the same tool still prompts (hash-based match, not prefix)
- [ ] Deny a tool call → the agent reports the rejection (the `/permission/:id/reply` POST reached the server)
- [ ] Resume — CRITICAL (sessionID capture, bug-2): run a full turn, close the
      workspace, then reopen it → it resumes the **SAME conversation**, not a
      fresh session. This proves `lastSessionID` was captured from the real
      opencode `session.status` event (which carries `sessionID` and fires by
      default) and passed back as `--session <id>`. Headless tests use a
      hand-authored SSE fixture and CANNOT prove the real binary emits the id —
      only this step does. (Note: `session.status` fires on state change, so the
      id is first captured when the opening turn begins, not at connect.)

## Diff view
- [ ] Switch to Diff view (View ▸ Diff or the keybinding)
- [ ] The changed-file list shows the written file
- [ ] Click a file → hunk view renders `+` lines
- [ ] Stage stages the hunk; Discard reverts it
- [ ] Glanceable diffstat: the sidebar row AND the status line for the active
      workspace show `+N −N` counts; they update live after a file changes
      (this is the §5.3 count, computed per-workspace off the fs:changed event)

## Layout — collapse (only the keybind/persist path is automated)
- [ ] `Ctrl-b` collapses the sidebar to zero width; the always-visible toggle
      rail (▶) re-expands it; the collapsed state survives a restart
- [ ] `` Ctrl-` `` collapses/expands the shell drawer; state persists

## Single-instance & attach (cross-process — no automated coverage)
The Wails `SingleInstanceLock` + `perch attach` path crosses two processes and a
D-Bus message; only this manual step proves the real round-trip.
- [ ] With a perch window already open, run `perch attach <repo-name-or-path>` in
      a second terminal → the EXISTING window raises/focuses and selects the
      matching workspace; NO second window opens. (On Linux the forwarding
      process exits non-zero — this is expected, a Wails behaviour.)
- [ ] A bare `perch` launched while one is running raises the existing window
      instead of opening a second cockpit

## Settings & theme
- [ ] Open Settings (menu ▸ Settings… or the `settings:open` command)
- [ ] Change theme (e.g. to `tokyo-night`) → colors update IMMEDIATELY
- [ ] Toggle Do-not-disturb
- [ ] Revoke an always-allow rule (if any exist)
- [ ] Close & reopen Settings → the theme selection persisted

## Desktop notification
- [ ] Background the window; ask the agent for a slow operation
- [ ] An OS desktop notification appears on turn completion
- [ ] With DND on, ambient/routine notifications are suppressed; blocking still surfaces

## Accessibility (keyboard)
- [ ] Command palette: ArrowUp/Down moves the selection, Enter runs it, Escape closes
- [ ] Menu bar: open a menu, arrow between items, Enter activates, Escape closes
- [ ] Pane split handles: focus and resize with arrow keys

## Cleanup
- [ ] Command palette → Remove workspace → ConfirmDialog
- [ ] Confirm → workspace gone from sidebar and from `~/.config/perch/workspaces.json`
- [ ] Close the GUI → no crash, no orphaned process (check `ps` for stray shells)
