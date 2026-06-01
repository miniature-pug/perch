# perch — design & implementation plan

> A keyboard-first TUI that orchestrates **Claude Code** and **opencode** sessions
> across git repos and worktrees, using **tmux** as the engine. perch is a
> *controller and UI on top of tmux* — it never reimplements a terminal or a
> multiplexer.

Status: **design locked, ready to implement.** This document is the handoff spec
for the implementing agent. Read it top to bottom before writing code. Patterns
were studied from existing tools (workmux, sesh, k9s, lazygit, claude-squad,
zoxide, tmux-sessionizer, the Charm libraries, and opencode itself); provenance is
credited inline so you can go read the original when a detail is ambiguous.

---

## 1. Problem & goals

Today, managing several AI coding sessions means juggling terminal windows: one
per agent, per project, with no single place to see what is running, what is
waiting on you, or what is done. Switching context is manual; isolating parallel
work on different branches is manual.

**perch** gives you one place to:

1. **Launch / resume** a Claude Code or opencode session in any project, from one
   screen, landing in a live shell where the agent runs.
2. **Isolate** parallel work via git worktrees — optionally, only when you want it.
3. **See and manage everything from one window** (the "admin"): every session,
   its project/tree, and its live status (working / waiting-on-you / done).

### Non-negotiable qualities
- **Lightweight**: a single small binary, instant launch, low overhead. No DB, no
  background daemon, no long-running helper process.
- **Beautiful, modern UX**: keyboard-first, fuzzy filtering, live status, one
  cohesive theme, graceful empty/error/small-screen states.
- **Smooth & efficient — and efficiency wins ties.** Aim for beautiful, but when
  polish and performance conflict, **performance wins** (drop an animation, a
  gradient, a live preview frame before you drop responsiveness). Target instant
  first paint (≈<100 ms) and a render loop that **never blocks** — every shell-out
  (git/tmux/agent) runs as an async `tea.Cmd`, never inline in `Update`/`View`.
  Batch tmux queries (one `list-panes -a`, not N), cache the repo scan, debounce
  the filter (~100 ms), keep the status tick low-frequency (~1 s), and capture a
  pane preview only for the highlighted running session. No jank, no decorative
  effect on a hot path, no spinner that hides a blocked UI.
- **Platform-agnostic**: identical behaviour on Linux and macOS (Go, cross-compiled).
- **Agent-agnostic by design**: Claude + opencode in v1, but built on an adapter
  interface so other tools can be added without touching the core.
- **Small, contained blast radius**: prefer mature, proven dependencies over new
  ones; keep the dependency tree small; and **isolate every fragile or undocumented
  integration behind an interface** (adapters, the git/tmux wrappers) so that when
  one breaks — a CLI changes flags, an internal file format drifts, a plugin API
  shifts — the failure is contained to that one feature and degrades gracefully,
  never crashing perch. This is *not* "no libraries"; it is "minimise the surface
  that can hurt us." It drives the Charm-v1 choice (§2), the defensive-parsing rule
  (§4), and the no-daemon stance (§17).

---

## 2. Architecture overview

perch is a **join over three systems that already persist their own state** — it
adds the minimum state of its own.

| Owns… | System | perch reads via |
|-------|--------|-----------------|
| Trees (working dirs / branches) | **git** | `git worktree list`, repo scan |
| Sessions (conversations, each tagged with a directory) | **the agent tools** | adapters (below) |
| Live windows (running processes) | **tmux** | `tmux list-*`, pane options |

The binding none of those three stores — "which live tmux window runs which agent
session" — is **stamped onto the tmux pane as an option** (`@perch_session`,
`@perch_pane_status`); those options are the *live source of truth*. Because tmux
options die when the tmux server dies (e.g. a reboot), perch also keeps a **persisted
shadow** of that binding on disk, used **only** to rebuild windows on `perch
resurrect` (§7). perch's two on-disk stores are described in §6 — both are plain
JSON, no database.

```
                ┌──────────────────────────────────────────┐
                │                perch (Go)                  │
                │                                            │
   you ───────► │   bubbletea TUI  ── one screen ──┐         │
                │   (picker + admin unified)        │        │
                │                                   ▼        │
                │   core model ◄── discover (scan repos)     │
                │      │       ◄── git    (worktrees)         │
                │      │       ◄── agent  (adapters)          │
                │      ▼                                      │
                │   tmux control ── create/switch windows,    │
                │                   set @perch_* options      │
                └───────┬──────────────────┬─────────────────┘
                        │                  │
                        ▼                  ▼
                     tmux  ──────────►  windows running
                                        `claude` / `opencode`
                                        in a tree (directory)
```

### Tech stack (Go module dependencies; versions below were verified against the Go module proxy on the plan's authoring date and satisfy the ≥30-day-old, stable policy; the implementing agent must re-verify and update to the latest qualifying versions at implementation time — §2 policy, §21.2 `.tool-versions`)
- **Go** 1.24+ (the stable Charm v1 line requires it — `bubbles v1.0.0` declares
  `go 1.24.2`). Pin the **toolchain** exactly via the `toolchain` directive in go.mod.
- **CLI**: the **standard library `flag`** package — no dependency. perch's command
  surface is 7 tiny verbs (§10); stdlib `flag` + a `switch` on the subcommand covers
  it with zero added supply-chain surface. (cobra was considered and dropped — it
  adds `spf13/pflag` for no benefit at this scale. If the CLI ever grows complex,
  cobra v1.10.2 is the documented escape hatch.)
- **TUI**: the **stable Charm v1 line** (mature, the most-documented combo, what
  claude-squad and most production Charm apps ship). These three versions are
  **mutually pinned** — `bubbles v1.0.0`'s own go.mod requires exactly the other two:
  - `github.com/charmbracelet/bubbletea` **v1.3.10** (released 2025-09-17)
  - `github.com/charmbracelet/lipgloss` **v1.1.0** (released 2025-03-12)
  - `github.com/charmbracelet/bubbles` **v1.0.0** (released 2026-02-09)
- **Config**: `github.com/BurntSushi/toml` **v1.6.0** (released 2025-12-18).
- **Runtime deps** (external CLIs, not bundled): `tmux`, `git`, and at least one of
  `claude` / `opencode`. `perch doctor` checks them.

#### Dependency & supply-chain policy (deps are an attack vector — treat them as one)
- **Exact-pin everything pinnable.** Every direct dependency is a single exact
  version in `go.mod` — no `^`, `~`, `>=`, or ranges. The **`toolchain`** directive
  pins the Go compiler. `go.sum` is committed and locks **every transitive module by
  cryptographic hash** — a tampered or swapped dependency fails the build.
- **Minimum age ≥ 30 days, active, stable.** Never adopt a version younger than 30
  days (supply-chain attacks surface fast and get yanked). Require the project to be
  actively maintained and the version a stable (non-pre-release) tag. The five deps
  above all satisfy this as of 2026-05-29.
- **Vendor all dependencies** (`go mod vendor`, commit `/vendor`) and build with
  `-mod=vendor` (set in `GOFLAGS`). The build is then hermetic and reproducible — no
  network fetch at build time, and every byte of every dependency is in-tree and
  auditable in review.
- **Scan before release.** `make vulncheck` runs `govulncheck` (pinned via
  `go run golang.org/x/vuln/cmd/govulncheck@<pinned>`); `make verify` runs
  `go mod verify`. Dev tools (linter, vulncheck) are themselves invoked at pinned
  versions via `go run tool@version`, so even tooling isn't floating.
- **No `curl | bash`.** Distribute via `go install …@<pinned tag>` or checksummed,
  signed release binaries. The only first-party non-Go artifact perch ships —
  `perch-status.ts` for opencode — has **zero npm dependencies** (it imports only
  opencode's own plugin types); it is plain code, not a package install.
- **Smallest tree that does the job.** Fewer deps = smaller attack surface (this is
  why the CLI uses stdlib `flag`). Justify every new dependency against this policy
  before adding it.

> **Stable over new — chosen deliberately.** The Charm **v2** line exists and is
> technically stable (`charm.land/bubbletea/v2` v2.0.6, lipgloss v2.0.3, bubbles
> v2.1.0) with slightly cleaner APIs (`LightDark`, `View() tea.View`). We
> **decline it for v1** because it is only weeks old: thin docs, few real-world
> examples, and an unproven track record — exactly the blast-radius perch's goals
> (below) tell us to avoid. The v1 line is battle-tested across years and hundreds
> of apps. perch needs nothing v2-only. v2 is a *future migration*, not a v1
> dependency. (§12 carries the v1 API surface inline so the implementer needn't
> hunt; the v1↔v2 deltas are noted there only so a future migration is cheap.)

---

## 3. Core model

Three nouns. Keep this vocabulary exact in code and UI — do **not** conflate
"directory" with "worktree".

- **Project** — a git repository discovered under the root (§5).
- **Tree** — a working directory perch can run a session in:
  - for a git project: its main checkout **plus** any worktrees (enumerated via
    `git worktree list`);
  - for a non-git path opened manually: just that directory (no worktree concept).
- **Session** — a Claude Code or opencode conversation. Every session is **already
  bound to a directory** by the tool itself; perch reads that binding, it does not
  invent it.
- **Window** — a live tmux window running one session in one tree. Its status icon
  comes from the tool's status hook (§9). Its persisted shadow record (§6) is what
  makes it recoverable after a tmux server restart.

### tmux layout (decided)
- **One tmux session per project.** Session name = slugified project name.
- **One window per running agent session**, inside that project's tmux session.
  Window name = slugified tree/branch; the agent session id is stored as the pane
  option `@perch_session`.
- perch's own TUI runs anywhere (inside or outside tmux); it drives tmux via
  `switch-client` (when inside) or `attach` (when outside). It does **not** require
  you to pre-start a tmux server — creating the first session bootstraps it.

**tmux conventions adopted from workmux / sesh / tmux-sessionizer:**
- Target windows/sessions with the exact-match prefix `=` (`tmux select-window -t
  '=<name>'`) so a name is never matched as a prefix of another. Window targeting
  requires anchoring `=` on **both** parts: `=session:=window` — a session-only
  `=name` is not sufficient to address a window deterministically (E4, M4 evidence).
- The canonical "connect" sequence (sesh `connector/tmux.go`, tmux-sessionizer):
  `has-session -t=<name>` → if absent `new-session -d -s <name> -n <window> -c <dir>
  -P -F '#{pane_id}'` (captures the new pane id from stdout) → else `new-window -t
  '=<session>' -n <window> -c <dir> -P -F '#{pane_id}'` → launch the agent via
  `send-keys -t <tgt> -l <literal>` then a **separate** `send-keys -t <tgt> Enter`
  keystroke (a bare or trailing `;` without `-l` makes tmux parse the rest as a tmux
  command chain — E6) → then `switch-client -t <name>` when `$TMUX` is set, else
  `attach-session -t <name>`.
- Cold-start (`has-session` / `list-sessions` against an absent server) exits 1 —
  classify by **exit code**, not stderr text. Two distinct stderr strings exist:
  `"error connecting to <socket>…"` when the socket file is absent, and `"no server
  running on <socket>"` after `kill-server`. perch treats exit ≥ 1 from `has-session`
  as "absent/cold" (E1/E2, M4 evidence).
- Derive a session/window display name as `<repoBasename>/<worktreeName>`
  (sesh `namer/git.go`) — slashes are valid in tmux names and read naturally.
  Name derivation **must sanitize**: tmux silently rewrites `.` and `:` to `_` in
  session names (invisible collisions — `a.b` and `a:b` both become `a_b`), and `.`/`:`
  in window names make them untargetable (`.` is the pane-index separator, `:` is the
  `session:window` separator). Safe character set = `[A-Za-z0-9_/-]`; map all other
  characters (including `.` and `:`) to `-` (E5, M4 evidence).
- Live-agent detection keys on `#{pane_current_command}` (changes `bash` → `claude` /
  `node` / `bun` when an agent starts), **not** `#{pane_pid}` (always the shell pid).
  `#{pane_dead}` is only meaningful with `remain-on-exit on` (E7, M4 evidence).
- Read pane paths from `list-panes` (`#{pane_current_path}`), not `list-sessions`.
  `#{pane_current_path}` in a `list-sessions` format string returns the querying
  process's cwd, not the session's start directory; use `#{session_path}` in
  `list-sessions` and `#{pane_current_path}` in `list-panes` (E11, M4 evidence).

---

## 4. Agent adapters

The contract that makes perch tool-agnostic. v1 ships `claude` and `opencode`.
Adding a tool = implement this interface; the core never changes.

**Defensive by contract (blast-radius rule, §1).** Adapters touch the most fragile
surfaces perch has — undocumented files, internal formats, plugin APIs. Therefore:
prefer a tool's **documented CLI** over parsing its internal files; when parsing is
unavoidable (claude has no list CLI), make it **fault-tolerant** — skip unparseable
records, never panic, and surface a partial list rather than failing. An adapter
that errors degrades to "that tool unavailable," never takes down perch.

```go
// internal/agent/adapter.go
type Adapter interface {
    Name() string                                   // "claude" | "opencode"
    Detect() bool                                   // CLI present on PATH?
    ListSessions(ctx context.Context) ([]Session, error)
    ResumeArgs(sessionID string) []string                    // resume in cwd
    ForkInto(sessionID, targetDir string) ([]string, error)  // prep fork into targetDir, return launch args (§7); err if unsupported
    NewArgs(opts NewOpts) []string                           // start fresh
    InstallStatusHook() error                       // wire status reporting (perch setup)
    // ReadyHeuristic / TrustPrompt are adapter data, not core logic — see note.
}

type Session struct {
    ID        string
    Tool      string
    Title     string
    Directory string    // the dir the session is bound to — the join key
    Updated   time.Time
}
```

> **Adapter, not hardcoded switch (lesson from claude-squad).** claude-squad has
> *no* adapter interface — it carries three bare string constants
> (`ProgramClaude/Aider/Gemini`) and branches on them inside its tmux layer for
> two things: dismissing the agent's trust/permission prompt, and detecting the
> agent's "ready for input" state. perch lifts those two behaviours into the
> `Adapter` as **data** (a ready-state heuristic and the trust-prompt
> text/keystrokes), so a new agent never requires touching the tmux package.

### Claude adapter (`internal/agent/claude.go`) — verified (2.1.158)
- **List**: no native list CLI. Enumerate `<claudeHome>/projects/<dir-slug>/*.jsonl`
  (top-level only; never descend into the sibling `<id>/` data dirs). The session
  **id is the filename stem**; use file mtime for `Updated`. **Title = the last
  `ai-title` record's `aiTitle` field**, falling back to the first non-meta user
  message (no `summary` record type exists; first/last line are unreliable).
  **Directory** precedence: the in-transcript `cwd` field → pid-tracker `cwd` →
  slug-decode (dashes↔slashes is lossy, so decode greedily stat-guided). Read
  `<claudeHome>/sessions/<pid>.json` for the pid-tracker `cwd` (undocumented,
  version-fragile, defensive). `claudeHome` = `$CLAUDE_CONFIG_DIR` else `~/.claude`.
- **Resume**: `claude --resume <id>` (works from any cwd).
- **Fork into a worktree**: `claude --resume <id> --fork-session`, launched with the
  target worktree as cwd — claude forks **natively**, no `.jsonl`/data-dir copy.
  (Corrected at M3; supersedes the earlier copy-then-resume approach.)
- **New**: `claude` (optionally `--model`, `--session-id <uuid>`, prompt positional).

### opencode adapter (`internal/agent/opencode.go`) — verified (v1.15.12, npm)
- **List**: `opencode session list --format json` → array of
  `{id, title, directory, created, updated, projectId}` (confirmed empirically on
  v1.15.12: flat camelCase fields, `created`/`updated` are **unix-ms numbers**,
  `id` is `ses_`-prefixed). There is **no `--roots` flag** (the assumed
  `{roots:true}` does not exist). Empty scope prints **zero bytes, not `[]`** —
  treat empty/whitespace and `[]` alike as zero sessions. The list is
  **project-scoped** (project = the registered worktree root that cwd resolves to),
  **not global**: run `session list` with the target dir as cwd (via
  `proc.RunInDir`) to scope it. Group by **`directory`** (the per-session field).
- **Resume**: perch launches interactive panes, so resume uses the top-level TUI
  form `opencode --session <id>` (alias `-s`), or `-c` for the most recent — **not**
  `opencode run` (which is the one-shot non-interactive form).
- **Fork into a worktree**: `opencode --fork` exists (requires `-c`/`-s`) but
  **v1 deliberately starts a fresh session** in the worktree (`ForkInto` returns
  the `ErrForkUnsupported` sentinel). Enabling `--fork` later is a small scope
  change, not a technical limitation.
- **New**: `opencode` (optionally `--agent`/`--model`/`--prompt`).
- **On disk** (for discovery/debugging only, never as the status source): sessions
  live in a SQLite DB at `~/.local/share/opencode/opencode.db`. We do **not** read
  the DB directly — `session list --format json` is the contract.

---

## 5. Discovery & scope

- **Root = the directory perch is launched in** (overridable: `perch [path]` or
  config `roots`). Predictable and explicit — launching in `~` scans all of `~`,
  so don't.
- **Find projects** by scanning under root for git repos. Required care:
  1. `.git` may be a **file** (worktrees, submodules), not only a directory —
     detect both.
  2. **Stop descending** once a repo root is found; **prune** `node_modules`,
     `vendor`, `.git` internals. Honour a configurable **max depth**. Scanning must
     stay fast on trees with large dependency folders.
  3. Enumerate a project's worktrees **authoritatively via `git worktree list
     --porcelain`**, not by scanning — worktrees often live *outside* root (e.g.
     sibling `repo__worktrees/`). The porcelain output also flags bare repos.
- **Default ordering = frecency** (zoxide algorithm, §6). The left selector lists
  projects most-recently-and-frequently used first, so the common case is one
  keystroke away. Falls back to alphabetical when no frecency data exists yet.
- **Open-anywhere escape hatch**: an action takes any path, git or not. For a
  non-git path, perch simply runs the session there and **hides the worktree
  prompt** (worktrees only exist inside a repo).

---

## 6. State & persistence

perch keeps **two** plain-JSON stores under `$XDG_STATE_HOME/perch/` (default
`~/.local/state/perch/`). No database, no daemon. The split is deliberate and
addresses concurrency: per-window records are resurrect-critical and may be written
by more than one perch instance, so they are isolated into one file each; the
shared `state.json` is written only by the interactive TUI and tolerates the rare
lost update.

### 6.1 `state.json` — choices & frecency (TUI-written, low contention)

```json
{
  "mappings": {
    "ses_18a0...": { "tool": "opencode", "tree": "/abs/path", "choice": "worktree" },
    "<claude-uuid>": { "tool": "claude", "tree": "/abs/path", "choice": "none" }
  },
  "projects": {
    "/home/me/trulioo/gitlab/workflows": { "rank": 12.3, "last_accessed": 1748476800 }
  }
}
```

- **`mappings`** — the worktree decision per session, so perch never re-prompts
  (§7). A flat map of `sessionID → {tool, tree, choice}`.
- **`projects`** — frecency data for selector ordering (§6.3).
- Written atomically (temp file + `rename`). Concurrency note: two perch instances
  could in principle clobber each other here, but `mappings` is written only on the
  interactive worktree prompt and `projects` tolerates a lost increment, so a lock
  is unnecessary in v1. **Do not** put resurrect-critical data here.

### 6.2 `windows/<paneKey>.json` — live-window shadow (per-window, resurrect-critical)

One small file per running window, named by a percent-encoded tmux pane id (workmux
`state/types.rs` naming). Isolating each window into its own file means concurrent
launches/kills from different perch instances never clobber each other — the lesson
workmux encodes with per-agent files, and the reason we do **not** fold these into
`state.json`.

```json
{
  "pane_key": "%17",
  "tool": "opencode",
  "session_id": "ses_18a0...",
  "tree": "/abs/path/workflows__worktrees/feat-aligner",
  "tmux_session": "workflows",
  "tmux_window": "feat-aligner",
  "boot_id": "1748470000",
  "updated": 1748476800
}
```

- **`boot_id`** is the tmux server's `#{start_time}` (workmux's crash sentinel). It
  is what distinguishes "window intentionally closed" from "window lost to a server
  restart" during reconciliation (§7.2).
- Written when perch launches a window; the record is the persisted shadow of the
  pane options. Removed on clean teardown.

### 6.3 Frecency (zoxide algorithm, verified from `zoxide/src/db/dir.rs`)

Score is **computed at query time, never stored**. Only `rank` (accumulated visit
weight) and `last_accessed` persist.

```go
// Sort the selector by descending score.
func FrecencyScore(rank float64, lastAccessed, now int64) float64 {
    switch d := now - lastAccessed; {
    case d < 3_600:   return rank * 4.0    // within the last hour
    case d < 86_400:  return rank * 2.0    // within the last day
    case d < 604_800: return rank * 0.5    // within the last week
    default:          return rank * 0.25   // older
    }
}

// On each project selection: bump, then age the table.
//   rank = max(rank + 1.0, 0); last_accessed = now
// Age (call after every bump) bounds total weight, evicting stale entries:
//   total := sum(rank); if total > maxAge { factor := 0.9*maxAge/total;
//     each rank *= factor; drop entries with rank < 1.0 }
//   maxAge default 10_000.0 (zoxide's $_ZO_MAXAGE default)
```

---

## 7. Worktree lifecycle & recovery

This is the safety-critical surface. Patterns are lifted from workmux (teardown
ordering, deferred cleanup, boot_id resurrect) and lazygit (remove guardrails,
force-escalation), reimplemented in Go.

### 7.1 Create

- New branch: `git worktree add -b <branch> <path> <base>` where `<base>` defaults
  to the project's `HEAD` **commit**, not the working tree — so uncommitted changes
  never bleed into the new tree (claude-squad `worktree_ops.go`).
- Placement convention: `<project>__worktrees/<branch-handle>` (sibling of the repo),
  overridable per-project via `worktree_dir` (§8). Branch handle = slugified branch.
- After the directory and tmux window exist, run **`post_create`** hooks (§8) with
  env `PERCH_HANDLE`, `PERCH_WORKTREE_PATH`, `PERCH_PROJECT_ROOT`, `PERCH_BRANCH`.
- File seeding (workmux `file_ops.rs`): copy `files.copy` globs and create **relative
  symlinks** (`pathdiff`-style `../../..`) for `files.symlink` globs from repo root
  into the new worktree — for sharing `.env`, `node_modules`, etc. Relative (not
  absolute) symlinks so they survive being moved. Every path is validated to stay
  **inside** the repo (reject absolute paths and `..` escape) before any filesystem
  op — port this guard verbatim; it is a security gate.

### 7.2 Remove (two-tier guardrails — lazygit pattern)

```
remove(tree):
  if tree is the project's main checkout  → hard error (never removable)
  if tree is the one you're focused in     → hard error (suggest switch first)
  confirm("Remove worktree <handle>?  branch <b> will be kept unless empty")
  on confirm: git worktree remove <path>
  if git refuses (dirty / untracked / submodule / locked):
      escalate → confirm("<handle> has modified/untracked files. Force remove?")
      on confirm: git worktree remove --force <path>
```

- **Locked worktree**: if a `.git/worktrees/<handle>/locked` file blocks
  `git worktree prune`, remove that lock file first (workmux `cleanup.rs`).
- **pre_remove** hooks (§8) run *before* any destructive op, while the tree is still
  intact, with the same env as `post_create`. Skippable with `--no-hooks`.
- **Deferred self-close** (workmux `cleanup.rs` + `tmux run-shell`): when the remove
  is triggered from *inside* the very window being torn down, you cannot kill that
  window synchronously without killing the process mid-cleanup. Build an ordered
  shell one-liner and dispatch it via `tmux run-shell` (runs detached):
  `sleep 0.3 → switch away → kill source window → mv tree to a .perch_trash_<h>_<ts>
  sibling → git worktree prune → (optional) git branch -d → rm -rf trash`.
  The rename-to-trash frees the path immediately even if a shell still has it as cwd.
  Order is mandatory (mv → prune → branch → rm). `git worktree prune` is best-effort
  (`|| true`) so a prune failure never strands the moved-to-trash tree before `rm`.
  The builder (`tmux.CleanupScript`) takes the main `RepoDir`; git steps run via
  `git -C <RepoDir>` because the tree is moved out from under any cwd. **Built in M4
  (`internal/tmux/cleanup.go`, pure builder); dispatched via `tmux run-shell` in M6.**
- Delete the window's shadow record (§6.2) as part of teardown.

### 7.3 Merge (optional convenience, v1-lean)

`perch merge <tree>` is **optional** for v1; if shipped, mirror workmux: validate no
uncommitted changes in the target, run **pre_merge** hooks, `merge`/`rebase`/`squash`
per config, then offer teardown. If it slips past v1, the worktree-remove flow above
is the must-have. (Do not pull in workmux's GitHub-PR polling — out of scope, §17.)

### 7.4 Recovery — `perch resurrect` (workmux boot_id reconcile)

After a reboot the tmux server is gone and with it all pane options, but the
`windows/<paneKey>.json` shadow records survive. `perch resurrect`:

1. Read every `windows/*.json` record.
2. Get the live tmux server's `#{start_time}` = current `boot_id`, and one batched
   `tmux list-panes -a -F '#{pane_id}…#{@perch_session}…'`.
3. Per record (two-track reconcile):
   - pane present and `boot_id` matches → live, healthy; keep.
   - pane **gone** and record `boot_id` **matches** current server → intentionally
     closed; delete the stale record.
   - pane gone and record `boot_id` **differs** → survived a server restart →
     **restore**: recreate the tmux session/window in `tree` and relaunch the agent
     via the adapter's resume args (claude `--resume <id>`; opencode `-s <id>`).
4. Match a record's `tree` to a live `git worktree list` entry by **descendant path**
   (the agent may have cd'd into a subdir). Skip restore if the handle already exists
   or the tree is the main checkout.

Run `perch resurrect` manually, or offer it automatically on TUI start when stale
records are detected. There is **no background process** doing this — it is a
one-shot reconcile (§17).

---

## 8. Configuration

Config is **optional** — perch runs on sensible defaults. Two layers, project wins
per-field (sesh + workmux merge model). TOML throughout.

### 8.1 Discovery (workmux three-tier)

1. Global: `$XDG_CONFIG_HOME/perch/config.toml`.
2. Per-project: walk **up** from cwd to the repo root looking for `.perch.toml`
   (then fall back to the main worktree root for linked worktrees).
3. Project config overrides global per-field; everything has a default.

### 8.2 Schema (fields modelled on sesh `model/config.go` + workmux `config.rs`)

```toml
# ---- global config.toml ----
roots        = ["~/trulioo/gitlab"]   # default: launch cwd
sort_order   = ["running", "pinned", "frecency"]  # left-selector source priority
blacklist    = ["**/archive/**"]      # glob patterns to hide
refresh_ms   = 1000                   # status tick interval (§9)

[default_session]
agent           = "claude"            # default tool when launching new
startup_command = ""                  # extra command to send after launch (optional)

[theme]                               # see §11/§12; adaptive light/dark by default
accent = "#EE6FF8"

# ---- per-project .perch.toml ----
base_branch  = "main"                 # branch-from for new worktrees
worktree_dir = "../wt"                # override <project>__worktrees placement
agent        = "opencode"             # default agent for this repo

[files]
copy    = [".env", ".env.local"]      # copied into each new worktree
symlink = ["node_modules"]            # relative-symlinked into each new worktree

post_create = ["direnv allow", "pnpm install"]   # hooks after worktree+window created
pre_remove  = []                                  # hooks before teardown
pre_merge   = []                                  # hooks before merge (if §7.3 shipped)

[[wildcard]]                          # sesh-style: auto-rules by path glob
pattern         = "**/experiments/*"
agent           = "claude"
```

**Security boundary (workmux):** a project's `.perch.toml` must **not** be able to
override security-relevant globals (e.g. force a different binary path for the
agent). Keep the agent *binary resolution* global-only; project config may pick
*which* known agent and pass model/prompt, not point at an arbitrary executable.
Validate path safety: `files.copy`/`files.symlink` entries must stay inside the repo (no absolute paths, no `..` traversal); `worktree_dir` may be a sibling, relative, or absolute path outside the repo (the default placement is a sibling directory) and is rejected only if it resolves into the repo's `.git` directory.

---

## 9. Status pipeline

Goal: each row in the admin shows 🤖 working / 💬 waiting-on-you / ✅ done / ○ idle.

Stable, tool-agnostic, **daemon-free** flow (the proven contract):

```
agent hook/plugin ──► `perch status set <state>` ──► tmux set -p @perch_status <state>
                                                            │
                              admin reads tmux options ◄────┘  (light tick poll)
```

- `perch status set <state>` resolves the current pane from `$TMUX_PANE` and sets
  `@perch_status`. perch never parses agent internals for status.
- **Claude** (verified pattern): `perch setup` installs `~/.claude/settings.json`
  hooks — `Notification(permission_prompt|elicitation_dialog) → waiting`,
  `PostToolUse → working`, `Stop → done`, `UserPromptSubmit → working`. (workmux
  installs equivalents today; perch installs its own pointing at `perch status set`.)
- **opencode** (verified against opencode `packages/plugin` + workmux's installed
  plugin): a plugin is an async function `({ $ }) => ({ event: async ({event}) =>
  … })`, where `$` is a Bun shell that can exec subprocesses. `perch setup` writes
  `~/.config/opencode/plugins/perch-status.ts` (auto-discovered; no config entry).
  Event → state mapping:

  | opencode event | condition | perch state |
  |---|---|---|
  | `session.status` | `status.type == "busy"` or `"retry"` | `working` |
  | `session.status` | `status.type == "idle"` | `done` |
  | `session.idle` | (deprecated alias) | `done` (fallback) |
  | `permission.asked` / `question.asked` | — | `waiting` |
  | `permission.replied` / `question.replied` | — | `working` |

  **Must** de-dupe per `event.properties.sessionID` (track last state) and gate out
  the **stale trailing `busy`** opencode emits right after `idle` — arm "accept
  busy" on a user `message.updated`, disarm on `done`. The handler shells out:
  `await $\`perch status set <state>\`.quiet()`. Note: `permission.asked` /
  `question.asked` fire on the bus but lag the typed v1 `Event` union — cast as
  needed (workmux does).
- The admin polls tmux options on a low-frequency **tick** (≈`refresh_ms`, default
  1 s) — cheap, no busy loop, no daemon. In bubbletea this is a re-armed
  `tea.Tick` (§12); guard overlapping polls with an in-flight bool (k9s's
  `atomic.CompareAndSwap` drop-guard, adapted).
- Storage: `@perch_status` (window option, drives the tmux status bar) and
  `@perch_pane_status` (pane option, what the admin reads). The admin enumerates
  `tmux list-panes -a -F '…#{@perch_pane_status}…'` and reconciles against live
  panes — **no status-state file**. Adopt workmux's **auto-clear-on-focus**: a
  `pane-focus-in` hook unsets a `waiting`/`done` badge when you focus that pane.
  Idle = no option / stale.

> **NOTE (M8 to resolve):** the ASCII diagram above and the first bullet both describe
> `perch status set` as doing `tmux set -p @perch_status` (pane-scoped), but this
> Storage bullet says `@perch_status` is a **window** option while `@perch_pane_status`
> is the **pane** option the admin reads. These two descriptions are internally
> inconsistent — the scope and name of the option written by `perch status set` need
> to be reconciled. M4 does not touch this (M4 round-trips `@perch_session`, pane-
> scoped, unambiguous). **M8 (status pipeline) must resolve the `@perch_status`
> scope/name contradiction before implementing `perch status set`.**

---

## 10. CLI surface (keep small)

| Command | Purpose |
|---------|---------|
| `perch` | Launch the TUI; root = cwd |
| `perch [path]` | Launch the TUI; root = path |
| `perch setup` | Detect installed tools; install status hooks/plugins |
| `perch resurrect` | Rebuild windows after a tmux server restart (§7.4) |
| `perch status set <working\|waiting\|done>` | Internal; called by hooks (reads `$TMUX_PANE`) |
| `perch doctor` | Check deps (tmux/git/agents), report what's missing |
| `perch version` | Version/build info |

Optional, cheap, and a clear edge over claude-squad (which can only act from inside
its TUI): `perch attach <query>` — fuzzy-match a session by title/branch and
`switch-client`/`attach` to it without entering the TUI. Ship if time allows.

---

## 11. UI / UX

**One unified screen** is both the picker and the admin — "manage from one window".
**Two panes: left = selector, right = preview.** Scroll the left list, the right pane
shows details + a live preview of the highlighted session; `↵` switches into it.

```
┌ perch ──────────────────────────────────────── ~/trulioo/gitlab ─┐
│ /aws_              │ feat/aligner · opencode · waiting 💬          │
│ ───────────────────│ ~/…/workflows__worktrees/feat-aligner        │
│ workflows          │                                              │
│   ● main       🤖  │ ── live preview ───────────────────────────  │
│ ▸ ● feat/aligner💬 │ > run the terraform plan and summarize       │
│   ○ GitLab OIDC ✓  │ ⠿ waiting for your approval to apply…        │
│ cartographer       │                                              │
│   ○ main        ○  │                                              │
│ ───────────────────│                                              │
│ 18 projects        │                                              │
├────────────────────┴──────────────────────────────────────────────┤
│ ↵ switch · n new · w worktree · x kill · / filter · ? help · q quit │
└─────────────────────────────────────────────────────────────────────┘
```

Left list groups by project, ordered by frecency (§6.3); `▸` marks the highlighted
row. Right pane previews the highlighted *running* session via `tmux capture-pane`
(details only for idle ones). `↵` issues a single `tmux switch-client`/`attach` —
instant switch.

### Interaction model (k9s + lazygit, rebuilt on bubbles/lipgloss)

- **Keyboard-first**, vim-style: `j/k` move, `/` fuzzy filter, `:` command mode,
  `↵` open/attach, `n` new session, `w` new-worktree session, `x` kill, `d` remove
  worktree, `?` help overlay, `q` quit, `z`/`Z` cycle screen mode.
- **`:` command bar vs `/` filter bar — one input widget, two modes** (k9s
  `cmd_buff.go`): a single `textinput` whose prefix, placeholder, and border colour
  switch on the activating key (`:` = command/aqua, `/` = filter/green). Debounce
  the actual filter/search with a 100 ms `tea.Tick` so it doesn't run per keystroke.
- **Contextual keybindings + auto-generated help** (lazygit `keybindings.go`,
  k9s `menu.go`): each view exposes a `[]Binding{Key, Desc, Tag, DisplayOnScreen,
  Disabled() string}`. The footer hint bar and the `?` overlay are *generated* from
  that registry (filter to `DisplayOnScreen && !Disabled`), never hand-maintained.
  A disabled binding is greyed out and its handler refuses to run with a toast
  reason. Rebuild the footer only on view change, not per keypress.
- **Status → colour** is a single function `statusColor(s Status) lipgloss.Color`
  backed by the theme (k9s `render/pod.go` colorer pattern); applied to the status
  cell. Optionally show `↑`/`↓` deltas on changed numeric cells (uptime).
- **Confirmation modals** (lazygit popup stack): destructive actions (`x` kill, `d`
  remove worktree, force-remove escalation) go through a confirm overlay rendered
  with a lipgloss border, placed over the UI. Model popups as a small stack;
  temporary (confirm/prompt) vs persistent (a multi-step new-worktree wizard).
- **Screen modes** (lazygit `window_arrangement_helper.go`): `normal` (two-pane),
  `full-list` (selector fills width), `full-preview` (preview fills width); flip
  side/main to **vertical stacking** on a narrow terminal.
- **One theme**: a single `internal/tui/styles.go` (lipgloss), adaptive light/dark
  via `LightDark` driven by `tea.BackgroundColorMsg` (§12), no hardcoded colours
  elsewhere. Optional live theme reload is a nice-to-have, **not** via a background
  watcher — only on explicit reload.
- **States handled explicitly**: loading, empty ("no git repos under <root>"),
  errors as non-blocking toasts, graceful reflow on small terminals.

---

## 12. Component map (Charm v1 — API surface carried inline)

Stable v1 line: bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0
(`github.com/charmbracelet/<lib>`). API carried inline so the implementer needn't
hunt through docs.

| perch view / feature | component | key API (v1) |
|---|---|---|
| Left selector (projects/trees/sessions) | `bubbles/list` | `list.New(items, delegate, w, h)` · `SelectedItem()` · `SetItems()` · custom `ItemDelegate` for status glyph + tool + relative time · built-in fuzzy filter (`SetFilterText`, `MatchesForItem`) |
| Admin session table | `bubbles/table` | `table.New(WithColumns, WithRows, WithHeight, WithFocused)` · `SetRows()` · `SelectedRow()` · `Focus()/Blur()` · per-cell style via `Styles{Header,Cell,Selected}` |
| Status polling tick | `tea.Tick` | `tea.Tick(refresh, fn)` returning a `pollMsg`; re-arm in `Update`; gate with in-flight bool |
| Launch / attach agent | `tea.ExecProcess` | `tea.ExecProcess(exec.Command("tmux","attach",…), cb)` — **pauses** the TUI, hands over the terminal, resumes on exit. Use plain `tea.Cmd` goroutines for non-interactive `tmux list-*` |
| Command / filter bar | `bubbles/textinput` | `textinput.New()` · `Focus()` · `Value()` · `Placeholder` · `SetSuggestions()` for fish-style completion |
| Help bar + overlay | `bubbles/help` + `bubbles/key` | `key.NewBinding(WithKeys, WithHelp)` · `b.SetEnabled(bool)` (disabled = hidden) · `help.New().View(keyMap)` auto-renders short/full from `ShortHelp()/FullHelp()` |
| Confirmation modal / wizard | custom model + `lipgloss` | `lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1,2)`; place over UI; popup stack |
| Two-pane + footer layout | `lipgloss.JoinHorizontal/JoinVertical` | `JoinHorizontal(lipgloss.Top, left, right)` then `JoinVertical(lipgloss.Left, body, footer)`; measure with `lipgloss.Width/Height` |
| Live preview / detail pane | `bubbles/viewport` | `viewport.New(w, h)` · `SetContent()` · `m.Style` · `/`-search by tracking matched line indices |
| Theme (light/dark) | `lipgloss.AdaptiveColor` | `accent := lipgloss.AdaptiveColor{Light:"#…", Dark:"#…"}` — lipgloss **auto-detects** the terminal background via termenv; no message plumbing needed |
| Colour-profile fallback | `lipgloss.CompleteColor` | `lipgloss.CompleteColor{TrueColor:"#…", ANSI256:"…", ANSI:"…"}` — lipgloss picks per the detected profile |
| "working" spinner | `bubbles/spinner` | `spinner.New()` · `Spinner = spinner.MiniDot` · `Tick()` · handle `spinner.TickMsg` |

In v1: `Model.View()` returns a **`string`**, key messages are **`tea.KeyMsg`**, and
adaptive/complete colour are **struct literals** that self-detect (simpler than v2 —
no `RequestBackgroundColor`/`BackgroundColorMsg` round-trip). `list.DefaultStyles()`
takes no argument.

### v1 → v2 deltas (for a *future* migration only — do not use v2 in v1)

If perch ever moves to v2: import paths become `charm.land/<lib>/v2`; `View()`
returns `tea.View` (wrap with `tea.NewView`); `tea.KeyMsg` → `tea.KeyPressMsg`;
`AdaptiveColor{}`/`CompleteColor{}` structs become the `LightDark(bool)` /
`Complete(profile)` functions fed by `tea.BackgroundColorMsg`; `list.DefaultStyles`
gains an `isDark bool`. None of this is needed for v1.

---

## 13. Competitive note — claude-squad

Our nearest peer (smtg-ai/claude-squad, Go, bubbletea). What to **match** (table
stakes it already proves work) and where to **deliberately differ** (our edge).

**Match:** bubbletea TUI with ~30% list / ~70% tabbed preview; tmux as the agent
host (`tmux new-session -d` then PTY-attach); `tmux capture-pane -p -e -J` for the
non-interactive preview poll; `Ctrl-Q`-style detach; git worktree per session
branched from the HEAD *commit*; pause/resume (commit-and-remove-worktree /
re-add-worktree); JSON state for resume across restarts; `+N/-N` diff stats in list
rows; an optional per-session Terminal tab (a separate shell session in the tree).

**Differ (perch's edge):**
- **Adopt external sessions.** claude-squad can only see sessions it created (its
  state file). perch *discovers* existing claude/opencode sessions on disk and
  running tmux windows and offers to manage them — a core requirement.
- **opencode + true adapters.** claude-squad hardcodes three agents as strings;
  perch ships an `Adapter` interface incl. opencode, with ready-state heuristic and
  trust-prompt as data (§4).
- **Non-git and main-checkout sessions.** claude-squad *requires* a git repo and a
  fresh worktree per session; perch supports non-git dirs and running in the main
  checkout — worktrees are opt-in.
- **Repo-keyed, multi-repo discovery** under one root, vs claude-squad's single
  global worktree dir and single launch dir.
- **No arbitrary limits** (claude-squad caps at 10 instances).
- **Pure git**, no `gh` CLI dependency for push.
- **Act from anywhere**: `perch resurrect`, `perch status`, optional
  `perch attach <query>` — no need to be inside the TUI.

---

## 14. Project layout (Go best practices)

```
perch/
├── cmd/perch/main.go            # entrypoint; stdlib flag + subcommand switch
├── internal/
│   ├── model/                   # Project, Tree, Session, Window types (no deps)
│   ├── discover/                # bounded repo scan under root
│   ├── git/                     # worktree list/add/remove + lifecycle (§7)
│   ├── agent/                   # Adapter interface + claude.go + opencode.go
│   ├── tmux/                    # session/window control, @perch_* options, deferred cleanup
│   ├── state/                   # state.json + windows/*.json stores; frecency (§6)
│   ├── status/                  # `perch status set`, option read helpers
│   ├── config/                  # TOML load + merge (global + .perch.toml) + defaults
│   └── tui/                     # bubbletea: app.go, list.go, table.go, prompt.go,
│                                #   confirm.go, help.go, styles.go, keys.go
├── testdata/                    # sanitized CLI output fixtures (§20.2)
│   ├── claude/
│   ├── opencode/
│   ├── git/
│   └── tmux/
├── resources/                   # perch-status.ts (opencode), claude settings snippet
├── scripts/                     # dev/CI helper scripts (not the user-facing install)
├── install.sh                   # user-facing bootstrap script (§21.3)
├── .tool-versions               # pinned versions for all external deps (§21.2)
├── Makefile
├── go.mod
├── README.md
└── plan.md
```

Each `internal/*` package has one clear job, a small surface, and table-driven
unit tests. External-command wrappers (`git`, `tmux`, `agent`) sit behind
interfaces so the TUI and core logic are testable without spawning processes.

---

## 15. Makefile (house style)

Follow the team Makefile conventions: real tabs, `.DELETE_ON_ERROR`,
`SHELL := /bin/bash`, `.FORCE`, `ROOT_DIR` via `git rev-parse`, `@`-quiet recipes,
`.PHONY`. No CI targets in v1.

```make
.DELETE_ON_ERROR:
SHELL := /bin/bash
.FORCE:

ROOT_DIR := $(shell git rev-parse --show-toplevel)
BIN      := perch
BIN_DIR  := $(ROOT_DIR)/bin
PKG      := ./...
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Hermetic, reproducible builds: vendored deps, no network at build time.
export GOFLAGS := -mod=vendor
# Pinned dev tools (run via `go run tool@version`, never a floating install).
GOLANGCI := v2.6.0
GOVULN   := v1.3.0

ifeq (, $(shell command -v go))
$(error 'go' not found on PATH)
endif

.PHONY: build install run test lint fmt vet tidy vendor verify vulncheck doctor clean cross

build:                ## build the binary into ./bin (vendored, reproducible)
	@mkdir -p $(BIN_DIR)
	@go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

install:              ## install to GOBIN / ~/go/bin
	@go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/perch

run: build            ## build then run
	@$(BIN_DIR)/$(BIN)

test:                 ## unit tests
	@go test -race -count=1 $(PKG)

test-integration:     ## integration tests (requires tmux and git)
	@go test -race -count=1 -tags=integration $(PKG)

test-all:             ## unit + integration
	@go test -race -count=1 -tags=integration $(PKG)

coverage:             ## coverage report for internal/ packages
	@go test -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1

lint:                 ## golangci-lint at the pinned version
	@GOFLAGS= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI) run

fmt:                  ## gofmt + goimports
	@gofmt -w . && (command -v goimports >/dev/null && goimports -w . || true)

vet:
	@go vet $(PKG)

tidy:                 ## tidy go.mod/go.sum then refresh the vendor tree
	@GOFLAGS= go mod tidy && go mod vendor

vendor:               ## refresh the committed /vendor tree
	@GOFLAGS= go mod vendor

verify:               ## verify every module checksum matches go.sum
	@GOFLAGS= go mod verify

vulncheck:            ## scan deps for known CVEs (pinned govulncheck)
	@GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@$(GOVULN) ./...

doctor: build         ## run perch's own dependency check
	@$(BIN_DIR)/$(BIN) doctor

cross:                ## cross-compile all platforms into ./bin
	@mkdir -p $(BIN_DIR); for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "building $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(BIN_DIR)/$(BIN)-$$os-$$arch ./cmd/perch; \
	done

clean:
	@rm -rf $(BIN_DIR)
```

`-trimpath` strips local filesystem paths from the binary (reproducible builds,
no path leakage). Tool targets clear `GOFLAGS` because `go run tool@version` needs
module mode, which `-mod=vendor` forbids.

---

## 16. Implementation milestones

Build in this order; each milestone is independently runnable/testable.

0. **Bootstrap** — `.tool-versions` + `install.sh` (§21). At implementation time,
   verify and pin each external dependency to the latest stable release that is ≥30
   days old (§2 policy) and write those versions into `.tool-versions` — the single
   source of truth. Then write `install.sh` as specified in §21.3, reading all
   versions from that file. Test by running inside a fresh `docker run --rm ubuntu:24.04`
   and `docker run --rm fedora:41` container. *Done when:* script exits 0 on both
   distros (with and without `--skip-agents`), and `perch doctor` and `perch version`
   both pass on a clean machine.

1. **Skeleton** — `go.mod` (exact-pinned deps + `toolchain`), vendored tree, stdlib
   `flag` root + subcommand switch, `Makefile`, `config` +
   `state` packages, `perch doctor`. *Done when:* `make build && ./bin/perch doctor`
   reports tool presence.
2. **Discovery + git** — `model`, bounded repo scan, `git worktree list --porcelain`
   wrapper, frecency store + ordering. *Done when:* a debug command lists projects +
   trees under a root, fast, with pruning, frecency-ordered.
3. **Adapters** — `claude` + `opencode` `ListSessions`; resume/fork/new arg builders.
   *Done when:* unit tests parse real fixtures; sessions group by directory.
4. **tmux control** — create session/window in a dir, run a command, switch/attach,
   set/read `@perch_*`, write the window shadow record. *Done when:* a debug command
   opens a shell window in a chosen tree and round-trips a pane option + record.
5. **TUI (open flow)** — unified two-pane list (project→tree→session), fuzzy filter,
   live preview, `↵` opens the right resume/new command in a tmux window. *Done when:*
   you can resume a real claude and a real opencode session from the list.
   — ✅ **DONE** (M5-1..M5-5; commits through `539569a`). Two-pane list + frecency
   ordering, fuzzy filter, capture-pane live preview, D6 live/idle join, `↵`
   resume/switch-or-relaunch split + `n` new, attach via `switch-client`/`tea.ExecProcess`,
   frecency bump + window shadow record. Gates green (unit 12/12, integration 12/12 on
   real tmux, lint 0, `go mod verify` clean, coverage 92.2% on `internal/` excl. `internal/tui`).
   **Honesty note:** the sentinel integration test proves the launch path *executes* and the D6
   join works end-to-end against a real server. Real **claude resume is demonstrated** — perch's
   `claude --resume <id>` argv launched into a tmux pty rendered full prior history (no auth/
   not-found error). Still manual: the **opencode** resume half (no sessions existed to resume —
   create-then-resume smoke-test) and the final alt-screen **handover into the user's own
   terminal** (can't be driven headlessly). Deferred to M6+: `w`/`d`/`x` worktree+cleanup keys, window-reuse on
   Connect (M6); resurrect/boot_id reconcile (M7); `@perch_status` pipeline (M8); command
   bar/help/modals/theme (M9). See `docs/superpowers/plans/2026-05-31-perch-m5-tui-open.md`.
6. **Worktree lifecycle + state** — create (file seeding, post_create), the 3-way
   mapping prompt, fork semantics, persistence/no-re-prompt, **remove with
   two-tier guardrails + deferred cleanup**. *Done when:* choices survive restart;
   "create worktree" forks into a new branch dir; remove safely handles dirty/locked.
   — ✅ **DONE** (M6-1..M6-7). git worktree add/remove/prune + slugify/placement/lock
   (`internal/git`), file seeding + post_create/pre_remove hooks (`internal/worktree`),
   config validate split + binary mapping enum with no-re-prompt (`internal/config`+
   `internal/state`), claude native fork (`--fork-session` + pinned `--session-id`) /
   opencode fresh-session fallback, `Tmux.RunShell -b` + `KillWindow` +
   `CurrentClientWindow` + Connect window-reuse + `worktree.DeferredRemove`, and the
   TUI `w`/`d`/`x` keys with a minimal modal state machine, the §7.2 two-tier remove
   guardrails (main-checkout + focused-in-tree hard errors; dirty→force escalation),
   and the 3-action worktree-create flow. Gates green (unit + `-race` integration on
   real git + tmux 3.6, `go mod verify` clean, gofmt/lint 0, coverage ≥80% on
   `internal/` excl. `internal/tui`). **Honesty note:** *demonstrated* — claude fork
   pinning verified empirically (print-mode + interactive tmux-pty, control showed
   auto-mint when unpinned); deferred self-close demonstrated **end-to-end** on a
   private tmux socket (the `run-shell -b` script's bare `tmux kill-window` inherits
   `$TMUX` and tears down window + tree + prune + branch + shadow record, ≤2s);
   `ErrWorktreeDirty` confirmed on an untracked file; mapping survives a simulated
   restart. *Manual/deferred* — alt-screen handover of a remove into the user's own
   terminal (not headlessly drivable, same as M5 attach); the in-repo claude-fork
   e2e demo is **env-gated** (`PERCH_CLAUDE_E2E=1`) since it needs live claude auth +
   network + folder-trust and isn't hermetic; opencode fork = `ErrForkUnsupported`
   by design; a Seed/post_create failure after `git worktree add` leaves an orphaned
   worktree dir (unique branch, non-blocking) — full create rollback deferred. See
   `docs/superpowers/plans/2026-05-31-perch-m6-worktree.md`.
7. **Recovery** — `perch resurrect` boot_id reconcile. *Done when:* after killing the
   tmux server, `perch resurrect` rebuilds the windows from shadow records.
   — ✅ **DONE** (M7-1..M7-3). reconcile engine (`internal/resurrect`) with single-snapshot
   classify-then-mutate (FD1), pane-id match (FD2), three-case KEEP/PRUNE/RESTORE (FD3),
   live-window guard (FD4), five RESTORE-branch guards incl. main-checkout skip (FD5),
   idempotency via save-then-remove ordering (FD6), adapter-owned resume argv (FD7);
   CLI wiring (`cmd/perch` `handleResurrect`); integration tests on real git + tmux 3.6
   (private socket). Engine bugfix: same-pane-key collision on server restart guarded
   (`paneID != w.PaneKey` before RemoveWindow), pinned by unit regression test.
   **Honesty note:** *demonstrated* — server-restart restore, intentional-close prune,
   double-run idempotency, main-checkout skip (real git + private-socket tmux);
   *manual/deferred* — TUI auto-offer → M9; opencode resume integration uses placeholder
   `sh` in integration (unit covers `--session <id>` argv); `save-failed` branch
   intentionally not triggered (unwritable state dir mid-run is impractical to inject).
   See `docs/superpowers/plans/2026-05-31-perch-m7-resurrect.md`.
8. **Admin/status** — live status tick + drop-guard, status colours, `x` kill,
   attach/jump; `perch setup` installs claude hooks and writes `perch-status.ts`;
   verify opencode status end-to-end. *Done when:* icons reflect a live agent's
   working/waiting/done.
9. **UX layer** — command/filter bar, contextual keybindings + generated help,
   confirmation modals, screen modes, theme pass, empty/error/small-screen states,
   README with install + usage.

---

## 17. Non-goals (v1 — YAGNI)

- No reimplementing a terminal or multiplexer — tmux is the engine.
- **No background daemon / long-running helper.** Status is a tick-poll of tmux
  options; recovery is a one-shot `perch resurrect`. (workmux's Unix-socket pub-sub
  sidebar daemon is explicitly *not* adopted — it conflicts with the lightweight,
  no-process ethos.)
- No remote/SSH/multi-machine orchestration.
- **No CI/CD pipeline** (explicitly out for now).
- No sandboxing/containers (workmux has it; out of scope).
- No GitHub/PR integration or polling.
- No filesystem watchers — no fsnotify config/git watching in v1.
- No SQLite/embedded DB — the two small JSON stores only.
- No tools beyond Claude + opencode (but the adapter interface keeps the door open).
- No web UI.

---

## 18. Open items to verify during build (be honest about these)

1. **opencode status plugin** — events verified from opencode source + workmux's
   installed plugin (§9). Remaining: confirm the installed opencode version's plugin
   loader/location still matches when `perch setup` writes `perch-status.ts`, and that
   `permission.asked`/`question.asked` still fire (they lag the typed v1 union).
2. **Fork-into-worktree** — ✅ RESOLVED at M3 (empirically, claude-code 2.1.158):
   claude forks **natively** via `--resume <id> --fork-session` (launched with the
   target worktree as cwd) — **no `.jsonl`/data-dir copy is needed**. The earlier
   "copy then --resume" plan is superseded. opencode = fresh session for v1
   (`--fork` flag exists but is a deliberate post-v1 scope deferral). Verify claude
   fork-session-into-worktree end-to-end at milestone 6.
3. **opencode `session list` scope** — ✅ CORRECTED at M3: it is **project-scoped**
   (the project is the registered worktree root that cwd resolves to), **NOT
   global**. There is no scope-broadening flag (the assumed `{roots:true}` does not
   exist). The only scoping lever is the process cwd, so M5 enumerates per-directory
   via `proc.RunInDir` (one `session list` per tree). Reading the SQLite DB directly
   stays forbidden.
4. **claude pid tracker** (`~/.claude/sessions/<pid>.json`) — ✅ verified at M3:
   real shape is `{pid, sessionId, cwd, status(idle|busy), startedAt/updatedAt(unix
   ms), …}` (more fields than `{sessionId,cwd,status}`). Used as a Directory
   fallback (after in-transcript `cwd`, before slug-decode); parsed defensively,
   undocumented, guarded against format drift.

> M3 evidence + the full list of plan↔reality corrections (opencode JSON field
> names/ms timestamps, no `--roots`, claude title source = `ai-title`.aiTitle,
> interactive resume = top-level `--session`, `agent.Session`→`model.Session`) are
> recorded in `docs/superpowers/plans/2026-05-30-perch-m3-adapters.md`.
5. **Charm stack — decided, not open.** Locked to the stable v1 line (bubbletea
   v1.3.10 / lipgloss v1.1.0 / bubbles v1.0.0, mutually pinned; requires Go 1.24+).
   No v2 in v1. Listed here only so nobody reopens it.

---

## 20. Testing strategy

### 20.1 Principles

- **Interface-first, no surprise I/O.** Every shell-out — `git`, `tmux`, `claude`,
  `opencode` — goes through a `Runner` interface. Unit tests use a `FakeRunner` that
  records every invocation and returns canned stdout/stderr/exit-codes. Real processes
  are never spawned in unit tests. The real `ExecRunner` is used only in integration
  tests and the production binary.
- **Table-driven, fixture-grounded.** Test cases are structs: input + expected output.
  Fixtures in `testdata/` are sanitized copies of real CLI output so tests break when
  upstream formats change, not when logic regresses.
- **No network, no clock dependency.** Frecency tests receive an explicit `now int64`.
  Status state-machine tests drive the machine directly. Nothing in unit tests reads
  the filesystem beyond `testdata/`.
- **Coverage target: ≥ 80 % on `internal/` excluding `internal/tui/`.**
  The TUI is model-tested (key sequences → state assertions), not line-counted.

### 20.2 `testdata/` layout

Committed sanitized fixtures; tests load them via `os.ReadFile("testdata/...")`.
**Fixtures are co-located per package** (Go idiom): `internal/git/testdata/`,
`internal/agent/testdata/`, etc. — not a single repo-root `testdata/`. The tree
below shows the logical grouping; physically each block lives under its package.

```
(internal/agent/)testdata/
├── claude/
│   ├── projects/
│   │   └── -home-user-myproject/
│   │       └── <uuid>.jsonl          # real-shaped records incl. an ai-title record
│   └── sessions/
│       └── <pid>.json                # sample pid-tracker record
├── opencode/
│   ├── session-list.json             # real `opencode session list --format json` output (multi-session)
│   ├── session-list-empty            # zero-byte file (the real empty-scope output, not "[]")
│   └── session-list-malformed.json   # `{broken`
(internal/git/)testdata/
├── git/
│   ├── worktree-list-porcelain-multi.txt   # project with two worktrees
│   ├── worktree-list-porcelain-single.txt  # main checkout only
│   └── worktree-list-porcelain-linked.txt  # linked worktree (.git-as-file)
└── tmux/
    ├── list-panes-with-options.txt    # includes @perch_session, @perch_pane_status
    └── list-panes-empty.txt           # no perch-managed panes
```

### 20.3 Unit test cases (table-driven, by package)

**`internal/agent/`**

| Test | Input | Expected |
|------|-------|----------|
| Claude slug decode — clean | `-home-user-myproject` | `/home/user/myproject` |
| Claude slug decode — ambiguous | `-home-user-foo-bar` | stat `/home/user/foo-bar` first; fall back to `/home/user/foo/bar`; final fallback is longest existing prefix |
| Claude JSONL parse — malformed line | line 3 is invalid JSON | lines 1, 2, 4 parsed; line 3 skipped + logged; no panic |
| Claude JSONL parse — empty file | empty file | zero sessions, no error |
| opencode session list — valid | `testdata/opencode/session-list.json` | N sessions with correct ID/dir/updated |
| opencode session list — empty | empty bytes (real empty-scope output) and `[]` | zero sessions, no error |
| opencode session list — malformed | `{broken` | error returned, no panic |

**`internal/state/` (frecency)**

| Test | Scenario | Expected |
|------|----------|----------|
| Time bucket: < 1 h | `now - lastAccessed = 3599` | `rank * 4.0` |
| Time bucket: boundary | `now - lastAccessed = 3600` | `rank * 2.0` (boundary belongs to next bucket) |
| Time bucket: < 1 day | `now - lastAccessed = 86399` | `rank * 2.0` |
| Time bucket: < 1 week | `now - lastAccessed = 604799` | `rank * 0.5` |
| Time bucket: old | `now - lastAccessed = 604800` | `rank * 0.25` |
| Aging: below threshold | total weight = 9999.0 | no eviction |
| Aging: above threshold | total weight = 10001.0 | each rank *= 0.9 * 10000 / 10001; entries < 1.0 evicted |
| Cold start | empty state | sort order falls back to alphabetical |

**`internal/status/` (state machine)**

These mirror the logic verified from `workmux-status.ts` (§9).

| Test | Event sequence | Expected calls to `perch status set` |
|------|----------------|--------------------------------------|
| Dedup: same state twice | `working`, `working` | only first `working` fires |
| Stale-busy rejected | `working`, `done`, `busy` | `working`, `done` — third `busy` dropped |
| Re-arm on user message | `working`, `done`, `message.updated(role=user)`, `busy` | `working`, `done`, `working` |
| waiting → working | `permission.asked`, `permission.replied` | `waiting`, `working` |
| Independent sessions | session A: `working`; session B: `done` | two separate status sets, no cross-contamination |

**`internal/config/`**

| Test | Input | Expected |
|------|-------|----------|
| Global only | valid `config.toml`, no `.perch.toml` | all defaults filled, global values applied |
| Project override | global `agent = "claude"`; project `agent = "opencode"` | merged config has `agent = "opencode"` |
| Non-overridden field | global `refresh_ms = 1000`; project has no `refresh_ms` | merged config has `refresh_ms = 1000` |
| Security: binary path | project config sets a custom binary path for an agent | field rejected/ignored; global binary resolution unchanged |
| Missing global | no `config.toml` at all | defaults filled, no error |

**`internal/discover/`**

| Test | Scenario | Expected |
|------|----------|----------|
| Max depth | repo at depth 3, limit 2 | not found |
| Prune `node_modules` | `node_modules/.git` present | `node_modules` never descended |
| Prune `vendor` | same | same |
| `.git` as file | linked worktree with `.git`-as-file | detected as repo root |
| Non-git dir | directory with no `.git` | discovered as a plain tree; no worktree enumeration; no crash |
| Nested repos | repo inside repo | outer found, inner found as separate project (not descendant of outer) |

### 20.4 Integration tests (`//go:build integration`)

Integration tests spawn real processes. Gated so `make test` never needs tmux or git.

Setup:
1. Create a temp git repo in `t.TempDir()`.
2. Start a dedicated tmux server on a test socket: `tmux -L perch-test-<pid> new-session -d`.
3. All tmux calls use that socket; never touching the user's live tmux server.

Teardown (deferred):
1. `tmux -L perch-test-<pid> kill-server`
2. Remove the leftover socket file — `kill-server` exits 0 but leaves the socket on
   disk; the harness computes the socket path respecting `$TMUX_TMPDIR` (fallback
   `/tmp/tmux-<uid>`) and calls `os.Remove` after `kill-server` (E9, M4 evidence).
3. Remove temp dirs.

The reusable `newTestServer(t) *tmux.Tmux` harness lives in
`internal/tmux/integration_test.go` and is shared by M5–M7.

Test cases:
- Create worktree → verify `git worktree list --porcelain` reflects it.
- Launch perch window in tree → set `@perch_session` → read it back → match.
- Write shadow record → kill tmux server (new socket) → `perch resurrect` → window
  recreated, shadow record removed from stale/recreated properly.
- Remove worktree (clean) → `git worktree list` no longer shows it.
- Remove worktree (dirty) → first attempt fails, escalated force succeeds.

These tests are slow (seconds). Run them explicitly with `make test-integration`.

### 20.5 TUI tests (`teatest`)

Uses `github.com/charmbracelet/x/exp/teatest` (part of the Charm ecosystem, the
standard test harness for bubbletea models).

```go
// Example: selecting the third item with j j and pressing enter triggers attach
func TestListSelectAndAttach(t *testing.T) {
    m := tui.NewListModel(fixtures.ThreeProjects())
    tm := teatest.NewTestModel(t, m)
    tm.Send(tea.KeyMsg{Type: tea.KeyRune, Rune: 'j'})
    tm.Send(tea.KeyMsg{Type: tea.KeyRune, Rune: 'j'})
    tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
    got := tm.FinalModel(t).(tui.ListModel)
    if got.Selected().ID != fixtures.ThreeProjects()[2].ID {
        t.Fatalf("expected third project selected")
    }
}
```

Rules:
- Test key sequences → model state. Never assert on rendered strings (brittle across
  terminal widths and lipgloss versions).
- Test that destructive actions (`x`, `d`) produce a confirmation modal in the model.
- Test that `/foo` filter reduces `VisibleItems()` and `Esc` clears.
- No golden-file screenshot tests in v1.

### 20.6 Makefile test targets

```makefile
test:                 ## unit tests only (no external deps required)
	@go test -race -count=1 ./...

test-integration:     ## integration tests (requires tmux ≥3.2 and git)
	@go test -race -count=1 -tags=integration ./...

test-all:             ## unit + integration
	@go test -race -count=1 -tags=integration ./...

coverage:             ## coverage report for internal/ packages
	@go test -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1
```

---

## 21. Dependency bootstrap & installation

### 21.1 Dependency matrix

All pinned versions live in `.tool-versions` (§21.2) — the single source of truth.
The install script and `perch doctor` both read that file; no version appears twice.

**Version pin policy (same as §2):** at implementation time, verify each dependency
against its official registry or release page and pin the **latest stable release that
is ≥ 30 days old**. Never pin a pre-release. Never pin from memory — always verify.
Update the pin only when intentionally upgrading.

| Dependency | Minimum | Required for | Notes |
|------------|---------|--------------|-------|
| `go` | 1.24 | build only | not needed to run the released binary; pin the exact patch release |
| `tmux` | 3.2 | runtime | 3.2 stabilised pane user-options (`@var`); system packages may be older — warn if below min, do not hard-fail |
| `git` | 2.20 | runtime | `worktree list --porcelain` added in 2.7; 2.20 for reliable worktree locking; not pinned (system-managed) |
| `claude` | any | runtime (≥ 1 agent required) | official native-binary installer; self-updates; pin the version verified at implementation time |
| `opencode` | 1.14 | runtime (≥ 1 agent required) | `session list --format json` added ≥ 1.14; pin the version verified at implementation time |

At least one of `claude` / `opencode` must be present. `perch doctor` enforces this
and prints a clear error if neither is found.

`node` (≥ 18) is consumed by the opencode plugin SDK at runtime inside opencode's own
bundled runtime — it is **not** a perch dependency and the install script does not
touch it.

### 21.2 `.tool-versions` (repo root, read by `install.sh` and `perch doctor`)

A plain text file, one `<tool> <version>` pair per line. Compatible with `asdf` and
`mise` for developers who use those. The **single source of truth** for all pinned
external tool versions — no version number appears anywhere else (not in the Makefile,
not in the install script, not in code).

Format:
```
golang   <version>
tmux     <version>
claude   <version>
opencode <version>
```

**At implementation time:** verify each version against its official source (§21.1
policy: ≥30 days old, stable, active) and write the pinned value here. The install
script reads this file with a POSIX `while read` loop — no special tooling required.
`perch doctor` reads it at runtime to detect version drift.

Rules:
- To upgrade a dependency: update this file and this file only.
- Never hardcode a version string anywhere else in the codebase.
- `git` is not listed because it is system-managed and not pinned by perch.

### 21.3 `scripts/install.sh` specification

`install.sh` lives at the **repo root** (where users expect it; `curl | sh` convention).
It is POSIX `sh` — no bashisms. Shebang: `#!/bin/sh`. A `shellcheck` pass is part of
`make lint`.

**Architecture detection:**

```sh
OS="$(uname -s)"    # Linux | Darwin
ARCH="$(uname -m)"  # x86_64 | aarch64 | arm64
# Normalise to Go's naming convention:
case "$ARCH" in
  x86_64)          ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac
case "$OS" in
  Linux)  GOOS="linux" ;;
  Darwin) GOOS="darwin" ;;
  *) die "Unsupported OS: $OS" ;;
esac
```

**Version parsing from `.tool-versions`:**

```sh
# Read a version for a named tool from .tool-versions in the repo root.
# Usage: tool_version go  →  the pinned version string
tool_version() {
  grep "^$1 " "${REPO_ROOT}/.tool-versions" | awk '{print $2}'
}
```

**Install steps (in order):**

1. **Go** — if `go version` output does not satisfy the minimum (§21.1):
   - Read `GO_VERSION` from `.tool-versions`.
   - Download `https://go.dev/dl/go${GO_VERSION}.${GOOS}-${ARCH}.tar.gz`
   - Fetch the expected SHA256 from `https://go.dev/dl/?mode=json&include=all` (one
     network call, parse with `grep`/`awk` — no `jq` dependency).
   - Verify with `sha256sum` (Linux) / `shasum -a 256` (macOS).
   - Extract to `/usr/local/go` (requires sudo on Linux; uses `sudo` only here if
     needed and only after printing what it will do).
   - Add `/usr/local/go/bin` to `PATH` for the rest of the script.

2. **tmux** — if `tmux -V` is absent or below minimum:
   - Detect package manager: `apt-get` (Debian/Ubuntu), `dnf` (Fedora/RHEL),
     `brew` (macOS), `pacman` (Arch). If none found, print instructions and
     continue (non-fatal if tmux is already present at any version — doctor will warn).
   - `sudo apt-get install -y tmux` / `sudo dnf install -y tmux` / `brew install tmux`.
   - Re-check version after install; if still below minimum, print a warning (system
     may have an older package) but do not fail — the user may be on an older LTS.

3. **git** — verify `git --version` ≥ 2.20. If absent or too old, print platform-
   specific install instructions and **exit 1** — git has too many distro/version
   variants to auto-install safely.

4. **claude** — if `claude` is absent:
   ```sh
   curl -fsSL https://cli.anthropic.com/install.sh | sh
   ```
   Then read `CLAUDE_VERSION` from `.tool-versions` and verify the installed version
   matches. If it differs (the upstream installer always fetches their latest), print:
   ```
   [warn] claude installed as X.Y.Z but .tool-versions pins A.B.C.
          Run 'claude update' or edit .tool-versions to match.
   ```
   Warning, not failure: claude self-updates and the pin is a reproducibility
   reference, not a constraint the external installer honours.
   Skipped with `--skip-agents`.

5. **opencode** — same pattern as claude using `OPENCODE_VERSION` from `.tool-versions`:
   ```sh
   curl -fsSL https://opencode.ai/install | sh
   ```
   Post-install version check; same warn-not-fail policy.
   Skipped with `--skip-agents`.

6. **Build perch** — `go build -trimpath -ldflags '...' -o "${INSTALL_PREFIX}/perch" ./cmd/perch`.
   `INSTALL_PREFIX` defaults to `/usr/local/bin` if writable, else `~/.local/bin`.
   Skipped with `--skip-build`.

7. **`perch setup`** — installs claude hooks and opencode plugin (§9, §21.5).
   Skipped with `--skip-setup`.

**Flags:**

| Flag | Effect |
|------|--------|
| `--skip-agents` | skip steps 4–5 (claude + opencode) |
| `--skip-build` | skip step 6 (perch binary build) |
| `--skip-setup` | skip step 7 (`perch setup`) |
| `--prefix=<path>` | override perch install destination |
| `--yes` | non-interactive; use defaults, don't prompt |

**Output format:** each step prints one of:
```
[skip]    go <version> already installed at /usr/local/go/bin/go
[install] tmux via apt-get
[ok]      tmux <version> installed
[warn]    claude <installed-ver> installed; .tool-versions pins <pinned-ver> — see above
```

**`curl | sh` supply-chain note:** perch itself is never distributed via pipe-to-shell
(§2). The agent installers (steps 4–5) use it because it is the only official install
path those projects provide. The post-install version check is the mitigation: if the
fetched script installs a version that deviates from the pin, the operator is warned
immediately so drift is visible. For fully locked environments, `--skip-agents` lets
the operator pre-install agents by other means.

**Idempotency:** every step checks the current state before acting. Re-running
`install.sh` on a fully provisioned machine prints `[skip]` for every step and exits 0.

**Verification (milestone 0):** the script is tested by running it inside:
```sh
docker run --rm -v "$(pwd)":/perch ubuntu:24.04   sh /perch/install.sh --skip-agents
docker run --rm -v "$(pwd)":/perch fedora:41       sh /perch/install.sh --skip-agents
docker run --rm -v "$(pwd)":/perch ubuntu:24.04   sh /perch/install.sh  # with agents
```
All three must exit 0 and `perch doctor` must pass before milestone 0 is complete.

### 21.4 `perch doctor` (enhanced specification)

`perch doctor` reads `.tool-versions`, checks every dependency, and reports clearly.
It is read-only — it never installs anything.

```
$ perch doctor
perch v0.1.0-dev

  [ok]   go        <version>   /usr/local/go/bin/go
  [ok]   tmux      <version>   /usr/bin/tmux
  [ok]   git       <version>   /usr/bin/git
  [ok]   claude    <version>   ~/.local/bin/claude
  [warn] opencode  not found (at least one agent is required — ok if claude present)
  [ok]   hooks     claude hooks present in ~/.claude/settings.json
  [warn] hooks     perch-status.ts not installed — run 'perch setup'
  [warn] tmux      server not running (will start automatically on first session)

1 warning. Run 'perch setup' to fix the hook issue.
```

Exit code: `0` if all required deps present (agents: at least one); `1` if a hard
requirement is missing; warnings do not affect exit code.

### 21.5 `perch setup` — hook coexistence (additive, never destructive)

`perch setup` is **additive by default**. It never removes or replaces another tool's
hooks. This is the correct default for the wider audience.

- **Claude** (`~/.claude/settings.json`): Perch merges its hooks into the existing
  hook arrays. If a `PostToolUse` hook array already exists (workmux, custom), perch
  appends its entry. Existing entries are untouched. The file is written atomically
  (temp + rename).

- **opencode** (`~/.config/opencode/plugins/perch-status.ts`): Written as a new file.
  Any existing `workmux-status.ts` or other plugin files are left in place. Both files
  are auto-discovered by opencode. Both will fire. `perch status set` and
  `workmux set-window-status` target different CLIs and different tmux options — they
  do not conflict.

- **`perch setup --replace`** (explicit opt-in only): removes conflicting hooks/plugins
  after user confirmation. Never the default. Documented as "for single-tool setups
  where you want perch to be the only status reporter."

---

## 19. Decisions captured (so the handoff agent doesn't re-litigate)

- **Go single binary + bubbletea TUI driving tmux/git** — chosen over a shell layer
  for true cross-platform consistency, a maintainable live-status TUI, and one-file
  distribution.
- **Stable Charm v1 line** (bubbletea v1.3.10 / lipgloss v1.1.0 / bubbles v1.0.0,
  mutually pinned, `github.com/charmbracelet/*`; Go 1.24+) — chosen over the newer
  v2 to minimise blast radius: mature, heavily documented, proven. v2 is a future
  migration, not a v1 dependency. API carried inline (§12).
- **Reduce blast radius as a standing principle** — mature deps, small tree, every
  fragile/undocumented integration isolated behind an interface and degraded
  gracefully on failure (§1, §4 defensive parsing, §17 no daemon).
- **Supply-chain hardening (deps are an attack vector)** — exact-pin every direct
  dep + the Go `toolchain`; commit `go.sum` (locks transitives by hash); **vendor**
  all deps and build `-mod=vendor -trimpath` (hermetic, reproducible); only adopt
  versions **≥30 days old, active, stable**; `govulncheck` + `go mod verify` before
  release; dev tools pinned via `go run tool@version`; no `curl|bash`. Full policy in §2.
- **CLI = stdlib `flag`, not cobra** — a 7-verb surface doesn't justify the
  dependency; dropping it shrinks the attack surface. cobra is the documented
  escape hatch only if the CLI grows complex.
- **Efficiency beats prettiness on conflict** — visual polish is a goal, but
  responsiveness is the constraint; drop the effect, never the smoothness (§1).
- **Two JSON stores, no DB, no daemon** — `state.json` (worktree choices + frecency,
  TUI-written) and per-window `windows/<paneKey>.json` (resurrect-critical, isolated
  per file to avoid concurrent-writer clobbering). The live window↔session binding
  lives in tmux pane options; the shadow records exist only to survive a server
  restart.
- **Linkage in perch's own JSON, not `git config`** — workmux stores mux metadata in
  `git config` because it is repo-scoped; perch is root-scoped, multi-repo, and
  supports non-git directories, so a git-config layer can't cover the open-anywhere
  case. One JSON mechanism covers every tree type.
- **Sessions are directory-keyed by the tools**; perch reads that mapping rather
  than inventing it.
- **Worktrees are optional**, surfaced via the 3-way prompt only when relevant
  (git repos), remembered after first choice.
- **Frecency (zoxide algorithm) for selector ordering** — exact formula in §6.3.
- **Adapters, not a hardcoded agent switch** — ready-state + trust-prompt as data.
- **v1 = Claude + opencode**, adapter-extensible.
- **Root = launch cwd**, scan for git repos beneath, with an open-anywhere escape
  hatch for non-git directories.
- **Prior art credited inline.** Status-hook events, tmux status options +
  auto-clear-on-focus, worktree placement, deferred-cleanup-via-`tmux run-shell`,
  boot_id resurrect, relative-symlink file seeding, and project-config discovery
  were studied from **workmux** (Rust, github.com/raine/workmux); the connect
  sequence and config schema from **sesh**; the admin-table/command-bar/generated-
  help patterns from **k9s** and **lazygit**; the frecency algorithm from
  **zoxide**; the competitive frame from **claude-squad**. perch reimplements the
  *ideas* in Go — not the code.
```