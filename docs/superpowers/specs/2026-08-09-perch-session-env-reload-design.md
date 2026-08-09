# Session environment reload

Date: 2026-08-09
Branch: feat/perch-v1
Status: Approved in conversation, implementation pending

## Problem

Credentials and environment change mid session. AWS SSO tokens expire, a new
AWS_PROFILE or API token is needed, a fresh variable has to reach the coding
agent. Today the only way to give a running agent a new environment variable is
to exit the agent, set the variable, and relaunch by hand. That breaks flow, and
it feels like walking out of the tool to a second terminal and back in.

## The constraint that shapes everything

A process reads its environment once, at exec time. A parent cannot push a new
variable into an already running child, and a child never sees variables the
parent exported after the child started. This is the Linux process model, not a
perch limitation, confirmed against man execve(2), proc_pid_environ(5), and
ptrace(2). No terminal topology changes it: making the bottom terminal the
agent's parent shell does not help, because a running child is sealed off from
the parent's later exports.

Two consequences split the feature cleanly.

- File based changes already reach a running agent. `aws sso login` writes a
  token cache under `~/.aws/sso/cache`, and the AWS SDK rereads that file on the
  next call, so the agent picks up refreshed credentials with no restart. Most
  day to day credential pain lands here and needs no code, only a hint so the
  user stops restarting out of habit.
- Environment variable changes, such as exported credentials from aws-vault or
  granted, or any new variable the agent reads at startup, can only reach the
  agent through a fresh exec. perch already has a lossless way to do that.
  OpenWorkspace tears down the agent pane and relaunches with `claude --resume
  <id>` or `opencode attach --session <id>`, and the official docs confirm
  `--resume` restores the complete conversation history including tool calls. So
  a relaunch preserves the thread.

The feature is therefore a thin, explicit path: take the environment the user set
in the session terminal, and relaunch the agent with it, preserving the
conversation.

## Scope

In scope.

- A `perch reload` command the user runs in the session terminal.
- A button in the per session terminal drawer that runs the same thing.
- Capture of the drawer shell environment, applied to a conversation preserving
  agent relaunch.
- A one line hint that file based credentials need no reload.
- Captured values held in memory only, never written to disk.

Out of scope, and deliberately so.

- No automatic trigger. Never watch for auth failures and surface this on its
  own. Explicit user action only. This is a firm decision.
- No keybinding.
- No persisted overlay, no global overlay, no environment editor UI.
- Removing a variable the user unset in the drawer is not handled. The capture
  adds and overrides, it does not delete a baseline variable.

## Mechanism

Objects involved: the per workspace terminal drawer, paneID `shell-{workspaceID}`
(frontend/src/App.svelte:1462, kept mounted across switches), the agent pane and
its monitor (app/app.go), and a new app owned loopback endpoint for environment
sync.

1. App owned env sync endpoint. On startup the app stands up one loopback
   listener dedicated to environment sync. It reuses the security pattern of
   internal/hooklistener, loopback only on 127.0.0.1, Bearer token compared with
   subtle.ConstantTimeCompare, body capped at 1 MiB, but it is a small dedicated
   handler rather than the agent hook listener, so the environment payload never
   has to be threaded through agent.Event. Prefer a new internal/envsync package
   for isolation and unit testing. The endpoint holds a map from a per session
   token to a workspace id.

2. Token injection into the drawer. When OpenShell opens a per workspace drawer,
   `shell-{workspaceID}`, the app mints or looks up that workspace's env sync
   token and injects three variables into the drawer environment: PERCH_ENVSYNC_URL,
   PERCH_ENVSYNC_TOKEN, PERCH_ENVSYNC_WS. The home drawer `shell-home`
   (app/app.go:54) has no workspace and receives none. OpenShell today passes nil
   env (app/app.go:1329 to 1331), so this is a new drawer specific env slice.

3. The `perch reload` command. A new dispatch case in cmd/perch/main.go. It reads
   its own os.Environ, which is the drawer shell's current environment including
   anything the user just exported, reads PERCH_ENVSYNC_URL, PERCH_ENVSYNC_TOKEN,
   PERCH_ENVSYNC_WS from its environment, and POSTs the environment plus the
   workspace id to the endpoint with the Bearer token. It prints a short
   confirmation and exits, and never launches the GUI. If the PERCH_ENVSYNC_*
   variables are absent, the command was not run inside a perch session terminal,
   so it prints a friendly error and exits non zero. A test asserts `reload` is a
   real command, mirroring the existing not a command tests for setup, status,
   resurrect in cmd/perch/main_test.go.

4. Delta and overlay. On receipt the app authenticates the token, maps it to the
   workspace id, and computes a delta against the baseline environment captured at
   app start, os.Environ. The delta is every key that is new or whose value
   differs, excluding PERCH_* internal variables. The delta is stored as an in
   memory overlay, `map[string][]string` keyed by workspace id, guarded by the app
   mutex. It is never persisted. Environment values are never logged.

5. Conversation preserving relaunch. After responding to the POST, the app
   triggers the relaunch asynchronously, `go func(){ a.OpenWorkspace(workspaceID)
   }()`, never inline in the request handler, so the endpoint is not torn down mid
   request and the event pump is not stalled (pump at app/app.go:795, OpenWorkspace
   takes a.mu around :741 and :746). OpenWorkspace already reuses LastSessionID to
   resume, so the conversation continues.

6. Applying the overlay at spawn. Both the agent pane (app/app.go:683) and the per
   workspace drawer (app/app.go:1331) build their environment through a new
   mergeEnv helper, mergeEnv(os.Environ, monitor PaneEnv, overlay for this
   workspace). mergeEnv deduplicates by key so the overlay overrides a base value
   of the same key, while the exit sentinel and env sync variables are preserved.
   A unit test covers precedence and dedup. Because the overlay lives in memory, a
   normal reopen of the session during the same perch run also carries it, and a
   perch restart clears it, rerun `perch reload` if needed.

7. The button. In the per workspace drawer chrome, frontend/src/lib/ShellDrawer.svelte,
   a single labeled control, an icon button that reads roughly env to agent. It
   calls a new bound App method, ReloadAgentEnv(paneID), which resolves the
   workspace id from the paneID and writes `perch reload\n` into that drawer's
   bridge, a.getBridge(paneID). The button is a convenience trigger for the same
   command, so there is one code path. The button is absent on the home drawer.

8. The hint. A concise inline hint near the button, shown as help or tooltip text.
   File based credentials such as AWS SSO refresh on the agent's next call with no
   reload, use this only for new or changed variables.

## Security and threat model

- Transport is loopback only on 127.0.0.1, with a per session Bearer token of 32
  crypto random bytes, compared in constant time, body capped at 1 MiB. This
  mirrors the existing exit sentinel and hook listener pattern in
  internal/hooklistener/listener.go.
- Only a process that holds the token can post. The token lives in the drawer
  shell environment, so the drawer and its children can sync, which is the
  intended trust boundary, the user's own shell, the same boundary as the
  existing PERCH_EXIT_TOKEN.
- A per session token maps to exactly one workspace, so a drawer for session A
  cannot request a relaunch of session B.
- The environment payload can contain secrets. It crosses only loopback, is held
  in memory only, and is never written to disk or logs. Persisting an expiring
  secret would be a leak with no benefit, which is why nothing is persisted.

## Testing

TDD throughout, and the container gate `make test-all` is ground truth.
Adversarial verification, a separate pass that runs the full gate and tries to
break each piece, is required before the work is called done, per the established
two stage pattern.

Go unit tests.

- mergeEnv precedence and dedup: overlay overrides base, sentinel preserved,
  PERCH_* excluded from the overlay.
- delta computation: new key captured, changed value captured, unchanged key
  ignored, PERCH_* excluded.
- env sync endpoint authentication: valid token accepted, wrong or missing token
  rejected, oversized body rejected, token maps to the right workspace, a cross
  workspace token is rejected.
- `perch reload` command: it is a real command, it errors cleanly when the
  PERCH_ENVSYNC_* variables are absent, and it posts the expected body when they
  are present, tested against a local test server.
- the relaunch is dispatched asynchronously and OpenWorkspace is invoked with the
  right workspace id, asserted through the existing spawn and bridge test seams.
- the overlay is applied at both the OpenWorkspace and OpenShell spawns.

Frontend tests, vitest.

- the button renders on a per workspace drawer and is absent on the home drawer.
- clicking it calls ReloadAgentEnv with the correct paneID.
- the hint text is present.

Manual smoke, real WebKit and a real agent, which the mock gate cannot exercise,
added to docs/smoke-checklist.md.

- run `aws sso login` in the session terminal, confirm the running agent uses
  fresh credentials on its next call with no reload.
- export a new variable, click the button, confirm the agent relaunches, the
  conversation is intact, and the agent sees the new variable.
- run `perch reload` by hand, confirm the same.
- confirm the terminal returns to a clean state after the relaunch.

## Files touched, anticipated

- cmd/perch/main.go and main_test.go: the `reload` subcommand.
- internal/envsync, new: the loopback endpoint, token map, delta, unit tests.
- app/app.go: baseline capture, overlay map, mergeEnv, wiring the endpoint, token
  injection at OpenShell, the async relaunch, the ReloadAgentEnv bound method.
- frontend/src/lib/ShellDrawer.svelte: the button and the hint.
- frontend/src/lib/wails.ts: the ReloadAgentEnv binding.
- docs/smoke-checklist.md and the README or usage doc: document `perch reload` and
  the file credential hint.
