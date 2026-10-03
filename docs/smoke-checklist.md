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
      error and installs the `perch` binary. There is no setup or status step.
      The run must never invoke one. The script builds the frontend (`npm ci`
      and `npm run build`) before `go build`, so Node.js and npm must be present.
- [ ] The installed `perch` launches a window. It must not exit with "this binary
      was built without the frontend". `perch version` alone does not catch that.

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

- [ ] The active session's terminal shows a live shell. Click another session
      to switch to it.
- [ ] Agent output appears in the pane. Start the agent and confirm bytes render.
      This is the wire from the pty, through WebKit, to xterm, across
      processes. Only a real run proves it works.
- [ ] `i` enters TERMINAL mode and keystrokes reach the agent.
- [ ] `Ctrl-\` then `Ctrl-n` returns to NORMAL mode.
- [ ] Clicking outside the terminal returns to NORMAL mode.
- [ ] Select text in the terminal, then copy with ctrl-shift-c or right-click
      Copy, and paste it elsewhere. Paste into the terminal with ctrl-shift-v or
      right-click Paste. Bare ctrl-c still sends SIGINT to the agent. It does
      not copy.
- [ ] ctrl-shift-c also copies a selection in the diff view, a dialog, and the
      editor, through the host clipboard (WebKit's own is unreliable).
- [ ] Use the terminal heavily, switch between the agent, code, and diff views,
      and resize the window and the shell drawer. The cursor stays in view.
      Typing `clear` fully clears the screen to an empty prompt, with no grey
      band and no lost cursor.
- [ ] In the bottom shell drawer specifically, run many commands (or `ls -al` in
      a large directory) so output overflows the drawer. The drawer scrolls
      naturally to the newest prompt at the very bottom. The cursor is never
      clipped below the fold. If you drag the drawer taller or shorter, the
      grid reflows to match.
- [ ] After a session ends, click Reopen: the "session has ended" overlay clears
      and stays gone, even if a late or stale exit arrives right after the reopen.

## The shell drawer

- [ ] The shell drawer at the bottom shows a live shell. Run a command and
      confirm output renders. This is a separate pty from the agent pane, keyed
      `shell-<id>` for the first terminal.

## Multiple terminals per session

The per-session shell drawer is a multi-terminal panel: a tab strip over N shell
terminals. This is real-pty territory. The mock gate cannot fully exercise it.

- [ ] The drawer opens with one shell tab. `+` adds another (a fresh
      `shell-<id>_<n>` pty). Click a tab to switch to it. Each tab keeps its
      own scrollback and running processes across switches. The shown grid
      reflows to the drawer height (the cursor is never clipped).
- [ ] The split button (⊟ Split) shows two terminals side by side. Each grid
      reflows to its half width. If you toggle split off, the drawer returns
      to a single VISIBLE terminal. The second tab remains in the strip. It
      is just no longer shown alongside.
- [ ] `×` on a tab closes that terminal. If you close the LAST tab, the
      drawer does not stay empty. A fresh terminal takes its place.
- [ ] A shell that exits on its own (type `exit`) closes its tab the same
      way, and the drawer never stays empty.
- [ ] When you close or remove the whole session, perch reaps every one of
      its terminals. No leftover shell process remains against a deleted
      worktree.

## Session environment reload

`perch reload` and its drawer button relaunch a running agent so it picks up an
environment variable it did not have at launch. This is real-credential,
real-agent territory. The mock gate cannot exercise it.

- [ ] Run `aws sso login` (or an equivalent file-based credential refresh) in the
      session terminal. With no reload, confirm the running agent's next call
      uses the fresh credentials.
- [ ] Export a new environment variable in the session terminal, and click
      the labelled reload button (↻ env → agent) in the terminal tab strip.
      The button reloads from the ACTIVE tab's environment. Confirm the agent
      relaunches, the conversation is intact (not a fresh session), and the
      agent can see the new variable.
- [ ] After the relaunch, the AGENT pane redraws cleanly into a fresh terminal:
      no garbled or overlapping agent UI painted over the old frame, and no
      wrong-width wrapping (the respawned agent is sized to the pane).
- [ ] Repeat by running `perch reload` by hand in the session terminal instead
      of clicking the button. Confirm the same outcome.
- [ ] Neither the reload button nor a typed `perch reload` prints "command not
      found". The drawer's PATH includes the running binary. `$PERCH_BIN
      reload` works too.
- [ ] After the relaunch, confirm the session terminal is left in a clean state,
      not mid-command or showing stray output from the reload.
- [ ] The reload button is absent from the home shell drawer.
- [ ] With more than one shell open, the reload button names the active shell
      (↻ env → agent · shell N), so its target is unambiguous. A ▾ caret
      beside it opens a picker listing every shell. Open two shells, and
      confirm the button names the active one. Use the caret menu to reload a
      specific chosen shell, not the focused tab. Confirm that shell's
      environment reaches the agent.

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
- [ ] After you answer, the look clears. A reply resumes running. Rejecting
      or cancelling falls back to a steady idle with no completion toast.
- [ ] When the question arrives on the active session while the agent view
      shows, perch focuses the pty. You can then type the answer, with a
      brief emphasis ring. Confirm it does not steal focus when the question
      is on a background session, or when you are on the code or diff view.

## Session and turn loop (opencode)

Mocks cover the opencode side-channel, against the v1.15.12 contract. There
is no automated real-binary test. Verify against a real `opencode`.

- [ ] A new `opencode` session runs `opencode serve`, then attaches. The TUI
      appears and accepts input.
- [ ] While the agent works, the sidebar shows running. When the turn ends,
      the sidebar shows done (a ✓), and an ambient completion notification
      fires.
- [ ] The done ✓ appears AND STAYS for a turn that involved a tool-approval
      prompt. Run a turn that edits a file or runs a command, so opencode
      asks for approval in its TUI. Approve it there, and let the turn
      finish. The sidebar settles on done (a ✓) and holds it. It never slips
      back to the idle dot. opencode publishes more than one idle frame for
      a single transition: a `session.status{idle}` event, then a deprecated
      `session.idle` alias, plus a repeated snapshot on a silent reconnect.
      A trailing idle used to revert the fresh ✓. The monitor now treats a
      redundant idle as a no-op, so the turn-done survives.
- [ ] Opening a session fires no completion toast before any turn runs.
- [ ] A tool call raises the amber "needs you" signal on the sidebar row plus a
      blocking "Approval needed" notification. You approve or reject in
      opencode's own attach TUI. perch pops no card and posts no reply,
      because opencode owns the prompt. When the turn ends, the signal
      clears to done.
- [ ] Resume: run a turn, close the session, and reopen it. It resumes the
      same conversation, not a fresh session. This proves perch captured the
      session id and passed it back.

## Diff view

- [ ] Switch to the diff view.
- [ ] The changed-file list shows the written file.
- [ ] Open a file to see its hunks with `+` lines.
- [ ] Stage stages a hunk. Discard reverts it.
- [ ] The sidebar row and the status line show live `+N -N` counts that update
      after a file changes.
- [ ] When you stage a hunk, its file row flashes, and the file leaves the
      list once its last hunk is staged.
- [ ] After staging, the counts and the files-to-review pill animate down.
      This proves staging refreshed the diffstat through the diff-changed
      path, not the filesystem-watch path.

## Layout

- [ ] `Ctrl-b` collapses the sidebar. The toggle rail re-expands it. The
      state survives a restart.
- [ ] `` Ctrl-` `` collapses and expands the shell drawer. The state persists.

## Single instance and attach

This path crosses two processes and a D-Bus message, so only a real run proves
it.

- [ ] With a window open, `perch attach <repo-name-or-path>` in a second terminal
      raises and focuses the existing window and selects the matching session. No
      second window opens. On Linux, the forwarding process exits non-zero.
      That exit code is expected.
- [ ] A bare `perch` launched while one is running raises the existing window.

## Settings and theme

- [ ] Open Settings.
- [ ] Change the theme to update colors immediately.
- [ ] Toggle do-not-disturb.
- [ ] Toggle glass effects off. Floating chrome becomes solid. Toggle glass
      on again to restore the frosted look. The setting persists across a
      restart.
- [ ] Revoke an always-allow rule if any exist.
- [ ] Close and reopen Settings. The theme choice persisted.

## Look and feel

Real WebKit renders the glass material, the focus ring, and the hover lift.
You cannot check them headlessly.

- [ ] Floating chrome (the palette, a dialog, a menu) is frosted and
      translucent. You can see the panes blurred behind it, and the text
      stays legible. Work panes are opaque.
- [ ] With `WEBKIT_DISABLE_COMPOSITING_MODE=1`, glass falls back to a solid tint
      and chrome stays legible.
- [ ] When you focus a sub-window, an accent ring shows on that zone. The
      other panes stay fully visible.
- [ ] Hovering a session row or a notification gives it a small lift. With
      reduced motion on, perch suppresses the lift.
- [ ] A session reaching done gives its status icon a one-shot settle pop.
      With reduced motion on, there is no pop.
- [ ] A BACKGROUND session draws a color-coded left bar and a soft glow
      across its whole row when it is awaiting your approval, awaiting an
      answer, or when it errored or finished. The colors are: amber for
      approval, info for a question, red for an error, and calm green for
      done. Awaiting and errored rows pulse slowly. A done row gives a
      gentler settle. The active (open) session never signals this way. With reduced
      motion on, the row shows a static colored bar and steady tint, no
      pulse. Confirm a finished, asking, or errored background row draws the
      eye without you switching to it.
- [ ] Finish-signal persistence. Let a BACKGROUND session finish (or error),
      so its row signals. Then open it. The signal goes quiet. Switch away to
      another session, and confirm the row stays quiet. Its ✓ (or ✗) status
      word still shows. Then let that session finish a fresh turn: its row
      signals again. An awaiting-approval or awaiting-answer row, by contrast,
      keeps signalling until you act on it.
- [ ] Each session has a stable color, a stripe on its row and the same color on
      its notifications, the same across restarts.

## Desktop notifications

- [ ] Background the window and trigger a blocking event (an approval or an
      agent error). An OS notification appears. OS notifications fire for
      blocking events only, and only while the window is unfocused.
      Completions are ambient and do not notify.
- [ ] With do-not-disturb on, perch silences ambient and routine events but
      still shows them in the hub, recorded as read. Blocking events still
      surface.
- [ ] Auto-read on view: with the window focused, switch to (or click into) a
      session that has unread notifications. Its notifications go read and the bell
      badge drops by that session's share, without opening the hub. They stay
      listed in the hub (read), not deleted. A turn that completes on the session
      you are already watching now bumps the bell too. The live signal is no
      longer swallowed on arrival. Switching away and back, or opening the
      hub, marks it read.
- [ ] Notifications that land while the window is unfocused, or for a session
      you are not viewing, still accumulate on the bell until you switch to
      them.
- [ ] A question superseded by an approval does not double up on the bell.
      Prompt an agent to ask a question, then let its next step need a tool
      approval. Once the agent moves on, the "Question" notification clears.
      Only the fresh "Approval needed" notification remains. A claude
      session with several approvals queued keeps all of them.

## Live status (both agents, across sessions)

- [ ] With two or more sessions open, watch a BACKGROUND session's sidebar row
      (verify for claude AND opencode). Its status icon updates live: running
      while it works, ✓ done when its turn ends, amber when it needs you.
      This happens even while you are focused on a different session, a
      different view, or when the window is unfocused. The row never goes
      stale waiting for you to click into it.
- [ ] Every session row shows its status word at all times (running, done, idle,
      needs you, asking you, error, exited), so you read all sessions' status from
      the left pane without switching between them. A running row's ◐ icon
      spins while it works. With reduced motion on, the icon holds still.

## Accessibility (keyboard)

- [ ] Command palette: arrow keys move the selection, Enter runs, Escape closes.
- [ ] Menu bar: open a menu, arrow between items, Enter activates, Escape closes.
- [ ] Pane split handles: focus and resize with arrow keys.

## Removal and stale cleanup

- [ ] Remove a clean worktree session: the confirm dialog says it removes the
      session and its worktree and keeps the branch. The tree and record are
      gone. The branch remains.
- [ ] Remove a dirty worktree session: a force-confirm appears. Cancel leaves
      everything intact. Force removes the tree and discards the changes, and
      the branch remains.
- [ ] Remove an in-repo session: the record disappears. The repository and
      its branch are untouched.
- [ ] With two sessions older than the stale threshold (set it low to
      trigger), the banner appears on relaunch with the right count.
      Dismissing the banner hides it for that session.
- [ ] Change the stale threshold in Settings (Sessions): sessions cross the
      stale line at the new day count, and clearing the field restores the
      30-day default.
- [ ] Open the cleanup panel. perch checks clean and merged rows. perch
      leaves dirty or unmerged rows unchecked, with a warning. Select-all
      checks all. Remove frees the trees and branches.
- [ ] In the cleanup panel, check a dirty or unmerged row. The safe Remove
      button skips it. A distinct warn-colored Force remove unsafe button
      appears, disabled until such a row is checked. That button asks a
      separate confirmation that names the data loss. Only then does it
      discard the tree and the unmerged branch.
- [ ] The cleanup panel's Open button opens that session.

## Sidebar resume and the home shell

- [ ] A sidebar row shows the branch, the agent, and a relative last-active time.
- [ ] An empty sidebar shows the no-sessions hint.
- [ ] Click a row to show the resume preview. Cancel leaves the session
      closed. Open resumes it.
- [ ] The resume preview says whether Open continues the previous conversation
      or starts fresh. It also shows the fork point (Forked from ...) for a
      worktree session that recorded one.
- [ ] The home screen (no active session) shows a welcome card and a live shell
      drawer rooted at the launch directory or your home directory.
- [ ] Run a command in the home shell, open a session, then return home. The
      earlier output is still in the scrollback. The home shell stays mounted.
- [ ] The home shell and a session shell are independent ptys.

## Real-WebKit and real-agent smoke

These prove the newest behavior from the finalization campaign. The mock
gate cannot reach it: a native drag off the desktop, solid glass over a live
pty, split keep-alive, an agent that dies on its own, and the stubbed-binary
guard.

- [ ] **Native file drop.** Drag a file from your desktop file manager onto an
      agent pane. The pane receives the file's absolute path, not a bare
      basename. If you drop on a specific pane, the file routes there,
      rather than to whichever pane was last active. The agent can open the
      path.
- [ ] **Solid overlays over the terminal.** Discard a hunk and read the undo
      toast, then reopen a closed session and read its resume preview. Both render
      as solid surfaces over the composited terminal, with no terminal text
      showing through them.
- [ ] **Split keep-alive.** Assign a session to the split pane and scroll its
      terminal back a few screens. Toggle the split off with `\` and on
      again. The scrollback is intact. perch did not rebuild the pane as a
      blank xterm.
- [ ] **Exited agent, and the silent close.** Let an agent exit or crash on its
      own (quit it from inside, or kill its process). The session flips to
      `exited`, with the "session has ended" overlay and a Reopen button.
      The sidebar row goes dim, not red. With the window backgrounded,
      confirm the OS fires an "Agent exited" notification for that
      self-death. Then, separately, close a session through perch (Remove or
      Close). The teardown is silent and fires no exited notification.
- [ ] **Opening a live session never restarts it.** With a session running, select
      it and press `Enter`, then also open it from the Session menu. Both
      only focus the pane. perch never kills or respawns the running agent,
      and its scrollback and conversation stay unbroken.
- [ ] **`i` reaches the agent.** With the agent view active, press `i`. The agent
      terminal takes focus and your typing goes to the agent, not the shell drawer
      or the app.
- [ ] **Splitter drag with a live agent.** With an agent mid-task, drag the pane
      splitter. The TUI reflows cleanly, with no garbling or repaint splutter.
- [ ] **Keep-alive across views and sessions.** Scroll an agent terminal, expand a
      diff hunk, and place the editor cursor mid-file. Switch to another view and
      another session, then switch back. The terminal scrollback, the expanded hunk,
      and the editor cursor and any unsaved edits are exactly as you left them.
      perch tore down and rebuilt nothing.
- [ ] **Stubbed-binary guard.** Build without the frontend (`make build`, `make
      install`, or a bare `go install`) and run the result. It refuses to launch and
      prints `perch: this binary was built without the frontend. Run 'make gui-build'
      (or 'make gui-install') and reinstall`. No blank window opens.
- [ ] **First-run empty state.** Launch with no active session. The empty screen
      shows the welcome guidance: what a session needs (a git repository and
      `claude` or `opencode` on `PATH`), a plain-word note on worktrees, and
      the pointer to `perch doctor` and `docs/usage.md`.
- [ ] **Attention count in the window title.** With perch backgrounded and a
      session awaiting your approval or a question, the OS window title reads
      `perch (N need you)`. The title returns to `perch` once every session
      is handled. The count follows approvals and questions, debounced so it
      does not thrash.

## Shutdown

- [ ] Remove a session through the command palette and confirm it leaves the
      sidebar and `workspaces.json`.
- [ ] Close the window with no crash and no orphaned process. Check `ps` for
      stray shells.
