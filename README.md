# perch

perch is a desktop GUI for managing AI coding sessions (`claude` and
`opencode`) across git worktrees. `perch` (no arguments) opens a **Wails v2
desktop window** — a Go backend embedded in a WebKit2GTK webview running a
Svelte 5 SPA. The left sidebar lists agent sessions; clicking a session opens a
full terminal (xterm.js) backed by a live tmux pane. There is no terminal TUI,
no persistent tmux frame, and **no listening TCP port** in production —
all IPC travels over the WebKit2GTK script-message channel and the `wails://`
custom asset scheme.

It is a single static binary with no background daemon and no database. Status
is polled from tmux pane options roughly every second, and recovery after a
server restart is a one-shot `perch resurrect`.

---

## Requirements

### Runtime

| Tool | Version |
|------|---------|
| tmux | 3.6 |
| WebKit2GTK + GTK3 | (system libraries — Linux only) |

At least one of **claude** or **opencode** must be installed and on `$PATH`.

Install the system libraries on Debian/Ubuntu:

```sh
sudo apt install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev
```

> **Linux only.** macOS and Windows are not supported because WebKit2GTK is a
> Linux-specific library. A native-toolkit port is a non-goal for v1.

### Build-time (source builds only)

| Tool | Version |
|------|---------|
| Go toolchain | 1.26.2 |
| Node.js | v22 |
| npm | (bundled with Node) |

---

## Install

### Build from source

```sh
git clone https://github.com/Miniature-Pug/perch
cd perch
make gui-build
```

`make gui-build` runs `npm --prefix frontend install && npm --prefix frontend run build`
(builds the Svelte SPA into `frontend/dist/`) and then
`go build -tags production -o bin/perch ./cmd/perch`.

The `-tags production` build tag is **required** — it embeds the compiled
frontend assets into the binary. Omitting it produces a binary that cannot
serve the webview.

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
# Open the GUI; root = current directory
perch

# Same, but with an explicit project root
perch /path/to/projects

# One-time setup: install status hooks for detected agents (claude / opencode)
perch setup
```

`perch` (no arguments) opens the desktop GUI window. The sidebar lists all
agent sessions discovered under the configured `roots`. Click any session to
open an embedded terminal tab. Multiple tabs can be open simultaneously; each
tab streams output from the underlying tmux pane in real time.

---

## Commands

| Command | Purpose |
|---------|---------|
| `perch` | Open the Wails GUI; root = cwd |
| `perch <path>` | Open the Wails GUI; root = given directory |
| `perch setup [--replace]` | Detect installed agents; install status hooks/plugins. `--replace` overwrites stale perch-owned hook entries |
| `perch attach <query>` | Fuzzy-attach the terminal to a live agent session matching the query |
| `perch resurrect` | Reconcile shadow records against live tmux panes (run after a server restart) |
| `perch status set <state>` | Write agent status to the current tmux pane (`working`, `waiting`, or `done`) — used by installed hooks |
| `perch doctor` | Check runtime dependencies and configuration |
| `perch version` | Print version and build info |

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

### No listening port

perch opens **no TCP or Unix socket** in production. The Wails webview
communicates with the Go backend exclusively over the WebKit2GTK
script-message channel; static assets are served via the `wails://` custom
scheme. The `ws://localhost:34115` reload socket is a `//go:build dev` only
artifact and is never compiled into a production binary.

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
as a tmux pane option. The backend polls those options on a ~1 s interval
(configurable via `refresh_ms`) and pushes a `sessions-changed` event to the
GUI sidebar, which re-renders the status glyph.

Glyphs: `🤖` working / `💬` waiting / `✓` done / `●` live (no status set) / `○` idle.

There is no daemon — perch never parses agent internals and does not need a
running background process.

---

## Resurrect

`perch resurrect` runs the `Reconcile` engine, which classifies each shadow
record as:

- **KEEP** — the pane is still live and the boot ID matches: do nothing.
- **PRUNE** — the pane is gone but the boot ID matches (clean shutdown): remove
  the record.
- **RESTORE** — the boot ID differs or the server is cold: re-launch the agent
  in a new window.

---

## Non-goals

- No background daemon or long-running helper
- No SQLite or embedded database (two small JSON stores only)
- No filesystem watchers (no fsnotify in v1)
- No remote, SSH, or multi-machine orchestration
- No CI/CD pipeline integration
- No GitHub or PR integration
- No sandboxing or containers
- No macOS / Windows support (WebKit2GTK is Linux-only)
- No tools beyond claude and opencode (adapter interface keeps the door open)

---

## Diagrams

Architecture, status-sequence, worktree-lifecycle, and
discovery-state diagrams live in [`docs/diagrams/`](docs/diagrams/). They
render in any Mermaid viewer or directly in GitHub (`.mmd` files in fenced
blocks).
