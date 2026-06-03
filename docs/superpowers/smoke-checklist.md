# perch Cockpit — Manual Smoke Checklist

Run before any release tag. All items must pass. This checklist covers the
cross-process / GUI behaviors that headless tests cannot reach (real WebKit,
real pty, real desktop notifications, a real agent).

> NOTE: On a headless/VM display you may need `WEBKIT_DISABLE_COMPOSITING_MODE=1 bin/perch`.

## Environment
- [ ] Linux with WebKit2GTK + GTK3 installed
- [ ] `claude` or `opencode` installed and authenticated
- [ ] `bash scripts/verify-build.sh` exited 0 (valid ELF at `bin/perch`)

## Launch
- [ ] `bin/perch` opens a GUI window; no crash in the terminal
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

## Tool-call approval (hook listener)
- [ ] Ask the agent to write a file
- [ ] ApprovalCard appears in the chrome (not inside the terminal grid)
- [ ] Card shows the tool name and an input summary
- [ ] Click Allow → the agent continues; the file is written on disk
- [ ] Sidebar status returns to idle/done
- [ ] (Keyboard) Command palette / approval is reachable and operable by keyboard

## Diff view
- [ ] Switch to Diff view (View ▸ Diff or the keybinding)
- [ ] The changed-file list shows the written file
- [ ] Click a file → hunk view renders `+` lines
- [ ] Stage stages the hunk; Discard reverts it

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
