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
- A row-level attention signal in the sidebar. A background session that is
  awaiting approval, awaiting an answer, errored, or done draws a color-coded
  left bar and a soft glow across its whole row. A finished or errored row goes
  quiet once you open the session and stays quiet when you switch away, until it
  finishes or errors again. An awaiting-approval or awaiting-answer row is a
  pending action, so it keeps signalling until you act on it. Awaiting and errored
  rows pulse; done gives a calmer settle; reduced motion renders a static bar.
  The active session never signals this way.
- A live status board in the sidebar. Every session row shows its status word at
  all times (running, done, idle, needs you, asking you, error, exited), so you
  read all sessions' status from the left pane without switching between them. A
  running session's disc icon spins so working reads as alive at a glance;
  reduced motion keeps it static.
- A diff view with per-hunk staging, unstaging, and discarding, a reversible
  discard with an undo toast, and sending a hunk to the agent as context.
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
- The production release binary builds inside that same pinned `perch-dev`
  image, so the release and the test gates share one toolchain. The release
  runner installs no GUI libraries or Node of its own.
- Prebuilt linux binaries for both `amd64` and `arm64`. Each architecture
  builds natively inside `perch-dev` on a runner of its own architecture, not
  cross-compiled. The Containerfile resolves the Go and Node architecture from
  the build host.
- A window and taskbar icon embedded in the binary, and a `.desktop` entry
  installed on Linux so the app switcher shows it too.
- One-key session actions in NORMAL mode: `n` for a new session, `x` to remove
  the selected one through its confirm, `?` or `F1` for help, and `i` to focus
  the agent terminal. The approval card takes `a` to allow, `d` to deny,
  `Shift+A` to always-allow, and `Enter` to allow.
- An `exited` session state with a Reopen button for an agent that ends or
  crashes, distinct from a running or errored one.
- First-run guidance on the empty screen: what a session needs, a plain-word
  note on worktrees, and a pointer to `perch doctor` and the usage guide.

### Changed

- Production GUI builds link WebKit2GTK 4.1 instead of 4.0, which is end of life.
- Modal dialogs are opaque surfaces rather than glass, so background content no
  longer bleeds through, and each theme declares a `color-scheme` so native form
  controls match.
- Creating a session spawns its terminal immediately, rather than waiting for a
  second click.
- The shell drawer's tab-strip actions are labeled again (⊟ Split, ↻ env →
  agent, ▼ Collapse), not bare icons. With more than one shell open the reload
  button names the active shell (↻ env → agent · shell N), and a ▾ caret opens a
  picker to reload any chosen shell rather than only the focused tab.
- The approval card shows the tool's input, scrollable when it runs long, rather
  than a blind summary that truncated a large input.
- Creating a session suggests a unique branch name from the session name and
  defaults the base ref to the repository's own branch, not a fixed `main`.
- Renamed the Go module path to lowercase `github.com/miniature-pug/perch`.
  GitHub's case redirect broke `go install` on the capitalized path, and the
  change is cheap before the first tag.

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
- An opencode turn that used a tool settled on done and then reverted to idle.
  opencode publishes more than one idle frame for a single transition, a
  `session.status{idle}` followed by a deprecated `session.idle` alias (and, on a
  silent reconnect, a repeated status snapshot), and any trailing idle clobbered the
  fresh ✓. The monitor now treats a redundant idle as a no-op, so the done ✓ persists
  until the next turn, the way Claude already holds it.
- A "Question" notification lingered on the bell after the agent moved on. When an
  agent asked a question and then its next tool needed approval, the answered
  question still showed as unread beside the fresh "Approval needed". Leaving
  awaiting-input now clears the superseded question, while any pending approval
  (Claude may have several queued at once) is left untouched.
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
- Terminal, editor, diff, and file-tree panes lost their content on a view or
  session switch; they now stay mounted and hidden, the editor keeps unsaved
  edits across an external file change, and toggling the split stage keeps each
  pane's terminal rather than spawning a blank one.
- Clicking or pressing Enter on a session that was already open reopened it and
  garbled the terminal. It now just focuses the session and never respawns or
  kills the running agent; only a new or closed session opens, and a closed
  session reopens when you click its dimmed row.
- The sidebar attention signal stayed lit after an approval was resolved. The
  agent monitors now emit a state event on a decision, so it clears on its own.
- Clicking a notification did nothing. It now navigates to the session it belongs
  to, and opening the hub marks its notifications read so the badge clears.
- Notifications for a removed session are now pruned.
- A turn completing on the session you were watching was marked read on arrival,
  which swallowed the bell badge. A live event now bumps the bell even for the
  session on screen; auto-read fires only when you switch to a session and catch
  up on what accumulated while you were elsewhere.
- A crashed or exited agent kept a stale `running` look. It now flips to a
  distinct `exited` state with a Reopen button, while a perch-initiated close
  stays silent and fires no exited notification.
- A file dropped from the OS file manager reached the agent as a bare basename
  instead of a path it could open; it now arrives as an absolute path, routed to
  the pane under the cursor.
- A binary built without the frontend opened a blank window; it now refuses to
  launch and prints how to rebuild it.
- A shell bridge leaked until shutdown; it now closes with its session. A failed
  session persist left an orphaned worktree and branch that blocked a retry; it
  now rolls them back. A slow `opencode serve` bind could kill the pane before
  its deadline; the readiness poll now derives its budget from the connect
  deadline.

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
- Bumped dompurify (3.4.7 to 3.4.13), clearing an mXSS advisory in the sole
  sanitizer for untrusted repository markdown, and mermaid (11.15.0 to 11.16.1),
  clearing five advisories in the renderer that draws untrusted `.mmd`. Both stay
  within their current major.
- Added `npm audit --omit=dev --audit-level=high` to the frontend gate, so a
  runtime-dependency advisory fails the build the way govulncheck guards the Go
  side.
- Added [SECURITY.md](SECURITY.md) with a private GitHub advisory reporting path
  and the in-scope threat surface.
- Corrected the trust boundary in the README and ARCHITECTURE: a cloned
  repository does carry committed agent-config hooks (`.claude/settings.json`, an
  opencode config) whose entries the agent runtime executes ungated on session
  start, distinct from `.git/hooks`, which clone and fetch do not carry.
- Release binaries ship a signed SLSA build provenance attestation. A download
  verifies against this repository with `gh attestation verify`. The release
  workflow pins its third-party action to a full commit SHA.
- Hardened both CI workflows: checkout runs with `persist-credentials: false`,
  each job sets a timeout, and run cancellation is limited to pull requests, so
  a push to `main` always keeps a recorded CI result.
