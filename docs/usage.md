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

- **Repository.** One of the repositories perch discovered under your roots.
- **Agent.** `claude` or `opencode`.
- **Worktree or in-repo.** A worktree (the default) isolates the work on its own
  branch in its own directory. Turn it off to run the agent in the repository
  itself, on the current branch.
- **New branch or existing branch.** A new branch starts from a base ref you
  pick. An existing branch checks out a branch you already have.

Branch names must match `A-Z a-z 0-9 . _ / -`. If you ask for a branch that a
perch session already owns, perch opens that session instead of creating a
duplicate. If the worktree cannot be created because the tree has uncommitted
changes, perch tells you and keeps the dialog open.

Creating a session registers it and spawns its terminal.

### Resuming a session

Clicking a session in the sidebar focuses it. If it is not open yet, whether new
or closed earlier, perch first shows a preview of its branch, agent, last active
time, and change count; choose **Open** to start it or **Cancel** to leave it
closed. A session that is already open switches to it without reopening, so its
terminal keeps running. Pressing **Enter** opens the selected session. When
perch opens a session, it passes the last session id back to the agent so the
conversation continues rather than starting fresh.

To bring back a session you closed, click its row again. A closed row is dimmed
until you reopen it.

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

When an agent wants to run a tool, perch blocks the call and shows an approval
card docked at the bottom of the active session. The card names the tool and
summarizes its input. You have three choices:

- **Allow** runs this one call.
- **Deny** refuses it.
- **Always** runs it and remembers the decision. perch stores a rule keyed on
  the agent, the tool, and a hash of the exact input. A later call auto-approves
  only when all three match. A different input to the same tool still asks.

**Approve all** and **Deny all** act on every pending request for the active
session. You manage and revoke stored rules in Settings.

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
| `running` | ◐ | running | dim, steady |
| `idle` | ◯ | idle | dim, steady |

Pulses are suppressed when your system asks for reduced motion. Each session
also carries a stable color, shown as a stripe on its row and on its
notifications, so you can track one agent across the window.

## The diff view

Switch to the diff view to see what a session changed. The file list shows
changed files with their counts. Open a file to see its hunks. For each hunk you
can:

- **Stage** it with `git apply --cached`.
- **Discard** it with a reverse apply.
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

## The file tree and editor

The file tree lists the session's worktree, respecting `.gitignore`. Right-click
a file to open it, reveal it in your file manager, copy its path, or send it to
the agent. The editor is CodeMirror with a git gutter and search. Save with
`Ctrl-S` or `Cmd-S`. Markdown, Mermaid, and images open in a read-only preview.
You can also drop files or text onto a terminal to write them into the pty.

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
approval you need to make or an agent error, and only while the perch window is
in the background. Completions are ambient and stay in the hub without
interrupting you.

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
| `Enter` | Open the selected session |
| `i` | Enter TERMINAL mode |
| `:` or `Ctrl-K` / `Cmd-K` | Open the command palette |

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
