# perch

perch is a keyboard-first Go TUI for discovering, launching, and managing AI
coding sessions (claude and opencode) across git worktrees in tmux. It is a
single static binary with no background daemon and no database — status is
tick-polled from tmux pane options, and recovery is a one-shot `perch resurrect`.

---

## Requirements

| Tool | Version |
|------|---------|
| tmux | 3.6 |
| golang | 1.26.2 (toolchain) |
| claude | 2.1.158 (optional) |
| opencode | 1.15.12 (optional) |

At least one agent (claude and/or opencode) must be installed and on `$PATH`.
git must be available for worktree discovery.

---

## Install

### Build from source

```sh
git clone https://github.com/Miniature-Pug/perch
cd perch
make build
```

Produces `./bin/perch`. The build is hermetic (vendored deps, `-trimpath`).

To install to `GOBIN` / `~/go/bin`:

```sh
make install
```

Both targets inject the version string via ldflags
(`-X main.version=$(git describe --tags --always --dirty)`).

Module path: `github.com/Miniature-Pug/perch`

---

## Quick start

```sh
# Launch TUI; root = current working directory
perch

# Launch TUI; root = path
perch /path/to/projects

# Install agent status hooks/plugins (run once after install)
perch setup
```

---

## Commands

| Command | Purpose |
|---------|---------|
| `perch` | Launch the TUI with root set to cwd |
| `perch [path]` | Launch the TUI with root set to the given directory |
| `perch setup` | Detect installed agents; install status hooks/plugins |
| `perch resurrect` | Reconcile shadow records against live tmux panes (run after reboot) |
| `perch status set <state>` | Write agent status to the current tmux pane (`working`, `waiting`, or `done`) |
| `perch doctor` | Check runtime dependencies and configuration |
| `perch version` | Print version and build info |

---

## Keybindings

### Navigation (list built-ins)

| Key | Action |
|-----|--------|
| `j` / `↓` | Move down |
| `k` / `↑` | Move up |

### Actions

| Key | Action |
|-----|--------|
| `↵` | Switch to selected session |
| `n` | New session |
| `w` | New worktree |
| `x` | Kill session |
| `d` | Remove session record |

### Filter

| Key | Action |
|-----|--------|
| `/` | Filter |
| `esc` | Clear filter |

### Display

| Key | Action |
|-----|--------|
| `z` | Screen mode (forward) |
| `Z` | Screen mode (back) |
| `?` | Toggle help |
| `q` / `ctrl+c` | Quit |

---

## Configuration

Config is optional — perch runs on sensible defaults. Files are TOML. Project
config overrides global per-field.

### Discovery (three-tier)

1. **Global:** `$XDG_CONFIG_HOME/perch/config.toml`
2. **Per-project:** walk up from cwd to the repo root looking for `.perch.toml`
   (linked worktrees fall back to the main worktree root)
3. Project config overrides global per-field; all fields have defaults

### Global config (`config.toml`)

```toml
roots        = ["~/projects"]          # default: launch cwd
sort_order   = ["running", "pinned", "frecency"]
blacklist    = ["**/archive/**"]
refresh_ms   = 1000                    # status tick interval in ms

[default_session]
agent           = "claude"
startup_command = ""

[theme]
accent = "#EE6FF8"
```

### Per-project config (`.perch.toml`)

```toml
base_branch  = "main"
worktree_dir = "../wt"
agent        = "opencode"

[files]
copy    = [".env", ".env.local"]
symlink = ["node_modules"]

post_create = ["direnv allow", "pnpm install"]
pre_remove  = []
```

### Security

Agent binary resolution is **global-only** and cannot be overridden by a
per-project `.perch.toml`. A project config may select which known agent to use
and supply model or prompt options, but it cannot point at an arbitrary
executable. This prevents a malicious repo from hijacking the agent binary
resolved at launch.

---

## Status pipeline

`perch setup` installs hooks into each detected agent: for claude it writes
`~/.claude/settings.json` hooks; for opencode it writes
`~/.config/opencode/plugins/perch-status.ts`. When an agent changes state its
hook calls `perch status set <working|waiting|done>`, which resolves the current
pane from `$TMUX_PANE` and writes `@perch_pane_status` as a tmux pane option.
The TUI tick-polls those options on a low-frequency interval (default 1 s,
configurable via `refresh_ms`). There is no daemon — perch never parses agent
internals and does not need a running background process.

---

## Non-goals

- No background daemon or long-running helper
- No SQLite or embedded database (two small JSON stores only)
- No filesystem watchers (no fsnotify in v1)
- No remote, SSH, or multi-machine orchestration
- No CI/CD pipeline integration
- No GitHub or PR integration
- No sandboxing or containers
- No web UI
- No tools beyond claude and opencode (adapter interface keeps the door open)
