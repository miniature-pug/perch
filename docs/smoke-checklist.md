[perch](../README.md) / [Docs](README.md) / Smoke checklist

# Pre-release smoke checklist

Run this before any release tag. Every item must pass. It covers the
cross-process and GUI behavior that headless tests cannot reach: a real WebKit
window, a real pty, real desktop notifications, and a real agent.

On a headless or VM display you may need `WEBKIT_DISABLE_COMPOSITING_MODE=1
bin/perch`.

Run the automated gate first. `make test-all` covers the Go suites, the frontend
suites, the linters, and the Playwright end-to-end tests in the container. This
checklist is what the gate cannot see.

## Installation

`install.sh` has no automated gate, so check it by hand.

- [ ] `shellcheck -s sh install.sh` and `sh -n install.sh` both exit 0.
- [ ] A fresh `./install.sh` (optionally `--skip-agents`) runs end to end with no
      error and installs the `perch` binary. There is no setup or status step;
      the run must never invoke one.

## Environment

- [ ] Linux with WebKit2GTK 4.1 and GTK3 installed.
- [ ] `claude` or `opencode` installed and authenticated.
- [ ] `bash scripts/verify-build.sh` exits 0 (a valid binary at `bin/perch`).

## Launch

- [ ] `bin/perch` opens a window with no crash in the terminal. This exercises
      the WebKit2GTK 4.1 link.
- [ ] The sidebar renders, and the status line shows NORMAL mode.

## Creating a session (claude)

- [ ] Open the New Session dialog from the command palette (`Ctrl-K` or `:`).
- [ ] Pick a repository, a branch, and the `claude` agent.
- [ ] Create. The session appears in the sidebar and its terminal opens with a
      live shell. The pty spawns on create, with no extra click.

## The terminal pane

This is the one path no automated test covers.

- [ ] The active session's terminal shows a live shell. Clicking another session
      switches to it.
- [ ] Agent output appears in the pane. Start the agent and confirm bytes render.
      This is the cross-process pty to WebKit to xterm wire; only a real run
      proves it.
- [ ] `i` enters TERMINAL mode and keystrokes reach the agent.
- [ ] `Ctrl-\` then `Ctrl-n` returns to NORMAL mode.
- [ ] Clicking outside the terminal returns to NORMAL mode.

## The shell drawer

- [ ] The shell drawer at the bottom shows a live shell. Run a command and
      confirm output renders. This is a separate pty from the agent pane, keyed
      `shell-<id>`.

## Session environment reload

`perch reload` and its drawer button relaunch a running agent so it picks up an
environment variable it did not have at launch. This is real-credential,
real-agent territory the mock gate cannot exercise.

- [ ] Run `aws sso login` (or an equivalent file-based credential refresh) in the
      session terminal. With no reload, confirm the running agent's next call
      uses the fresh credentials.
- [ ] Export a new environment variable in the session terminal and click the
      reload button in the drawer header. Confirm the agent relaunches, the
      conversation is intact (not a fresh session), and the agent can see the
      new variable.
- [ ] Repeat by running `perch reload` by hand in the session terminal instead
      of clicking the button. Confirm the same outcome.
- [ ] After the relaunch, confirm the session terminal is left in a clean state,
      not mid-command or showing stray output from the reload.
- [ ] The reload button is absent from the home shell drawer.

## Tool-call approval

- [ ] Ask the agent to write a file.
- [ ] The approval card appears in the chrome, not inside the terminal grid.
- [ ] The card shows the tool name and an input summary.
- [ ] Allow lets the agent continue and the file is written.
- [ ] The sidebar status returns to idle or done.
- [ ] The approval is reachable and operable by keyboard.

## Agent question

A question is an attention signal, not an approval. You answer it in the agent's
own pane, and perch must not pop a card or block the agent.

- [ ] (claude) Prompt the agent so it uses `AskUserQuestion`. The sidebar row
      shows the question look (a `?`, "asking you", cyan, slow pulse), distinct
      from the approval look (`⚠`, "needs you", amber, fast pulse). You answer in
      the agent's pane. perch pops no card and does not block.
- [ ] (opencode) The same through `question.asked`: the cyan slow-pulse look, the
      answer in the attach TUI, no card, and no reply on the question endpoint.
- [ ] After you answer, the look clears. Replying resumes running; rejecting or
      cancelling falls back to a steady idle with no completion toast.
- [ ] When the question arrives on the active session while the agent view shows,
      perch focuses the pty so you can type the answer, with a brief emphasis
      ring. Confirm it does not steal focus when the question is on a background
      session, or when you are on the code or diff view.

## Session and turn loop (opencode)

The opencode side-channel is covered by mocks against the v1.15.12 contract;
there is no automated real-binary test. Verify against a real `opencode`.

- [ ] A new `opencode` session runs `opencode serve` then attaches; the TUI
      appears and accepts input.
- [ ] While the agent works, the sidebar shows running; when the turn ends it
      shows done, and an ambient completion notification fires.
- [ ] Opening a session fires no completion toast before any turn runs.
- [ ] A tool call surfaces an approval card, and Allow lets it proceed.
- [ ] "Always allow" a tool input. The same input auto-approves next time, and a
      different input to the same tool still prompts.
- [ ] Deny a tool call and the agent reports the rejection.
- [ ] Resume: run a turn, close the session, reopen it. It resumes the same
      conversation, not a fresh session. This proves the session id was captured
      and passed back.

## Diff view

- [ ] Switch to the diff view.
- [ ] The changed-file list shows the written file.
- [ ] Opening a file renders its hunks with `+` lines.
- [ ] Stage stages a hunk; Discard reverts it.
- [ ] The sidebar row and the status line show live `+N -N` counts that update
      after a file changes.
- [ ] Staging a hunk flashes its file row, and the file leaves the list once its
      last hunk is staged.
- [ ] After staging, the counts and the files-to-review pill animate down, which
      proves staging refreshed the diffstat through the diff-changed path rather
      than the filesystem-watch path.

## Layout

- [ ] `Ctrl-b` collapses the sidebar; the toggle rail re-expands it; the state
      survives a restart.
- [ ] `` Ctrl-` `` collapses and expands the shell drawer; the state persists.

## Single instance and attach

This path crosses two processes and a D-Bus message, so only a real run proves
it.

- [ ] With a window open, `perch attach <repo-name-or-path>` in a second terminal
      raises and focuses the existing window and selects the matching session. No
      second window opens. On Linux the forwarding process exits non-zero, which
      is expected.
- [ ] A bare `perch` launched while one is running raises the existing window.

## Settings and theme

- [ ] Open Settings.
- [ ] Changing the theme updates colors immediately.
- [ ] Toggle do-not-disturb.
- [ ] Toggle glass effects off. Floating chrome becomes solid; toggling on
      restores the frosted look. The setting persists across a restart.
- [ ] Revoke an always-allow rule if any exist.
- [ ] Close and reopen Settings; the theme choice persisted.

## Look and feel

The glass material, the focus ring, and the hover lift are rendered by real
WebKit and cannot be checked headlessly.

- [ ] Floating chrome (the palette, a dialog, a menu) is frosted and translucent;
      you can see the panes blurred behind it and the text stays legible. Work
      panes are opaque.
- [ ] With `WEBKIT_DISABLE_COMPOSITING_MODE=1`, glass falls back to a solid tint
      and chrome stays legible.
- [ ] Focusing a sub-window shows an accent ring on that zone; the other panes
      stay fully visible.
- [ ] Hovering a session row or a notification gives a small lift; with reduced
      motion on, the lift is suppressed.
- [ ] A session reaching done gives its status icon a one-shot settle pop; with
      reduced motion on, no pop.
- [ ] Each session has a stable color, a stripe on its row and the same color on
      its notifications, the same across restarts.

## Desktop notifications

- [ ] Background the window and trigger a blocking event (an approval or an
      agent error). An OS notification appears. OS notifications fire for
      blocking events only and only while the window is unfocused; completions
      are ambient and do not notify.
- [ ] With do-not-disturb on, ambient and routine events are silenced but still
      appear in the hub, recorded as read; blocking events still surface.

## Accessibility (keyboard)

- [ ] Command palette: arrow keys move the selection, Enter runs, Escape closes.
- [ ] Menu bar: open a menu, arrow between items, Enter activates, Escape closes.
- [ ] Pane split handles: focus and resize with arrow keys.

## Removal and stale cleanup

- [ ] Remove a clean worktree session: the confirm dialog says it removes the
      session and its worktree and keeps the branch. The tree and record are
      gone; the branch remains.
- [ ] Remove a dirty worktree session: a force-confirm appears. Cancel leaves
      everything intact; Force removes the tree and discards the changes, and the
      branch remains.
- [ ] Remove an in-repo session: the record disappears; the repository and its
      branch are untouched.
- [ ] With two sessions older than the stale threshold (set it low to trigger),
      the banner appears on relaunch with the right count, and dismissing hides
      it for the session.
- [ ] Change the stale threshold in Settings (Sessions): sessions cross the
      stale line at the new day count, and clearing the field restores the
      30-day default.
- [ ] Open the cleanup panel: clean and merged rows are checked, dirty or
      unmerged rows are unchecked with a warning, Select-all checks all, and
      Remove frees the trees and branches.
- [ ] In the cleanup panel, check a dirty or unmerged row: the safe Remove skips
      it, and a distinct warn-colored Force remove unsafe button appears, disabled
      until such a row is checked. It asks a separate confirmation that names the
      data loss, and only then discards the tree and the unmerged branch.
- [ ] The cleanup panel's Open button opens that session.

## Sidebar resume and the home shell

- [ ] A sidebar row shows the branch, the agent, and a relative last-active time.
- [ ] An empty sidebar shows the no-sessions hint.
- [ ] Clicking a row shows the resume preview. Cancel leaves the session closed;
      Open resumes it.
- [ ] The resume preview says whether Open continues the previous conversation or
      starts fresh, and shows the fork point (Forked from ...) for a worktree
      session that recorded one.
- [ ] The home screen (no active session) shows a welcome card and a live shell
      drawer rooted at the launch directory or your home directory.
- [ ] Run a command in the home shell, open a session, return home: the earlier
      output is still in the scrollback. The home shell stays mounted.
- [ ] The home shell and a session shell are independent ptys.

## Real-WebKit and real-agent smoke

These prove the finalization campaign's newest behavior, which the mock gate
cannot reach: a native drag off the desktop, solid glass over a live pty, split
keep-alive, an agent that dies on its own, and the stubbed-binary guard.

- [ ] **Native file drop.** Drag a file from your desktop file manager onto an
      agent pane. The pane receives the file's absolute path, not a bare basename,
      and dropping on a specific pane routes it there rather than to whichever pane
      was last active. The agent can open the path.
- [ ] **Solid overlays over the terminal.** Discard a hunk and read the undo
      toast, then reopen a closed session and read its resume preview. Both render
      as solid surfaces over the composited terminal, with no terminal text
      bleeding through them.
- [ ] **Split keep-alive.** Assign a session to the split pane and scroll its
      terminal back a few screens. Toggle the split off with `\` and on again. The
      scrollback is intact; the pane was not rebuilt as a blank xterm.
- [ ] **Exited agent, and the silent close.** Let an agent exit or crash on its
      own (quit it from inside, or kill its process). The session flips to `exited`
      with the "session has ended" overlay and a Reopen button, and the sidebar row
      goes dim, not red. With the window backgrounded, confirm the OS fires an
      "Agent exited" notification for that self-death. Then, separately, close a
      session through perch (Remove or Close): the teardown is silent and fires no
      exited notification.
- [ ] **Opening a live session never restarts it.** With a session running, select
      it and press `Enter`, then also open it from the Session menu. Both only focus
      the pane; the running agent is never killed or respawned, and its scrollback
      and conversation are unbroken.
- [ ] **`i` reaches the agent.** With the agent view active, press `i`. The agent
      terminal takes focus and your typing goes to the agent, not the shell drawer
      or the app.
- [ ] **Splitter drag with a live agent.** With an agent mid-task, drag the pane
      splitter. The TUI reflows cleanly, with no garbling or repaint splutter.
- [ ] **Keep-alive across views and sessions.** Scroll an agent terminal, expand a
      diff hunk, and place the editor cursor mid-file. Switch to another view and
      another session, then switch back. The terminal scrollback, the expanded hunk,
      and the editor cursor and any unsaved edits are exactly as you left them.
      Nothing was torn down and rebuilt.
- [ ] **Stubbed-binary guard.** Build without the frontend (`make build`, `make
      install`, or a bare `go install`) and run the result. It refuses to launch and
      prints `perch: this binary was built without the frontend. Run 'make gui-build'
      (or 'make gui-install') and reinstall`. No blank window opens.
- [ ] **First-run empty state.** Launch with no active session. The empty screen
      shows the welcome guidance: what a session needs (a git repo and `claude` or
      `opencode` on `PATH`), a plain-word note on worktrees, and the pointer to
      `perch doctor` and `docs/usage.md`.
- [ ] **Attention count in the window title.** With perch backgrounded and a
      session awaiting your approval or a question, the OS window title reads
      `perch (N need you)`, and returns to `perch` once every session is handled.
      The count follows approvals and questions, debounced so it does not thrash.

## Shutdown

- [ ] Remove a session through the command palette and confirm it leaves the
      sidebar and `workspaces.json`.
- [ ] Close the window with no crash and no orphaned process; check `ps` for
      stray shells.
