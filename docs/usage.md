[perch](../README.md) / [Docs](README.md) / Usage

# Using perch

This guide covers driving the cockpit: sessions, approvals, diffs, the shell,
settings, and keyboard control. For how perch is built, see
[ARCHITECTURE.md](../ARCHITECTURE.md).

## Opening the cockpit

```sh
perch                 # project root is the current directory
perch /path/to/code   # project root is the given directory
```

The project root is where perch looks for repositories. With a `config.toml`
listing `roots`, perch scans those instead. The sidebar on the left lists your
sessions. The main stage on the right shows the selected session.

## Sessions

A session pairs a git worktree with an agent. The worktree is a real branch
checked out into its own directory, so the agent works without touching your
main checkout.

### Creating a session

Open the New Session dialog from the menu, the command palette, or the
quick-start buttons on the empty screen. You choose:

- **Name.** An optional label for the session. The repository, branch, and
  agent show next to it, so a short name for the task is enough. If you leave
  the name blank, the session takes the branch name.
- **Repository.** One of the repositories perch discovered under your roots.
- **Agent.** `claude` or `opencode`.
- **Worktree or in-repo.** A worktree (the default) isolates the work on its own
  branch in its own directory. Turn it off to run the agent in the repository
  itself, on the current branch.
- **New branch or existing branch.** A new branch starts from a base ref that
  defaults to the repository's own branch, not a fixed `main`. perch also
  suggests a branch name. It draws the name from the agent and the session
  name: a `feature` session on `claude` becomes `claude/feature`. perch makes
  the name unique against the branches you already hold, so a second session
  never collides with the first. Edit the suggestion if it does not suit. An
  existing branch checks out a branch you already have.

Branch names must match `A-Z a-z 0-9 . _ / -`. If you ask for a branch that a
perch session already owns, perch opens that session instead of creating a
duplicate. If perch cannot create the worktree because the tree has
uncommitted changes, perch tells you and keeps the dialog open.

When you create a session, perch registers it and spawns its terminal. To
rename a session later, double-click or right-click its name in the sidebar
and type a new one.

### Resuming a session

Click a session in the sidebar to focus it. If the session is not open yet,
whether new or closed earlier, perch first shows a preview: its branch,
agent, last active time, and change count. Choose **Open** to start the
session, or **Cancel** to leave it closed. If the session is already open,
clicking it switches you to that session without reopening, and its terminal
keeps running. Press **Enter** to open the selected session. On a session
that is already live, **Enter** only focuses the pane. It never restarts the
agent. When perch opens a session, perch passes the last session id back to
the agent, so the conversation continues rather than starting fresh.

To bring back a session you closed, click its row again. A closed row is
dimmed until you reopen it. If a session's agent exits or crashes while its
terminal stays alive, the pane reads **exited**. It shows a **Reopen** button
that starts the agent again. The sidebar row goes dim, not red, because an
ended agent is not an error. See [Reading the sidebar](#reading-the-sidebar).

You never lose your place when you move around. Switching views or sessions
keeps every pane alive: the agent terminal and its scrollback, the shell
drawer, the diff and its opened hunks, and the editor with its cursor and
unsaved edits. Every pane stays mounted and returns exactly as you left it.
Perch tears down and rebuilds nothing behind your back. Toggle the split
stage, and each pane keeps its terminal rather than spawning a blank one.

### Removing a session

You remove a session in two reversible steps. The confirm dialog states
plainly that removal deletes the session and its worktree from disk, and
keeps the branch. After you confirm, perch hides the row and shows an undo
toast for six seconds. Then perch does the real removal.

- perch deletes a worktree session's tree with `git worktree remove`. If the
  tree has uncommitted changes, perch asks again before a forced removal that
  discards them. Remove never deletes the branch.
- perch drops an in-repo session from the registry only. The repository and
  its branch are untouched.

### Cleaning up stale sessions

When sessions go unused past a threshold, the sidebar shows a banner. Open the
cleanup panel to review them in a table. perch pre-checks rows that are clean
and already merged, marking them safe to remove. perch leaves dirty or
unmerged rows unchecked, with a warning. When you remove selected sessions,
perch frees their worktrees and deletes their branches.

## Approvals

Approvals work one way for Claude and another for opencode, because the two
agents hand perch different reins.

For **Claude**, perch owns the decision. When Claude wants to run a tool, its
hook blocks the call. perch shows an approval card, docked at the bottom of
the active session. The card names the tool and shows its input. The input
scrolls when it runs long, so a large or many-lined request stays legible
instead of a blind summary. You have three choices:

- **Allow** runs this one call.
- **Deny** refuses it.
- **Always** runs it and remembers the decision. perch stores a rule keyed on
  the agent, the tool, and a hash of the exact input. A later call auto-approves
  only when all three match. A different input to the same tool still asks.

From the keyboard the card takes `a` to allow, `d` to deny, and `Shift+A` to
always-allow. The Allow button holds focus the moment the card opens, so `Enter`
allows.

**Approve all** and **Deny all** act on every pending request for the active
session. You manage and revoke stored rules in Settings.

For **opencode**, the agent owns the decision. opencode's own terminal runs its
approval prompt, and perch cannot silence it. So perch stands back, rather
than prompting you twice. It shows no card and stores no rule. perch only
marks the session as waiting on you, so the sidebar tells you a decision is
due. You answer in opencode's own prompt in the pane.

### Questions are a signal, not a card

An agent can also ask you a question, through Claude's `AskUserQuestion` or
opencode's `question.asked`. This is different from an approval. perch does not
pop a card and does not answer for you. perch marks the session as asking you.
When that session is active and you are on the agent view, perch focuses its
terminal. You can then answer in the agent's own interface.

## Reading the sidebar

Each session shows an attention state with a color, an icon, and a label, never
color alone.

| State | Icon | Label | Look |
|-------|------|-------|------|
| `awaiting-approval` | ⚠ | needs you | amber, fast pulse |
| `awaiting-input` | ? | asking you | cyan, slow pulse |
| `done` | ✓ | done | green, steady |
| `errored` | ✗ | error | red, steady |
| `exited` | ⏻ | exited | dim, steady |
| `running` | ◐ | running | green, spinning |
| `idle` | ◯ | idle | dim, steady |

The `exited` state means the agent process ended, gracefully or by a crash,
while its shell stayed alive. The session shows the in-pane "session has
ended" overlay with a Reopen button. Its sidebar row reads dim, not as a red
error.

Every row shows its status word at all times, so you can read all sessions'
status from the left pane without switching between them. When your system
asks for reduced motion, perch suppresses pulses and the running spin. Each
session also carries a stable color, shown as a stripe on its row and on its
notifications, so you can track one agent across the window.

A background session is one you are not viewing. If it needs you, is asking
you, has errored, or has just finished, it also raises a row-level signal: a
color-coded left bar and a soft glow across the whole row, in that state's
color. This way, a finished or waiting agent catches your eye without you
switching to it. Once you open a finished or errored session, the signal
goes quiet. It stays quiet when you switch away, until that session finishes
or errors again. A session awaiting your approval or an answer is a pending
action, so its row keeps signalling
until you act on it. Awaiting and errored rows pulse. A finished row settles.
Reduced motion renders a static bar. The session you are viewing never signals this way.

## The diff view

Switch to the diff view to see what a session changed. The file list shows
changed files with their counts. Open a file to see its hunks. For each hunk you
can:

- **Stage** it with `git apply --cached`. A staged hunk then offers **Unstage**.
  Unstage lifts it back out of the index with `git apply --reverse --cached`.
- **Discard** it with a reverse apply. Discard is reversible. perch shows an
  undo toast for a moment before it commits the removal. So a hunk sent away
  by mistake is a click from coming back.
- **Send** it to the agent.

Staging touches the index. The file leaves the list once its last hunk is
staged, and the change counts in the sidebar and status line update. A file's
row flashes briefly when you stage it.

## The shell

Every session has a collapsible shell drawer at the bottom, a separate terminal
from the agent's pane, opened in the session's worktree. The home screen, shown
when no session is active, has its own shell drawer rooted at the launch
directory (or your home directory). The home shell stays mounted as you move
around, so its scrollback survives.

### Giving the agent a new environment variable

A process reads its environment once, at exec. Export a new `AWS_PROFILE`, an
API token, or any other variable in the session terminal. The running agent
never sees it. The agent started before the export, and has no way to notice
a variable made later. Two things close that gap: running `perch reload` in
the session terminal, or clicking the reload button in that session's shell
drawer. Both take the drawer's current environment and relaunch the agent
with it. The same conversation continues, rather than starting over. The
reload button sits in the drawer's header, next to the collapse control. It
is absent on the home shell, because the home shell has no agent to relaunch.

Most credential refreshes need no reload at all. Something like `aws sso login`
writes a fresh token to a cache file on disk. The agent's SDK rereads that
file on its next call. So the update reaches a running agent with no restart.
Use `perch reload` only when the agent needs a variable it did not have at
launch: a new or changed environment variable, not a refreshed file-based
credential.

## The file tree and editor

The file tree lists the session's worktree, respecting `.gitignore`.
Right-click a file for a context menu. From it you can:

- Open the file.
- Reveal it in your file manager.
- Copy its path.
- Send it to the agent.

The editor is CodeMirror with a git gutter and search. Save with `Ctrl-S` or
`Cmd-S`. Markdown, Mermaid, and images open in a read-only preview. You can
also drop files or text onto a terminal to write them into the pty. A file
dragged in from your desktop file manager lands in the pane under the cursor
as its absolute path. The agent then receives a path it can open, rather than
a bare filename.

## Settings

Open Settings from the menu or the command palette.

- **Appearance.** Theme, density, font, and a glass-effects switch.
  - Themes: `gruvbox` (default), `tokyo-night`, `catppuccin`, `dracula`, `nord`,
    `rose-pine`, `one-dark`, `perch-cyan`, and `light`.
  - Densities: `dense` (default), `comfortable`, `ultra`.
  - Fonts: `geist` (default), `ibm-plex`, `inter`.
  - With glass effects on, floating chrome looks frosted. With them off, it
    looks solid.
- **Notifications.** A do-not-disturb switch. With it on, perch silences
  ambient and routine notifications but still records them in the hub, so a
  catch-up stays complete. Blocking notifications still surface.
- **Always-allow rules.** The list of stored approval rules, each revocable.

Theme and other appearance changes apply immediately and persist across
restarts.

## Notifications

The notification hub collects events. You can filter it by approvals, errors,
and completions. Click a notification to go to the session it belongs to.
When you open the hub, perch marks its notifications read, so the unread
count clears once you have seen them. A desktop notification fires only for
a blocking event: an approval you need to make, an agent error, or an agent
that has exited. It fires only while the perch window is in the background.
Completions are ambient and stay in the hub without interrupting you.

## Keyboard control

The cockpit is mouse-first. Every action is clickable. A modal keyboard layer
accelerates the cockpit. The current mode shows in the status line.

### NORMAL mode

| Key | Action |
|-----|--------|
| `j` / `k` | Next / previous session |
| `1` / `2` / `3` | Agent / Code / Diff view |
| `g` then `d` / `e` | Diff view / Code view |
| `g` then `t` / `T` | Cycle to the next / previous view |
| `\` | Toggle the split stage |
| `` Ctrl-` `` | Toggle the shell drawer |
| `Ctrl-b` | Toggle the sidebar |
| `/` | Filter sessions |
| `n` | New session |
| `x` | Remove the selected session, through the confirm dialog |
| `Enter` | Open the selected session |
| `i` | Enter TERMINAL mode, focusing the agent terminal |
| `:` or `Ctrl-K` / `Cmd-K` | Open the command palette |
| `?` or `F1` | Open the shortcuts and help panel |

### TERMINAL mode

Every key goes to the focused terminal. Leave with the `Ctrl-\` then `Ctrl-n`
chord, or by clicking outside the terminal. `Esc` does not leave TERMINAL mode.
perch sends `Esc` to the agent instead.

### COMMAND mode

The command palette owns the keys. Arrow up and down move the selection, `Enter`
runs the command, and `Esc` closes the palette. The palette covers session and
view actions, approvals, notifications, settings, and help, and remembers what
you ran recently.

## Multiple windows

A second `perch` launch does not open a second window. The single-instance
lock raises the running window. Use `perch attach <query>` from a terminal to
focus a specific session in the running window.
