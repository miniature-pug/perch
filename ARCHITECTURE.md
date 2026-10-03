[perch](README.md) / Architecture

# Architecture

This is how perch works at the system level. For driving the cockpit, see the
[usage guide](docs/usage.md). For the package layout, see
[internal/README.md](internal/README.md) and
[frontend/README.md](frontend/README.md).

## Overview

Perch is a desktop cockpit for the `claude` and `opencode` coding agents.
Running `perch` or `perch <path>` launches a Wails v2 window: a Go backend
embedded in a WebKit2GTK webview that drives a Svelte 5 SPA. A few CLI
subcommands (`attach`, `doctor`, `version`) support scripting and agent
integration.

The shape of the system:

- **One static binary.** No tmux, no daemon, no server process.
- **One direct pty per pane.** Each terminal is a pseudo-terminal the backend
  spawns with a login shell, using `creack/pty`. No multiplexer.
- **No IPC port.** The frontend calls the backend over Wails bindings
  (`window.go.app.App.<Method>`) and receives Wails events. The only production
  network surface is the two listeners described under
  [Security model](#security-model): the per-agent hook listener and the
  app-wide env-sync listener behind `perch reload`.
- **JSON state.** The registry, settings, and layout are small JSON files. There
  is no database.

Facts of record:

- Module: `github.com/miniature-pug/perch`
- Go toolchain: `go1.26.6` (language floor `go 1.25.0`)
- Frontend: Svelte 5 runes built by Vite, in `frontend/`
- Key dependencies: `creack/pty` (the pty bridge), `bmatcuk/doublestar/v4`
  (globbing), `BurntSushi/toml` (config)
- Platform: Linux, with WebKit2GTK 4.1 and GTK3

## The Wails GUI model

### Process model

Perch runs as one OS process. The Wails runtime embeds a WebKit2GTK webview in a
GTK window. `//go:embed` embeds the Svelte SPA from `frontend/dist/`, so it
ships inside the binary. The `production` build tag selects the Wails
production runtime, and `webkit2_41` links WebKit2GTK 4.1. There is no separate
frontend process and no IPC server in a release build.

```
+-----------------------------------------------------------+
|  perch process                                            |
|  +----------------+   Wails bindings + events             |
|  |  Go backend    | <===================================> |
|  |  (app.App)     |          WebKit2GTK webview           |
|  +----------------+          (Svelte 5 SPA)               |
|        |                                                  |
|        |-- Bridge ........... login shell in a pty (per pane)
|        |-- ClaudeMonitor .... hooklistener (127.0.0.1:ephemeral)
|        +-- OpencodeMonitor .. opencode serve + SSE
+-----------------------------------------------------------+
```

The three downstream seams:

- **App to Bridge to shell.** `OpenWorkspace` spawns a login shell in a pty in
  the worktree. Raw bytes stream to the frontend as Wails events and keystrokes
  flow back through `WriteToPty`.
- **App to ClaudeMonitor to hooklistener.** For Claude sessions, lifecycle and
  tool-call events arrive over a per-session hook listener.
- **App to OpencodeMonitor to serve and SSE.** For opencode sessions, events
  arrive over the agent's `opencode serve` Server-Sent-Events stream.

### Single-instance lock

`app/options.go` registers a Wails `SingleInstanceLock` with the id
`com.miniature-pug.perch`. A second `perch` process, including `perch attach
<query>`, hands its arguments to the running instance through
`onSecondInstance`, which raises the window and emits a `workspace:attach` event
carrying the query. The second process then exits with a non-zero status on
Linux. This is the expected behavior of the forwarding path. When nothing is
running, `perch attach` launches the cockpit normally.

### Bound-method API

The whole `app.App` struct is bound to the frontend, so every exported method is
callable over the bridge. The load-bearing methods:

| Method | Purpose |
|--------|---------|
| `ListWorkspaces() []WorkspaceVM` | All registered sessions, with live state and caps for any that are open |
| `CreateWorkspace(agentName, repoPath, baseRef, branch, title string, worktree bool) (WorkspaceVM, error)` | Validate, resolve or create the worktree, and register the session; a blank title falls back to the branch slug |
| `SetWorkspaceTitle(id, title string) error` | Rename a session; rejects a blank title |
| `OpenWorkspace(id string) error` | Spawn the pty, prepare and start the Monitor, begin streaming |
| `CloseWorkspace(id string) error` | Tear down the pty bridge and Monitor; keep the record |
| `RemoveWorkspace(id string) error` | Remove the record; for worktree sessions run `git worktree remove`, returning `ErrWorktreeDirty` on uncommitted changes; the branch is kept |
| `ForceRemoveWorkspace(id string) error` | Force-remove the worktree tree, discarding changes; the branch is kept |
| `ListStaleSessions() ([]StaleSessionVM, error)` | Worktree sessions unused past the stale threshold, with per-row clean and merged state |
| `CleanupSessions(ids []string, force bool) error` | Bulk-remove sessions: remove the tree and delete the branch |
| `WriteToPty(paneID string, data []int) error` | Forward keystrokes to a pane's pty |
| `ResizePty(paneID string, cols, rows uint16) error` | Resize a pane's pty |
| `OpenShell(paneID, cwd string) error` | Spawn an auxiliary login-shell pty for the shell drawer |
| `HomeShellCwd() string` | The working directory for the home shell |
| `Approve(reqID, decision string) error` | Resolve a pending approval as allow, deny, or always |
| `DiffStat / Hunks / StageHunk / DiscardHunk` | The diff view and per-hunk staging for a worktree |
| `Branches(repo string) ([]string, error)` | Local branch names |
| `ListDir / ReadFile / WriteFile / RevealInFiles / CopyPath` | File-tree operations |
| `GetSettings / SaveSettings / GetLayout / SaveLayout` | Persisted UI state |
| `SetWindowFocus(focused bool)` | Track focus so OS notifications fire only while unfocused |
| `DiscoverRepos() ([]RepoInfo, error)` | Repositories under the configured roots, for the New Session dialog |

The full set is in [internal/README.md](internal/README.md) and `app/app.go`.
There is no model or token parameter anywhere: perch chooses its agents per
session and never meters usage.

`app.App` validates every argument that crosses the bridge before any pty or
git work. It checks session and pane identifiers against an `A-Za-z0-9_-`
charset allowlist, and it resolves and verifies worktree paths under the
configured roots. All git work runs as argv through `internal/proc`, never
through a shell.

### The direct-pty bridge

`OpenWorkspace` spawns a login shell inside a pseudo-terminal, with `creack/pty`,
in the worktree. The pty captures raw output and forwards it to the frontend as
Wails events, where a Svelte component feeds the bytes to an xterm.js terminal.
Keystrokes return through `WriteToPty` and resizes through `ResizePty`.
`OpenWorkspace` writes the agent's launch command, produced by the Monitor's
`Prepare`, into the shell, so the agent starts in the same pty. Because the
agent runs inside the shell rather than as the pane process, its exit returns
control to the still-alive shell and fires no `pty:exit`. So the launch line
carries an exit sentinel. After the agent command, the shell captures `$?` and
pings a loopback listener. The Monitor translates this into the `exited` state. The listener's token and
URL reach the shell through its process environment (`Monitor.PaneEnv`,
injected at spawn) rather than the typed line, which the interactive shell
would echo. Closing the bridge kills the shell's process group; an
interactive shell runs the agent as a job in its own process group, which the
tty hangup reaches while it is in the foreground. The opencode launch line also
kills its backgrounded `opencode serve` itself when `attach` exits. The launch
lines are POSIX `sh`; when the login shell is fish, nushell, elvish, csh/tcsh
or PowerShell, the line is typed as `sh -c '…'` instead.

### The agent Monitor seam

Each agent implements `agent.Monitor` (`internal/agent/monitor.go`):

```go
Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
PaneEnv() []string                    // exit-sentinel env injected at pty spawn
Start(ctx context.Context)            // launch the event pump bound to ctx
Events() <-chan Event
Approve(reqID string, d Decision) error
Capabilities() Caps                   // {approvals, attention}
Teardown() error
CurrentState() State
LastApprovalTool() string
```

`agent.NewMonitor(tool, adapter)` returns a `ClaudeMonitor` or an
`OpencodeMonitor`. `OpenWorkspace` calls `Prepare`, which provisions the listener
or the serve command, then `Start`, which translates the agent's native events
into the unified `Event` channel. The app forwards each event to the frontend as
an `agent:event`, after stamping the workspace id and a routable approval id.

### State sync

The backend forwards Monitor events to the frontend in real time and emits
`fs:changed` from a per-session filesystem watcher, debounced by 150ms. The
frontend reacts to create, open, close, and remove optimistically, so the
sidebar stays responsive. On each `fs:changed`, the frontend refreshes the
diffstat for the affected session and shows the result as a change count in the
sidebar row and the status line.

The Go-to-frontend events:

| Event | Payload | Trigger |
|-------|---------|---------|
| `agent:event` | `agent.Event` | A Monitor produces a state, approval, or question event |
| `fs:changed` | `{workspaceId, path}` | The per-session watcher fires, debounced |
| `notify` | `{tier, title, body, workspaceId}` | A notification is dispatched |
| `pty:data:<paneId>` | byte values | The pty read loop has output |
| `pty:exit:<paneId>` | exit code | A pty process exits |
| `workspace:attach` | `{query}` | A second instance forwarded a query |

These event names are string literals in the backend, with named constants only
for the two pty prefixes; the frontend mirrors them as `EVT_*` constants in
`frontend/src/lib/wails.ts`.

## Package map

`docs/diagrams/architecture.mmd` shows the dependency graph.

| Package | Responsibility |
|---------|----------------|
| `cmd/perch` | CLI entry point; dispatches subcommands and launches the GUI through `app.Run` |
| `app/` | The Wails `App`: bound-method API, input validation, pty and Monitor lifecycle, event forwarding, the fs-watcher debounce |
| `internal/pty` | The direct pty bridge; runs a login shell, forwards output, routes keystrokes and resize |
| `internal/agent` | The Monitor seam and Adapter interface, with `ClaudeMonitor` and `OpencodeMonitor` |
| `internal/hooklistener` | The per-session loopback listener that receives Claude hook posts and blocks `PermissionRequest` |
| `internal/envsync` | The one-per-app loopback listener behind `perch reload`, with a per-workspace bearer token and an in-memory-only environment delta |
| `internal/registry` | The workspace registry persisted at `~/.config/perch/workspaces.json` |
| `internal/config` | The single-layer TOML config that exposes `Config{Roots}` |
| `internal/discover` | The filesystem scanner that finds repositories under the roots |
| `internal/doctor` | The `perch doctor` health check, with OS calls injected for testing |
| `internal/git` | git subprocess wrappers behind `proc.Runner`, including ref validation and diff and hunk staging |
| `internal/fs` | Worktree filesystem helpers: directory listing and a change watcher |
| `internal/model` | Shared domain vocabulary as pure data |
| `internal/notify` | Asynchronous desktop notifications over D-Bus, with a `notify-send` fallback |
| `internal/proc` | The `Runner` interface, its `ExecRunner`, and the `FakeRunner` used in tests |
| `frontend/` | The Svelte 5 SPA |

## The Runner seam

Every shell-out in perch, every git call and hook execution, goes through the
`proc.Runner` interface:

```go
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
    RunInDir(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
    RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error)
}
```

Production code uses `ExecRunner`, which wraps `os/exec`. Unit tests inject
`FakeRunner`, which records the argv of every call and never spawns a process.
This makes the whole non-frontend surface testable without a live repository.
Integration tests, tagged `//go:build integration`, exercise real worktree and
pty behavior against throwaway repositories and run under `make test-integration`.
The project targets at least 80% statement coverage per `internal` package;
`frontend/` is exempt because its components need a browser.

## Configuration and constants

`registry.DefaultConfigDir()` resolves the state directory under
`~/.config/perch` and honors `$XDG_CONFIG_HOME`. `config.DefaultGlobalPath()`
reuses it, so the path is computed in one place. perch creates the config
directory with mode `0o700` (`registry.ConfigDirMode`). It writes
`settings.json` and `layout.json` with mode `0o600` through an atomic
write-and-rename. The loopback host `127.0.0.1` is the exported
`hooklistener.LoopbackHost`, used by both monitors.

Tuning values are named constants rather than literals. The Go side holds pty
defaults, buffer sizes, file modes, debounce intervals, the token size, and
frecency weights. The frontend gathers its timers, limits, layout defaults,
option lists, and MIME types in `frontend/src/lib/constants.ts`. It keeps its
design tokens (the type scale, spacing grid, motion, z-index ladder, colors,
and glass material) in `frontend/src/tokens/`. The settings defaults appear on
both sides of the bridge. Go and the SPA share no module, so maintainers keep
the two copies in sync by hand.

## Workspace registry and discovery

`docs/diagrams/discovery-state.mmd` shows the data flow.

perch stores sessions as a single JSON array in `workspaces.json`. Each record
carries:

| Field | Contents |
|-------|----------|
| `id` | Stable, charset-validated identifier |
| `worktreePath` | Absolute path to the worktree, equal to `repoPath` for in-repo sessions |
| `repoPath` | Absolute path to the source repository |
| `worktree` | `true` for an isolated linked tree, `false` for an in-repo session |
| `agent` | `claude` or `opencode` |
| `branch` | The session's branch |
| `baseRef` | The branch it was created from, used for the merged check at cleanup |
| `title` | Display label |
| `lastSessionID` | The agent resume id, passed to `Monitor.Prepare` |
| `lastActive` | Timestamp for ordering |

`Store.List()` returns every record, sorted by last-active descending, as a
non-nil slice. `ListWorkspaces` maps each to a view model and attaches live
state only for sessions that are open. Settings, including the always-allow
rules and the stale threshold (30 days by default), live in `settings.json`.

Discovery starts from the roots, the launch directory by default. `discover.Scan`
walks each root (a symlinked root is resolved first) to a maximum depth of 8, pruning `node_modules`, `vendor`, build-output directories such as `target`, `build`, and `dist`, and
`.git`, and returns the directories that contain a `.git` entry. `discover.Projects` orders them alphabetically; the
frecency ranking exists but nothing records access stats yet. `DiscoverRepos` runs this across all roots, dedupes by repository, and
reports an error only when every root fails.

## Status and approval pipeline

`docs/diagrams/status-sequence.mmd` shows the sequence.

For Claude, approval is a hook loop:

```
claude agent
  -> hooklistener (PermissionRequest POST, blocks)
    -> app.App emits "agent:event" (approval)
      -> Svelte ApprovalCard
        -> user clicks Allow / Deny / Always
          -> App.Approve -> Monitor.Approve -> hooklistener.Decide
            -> the POST unblocks and the agent continues
              -> App emits a state event and the UI re-renders
```

`ClaudeMonitor` installs its hooks through a per-session settings file (see
below). `PermissionRequest` fires only when Claude would itself show a
permission dialog, so Claude's allow rules and permission modes keep working;
Claude shows its own dialog while the hook waits, and if you answer there, the
hook is cancelled and the listener emits a retraction (`Event.ResolvedReqID`,
kind `approval-resolved`) so the card disappears. `awaiting-approval` clears
only when the last pending approval resolves, and `Monitor.Approve` never
blocks. The lifecycle hooks map `SessionStart` to idle (a compaction keeps the
state), `UserPromptSubmit` to running, `Stop` to done, and `StopFailure` to
errored. `PreToolUse` and `PostToolUse`, matched to `AskUserQuestion` and
`ExitPlanMode` only, raise and clear the question signal; perch never answers
those two tools' own dialogs. Stop does not fire when you interrupt a turn, so
a `Notification` hook (`idle_prompt`, about a minute after Claude stops) settles
an interrupted turn to idle. Every resolved approval emits one event carrying
`ResolvedReqID`. For opencode, the same lifecycle events
arrive over the `opencode serve` SSE stream: `session.status` carries busy and
idle and the session id used for resume, and `session.error` becomes errored
and stays errored across the idle opencode sends right after it (an Esc abort
or an auto-compacting context overflow is not an error). Frames of subagent
sessions (those with a `parentID`) are ignored, and `permission.replied` clears
the approval signal. The SSE reader skips over-long frames, reconnects a stream
silent for 30 seconds with backoff, and resyncs from `GET /session/status`
after a reconnect. opencode's experimental step frames sit behind
an environment flag perch never sets, so they never fire. For both agents, the
exit sentinel's `AgentExit` ping becomes the `exited` state, distinct from
`errored`, so a graceful `/exit` never reads as a failure. For opencode, a
terminal guard makes this final, so a straggling status frame from the
backgrounded `serve` cannot revive it.

The approval loop above is Claude's alone. opencode's `attach` terminal is an
independent interactive client that runs its own permission prompt in the pane,
and perch has no hook to intercept it. So perch does not answer opencode
approvals: `OpencodeMonitor.Capabilities()` returns `approvals: false`, its
`permission.asked` frame becomes a passive `awaiting-approval` attention signal
with no `Approval` payload, and perch registers no pending approval. The user
decides in opencode's own prompt. `OpencodeMonitor.Approve` remains only to
satisfy the Monitor interface. It is an unreachable no-op, since nothing ever
registers a pending opencode approval to answer.

Claude status reporting needs no manual setup, and perch writes nothing into the
worktree. `Prepare` writes the session's hooks (its listener address and token)
to a private per-session settings file (a `0700` directory under
`$XDG_RUNTIME_DIR`, else the temp directory, and a `0600` file; leftovers of a
crashed perch are swept) and the launch line loads it with `claude --settings <file>`. Claude
merges hook lists across settings sources, so the repository's and the user's
own hooks still run. `Teardown` deletes the file. Two sessions on one cwd each
reach their own listener, a crash leaves no stale hook in the repository, and
`git status` stays clean. A `.claude/settings.json` left in a worktree by an
older perch is deleted once, and only if it holds nothing but perch's own
hooks and git does not track it. opencode reports natively over its SSE stream;
its Basic-auth password reaches the pane as `PERCH_OPENCODE_PASSWORD` and the
launch line exports it, with the pinned username, after the login rc runs.

### States and the attention model

A unified `agent.Event` carries a `Kind` of `state`, `approval`, or `question`,
and for state events a `State`. The seven states:

| State | Meaning |
|-------|---------|
| `running` | The agent is working a turn |
| `idle` | Steady idle, such as at connect or between turns |
| `awaiting-approval` | A tool call needs your verdict; for Claude perch gates it behind the card, for opencode it is a passive signal |
| `awaiting-input` | The agent is asking you a question, distinct from an approval |
| `done` | A turn completed; drives the ambient completion toast |
| `errored` | The agent reported a failure |
| `exited` | The agent process is gone (graceful `/exit` or crash) while its shell lives; reopen from the in-pane overlay |

For Claude, an approval is a request to act. perch gates it behind the approval
card until you decide, because Claude's blocking `PermissionRequest` hook lets
perch answer the prompt Claude would otherwise ask you in the pane. For opencode, `awaiting-approval` is
a signal only: opencode's `attach` terminal owns the permission prompt, so perch
renders no card, registers no pending, and sends no reply. It behaves exactly
like a question in that respect, but keeps its own distinct amber
`awaiting-approval` glance so you can tell an approval apart from a question.

A question is the agent asking you to choose: Claude's `AskUserQuestion`, whose
hooks perch answers with no decision so the agent renders the question in its
own pane through its normal flow,
and opencode's `question.asked`. A question is a signal only. perch renders no
card and sends no reply. You answer in the agent's pane. Claude's `ExitPlanMode`
stays on the normal approval path, so you still review a plan. opencode's
`question.replied` resumes running and `question.rejected` falls back to idle.

The sidebar gives each state a glanceable look, with color, icon, and label
together so it never relies on color alone. The sidebar suppresses pulses under
`prefers-reduced-motion`.

## Worktree lifecycle

`docs/diagrams/worktree-lifecycle.mmd` shows the three paths.

### Create

1. Preflight: resolve `repoPath` under a root, validate the branch name with
   `git.ValidRef`, and confirm the agent name is known.
2. Derive the worktree path from the repository and the slugified branch, then
   re-check that it stays under the roots.
3. Run `git worktree add`. A new-branch session uses `-b` and surfaces
   `ErrBranchExists` if the branch already exists. An existing-branch session
   checks out the branch without `-b`.
4. Record the session. Opening it spawns the pty and starts the Monitor.

### Remove

1. A confirm dialog, then a deferred removal with an undo window.
2. perch tears down the pty bridge and Monitor. The Monitor's `Teardown` deletes
   the session's private hook settings file and closes the listener.
3. perch removes the record. For a worktree session, `git worktree remove`
   deletes the tree; a dirty tree requires `ForceRemoveWorkspace`. An in-repo
   session is record-only, and remove never deletes the branch.

## Security model

### IPC has no listening port

Frontend and backend speak over Wails bindings and events, not a socket. A
release build opens no TCP or Unix socket for IPC. The `//go:build dev` tag
gates the `ws://localhost:34115` hot-reload socket, and release builds strip it.

### The agent hook listener

Each Claude monitor creates its own listener bound to `127.0.0.1` on an ephemeral
port, guarded by a per-listener bearer token of 32 random bytes.

| Constraint | Detail |
|------------|--------|
| Bind address | `127.0.0.1:0`, loopback only |
| Port | Ephemeral, assigned by the kernel per listener |
| Auth | Random bearer token, compared with `subtle.ConstantTimeCompare` |
| Blocking | `PermissionRequest` blocks in the handler until `Decide` supplies a verdict; a client disconnect emits a `PermissionRequestCancelled` event, and shutdown cancels cleanly. Every other event returns at once with no decision |
| Lifetime | One listener per active Claude session; `Teardown` closes it and deletes its hook settings file |

### The env-sync listener for `perch reload`

`internal/envsync` backs `perch reload` and the drawer's reload button
(`App.ReloadAgentEnv`, which just types `perch reload` into the drawer's
bridge, so both share one code path). Unlike the hook listener, this is one
listener for the app's whole run, shared by every workspace. It stands up at
startup and tears down at exit (`app/app.go`, wired alongside the baseline
`os.Environ()` capture).

| Constraint | Detail |
|------------|--------|
| Bind address | `127.0.0.1:0`, loopback only |
| Port | Ephemeral, one listener shared by every workspace |
| Auth | Per-workspace bearer token, minted on first use (`TokenFor`) and compared with `subtle.ConstantTimeCompare`; a token minted for one workspace is rejected for another (`workspace mismatch`, 403) |
| Body | Capped at 1 MiB |
| Storage | `computeDelta` keeps only keys new or changed versus the app's baseline `os.Environ()`, excludes `PERCH_*` and shell-volatile keys (`PWD`, `OLDPWD`, `SHLVL`, `_`, `COLUMNS`, `LINES`), and stores the result in an in-memory-only overlay (`App.envOverlay`), never written to disk or logged. `NewWithDelta` also reports keys the user unset (`Delta.Unset`). `*PATH` values are deduplicated so repeated reloads do not grow them |
| Revocation | `Revoke(workspaceID)` invalidates a workspace's token |
| Relaunch | `onSync` stores the overlay, then dispatches `OpenWorkspace` on its own goroutine, never inline in the handler, so the conversation resumes from its saved session id (claude `--resume`, opencode `--session`) without stalling the request or the event pump |
| Lifetime | One listener for the app's whole run; closed on shutdown |

Between the two listeners above, this is the entire production network
surface.

### Always-allow rules

When you choose "Always," perch stores an `AlwaysRule{agent, tool, pattern,
hash}` in `settings.json`. The `hash` is the SHA-256 of the full tool input
captured when you clicked, canonicalized with the model's free-text keys
removed (`description`, Bash `timeout` and `run_in_background`, WebFetch
`prompt`), so a re-worded description still matches. `pattern` is a truncated copy kept only for display,
not the boundary. A later request auto-approves only when the agent, the tool,
and the input hash all match. A rule with no hash never auto-approves. Hashing
the full input means two calls that share a prefix but differ later cannot
collide, so a rule never grants more than the exact request you approved.

### Input validation and teardown

| Constraint | Detail |
|------------|--------|
| Identifiers | Session and pane ids are checked against an `A-Za-z0-9_-` allowlist |
| Worktree paths | Resolved to absolute paths and verified under the roots |
| Git refs | `git.ValidRef` rejects empty names, leading `-`, `..`, control characters, and other forms `git check-ref-format` forbids |
| Process groups | Closing a pty bridge kills the shell's process group and hangs up the tty, which reaches the agent job (its own process group) while it is in the foreground; the opencode launch line kills its backgrounded `serve` when `attach` exits |

### Repository trust

Opening a repository can run code through two distinct paths, and they differ
in kind. The first is git's own hooks. perch runs `git worktree add` and `git
checkout`, and git may run a repository's configured `.git/hooks`, the same as if
you ran those commands yourself. perch does not disable them, because that would
break workflows like git-lfs and submodules. Those hooks live outside the tree.
Clone, fetch, and push do not transfer them, so exposure comes only from hooks
already present in a local repository.

The second path travels with the tree. An agent reads its configuration from the
worktree on startup: `claude` from `.claude/settings.json`, opencode from its own
config. A repository can commit such a file, and the agent runtime executes its
`PreToolUse` or command hooks as shell the moment a session opens. Perch's
own approval channel is itself a command hook, but it lives in a per-session file
outside the tree, loaded with `--settings`, so perch never touches a repository's
`.claude/settings.json`. The approval gate intercepts the
tool calls an agent routes through the loopback listener. It does not intercept
the configuration's own hooks, which the runtime executes before and outside
that channel. Committed agent-config hooks therefore run ungated. Before opening
an untrusted repository, look at its `.claude` and opencode configuration. Open
repositories you trust.

## Capabilities and degradation

Each Monitor advertises `Caps{approvals, attention}`. The frontend surfaces only
the controls an agent supports. It hides, rather than disables, the surface for
any capability an agent omits. Both monitors advertise `attention`. Only Claude
advertises `approvals`, because only Claude's blocking hook lets perch answer
the agent's permission prompt. opencode returns `approvals: false`,
so the card stays hidden and its own terminal owns the approval.

## Debug aids

`perch debug discover [path]` inspects the discovery pipeline. It is hidden from
the help output and is meant for development.
