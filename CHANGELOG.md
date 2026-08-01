[perch](README.md) / Changelog

# Changelog

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Perch has not
tagged a release yet, so everything to date sits under Unreleased.

## [Unreleased]

### Added

- A Wails desktop cockpit: a Go backend in a WebKit2GTK webview driving a Svelte
  5 SPA, as a single static binary with no tmux, daemon, or background server.
- Worktree sessions. Creating a session derives a linked git worktree on its own
  branch and runs `git worktree add`. In-repo sessions run the agent in the
  repository itself without a worktree.
- A direct pseudo-terminal per pane, rendered with xterm.js. The agent runs in
  it, and closing a pane kills its process group.
- A three-zone layout: a session sidebar, a stage that swaps between agent, code,
  and diff views and can split into two sessions, and a pinned shell drawer.
  Sizes and collapse state persist across launches.
- A home screen with a welcome card and a shell rooted at the launch directory,
  kept mounted as you move between sessions.
- Agents for `claude` (a per-session loopback hook listener) and `opencode`
  (the `opencode serve` SSE stream), each advertising its capabilities so the UI
  shows only what it supports.
- Inline tool-call approvals for Claude: a docked Allow, Deny, or Always card per
  request, with Approve all and Deny all scoped to the active session. opencode
  approvals are owned by opencode's own terminal prompt; perch surfaces only a
  passive attention signal for them, not a card.
- Always-allow rules, stored and revocable in Settings, matched on the agent,
  the tool, and a hash of the exact input.
- A question signal that distinguishes an agent asking you something from a tool
  needing approval. perch marks the session and focuses its terminal; you answer
  in the agent's own pane.
- A notification hub and OS desktop notifications in tiers, with do-not-disturb
  that silences the quieter tiers while still recording them.
- A diff view with per-hunk staging and discarding, and sending a hunk to the
  agent as context.
- A CodeMirror editor with a git gutter, search, and save, a gitignore-aware
  file tree with reveal and copy-path, and a read-only preview for markdown,
  Mermaid, and images.
- Stale-session cleanup: a launch banner and a panel that lists unused worktree
  sessions, pre-checks the clean and merged ones, and removes their trees and
  branches.
- Session resume from a sidebar preview, and reversible removal with an undo
  window.
- Nine themes, three densities, three fonts, and a glass-effects toggle, with a
  modal keyboard layer and a fuzzy command palette over a mouse-first UI.
- `perch attach <query>` to focus the running window on a session, and `perch
  doctor` to check dependencies.
- A top-level error boundary with a crash screen, so an uncaught render error no
  longer leaves a blank window.
- A container-first test framework: one `perch-dev` image that every check runs
  in, toggled by `CONTAINERIZE`, leaving the working tree untouched.
- A window and taskbar icon embedded in the binary, and a `.desktop` entry
  installed on Linux so the app switcher shows it too.

### Changed

- Production GUI builds link WebKit2GTK 4.1 instead of 4.0, which is end of life.
- Modal dialogs are opaque surfaces rather than glass, so background content no
  longer bleeds through, and each theme declares a `color-scheme` so native form
  controls match.
- Creating a session spawns its terminal immediately, rather than waiting for a
  second click.

### Removed

- Token and cost metering, including transcript tailing.
- The model-selection parameter. opencode chooses its model in its own TUI.
- The global status-hook setup. Claude status now comes from the per-session
  hook listener, so there is no setup step.

### Fixed

- A double approval prompt and a stuck approval card for opencode. opencode's
  `attach` terminal runs its own permission prompt that perch cannot suppress,
  and opencode emits no resolution event when you answer it, so perch's own card
  both duplicated the prompt and never cleared. opencode now advertises
  `approvals: false`; its `permission.asked` becomes a passive attention signal
  (sidebar state plus a blocking notification) with no card and no reply, exactly
  as opencode questions are already handled. Claude's card is unchanged.
- Tool-approval notifications that never fired because of an event-kind mismatch.
- A new-branch session that silently persisted a broken record when the branch
  already existed; the error now surfaces and nothing is saved.
- A corrupt `workspaces.json` or `settings.json` that bricked launch; the bad
  file is now quarantined and perch falls back to defaults.
- Empty backend results that serialized to JSON `null` and crashed the frontend
  at boot; they now serialize as `[]`.
- Staging or discarding a hunk that left the file list and change counts stale.
- `install.sh` building without the WebKit2GTK 4.1 build tag, which failed to
  link on a host set up by the script itself.
- Terminal and editor panes lost their content on a view switch; they now stay
  mounted and hidden, and the editor keeps unsaved edits across an external file
  change.
- Clicking a session that was already open reopened it and garbled the terminal.
  Clicking an open session now just focuses it; only a new or closed session
  opens. A closed session reopens when you click its dimmed row.
- The sidebar attention indicator stayed lit after an approval was resolved. The
  agent monitors now emit a state event on a decision, so it clears on its own.
- Clicking a notification did nothing. It now navigates to the session it belongs
  to, and opening the hub marks its notifications read so the badge clears.
- Notifications for a removed session are now pruned.

### Security

- No listening IPC port. Frontend and backend speak over the WebKit2GTK
  script-message channel, and the dev reload socket is absent from release
  builds.
- The only production network surface is the per-session Claude hook listener:
  loopback only, an ephemeral port, and a random per-listener bearer token
  compared in constant time.
- Always-allow rules match on the hash of the full tool input, not a truncated
  display string.
- Every IPC argument is validated: identifiers against a charset allowlist and
  worktree paths against the configured roots. All git work runs as argv, never
  through a shell.
- Pinned the Go toolchain to `go1.26.5`, which carries the fix for the
  `crypto/tls` advisory GO-2026-5856.
