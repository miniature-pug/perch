# perch

Perch gives every coding agent its own branch, its own worktree, and its own
terminal, then gathers them into one window where you can watch the work and
step in when it matters.

It is a desktop cockpit for the `claude` and `opencode` coding agents. Each
session is a real git worktree with a live terminal, a diff view with
hunk-level staging, a file tree, and inline approval of the tool calls an agent
wants to run. You can drive several agents at once and move between them without
losing your place.

Perch is a single static binary. There is no tmux, no daemon, and no background
server. The Svelte frontend talks to the Go backend through Wails bindings and
events rather than HTTP, so a release build opens no network port of its own.
The one local listener is a short-lived, loopback, token-gated channel for each
active Claude session, covered under [Security](#security).

Perch runs on Linux. It depends on WebKit2GTK and GTK3, which are Linux
libraries, so macOS and Windows are out of scope for now.

## What you get

- **A worktree per session.** Every session is a `git worktree` on its own
  branch. Agents work in isolation and never disturb your main checkout. You
  can also run a session in the repo itself when isolation is not what you want.
- **A live terminal per pane.** Each terminal is xterm.js over a direct
  pseudo-terminal the Go backend spawns. The agent runs inside it. No
  multiplexer sits in between.
- **Glanceable status.** The sidebar tells you, at a glance, which agent is
  working, which is done, which hit an error, and which is waiting on you.
- **Inline approvals.** When an agent asks to run a tool, perch blocks it and
  shows you the request. You allow it once, allow it always, or deny it.
- **A diff you can act on.** Stage or discard individual hunks, or send a hunk
  back to the agent, without leaving the window.
- **Desktop notifications.** When the window is in the background and an agent
  needs you or fails, your desktop tells you.

Perch is the cockpit around the agents, not a replacement for them. The agents
stay the intelligence and your editor stays your editor.

## Requirements

| Need | Detail |
|------|--------|
| Operating system | Linux |
| System libraries | WebKit2GTK 4.1 and GTK3 |
| git | any recent version |
| An agent | `claude` or `opencode` on your `PATH` (at least one) |

Install the system libraries on Debian or Ubuntu:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

`libwebkit2gtk-4.1-dev` pulls in `libsoup-3.0-dev`. WebKit2GTK 4.0 is end of
life, so perch links 4.1.

## Install and run

```sh
git clone https://github.com/Miniature-Pug/perch
cd perch
make gui-build
./bin/perch
```

`make gui-build` installs the frontend dependencies, builds the Svelte SPA into
`frontend/dist/`, and compiles the binary with `go build -tags "production
webkit2_41"`. The `webkit2_41` tag links WebKit2GTK 4.1; without it the build
falls back to 4.0, which is end of life. The `production` tag selects the Wails
production runtime, which omits the dev reload server. The frontend is embedded
from `frontend/dist/`, so `make gui-build` rebuilds it first. A plain `make
build` skips that rebuild and embeds the committed placeholder, so prefer `make
gui-build` for a binary you mean to run. The `wails` CLI is not used; see
[CONTRIBUTING.md](CONTRIBUTING.md).

To install into your `GOBIN`:

```sh
make install
```

Building from source needs the Go toolchain (`go1.26.5`) and Node.js
(`22.22.3`). The exact pins live in `.tool-versions`. Module path:
`github.com/Miniature-Pug/perch`.

## Quick start

```sh
perch                 # open the cockpit; the project root is the current directory
perch /path/to/code   # open the cockpit with an explicit project root
```

Perch opens the desktop window. The sidebar lists the sessions in your registry.
Create one to spin up a worktree and an agent, then select it to open its
terminal. For the full walkthrough of sessions, approvals, diffs, themes, and
keyboard control, read the [usage guide](docs/usage.md).

## Commands

| Command | What it does |
|---------|--------------|
| `perch` | Open the cockpit; project root is the current directory |
| `perch <path>` | Open the cockpit; project root is the given directory |
| `perch attach <query>` | Focus the running window on the session matching the query, or launch the cockpit if none is running |
| `perch doctor` | Check that dependencies and configuration are in order |
| `perch version` | Print version and build information |

`perch attach` relies on a single-instance lock. If perch is already running,
the query goes to that window, which raises itself and selects the best match
by worktree path, then by a case-insensitive match on path, title, or branch.
The forwarding process exits afterward, with a non-zero status on Linux, which
is expected.

## Agents

Each agent plugs in behind a Monitor seam in `internal/agent`. Two integrations
ship today.

- **claude** reports through hooks. When you open a Claude session, perch writes
  a hook configuration with a listener URL and a bearer token into the
  worktree's `.claude/settings.json`. Claude then posts tool and lifecycle
  events back, and a `PreToolUse` event blocks until you approve.
- **opencode** reports through its own loopback HTTP server. Perch launches
  `opencode serve`, then reads the Server-Sent-Events stream to follow
  lifecycle, approval, and question events.

The cockpit shows only what an agent supports. Each agent advertises a small
capability set (approvals and attention), and any surface an agent does not
support stays hidden rather than showing a dead control. Perch does not meter
tokens or cost; that surface does not exist.

## Configuration

Perch keeps its state under `~/.config/perch` (or `$XDG_CONFIG_HOME/perch`):

| File | Holds |
|------|-------|
| `workspaces.json` | The session registry |
| `settings.json` | Theme, density, font, notification, and always-allow settings |
| `layout.json` | Saved window layout |
| `config.toml` | The project `roots` perch scans for repositories |

With no `config.toml`, the launch directory is the only root. Perch reads no
project-local config, so opening a repository cannot change how perch behaves.

## Security

Perch is built to be safe to point at a repository, with three deliberate
boundaries.

- **No IPC port.** The frontend and backend speak over Wails bindings and
  events, not a socket. A release build opens no TCP or Unix socket for IPC.
  Every argument that crosses the boundary is validated in the backend: session
  and pane identifiers against a strict charset, and worktree paths against the
  configured roots.
- **One small agent listener.** Each Claude session gets its own listener bound
  to `127.0.0.1` on an ephemeral port, guarded by a random per-listener bearer
  token compared in constant time. It receives Claude's hook posts and blocks
  `PreToolUse` until you decide. perch tears it down when the session closes.
  This is the entire production network surface.
- **Exact-match always-allow.** Choosing "Always" on an approval stores a rule
  keyed on the agent, the tool, and a SHA-256 hash of the full tool input. A
  later call auto-approves only when all three match, so a rule can never grant
  more than the exact request you approved. Rules are listed and revocable in
  Settings.

One caveat is yours to own: perch runs `git worktree add` and `git checkout`,
and git may run a repository's existing hooks, exactly as it would if you ran
those commands yourself. Open repositories you trust. Cloned and fetched
repositories do not carry their author's hooks, so the exposure is only from
hooks already present in a local repository.

The full system-level treatment is in [ARCHITECTURE.md](ARCHITECTURE.md).

## Documentation

| Document | For |
|----------|-----|
| [Usage guide](docs/usage.md) | Driving the cockpit day to day |
| [Architecture](ARCHITECTURE.md) | How perch is built |
| [Contributing](CONTRIBUTING.md) | Toolchain, build, and the test workflow |
| [Container framework](containers/README.md) | The one image every check runs in |
| [Backend packages](internal/README.md) | A map of `internal/` |
| [Frontend](frontend/README.md) | A map of the Svelte SPA |
| [Diagrams](docs/diagrams/README.md) | Architecture and flow diagrams |
| [Documentation map](docs/README.md) | Index of everything above |
| [Changelog](CHANGELOG.md) | Release history |

## Non-goals

Perch will not be a tmux replacement, a daemon, a database-backed app, a remote
or multi-machine orchestrator, or a CI integration. It does not sandbox the
repositories you open. It does not target macOS or Windows. It integrates
`claude` and `opencode`, and the Monitor seam leaves room for more.

## License

[MIT](LICENSE). Copyright 2026 Manjot Singh Randhawa.
