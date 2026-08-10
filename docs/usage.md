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

- **Name.** An optional label for the session. The repo, branch, and agent show
  next to it, so a short name for the task is enough. Left blank, the session
  takes the branch name.
- **Repository.** One of the repositories perch discovered under your roots.
- **Agent.** `claude` or `opencode`.
- **Worktree or in-repo.** A worktree (the default) isolates the work on its own
  branch in its own directory. Turn it off to run the agent in the repository
  itself, on the current branch.
- **New branch or existing branch.** A new branch starts from a base ref that
  defaults to the repository's own branch rather than a fixed `main`. perch
  suggests the branch name too, drawn from the agent and the session name (a
  `feature` session on `claude` becomes `claude/feature`) and made unique against
  the branches you already hold, so a second session never collides with the
  first. Edit the suggestion if it does not suit. An existing branch checks out a
  branch you already have.

Branch names must match `A-Z a-z 0-9 . _ / -`. If you ask for a branch that a
perch session already owns, perch opens that session instead of creating a
duplicate. If the worktree cannot be created because the tree has uncommitted
changes, perch tells you and keeps the dialog open.

Creating a session registers it and spawns its terminal. To rename a session
later, double-click or right-click its name in the sidebar and type a new one.

### Resuming a session

Clicking a session in the sidebar focuses it. If it is not open yet, whether new
or closed earlier, perch first shows a preview of its branch, agent, last active
time, and change count; choose **Open** to start it or **Cancel** to leave it
closed. A session that is already open switches to it without reopening, so its
terminal keeps running. Pressing **Enter** opens the selected session; on one
that is already live it only focuses the pane and never restarts the agent. When
perch opens a session, it passes the last session id back to the agent so the
conversation continues rather than starting fresh.

To bring back a session you closed, click its row again. A closed row is dimmed
until you reopen it. If a session's agent exits or crashes while its terminal
stays alive, the pane reads **exited** and shows a **Reopen** button that starts
it again; the sidebar row goes dim rather than red, since an ended agent is not
an error. See [Reading the sidebar](#reading-the-sidebar).

Moving around never costs you your place. Switching views or sessions keeps every
pane alive: the agent terminal and its scrollback, the shell drawer, the diff and
its opened hunks, and the editor with its cursor and unsaved edits all stay
mounted and return exactly as you left them. Nothing is torn down and rebuilt
behind your back, and toggling the split stage keeps each pane's terminal rather
than spawning a blank one.

### Removing a session

Removing a session is a two-step, reversible action. The confirm dialog states
plainly that it removes the session and its worktree from disk and keeps the
branch. After you confirm, perch hides the row and shows an undo toast for six
seconds before it does the real removal.

- A worktree session has its tree deleted with `git worktree remove`. If the
  tree has uncommitted changes, perch asks again before a forced removal that
  discards them. Remove never deletes the branch.
- An in-repo session is dropped from the registry only. The repository and its
  branch are untouched.

### Cleaning up stale sessions

When sessions go unused past a threshold, the sidebar shows a banner. Open the
cleanup panel to review them in a table. Rows that are clean and already merged
are pre-checked as safe to remove; dirty or unmerged rows are left unchecked
with a warning. Removing selected sessions frees their worktrees and deletes
their branches.

## Approvals

Approvals work one way for Claude and another for opencode, because the two
agents hand perch different reins.

For **Claude**, perch owns the decision. When Claude wants to run a tool, its
hook blocks the call and perch shows an approval card docked at the bottom of the
active session. The card names the tool and shows its input, scrollable when it
runs long, so a large or many-lined request is legible rather than reduced to a
blind summary. You have three choices:

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
permission prompt, and perch cannot silence it, so perch stands back rather than
prompting you twice. It shows no card and stores no rule. It only marks the
session as waiting on you so the sidebar tells you a decision is due, and you
answer in opencode's own prompt in the pane.

### Questions are a signal, not a card

An agent can also ask you a question, through Claude's `AskUserQuestion` or
opencode's `question.asked`. This is different from an approval. perch does not
pop a card and does not answer for you. It marks the session as asking you and,
when that session is active and you are on the agent view, focuses its terminal
so you can answer in the agent's own interface.

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
| `running` | ◐ | running | dim, steady |
| `idle` | ◯ | idle | dim, steady |

The `exited` state means the agent process ended, gracefully or by a crash, while
its shell stayed alive; the session shows the in-pane "session has ended" overlay
with a Reopen button, and its sidebar row reads dim rather than as a red error.

Pulses are suppressed when your system asks for reduced motion. Each session
also carries a stable color, shown as a stripe on its row and on its
notifications, so you can track one agent across the window.

A background session (one you are not currently viewing) that needs you, is
asking you, errored, or has just finished also raises a row-level signal: a
color-coded left bar and a soft glow across the whole row, in that state's
color. It persists until you open the session, so a finished or waiting agent
catches your eye without your switching to it. Awaiting and errored rows pulse;
a finished row settles; reduced motion renders a static bar. The session you are
viewing never begs.

## The diff view

Switch to the diff view to see what a session changed. The file list shows
changed files with their counts. Open a file to see its hunks. For each hunk you
can:

- **Stage** it with `git apply --cached`. A staged hunk then offers **Unstage**,
  which lifts it back out of the index with `git apply --reverse --cached`.
- **Discard** it with a reverse apply. Discard is reversible: perch shows an undo
  toast for a moment before it commits the removal, so a hunk sent away by mistake
  is a click from coming back.
- **Send** it to the agent.

Staging touches the index, so the file leaves the list once its last hunk is
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
API token, or any other variable in the session terminal, and the running agent
never sees it: it started before the export and has no way to notice one made
later. `perch reload`, run in the session terminal, or the reload button in that
session's shell drawer, closes that gap. Both take the drawer's current
environment and relaunch the agent with it, resuming the same conversation
rather than starting over. The reload button sits in the drawer's header, next
to the collapse control, and is absent on the home shell, which has no agent to
relaunch.

Most credential refreshes need no reload at all. Something like `aws sso login`
writes a fresh token to a cache file on disk, and the agent's SDK rereads that
file on its next call, so the update reaches a running agent with no restart.
Reach for `perch reload` only when the agent needs a variable it did not have at
launch: a new or changed environment variable, not a refreshed file-based
credential.

## The file tree and editor

The file tree lists the session's worktree, respecting `.gitignore`. Right-click
a file to open it, reveal it in your file manager, copy its path, or send it to
the agent. The editor is CodeMirror with a git gutter and search. Save with
`Ctrl-S` or `Cmd-S`. Markdown, Mermaid, and images open in a read-only preview.
You can also drop files or text onto a terminal to write them into the pty. A
file dragged in from your desktop file manager lands in the pane under the
cursor as its absolute path, so the agent receives a path it can open rather
than a bare filename.

## Settings

Open Settings from the menu or the command palette.

- **Appearance.** Theme, density, font, and a glass-effects switch.
  - Themes: `gruvbox` (default), `tokyo-night`, `catppuccin`, `dracula`, `nord`,
    `rose-pine`, `one-dark`, `perch-cyan`, and `light`.
  - Densities: `dense` (default), `comfortable`, `ultra`.
  - Fonts: `geist` (default), `ibm-plex`, `inter`.
  - Glass effects on makes floating chrome frosted; off makes it solid.
- **Notifications.** A do-not-disturb switch. With it on, ambient and routine
  notifications are silenced but still recorded in the hub, so a catch-up stays
  complete. Blocking notifications still surface.
- **Always-allow rules.** The list of stored approval rules, each revocable.

Theme and other appearance changes apply immediately and persist across
restarts.

## Notifications

The notification hub collects events and can be filtered by approvals, errors,
and completions. Clicking a notification takes you to the session it belongs to.
Opening the hub marks its notifications read, so the unread count clears once you
have seen them. A desktop notification fires only for a blocking event, an
approval you need to make, an agent error, or an agent that has exited, and only
while the perch window is in the background. Completions are ambient and stay in
the hub without interrupting you.

## Keyboard control

The cockpit is mouse-first; every action is clickable. A modal keyboard layer
accelerates it. The current mode shows in the status line.

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
chord, or by clicking outside the terminal. `Esc` does not leave TERMINAL mode;
it is yours to send to the agent.

### COMMAND mode

The command palette owns the keys. Arrow up and down move the selection, `Enter`
runs the command, and `Esc` closes the palette. The palette covers session and
view actions, approvals, notifications, settings, and help, and remembers what
you ran recently.

## Multiple windows

Launching a second `perch` does not open a second window. The single-instance
lock raises the running window. Use `perch attach <query>` from a terminal to
focus a specific session in the running window.
