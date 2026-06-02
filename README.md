# perch

perch is a keyboard-first Go TUI for managing AI coding sessions (claude and
opencode) across git worktrees inside tmux. Think of it as a **website in a
terminal**: the session list lives in a persistent left sidebar, and the right
pane shows whichever agent you last opened — switching sessions swaps the agent
into the main pane without re-running it.

It is a single static binary with no background daemon and no database. Status
is tick-polled from tmux pane options, and recovery after a server restart is a
one-shot `perch resurrect`.

---

## Requirements

| Tool | Version |
|------|---------|
| tmux | 3.6 |
| Go toolchain | 1.26.2 (build only) |
| git | any recent version |

At least one of **claude** or **opencode** must be installed and on `$PATH`.

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
# Bootstrap the perch tmux frame + sidebar TUI; root = current directory
perch

# Same, but with an explicit project root
perch /path/to/projects

# One-time setup: install status hooks for detected agents (claude / opencode)
perch setup
```

`perch` creates (or reuses) a tmux session called `perch` containing a sidebar
pane running the TUI and a main pane for the currently selected agent. If tmux
is unavailable or frame setup fails, perch falls back to a direct two-pane TUI.

---

## Commands

| Command | Purpose |
|---------|---------|
| `perch` | Bootstrap the tmux frame + TUI; root = cwd |
| `perch <path>` | Bootstrap the tmux frame + TUI; root = given directory |
| `perch setup [--replace]` | Detect installed agents; install status hooks/plugins. `--replace` overwrites stale perch-owned hook entries |
| `perch attach <query>` | Fuzzy-attach the terminal to a live agent session matching the query |
| `perch resurrect` | Reconcile shadow records against live tmux panes (run after a server restart) |
| `perch status set <state>` | Write agent status to the current tmux pane (`working`, `waiting`, or `done`) — used by installed hooks |
| `perch doctor` | Check runtime dependencies and configuration |
| `perch version` | Print version and build info |

---

## Keybindings

Up/Down navigation is owned by the list — they are not listed here.

| Key | Action |
|-----|--------|
| `/` | Filter sessions |
| `esc` | Clear filter |
| `↵` | Open selected session (swap into main pane) |
| `n` | New session |
| `w` | New worktree |
| `d` | Remove session record |
| `x` | Kill session |
| `z` | Screen mode (forward) |
| `Z` | Screen mode (back) |
| `c` | Collapse sidebar |
| `?` | Toggle help |
| `:` | Open command bar |
| `q` / `ctrl+c` | Quit |

### Layout & focus

The sidebar stays visible at all times. Pressing `↵` swaps the selected agent
into the main pane and moves focus there. To return focus to the sidebar, use
the tmux prefix followed by `←` or `→` (the default-socket model — this is
intentional; perch uses the default tmux socket and relies on standard tmux key
bindings for cross-pane navigation).

---

## Command bar

Press `:` to open the command bar. Supported verbs:

| Verb | Action |
|------|--------|
| `:q` / `:quit` | Quit perch |
| `:new` | New session for the selected project |
| `:attach <query>` | Fuzzy-select and open a live session matching the query |
| `:proj <name>` / `:project <name>` | Jump to the session matching the given name |
| `:setup [--replace]` | Run setup (suspends TUI, reruns agent hook install) |
| `:doctor` | Run doctor (suspends TUI) |
| `:resurrect` | Reconcile shadow records (frame-gated; see note below) |
| `:help` / `:h` / `:?` | Toggle help |

Unknown verbs show a toast error. An empty command bar entry silently cancels.

**Note:** `:resurrect` is refused while inside the perch frame. Run it from a
shell (`perch resurrect`) or let the startup auto-offer handle it after a server
restart.

---

## Configuration

Config is optional — perch runs on sensible defaults. Files are TOML. Config is
discovered in three tiers (global → project walk-up → project wins per field).

### Discovery

1. **Global:** `$XDG_CONFIG_HOME/perch/config.toml` (default `~/.config/perch/config.toml`)
2. **Per-project:** walk up from the launch directory to the nearest `.git`
   boundary, looking for `.perch.toml`. Linked worktrees fall back to the main
   worktree root.
3. Project config overrides global per-field; all fields have defaults.

### Global config (`~/.config/perch/config.toml`)

```toml
roots        = ["~/projects"]       # directories perch scans for git repos (default: launch cwd)
sort_order   = ["running","frecency"] # live sessions first, then frecency; unknown tokens ignored
blacklist    = ["**/archive/**"]    # doublestar globs that hide matching project/tree paths
refresh_ms   = 1000                 # status-tick interval in milliseconds

[default_session]
agent           = "claude"          # default tool for new sessions
startup_command = ""                # command sent to the agent after launch

[theme]
accent = "#EE6FF8"                  # UI accent colour

[agents]
claude   = "/usr/local/bin/claude"  # absolute binary path (global-only security boundary)
opencode = "/usr/local/bin/opencode"
```

### Per-project config (`.perch.toml`)

```toml
base_branch  = "main"
worktree_dir = "../wt"          # relative paths only; absolute paths are rejected
agent        = "opencode"       # overrides [default_session].agent for this project

post_create = ["direnv allow", "pnpm install"]   # hooks run after worktree creation
pre_remove  = ["pnpm run cleanup"]               # hooks run before worktree removal

[files]
copy    = [".env", ".env.local"]    # files copied into each new worktree
symlink = ["node_modules"]          # files symlinked into each new worktree

[[wildcard]]
pattern = "**/experiments/**"   # path-glob rule to auto-pick an agent for matching trees
agent   = "claude"
```

**New-session tool resolution order:**
1. Existing session metadata (the tool already stored for that session)
2. First matching `[[wildcard]]` rule
3. `[default_session].agent` / project `agent`
4. `"claude"` as unconditional fallback

---

## Security / trust model

### Trust prompt for `.perch.toml` hooks

When a repo's `.perch.toml` defines shell hooks (`post_create` or `pre_remove`)
and perch needs to run them, it displays a trust modal:

```
.perch.toml in <dir> defines shell hooks (<phase>). Run them?
(a) trust always  (o) once  (d) deny
```

- `a` — trust always: stores approval keyed on the config path + content hash
  in `<state-dir>/trust.json` (mode 0600); re-prompts if the file changes.
- `o` — once: runs the hooks this time only.
- `d` — deny: skips the hooks.

The file is re-hashed immediately before hook execution to close the TOCTOU
window.

### Global-only agent binary boundary

`[agents]` binary paths and `startup_command` come **only** from the global
config. A project `.perch.toml` has no `[agents]` field — the struct
intentionally lacks it, so any such key in a project file is silently dropped by
the TOML decoder. A malicious repo cannot point perch at an arbitrary binary.

`startup_command` is trusted without a prompt because it comes from your own
global config, not from a project file.

### Relative-only project `worktree_dir`

Project `.perch.toml` may only set a relative `worktree_dir`. Absolute paths
are accepted only from the global config.

See [docs/security-audit.md](docs/security-audit.md) and
[ARCHITECTURE.md](ARCHITECTURE.md) for the full security model.

---

## Status pipeline

`perch setup` installs hooks into each detected agent:

- **claude:** writes `~/.claude/settings.json` hooks
- **opencode:** writes `~/.config/opencode/plugins/perch-status.ts`

When an agent changes state, its hook calls `perch status set <working|waiting|done>`,
which resolves the current pane from `$TMUX_PANE` and writes `@perch_pane_status`
as a tmux pane option. The TUI polls those options on a low-frequency interval
(default 1 s, configurable via `refresh_ms`) and updates the list glyph accordingly.

Focusing an agent (swap-in) automatically clears its status badge.

There is no daemon — perch never parses agent internals and does not need a
running background process.

---

## Resurrect

`perch resurrect` (and the bootstrap auto-offer after a detected server restart)
runs the `Reconcile` engine, which classifies each shadow record as:

- **KEEP** — the pane is still live and the boot ID matches: do nothing.
- **PRUNE** — the pane is gone but the boot ID matches (clean shutdown): remove
  the record.
- **RESTORE** — the boot ID differs or the server is cold: re-launch the agent
  in a new window.

The auto-offer runs before the perch frame is created, prompts only on a real
tty (defaults to `N`), and reports a summary of restored/pruned/kept sessions.

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

---

## Diagrams

Architecture, frame-swap, status-sequence, worktree-lifecycle, and
discovery-state diagrams live in [`docs/diagrams/`](docs/diagrams/). They
render in any Mermaid viewer or directly in GitHub (`.mmd` files in fenced
blocks).
