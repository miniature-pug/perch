# perch Agent-Cockpit Implementation Plan

> **⚠ Historical planning document.** This plan records the original design and
> contains contracts that were superseded during implementation — notably a
> `Caps{Tokens}` bit and token/cost metering (removed entirely; perch is not a
> usage meter) and an earlier opencode SSE event API. For the authoritative
> as-built design, see [`ARCHITECTURE.md`](../../../ARCHITECTURE.md). Kept for
> traceability; do not treat its contracts as current.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild perch as a worktree-native, keyboard-first Linux GUI cockpit that runs Claude Code and opencode in direct-pty terminals across git worktrees, with desktop notifications, inline tool-call approvals, visual diffs, and a thin real editor — replacing the deleted tmux/Bubble-Tea architecture.

**Architecture:** Go backend (Wails v2.12.0, WebKit2GTK) binds one `App` object to a Svelte 5 SPA. One **direct pty per pane** runs `$SHELL -l` in a worktree; the agent is launched inside that shell. State-bearing features (attention, approvals, tokens) come from an **agent-specific side-channel** — Claude via a localhost token-authed hook listener + transcript tail; opencode via `serve` + SSE + REST — abstracted behind a `Monitor` interface that composes the existing `agent.Adapter` (argv/config). A workspace registry (JSON) survives GUI close; worktrees and agent conversations persist on disk and resume.

**Tech Stack:** Go 1.25 · Wails v2.12.0 · creack/pty v1.1.24 · Svelte 5.55 · Vite 8 · xterm.js 6 + addon-fit · CodeMirror 6 · Vitest 4 + @testing-library/svelte 5 · fsnotify · golangci-lint / govulncheck.

---

## Scope & Plan Segmentation

This is one feature-complete product (the user's explicit mandate: no phased product cuts). The plan document is **segmented into six phases purely for execution ordering**; each phase yields working, testable software on its own. This segmentation is a build order, **not** product versioning — every listed feature ships.

- **Phase 0 — Demolition & Scaffold:** delete tmux/tui/frame + Bubble Tea deps; repoint the pty bridge off tmux; keep the build green.
- **Phase 1 — Validation Spikes (gated, early):** five experiment tasks that de-risk documented-but-unverified Claude/Wails behaviors. Each has a **named fallback** wired to a `Caps` bit, so a failed spike flips a capability flag and hides one UI surface — it never forces re-architecture. Contracts (below) are locked **before** spikes resolve; spike scaffolding becomes the first real code of its package.
- **Phase 2 — Backend Core:** `pty`, `registry`, `git` (hunk stage/discard), `fs`, `notify`, `hooklistener`, `agent` Monitors (+ fake). Pure Go, TDD with fakes/temp repos; never runs a real `claude`/`opencode`.
- **Phase 3 — App & CLI:** rewrite `app` bound methods to direct-pty + Monitor + registry + git + fs; rework `cmd/perch` (GUI default; keep setup/doctor/version; fold attach/resurrect/status).
- **Phase 4 — Frontend:** design tokens + 9 themes, the Stage/split/layout-persistence shell, Terminal, Editor, DiffView, FileTree, ShellDrawer, MenuBar, Sidebar, CommandPalette, NotificationHub, ApprovalCard, dialogs, Preview, vim modal model, drag-drop.
- **Phase 5 — Integration & Gates:** fake-agent end-to-end smoke, quality gates (vet/lint/govulncheck/tsc, production ELF build), docs/diagrams refresh.

---

## File Structure

Created or substantially rewritten (responsibility in parens):

```
cmd/perch/main.go        REWRITE  CLI dispatch: GUI default + setup/doctor/version; attach→focus, resurrect/status→registry
cmd/perch/gui.go         KEEP     launchGUI seam → app.Run
app/app.go               REWRITE  Wails App: new bound methods (workspaces, pty, approvals, git, fs, layout, settings)
app/options.go           KEEP±    Run(): no-port production Wails config; add DisableWebViewDrop (spike 4)
internal/pty/bridge.go   REWRITE  direct-pty: Spawn(cwd, argv, …) — drop tmux import; keep []int pump + Bridge
internal/registry/       CREATE   workspace registry (JSON, atomic, $XDG_CONFIG_HOME/perch/workspaces.json)
internal/git/hunk.go     CREATE   per-file unified hunks + stage/discard at hunk granularity
internal/git/*.go        KEEP±    existing Diff/DiffStat/worktree; add Branches/Worktrees enumeration
internal/fs/             CREATE   ListDir (gitignore-aware), ReadFile/WriteFile, fsnotify Watcher, RevealInFiles/CopyPath
internal/notify/         CREATE   Linux OS desktop notifications (dbus/notify-send seam) + FakeNotifier
internal/hooklistener/   CREATE   localhost token-authed HTTP server for Claude hooks (PreToolUse/Stop/SessionStart)
internal/agent/monitor.go CREATE  Monitor interface + Caps/Event/State/Decision + registry of monitors
internal/agent/claude_monitor.go   CREATE  ClaudeMonitor: composes Claude Adapter + hooklistener + transcript tail
internal/agent/opencode_monitor.go CREATE  OpencodeMonitor: composes Opencode Adapter + serve + SSE + REST
internal/agent/fake_monitor.go     CREATE  scripted fake Monitor for app/e2e tests
internal/agent/adapter.go          KEEP     existing config/argv Adapter (Name/Detect/ListSessions/ResumeArgs/NewArgs/InstallStatusHook)
frontend/src/tokens/     CREATE   design tokens (CSS custom props) + 9 theme palettes + ThemeProvider
frontend/src/lib/        CREATE±  Svelte 5 components (see §Frontend Contracts); rewrite existing Terminal/Sidebar/etc.
frontend/src/lib/stores/ CREATE   layout, mode (vim), notifications, settings stores (runes)
```

Deleted entirely: `internal/tmux/`, `internal/frame/` (if present), the tmux attach-pty bridge behavior, `internal/state`, `internal/resurrect`, `internal/status`, `internal/trust` (re-evaluated in Task 0.x), and all `charmbracelet/*` deps.

---

## Shared Contracts (authoritative — every task conforms to these names/signatures)

These types and signatures are **frozen**. If a task needs a name not here, it is a plan bug — stop and reconcile, do not invent a divergent name.

### Go — `internal/pty`

```go
// EmitFunc delivers a named event with optional payload to the frontend (wraps
// wails runtime.EventsEmit in prod; captured in tests). UNCHANGED from existing.
type EmitFunc func(event string, data ...any)

// LoginShellArgv returns the interactive login-shell argv: [$SHELL, "-l"],
// falling back to ["/bin/bash", "-l"] when $SHELL is unset.
func LoginShellArgv() []string

// Spawn starts argv[0] argv[1:] inside a pty in working directory cwd, pumping
// output to emit on `event` as bounded []int chunks (≤ maxChunk). No tmux.
// Closing the returned Bridge kills the process group and reaps it.
func Spawn(ctx context.Context, cwd string, argv []string, event string, emit EmitFunc, cols, rows uint16) (*Bridge, error)

// Bridge — UNCHANGED public surface: Write([]byte)(int,error); Resize(cols,rows uint16) error; Close() error.
```

### Go — `internal/registry`

```go
type Workspace struct {
    ID            string    `json:"id"`            // stable opaque id (validateSessionID charset)
    WorktreePath  string    `json:"worktreePath"`  // abs, validated under roots
    Agent         string    `json:"agent"`         // "claude" | "opencode"
    LastSessionID string    `json:"lastSessionID"` // resume target ("" = fresh)
    Title         string    `json:"title"`
    LastActive    time.Time `json:"lastActive"`
}

type Store struct{ /* unexported: path, mu, items */ }

func Load(configDir string) (*Store, error)      // reads workspaces.json; missing file = empty store
func (s *Store) List() []Workspace               // sorted by LastActive desc
func (s *Store) Get(id string) (Workspace, bool)
func (s *Store) Upsert(w Workspace) error        // atomic temp+rename write
func (s *Store) Remove(id string) error
func DefaultConfigDir() string                   // $XDG_CONFIG_HOME/perch or ~/.config/perch
```

### Go — `internal/git` (additions; existing Diff/DiffStat/worktree retained)

```go
type FileDiff struct { Path string `json:"path"`; Added int `json:"added"`; Removed int `json:"removed"`; Status string `json:"status"` } // status: "M"|"A"|"D"|"R"|"?"

type HunkLine struct { Kind string `json:"kind"` /* "ctx"|"add"|"del" */; Text string `json:"text"` }
type Hunk struct {
    File     string     `json:"file"`
    Index    int        `json:"index"`     // 0-based position within the file's hunk list
    Header   string     `json:"header"`    // the @@ -a,b +c,d @@ line
    OldStart int        `json:"oldStart"`; OldLines int `json:"oldLines"`
    NewStart int        `json:"newStart"`; NewLines int `json:"newLines"`
    Lines    []HunkLine `json:"lines"`
}

func DiffStat(worktree string) ([]FileDiff, error)
func Hunks(worktree, file string) ([]Hunk, error)
func StageHunk(worktree, file string, index int) error    // git apply --cached  (verbatim Nth hunk of file; index relative to current Hunks output)
func DiscardHunk(worktree, file string, index int) error  // git apply --reverse (verbatim Nth hunk of file; re-derive hunks after each call)
func Branches(repo string) ([]string, error)
type WorktreeInfo struct { Path string `json:"path"`; Branch string `json:"branch"`; Head string `json:"head"` }
func Worktrees(repo string) ([]WorktreeInfo, error)
```

### Go — `internal/fs`

```go
type Node struct {
    Name  string `json:"name"`
    Path  string `json:"path"`  // abs
    IsDir bool   `json:"isDir"`
    // children fetched lazily per directory; not embedded
}

func ListDir(absDir string, gitignoreAware bool) ([]Node, error) // sorted dirs-first, name asc; honors .gitignore when true
func ReadFile(absPath string) ([]byte, error)
func WriteFile(absPath string, data []byte) error                // atomic temp+rename, mode-preserving
func RevealInFiles(absPath string) error                         // xdg-open the containing dir
func CopyPath(absPath string) string                             // returns the path (clipboard write is frontend-side)

type Watcher struct{ /* unexported */ }
func Watch(absRoot string, onChange func(absPath string)) (*Watcher, error) // fsnotify, recursive
func (w *Watcher) Close() error
```

### Go — `internal/notify`

```go
type Notifier interface { Notify(title, body string) error }
func New() Notifier            // Linux: dbus org.freedesktop.Notifications, fallback notify-send
type FakeNotifier struct{ Calls []struct{ Title, Body string } } // test double; implements Notifier
```

### Go — `internal/hooklistener`

```go
type Decision struct { Allow bool `json:"allow"`; Always bool `json:"always"` }

type HookEvent struct {
    Type           string          `json:"hook_event_name"` // "PreToolUse"|"Stop"|"StopFailure"|"SessionStart"|"Notification"
    SessionID      string          `json:"session_id"`
    TranscriptPath string          `json:"transcript_path"` // Claude supplies this in the payload — no slug algorithm needed for live path
    Cwd            string          `json:"cwd"`
    ToolName       string          `json:"tool_name"`       // PreToolUse only
    ToolInput      json.RawMessage `json:"tool_input"`      // PreToolUse only
    ErrorType      string          `json:"error_type"`      // StopFailure only
    ReqID          string          `json:"-"`               // assigned by listener for approval correlation
}

type Listener struct{ /* unexported: srv, ln, token, events, pending */ }
func New() (*Listener, error)                 // binds 127.0.0.1:0; generates 256-bit bearer token
func (l *Listener) Addr() string              // "127.0.0.1:NNNNN"
func (l *Listener) Token() string
func (l *Listener) Events() <-chan HookEvent  // SessionStart/Stop/StopFailure/Notification + PreToolUse(req)
func (l *Listener) Decide(reqID string, d Decision)  // unblocks a pending PreToolUse response
func (l *Listener) Close() error
// HTTP: POST /hook  (Authorization: Bearer <token>; body = Claude hook JSON on stdin-equivalent)
//   PreToolUse → blocks until Decide(reqID) → responds {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"|"deny"}}
//   others     → 200, event pushed to Events()
```

### Go — `internal/agent` (new live layer; existing `Adapter` retained & composed)

```go
type State string
const ( StateRunning State = "running"; StateIdle State = "idle"; StateAwaitingApproval State = "awaiting-approval"; StateDone State = "done"; StateErrored State = "errored" )

type Caps struct { Approvals bool `json:"approvals"`; Attention bool `json:"attention"`; Tokens bool `json:"tokens"` }

type ApprovalReq struct { ReqID string `json:"reqId"`; Tool string `json:"tool"`; Summary string `json:"summary"` }
type Decision struct { Allow bool; Always bool } // mirrors hooklistener.Decision

type Event struct {
    WorkspaceID string       `json:"workspaceId"`
    Kind        string       `json:"kind"`     // "state"|"usage"|"approval"|"tool"
    State       State        `json:"state"`
    Tokens      int          `json:"tokens"`
    Cost        float64      `json:"cost"`
    Approval    *ApprovalReq `json:"approval,omitempty"`
    Err         string       `json:"err,omitempty"`
}

// Monitor owns the structured side-channel for one workspace's agent. It does
// NOT own the pty (app spawns $SHELL -l); it returns the shell command to launch
// the agent inside that pty, and streams structured Events independently.
type Monitor interface {
    // Prepare sets up the side-channel (hooks/serve) for cwd+resumeID and returns
    // the shell command line to type into the pane's pty to launch the agent.
    Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
    Events() <-chan Event
    Approve(reqID string, d Decision) error
    Capabilities() Caps
    Teardown() error            // remove hook config / stop serve; idempotent
    CurrentState() State        // last observed lifecycle state (mutex-guarded); StateIdle before the first event
    LastApprovalTool() string   // tool name of the most recent PreToolUse approval request ("" if none yet)
}

func NewMonitor(tool string, adapter Adapter) (Monitor, error) // "claude"→ClaudeMonitor, "opencode"→OpencodeMonitor
```

**Caps fallback table (spike outcome → degradation):**

| Spike | If it fails | Caps effect | UI degradation |
|---|---|---|---|
| 1 PreToolUse interception | approve in-terminal | `Approvals=false` | hide ApprovalCard; agent prompts in pane |
| 2 transcript token/cost | — | `Tokens=false` | hide token/cost meter |
| 3 opencode SSE | opencode only | opencode `Approvals/Tokens=false` | Claude (primary) unaffected |
| 4 OnFileDrop #3686 | open-file dialog | n/a (frontend) | OS drop-in → "Open file…" + Copy-path |
| 5 OS notify | in-app only | n/a (`notify.New` returns no-op) | NotificationHub still works |

### Go — `app` bound methods (IPC surface `window.go.app.App.<Method>`)

```go
func (a *App) ListWorkspaces() []WorkspaceVM
func (a *App) CreateWorkspace(agent, repoPath, branch, model string) (WorkspaceVM, error)
func (a *App) OpenWorkspace(id string) error            // spawn pane pty + Monitor.Prepare + write launchCmd
func (a *App) CloseWorkspace(id string) error           // Bridge.Close + Monitor.Teardown (workspace stays in registry)
func (a *App) RemoveWorkspace(id string) error          // destructive: also drop from registry
func (a *App) WriteToPty(paneID string, data []int) error
func (a *App) ResizePty(paneID string, cols, rows uint16) error
func (a *App) OpenShell(paneID, cwd string) error       // pinned shell drawer (its own pty)
func (a *App) Approve(reqID, decision string) error     // decision: "allow"|"deny"|"always"
func (a *App) DiffStat(worktree string) ([]git.FileDiff, error)
func (a *App) Hunks(worktree, file string) ([]git.Hunk, error)
func (a *App) StageHunk(worktree, file string, index int) error
func (a *App) DiscardHunk(worktree, file string, index int) error
func (a *App) ListDir(absDir string) ([]fs.Node, error)
func (a *App) ReadFile(absPath string) (string, error)
func (a *App) WriteFile(absPath, content string) error
func (a *App) RevealInFiles(absPath string) error
func (a *App) Branches(repo string) ([]string, error)
func (a *App) Worktrees(repo string) ([]git.WorktreeInfo, error)
func (a *App) GetLayout() (string, error)               // JSON blob, opaque to Go
func (a *App) SaveLayout(layoutJSON string) error
func (a *App) GetSettings() (Settings, error)
func (a *App) SaveSettings(s Settings) error

type WorkspaceVM struct {
    ID           string      `json:"id"`
    WorktreePath string      `json:"worktreePath"`
    Agent        string      `json:"agent"`
    Title        string      `json:"title"`
    Branch       string      `json:"branch"`
    State        agent.State `json:"state"`
    Caps         agent.Caps  `json:"caps"`
    PaneID       string      `json:"paneId"`
    LastActive   time.Time   `json:"lastActive"`
}
type Settings struct {
    Theme    string `json:"theme"`    // default "gruvbox"
    Density  string `json:"density"`  // "dense"|"comfortable"|"ultra"
    Font     string `json:"font"`     // "geist"|"ibm-plex"|"inter"
    DND      bool   `json:"dnd"`
    AlwaysRules []AlwaysRule `json:"alwaysRules"`
}
type AlwaysRule struct { Agent, Tool, Pattern string }
```

### Wails events (Go → JS), via `runtime.EventsEmit`

| Event name | Payload | Emitted by |
|---|---|---|
| `pty:data:<paneID>` | `number[]` (the `[]int` chunk) | pty pump |
| `pty:exit:<paneID>` | `{ code:number }` | pty Close/exit |
| `agent:event` | `agent.Event` (carries `workspaceId`) | Monitor pump |
| `fs:changed` | `{ workspaceId, path }` | fs.Watcher |
| `notify` | `{ tier:"blocking"|"ambient"|"routine", title, body, workspaceId }` | App notify dispatch |

### Frontend — components (`frontend/src/lib/`) & store contracts (Svelte 5 runes, callback props — no `createEventDispatcher`)

```
App.svelte            root; ThemeProvider wrapper; owns layout store; routes keymap by mode
MenuBar.svelte        props: { onCommand:(id:string)=>void }; Session/Worktree/View/Agent/Help menus + notification bell
Sidebar.svelte        props: { workspaces:WorkspaceVM[], activeId:string, onSelect:(id)=>void, onNew:()=>void }
Stage.svelte          props: { view:"agent"|"code"|"diff", split:boolean, workspace:WorkspaceVM, onView:(v)=>void, onSplit:()=>void }
Terminal.svelte       props: { paneId:string, cwd:string }; xterm.js + FitAddon; subscribes pty:data:<paneId>, sends WriteToPty/ResizePty
Editor.svelte         props: { path:string|null, worktree:string }; CodeMirror 6 editable + git gutter; save → WriteFile
DiffView.svelte       props: { worktree:string }; DiffStat list + per-file Hunks + Stage/Discard buttons
FileTree.svelte       props: { root:string, onOpen:(path)=>void }; lazy ListDir; action menu (Open/Reveal/Copy path/Send to agent)
ShellDrawer.svelte    props: { paneId:string, cwd:string }; pinned Terminal instance; collapsible
CommandPalette.svelte props: { open:boolean, commands:Command[], onRun:(id)=>void, onClose:()=>void }; fuzzy, prefix-grouped
NotificationHub.svelte props: { items:Notification[], dnd:boolean, onDismiss, onToggleDnd, onClearRead }
ApprovalCard.svelte   props: { req:ApprovalReq, queue:ApprovalReq[], onDecision:(reqId,"allow"|"deny"|"always")=>void }
NewSessionDialog.svelte props: { open, repos:string[], branches:string[], onCreate:(agent,repo,branch,model)=>void, onClose }
ConfirmDialog.svelte  props: { open, message:string, onConfirm:()=>void, onCancel:()=>void }  // + undo affordance for destructive ops
Preview.svelte        props: { path:string, kind:"markdown"|"mermaid"|"image" }
ThemeProvider.svelte  props: { theme:string }; sets data-theme + density on :root

stores/layout.svelte.ts        $state layout {sidebarW, shellH, view, split, collapsed{}}; persists via SaveLayout (debounced)
stores/mode.svelte.ts          $state mode:"normal"|"terminal"|"command"; transitions per §Vim
stores/notifications.svelte.ts $state items[]; addBlocking/addAmbient/addRoutine; dnd
stores/settings.svelte.ts      $state Settings; loads GetSettings on boot; SaveSettings on change
wails.ts                       typed wrappers over window.go.app.App.* + EventsOn helpers
```

### Frontend — design tokens (CSS custom properties, prefix `--perch-`)

Structure/type/spacing constant across themes; **only palette vars** change per `[data-theme]`. Token names (frozen):
`--perch-bg`, `--perch-bg-elev`, `--perch-surface`, `--perch-border`, `--perch-text`, `--perch-text-dim`, `--perch-accent`, `--perch-accent-fg`, `--perch-ok`, `--perch-warn`, `--perch-err`, `--perch-info`; type: `--perch-font-sans`, `--perch-font-mono`, `--perch-fs-code:14px`, `--perch-lh-code:1.55`, `--perch-fs-shell:13px`, `--perch-lh-shell:1.5`, `--perch-fs-body:13px`, `--perch-fs-caption:12px`, `--perch-fs-label:11px`; spacing scale `--perch-sp-1..-8` (8pt grid, dense default = ×0.75 multiplier on a `[data-density]` attr); motion `--perch-dur:120ms`, `--perch-ease:cubic-bezier(.4,0,.2,1)`. Status is always **color + icon + label**; never color alone. No pure `#fff`/`#000`. Contrast: text ≥ 4.5:1, borders/icons/focus ≥ 3:1 (WCAG 2.2 AA). 9 themes: `gruvbox`(default), `tokyo-night`, `catppuccin`, `dracula`, `nord`, `rose-pine`, `one-dark`, `perch-cyan`, `light`.

### Frontend — vim modal model + keymap (authoritative)

Status-line shows the mode. **Mouse is always primary and never gated by mode.**

| Mode | Enter via | Leave via | Keys (in this mode) |
|---|---|---|---|
| NORMAL | default; `Ctrl-\ Ctrl-n` from terminal; click chrome | `i`→terminal; `:`→command | `j/k` move sessions · `gt/gT` next/prev tab · `1/2/3` Agent/Code/Diff view · `gd` diff · `ge` editor · `` ^` `` shell drawer · `\` split · `/` filter sessions · `:` command · `⏎` open selected · `i` enter terminal |
| TERMINAL | `i` in normal; click terminal | `Ctrl-\ Ctrl-n`; click chrome | all keys pass through to agent/shell pty |
| COMMAND | `:` or `⌘/Ctrl-K` | `Esc`; run | palette/command input |

### Status icon mapping (color + icon + label)

`◐ running` · `◯ idle` · `⚠ needs you` (awaiting-approval) · `✓ done` · `✗ error`. Drives Sidebar, notification tiers (§8), token meter.

---

## Conventions every task follows

- **TDD:** write failing test → run it red → minimal impl → run green → commit. Each step is one 2–5 min action.
- **Test isolation (security-critical):** never run a real `claude`/`opencode`; never write the real `$HOME` (`t.Setenv("HOME", t.TempDir())`); never touch a shared server. Use the fake agent / fixtures.
- **Go commands:** unit `go test -race -count=1 ./...`; a single test `go test -race -run TestName ./internal/pkg`. Build the GUI `make gui-build`. Lint `make lint` (golangci-lint), `make vet`, `make vulncheck` (govulncheck).
- **Frontend commands:** `npm --prefix frontend test` (Vitest), single file `npm --prefix frontend test -- src/lib/X.test.ts`, type-check `npm --prefix frontend run check` (tsc).
- **Idiom references (read before writing code in that layer):** Go pty/Wails-emit pattern → `internal/pty/bridge.go`; Svelte 5 runes + xterm mount idiom → `frontend/src/lib/Terminal.svelte`; existing Adapter idiom → `internal/agent/claude.go`. Match these; do not reinvent library calls the repo already uses correctly.
- **Branch:** all work on `feat/perch-v1`. Never commit to `main`. Frequent commits (one per task minimum).
- **Validation gating:** Phase-1 spike tasks record findings to `docs/superpowers/spikes/`. A dependent feature task reads the recorded `Caps` outcome; if a spike chose its fallback, the feature implements the fallback path and the relevant `Caps` bit is set false.

---

<!-- TASKS:BEGIN -->

## Phase 0 — Demolition & Scaffold

### Task 0.1: Delete `internal/tmux`; stub non-CLI callers; trim `main.go`/`main_test.go` to green

`internal/tmux` is imported by eight files outside the package. Every one must be cleared **in this task** so `go build ./...` is green at the end. The attach CLI surface is tmux all the way down, so it must also be removed here (not deferred to 0.2).

**Callers of `internal/tmux` (non-test, outside the package):**
- `internal/pty/bridge.go` — old Spawn signature; replace with frozen-signature stub (full impl in 0.3)
- `internal/status/status.go` — `Set`/`Deps` call `tmux.SetPaneOption`; replace with no-op stub; keep `Machine` pure logic
- `internal/resurrect/resurrect.go` — uses `tmux.New()`; replace whole file with a stub (deleted in 0.2)
- `internal/worktree/deferred.go` — calls `t.RunShell` + `state.RemoveWindow`; stub to `return nil`
- `internal/attach/attach.go` — `Deps.Tmux tmux.Tmux`; replace whole file with minimal compile stub (deleted in 0.2)
- `app/app.go` — `App.tmux tmux.Tmux`, `CreateAgent`, `ListSessions`, etc.; replace every tmux call with a stub (full rewrite Phase 3)
- `cmd/perch/main.go` — `handleAttach`/`handleResurrect`/`handleStatus`/`handleDebugTmux` + `attachDeps`/`attachProduction`/`attachCore`; delete all of it; remove tmux import
- `cmd/perch/main_test.go` — imports `tmux`, `proc`, `attach`, `model`; all attach/resurrect/status tests; must be trimmed in this task so the test binary compiles

**Other deletions:**
- `internal/tmux/` (entire package)
- `internal/pty/integration_test.go` (has `//go:build integration`; calls old `Spawn(ctx, tmux.Tmux, …)`)

**Files:**
- Delete: `internal/tmux/`, `internal/pty/integration_test.go`
- Modify: `internal/pty/bridge.go`, `internal/status/status.go`, `internal/resurrect/resurrect.go`, `internal/worktree/deferred.go`, `internal/attach/attach.go`, `app/app.go`, `cmd/perch/main.go`, `cmd/perch/main_test.go`
- Test: `go build ./... && go test -race -count=1 ./...`

- [ ] **Step 1: Delete `internal/tmux/` and the integration test**
  ```bash
  rm -rf internal/tmux
  rm internal/pty/integration_test.go
  ```

- [ ] **Step 2: Stub `internal/pty/bridge.go`**

  Replace the entire file. The frozen `Spawn` signature is declared; the real implementation lands in Task 0.3. The existing `bridge_test.go` tests (Write, Resize, pumpReader) continue to compile and pass because they never call `Spawn`.

  ```go
  // Package pty provides a direct pseudo-terminal bridge per pane (no tmux).
  package pty

  import (
  	"context"
  	"fmt"
  	"io"
  	"os"
  	"sync"
  )

  // EmitFunc delivers a named event with optional payload to the frontend.
  type EmitFunc func(event string, data ...any)

  // Bridge owns one pseudo-terminal.
  type Bridge struct {
  	mu      sync.Mutex
  	ptyFile io.WriteCloser
  	closer  func() error
  	setsize func(cols, rows uint16) error
  }

  func (b *Bridge) Write(p []byte) (int, error) {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.ptyFile == nil {
  		return 0, os.ErrClosed
  	}
  	return b.ptyFile.Write(p)
  }

  func (b *Bridge) Resize(cols, rows uint16) error {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.setsize == nil {
  		return nil
  	}
  	return b.setsize(cols, rows)
  }

  func (b *Bridge) Close() error {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.closer == nil {
  		return nil
  	}
  	c := b.closer
  	b.closer = nil
  	b.ptyFile = nil
  	return c()
  }

  const maxChunk = 16 * 1024

  // LoginShellArgv returns [$SHELL, "-l"], falling back to ["/bin/bash", "-l"].
  func LoginShellArgv() []string {
  	sh := os.Getenv("SHELL")
  	if sh == "" {
  		sh = "/bin/bash"
  	}
  	return []string{sh, "-l"}
  }

  // Spawn is the frozen direct-pty signature. Implemented in Task 0.3.
  func Spawn(_ context.Context, _ string, argv []string, _ string, _ EmitFunc, _ uint16, _ uint16) (*Bridge, error) {
  	if len(argv) == 0 {
  		return nil, fmt.Errorf("pty Spawn: argv must not be empty")
  	}
  	return nil, fmt.Errorf("pty Spawn: not yet implemented")
  }

  func pumpReader(r io.Reader, event string, emit EmitFunc, maxChunk int) {
  	buf := make([]byte, maxChunk)
  	for {
  		n, err := r.Read(buf)
  		if n > 0 {
  			out := make([]int, n)
  			for i := 0; i < n; i++ {
  				out[i] = int(buf[i])
  			}
  			emit(event, out)
  		}
  		if err != nil {
  			return
  		}
  	}
  }
  ```

- [ ] **Step 3: Stub `internal/status/status.go`**

  Remove the `"github.com/Miniature-Pug/perch/internal/tmux"` import and the `"context"` import (neither is referenced by `Machine`). Replace `Deps` and `Set`:
  ```go
  // Deps stub — tmux removed; status delivery flows through agent.Monitor events.
  type Deps struct{}

  // Set is a no-op stub.
  func Set(_ context.Context, _ Deps, _, _ string) error { return nil }
  ```
  Keep `"context"` in the import block only if any remaining code uses it; `Machine` and its methods do not, so remove it. Keep all `Machine` code verbatim.

  **Note:** `"context"` IS needed in the file signature for `Set`'s parameter — keep it.

- [ ] **Step 4: Stub `internal/resurrect/resurrect.go`**

  Replace the entire file. The package is deleted in Task 0.2 immediately after this task; the stub exists only so `cmd/perch/main.go` compiles with the `handleResurrect` stub below.
  ```go
  package resurrect

  import "context"

  // Deps stub — tmux and state removed; package deleted in Task 0.2.
  type Deps struct{}

  // Report stub.
  type Report struct {
  	Restored []string
  	Pruned   []string
  	Kept     []string
  	Skipped  []struct{ PaneKey, Tree, Reason string }
  }

  // Reconcile stub — always returns an empty report.
  func Reconcile(_ context.Context, _ Deps) (Report, error) { return Report{}, nil }
  ```
  Also delete the existing test files — `resurrect_test.go` imports `tmux` and `state` (confirmed), so it cannot compile after tmux is deleted. `integration_test.go` also imports tmux. Delete both now; the whole package is removed in Task 0.2:
  ```bash
  rm internal/resurrect/resurrect_test.go internal/resurrect/integration_test.go
  ```
  Only `resurrect.go` (now the stub) should remain in the directory.

- [ ] **Step 5: Stub `internal/worktree/deferred.go`**

  Drop `tmux` and `state` imports; return nil:
  ```go
  package worktree

  import "context"

  // DeferredRemove is a no-op stub. Teardown is handled by the registry (Phase 3).
  func DeferredRemove(_ context.Context, _ string, _ string, _ int64, _ string) error {
  	return nil
  }
  ```

- [ ] **Step 6: Stub `internal/attach/attach.go`**

  Replace with minimal types so the file compiles. The package is deleted in Task 0.2. Delete any other `attach/*.go` files that import tmux now.
  ```go
  // Package attach is a stub pending deletion in Task 0.2.
  package attach

  import "context"

  type Candidate struct {
  	Project, Branch, Tool, TmuxSession, TmuxWindow, LiveTarget string
  	IsLive                                                      bool
  }
  type Result struct {
  	Count     int
  	Matched   *Candidate
  	Ambiguous []Candidate
  }
  type Deps struct{}

  func Gather(_ context.Context, _ Deps) ([]Candidate, error) { return nil, nil }
  func Resolve(_ string, _ []Candidate) Result                 { return Result{} }
  func FormatAmbiguous(_ []Candidate) string                   { return "" }
  ```

- [ ] **Step 7: Stub `app/app.go`**

  **Exact import set after stubs** (verified against surviving code):
  - REMOVE: `tmux`, `state`, `model`, `worktree`, `config` (only `CreateAgent` used it; that's now stubbed)
  - KEEP: `context`, `crypto/rand`, `fmt`, `path/filepath`, `strings`, `sync`, `time`, `internalpty` (pty bridge), `agent`, `gitpkg`, `proc` (`App.run proc.Runner` is used by `Diff` which calls `gitpkg` via `a.run`), `wailsruntime`

  `App.tmux` field: remove. `App.run` field: **keep** — `Diff` calls `gitpkg.Diff(ctx, a.run, …)` and `gitpkg.DiffStat(ctx, a.run, …)`.

  `NewApp`: remove `tmux: tmux.New()` from the struct literal; keep `run: proc.ExecRunner{}`.

  Stub out:
  - `ListSessions() ([]SessionInfo, error)` → `return nil, nil`
  - `liveSession(id string) (SessionInfo, bool, error)` → keep `validateSessionID(id)`; return `SessionInfo{}, false, nil`
  - `OpenTerminal(tabID, sessionID string) error` → `return fmt.Errorf("not implemented")`
  - `KillSession(id string) error` → `return nil`
  - `CreateAgent(tool, projectPath, branch string) (string, error)` → `return "", fmt.Errorf("not implemented")`
  - `pollOnce()` → body becomes a no-op comment: `// no-op until app rewrite (Phase 3)`

  Keep intact: `Diff`, `adapterFor` (replace `model.Tool(tool)` switch with string literal switch since `model` is removed), `validateSessionID`, `validateWorktreeUnderRoots`, `containedUnderRoots`, `newSessionID`, `sessionsSignature`, `sessionsSignature`, `startPolling`, `startup`, `shutdown`, `putBridge`, `getBridge`, `removeBridge`, `WriteToPty`, `ResizePty`, `CloseTerminal`.

  `adapterFor` replacement (drops `model` dep):
  ```go
  func adapterFor(tool string) (agent.Adapter, bool) {
  	switch tool {
  	case "claude":
  		return agent.NewClaude(), true
  	case "opencode":
  		return agent.NewOpencode(), true
  	default:
  		return nil, false
  	}
  }
  ```

- [ ] **Step 8: Trim `cmd/perch/main.go`**

  After deleting `handleAttach`/`handleResurrect`/`handleStatus`/`handleDebugTmux`/`attachDeps`/`attachProduction`/`attachCore`, the surviving functions are: `main`, `run`, `handleLaunch`, `handleSetup`, `setupMessage`, `handleVersion`, `handlePathArg`, `handleDebugDiscover`, `handleDebug`, `writeProjects`, `printUsage`.

  **Exact import set after trim** (verified against surviving function bodies):
  - REMOVE: `attach`, `resurrect`, `status` (only `handleStatus` referenced `status.State*`; the package itself survives for `agent/claude.go`, but main.go no longer imports it), `tmux`, `model`, `strings` (only `handleAttach` used it), `path/filepath` (only `handleDebugTmux`), `os/exec` (only `attachProduction`), `config` (only `handleResurrect`)
  - KEEP: `context` (`handleDebugDiscover`), `fmt`, `io`, `os`, `runtime`, `runtime/debug`, `time` (`handleDebugDiscover` calls `time.Now().Unix()`), `agent`, `discover`, `doctor`, `proc`, `state` (for `handleDebugDiscover`'s `map[string]state.ProjectStat{}` until 0.2)

  Delete functions: `handleAttach`, `handleResurrect`, `handleStatus`, `handleDebugTmux`, `attachDeps`, `attachProduction`, `attachCore`.

  Update `run()` switch:
  ```go
  switch args[0] {
  case "setup":
      return handleSetup(args[1:], stdout, stderr)
  case "doctor":
      return doctor.Run(version, stdout, doctor.RealSystem())
  case "version":
      return handleVersion(stdout)
  case "debug":
      return handleDebug(args[1:], stdout, stderr)
  default:
      return handlePathArg(args[0], stdout, stderr)
  }
  ```

  Update `handleDebug` — remove `"tmux"` sub-case:
  ```go
  func handleDebug(args []string, stdout, stderr io.Writer) int {
  	if len(args) >= 1 && args[0] == "discover" {
  		return handleDebugDiscover(args[1:], stdout, stderr)
  	}
  	_, _ = fmt.Fprintln(stderr, "Usage: perch debug discover [path]")
  	return 2
  }
  ```

  Update `printUsage`:
  ```go
  func printUsage(w io.Writer) {
  	_, _ = fmt.Fprintln(w, "Usage: perch [path]")
  	_, _ = fmt.Fprintln(w, "       perch setup [--replace]")
  	_, _ = fmt.Fprintln(w, "       perch doctor")
  	_, _ = fmt.Fprintln(w, "       perch version")
  }
  ```

  `handleDebugDiscover` calls `discover.Projects(…, map[string]state.ProjectStat{}, …)` — after removing the `state` import, change that argument to `map[string]discover.ProjectStat{}` (Task 0.2 will add this type to `discover`; for now, the `state` package still exists so leave the arg as `map[string]state.ProjectStat{}` until 0.2). Wait — `state` package still exists at end of 0.1 (it's deleted in 0.2), so `cmd/perch/main.go` can still import `state` just for this call site in 0.1. Only the `tmux` import is removed in 0.1. Remove import `attach`/`resurrect`/`model`/`tmux`; keep `state` for now.

- [ ] **Step 9: Trim `cmd/perch/main_test.go`**

  **Exact import set after trim** (verified against surviving test functions):
  - REMOVE: `attach`, `tmux`, `proc`, `context` (only `makeAttachDeps` used `context.Background()`)
  - KEEP: `io`, `os`, `path/filepath`, `strings`, `testing`, `discover`, `model` (`TestWriteProjects` constructs `model.Project{}` and `[]model.Tree{}`)

  Delete test functions: `TestRun_Resurrect_Exit0`, `TestRun_StatusSetWorking_Exit0`, `TestRun_StatusSetWaiting_Exit0`, `TestRun_StatusSetDone_Exit0`, `TestRun_StatusSetInvalid_Exit2`, `TestRun_StatusNoArgs_Exit2`, `TestAttach_NoQuery_Exit2`, `TestAttach_WhitespaceOnlyQuery_Exit2`, `TestAttachCore_ZeroMatches_Exit1`, `TestAttachCore_OneMatch_OutsideTmux_Exit0`, `TestAttachCore_OneMatch_InsideTmux_Exit0`, `TestAttachCore_AmbiguousMatches_Exit2`, `TestAttachCore_ArgvSafety_QueryNeverBecomesTarget`, `TestAttach_Routes`, `TestPrintUsage_ContainsAttach`, `makeAttachDeps`.

  Update `TestPrintUsage_NoDebug`:
  ```go
  func TestPrintUsage_NoDebug(t *testing.T) {
  	_, errOut, _ := callRun([]string{"doctr"})
  	for _, hidden := range []string{"debug", "attach", "resurrect", "status"} {
  		if strings.Contains(errOut, hidden) {
  			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
  		}
  	}
  	for _, visible := range []string{"setup", "doctor", "version"} {
  		if !strings.Contains(errOut, visible) {
  			t.Errorf("printUsage must mention surviving verb %q; stderr: %q", visible, errOut)
  		}
  	}
  }
  ```

  Add regression tests:
  ```go
  func TestRun_RemovedVerbs_Exit2(t *testing.T) {
  	for _, verb := range []string{"resurrect", "status"} {
  		t.Run(verb, func(t *testing.T) {
  			_, errOut, code := callRun([]string{verb})
  			if code != 2 {
  				t.Errorf("removed verb %q: want exit 2, got %d", verb, code)
  			}
  			if !strings.Contains(errOut, "Usage") {
  				t.Errorf("removed verb %q: want Usage on stderr; got %q", verb, errOut)
  			}
  		})
  	}
  }
  ```

- [ ] **Step 10: Verify** — `go build ./... && go test -race -count=1 ./...`; Expected: PASS

- [ ] **Step 11: Commit**
  ```bash
  git add internal/tmux internal/pty/bridge.go internal/pty/integration_test.go \
          internal/status/status.go internal/resurrect/ \
          internal/worktree/deferred.go internal/attach/ \
          app/app.go cmd/perch/main.go cmd/perch/main_test.go && \
  git commit -m "$(cat <<'EOF'
  chore(phase-0): delete internal/tmux; stub all callers; trim CLI to green

  Removes internal/tmux entirely, drops the tmux-attach integration test,
  stubs status/resurrect/worktree/attach/app, and removes the attach,
  resurrect, and status subcommands from cmd/perch. The frozen Spawn
  signature is declared; full implementation in Task 0.3.
  EOF
  )"
  ```

---

### Task 0.2: Delete orphaned packages `internal/state`, `internal/resurrect`, `internal/trust`, `internal/attach`; sever `discover` dep

**Packages being deleted:** `state`, `resurrect`, `trust`, `attach`.

**Remaining callers that must be updated first:**
- `cmd/perch/main.go` — imports `state` for `handleDebugDiscover`'s `map[string]state.ProjectStat{}` arg
- `internal/discover/catalog.go` — imports `state` for `ProjectStat` type and `SortedPaths` function
- `internal/discover/catalog_test.go` — imports `state` for `state.ProjectStat{}`

**Sever plan for `discover`:**
`state.ProjectStat` has two fields (`Rank float64`, `LastAccessed int64`). `state.SortedPaths` and `state.FrecencyScore` are pure functions. Move them into `internal/discover/frecency.go` as exported types/funcs, then update `catalog.go`, `catalog_test.go`, and `cmd/perch/main.go`.

**Files:**
- Delete: `internal/state/`, `internal/resurrect/`, `internal/trust/`, `internal/attach/`
- Create: `internal/discover/frecency.go`
- Modify: `internal/discover/catalog.go`, `internal/discover/catalog_test.go`, `cmd/perch/main.go`
- Test: `go build ./... && go test -race -count=1 ./...`

- [ ] **Step 1: Create `internal/discover/frecency.go`**

  ```go
  package discover

  import "sort"

  // ProjectStat holds the two fields that drive frecency ranking.
  // Inlined from internal/state after that package's deletion.
  type ProjectStat struct {
  	Rank         float64 `json:"rank"`
  	LastAccessed int64   `json:"last_accessed"`
  }

  const (
  	frecencyHour = 3_600
  	frecencyDay  = 86_400
  	frecencyWeek = 604_800
  )

  func frecencyScore(rank float64, lastAccessed, now int64) float64 {
  	switch d := now - lastAccessed; {
  	case d < frecencyHour:
  		return rank * 4.0
  	case d < frecencyDay:
  		return rank * 2.0
  	case d < frecencyWeek:
  		return rank * 0.5
  	default:
  		return rank * 0.25
  	}
  }

  // SortedPaths returns project paths sorted by descending frecency score.
  // Ties break alphabetically. Matches the contract of the deleted state.SortedPaths.
  func SortedPaths(projects map[string]ProjectStat, now int64) []string {
  	paths := make([]string, 0, len(projects))
  	for k := range projects {
  		paths = append(paths, k)
  	}
  	sort.Slice(paths, func(i, j int) bool {
  		si := frecencyScore(projects[paths[i]].Rank, projects[paths[i]].LastAccessed, now)
  		sj := frecencyScore(projects[paths[j]].Rank, projects[paths[j]].LastAccessed, now)
  		if si != sj {
  			return si > sj
  		}
  		return paths[i] < paths[j]
  	})
  	return paths
  }
  ```

- [ ] **Step 2: Update `internal/discover/catalog.go`**

  Remove `"github.com/Miniature-Pug/perch/internal/state"` import. Change the `stats` parameter type from `map[string]state.ProjectStat` to `map[string]ProjectStat`. Replace `state.ProjectStat{}` with `ProjectStat{}`. Replace `state.SortedPaths(discovered, now)` with `SortedPaths(discovered, now)`. No other logic changes.

- [ ] **Step 3: Update `internal/discover/catalog_test.go`**

  Remove `"github.com/Miniature-Pug/perch/internal/state"` import. Replace every `state.ProjectStat{}` and `map[string]state.ProjectStat{...}` with `ProjectStat{}` and `map[string]ProjectStat{...}` respectively. The test is in `package discover` so `ProjectStat` is directly accessible.

- [ ] **Step 4: Update `cmd/perch/main.go`**

  Remove `"github.com/Miniature-Pug/perch/internal/state"` import. In `handleDebugDiscover`, change the `discover.Projects` call argument from `map[string]state.ProjectStat{}` to `map[string]discover.ProjectStat{}`.

- [ ] **Step 5: Delete the packages**
  ```bash
  rm -rf internal/state internal/resurrect internal/trust internal/attach
  ```

- [ ] **Step 6: Verify** — `go build ./... && go test -race -count=1 ./...`; Expected: PASS

- [ ] **Step 7: Commit**
  ```bash
  git add internal/state internal/resurrect internal/trust internal/attach \
          internal/discover/frecency.go internal/discover/catalog.go \
          internal/discover/catalog_test.go cmd/perch/main.go && \
  git commit -m "$(cat <<'EOF'
  chore(phase-0): delete state/resurrect/trust/attach; inline frecency into discover

  Moves ProjectStat + SortedPaths into internal/discover/frecency.go to
  sever the state dep. Updates catalog, its tests, and the CLI. Build
  and test suite remain green.
  EOF
  )"
  ```

---

### Task 0.3: Implement the frozen `Spawn` + `LoginShellArgv` in `internal/pty/bridge.go`

**Files:**
- Modify: `internal/pty/bridge.go` (replace stub Spawn with full creack/pty implementation)
- Modify: `internal/pty/bridge_test.go` (add tests; merge new imports into existing block)
- Test: `internal/pty/bridge_test.go`

- [ ] **Step 1: Write failing tests**

  Append to `internal/pty/bridge_test.go`. Merge the new imports (`context`, `strings`, `time`) into the existing import block; do NOT add a second package declaration. Add these test functions:

  ```go
  func TestLoginShellArgv_UsesShellEnv(t *testing.T) {
  	t.Setenv("SHELL", "/bin/sh")
  	argv := LoginShellArgv()
  	if len(argv) != 2 || argv[0] != "/bin/sh" || argv[1] != "-l" {
  		t.Fatalf("LoginShellArgv = %v, want [/bin/sh -l]", argv)
  	}
  }

  func TestLoginShellArgv_FallsBackToBash(t *testing.T) {
  	t.Setenv("SHELL", "")
  	argv := LoginShellArgv()
  	if len(argv) != 2 || argv[0] != "/bin/bash" || argv[1] != "-l" {
  		t.Fatalf("LoginShellArgv = %v, want [/bin/bash -l]", argv)
  	}
  }

  func TestBridge_CloseIdempotent(t *testing.T) {
  	closed := 0
  	b := &Bridge{closer: func() error { closed++; return nil }}
  	if err := b.Close(); err != nil {
  		t.Fatalf("first Close: %v", err)
  	}
  	if err := b.Close(); err != nil {
  		t.Fatalf("second Close: %v", err)
  	}
  	if closed != 1 {
  		t.Errorf("closer called %d times, want exactly 1", closed)
  	}
  }

  // TestSpawn_RoundTrip spawns `sh -c 'printf hi'` and asserts emitted []int
  // bytes contain "hi". Polls with a deadline before Close so bytes are not lost.
  func TestSpawn_RoundTrip(t *testing.T) {
  	t.Setenv("HOME", t.TempDir())

  	var mu sync.Mutex
  	var collected []byte

  	emit := func(event string, data ...any) {
  		if event != "test-event" {
  			return
  		}
  		if len(data) == 1 {
  			if chunk, ok := data[0].([]int); ok {
  				mu.Lock()
  				for _, v := range chunk {
  					collected = append(collected, byte(v))
  				}
  				mu.Unlock()
  			}
  		}
  	}

  	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  	defer cancel()

  	br, err := Spawn(ctx, t.TempDir(), []string{"sh", "-c", "printf hi"}, "test-event", emit, 80, 24)
  	if err != nil {
  		t.Fatalf("Spawn: %v", err)
  	}

  	// Poll for "hi" before closing; process exits quickly.
  	deadline := time.Now().Add(3 * time.Second)
  	for time.Now().Before(deadline) {
  		mu.Lock()
  		got := string(collected)
  		mu.Unlock()
  		if strings.Contains(got, "hi") {
  			break
  		}
  		time.Sleep(20 * time.Millisecond)
  	}

  	_ = br.Close()

  	mu.Lock()
  	got := string(collected)
  	mu.Unlock()
  	if !strings.Contains(got, "hi") {
  		t.Errorf("round-trip bytes = %q, want to contain %q", got, "hi")
  	}
  }
  ```

- [ ] **Step 2: Run test to verify it fails** — `go test -race -count=1 ./internal/pty/`; Expected: FAIL — `TestSpawn_RoundTrip` fails with `"not yet implemented"`, `TestLoginShellArgv_*` fail (stub returns wrong results)

- [ ] **Step 3: Replace stub `Spawn` in `internal/pty/bridge.go` with the full implementation**

  Replace only the `Spawn` function body (keep everything else verbatim from the Task 0.1 stub). Add the `creackpty` import:

  ```go
  // Package pty provides a direct pseudo-terminal bridge per pane (no tmux).
  package pty

  import (
  	"context"
  	"fmt"
  	"io"
  	"os"
  	"os/exec"
  	"sync"

  	creackpty "github.com/creack/pty"
  )

  type EmitFunc func(event string, data ...any)

  type Bridge struct {
  	mu      sync.Mutex
  	ptyFile io.WriteCloser
  	closer  func() error
  	setsize func(cols, rows uint16) error
  }

  func (b *Bridge) Write(p []byte) (int, error) {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.ptyFile == nil {
  		return 0, os.ErrClosed
  	}
  	return b.ptyFile.Write(p)
  }

  func (b *Bridge) Resize(cols, rows uint16) error {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.setsize == nil {
  		return nil
  	}
  	return b.setsize(cols, rows)
  }

  func (b *Bridge) Close() error {
  	b.mu.Lock()
  	defer b.mu.Unlock()
  	if b.closer == nil {
  		return nil
  	}
  	c := b.closer
  	b.closer = nil
  	b.ptyFile = nil
  	return c()
  }

  const maxChunk = 16 * 1024

  // LoginShellArgv returns [$SHELL, "-l"], falling back to ["/bin/bash", "-l"].
  func LoginShellArgv() []string {
  	sh := os.Getenv("SHELL")
  	if sh == "" {
  		sh = "/bin/bash"
  	}
  	return []string{sh, "-l"}
  }

  // Spawn starts argv[0] argv[1:] inside a pty in working directory cwd,
  // pumping output to emit on `event` as bounded []int chunks (≤ maxChunk).
  // No tmux. Closing the returned Bridge kills the process group and reaps it.
  func Spawn(ctx context.Context, cwd string, argv []string, event string, emit EmitFunc, cols, rows uint16) (*Bridge, error) {
  	if len(argv) == 0 {
  		return nil, fmt.Errorf("pty Spawn: argv must not be empty")
  	}
  	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // caller-controlled input
  	cmd.Dir = cwd
  	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Cols: cols, Rows: rows})
  	if err != nil {
  		return nil, fmt.Errorf("pty Spawn: start %q: %w", argv[0], err)
  	}
  	b := &Bridge{
  		ptyFile: f,
  		setsize: func(c, r uint16) error {
  			return creackpty.Setsize(f, &creackpty.Winsize{Cols: c, Rows: r})
  		},
  		closer: func() error {
  			ferr := f.Close()
  			if cmd.Process != nil {
  				_ = cmd.Process.Kill()
  				_, _ = cmd.Process.Wait()
  			}
  			return ferr
  		},
  	}
  	go pumpReader(f, event, emit, maxChunk)
  	return b, nil
  }

  func pumpReader(r io.Reader, event string, emit EmitFunc, maxChunk int) {
  	buf := make([]byte, maxChunk)
  	for {
  		n, err := r.Read(buf)
  		if n > 0 {
  			out := make([]int, n)
  			for i := 0; i < n; i++ {
  				out[i] = int(buf[i])
  			}
  			emit(event, out)
  		}
  		if err != nil {
  			return
  		}
  	}
  }
  ```

- [ ] **Step 4: Verify** — `go test -race -count=1 ./internal/pty/`; Expected: PASS

- [ ] **Step 5: Commit**
  ```bash
  git add internal/pty/bridge.go internal/pty/bridge_test.go && \
  git commit -m "$(cat <<'EOF'
  feat(pty): implement direct-pty Spawn and LoginShellArgv

  Replaces the tmux-attach bridge with a direct creack/pty spawn.
  Spawn(ctx, cwd, argv, event, emit, cols, rows) runs argv in cwd with
  the frozen signature. LoginShellArgv returns [$SHELL -l] / /bin/bash.
  Tests cover round-trip bytes, shell env, fallback, idempotent Close.
  EOF
  )"
  ```

---

### Task 0.4: `go mod tidy` + `go mod vendor`; full build and suite gate

`charmbracelet/*` was never in `go.mod` (confirmed). This task removes any transitive orphans introduced by the package deletions and refreshes the vendor tree.

**Files:** `go.mod`, `go.sum`, `vendor/`

- [ ] **Step 1: Tidy and refresh vendor**
  ```bash
  GOFLAGS= go mod tidy && GOFLAGS= go mod vendor
  ```

- [ ] **Step 2: Full build + test**
  `go build ./... && go test -race -count=1 ./...`; Expected: PASS

- [ ] **Step 3: Commit**
  ```bash
  git add go.mod go.sum vendor && \
  git commit -m "$(cat <<'EOF'
  chore(phase-0): go mod tidy + vendor after demolition

  Cleans up transitive orphans left by removing state/resurrect/trust/
  attach/tmux. Vendor tree refreshed. Full build and test suite green.
  EOF
  )"
  ```

---

> **Packages still-referenced and therefore NOT deleted in Phase 0:** `internal/status` (kept — `agent/claude.go` imports it for `StateWorking`/`StateDone`/`StateWaiting` string constants; `Machine` pure logic will feed the Phase-2 Monitor); `internal/model` (kept — used by `doctor`, `config`, `git`, `agent`, `main_test.go`'s `TestWriteProjects`); `internal/discover` (kept — `state` dep severed in Task 0.2, frecency inlined); `internal/worktree` (kept — on the user's explicit keep list; `DeferredRemove` is no-op; Phase 3 rewrites when `app/app.go` is rebuilt); `internal/proc` (kept — `App.run proc.Runner` used by `Diff`; `handleDebugDiscover` + `discover` also use it). Executor should revisit `internal/worktree` in Phase 3 (app rewrite) and `internal/status` constants when `agent/claude.go` evolves in Phase 2.

---

## Phase 1 — Validation Spikes (gated, early)

These five spikes are **manual dev-machine experiments**, never part of CI and never
executed by the test suite. Each spike lives in a throwaway `cmd/spike-<n>/` harness;
that entire directory tree is deleted in the Phase-5 cleanup task. The real packages
(`internal/hooklistener`, `internal/notify`, etc.) are built in Phase 2, informed by
whatever each spike recorded in `docs/superpowers/spikes/`.

---

### Spike 1: Claude `PreToolUse` GUI approval interception   (Caps bit: `Approvals` · Fallback: in-terminal approval)

**Goal:** Confirm that a `PreToolUse` hook of type `http` (or `command`) posting to a
localhost bearer-authed endpoint can block Claude's tool invocation and receive
`permissionDecision: "deny"` — replacing the in-TUI prompt — and that the hook payload
carries `session_id`, `transcript_path`, `cwd`, `tool_name`, and `tool_input`.

**Prereqs:** Real `claude` binary on PATH; a temp project directory created by the harness.

- [ ] **Step 1: Build the minimal harness** — create `cmd/spike-1/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const token = "spike1-secret"

type hookPayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr().String()
	fmt.Println("hook listener:", addr)

	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		var p hookPayload
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		fmt.Printf("=== HOOK EVENT ===\n")
		fmt.Printf("hook_event_name : %s\n", p.HookEventName)
		fmt.Printf("session_id      : %s\n", p.SessionID)
		fmt.Printf("transcript_path : %s\n", p.TranscriptPath)
		fmt.Printf("cwd             : %s\n", p.Cwd)
		fmt.Printf("tool_name       : %s\n", p.ToolName)
		fmt.Printf("tool_input      : %s\n", string(p.ToolInput))

		resp := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "deny",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	go func() { _ = http.Serve(ln, mux) }()

	// Write a temp project with .claude/settings.json registering our HTTP hook.
	projDir, err := os.MkdirTemp("", "spike1-proj-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(projDir)

	claudeDir := filepath.Join(projDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		log.Fatal(err)
	}

	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type": "http",
							"url":  "http://" + addr + "/hook",
							"headers": map[string]any{
								"Authorization": "Bearer " + token,
							},
						},
					},
				},
			},
		},
	}
	settingsJSON, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsJSON, 0o644); err != nil {
		log.Fatal(err)
	}

	fmt.Println("project dir:", projDir)
	fmt.Println("settings written; launching claude — type a message that triggers a tool call (e.g. 'list files in current dir')")
	fmt.Println("press Ctrl-C to exit spike")

	cmd := exec.Command("claude")
	cmd.Dir = projDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("claude exited:", err)
	}
}
```

- [ ] **Step 2: Run the experiment**

```
go run ./cmd/spike-1/
# claude opens; type something that forces a file-system tool call, e.g.:
#   list the files in the current directory
# The harness will print the hook payload and deny the tool call.
```

- [ ] **Step 3: Observe**
  - Server log prints all five fields (`hook_event_name`, `session_id`, `transcript_path`, `cwd`, `tool_name`, `tool_input`).
  - `transcript_path` is an absolute path to a `.jsonl` file (not empty) — confirming that the live hook payload delivers the transcript path directly, making slug-derivation unnecessary for the live code path.
  - Claude's TUI shows the tool call was denied without displaying its own in-TUI permission prompt (i.e. the HTTP hook intercept fully replaced the prompt).
  - HTTP response `permissionDecision: "deny"` causes the tool to be blocked.

- [ ] **Step 4: Decide**
  - **PASS:** all five payload fields present; `transcript_path` non-empty absolute path; claude denied the tool without in-TUI prompt.
  - **FAIL:** hook not called, payload missing fields, or claude still shows its own in-TUI prompt (intercept did not suppress it).

- [ ] **Step 5: Record** — write findings to `docs/superpowers/spikes/1-pretooluse-interception.md` with headings: `Outcome PASS/FAIL`, `Evidence` (paste printed payload), `Caps decision` (`Approvals=true` or `Approvals=false`), `Fallback-engaged?`. Commit.

**If FAIL:** Engage fallback `Approvals=false`. `ClaudeMonitor.Capabilities()` returns `Caps{Approvals: false}`. `ApprovalCard` is hidden; Claude uses its own in-TUI permission prompt inside the pane. No `hooklistener` `PreToolUse` handler is wired; only `Stop`/`StopFailure`/`SessionStart` hooks remain active for attention/state tracking.

---

### Spike 2: Claude transcript token/cost + project-slug   (Caps bit: `Tokens` · Fallback: hide meter)

**Goal:** Confirm that (a) `transcript_path` from the hook payload is an absolute path
to the live JSONL, making slug-derivation unnecessary for live code; (b) per-turn JSONL
records carry `usage` fields with token counts and cost; (c) the cold-open slug scheme
(`/home/u/p` → `-home-u-p`) matches what `internal/agent/testdata/claude/projects/`
demonstrates.

**Prereqs:** Real `claude` binary on PATH; Spike 1 harness or equivalent to capture `transcript_path`.

- [ ] **Step 1: Build the minimal harness** — create `cmd/spike-2/main.go`:

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const token = "spike2-secret"

var capturedTranscript string

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p struct {
			HookEventName  string `json:"hook_event_name"`
			TranscriptPath string `json:"transcript_path"`
			SessionID      string `json:"session_id"`
		}
		_ = json.Unmarshal(body, &p)
		if p.TranscriptPath != "" && capturedTranscript == "" {
			capturedTranscript = p.TranscriptPath
			fmt.Println("captured transcript_path:", capturedTranscript)
			go tailTranscript(capturedTranscript)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	go func() { _ = http.Serve(ln, mux) }()

	projDir, _ := os.MkdirTemp("", "spike2-proj-*")
	defer os.RemoveAll(projDir)

	claudeDir := filepath.Join(projDir, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)

	// Register Stop hook to capture transcript_path (available on all event types).
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type": "http",
							"url":  "http://" + addr + "/hook",
							"headers": map[string]any{
								"Authorization": "Bearer " + token,
							},
						},
					},
				},
			},
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type": "http",
							"url":  "http://" + addr + "/hook",
							"headers": map[string]any{
								"Authorization": "Bearer " + token,
							},
						},
					},
				},
			},
		},
	}
	settingsJSON, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsJSON, 0o644)

	// Derive the expected slug from projDir and print it for manual verification.
	slug := "-" + strings.ReplaceAll(projDir[1:], "/", "-")
	fmt.Println("project dir:", projDir)
	fmt.Println("expected slug:", slug)
	fmt.Println("launching claude — complete one turn, then Ctrl-C")

	cmd := exec.Command("claude")
	cmd.Dir = projDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

type usageRecord struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		CacheRead    int     `json:"cache_read_input_tokens"`
		CacheWrite   int     `json:"cache_creation_input_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	} `json:"usage"`
	CostUSD float64 `json:"costUSD"` // alternate top-level field seen in some versions
}

func tailTranscript(path string) {
	// Poll until the file appears (hook may fire before first write).
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Println("tail: open error:", err)
		return
	}
	defer f.Close()
	// Seek to end, then follow new lines.
	_, _ = f.Seek(0, io.SeekStart)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for {
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var rec usageRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.Usage != nil {
				fmt.Printf("USAGE record (type=%s): in=%d out=%d cache_read=%d cache_write=%d cost_usd=%v top_cost_usd=%v\n",
					rec.Type,
					rec.Usage.InputTokens, rec.Usage.OutputTokens,
					rec.Usage.CacheRead, rec.Usage.CacheWrite,
					rec.Usage.CostUSD, rec.CostUSD)
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Println("tail scan error:", err)
			return
		}
		time.Sleep(300 * time.Millisecond)
		// Reset scanner to continue reading new data appended to the file.
		sc = bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	}
}
```

- [ ] **Step 2: Run the experiment**

```
go run ./cmd/spike-2/
# Complete one full turn (ask a question, let claude finish).
# The harness will print:
#   - captured transcript_path (from hook payload)
#   - expected slug (derived from projDir)
#   - any USAGE records found while tailing the transcript
```

- [ ] **Step 3: Observe**
  - `transcript_path` is non-empty and is an absolute path — confirms live-path delivery makes slug-derivation a cold-open-only concern.
  - At least one USAGE record is printed showing `input_tokens`, `output_tokens`, and either `cost_usd` nested under `usage` or `costUSD` at the top level — confirms token/cost are present in the JSONL.
  - The slug printed matches the actual `~/.claude/projects/<slug>` directory name that was created for the session (verify with `ls ~/.claude/projects/` after the run).

- [ ] **Step 4: Decide**
  - **PASS:** `transcript_path` non-empty; at least one USAGE record with token counts and a cost field; slug derivation rule `/foo/bar` → `-foo-bar` confirmed.
  - **FAIL:** `transcript_path` empty or absent, OR no USAGE records found after a completed turn.

- [ ] **Step 5: Record** — write findings to `docs/superpowers/spikes/2-transcript-tokens.md`: `Outcome PASS/FAIL`, `Evidence` (paste sample USAGE record JSON), `Caps decision` (`Tokens=true/false`), `Fallback-engaged?`, field path for tokens and cost. Commit.

**If FAIL:** Engage fallback `Tokens=false`. `ClaudeMonitor.Capabilities()` returns `Caps{Tokens: false}`. Token/cost meter in the status bar is hidden. The transcript tail still runs (for title and cwd), but no usage events are emitted.

---

### Spike 3: opencode `serve` + SSE + REST approval   (Caps bit: opencode `Approvals` + `Tokens` · Fallback: opencode-only degrade)

**Goal:** Confirm that `opencode serve` exposes `GET /event` as a proper SSE stream
emitting `session.next.step.started/ended/failed` frames (with token+cost) and
`permission.v2.asked`; and that `POST /permission/{id}/respond` with body
`{"decision":"reject"}` (or `once`/`always`) resolves the pending permission.

**Prereqs:** Real `opencode` binary on PATH; `OPENCODE_SERVER_PASSWORD` set in env; a test project directory.

- [ ] **Step 1: Build the minimal harness** — create `cmd/spike-3/main.go`:

```go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	if password == "" {
		password = "spike3secret"
		os.Setenv("OPENCODE_SERVER_PASSWORD", password)
	}

	projDir, err := os.MkdirTemp("", "spike3-proj-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(projDir)

	// Start opencode serve in the background.
	serveCmd := exec.Command("opencode", "serve")
	serveCmd.Dir = projDir
	serveCmd.Stdout = os.Stdout
	serveCmd.Stderr = os.Stderr
	if err := serveCmd.Start(); err != nil {
		log.Fatal("opencode serve:", err)
	}
	defer serveCmd.Process.Kill()

	// Give the server a moment to bind.
	time.Sleep(2 * time.Second)

	// Discover the URL: opencode serve prints "Listening on http://..." to stderr/stdout.
	// For the spike, hardcode the default or read from env.
	baseURL := os.Getenv("OPENCODE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:4096" // opencode default
	}
	fmt.Println("connecting to:", baseURL)

	// Subscribe to SSE /event in a goroutine; print every frame.
	go func() {
		req, _ := http.NewRequest("GET", baseURL+"/event", nil)
		req.Header.Set("Authorization", "Bearer "+password)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("SSE connect error:", err)
			return
		}
		defer resp.Body.Close()
		fmt.Println("SSE connected; status:", resp.StatusCode)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			fmt.Println("SSE:", line)
			// If a permission.v2.asked event arrives, auto-reply reject after 1s.
			if strings.Contains(line, "permission.v2.asked") {
				var outer struct {
					Event struct {
						Properties struct {
							ID string `json:"id"`
						} `json:"properties"`
					} `json:"event"`
				}
				// data: {"event": ...}
				data := strings.TrimPrefix(line, "data: ")
				if err := json.Unmarshal([]byte(data), &outer); err == nil {
					permID := outer.Event.Properties.ID
					if permID != "" {
						time.Sleep(time.Second)
						replyPermission(baseURL, password, permID, "reject")
					}
				}
			}
		}
	}()

	fmt.Println("opencode serve running; open another terminal and run:")
	fmt.Printf("  opencode attach %s\n", baseURL)
	fmt.Println("then trigger a tool call that requires permission.")
	fmt.Println("press Enter here to stop the spike.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func replyPermission(base, password, id, decision string) {
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req, _ := http.NewRequest("POST", base+"/permission/"+id+"/respond", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("permission reply error:", err)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Printf("permission reply %s → %d %s\n", decision, resp.StatusCode, strings.TrimSpace(string(out)))
}
```

- [ ] **Step 2: Run the experiment**

```
OPENCODE_SERVER_PASSWORD=spike3secret go run ./cmd/spike-3/
# In a second terminal:
opencode attach http://127.0.0.1:4096
# Trigger a tool call that requires permission (e.g. "write a hello.txt file").
# The spike harness will print all SSE frames and auto-reject the permission.
```

- [ ] **Step 3: Observe**
  - SSE stream connects (status 200).
  - `session.next.step.started` and `session.next.step.ended` frames appear; observe whether they carry token+cost fields.
  - `permission.v2.asked` frame appears when a permissioned tool runs.
  - `POST /permission/{id}/respond` with `{"decision":"reject"}` returns 200 and the tool is blocked in the TUI.
  - Note exact event name casing and JSON field paths for tokens/cost.

- [ ] **Step 4: Decide**
  - **PASS:** SSE stream works; step events carry token counts; permission.v2 round-trip resolves.
  - **FAIL:** serve not found, SSE fails, step events lack token data, or permission reply has no effect.

- [ ] **Step 5: Record** — write findings to `docs/superpowers/spikes/3-opencode-sse-approval.md`: `Outcome PASS/FAIL`, `Evidence` (paste 2–3 representative SSE frames and permission round-trip output), `Caps decision` (opencode `Approvals`/`Tokens` true or false), `Fallback-engaged?`, exact field paths. Commit.

**If FAIL:** Engage opencode-only fallback. `OpencodeMonitor.Capabilities()` returns `Caps{Approvals: false, Tokens: false}`. Claude (primary) is unaffected; opencode integration degrades to pty-only (no structured side-channel events, no ApprovalCard for opencode sessions, no token meter for opencode).

---

### Spike 4: Wails `OnFileDrop` Linux #3686   (Caps bit: n/a — frontend fallback · Fallback: "Open file…" dialog + Copy-path)

**Goal:** Confirm that setting `DisableWebViewDrop: true` plus `preventDefault` on
`dragover` and `drop` in the page prevents WebKitGTK from hijacking the drop and
replacing the UI, and that `OnFileDrop` delivers the dropped file path to Go code.

**Prereqs:** Real Wails v2.12.0 build environment; a Linux machine with a desktop file manager (Nautilus, Thunar, etc.); `npm` and Go available.

- [ ] **Step 1: Build the minimal harness** — create `cmd/spike-4/main.go` and `cmd/spike-4/frontend/`:

`cmd/spike-4/main.go`:
```go
//go:build ignore

package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed frontend/dist
var assets embed.FS

type App struct{}

func (a *App) OnDrop(x, y int, paths []string) {
	fmt.Printf("OnFileDrop: x=%d y=%d paths=%v\n", x, y, paths)
}

func main() {
	app := &App{}
	err := wails.Run(&options.App{
		Title:  "Spike 4 — OnFileDrop",
		Width:  640,
		Height: 480,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) { fmt.Println("ready") },
		OnFileDrop: app.OnDrop,
		Linux: &linux.Options{
			WindowIsTranslucent: false,
		},
		// DisableWebViewDrop prevents WebKitGTK from intercepting OS drop events
		// before OnFileDrop can fire (issue #3686).
		DisableWebViewDrop: true,
		Bind:               []interface{}{app},
	})
	if err != nil {
		fmt.Println("Error:", err)
	}
}
```

`cmd/spike-4/frontend/src/main.js` (minimal Vite app):
```js
// Prevent the browser drop handler from firing — OnFileDrop needs this.
document.addEventListener('dragover', e => e.preventDefault())
document.addEventListener('drop', e => e.preventDefault())

document.body.innerHTML = `
  <div id="drop-zone" style="width:100%;height:100vh;display:flex;align-items:center;
    justify-content:center;font-family:sans-serif;font-size:18px;
    border:3px dashed #888;box-sizing:border-box;">
    Drop a file here from your file manager
  </div>
`
```

Build and run:
```
cd cmd/spike-4 && npm create vite@latest frontend -- --template vanilla
# replace frontend/src/main.js with the content above
npm --prefix frontend install && npm --prefix frontend run build
wails build -tags production    # or: go build -tags production
./build/bin/spike-4
```

- [ ] **Step 2: Run the experiment**

```
./build/bin/spike-4
# Open Nautilus / Thunar, drag a file onto the running window.
```

- [ ] **Step 3: Observe**
  - PASS path: terminal prints `OnFileDrop: x=N y=N paths=[/path/to/file]`; the Wails window UI is NOT replaced/hijacked.
  - FAIL path: the WebKitGTK view is replaced by the OS default drop handler (file opens in a new window, or the UI shows a blank page); `OnFileDrop` is never called.

- [ ] **Step 4: Decide**
  - **PASS:** `OnFileDrop` fires with correct path; UI unchanged after drop.
  - **FAIL:** UI replaced or `OnFileDrop` not called.

- [ ] **Step 5: Record** — write findings to `docs/superpowers/spikes/4-ondrop-linux-3686.md`: `Outcome PASS/FAIL`, `Evidence` (terminal output or screenshot description), `Caps decision` (n/a — frontend toggle), `Fallback-engaged?`. Commit.

**If FAIL:** Engage "Open file…" + Copy-path fallback. `app/options.go` keeps `DisableWebViewDrop: true` (belt-and-suspenders for future fix). The drag-drop `@mention` / file-send affordance is replaced by an "Open file…" button (calls `ShowOpenFileDialog` via Wails runtime) and a "Copy path" action in `FileTree`'s context menu.

---

### Spike 5: Linux OS desktop notification   (Caps bit: n/a · Fallback: `notify.New` returns no-op)

**Goal:** Confirm that sending a notification via the `org.freedesktop.Notifications`
D-Bus interface (and a `notify-send` fallback path) causes a visible desktop
notification banner when the application window is unfocused.

**Prereqs:** Linux desktop with a notification daemon (GNOME Shell, KDE Plasma, or `dunst`); `dbus-send` available; Go available.

- [ ] **Step 1: Build the minimal harness** — create `cmd/spike-5/main.go`:

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/godbus/dbus/v5"
)

func main() {
	fmt.Println("Testing D-Bus org.freedesktop.Notifications ...")
	fmt.Println("Unfocus this terminal, then watch for a desktop notification.")
	time.Sleep(3 * time.Second) // give user time to unfocus

	if err := notifyDBus("perch spike 5", "D-Bus path: notification from org.freedesktop.Notifications"); err != nil {
		fmt.Println("D-Bus notify failed:", err)
		fmt.Println("Falling back to notify-send ...")
		if err2 := notifySend("perch spike 5", "notify-send fallback path"); err2 != nil {
			fmt.Println("notify-send also failed:", err2)
			os.Exit(1)
		}
		fmt.Println("notify-send succeeded.")
		return
	}
	fmt.Println("D-Bus notify succeeded.")
}

func notifyDBus(title, body string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("dbus.SessionBus: %w", err)
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	// Notify(app_name, replaces_id, icon, summary, body, actions, hints, expire_timeout)
	call := obj.Call(
		"org.freedesktop.Notifications.Notify", 0,
		"perch",       // app_name
		uint32(0),     // replaces_id (0 = new)
		"dialog-info", // icon
		title,         // summary
		body,          // body
		[]string{},    // actions
		map[string]dbus.Variant{}, // hints
		int32(5000),   // expire_timeout ms
	)
	return call.Err
}

func notifySend(title, body string) error {
	cmd := exec.Command("notify-send", "-t", "5000", title, body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
```

Add `github.com/godbus/dbus/v5` to a temporary go.mod inside `cmd/spike-5/` (do not modify the root module):

```
cd cmd/spike-5
go mod init spike5
go get github.com/godbus/dbus/v5
go run main.go
```

- [ ] **Step 2: Run the experiment**

```
cd cmd/spike-5 && go mod init spike5 && go get github.com/godbus/dbus/v5
go run main.go
# The harness sleeps 3s — click away to unfocus the terminal, then wait.
```

- [ ] **Step 3: Observe**
  - A desktop notification banner appears with title "perch spike 5" and the appropriate body.
  - Record which path succeeded: D-Bus direct or `notify-send` fallback.
  - Note: if a Wayland compositor is in use without XDG_RUNTIME_DIR / DBUS_SESSION_BUS_ADDRESS set, the D-Bus path may fail; the `notify-send` fallback should still work.

- [ ] **Step 4: Decide**
  - **PASS:** notification banner appears via either path.
  - **FAIL:** neither path produces a visible notification (no daemon, headless CI, missing socket).

- [ ] **Step 5: Record** — write findings to `docs/superpowers/spikes/5-linux-notify.md`: `Outcome PASS/FAIL`, `Evidence` (which path worked; any errors), `Caps decision` (n/a — `notify.New` returns no-op on FAIL), `Fallback-engaged?`. Commit.

**If FAIL:** `notify.New()` returns a no-op `Notifier` (no panic, no error). The `NotificationHub` in-app hub still receives all events and functions normally; only OS desktop banner delivery is absent. The `FakeNotifier` in tests is unaffected.

---

## Phase 2 — Backend Core (data & git)

All tasks run on branch `feat/perch-v1`. Every test uses `-race`. No real `claude`/`opencode` is ever invoked.

---

### Task 2.1: internal/registry — workspace store

**Files:**
- Create `internal/registry/registry.go`
- Create `internal/registry/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/registry/registry_test.go
package registry_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/registry"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("want empty list, got %d items", len(got))
	}

	now := time.Now().Truncate(time.Second)
	w := registry.Workspace{
		ID:           "ws-abc123",
		WorktreePath: "/tmp/repo",
		Agent:        "claude",
		LastSessionID: "ses_xyz",
		Title:        "my workspace",
		LastActive:   now,
	}
	if err := s.Upsert(w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok := s.Get("ws-abc123")
	if !ok {
		t.Fatal("Get returned not-found after Upsert")
	}
	if got.Title != "my workspace" || got.Agent != "claude" {
		t.Fatalf("unexpected workspace: %+v", got)
	}

	// Reload from disk — persistence check
	s2, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load after Upsert: %v", err)
	}
	list := s2.List()
	if len(list) != 1 {
		t.Fatalf("want 1 item after reload, got %d", len(list))
	}
	if list[0].ID != "ws-abc123" {
		t.Fatalf("unexpected ID: %s", list[0].ID)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	// workspaces.json does not exist — Load must succeed with empty store
	s, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("expected empty store, got %d items", len(s.List()))
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{
		ID: "ws-1", WorktreePath: "/tmp/x", Agent: "opencode",
		Title: "x", LastActive: time.Now(),
	})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "workspaces.json" {
			t.Errorf("unexpected file after Upsert: %s", e.Name())
		}
	}
}

func TestSortOrderLastActiveDesc(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	base := time.Now().Truncate(time.Second)
	for i, id := range []string{"ws-old", "ws-new", "ws-mid"} {
		_ = s.Upsert(registry.Workspace{
			ID: id, WorktreePath: "/tmp/" + id, Agent: "claude",
			Title: id, LastActive: base.Add(time.Duration(i) * time.Hour),
		})
	}
	list := s.List()
	if list[0].ID != "ws-mid" || list[1].ID != "ws-new" || list[2].ID != "ws-old" {
		t.Fatalf("wrong sort order: %v", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

func TestRemove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s, _ := registry.Load(dir)
	_ = s.Upsert(registry.Workspace{
		ID: "ws-del", WorktreePath: "/tmp/del", Agent: "claude",
		Title: "del", LastActive: time.Now(),
	})
	if err := s.Remove("ws-del"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("ws-del"); ok {
		t.Fatal("Get returned item after Remove")
	}
}

func TestDefaultConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got := registry.DefaultConfigDir()
		want := filepath.Join(xdg, "perch")
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("XDG_CONFIG_HOME unset", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		got := registry.DefaultConfigDir()
		want := filepath.Join(home, ".config", "perch")
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run TestRoundTrip ./internal/registry/
```

Expected FAIL: `cannot find package "github.com/Miniature-Pug/perch/internal/registry"`

- [ ] **Step 3: Write minimal implementation**

```go
// internal/registry/registry.go
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Workspace is the persistent record for one perch workspace.
// JSON tags are frozen — do not rename.
type Workspace struct {
	ID            string    `json:"id"`
	WorktreePath  string    `json:"worktreePath"`
	Agent         string    `json:"agent"`
	LastSessionID string    `json:"lastSessionID"`
	Title         string    `json:"title"`
	LastActive    time.Time `json:"lastActive"`
}

// Store is a thread-safe, file-backed workspace registry.
type Store struct {
	path  string
	mu    sync.Mutex
	items map[string]Workspace
}

// DefaultConfigDir returns the perch config directory following the XDG Base
// Directory spec: $XDG_CONFIG_HOME/perch, falling back to ~/.config/perch.
func DefaultConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".config", "perch")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "perch")
}

// Load reads workspaces.json from configDir. A missing file is not an error
// and returns an empty store. configDir is created if it does not exist.
func Load(configDir string) (*Store, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("registry: mkdir %s: %w", configDir, err)
	}
	path := filepath.Join(configDir, "workspaces.json")
	s := &Store{path: path, items: make(map[string]Workspace)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	var items []Workspace
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	for _, w := range items {
		s.items[w.ID] = w
	}
	return s, nil
}

// List returns all workspaces sorted by LastActive descending.
func (s *Store) List() []Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActive.After(out[j].LastActive)
	})
	return out
}

// Get returns the workspace with the given ID, or false if not found.
func (s *Store) Get(id string) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.items[id]
	return w, ok
}

// Upsert inserts or replaces the workspace and atomically persists the store.
func (s *Store) Upsert(w Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[w.ID] = w
	return s.flush()
}

// Remove deletes the workspace with the given ID and atomically persists.
// A missing ID is not an error.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return s.flush()
}

// flush writes all items to disk atomically (temp file + rename).
// Must be called with s.mu held.
func (s *Store) flush() error {
	list := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		list = append(list, w)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".workspaces-*.json.tmp")
	if err != nil {
		return fmt.Errorf("registry: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestRoundTrip|TestMissingFileIsEmpty|TestAtomicWriteLeavesNoTemp|TestSortOrderLastActiveDesc|TestRemove|TestDefaultConfigDir" ./internal/registry/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go && git commit -m "feat(registry): workspace Store — Load/List/Get/Upsert/Remove atomic JSON, XDG DefaultConfigDir"
```

---

### Task 2.2: internal/git/hunk.go — FileDiff types + DiffStat(worktree)

**Files:**
- Create `internal/git/hunk.go`
- Create `internal/git/hunk_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/git/hunk_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// initRepo creates a temp git repo with an initial commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], append(c[1:], "-C", dir)...).CombinedOutput()
		// git init -b and git config don't accept -C at all positions; use Dir instead
		_ = out
		_ = err
	}
	// re-do with Dir set
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// initial commit
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestDiffStat_ModifiedAddedDeleted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Create initial state: a.txt, b.txt, c.txt all committed
	for name, content := range map[string]string{
		"a.txt": "line1\nline2\n",
		"b.txt": "orig\n",
		"c.txt": "will delete\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "add files"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Modify a.txt, add new.txt, delete c.txt
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("line1\nmodified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "c.txt")); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	ctx := context.Background()
	diffs, err := git.DiffStat(ctx, r, repo)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}

	byPath := make(map[string]git.FileDiff)
	for _, d := range diffs {
		byPath[d.Path] = d
	}

	// a.txt: modified (unstaged) — should show as "M"
	a, ok := byPath["a.txt"]
	if !ok {
		t.Fatal("a.txt missing from DiffStat")
	}
	if a.Status != "M" {
		t.Errorf("a.txt status = %q, want M", a.Status)
	}
	if a.Added < 1 || a.Removed < 1 {
		t.Errorf("a.txt added=%d removed=%d, want both ≥1", a.Added, a.Removed)
	}

	// c.txt: deleted (unstaged)
	c, ok := byPath["c.txt"]
	if !ok {
		t.Fatal("c.txt missing from DiffStat")
	}
	if c.Status != "D" {
		t.Errorf("c.txt status = %q, want D", c.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run TestDiffStat_ModifiedAddedDeleted ./internal/git/
```

Expected FAIL: `undefined: git.DiffStat` (the existing `DiffStat` in diff.go returns `Stat`, not `[]FileDiff`)

- [ ] **Step 3: Write minimal implementation**

```go
// internal/git/hunk.go
package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// FileDiff carries the per-file summary from DiffStat.
// Status values: "M" modified, "A" added (untracked or staged new), "D" deleted,
// "R" renamed, "?" untracked.
// JSON tags are frozen — do not rename.
type FileDiff struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Status  string `json:"status"`
}

// HunkLine is one line inside a unified-diff hunk.
// Kind: "ctx" (context), "add" (addition), "del" (deletion).
// JSON tags are frozen — do not rename.
type HunkLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Hunk is one @@ block from a unified diff for a single file.
// Index is the 0-based position of this hunk within the file's hunk list.
// JSON tags are frozen — do not rename.
type Hunk struct {
	File     string     `json:"file"`
	Index    int        `json:"index"`
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []HunkLine `json:"lines"`
}

// DiffStat returns per-file diff summaries for all uncommitted changes in
// worktree (staged + unstaged). It combines `git diff --numstat` (unstaged)
// and `git diff --cached --numstat` (staged), then overlays status via
// `git status --porcelain`.
func DiffStat(ctx context.Context, r proc.Runner, worktree string) ([]FileDiff, error) {
	// Collect status codes from git status --porcelain
	stOut, stErr, err := r.Run(ctx, "git", "-C", worktree, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(stErr)))
	}

	type fileInfo struct {
		added, removed int
		status         string
	}
	files := make(map[string]*fileInfo)

	for _, line := range strings.Split(strings.TrimRight(string(stOut), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		// For renames "old -> new" git porcelain shows "R  old -> new" but
		// with --porcelain v1 it is "R  new\x00old" — we take the first path token.
		if i := strings.Index(path, "\x00"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			continue
		}
		status := statusCode(xy)
		if _, ok := files[path]; !ok {
			files[path] = &fileInfo{status: status}
		} else {
			files[path].status = status
		}
	}

	// Collect line counts from numstat for unstaged + staged
	for _, extraArgs := range [][]string{
		{"diff", "--numstat"},
		{"diff", "--cached", "--numstat"},
	} {
		args := append([]string{"-C", worktree}, extraArgs...)
		out, errOut, runErr := r.Run(ctx, "git", args...)
		if runErr != nil {
			return nil, fmt.Errorf("git %v: %w: %s", extraArgs, runErr, strings.TrimSpace(string(errOut)))
		}
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			path := strings.TrimSpace(parts[2])
			a, _ := strconv.Atoi(parts[0])
			d, _ := strconv.Atoi(parts[1])
			if _, ok := files[path]; !ok {
				files[path] = &fileInfo{status: "M"}
			}
			files[path].added += a
			files[path].removed += d
		}
	}

	out := make([]FileDiff, 0, len(files))
	for path, info := range files {
		out = append(out, FileDiff{
			Path:    path,
			Added:   info.added,
			Removed: info.removed,
			Status:  info.status,
		})
	}
	return out, nil
}

// statusCode maps a git porcelain XY two-char status string to a single letter.
func statusCode(xy string) string {
	if len(xy) < 2 {
		return "?"
	}
	x, y := rune(xy[0]), rune(xy[1])
	switch {
	case x == 'D' || y == 'D':
		return "D"
	case x == 'R' || y == 'R':
		return "R"
	case x == 'A' || y == 'A':
		return "A"
	case x == '?' && y == '?':
		return "?"
	default:
		return "M"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run TestDiffStat_ModifiedAddedDeleted ./internal/git/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/git/hunk.go internal/git/hunk_test.go && git commit -m "feat(git): FileDiff/HunkLine/Hunk types + DiffStat(worktree) per-file stat with status codes"
```

---

### Task 2.3: internal/git — Hunks(worktree, file)

**Files:**
- Modify `internal/git/hunk.go` (add `Hunks`)
- Create `internal/git/hunks_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/git/hunks_test.go
package git_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestHunks_MultiHunk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t) // defined in hunk_test.go

	// Write a file with enough lines to produce two separate hunks when modified
	orig := ""
	for i := 0; i < 20; i++ {
		orig += "line\n"
	}
	target := filepath.Join(repo, "multi.txt")
	if err := os.WriteFile(target, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "add multi"}} {
		runGit(t, repo, args...)
	}

	// Modify line 1 and line 19 to create two non-adjacent hunks
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "CHANGED_TOP\n"
	lines[18] = "CHANGED_BOTTOM\n"
	modified := ""
	for _, l := range lines {
		modified += l
	}
	if err := os.WriteFile(target, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	hunks, err := git.Hunks(context.Background(), r, repo, "multi.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	if len(hunks) < 2 {
		t.Fatalf("expected ≥2 hunks, got %d", len(hunks))
	}
	// Indices must be 0-based and sequential
	for i, h := range hunks {
		if h.Index != i {
			t.Errorf("hunk[%d].Index = %d, want %d", i, h.Index, i)
		}
		if h.File != "multi.txt" {
			t.Errorf("hunk[%d].File = %q, want multi.txt", i, h.File)
		}
		if h.Header == "" {
			t.Errorf("hunk[%d].Header is empty", i)
		}
		if len(h.Lines) == 0 {
			t.Errorf("hunk[%d].Lines is empty", i)
		}
		// Every line must have a valid kind
		for j, l := range h.Lines {
			switch l.Kind {
			case "ctx", "add", "del":
			default:
				t.Errorf("hunk[%d].Lines[%d].Kind = %q, want ctx/add/del", i, j, l.Kind)
			}
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	import_exec := func() interface{ Command(string, ...string) interface{ CombinedOutput() ([]byte, error); Run() error } } {
		return nil
	}
	_ = import_exec
	// Use os/exec directly
	import "os/exec"
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
```

The `runGit` helper above has a syntax error placeholder — write it properly:

```go
// internal/git/hunks_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestHunks_MultiHunk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t) // defined in hunk_test.go

	// Write a file with 20 lines and commit it
	orig := ""
	for i := 0; i < 20; i++ {
		orig += "line\n"
	}
	target := filepath.Join(repo, "multi.txt")
	if err := os.WriteFile(target, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add multi")

	// Modify line 1 and line 19 to force two non-adjacent hunks
	var lines [20]string
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "CHANGED_TOP\n"
	lines[18] = "CHANGED_BOTTOM\n"
	modified := ""
	for _, l := range lines {
		modified += l
	}
	if err := os.WriteFile(target, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	r := proc.ExecRunner{}
	hunks, err := git.Hunks(context.Background(), r, repo, "multi.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	if len(hunks) < 2 {
		t.Fatalf("expected ≥2 hunks, got %d", len(hunks))
	}
	for i, h := range hunks {
		if h.Index != i {
			t.Errorf("hunk[%d].Index = %d, want %d", i, h.Index, i)
		}
		if h.File != "multi.txt" {
			t.Errorf("hunk[%d].File = %q, want multi.txt", i, h.File)
		}
		if h.Header == "" {
			t.Errorf("hunk[%d].Header is empty", i)
		}
		for j, l := range h.Lines {
			switch l.Kind {
			case "ctx", "add", "del":
			default:
				t.Errorf("hunk[%d].Lines[%d].Kind = %q, want ctx/add/del", i, j, l.Kind)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run TestHunks_MultiHunk ./internal/git/
```

Expected FAIL: `undefined: git.Hunks`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/git/hunk.go`:

```go
// Hunks parses `git diff` unified output for a single file in worktree and
// returns the slice of Hunk structs with 0-based Index values.
func Hunks(ctx context.Context, r proc.Runner, worktree, file string) ([]Hunk, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", worktree, "diff", "--unified=3", "--no-color", "--", file)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w: %s", file, err, strings.TrimSpace(string(errOut)))
	}
	return parseUnifiedDiff(file, string(out)), nil
}

// parseUnifiedDiff parses unified diff output for a single file into []Hunk.
func parseUnifiedDiff(file, raw string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			h := Hunk{
				File:   file,
				Index:  len(hunks),
				Header: line,
			}
			// Parse @@ -oldStart,oldLines +newStart,newLines @@
			parseHunkHeader(line, &h)
			cur = &h
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "add", Text: line[1:]})
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "del", Text: line[1:]})
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "ctx", Text: line[1:]})
		case line == `\ No newline at end of file`:
			// skip
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// parseHunkHeader parses "@@ -a,b +c,d @@" into h fields.
// Missing comma-count defaults to 1 (git omits it for single-line hunks).
func parseHunkHeader(header string, h *Hunk) {
	// Find the @@ ... @@ span
	inner := strings.TrimPrefix(header, "@@ ")
	end := strings.Index(inner, " @@")
	if end > 0 {
		inner = inner[:end]
	}
	parts := strings.Fields(inner)
	if len(parts) >= 2 {
		h.OldStart, h.OldLines = parseRange(parts[0])
		h.NewStart, h.NewLines = parseRange(parts[1])
	}
}

// parseRange parses "-a,b" or "+c,d" into (start, lines).
func parseRange(s string) (int, int) {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	if len(parts) == 1 {
		return start, 1
	}
	lines, _ := strconv.Atoi(parts[1])
	return start, lines
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run TestHunks_MultiHunk ./internal/git/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/git/hunk.go internal/git/hunks_test.go && git commit -m "feat(git): Hunks — parse unified diff into []Hunk with 0-based Index and classified HunkLines"
```

---

### Task 2.4: internal/git — StageHunk + DiscardHunk

> **⚠ SUPERSEDED (implemented, then revised).** The reconstruct-patch-from-`Hunk` design shown in this task was found to corrupt the index for files without a trailing newline and to mis-stage file deletions. It was replaced with **index-based, raw-text staging** — `StageHunk(ctx, r, worktree, file string, index int)` / `DiscardHunk(...)` re-run `git diff` and apply the index-th hunk's verbatim text. Authoritative signatures live in the Shared Contracts above; the as-built implementation and tests are in `internal/git/hunk.go` / `stage_test.go` (commit `6f5a495`). The code blocks below are retained only as historical record — do not re-implement them.

**Files:**
- Modify `internal/git/hunk.go` (add `StageHunk`, `DiscardHunk`, `reconstructPatch`)
- Create `internal/git/stage_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/git/stage_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

// twoHunkFile creates a repo with a two-hunk file ready to test staging.
// Returns repo path. The file "target.txt" has 20 lines committed, then lines
// 1 and 19 modified (unstaged) so git produces two hunks.
func twoHunkFile(t *testing.T) string {
	t.Helper()
	repo := initRepo(t)
	orig := ""
	for i := 0; i < 20; i++ {
		orig += "line\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var lines [20]string
	for i := range lines {
		lines[i] = "line\n"
	}
	lines[0] = "TOP_CHANGE\n"
	lines[18] = "BOTTOM_CHANGE\n"
	content := ""
	for _, l := range lines {
		content += l
	}
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestStageHunk_OneOfTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("Hunks setup: err=%v count=%d", err, len(hunks))
	}

	// Stage only the first hunk
	if err := git.StageHunk(ctx, r, repo, hunks[0]); err != nil {
		t.Fatalf("StageHunk: %v", err)
	}

	// git diff --cached should show the staged hunk (first change)
	cachedOut, cachedErr, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v: %s", runErr, cachedErr)
	}
	cached := string(cachedOut)
	if !strings.Contains(cached, "TOP_CHANGE") {
		t.Errorf("cached diff should contain TOP_CHANGE, got:\n%s", cached)
	}
	// The second hunk should NOT be staged
	if strings.Contains(cached, "BOTTOM_CHANGE") {
		t.Errorf("cached diff should NOT contain BOTTOM_CHANGE, got:\n%s", cached)
	}

	// git diff (unstaged) should still contain BOTTOM_CHANGE
	unstagedOut, _, _ := r.Run(ctx, "git", "-C", repo, "diff", "--", "target.txt")
	if !strings.Contains(string(unstagedOut), "BOTTOM_CHANGE") {
		t.Errorf("unstaged diff should still contain BOTTOM_CHANGE")
	}
}

func TestDiscardHunk_RevertsWorktreeLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := proc.ExecRunner{}
	ctx := context.Background()

	hunks, err := git.Hunks(ctx, r, repo, "target.txt")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("Hunks setup: err=%v count=%d", err, len(hunks))
	}

	// Discard the first hunk (TOP_CHANGE → reverts to "line\n")
	if err := git.DiscardHunk(ctx, r, repo, hunks[0]); err != nil {
		t.Fatalf("DiscardHunk: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(repo, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "TOP_CHANGE") {
		t.Errorf("TOP_CHANGE should have been discarded, file:\n%s", content)
	}
	if !strings.Contains(string(content), "BOTTOM_CHANGE") {
		t.Errorf("BOTTOM_CHANGE should still be present, file:\n%s", content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestStageHunk_OneOfTwo|TestDiscardHunk_RevertsWorktreeLines" ./internal/git/
```

Expected FAIL: `undefined: git.StageHunk`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/git/hunk.go`:

```go
// reconstructPatch builds a minimal unified-diff patch string from a single
// Hunk that git-apply can consume. The patch includes the standard file header
// lines (--- a/<file> / +++ b/<file>) and the @@ line with all hunk lines.
func reconstructPatch(h Hunk) string {
	var sb strings.Builder
	sb.WriteString("--- a/" + h.File + "\n")
	sb.WriteString("+++ b/" + h.File + "\n")
	sb.WriteString(h.Header + "\n")
	for _, l := range h.Lines {
		switch l.Kind {
		case "add":
			sb.WriteString("+" + l.Text + "\n")
		case "del":
			sb.WriteString("-" + l.Text + "\n")
		default:
			sb.WriteString(" " + l.Text + "\n")
		}
	}
	return sb.String()
}

// StageHunk applies a single hunk to the git index (staging area) using
// `git apply --cached`. The patch is reconstructed from h.
func StageHunk(ctx context.Context, r proc.Runner, worktree string, h Hunk) error {
	patch := reconstructPatch(h)
	return applyPatch(ctx, r, worktree, patch, "--cached")
}

// DiscardHunk reverses a single hunk in the worktree using
// `git apply --reverse`. The patch is reconstructed from h.
func DiscardHunk(ctx context.Context, r proc.Runner, worktree string, h Hunk) error {
	patch := reconstructPatch(h)
	return applyPatch(ctx, r, worktree, patch, "--reverse")
}

// applyPatch pipes patch bytes into `git apply <extraFlag>` running in worktree.
// It uses RunInDir so the stdin pipe is available via os/exec directly.
func applyPatch(ctx context.Context, r proc.Runner, worktree, patch, flag string) error {
	// proc.Runner.Run does not expose stdin; use os/exec directly for the apply step.
	import_exec := exec.CommandContext // will resolve via os/exec import below
	_ = import_exec
	return applyPatchExec(ctx, worktree, patch, flag)
}
```

The `applyPatch` above needs to call `os/exec` directly because `proc.Runner` does not expose stdin. Add a helper that uses `os/exec` and update the import:

```go
// internal/git/hunk.go — complete revised file (replace prior content)
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// FileDiff carries the per-file summary from DiffStat.
// Status: "M" modified, "A" added, "D" deleted, "R" renamed, "?" untracked.
type FileDiff struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Status  string `json:"status"`
}

// HunkLine is one line inside a unified-diff hunk.
// Kind: "ctx" context, "add" addition, "del" deletion.
type HunkLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Hunk is one @@ block from a unified diff for a single file.
type Hunk struct {
	File     string     `json:"file"`
	Index    int        `json:"index"`
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []HunkLine `json:"lines"`
}

// DiffStat returns per-file diff summaries for all uncommitted changes in worktree.
func DiffStat(ctx context.Context, r proc.Runner, worktree string) ([]FileDiff, error) {
	stOut, stErr, err := r.Run(ctx, "git", "-C", worktree, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(stErr)))
	}

	type fileInfo struct {
		added, removed int
		status         string
	}
	files := make(map[string]*fileInfo)

	for _, line := range strings.Split(strings.TrimRight(string(stOut), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, "\x00"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			continue
		}
		status := statusCode(xy)
		if _, ok := files[path]; !ok {
			files[path] = &fileInfo{status: status}
		} else {
			files[path].status = status
		}
	}

	for _, extraArgs := range [][]string{
		{"diff", "--numstat"},
		{"diff", "--cached", "--numstat"},
	} {
		args := append([]string{"-C", worktree}, extraArgs...)
		out, errOut, runErr := r.Run(ctx, "git", args...)
		if runErr != nil {
			return nil, fmt.Errorf("git %v: %w: %s", extraArgs, runErr, strings.TrimSpace(string(errOut)))
		}
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			path := strings.TrimSpace(parts[2])
			a, _ := strconv.Atoi(parts[0])
			d, _ := strconv.Atoi(parts[1])
			if _, ok := files[path]; !ok {
				files[path] = &fileInfo{status: "M"}
			}
			files[path].added += a
			files[path].removed += d
		}
	}

	out := make([]FileDiff, 0, len(files))
	for path, info := range files {
		out = append(out, FileDiff{
			Path:    path,
			Added:   info.added,
			Removed: info.removed,
			Status:  info.status,
		})
	}
	return out, nil
}

func statusCode(xy string) string {
	if len(xy) < 2 {
		return "?"
	}
	x, y := rune(xy[0]), rune(xy[1])
	switch {
	case x == 'D' || y == 'D':
		return "D"
	case x == 'R' || y == 'R':
		return "R"
	case x == 'A' || y == 'A':
		return "A"
	case x == '?' && y == '?':
		return "?"
	default:
		return "M"
	}
}

// Hunks parses `git diff` unified output for one file and returns []Hunk.
func Hunks(ctx context.Context, r proc.Runner, worktree, file string) ([]Hunk, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", worktree, "diff", "--unified=3", "--no-color", "--", file)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w: %s", file, err, strings.TrimSpace(string(errOut)))
	}
	return parseUnifiedDiff(file, string(out)), nil
}

func parseUnifiedDiff(file, raw string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			h := Hunk{File: file, Index: len(hunks), Header: line}
			parseHunkHeader(line, &h)
			cur = &h
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "add", Text: line[1:]})
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "del", Text: line[1:]})
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "ctx", Text: line[1:]})
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

func parseHunkHeader(header string, h *Hunk) {
	inner := strings.TrimPrefix(header, "@@ ")
	if end := strings.Index(inner, " @@"); end > 0 {
		inner = inner[:end]
	}
	parts := strings.Fields(inner)
	if len(parts) >= 2 {
		h.OldStart, h.OldLines = parseRange(parts[0])
		h.NewStart, h.NewLines = parseRange(parts[1])
	}
}

func parseRange(s string) (int, int) {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	if len(parts) == 1 {
		return start, 1
	}
	lines, _ := strconv.Atoi(parts[1])
	return start, lines
}

// reconstructPatch builds a git-apply-compatible unified diff patch from one Hunk.
// Note: line Text values stored in HunkLine do NOT include the leading sigil (+/-/ ).
func reconstructPatch(h Hunk) string {
	var sb strings.Builder
	sb.WriteString("--- a/" + h.File + "\n")
	sb.WriteString("+++ b/" + h.File + "\n")
	sb.WriteString(h.Header + "\n")
	for _, l := range h.Lines {
		switch l.Kind {
		case "add":
			sb.WriteString("+" + l.Text + "\n")
		case "del":
			sb.WriteString("-" + l.Text + "\n")
		default:
			sb.WriteString(" " + l.Text + "\n")
		}
	}
	return sb.String()
}

// StageHunk applies a single hunk to the git index using `git apply --cached`.
func StageHunk(ctx context.Context, r proc.Runner, worktree string, h Hunk) error {
	_ = r // git apply needs stdin; use os/exec directly
	return gitApplyPatch(ctx, worktree, reconstructPatch(h), "--cached")
}

// DiscardHunk reverts a single hunk in the worktree using `git apply --reverse`.
func DiscardHunk(ctx context.Context, r proc.Runner, worktree string, h Hunk) error {
	_ = r
	return gitApplyPatch(ctx, worktree, reconstructPatch(h), "--reverse")
}

// gitApplyPatch pipes patch into `git apply <flag>` with worktree as cwd.
func gitApplyPatch(ctx context.Context, worktree, patch, flag string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", flag, "-")
	cmd.Dir = worktree
	cmd.Stdin = bytes.NewBufferString(patch)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply %s: %w: %s", flag, err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestStageHunk_OneOfTwo|TestDiscardHunk_RevertsWorktreeLines" ./internal/git/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/git/hunk.go internal/git/stage_test.go && git commit -m "feat(git): StageHunk (git apply --cached) + DiscardHunk (git apply --reverse) from reconstructed patch"
```

---

### Task 2.5: internal/git — Branches + Worktrees enumeration

**Files:**
- Create `internal/git/branches.go`
- Create `internal/git/branches_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/git/branches_test.go
package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestBranches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Create an extra branch
	cmd := exec.Command("git", "branch", "feature-x")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	branches, err := git.Branches(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	found := make(map[string]bool)
	for _, b := range branches {
		found[b] = true
	}
	if !found["main"] {
		t.Errorf("branches should include main, got %v", branches)
	}
	if !found["feature-x"] {
		t.Errorf("branches should include feature-x, got %v", branches)
	}
}

func TestWorktrees_WithLinked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initRepo(t)

	// Add a linked worktree
	wtDir := filepath.Join(t.TempDir(), "linked-wt")
	cmd := exec.Command("git", "worktree", "add", "-b", "wt-branch", wtDir, "HEAD")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	r := proc.ExecRunner{}
	wts, err := git.Worktrees(context.Background(), r, repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) < 2 {
		t.Fatalf("expected ≥2 worktrees, got %d", len(wts))
	}

	found := make(map[string]bool)
	for _, wt := range wts {
		if wt.Path == "" {
			t.Errorf("worktree has empty path: %+v", wt)
		}
		found[wt.Branch] = true
		if wt.Head == "" {
			t.Errorf("worktree has empty Head: %+v", wt)
		}
		// Path must exist on disk
		if _, err := os.Stat(wt.Path); err != nil {
			t.Errorf("worktree path %s not stat-able: %v", wt.Path, err)
		}
	}
	if !found["wt-branch"] {
		t.Errorf("linked worktree branch wt-branch not found in %v", wts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestBranches|TestWorktrees_WithLinked" ./internal/git/
```

Expected FAIL: `undefined: git.Branches`

- [ ] **Step 3: Write minimal implementation**

```go
// internal/git/branches.go
package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// WorktreeInfo is a summary of one git worktree from `git worktree list --porcelain`.
// JSON tags are frozen — do not rename.
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// Branches returns all local branch names in repo (bare names, no refs/heads/ prefix).
func Branches(ctx context.Context, r proc.Runner, repo string) ([]string, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", repo, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch: %w: %s", err, strings.TrimSpace(string(errOut)))
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// Worktrees returns all worktrees for repo via `git worktree list --porcelain`,
// skipping bare entries. Reuses the existing ParsePorcelain/ListWorktrees logic.
func Worktrees(ctx context.Context, r proc.Runner, repo string) ([]WorktreeInfo, error) {
	wts, err := ListWorktrees(ctx, r, repo)
	if err != nil {
		return nil, err
	}
	var out []WorktreeInfo
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		out = append(out, WorktreeInfo{
			Path:   wt.Path,
			Branch: wt.Branch,
			Head:   wt.Head,
		})
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestBranches|TestWorktrees_WithLinked" ./internal/git/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/git/branches.go internal/git/branches_test.go && git commit -m "feat(git): Branches + Worktrees/WorktreeInfo enumeration via git branch and git worktree list"
```

---

### Task 2.6: internal/fs — Node + ListDir (gitignore-aware)

**Files:**
- Create `internal/fs/fs.go`
- Create `internal/fs/listdir_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/fs/listdir_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestListDir_DirsFirstNameAsc(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	// Create: z.txt, a.txt, subdir/, subdir/file.go
	for _, name := range []string{"z.txt", "a.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "subdir", "file.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes, err := fs.ListDir(root, false)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(nodes) < 3 {
		t.Fatalf("expected ≥3 nodes, got %d", len(nodes))
	}
	// First entry must be the dir
	if !nodes[0].IsDir || nodes[0].Name != "subdir" {
		t.Errorf("expected first node to be dir 'subdir', got %+v", nodes[0])
	}
	// Files sorted by name asc
	if nodes[1].Name != "a.txt" || nodes[2].Name != "z.txt" {
		t.Errorf("unexpected file order: %s %s", nodes[1].Name, nodes[2].Name)
	}
	// Path must be absolute
	for _, n := range nodes {
		if !filepath.IsAbs(n.Path) {
			t.Errorf("node %s has non-absolute path %s", n.Name, n.Path)
		}
	}
}

func TestListDir_GitignoreAware(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}

	nodes, err := fs.ListDir(root, true)
	if err != nil {
		t.Fatalf("ListDir gitignore-aware: %v", err)
	}
	for _, n := range nodes {
		if n.Name == "skip.log" {
			t.Errorf("skip.log should have been filtered by .gitignore")
		}
		if n.Name == "ignored" {
			t.Errorf("ignored/ dir should have been filtered by .gitignore")
		}
	}
	found := false
	for _, n := range nodes {
		if n.Name == "keep.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("keep.go should be present")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestListDir_DirsFirstNameAsc|TestListDir_GitignoreAware" ./internal/fs/
```

Expected FAIL: `cannot find package "github.com/Miniature-Pug/perch/internal/fs"`

- [ ] **Step 3: Write minimal implementation**

```go
// internal/fs/fs.go
package fs

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Node is one entry in a directory listing.
// JSON tags are frozen — do not rename.
type Node struct {
	Name  string `json:"name"`
	Path  string `json:"path"`  // absolute
	IsDir bool   `json:"isDir"`
}

// ListDir returns the immediate children of absDir sorted dirs-first, then
// files ascending by name. When gitignoreAware is true, entries matching
// any pattern in absDir/.gitignore are excluded (single-level patterns only;
// no recursive gitignore walk).
func ListDir(absDir string, gitignoreAware bool) ([]Node, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var patterns []string
	if gitignoreAware {
		patterns = loadGitignorePatterns(filepath.Join(absDir, ".gitignore"))
	}

	var dirs, files []Node
	for _, e := range entries {
		name := e.Name()
		if gitignoreAware && matchesAny(name, patterns) {
			continue
		}
		n := Node{
			Name:  name,
			Path:  filepath.Join(absDir, name),
			IsDir: e.IsDir(),
		}
		if e.IsDir() {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return append(dirs, files...), nil
}

// loadGitignorePatterns reads pattern lines from a .gitignore file.
// Blank lines and comments (#) are ignored.
func loadGitignorePatterns(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchesAny reports whether name matches any gitignore pattern using
// doublestar glob matching. Trailing-slash patterns (dir patterns) match
// the name without the slash.
func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		if ok, _ := doublestar.Match(p, name); ok {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestListDir_DirsFirstNameAsc|TestListDir_GitignoreAware" ./internal/fs/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/fs.go internal/fs/listdir_test.go && git commit -m "feat(fs): Node + ListDir dirs-first name-asc with gitignore filtering via bmatcuk/doublestar"
```

---

### Task 2.7: internal/fs — ReadFile + WriteFile (atomic, mode-preserving)

**Files:**
- Modify `internal/fs/fs.go` (add `ReadFile`, `WriteFile`)
- Create `internal/fs/readwrite_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/fs/readwrite_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestReadWriteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")

	if err := fs.WriteFile(path, []byte("hello world")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWriteFile_ModePreservedOnOverwrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	// Create with mode 0o755
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Overwrite via WriteFile — mode must be preserved
	if err := fs.WriteFile(path, []byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("WriteFile overwrite: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho hi\n" {
		t.Errorf("content mismatch: %q", data)
	}
}

func TestWriteFile_NewFileDefaultMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	if err := fs.WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0 {
		t.Errorf("new file has mode 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestReadWriteRoundTrip|TestWriteFile_ModePreservedOnOverwrite|TestWriteFile_NewFileDefaultMode" ./internal/fs/
```

Expected FAIL: `undefined: fs.ReadFile`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/fs/fs.go`:

```go
// ReadFile reads and returns the contents of absPath.
func ReadFile(absPath string) ([]byte, error) {
	return os.ReadFile(absPath)
}

// WriteFile writes data to absPath atomically using a temp file + rename.
// If absPath already exists its permission bits are preserved; new files
// get mode 0o644.
func WriteFile(absPath string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(absPath); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, absPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestReadWriteRoundTrip|TestWriteFile_ModePreservedOnOverwrite|TestWriteFile_NewFileDefaultMode" ./internal/fs/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/fs.go internal/fs/readwrite_test.go && git commit -m "feat(fs): ReadFile + WriteFile atomic temp+rename, mode-preserving on overwrite"
```

---

### Task 2.8: internal/fs — Watcher via fsnotify

**Files:**
- Modify `internal/fs/fs.go` (add `Watcher`, `Watch`, `Close`)
- Create `internal/fs/watcher_test.go`

**Dependency note:** `github.com/fsnotify/fsnotify` must be added. Before editing go.mod by hand, verify the latest stable version:

```bash
# Verify latest stable from the Go module proxy — run this before go get:
curl -s 'https://proxy.golang.org/github.com/fsnotify/fsnotify/@latest' | grep -o '"Version":"[^"]*"'
# Then run:
go get github.com/fsnotify/fsnotify@<version-confirmed-above>
go mod tidy
go mod vendor
```

Do **not** hardcode a version without running the proxy check above.

- [ ] **Step 1: Write the failing test**

```go
// internal/fs/watcher_test.go
package fs_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/fs"
)

func TestWatcher_FileCreateFiresOnChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()

	fired := make(chan string, 4)
	w, err := fs.Watch(root, func(absPath string) {
		fired <- absPath
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer w.Close()

	newFile := filepath.Join(root, "created.txt")
	if err := os.WriteFile(newFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-fired:
		if filepath.Dir(got) != root && got != newFile {
			// fsnotify may report the dir or the file; either is acceptable
			t.Logf("onChange fired with path: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: onChange not fired after file create")
	}
}

func TestWatcher_Close(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	w, err := fs.Watch(root, func(string) {})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Second Close must not panic
	_ = w.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestWatcher_FileCreateFiresOnChange|TestWatcher_Close" ./internal/fs/
```

Expected FAIL: `undefined: fs.Watch`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/fs/fs.go` (also add `"sync"` to imports):

```go
import (
	// existing imports ...
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches a directory tree for filesystem changes.
type Watcher struct {
	fw       *fsnotify.Watcher
	onChange func(string)
	once     sync.Once
	done     chan struct{}
}

// Watch creates a Watcher for absRoot. onChange is called with the absolute
// path of any changed file or directory. Watch returns an error if fsnotify
// cannot be initialised or the root cannot be added.
func Watch(absRoot string, onChange func(absPath string)) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(absRoot); err != nil {
		_ = fw.Close()
		return nil, err
	}
	w := &Watcher{fw: fw, onChange: onChange, done: make(chan struct{})}
	go w.loop()
	return w, nil
}

func (w *Watcher) loop() {
	for {
		select {
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.onChange(event.Name)
		case _, ok := <-w.fw.Errors:
			if !ok {
				return
			}
		case <-w.done:
			return
		}
	}
}

// Close stops the watcher. Idempotent.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.fw.Close()
	})
	return err
}
```

After adding the code, run:

```bash
go get github.com/fsnotify/fsnotify@<version-confirmed-from-proxy>
go mod tidy
go mod vendor
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestWatcher_FileCreateFiresOnChange|TestWatcher_Close" ./internal/fs/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/fs.go internal/fs/watcher_test.go go.mod go.sum vendor/ && git commit -m "feat(fs): Watcher/Watch/Close via fsnotify — file-create fires onChange with path"
```

---

### Task 2.9: internal/fs — RevealInFiles + CopyPath

**Files:**
- Modify `internal/fs/fs.go` (add `RevealInFiles`, `CopyPath`, injectable runner seam)
- Create `internal/fs/reveal_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/fs/reveal_test.go
package fs_test

import (
	"testing"

	"github.com/Miniature-Pug/perch/internal/fs"
)

// fakeRevealRunner records the command passed to it without executing anything.
type fakeRevealRunner struct {
	calls [][]string
}

func (f *fakeRevealRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return nil
}

func TestRevealInFiles_CallsXdgOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeRevealRunner{}
	fs.SetRevealRunner(fake)
	defer fs.SetRevealRunner(nil) // restore default

	if err := fs.RevealInFiles("/home/user/project/file.go"); err != nil {
		t.Fatalf("RevealInFiles: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	cmd := fake.calls[0]
	if cmd[0] != "xdg-open" {
		t.Errorf("expected xdg-open, got %s", cmd[0])
	}
	// Must open the containing directory, not the file itself
	if cmd[1] != "/home/user/project" {
		t.Errorf("expected dir /home/user/project, got %s", cmd[1])
	}
}

func TestCopyPath_ReturnsPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const absPath = "/some/abs/path/to/file.go"
	got := fs.CopyPath(absPath)
	if got != absPath {
		t.Errorf("CopyPath(%q) = %q, want %q", absPath, got, absPath)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race -run "TestRevealInFiles_CallsXdgOpen|TestCopyPath_ReturnsPath" ./internal/fs/
```

Expected FAIL: `undefined: fs.SetRevealRunner`

- [ ] **Step 3: Write minimal implementation**

Add to `internal/fs/fs.go`:

```go
// RevealRunner is the seam for RevealInFiles so tests can inject a fake
// without launching xdg-open.
type RevealRunner interface {
	Run(name string, args ...string) error
}

// defaultRevealRunner uses os/exec to run the command.
type execRevealRunner struct{}

func (execRevealRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

// revealRunner is the active runner; nil means use the default exec runner.
var revealRunner RevealRunner

// SetRevealRunner replaces the runner used by RevealInFiles. Pass nil to
// restore the default (xdg-open via os/exec). For tests only.
func SetRevealRunner(r RevealRunner) {
	revealRunner = r
}

// RevealInFiles opens the directory containing absPath in the OS file manager
// via xdg-open. The injectable runner seam allows tests to assert the command
// without launching anything.
func RevealInFiles(absPath string) error {
	dir := filepath.Dir(absPath)
	r := revealRunner
	if r == nil {
		r = execRevealRunner{}
	}
	return r.Run("xdg-open", dir)
}

// CopyPath returns absPath. The actual clipboard write is performed frontend-side;
// this function exists so the app's bound method has a Go implementation to call.
func CopyPath(absPath string) string {
	return absPath
}
```

Also ensure `"os/exec"` is in the import block of `fs.go` (it was added in Task 2.7's WriteFile; confirm it's present).

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race -run "TestRevealInFiles_CallsXdgOpen|TestCopyPath_ReturnsPath" ./internal/fs/
```

Expected PASS

Run the full `internal/fs` suite to confirm no regressions:

```bash
go test -race ./internal/fs/
```

Expected PASS

- [ ] **Step 5: Commit**

```bash
git add internal/fs/fs.go internal/fs/reveal_test.go && git commit -m "feat(fs): RevealInFiles (xdg-open dir, injectable runner) + CopyPath returns path"
```

---

## Phase 2 — Backend Core (agent side-channel)

Tasks 2.10–2.19. Pure Go, TDD with `-race`, `t.Setenv("HOME", t.TempDir())`,
`httptest`, recorded fixtures and scripted fakes. Never run a real `claude` or
`opencode`. Branch: `feat/perch-v1`.

---

### Task 2.10: `internal/notify` — Notifier interface, dbus impl, FakeNotifier

**Files:** `internal/notify/notify.go`, `internal/notify/notify_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/notify/notify_test.go
package notify_test

import (
	"testing"
	"github.com/Miniature-Pug/perch/internal/notify"
)

func TestFakeNotifierRecordsCalls(t *testing.T) {
	t.Parallel()
	f := &notify.FakeNotifier{}
	_ = f.Notify("t1", "b1")
	_ = f.Notify("t2", "b2")
	if len(f.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(f.Calls))
	}
	if f.Calls[0].Title != "t1" || f.Calls[0].Body != "b1" {
		t.Errorf("call[0]: %+v", f.Calls[0])
	}
}

func TestRunnerSeamFallback(t *testing.T) {
	t.Parallel()
	var got []string
	n := notify.NewWithRunner(func(name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	})
	_ = n.Notify("hello", "world")
	if len(got) < 3 || got[0] != "notify-send" || got[1] != "hello" || got[2] != "world" {
		t.Errorf("unexpected argv: %v", got)
	}
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestFakeNotifier ./internal/notify/` → FAIL: no package

- [ ] **Step 3: Implement**

```go
// internal/notify/notify.go
package notify

import (
	"os/exec"
	"github.com/godbus/dbus/v5"
)

type Notifier interface{ Notify(title, body string) error }

type FakeNotifier struct{ Calls []struct{ Title, Body string } }
func (f *FakeNotifier) Notify(title, body string) error {
	f.Calls = append(f.Calls, struct{ Title, Body string }{title, body})
	return nil
}

// RunFunc is the injectable seam for notify-send (mirrors claude.go func-field idiom).
type RunFunc func(name string, args ...string) error

type dbusNotifier struct{}
func (d dbusNotifier) Notify(title, body string) error {
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Auth(nil); err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	return obj.Call("org.freedesktop.Notifications.Notify", 0,
		"perch", uint32(0), "", title, body,
		[]string{}, map[string]dbus.Variant{}, int32(5000)).Err
}

type runnerNotifier struct{ run RunFunc }
func (r runnerNotifier) Notify(title, body string) error { return r.run("notify-send", title, body) }

// New returns a dbus Notifier; falls back to notify-send if dbus is unavailable.
func New() Notifier {
	if _, err := dbus.SessionBusPrivate(); err == nil {
		return dbusNotifier{}
	}
	return NewWithRunner(func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	})
}

// NewWithRunner returns a Notifier backed by the injected runner (test seam).
func NewWithRunner(run RunFunc) Notifier { return runnerNotifier{run: run} }
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/notify/`

- [ ] **Step 5: Commit**
  ```bash
  # godbus/dbus/v5 is already indirect at v5.1.0; promote to direct:
  # 1. Confirm current latest: curl -s 'https://proxy.golang.org/github.com/godbus/dbus/v5/@latest' | jq .Version
  # 2. If newer stable exists: go get github.com/godbus/dbus/v5@<confirmed-version>
  # 3. go mod tidy && go mod vendor
  git add internal/notify/ go.mod go.sum vendor/
  git commit -m "feat(notify): Notifier interface, dbus impl, notify-send seam, FakeNotifier"
  ```

---

### Task 2.11: `internal/hooklistener` — bind, Addr, Token, Close, 401 on bad auth

**Files:** `internal/hooklistener/listener.go`, `internal/hooklistener/listener_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/hooklistener/listener_test.go
package hooklistener_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

func TestListenerAddrAndToken(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()
	if !strings.HasPrefix(l.Addr(), "127.0.0.1:") {
		t.Errorf("want 127.0.0.1:PORT, got %q", l.Addr())
	}
	if len(l.Token()) != 64 { // 256-bit = 32 bytes = 64 hex chars
		t.Errorf("want 64 hex token, got len=%d", len(l.Token()))
	}
}

func TestListenerUnauthorized(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()
	resp, err := http.Post(fmt.Sprintf("http://%s/hook", l.Addr()),
		"application/json", strings.NewReader(`{}`))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestListener ./internal/hooklistener/` → FAIL: no package

- [ ] **Step 3: Implement**

```go
// internal/hooklistener/listener.go
package hooklistener

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

type Decision struct {
	Allow  bool `json:"allow"`
	Always bool `json:"always"`
}

type HookEvent struct {
	Type           string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ErrorType      string          `json:"error_type"`
	ReqID          string          `json:"-"` // assigned by listener
}

type pending struct{ ch chan Decision }

type Listener struct {
	srv    *http.Server
	ln     net.Listener
	token  string
	events chan HookEvent
	mu     sync.Mutex
	reqs   map[string]*pending
}

func New() (*Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { return nil, fmt.Errorf("hooklistener.New: %w", err) }
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil { _ = ln.Close(); return nil, err }
	l := &Listener{
		ln: ln, token: hex.EncodeToString(raw),
		events: make(chan HookEvent, 64),
		reqs:   make(map[string]*pending),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", l.handleHook)
	l.srv = &http.Server{Handler: mux}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

func (l *Listener) Addr() string          { return l.ln.Addr().String() }
func (l *Listener) Token() string         { return l.token }
func (l *Listener) Events() <-chan HookEvent { return l.events }
func (l *Listener) Close() error          { return l.srv.Close() }

func (l *Listener) auth(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	return strings.HasPrefix(h, "Bearer ") && strings.TrimPrefix(h, "Bearer ") == l.token
}

func (l *Listener) handleHook(w http.ResponseWriter, r *http.Request) {
	if !l.auth(r) { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	// body decoding and routing in Tasks 2.12/2.13
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (l *Listener) Decide(reqID string, d Decision) {
	l.mu.Lock(); p := l.reqs[reqID]; l.mu.Unlock()
	if p != nil { p.ch <- d }
}
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/hooklistener/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/hooklistener/
  git commit -m "feat(hooklistener): Listener bind, token gen, 401 on missing auth"
  ```

---

### Task 2.12: `internal/hooklistener` — HookEvent + non-PreToolUse POST handler

**Files:** `internal/hooklistener/listener.go` (extend handleHook), `internal/hooklistener/listener_test.go`

- [ ] **Step 1: Write failing test** (add to existing test file)

```go
func TestStopEventArrives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()

	body := `{"hook_event_name":"Stop","session_id":"s1","transcript_path":"/t.jsonl","cwd":"/p"}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatalf("POST: %v", err) }
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK { t.Fatalf("want 200, got %d", resp.StatusCode) }

	select {
	case ev := <-l.Events():
		if ev.Type != "Stop" || ev.SessionID != "s1" || ev.TranscriptPath != "/t.jsonl" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestStopEvent ./internal/hooklistener/` → want 200, got 501

- [ ] **Step 3: Implement** — replace `handleHook` body:

```go
func (l *Listener) handleHook(w http.ResponseWriter, r *http.Request) {
	if !l.auth(r) { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	var ev HookEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	if ev.Type != "PreToolUse" {
		select { case l.events <- ev: default: }
		w.WriteHeader(http.StatusOK)
		return
	}
	// PreToolUse handled in Task 2.13
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/hooklistener/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/hooklistener/
  git commit -m "feat(hooklistener): HookEvent type + non-PreToolUse POST handler"
  ```

---

### Task 2.13: `internal/hooklistener` — PreToolUse blocking path + `Decide`

**Files:** `internal/hooklistener/listener.go`, `internal/hooklistener/listener_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestPreToolUseAllowDeny(t *testing.T) {
	for _, tc := range []struct{ name string; allow bool; want string }{
		{"allow", true, "allow"}, {"deny", false, "deny"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Setenv("HOME", t.TempDir())
			l, err := hooklistener.New()
			if err != nil { t.Fatalf("New: %v", err) }
			defer func() { _ = l.Close() }()

			payload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`
			req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+l.Token())
			req.Header.Set("Content-Type", "application/json")

			type result struct{ body string; code int }
			ch := make(chan result, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err != nil { ch <- result{code: -1}; return }
				defer func() { _ = resp.Body.Close() }()
				b, _ := io.ReadAll(resp.Body)
				ch <- result{body: strings.TrimSpace(string(b)), code: resp.StatusCode}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			select {
			case ev := <-l.Events():
				if ev.Type != "PreToolUse" || ev.ReqID == "" { t.Errorf("bad event: %+v", ev) }
				l.Decide(ev.ReqID, hooklistener.Decision{Allow: tc.allow})
			case <-ctx.Done():
				t.Fatal("timeout waiting for event")
			}

			select {
			case r := <-ch:
				if r.code != http.StatusOK { t.Errorf("want 200, got %d", r.code) }
				want := `"permissionDecision":"` + tc.want + `"`
				if !strings.Contains(r.body, want) { t.Errorf("body %q missing %q", r.body, want) }
			case <-ctx.Done():
				t.Fatal("timeout waiting for response")
			}
		})
	}
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestPreToolUse ./internal/hooklistener/` → want 200, got 501

- [ ] **Step 3: Implement** — replace the PreToolUse branch in `handleHook`:

```go
// (inside handleHook, after the non-PreToolUse path returns)
reqBytes := make([]byte, 8); _, _ = rand.Read(reqBytes)
ev.ReqID = hex.EncodeToString(reqBytes)
p := &pending{ch: make(chan Decision, 1)}
l.mu.Lock(); l.reqs[ev.ReqID] = p; l.mu.Unlock()
defer func() { l.mu.Lock(); delete(l.reqs, ev.ReqID); l.mu.Unlock() }()
select { case l.events <- ev: default: }
d := <-p.ch
perm := "deny"
if d.Allow { perm = "allow" }
w.Header().Set("Content-Type", "application/json")
_ = json.NewEncoder(w).Encode(map[string]any{
	"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": perm,
	},
})
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/hooklistener/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/hooklistener/
  git commit -m "feat(hooklistener): PreToolUse blocking path, Decide unblocks response"
  ```

---

### Task 2.14: `internal/agent` — Monitor interface, types, NewMonitor dispatch

**Files:** `internal/agent/monitor.go`, `internal/agent/claude_monitor.go` (stub),
`internal/agent/opencode_monitor.go` (stub), `internal/agent/monitor_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/agent/monitor_test.go
package agent_test

import (
	"context"
	"testing"
	"github.com/Miniature-Pug/perch/internal/agent"
)

func TestNewMonitorDispatch(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"claude", "opencode"} {
		m, err := agent.NewMonitor(tool, agent.NewClaude())
		if err != nil || m == nil {
			t.Errorf("NewMonitor(%q): err=%v m=%v", tool, err, m)
		}
	}
	_, err := agent.NewMonitor("unknown", agent.NewClaude())
	if err == nil { t.Error("want error for unknown tool") }
}

func TestNewMonitorNilSafe(t *testing.T) {
	t.Parallel()
	t.Setenv("HOME", t.TempDir())
	// Prepare on a production-path monitor must not panic (listener created lazily).
	m, err := agent.NewMonitor("claude", agent.NewClaude())
	if err != nil { t.Fatalf("NewMonitor: %v", err) }
	if m.Events() == nil { t.Error("Events() channel must not be nil") }
	if err := m.Teardown(); err != nil { t.Errorf("Teardown: %v", err) }
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestNewMonitor ./internal/agent/` → FAIL: no Monitor

- [ ] **Step 3: Implement `monitor.go`** with all frozen types:

```go
// internal/agent/monitor.go
package agent

import (
	"context"
	"fmt"
)

type State string
const (
	StateRunning          State = "running"
	StateIdle             State = "idle"
	StateAwaitingApproval State = "awaiting-approval"
	StateDone             State = "done"
	StateErrored          State = "errored"
)

type Caps     struct { Approvals, Attention, Tokens bool }
type Decision struct { Allow, Always bool }
type ApprovalReq struct { ReqID, Tool, Summary string }

type Event struct {
	WorkspaceID string       `json:"workspaceId"`
	Kind        string       `json:"kind"`
	State       State        `json:"state"`
	Tokens      int          `json:"tokens"`
	Cost        float64      `json:"cost"`
	Approval    *ApprovalReq `json:"approval,omitempty"`
	Err         string       `json:"err,omitempty"`
}

type Monitor interface {
	Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
	Events() <-chan Event
	Approve(reqID string, d Decision) error
	Capabilities() Caps
	Teardown() error
	CurrentState() State      // last observed lifecycle state (mutex-guarded); StateIdle before the first event
	LastApprovalTool() string // tool name of the most recent PreToolUse approval request ("" if none yet)
}

// IMPLEMENTATION NOTE (applies to FakeMonitor, ClaudeMonitor, OpencodeMonitor):
// every Monitor tracks two mutex-guarded fields updated as Events are produced —
// `state State` (set from each emitted Event.State; CurrentState() returns it,
// defaulting to StateIdle when unset) and `lastTool string` (set from
// Event.Approval.Tool on each approval event; LastApprovalTool() returns it).
// app.ListWorkspaces (Task 3.2) reads CurrentState(); app.Approve (Task 3.7)
// reads LastApprovalTool() to persist an AlwaysRule. These accessors are part of
// the frozen Monitor contract — Tasks 2.15/2.17/2.19 must implement them.

func NewMonitor(tool string, adapter Adapter) (Monitor, error) {
	switch tool {
	case "claude":
		return newClaudeMonitor(adapter), nil
	case "opencode":
		return newOpencodeMonitor(adapter), nil
	default:
		return nil, fmt.Errorf("agent.NewMonitor: unknown tool %q", tool)
	}
}
```

- [ ] **Step 4: Add compiling stubs** for `claude_monitor.go` and `opencode_monitor.go`
  (full fields shown; method bodies filled in 2.16/2.19):

```go
// internal/agent/claude_monitor.go
package agent

import (
	"context"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when we created listener; Teardown closes it
	events   chan Event
	cwd      string
}

func newClaudeMonitor(a Adapter) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, events: make(chan Event, 64)}
}
func NewClaudeMonitorWithListener(a Adapter, l *hooklistener.Listener) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, listener: l, events: make(chan Event, 64)}
}
func (m *ClaudeMonitor) Events() <-chan Event { return m.events }
func (m *ClaudeMonitor) Approve(_ string, _ Decision) error { return nil }
func (m *ClaudeMonitor) Capabilities() Caps { return Caps{Approvals: true, Attention: true, Tokens: true} }
func (m *ClaudeMonitor) Prepare(_ context.Context, _, _, _ string) (string, error) { return "claude", nil }
func (m *ClaudeMonitor) Teardown() error {
	if m.ownedLn && m.listener != nil { return m.listener.Close() }
	return nil
}
```

```go
// internal/agent/opencode_monitor.go
package agent

import (
	"context"
	"net/http"
)

type OpencodeMonitor struct {
	adapter    Adapter
	serverURL  string
	password   string
	events     chan Event
	httpClient *http.Client
}

func newOpencodeMonitor(a Adapter) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, events: make(chan Event, 64), httpClient: &http.Client{}}
}
func NewOpencodeMonitorWithServer(a Adapter, serverURL, pw string) *OpencodeMonitor {
	return &OpencodeMonitor{adapter: a, serverURL: serverURL, password: pw,
		events: make(chan Event, 64), httpClient: &http.Client{}}
}
func (m *OpencodeMonitor) Events() <-chan Event { return m.events }
func (m *OpencodeMonitor) Approve(_ string, _ Decision) error { return nil }
func (m *OpencodeMonitor) Capabilities() Caps { return Caps{Approvals: true, Attention: true, Tokens: true} }
func (m *OpencodeMonitor) Prepare(_ context.Context, _, _, _ string) (string, error) { return "opencode", nil }
func (m *OpencodeMonitor) Teardown() error { return nil }
```

- [ ] **Step 5: Run passing** — `go test -race -count=1 ./internal/agent/`

- [ ] **Step 6: Commit**
  ```bash
  git add internal/agent/monitor.go internal/agent/claude_monitor.go \
          internal/agent/opencode_monitor.go internal/agent/monitor_test.go
  git commit -m "feat(agent): Monitor interface, types, NewMonitor dispatch with nil-safe stubs"
  ```

---

### Task 2.15: `internal/agent` — FakeMonitor scripted test double

**Files:** `internal/agent/fake_monitor.go`, `internal/agent/fake_monitor_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/agent/fake_monitor_test.go
package agent_test

import (
	"context"
	"testing"
	"time"
	"github.com/Miniature-Pug/perch/internal/agent"
)

func TestFakeMonitorEventSequence(t *testing.T) {
	t.Parallel()
	t.Setenv("HOME", t.TempDir())
	seq := []agent.Event{
		{Kind: "state", State: agent.StateRunning},
		{Kind: "approval", State: agent.StateAwaitingApproval,
			Approval: &agent.ApprovalReq{ReqID: "r1", Tool: "Bash", Summary: "ls"}},
		{Kind: "usage", Tokens: 200, Cost: 0.001},
		{Kind: "state", State: agent.StateDone},
	}
	f := agent.NewFakeMonitor(seq)

	cmd, err := f.Prepare(context.Background(), "ws1", "/repo", "")
	if err != nil || cmd == "" { t.Fatalf("Prepare: err=%v cmd=%q", err, cmd) }
	caps := f.Capabilities()
	if !caps.Approvals || !caps.Attention || !caps.Tokens {
		t.Errorf("want all-true caps, got %+v", caps)
	}

	var got []agent.Event
	deadline := time.After(3 * time.Second)
	for len(got) < len(seq) {
		select {
		case ev := <-f.Events(): got = append(got, ev)
		case <-deadline: t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].State != agent.StateRunning { t.Errorf("ev[0]: %+v", got[0]) }
	if got[1].Approval == nil || got[1].Approval.ReqID != "r1" { t.Errorf("ev[1]: %+v", got[1]) }

	_ = f.Approve("r1", agent.Decision{Allow: true})
	if len(f.Decisions()) == 0 || !f.Decisions()[0].Allow { t.Error("want recorded allow") }
	_ = f.Teardown()
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestFakeMonitor ./internal/agent/` → FAIL: undefined NewFakeMonitor

- [ ] **Step 3: Implement**

```go
// internal/agent/fake_monitor.go
package agent

import (
	"context"
	"sync"
)

type FakeMonitor struct {
	sequence  []Event
	events    chan Event
	mu        sync.Mutex
	decisions []Decision
	state     State
	lastTool  string
}

func NewFakeMonitor(seq []Event) *FakeMonitor {
	return &FakeMonitor{sequence: seq, events: make(chan Event, len(seq)+4)}
}
func (f *FakeMonitor) Prepare(_ context.Context, workspaceID, _, _ string) (string, error) {
	go func() {
		for _, ev := range f.sequence {
			ev.WorkspaceID = workspaceID
			f.mu.Lock()
			if ev.State != "" {
				f.state = ev.State
			}
			if ev.Approval != nil && ev.Approval.Tool != "" {
				f.lastTool = ev.Approval.Tool
			}
			f.mu.Unlock()
			f.events <- ev
		}
	}()
	return "claude --fake", nil
}
func (f *FakeMonitor) Events() <-chan Event               { return f.events }
func (f *FakeMonitor) Approve(_ string, d Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions = append(f.decisions, d)
	return nil
}
func (f *FakeMonitor) Capabilities() Caps { return Caps{true, true, true} }
func (f *FakeMonitor) Teardown() error    { return nil }
func (f *FakeMonitor) Decisions() []Decision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decisions
}
func (f *FakeMonitor) CurrentState() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return StateIdle
	}
	return f.state
}
func (f *FakeMonitor) LastApprovalTool() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTool
}
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/agent/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/agent/fake_monitor.go internal/agent/fake_monitor_test.go
  git commit -m "feat(agent): FakeMonitor scripted test double, all-true Caps"
  ```

---

### Task 2.16: `ClaudeMonitor` part 1 — Prepare (hook config) + Teardown

**Files:** `internal/agent/claude_monitor.go`, `internal/agent/claude_monitor_test.go`

Reuse the `mergeClaudeHooks`/`dropPerchGroups` **algorithm** from `claude.go` (read
before writing): additive merge, sentinel-keyed group detection, atomic temp+rename.
The target here is `<cwd>/.claude/settings.json` (workspace-scoped, not user-global).
Perch-owned groups are identified by a stable marker string `"perch-monitor-hook"` in
the command (addr/token vary per process, so they cannot be the sentinel). `Teardown`
drops only perch-owned groups; foreign hooks survive.

- [ ] **Step 1: Write failing tests** (shared helper `newMonitorWithTestListener` used
  by 2.16/2.17/2.18 to avoid repeating listener setup):

```go
// internal/agent/claude_monitor_test.go
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

// newMonitorWithTestListener creates a ClaudeMonitor backed by a real in-process
// hooklistener. Caller defers cleanup().
func newMonitorWithTestListener(t *testing.T) (*agent.ClaudeMonitor, *hooklistener.Listener, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("listener: %v", err) }
	m := agent.NewClaudeMonitorWithListener(agent.NewClaude(), l)
	return m, l, func() { _ = l.Close() }
}

func TestClaudeMonitorPrepare(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	_ = os.MkdirAll(worktree, 0o755)

	// Pre-seed a foreign Stop hook so we can assert it survives.
	claudeDir := filepath.Join(worktree, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	foreign := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"foreign-tool notify"}]}]}}`
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(foreign), 0o644)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
	if err != nil { t.Fatalf("Prepare: %v", err) }
	if !strings.HasPrefix(cmd, "claude") { t.Errorf("unexpected cmd: %q", cmd) }

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s map[string]any
	_ = json.Unmarshal(data, &s)
	hooks, _ := s["hooks"].(map[string]any)
	for _, ev := range []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"} {
		arr, _ := hooks[ev].([]any)
		if len(arr) == 0 { t.Errorf("hooks[%q] missing after Prepare", ev) }
	}
	// Foreign Stop hook must still be present.
	stopArr, _ := hooks["Stop"].([]any)
	found := false
	for _, item := range stopArr {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") { found = true }
		}
	}
	if !found { t.Error("foreign Stop hook was removed by Prepare") }

	// Teardown must remove perch hooks but leave foreign ones.
	_ = m.Teardown()
	data2, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s2 map[string]any
	_ = json.Unmarshal(data2, &s2)
	hooks2, _ := s2["hooks"].(map[string]any)
	stopArr2, _ := hooks2["Stop"].([]any)
	foreignStillThere := false
	for _, item := range stopArr2 {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") { foreignStillThere = true }
			if strings.Contains(fmt.Sprint(hm["command"]), "perch-monitor-hook") {
				t.Error("perch hook survived Teardown")
			}
		}
	}
	if !foreignStillThere { t.Error("foreign Stop hook missing after Teardown") }
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestClaudeMonitorPrepare ./internal/agent/` → FAIL

- [ ] **Step 3: Implement** — replace Prepare/Teardown stubs in `claude_monitor.go`:

```go
// (full replacement of method bodies; imports: bufio, bytes, context, encoding/json,
//  fmt, os, path/filepath, strings, hooklistener)

const perchMonitorSentinel = "perch-monitor-hook"
var perchMonitorEvents = []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"}

func (m *ClaudeMonitor) Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (string, error) {
	m.cwd = cwd
	if m.listener == nil {
		l, err := hooklistener.New()
		if err != nil { return "", fmt.Errorf("ClaudeMonitor.Prepare: listener: %w", err) }
		m.listener = l; m.ownedLn = true
	}
	if err := m.writeHooks(cwd); err != nil { return "", err }
	var args []string
	if resumeID != "" { args = m.adapter.ResumeArgs(resumeID) } else { args = m.adapter.NewArgs(NewOpts{}) }
	return strings.Join(append([]string{m.adapter.Name()}, args...), " "), nil
}

func (m *ClaudeMonitor) writeHooks(cwd string) error {
	dir := filepath.Join(cwd, ".claude")
	path := filepath.Join(dir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) { return err }
	merged, err := mergeMonitorHooks(existing, m.listener.Addr(), m.listener.Token())
	if err != nil { return err }
	if err := os.MkdirAll(dir, 0o755); err != nil { return err }
	return atomicWrite(path, merged)
}

// mergeMonitorHooks is pure (no I/O). It uses the same additive-append+sentinel
// idiom as mergeClaudeHooks in claude.go: each perch group has a command
// containing perchMonitorSentinel so it can be identified on Teardown.
func mergeMonitorHooks(existing []byte, addr, token string) ([]byte, error) {
	var s map[string]any
	if tr := strings.TrimSpace(string(existing)); tr != "" {
		if err := json.Unmarshal(existing, &s); err != nil { return nil, err }
	}
	if s == nil { s = map[string]any{} }
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil { hm = map[string]any{} }
	s["hooks"] = hm

	url := "http://" + addr + "/hook"
	cmd := fmt.Sprintf(`curl -sf -X POST -H "Authorization: Bearer %s" -H "Content-Type: application/json" -d @- %s # %s`,
		token, url, perchMonitorSentinel)
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		if isPerchMonitorGroupPresent(arr) { continue } // idempotent
		arr = append(arr, map[string]any{
			"matcher": "",
			"hooks":   []any{map[string]any{"type": "command", "command": cmd}},
		})
		hm[ev] = arr
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil { return nil, err }
	return append(out, '\n'), nil
}

func isPerchMonitorGroupPresent(arr []any) bool {
	for _, item := range arr {
		g, _ := item.(map[string]any)
		if isPerchMonitorGroup(g) { return true }
	}
	return false
}

func isPerchMonitorGroup(g map[string]any) bool {
	hs, _ := g["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if strings.Contains(fmt.Sprint(hm["command"]), perchMonitorSentinel) { return true }
	}
	return false
}

func (m *ClaudeMonitor) Teardown() error {
	if m.cwd != "" {
		path := filepath.Join(m.cwd, ".claude", "settings.json")
		if data, err := os.ReadFile(path); err == nil {
			_ = removeMonitorHooks(path, data)
		}
	}
	if m.ownedLn && m.listener != nil { return m.listener.Close() }
	return nil
}

func removeMonitorHooks(path string, data []byte) error {
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil { return nil }
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil { return nil }
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		var kept []any
		for _, item := range arr {
			g, _ := item.(map[string]any)
			if !isPerchMonitorGroup(g) { kept = append(kept, item) }
		}
		if len(kept) == 0 { delete(hm, ev) } else { hm[ev] = kept }
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	return atomicWrite(path, append(out, '\n'))
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil { return err }
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil { _ = tmp.Close(); _ = os.Remove(name); return err }
	if err := tmp.Close(); err != nil { _ = os.Remove(name); return err }
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil { mode = fi.Mode().Perm() }
	_ = os.Chmod(name, mode)
	return os.Rename(name, path)
}
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/agent/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/agent/claude_monitor.go internal/agent/claude_monitor_test.go
  git commit -m "feat(agent): ClaudeMonitor Prepare writes workspace hook config; Teardown restores foreign hooks"
  ```

---

### Task 2.17: `ClaudeMonitor` part 2 — HookEvent → agent.Event translation + Approve routing

**Files:** `internal/agent/claude_monitor.go`, `internal/agent/claude_monitor_test.go`

- [ ] **Step 1: Write failing test** (reuses `newMonitorWithTestListener` from 2.16 test file)

```go
func TestClaudeMonitorEventTranslation(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartTranslating(ctx)

	post := func(payload string) {
		req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil { _ = resp.Body.Close() }
	}
	post(`{"hook_event_name":"SessionStart","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)
	post(`{"hook_event_name":"Stop","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 2 {
		select {
		case ev := <-m.Events(): got = append(got, ev)
		case <-deadline: t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].Kind != "state" || got[0].State != agent.StateRunning { t.Errorf("ev[0]: %+v", got[0]) }
	if got[1].Kind != "state" || got[1].State != agent.StateIdle { t.Errorf("ev[1]: %+v", got[1]) }
}
```

- [ ] **Step 2: Run failing** — `go test -race -run TestClaudeMonitorEventTranslation ./internal/agent/` → FAIL

- [ ] **Step 3: Implement** — add `StartTranslating` and `translateAndEmit` to `claude_monitor.go`, and real `Approve`:

```go
func (m *ClaudeMonitor) StartTranslating(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done(): return
			case he, ok := <-m.listener.Events():
				if !ok { return }
				m.translateAndEmit(he)
			}
		}
	}()
}

func (m *ClaudeMonitor) translateAndEmit(he hooklistener.HookEvent) {
	switch he.Type {
	case "SessionStart":
		m.events <- Event{Kind: "state", State: StateRunning}
	case "Stop":
		m.events <- Event{Kind: "state", State: StateIdle}
	case "StopFailure":
		m.events <- Event{Kind: "state", State: StateErrored, Err: he.ErrorType}
	case "Notification":
		m.events <- Event{Kind: "state", State: StateIdle}
	case "PreToolUse":
		sum := he.ToolName
		if len(he.ToolInput) > 0 && len(he.ToolInput) < 120 { sum += ": " + string(he.ToolInput) }
		m.events <- Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: &ApprovalReq{ReqID: he.ReqID, Tool: he.ToolName, Summary: sum}}
	}
}

func (m *ClaudeMonitor) Approve(reqID string, d Decision) error {
	m.listener.Decide(reqID, hooklistener.Decision{Allow: d.Allow, Always: d.Always})
	return nil
}
```

- [ ] **Step 4: Run passing** — `go test -race -count=1 ./internal/agent/`

- [ ] **Step 5: Commit**
  ```bash
  git add internal/agent/claude_monitor.go internal/agent/claude_monitor_test.go
  git commit -m "feat(agent): ClaudeMonitor translates HookEvents to agent.Events; routes Approve to Decide"
  ```

---

### Task 2.18: `ClaudeMonitor` part 3 — transcript tail for usage events

**Files:** `internal/agent/claude_monitor.go`, `internal/agent/claude_monitor_test.go`,
`internal/agent/testdata/claude/transcript-usage.jsonl` (new)

- [ ] **Step 1: Add fixture**

```jsonl
{"type":"user","sessionId":"uu-1","cwd":"/repo","message":{"role":"user","content":"hi"}}
{"type":"assistant","sessionId":"uu-1","cwd":"/repo","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":10,"output_tokens":5}}}
{"type":"assistant","sessionId":"uu-1","cwd":"/repo","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
```

Third record has no `usage` — implementation must tolerate this (emit nothing, no error).
If Spike 2 reveals additional fields (e.g. `cost`), add them to the fixture and the impl
then; for now `Event.Cost` stays 0 when absent.

- [ ] **Step 2: Write failing test** (add to `claude_monitor_test.go`)

```go
func TestClaudeMonitorTranscriptTail(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	abs, _ := filepath.Abs(filepath.Join("testdata", "claude", "transcript-usage.jsonl"))
	m.TailTranscript(ctx, abs)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-m.Events():
			if ev.Kind == "usage" {
				if ev.Tokens != 15 { t.Errorf("want tokens=15 (10+5), got %d", ev.Tokens) }
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for usage event")
		}
	}
}
```

- [ ] **Step 3: Run failing** — `go test -race -run TestClaudeMonitorTranscriptTail ./internal/agent/` → FAIL

- [ ] **Step 4: Implement** (add to `claude_monitor.go`; imports: `bufio`, `encoding/json`, `os`):

```go
// TailTranscript reads transcriptPath and emits a usage Event for each assistant
// record carrying a usage field. Absent fields produce no event, set no error.
func (m *ClaudeMonitor) TailTranscript(ctx context.Context, transcriptPath string) {
	go func() {
		f, err := os.Open(transcriptPath)
		if err != nil { return }
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			select { case <-ctx.Done(): return; default: }
			var rec struct {
				Type    string `json:"type"`
				Message *struct {
					Usage *struct {
						Input  int `json:"input_tokens"`
						Output int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "assistant" ||
				rec.Message == nil || rec.Message.Usage == nil { continue }
			total := rec.Message.Usage.Input + rec.Message.Usage.Output
			if total > 0 { m.events <- Event{Kind: "usage", Tokens: total} }
		}
	}()
}
```

- [ ] **Step 5: Run passing** — `go test -race -count=1 ./internal/agent/`

- [ ] **Step 6: Commit**
  ```bash
  git add internal/agent/claude_monitor.go internal/agent/claude_monitor_test.go \
          internal/agent/testdata/claude/transcript-usage.jsonl
  git commit -m "feat(agent): ClaudeMonitor transcript tail emits usage events; tolerates absent usage fields"
  ```

---

### Task 2.19: `OpencodeMonitor` — Prepare, SSE parser, Approve, Capabilities

**Files:** `internal/agent/opencode_monitor.go`, `internal/agent/opencode_monitor_test.go`,
`internal/agent/testdata/opencode/events.sse` (new)

> **Verify before implementing:** Spike 3 is not yet run. The SSE frame shapes
> (`step.tokens.*`, `step.cost`), the `POST /permission` path, and the body
> schema below are based on opencode's documented server API. Before merging,
> confirm against `docs/superpowers/spikes/3-opencode-sse-approval.md` findings.

- [ ] **Step 1: Add SSE fixture** (`internal/agent/testdata/opencode/events.sse`)

```
data: {"type":"session.next.step.started","sessionId":"ses-1","step":{"type":"text"}}

data: {"type":"session.next.step.ended","sessionId":"ses-1","step":{"type":"text","cost":0.0012,"tokens":{"input":100,"output":40}}}

data: {"type":"permission.v2.asked","sessionId":"ses-1","permissionId":"perm-1","tool":"Bash","input":{"command":"ls"}}

data: {"type":"session.next.step.failed","sessionId":"ses-1","error":"timeout"}

```

- [ ] **Step 2: Write failing test**

```go
// internal/agent/opencode_monitor_test.go
package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
)

func TestOpencodeMonitorSSEParser(t *testing.T) {
	t.Parallel()
	t.Setenv("HOME", t.TempDir())

	fixture, err := os.ReadFile(filepath.Join("testdata", "opencode", "events.sse"))
	if err != nil { t.Fatalf("fixture: %v", err) }

	var postedDecision string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(fixture)
		case "/permission":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			postedDecision = body["decision"]
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	om := agent.NewOpencodeMonitorWithServer(agent.NewOpencode(), srv.URL, "test-pw")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	om.StartSSE(ctx)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 3 {
		select {
		case ev := <-om.Events(): got = append(got, ev)
		case <-deadline: t.Fatalf("timeout after %d events", len(got))
		}
	}

	if got[0].Kind != "state" || got[0].State != agent.StateRunning {
		t.Errorf("ev[0]: want state=running, got %+v", got[0])
	}
	if got[1].Kind != "usage" || got[1].Tokens != 140 {
		t.Errorf("ev[1]: want usage tokens=140, got %+v", got[1])
	}
	if got[2].State != agent.StateAwaitingApproval || got[2].Approval == nil {
		t.Errorf("ev[2]: want awaiting-approval, got %+v", got[2])
	}

	_ = om.Approve(got[2].Approval.ReqID, agent.Decision{Allow: true})
	if postedDecision != "once" { t.Errorf("want decision=once, got %q", postedDecision) }

	caps := om.Capabilities()
	if !caps.Approvals || !caps.Attention || !caps.Tokens { t.Errorf("caps: %+v", caps) }
}
```

- [ ] **Step 3: Run failing** — `go test -race -run TestOpencodeMonitor ./internal/agent/` → FAIL

- [ ] **Step 4: Implement** — replace stubs in `opencode_monitor.go`:

```go
// (full replacement; imports: bufio, bytes, context, encoding/json, fmt, net/http, strings)

func (m *OpencodeMonitor) Prepare(_ context.Context, _, cwd, _ string) (string, error) {
	return fmt.Sprintf(
		"OPENCODE_SERVER_PASSWORD=%s opencode serve & opencode attach $OPENCODE_URL",
		m.password), nil
}

func (m *OpencodeMonitor) StartSSE(ctx context.Context) {
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.serverURL+"/event", nil)
		if err != nil { return }
		req.Header.Set("Authorization", "Bearer "+m.password)
		resp, err := m.httpClient.Do(req)
		if err != nil { return }
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") { continue }
			m.translateSSE([]byte(strings.TrimPrefix(line, "data: ")))
		}
	}()
}

type sseFrame struct {
	Type         string `json:"type"`
	PermissionID string `json:"permissionId"`
	Tool         string `json:"tool"`
	Error        string `json:"error"`
	Step         *struct {
		Cost   float64 `json:"cost"`
		Tokens *struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
	} `json:"step"`
	Input json.RawMessage `json:"input"`
}

func (m *OpencodeMonitor) translateSSE(data []byte) {
	var f sseFrame
	if json.Unmarshal(data, &f) != nil { return }
	switch f.Type {
	case "session.next.step.started":
		m.events <- Event{Kind: "state", State: StateRunning}
	case "session.next.step.ended":
		if f.Step != nil && f.Step.Tokens != nil {
			m.events <- Event{Kind: "usage",
				Tokens: f.Step.Tokens.Input + f.Step.Tokens.Output, Cost: f.Step.Cost}
		}
	case "session.next.step.failed":
		m.events <- Event{Kind: "state", State: StateErrored, Err: f.Error}
	case "permission.v2.asked":
		sum := f.Tool
		if len(f.Input) > 0 && len(f.Input) < 120 { sum += ": " + string(f.Input) }
		m.events <- Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: &ApprovalReq{ReqID: f.PermissionID, Tool: f.Tool, Summary: sum}}
	}
}

func (m *OpencodeMonitor) Approve(reqID string, d Decision) error {
	decision := "reject"
	if d.Allow && d.Always { decision = "always" } else if d.Allow { decision = "once" }
	body, _ := json.Marshal(map[string]string{"permissionId": reqID, "decision": decision})
	req, _ := http.NewRequest(http.MethodPost, m.serverURL+"/permission", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.password)
	resp, err := m.httpClient.Do(req)
	if err != nil { return err }
	return resp.Body.Close()
}
```

- [ ] **Step 5: Run all Phase 2 tests passing**
  ```bash
  go test -race -count=1 ./internal/notify/ ./internal/hooklistener/ ./internal/agent/
  go build ./...
  ```

- [ ] **Step 6: Commit**
  ```bash
  git add internal/agent/opencode_monitor.go internal/agent/opencode_monitor_test.go \
          internal/agent/testdata/opencode/events.sse
  git commit -m "feat(agent): OpencodeMonitor SSE parser, Approve REST reply, all-true Caps"
  ```

---

## Phase 3 — App & CLI

> **Branch:** `feat/perch-v1`  
> **Prereqs:** Phases 0–2 green (demolition done; `internal/pty`, `internal/registry`,
> `internal/git` hunk API, `internal/fs`, `internal/agent` Monitor interface + fakes all
> implemented and passing).  
> **Run commands (per task):**  
> - Unit: `go test -race -count=1 ./...`  
> - Single pkg: `go test -race -count=1 -run TestName ./app/` (or `./cmd/perch/`)  
> - Build gate: `go build ./...`

---

### Task 3.1: App struct + constructor + `startup`/`shutdown`

Replace the old poller-based `App` with the new struct that holds a
`*registry.Store`, a map of paneID→`*pty.Bridge`, a map of
workspaceID→`agent.Monitor`, and the `emit` seam.  On shutdown, every Bridge is
closed and every Monitor is torn down.  The old `SessionInfo`, `ListSessions`,
`liveSession`, `OpenTerminal`, `KillSession`, `sessionsSignature`, `pollOnce`,
`startPolling` are removed.

**Files:** `app/app.go`  
**New test file:** `app/app_test.go` (replace existing; keep `validateSessionID*` +
`validateWorktreeUnderRoots*` + `containedUnderRoots*` subtests verbatim — they still
compile)

- [ ] **Step 1: Write failing tests** — add to `app/app_test.go`:

```go
package app

import (
	"testing"
	"sync"

	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/registry"
)

// fakeBridge is a test double for *pty.Bridge. We only need Close to be
// observable; Write/Resize delegate to the real zero-value Bridge (no-op).
type fakeBridge struct {
	closed int
	mu     sync.Mutex
}

func (f *fakeBridge) Close() error        { f.mu.Lock(); defer f.mu.Unlock(); f.closed++; return nil }
func (f *fakeBridge) Write([]byte) (int, error) { return 0, nil }
func (f *fakeBridge) Resize(_, _ uint16) error  { return nil }

func TestNewApp_Fields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	a := NewApp(store, []string{"/tmp/root"})
	if a == nil {
		t.Fatal("NewApp returned nil")
	}
	if a.bridges == nil {
		t.Fatal("bridges map not initialised")
	}
	if a.monitors == nil {
		t.Fatal("monitors map not initialised")
	}
	if a.emit == nil {
		t.Fatal("emit seam must be non-nil before startup")
	}
}

func TestApp_Shutdown_ClosesBridgesAndTearsDownMonitors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	fb := &fakeBridge{}
	fm := agent.NewFakeMonitor("ws1")

	a := &App{
		store:    store,
		roots:    []string{"/tmp"},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	// Inject a real Bridge backed by the fake closer, and a FakeMonitor.
	a.bridges["pane-1"] = makeBridgeWithCloser(fb)
	a.monitors["ws1"] = fm

	a.shutdown(t.Context())

	if fb.closed == 0 {
		t.Fatal("shutdown must close all Bridges")
	}
	if !fm.TornDown() {
		t.Fatal("shutdown must call Monitor.Teardown on all monitors")
	}
}

func TestApp_Shutdown_Idempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{"/tmp"},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	a.shutdown(t.Context())
	a.shutdown(t.Context()) // must not panic
}
```

`makeBridgeWithCloser` is a package-internal helper in `app/app_test.go` that
builds a real `*internalpty.Bridge` whose closer calls `fb.Close()`:

```go
func makeBridgeWithCloser(f *fakeBridge) *internalpty.Bridge {
	return internalpty.NewBridgeForTest(f.Close)
}
```

> **Note:** `internalpty.NewBridgeForTest(closer func() error) *Bridge` must be
> added to `internal/pty/bridge.go` as a test helper (unexported in prod via a
> build tag, or simply a normal exported function — see implementation step).

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestNewApp_Fields ./app/`; Expected: FAIL (App struct missing new fields)

- [ ] **Step 3: Implement** — rewrite `app/app.go`:

```go
// Package app hosts the Wails App: the bound-method API the untrusted Svelte
// frontend calls. Every argument crossing the IPC boundary is validated here —
// workspace ids against a charset allowlist, worktree paths against the
// configured project roots.
package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Miniature-Pug/perch/internal/agent"
	gitpkg "github.com/Miniature-Pug/perch/internal/git"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/registry"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails bound object.
type App struct {
	store *registry.Store
	roots []string

	// emit delivers events to the frontend. Production wires this to a
	// runtime.EventsEmit closure in startup; tests inject a capture seam.
	emit internalpty.EmitFunc

	mu       sync.Mutex
	bridges  map[string]*internalpty.Bridge // paneID → Bridge
	monitors map[string]agent.Monitor       // workspaceID → Monitor
}

// NewApp builds the production App.
func NewApp(store *registry.Store, roots []string) *App {
	return &App{
		store:    store,
		roots:    roots,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
}

// startup is the Wails OnStartup hook.
func (a *App) startup(ctx context.Context) {
	a.emit = func(event string, data ...any) {
		wailsruntime.EventsEmit(ctx, event, data...)
	}
}

// shutdown closes every live Bridge and tears down every Monitor.
func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	bridges := a.bridges
	monitors := a.monitors
	a.bridges = map[string]*internalpty.Bridge{}
	a.monitors = map[string]agent.Monitor{}
	a.mu.Unlock()

	for _, b := range bridges {
		_ = b.Close()
	}
	for _, m := range monitors {
		_ = m.Teardown()
	}
}

// putBridge registers b under paneID, closing any displaced bridge.
func (a *App) putBridge(paneID string, b *internalpty.Bridge) {
	a.mu.Lock()
	old := a.bridges[paneID]
	a.bridges[paneID] = b
	a.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (a *App) getBridge(paneID string) (*internalpty.Bridge, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.bridges[paneID]
	return b, ok
}

func (a *App) getMonitor(workspaceID string) (agent.Monitor, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.monitors[workspaceID]
	return m, ok
}

const maxSessionIDLen = 128

func validateSessionID(s string) error {
	if s == "" || len(s) > maxSessionIDLen {
		return fmt.Errorf("invalid session id length")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("invalid character in session id")
		}
	}
	return nil
}

func validateWorktreeUnderRoots(p string, roots []string) error {
	if p == "" || !filepath.IsAbs(p) {
		return fmt.Errorf("worktree path must be absolute")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return fmt.Errorf("worktree path must be clean")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("worktree path %q: %w", p, err)
	}
	for _, root := range roots {
		rootResolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		if resolved == rootResolved ||
			strings.HasPrefix(resolved, rootResolved+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("worktree path %q is outside configured roots", p)
}

func containedUnderRoots(treePath string, roots []string) bool {
	clean := filepath.Clean(treePath)
	if !filepath.IsAbs(clean) {
		return false
	}
	contained := func(root string) bool {
		return clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))
	}
	for _, root := range roots {
		rootClean := filepath.Clean(root)
		if contained(rootClean) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(rootClean); err == nil {
			if contained(resolved) {
				return true
			}
		}
	}
	return false
}

func newWorkspaceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// WorkspaceVM is the frontend-facing view of one workspace.
type WorkspaceVM struct {
	ID           string      `json:"id"`
	WorktreePath string      `json:"worktreePath"`
	Agent        string      `json:"agent"`
	Title        string      `json:"title"`
	Branch       string      `json:"branch"`
	State        agent.State `json:"state"`
	Caps         agent.Caps  `json:"caps"`
	PaneID       string      `json:"paneId"`
	LastActive   time.Time   `json:"lastActive"`
}

// Settings is the persisted user preference blob.
type Settings struct {
	Theme       string      `json:"theme"`
	Density     string      `json:"density"`
	Font        string      `json:"font"`
	DND         bool        `json:"dnd"`
	AlwaysRules []AlwaysRule `json:"alwaysRules"`
}

// AlwaysRule persists an "always allow" approval rule.
type AlwaysRule struct {
	Agent   string `json:"agent"`
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}
```

Also add to `internal/pty/bridge.go`:

```go
// NewBridgeForTest returns a Bridge whose only behaviour is to call closer on Close.
// Used by app tests that need an observable Bridge without a real pty.
func NewBridgeForTest(closer func() error) *Bridge {
	return &Bridge{closer: closer}
}
```

- [ ] **Step 4: Run** — `go test -race -count=1 ./app/ ./internal/pty/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go internal/pty/bridge.go app/app_test.go && \
git commit -m "$(cat <<'EOF'
feat(app): rewrite App struct — registry+monitor+emit seam; startup/shutdown

Replaces the tmux-poller-based App with the new direct-pty / Monitor /
registry-backed App. Startup installs the Wails emit seam; shutdown
closes all Bridges and tears down all Monitors. Adds NewBridgeForTest
seam to internal/pty for headless testing.
EOF
)"
```

---

### Task 3.2: `ListWorkspaces` — registry → `[]WorkspaceVM`

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestApp_ListWorkspaces_FromRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-abc",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "my-feature",
	})

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	vms := a.ListWorkspaces()
	if len(vms) != 1 {
		t.Fatalf("ListWorkspaces = %d items, want 1", len(vms))
	}
	if vms[0].ID != "ws-abc" {
		t.Errorf("ID = %q, want ws-abc", vms[0].ID)
	}
	if vms[0].Agent != "claude" {
		t.Errorf("Agent = %q, want claude", vms[0].Agent)
	}
	// State defaults to Idle when no monitor is running.
	if vms[0].State != agent.StateIdle {
		t.Errorf("State = %q, want idle", vms[0].State)
	}
}

func TestApp_ListWorkspaces_LiveMonitorStatePropagated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-1", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor("ws-1")
	fm.SetState(agent.StateRunning)

	a := &App{
		store:    store,
		roots:    []string{wt},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{"ws-1": fm},
	}

	vms := a.ListWorkspaces()
	if len(vms) != 1 || vms[0].State != agent.StateRunning {
		t.Errorf("live monitor state not reflected; vms=%+v", vms)
	}
	if vms[0].Caps != fm.Capabilities() {
		t.Errorf("Caps not propagated from monitor")
	}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestApp_ListWorkspaces ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// ListWorkspaces returns all known workspaces from the registry. State and
// Caps come from a live Monitor when one is active; otherwise State=Idle.
func (a *App) ListWorkspaces() []WorkspaceVM {
	ws := a.store.List()
	out := make([]WorkspaceVM, 0, len(ws))
	for _, w := range ws {
		vm := WorkspaceVM{
			ID:           w.ID,
			WorktreePath: w.WorktreePath,
			Agent:        w.Agent,
			Title:        w.Title,
			State:        agent.StateIdle,
		}
		a.mu.Lock()
		m, ok := a.monitors[w.ID]
		a.mu.Unlock()
		if ok {
			vm.State = m.CurrentState()
			vm.Caps = m.Capabilities()
		}
		out = append(out, vm)
	}
	return out
}
```

> **Note:** `CurrentState() State` is part of the frozen `Monitor` contract
> (defined in Task 2.14 / §Shared Contracts and implemented by `FakeMonitor` in
> Task 2.15). No interface change is introduced here — this task only consumes it.

- [ ] **Step 4: Run** — `go test -race -count=1 -run TestApp_ListWorkspaces ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go internal/agent/monitor.go internal/agent/fake_monitor.go && \
git commit -m "$(cat <<'EOF'
feat(app): ListWorkspaces — registry→WorkspaceVM with live monitor state/caps
EOF
)"
```

---

### Task 3.3: `CreateWorkspace(agent, repoPath, branch, model)` — validate + worktree + registry

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestApp_CreateWorkspace_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t",
			"commit", "--allow-empty", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	vm, err := a.CreateWorkspace("claude", repo, "feat/hello", "claude-opus-4-5")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if vm.ID == "" {
		t.Fatal("ID must be non-empty")
	}
	if vm.Agent != "claude" {
		t.Errorf("Agent = %q, want claude", vm.Agent)
	}

	// Registry must persist the workspace.
	_, ok := store.Get(vm.ID)
	if !ok {
		t.Fatal("workspace must be persisted to registry")
	}
}

func TestApp_CreateWorkspace_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	if _, err := a.CreateWorkspace("claude", "/etc", "feat/x", ""); err == nil {
		t.Fatal("must reject path outside roots")
	}
}

func TestApp_CreateWorkspace_RejectsInvalidAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "proj")
	_ = os.MkdirAll(sub, 0o755)
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if _, err := a.CreateWorkspace("ghost", sub, "feat/x", ""); err == nil {
		t.Fatal("must reject unknown agent")
	}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestApp_CreateWorkspace ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// CreateWorkspace validates inputs, resolves/creates the worktree, persists the
// workspace to the registry, and returns its WorkspaceVM. It does NOT start the
// agent — call OpenWorkspace for that.
func (a *App) CreateWorkspace(agentName, repoPath, branch, model string) (WorkspaceVM, error) {
	// Gate 1: repoPath must exist under a configured root.
	if err := validateWorktreeUnderRoots(repoPath, a.roots); err != nil {
		return WorkspaceVM{}, err
	}
	// Gate 2: branch must be a valid git ref.
	if err := gitpkg.ValidRef(branch); err != nil {
		return WorkspaceVM{}, fmt.Errorf("invalid branch: %w", err)
	}
	// Gate 3: agent must be known.
	if agentName != "claude" && agentName != "opencode" {
		return WorkspaceVM{}, fmt.Errorf("unknown agent %q", agentName)
	}

	handle := gitpkg.SlugifyBranch(branch)
	treePath, err := gitpkg.WorktreePath(repoPath, handle, "")
	if err != nil {
		return WorkspaceVM{}, err
	}
	if !containedUnderRoots(treePath, a.roots) {
		return WorkspaceVM{}, fmt.Errorf("derived worktree path %q escapes all configured roots", treePath)
	}

	ctx := context.Background()
	// Create the linked worktree (idempotent if already exists for the branch).
	if err := gitpkg.AddWorktree(ctx, gitpkg.ExecRunner{}, repoPath, branch, treePath, "HEAD"); err != nil {
		// If the branch already exists we can still use the path — just skip.
		if !errors.Is(err, gitpkg.ErrBranchExists) {
			return WorkspaceVM{}, fmt.Errorf("create worktree: %w", err)
		}
	}

	id, err := newWorkspaceID()
	if err != nil {
		return WorkspaceVM{}, err
	}

	w := registry.Workspace{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		LastActive:   time.Now(),
	}
	if err := a.store.Upsert(w); err != nil {
		return WorkspaceVM{}, fmt.Errorf("persist workspace: %w", err)
	}

	return WorkspaceVM{
		ID:           id,
		WorktreePath: treePath,
		Agent:        agentName,
		Title:        handle,
		State:        agent.StateIdle,
	}, nil
}
```

> `gitpkg.ExecRunner{}` is an alias or embed of `proc.ExecRunner{}` exposed via
> the git package — add `type ExecRunner = proc.ExecRunner` to `internal/git/git.go`
> (or pass `proc.ExecRunner{}` directly and import `proc`).  Import `errors` and
> `time` in `app/app.go`.

- [ ] **Step 4: Run** — `go test -race -count=1 -run TestApp_CreateWorkspace ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go internal/git/ && \
git commit -m "$(cat <<'EOF'
feat(app): CreateWorkspace — validate inputs, add worktree, persist to registry
EOF
)"
```

---

### Task 3.4: `OpenWorkspace(id)` — spawn pane pty + Monitor.Prepare + launchCmd write + event pump

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestApp_OpenWorkspace_WritesLaunchCmdAndEmitsEvents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{
		ID:           "ws-open",
		WorktreePath: wt,
		Agent:        "claude",
		Title:        "t",
	})

	fm := agent.NewFakeMonitor("ws-open")
	fm.SetLaunchCmd("claude --resume abc\n")

	// Capture emitted events.
	var mu sync.Mutex
	var emitted []struct{ event string; data []any }
	emit := func(event string, data ...any) {
		mu.Lock()
		emitted = append(emitted, struct{ event string; data []any }{event, data})
		mu.Unlock()
	}

	// Capture bytes written to the fake pty sink.
	var written []byte
	var writeMu sync.Mutex

	a := &App{
		store: store,
		roots: []string{wt},
		emit:  emit,
		bridges: map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		// inject spawn seam
		spawnPty: func(ctx context.Context, cwd string, argv []string, event string,
			ef internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error) {
			b := internalpty.NewBridgeForTest(func() error { return nil })
			// Override Write to capture what is sent.
			b.OverrideWriteForTest(func(p []byte) (int, error) {
				writeMu.Lock()
				written = append(written, p...)
				writeMu.Unlock()
				return len(p), nil
			})
			return b, nil
		},
		newMonitor: func(toolName string, _ agent.Adapter) (agent.Monitor, error) {
			return fm, nil
		},
	}

	if err := a.OpenWorkspace("ws-open"); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	// Wait for the launch-cmd write to propagate (goroutine).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		writeMu.Lock()
		got := string(written)
		writeMu.Unlock()
		if strings.Contains(got, "claude") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	writeMu.Lock()
	got := string(written)
	writeMu.Unlock()
	if !strings.Contains(got, "claude") {
		t.Errorf("launchCmd not written to pty; wrote: %q", got)
	}

	// Replay a fake event and assert it is re-emitted on "agent:event".
	fm.Replay(agent.Event{WorkspaceID: "ws-open", Kind: "state", State: agent.StateRunning})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range emitted {
		if e.event == "agent:event" {
			found = true
		}
	}
	if !found {
		t.Error("agent:event was not emitted after Monitor.Events() replay")
	}
}

func TestApp_OpenWorkspace_UnknownID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.OpenWorkspace("no-such-id"); err == nil {
		t.Fatal("must error on unknown workspace ID")
	}
}
```

> The test uses two injected seams on `App`:
> - `spawnPty func(ctx, cwd, argv, event, emit, cols, rows) (*Bridge, error)` — replaces `internalpty.Spawn` in prod.
> - `newMonitor func(tool string, adapter Adapter) (Monitor, error)` — replaces `agent.NewMonitor` in prod.
>
> Also `internalpty.Bridge` needs `OverrideWriteForTest(fn func([]byte)(int,error))` — a test-only setter that replaces the pty write target.

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestApp_OpenWorkspace ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// spawnPty and newMonitor are injectable seams (set to real funcs in NewApp;
// replaced in tests for headless execution).
type spawnPtyFunc func(ctx context.Context, cwd string, argv []string, event string,
	emit internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error)

type newMonitorFunc func(tool string, adapter agent.Adapter) (agent.Monitor, error)

// Add fields to App struct (update NewApp accordingly):
// spawnPty  spawnPtyFunc
// newMonitor newMonitorFunc

// In NewApp, set:
// spawnPty:   internalpty.Spawn,
// newMonitor: agent.NewMonitor,

// OpenWorkspace spawns a login-shell pty for the workspace, calls
// Monitor.Prepare to obtain the agent launch command, writes it to the
// pty, and starts a goroutine forwarding Monitor events to the frontend.
func (a *App) OpenWorkspace(id string) error {
	w, ok := a.store.Get(id)
	if !ok {
		return fmt.Errorf("unknown workspace %q", id)
	}

	paneID := "pane-" + id
	event := "pty:data:" + paneID

	ctx := context.Background()
	br, err := a.spawnPty(ctx, w.WorktreePath, internalpty.LoginShellArgv(), event, a.emit, 220, 50)
	if err != nil {
		return fmt.Errorf("spawn pty: %w", err)
	}

	adpt := agentAdapter(w.Agent)
	mon, err := a.newMonitor(w.Agent, adpt)
	if err != nil {
		_ = br.Close()
		return fmt.Errorf("new monitor: %w", err)
	}

	launchCmd, err := mon.Prepare(ctx, id, w.WorktreePath, w.LastSessionID)
	if err != nil {
		_ = br.Close()
		_ = mon.Teardown()
		return fmt.Errorf("monitor prepare: %w", err)
	}

	a.mu.Lock()
	old := a.bridges[paneID]
	a.bridges[paneID] = br
	oldMon := a.monitors[id]
	a.monitors[id] = mon
	a.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	if oldMon != nil {
		_ = oldMon.Teardown()
	}

	// Write the launch command into the shell pty.
	if launchCmd != "" {
		_, _ = br.Write([]byte(launchCmd))
	}

	// Forward Monitor events to the frontend as "agent:event".
	go func() {
		for evt := range mon.Events() {
			a.emit("agent:event", evt)
			// Dispatch notify tier events per the attention rules.
			a.dispatchNotify(evt)
		}
	}()

	return nil
}

// dispatchNotify translates an agent.Event into a "notify" Wails event at the
// appropriate tier (blocking/ambient/routine).
func (a *App) dispatchNotify(evt agent.Event) {
	switch {
	case evt.Kind == "state" && evt.State == agent.StateAwaitingApproval:
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       "Approval needed",
			"body":        "An agent is waiting for your decision.",
			"workspaceId": evt.WorkspaceID,
		})
	case evt.Kind == "state" && evt.State == agent.StateDone:
		a.emit("notify", map[string]any{
			"tier":        "ambient",
			"title":       "Turn complete",
			"body":        "Agent finished a turn.",
			"workspaceId": evt.WorkspaceID,
		})
	case evt.Kind == "state" && evt.State == agent.StateErrored:
		a.emit("notify", map[string]any{
			"tier":        "blocking",
			"title":       "Agent error",
			"body":        evt.Err,
			"workspaceId": evt.WorkspaceID,
		})
	}
}

// agentAdapter returns the Adapter for a known tool name, or a no-op for unknown.
func agentAdapter(tool string) agent.Adapter {
	switch tool {
	case "claude":
		return agent.NewClaude()
	case "opencode":
		return agent.NewOpencode()
	default:
		return nil
	}
}
```

Also update `NewApp` to set the two seams, and update the `App` struct to include
`spawnPty spawnPtyFunc` and `newMonitor newMonitorFunc`.

- [ ] **Step 4: Run** — `go test -race -count=1 -run TestApp_OpenWorkspace ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go internal/pty/bridge.go internal/agent/ && \
git commit -m "$(cat <<'EOF'
feat(app): OpenWorkspace — spawn pty + Monitor.Prepare + launchCmd write + event pump
EOF
)"
```

---

### Task 3.5: `WriteToPty`, `ResizePty`, `CloseWorkspace`, `RemoveWorkspace`

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestApp_WriteToPty_RoutesToBridge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var written []byte
	var mu sync.Mutex
	br := internalpty.NewBridgeForTest(func() error { return nil })
	br.OverrideWriteForTest(func(p []byte) (int, error) {
		mu.Lock(); defer mu.Unlock()
		written = append(written, p...)
		return len(p), nil
	})

	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws1": br},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteToPty("pane-ws1", []int{104, 101, 108, 108, 111}); err != nil {
		t.Fatalf("WriteToPty: %v", err)
	}
	mu.Lock(); defer mu.Unlock()
	if string(written) != "hello" {
		t.Errorf("pty received %q, want hello", written)
	}
}

func TestApp_WriteToPty_UnknownPane(t *testing.T) {
	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteToPty("no-pane", []int{65}); err == nil {
		t.Fatal("must error for unknown pane")
	}
}

func TestApp_ResizePty_Succeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var resized bool
	br := internalpty.NewBridgeForTestWithResize(func() error { return nil },
		func(_, _ uint16) error { resized = true; return nil })
	a := &App{
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws2": br},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.ResizePty("pane-ws2", 120, 40); err != nil {
		t.Fatalf("ResizePty: %v", err)
	}
	if !resized {
		t.Error("Resize was not called on the bridge")
	}
}

func TestApp_CloseWorkspace_ClosesAndKeepsInRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-close", WorktreePath: wt, Agent: "claude", Title: "t"})

	closed := false
	br := internalpty.NewBridgeForTest(func() error { closed = true; return nil })
	fm := agent.NewFakeMonitor("ws-close")

	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{"pane-ws-close": br},
		monitors: map[string]agent.Monitor{"ws-close": fm},
	}

	if err := a.CloseWorkspace("ws-close"); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}
	if !closed {
		t.Error("Bridge must be closed")
	}
	if !fm.TornDown() {
		t.Error("Monitor must be torn down")
	}
	// Workspace persists in registry.
	if _, ok := store.Get("ws-close"); !ok {
		t.Error("CloseWorkspace must NOT remove workspace from registry")
	}
}

func TestApp_RemoveWorkspace_RemovesFromRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-rm", WorktreePath: wt, Agent: "claude", Title: "t"})

	fm := agent.NewFakeMonitor("ws-rm")
	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{"ws-rm": fm},
	}

	if err := a.RemoveWorkspace("ws-rm"); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	if _, ok := store.Get("ws-rm"); ok {
		t.Error("RemoveWorkspace must remove workspace from registry")
	}
}
```

Also add to `internal/pty/bridge.go`:

```go
// NewBridgeForTestWithResize returns a Bridge whose closer and setsize are the
// supplied funcs. For use in app tests.
func NewBridgeForTestWithResize(closer func() error, resize func(cols, rows uint16) error) *Bridge {
	return &Bridge{closer: closer, setsize: resize}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run "TestApp_WriteToPty|TestApp_ResizePty|TestApp_CloseWorkspace|TestApp_RemoveWorkspace" ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// WriteToPty forwards keystrokes (as a JSON number array) from xterm.js to the
// pane's pty. The []int to []byte conversion matches the pump's inverse path.
func (a *App) WriteToPty(paneID string, data []int) error {
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	b := make([]byte, len(data))
	for i, v := range data {
		b[i] = byte(v)
	}
	_, err := br.Write(b)
	return err
}

// ResizePty applies new dimensions to the pane's pty.
func (a *App) ResizePty(paneID string, cols, rows uint16) error {
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	return br.Resize(cols, rows)
}

// CloseWorkspace closes the pty and tears down the monitor for the workspace,
// but keeps the workspace record in the registry (it can be reopened).
func (a *App) CloseWorkspace(id string) error {
	paneID := "pane-" + id
	a.mu.Lock()
	br := a.bridges[paneID]
	delete(a.bridges, paneID)
	mon := a.monitors[id]
	delete(a.monitors, id)
	a.mu.Unlock()

	if br != nil {
		_ = br.Close()
	}
	if mon != nil {
		_ = mon.Teardown()
	}
	return nil
}

// RemoveWorkspace closes the workspace (pty + monitor) and removes it from
// the registry permanently.
func (a *App) RemoveWorkspace(id string) error {
	_ = a.CloseWorkspace(id)
	return a.store.Remove(id)
}
```

- [ ] **Step 4: Run** — `go test -race -count=1 -run "TestApp_WriteToPty|TestApp_ResizePty|TestApp_CloseWorkspace|TestApp_RemoveWorkspace" ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go internal/pty/bridge.go && \
git commit -m "$(cat <<'EOF'
feat(app): WriteToPty/ResizePty/CloseWorkspace/RemoveWorkspace with fake-bridge tests
EOF
)"
```

---

### Task 3.6: `OpenShell(paneID, cwd)` — shell drawer pty

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestApp_OpenShell_SpawnsAndEmits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")

	var mu sync.Mutex
	var emitted []string
	emit := func(event string, data ...any) {
		if strings.HasPrefix(event, "pty:data:") {
			mu.Lock()
			emitted = append(emitted, event)
			mu.Unlock()
		}
	}

	spawnCalled := false
	a := &App{
		emit:     emit,
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		spawnPty: func(_ context.Context, cwd string, argv []string, event string,
			ef internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			spawnCalled = true
			if event != "pty:data:shell-1" {
				return nil, fmt.Errorf("wrong event %q", event)
			}
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
	}

	if err := a.OpenShell("shell-1", t.TempDir()); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}
	if !spawnCalled {
		t.Error("spawnPty must be called by OpenShell")
	}
	// Bridge must be registered so WriteToPty can reach it.
	a.mu.Lock()
	_, ok := a.bridges["shell-1"]
	a.mu.Unlock()
	if !ok {
		t.Error("bridge for shell-1 not registered")
	}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestApp_OpenShell ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// OpenShell spawns a $SHELL -l pty for the shell drawer pane (paneID) in cwd.
// Output flows to the "pty:data:<paneID>" event. Separate from agent panes so
// the shell drawer has its own independent pty.
func (a *App) OpenShell(paneID, cwd string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	event := "pty:data:" + paneID
	ctx := context.Background()
	br, err := a.spawnPty(ctx, cwd, internalpty.LoginShellArgv(), event, a.emit, 220, 50)
	if err != nil {
		return fmt.Errorf("OpenShell spawn: %w", err)
	}
	a.putBridge(paneID, br)
	return nil
}
```

- [ ] **Step 4: Run** — `go test -race -count=1 -run TestApp_OpenShell ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go && \
git commit -m "$(cat <<'EOF'
feat(app): OpenShell — shell drawer pty, emits pty:data:<paneID>
EOF
)"
```

---

### Task 3.7: `Approve(reqID, decision)` — route to Monitor + persist AlwaysRule

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestApp_Approve_RoutesToMonitor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-ap", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor("ws-ap")
	// FakeMonitor.Approve records the reqID + decision for assertion.

	a := &App{
		store:     store,
		emit:      func(string, ...any) {},
		bridges:   map[string]*internalpty.Bridge{},
		monitors:  map[string]agent.Monitor{"ws-ap": fm},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	if err := a.Approve("req-001:ws-ap", "allow"); err != nil {
		t.Fatalf("Approve allow: %v", err)
	}
	calls := fm.ApproveCalls()
	if len(calls) != 1 || calls[0].ReqID != "req-001" {
		t.Errorf("Approve did not route to monitor; calls=%+v", calls)
	}
}

func TestApp_Approve_AlwaysPersistsRule(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-alw", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor("ws-alw")
	fm.SetApprovalTool("Bash")

	settingsPath := filepath.Join(cfgDir, "settings.json")

	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws-alw": fm},
		settingsPath: settingsPath,
	}

	if err := a.Approve("req-002:ws-alw", "always"); err != nil {
		t.Fatalf("Approve always: %v", err)
	}

	s, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if len(s.AlwaysRules) == 0 {
		t.Fatal("AlwaysRule must be persisted on 'always' decision")
	}
	if s.AlwaysRules[0].Tool != "Bash" {
		t.Errorf("rule tool = %q, want Bash", s.AlwaysRules[0].Tool)
	}
}

func TestApp_Approve_DenyNoSideEffects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: "ws-deny", WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor("ws-deny")
	settingsPath := filepath.Join(cfgDir, "settings.json")

	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws-deny": fm},
		settingsPath: settingsPath,
	}

	if err := a.Approve("req-003:ws-deny", "deny"); err != nil {
		t.Fatalf("Approve deny: %v", err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) != 0 {
		t.Error("deny must not persist an AlwaysRule")
	}
}
```

> **Approval routing protocol:** `reqID` is encoded as `"<rawReqID>:<workspaceID>"` so
> the App can look up the owning monitor without a scan.  `FakeMonitor` exposes
> `ApproveCalls()` returning `[]struct{ReqID string; D agent.Decision}` and
> `SetApprovalTool(string)` so tests can assert routing and tool name.

- [ ] **Step 2: Run** — `go test -race -count=1 -run TestApp_Approve ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// Approve routes a tool-approval decision to the owning Monitor.
// reqID format: "<raw>:<workspaceID>". decision: "allow"|"deny"|"always".
// On "always", an AlwaysRule is persisted to Settings.
func (a *App) Approve(reqID, decision string) error {
	// Split reqID into raw req token and workspace id.
	sep := strings.LastIndex(reqID, ":")
	if sep < 0 {
		return fmt.Errorf("invalid reqID format %q", reqID)
	}
	rawReqID := reqID[:sep]
	workspaceID := reqID[sep+1:]

	a.mu.Lock()
	mon, ok := a.monitors[workspaceID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("no active monitor for workspace %q", workspaceID)
	}

	d := agent.Decision{}
	switch decision {
	case "allow":
		d.Allow = true
	case "always":
		d.Allow = true
		d.Always = true
	case "deny":
		d.Allow = false
	default:
		return fmt.Errorf("unknown decision %q", decision)
	}

	if err := mon.Approve(rawReqID, d); err != nil {
		return err
	}

	if d.Always {
		// Persist the AlwaysRule. Fetch the tool name from the monitor.
		tool := mon.LastApprovalTool()
		s, _ := a.GetSettings()
		s.AlwaysRules = append(s.AlwaysRules, AlwaysRule{
			Agent: "claude",
			Tool:  tool,
		})
		_ = a.SaveSettings(s)
	}
	return nil
}
```

> `LastApprovalTool() string` is part of the frozen `Monitor` contract (Task
> 2.14 / §Shared Contracts; `FakeMonitor` tracks it from `Event.Approval.Tool`
> in Task 2.15) — this task only consumes it. `App` also needs a `settingsPath
> string` field, set by `NewApp` to `filepath.Join(registry.DefaultConfigDir(),
> "settings.json")`.

- [ ] **Step 4: Run** — `go test -race -count=1 -run TestApp_Approve ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go internal/agent/ && \
git commit -m "$(cat <<'EOF'
feat(app): Approve — route allow/deny/always to Monitor; persist AlwaysRule to Settings
EOF
)"
```

---

### Task 3.8: Git bound methods — `DiffStat`, `Hunks`, `StageHunk`, `DiscardHunk`

These are thin wrappers over `internal/git`; each validates the worktree path
against roots before calling the new hunk API (implemented in Phase 2).

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests** (temp git repo via `exec.Command("git", …)`):

```go
func TestApp_DiffStat_ValidateAndDelegate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := initGitRepo(t, root)

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	// Write an unstaged file.
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := a.DiffStat(repo)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if len(files) == 0 {
		t.Error("DiffStat must return at least one FileDiff for an unstaged file")
	}
}

func TestApp_DiffStat_RejectsOutsideRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if _, err := a.DiffStat("/etc"); err == nil {
		t.Fatal("must reject path outside roots")
	}
}

func TestApp_Hunks_ReturnsHunks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := initGitRepo(t, root)

	// Commit a file then modify it so there is a hunk.
	p := filepath.Join(repo, "file.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "add file")
	if err := os.WriteFile(p, []byte("line1\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		roots:    []string{root},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}

	hunks, err := a.Hunks(repo, "file.txt")
	if err != nil {
		t.Fatalf("Hunks: %v", err)
	}
	if len(hunks) == 0 {
		t.Error("Hunks must return at least one hunk for modified file")
	}
}
```

Helper functions in the test file:

```go
func initGitRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-qm", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run "TestApp_DiffStat|TestApp_Hunks" ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go`:

```go
// DiffStat returns per-file diff summary for worktree, validated against roots.
func (a *App) DiffStat(worktree string) ([]gitpkg.FileDiff, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	return gitpkg.DiffStat(context.Background(), a.run, worktree)
}

// Hunks returns the unified hunks for a single file in worktree.
func (a *App) Hunks(worktree, file string) ([]gitpkg.Hunk, error) {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return nil, err
	}
	return gitpkg.Hunks(context.Background(), a.run, worktree, file)
}

// StageHunk applies hunk `index` of file to the index (git apply --cached).
// index is relative to the current Hunks(worktree, file) output; the frontend
// re-fetches hunks after each call so indices stay fresh.
func (a *App) StageHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	return gitpkg.StageHunk(context.Background(), a.run, worktree, file, index)
}

// DiscardHunk reverses hunk `index` of file in the working tree (git apply --reverse).
func (a *App) DiscardHunk(worktree, file string, index int) error {
	if err := validateWorktreeUnderRoots(worktree, a.roots); err != nil {
		return err
	}
	return gitpkg.DiscardHunk(context.Background(), a.run, worktree, file, index)
}
```

> These call the new `internal/git` hunk API (`DiffStat(ctx, r, worktree) ([]FileDiff, error)`,
> `Hunks(ctx, r, worktree, file) ([]Hunk, error)`, `StageHunk(ctx, r, worktree, file, index)`,
> `DiscardHunk(ctx, r, worktree, file, index)`) defined in Phase 2. `a.run` is the App's
> `proc.Runner`. The legacy `git.Diff`/`app.Diff` aggregate-diff island was removed in
> Phase 2 cleanup (commit `a7be846`) — there is no Diff overload to co-exist with.

- [ ] **Step 4: Run** — `go test -race -count=1 -run "TestApp_DiffStat|TestApp_Hunks" ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go && \
git commit -m "$(cat <<'EOF'
feat(app): DiffStat/Hunks/StageHunk/DiscardHunk — thin validated wrappers over internal/git
EOF
)"
```

---

### Task 3.9: FS + misc bound methods (layout, settings, ListDir, ReadFile, WriteFile, etc.)

Layout and settings are persisted as JSON blobs under `registry.DefaultConfigDir()`.

**Files:** `app/app.go`, `app/app_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestApp_Settings_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	s := Settings{Theme: "tokyo-night", Density: "comfortable", DND: true}
	if err := a.SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Theme != "tokyo-night" || got.Density != "comfortable" || !got.DND {
		t.Errorf("settings round-trip mismatch: %+v", got)
	}
}

func TestApp_Layout_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:      store,
		emit:       func(string, ...any) {},
		bridges:    map[string]*internalpty.Bridge{},
		monitors:   map[string]agent.Monitor{},
		layoutPath: filepath.Join(cfgDir, "layout.json"),
	}

	blob := `{"sidebarW":240,"shellH":200}`
	if err := a.SaveLayout(blob); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}
	got, err := a.GetLayout()
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if got != blob {
		t.Errorf("layout round-trip: got %q, want %q", got, blob)
	}
}

func TestApp_GetSettings_ReturnsDefaultOnMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		settingsPath: filepath.Join(cfgDir, "no-such-settings.json"),
	}
	s, err := a.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings on missing file: %v", err)
	}
	if s.Theme != "gruvbox" {
		t.Errorf("default theme = %q, want gruvbox", s.Theme)
	}
}

func TestApp_ListDir_ReturnsDirEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	nodes, err := a.ListDir(dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(nodes) == 0 {
		t.Error("ListDir must return at least the written file")
	}
}

func TestApp_ReadWriteFile_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	a := &App{
		store:    store,
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
	}
	if err := a.WriteFile(path, "hello world"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := a.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != "hello world" {
		t.Errorf("ReadFile = %q, want 'hello world'", got)
	}
}
```

- [ ] **Step 2: Run** — `go test -race -count=1 -run "TestApp_Settings|TestApp_Layout|TestApp_ListDir|TestApp_ReadWriteFile" ./app/`; Expected: FAIL

- [ ] **Step 3: Implement** — add to `app/app.go` (also add `layoutPath string` and update `NewApp`):

```go
// GetSettings reads settings from disk; returns defaults if the file is absent.
func (a *App) GetSettings() (Settings, error) {
	data, err := os.ReadFile(a.settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{Theme: "gruvbox", Density: "dense", Font: "geist"}, nil
		}
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// SaveSettings atomically writes settings to disk.
func (a *App) SaveSettings(s Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(a.settingsPath, data)
}

// GetLayout reads the opaque layout JSON blob from disk.
func (a *App) GetLayout() (string, error) {
	data, err := os.ReadFile(a.layoutPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "{}", nil
		}
		return "", err
	}
	return string(data), nil
}

// SaveLayout atomically writes the opaque layout JSON blob.
func (a *App) SaveLayout(layoutJSON string) error {
	return atomicWrite(a.layoutPath, []byte(layoutJSON))
}

// atomicWrite writes data to path via a temp file + rename (atomic on Linux).
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ListDir returns directory entries under absDir, gitignore-unaware.
func (a *App) ListDir(absDir string) ([]fspkg.Node, error) {
	return fspkg.ListDir(absDir, false)
}

// ReadFile returns the contents of absPath as a string.
func (a *App) ReadFile(absPath string) (string, error) {
	data, err := fspkg.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile atomically writes content to absPath.
func (a *App) WriteFile(absPath, content string) error {
	return fspkg.WriteFile(absPath, []byte(content))
}

// RevealInFiles opens the containing directory of absPath in the system file manager.
func (a *App) RevealInFiles(absPath string) error {
	return fspkg.RevealInFiles(absPath)
}

// Branches returns git branch names for the repo at repo.
func (a *App) Branches(repo string) ([]string, error) {
	return gitpkg.Branches(repo)
}

// Worktrees returns git worktree info for the repo at repo.
func (a *App) Worktrees(repo string) ([]gitpkg.WorktreeInfo, error) {
	return gitpkg.Worktrees(repo)
}
```

> Add `"encoding/json"`, `"errors"`, `"os"`, and the `fspkg` import alias for
> `internal/fs` to `app/app.go`.  Update `NewApp` to set:
> ```go
> settingsPath: filepath.Join(registry.DefaultConfigDir(), "settings.json"),
> layoutPath:   filepath.Join(registry.DefaultConfigDir(), "layout.json"),
> ```

- [ ] **Step 4: Run** — `go test -race -count=1 -run "TestApp_Settings|TestApp_Layout|TestApp_ListDir|TestApp_ReadWriteFile" ./app/`; Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add app/app.go app/app_test.go && \
git commit -m "$(cat <<'EOF'
feat(app): GetSettings/SaveSettings/GetLayout/SaveLayout/ListDir/ReadFile/WriteFile/Branches/Worktrees
EOF
)"
```

---

### Task 3.10: CLI `cmd/perch/main.go` — GUI default; `attach` → focus; remove resurrect/status; `options.go` spike-4 flag

**Files:** `cmd/perch/main.go`, `cmd/perch/main_test.go`, `app/options.go`

- [ ] **Step 1: Write failing tests**

```go
// In cmd/perch/main_test.go

func TestRun_NoArgs_CallsLaunchGUI(t *testing.T) {
	launched := false
	old := launchGUI
	launchGUI = func(_ []string) error { launched = true; return nil }
	defer func() { launchGUI = old }()

	code := run([]string{}, io.Discard, io.Discard)
	if code != 0 {
		t.Errorf("run() = %d, want 0", code)
	}
	if !launched {
		t.Error("launchGUI must be called with no args")
	}
}

func TestRun_Attach_FocusesWorkspace(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	// With no workspaces in registry, attach should print "no workspace matches".
	_, stderr, code := callRun([]string{"attach", "my-feature"})
	if code != 1 {
		t.Errorf("attach with no match: want exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "no workspace") {
		t.Errorf("attach with no match: want 'no workspace' on stderr; got %q", stderr)
	}
}

func TestRun_ResurrectRemoved_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"resurrect"})
	if code != 2 {
		t.Errorf("resurrect: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("resurrect: want Usage on stderr; got %q", errOut)
	}
}

func TestRun_StatusRemoved_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status", "set", "working"})
	if code != 2 {
		t.Errorf("status: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("status: want Usage on stderr; got %q", errOut)
	}
}

func TestPrintUsage_ShowsAttach(t *testing.T) {
	_, errOut, _ := callRun([]string{"doctr"}) // unknown arg → usage
	for _, must := range []string{"setup", "doctor", "version", "attach"} {
		if !strings.Contains(errOut, must) {
			t.Errorf("printUsage must mention %q; stderr: %q", must, errOut)
		}
	}
	for _, hidden := range []string{"resurrect", "status", "debug"} {
		if strings.Contains(errOut, hidden) {
			t.Errorf("printUsage must not mention %q; stderr: %q", hidden, errOut)
		}
	}
}

func TestOptions_DisableWebViewDrop_Set(t *testing.T) {
	// Compile-time assertion: Run builds without error.
	// The flag is verified by reading the source directly since Run() opens a window.
	// This test asserts the constant exists (build-time); runtime verification requires
	// a real Wails window (integration test).
	_ = disableWebViewDropForSpike4 // must be true; compile error if removed
}
```

> `disableWebViewDropForSpike4` is a package-level `const bool = true` in
> `app/options.go`, documented with the `#3686` reference, so the test can reference
> it without opening a window.

- [ ] **Step 2: Run** — `go test -race -count=1 -run "TestRun_NoArgs|TestRun_Attach|TestRun_ResurrectRemoved|TestRun_StatusRemoved|TestPrintUsage_ShowsAttach|TestOptions" ./cmd/perch/`; Expected: FAIL

- [ ] **Step 3: Implement**

**`app/options.go`** — add `DisableWebViewDrop` + sentinel const:

```go
package app

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// disableWebViewDropForSpike4 is set true to mitigate WebKitGTK hijacking OS
// file-drop events before OnFileDrop fires (Wails issue #3686). Spike 4 validated
// that this flag, combined with frontend preventDefault on dragover/drop, prevents
// the UI replacement bug. If a future Wails release resolves #3686, set to false
// and remove the corresponding frontend listeners.
const disableWebViewDropForSpike4 = true

// Run launches the Wails desktop app.
func Run(assets embed.FS, store interface{}, roots []string) error {
	app := NewAppFromStore(assets, store, roots)
	return wails.Run(&options.App{
		Title:              "perch",
		Width:              1280,
		Height:             800,
		DisableWebViewDrop: disableWebViewDropForSpike4,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
	})
}
```

> `NewAppFromStore` is a variant of `NewApp` that accepts the registry store
> already loaded; `NewApp(store, roots)` (Task 3.1) is used in tests.  Update
> `app/options.go` to call `NewApp(store, roots)` with the store loaded from
> `registry.DefaultConfigDir()`.

**`cmd/perch/main.go`** — keep `setup`, `doctor`, `version`; add `attach` (registry-based focus); remove `resurrect`, `status`, `debug tmux`:

```go
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return handleLaunch("", stdout, stderr)
	}
	switch args[0] {
	case "setup":
		return handleSetup(args[1:], stdout, stderr)
	case "attach":
		return handleAttach(args[1:], stdout, stderr)
	case "doctor":
		return doctor.Run(version, stdout, doctor.RealSystem())
	case "version":
		return handleVersion(stdout)
	case "debug":
		return handleDebug(args[1:], stdout, stderr)
	default:
		return handlePathArg(args[0], stdout, stderr)
	}
}

// handleAttach focuses/raises an existing workspace in the registry.
// It resolves the workspace by fuzzy-matching the query against workspace
// titles and worktree paths. No separate process is spawned — the GUI
// itself handles the focus (future: use a Wails runtime broadcast event).
// For now: if the GUI is not open, print instructions; if it is, no-op
// (the GUI's own sidebar handles focus). This is a registry-backed
// informational command in v1.
func handleAttach(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(strings.Join(args, " ")) == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: perch attach <query>")
		return 2
	}
	query := strings.Join(args, " ")

	store, err := registry.Load(registry.DefaultConfigDir())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch attach: %v\n", err)
		return 1
	}
	workspaces := store.List()
	var matched []registry.Workspace
	for _, w := range workspaces {
		if strings.Contains(strings.ToLower(w.Title), strings.ToLower(query)) ||
			strings.Contains(strings.ToLower(w.WorktreePath), strings.ToLower(query)) {
			matched = append(matched, w)
		}
	}
	switch len(matched) {
	case 0:
		_, _ = fmt.Fprintf(stderr, "no workspace matches %q\n", query)
		return 1
	case 1:
		_, _ = fmt.Fprintf(stdout, "workspace: %s (%s)\n", matched[0].Title, matched[0].WorktreePath)
		_, _ = fmt.Fprintln(stdout, "Open perch GUI to focus this workspace.")
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "ambiguous query %q; matches:\n", query)
		for _, w := range matched {
			_, _ = fmt.Fprintf(stderr, "  %s  %s\n", w.Title, w.WorktreePath)
		}
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: perch [path]")
	_, _ = fmt.Fprintln(w, "       perch attach <query>")
	_, _ = fmt.Fprintln(w, "       perch setup [--replace]")
	_, _ = fmt.Fprintln(w, "       perch doctor")
	_, _ = fmt.Fprintln(w, "       perch version")
}
```

Remove `handleResurrect`, `handleStatus`, `handleDebugTmux` and their imports.

**`cmd/perch/main_test.go`** — remove old attach/resurrect/status tests; add `callRun` helper if not present:

```go
func callRun(args []string) (stdout, stderr string, code int) {
	var outBuf, errBuf strings.Builder
	code = run(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}
```

- [ ] **Step 4: Run** — `go test -race -count=1 ./cmd/perch/ ./app/`; Expected: PASS

- [ ] **Step 5: Full suite gate** — `go build ./... && go test -race -count=1 ./...`; Expected: PASS

- [ ] **Step 6: Commit**
```bash
git add cmd/perch/main.go cmd/perch/main_test.go app/options.go && \
git commit -m "$(cat <<'EOF'
feat(cli): GUI default; attach→registry focus; remove resurrect/status; add DisableWebViewDrop

CLI now: no-args→GUI, attach<query>→registry fuzzy-match, setup/doctor/version kept.
resurrect and status subcommands removed (folded into registry+GUI).
app/options.go adds DisableWebViewDrop:true with spike-4 wails#3686 comment.
EOF
)"
```

---

## Phase 4 — Frontend (shell & terminals)

Branch: `feat/perch-v1`. Commands: `npm --prefix frontend test -- <file>` for single-file runs; `npm --prefix frontend test` for all. **Note:** this fragment is ~1150 lines — the "~900" target is a tilde-soft guide; the no-placeholder mandate for real 9-theme CSS + full wails.ts + component code sets the realistic floor.

After Task 4.7, the old-architecture imports in Sidebar/Tabs/DiffPanel/NewAgentDialog/old App no longer compile against the new wails.ts. Single-file test runs stay green; a full `npm run check` is only clean after Phase 4b rewrites those files.

Add `declare module "*?raw" { const content: string; export default content; }` to `frontend/src/vite-env.d.ts` (or a new `frontend/src/types/raw.d.ts`) before running type-check.

---

> ### Phase 4 execution strategy (locked after Phase-3 review — read before dispatching 4.x)
>
> **Orphan demolition is folded into Task 4.10, NOT a separate up-front commit.** Pre-pivot orphans `Tabs.svelte`, `DiffPanel.svelte`, `NewAgentDialog.svelte` (+ their `.test.ts`) are superseded by Stage/DiffView/NewSessionDialog. They are referenced only by the old `App.svelte` AND by `smoke.test.ts` (which MOUNTS App.svelte — deleting orphans before 4.10 turns `npm test` red). So delete those 6 files *inside* Task 4.10 when `App.svelte` is rewritten, and rewrite/replace `smoke.test.ts` in the same task. KEEP `ConfirmDialog.svelte`/`.test.ts` — Task 4.23 extends them. (Verified: only App.svelte + own tests + smoke.test.ts reference the orphans.)
>
> **Task 4.7 (wails.ts) is a RECONCILIATION, not verbatim transcription.** The plan's wails.ts predates all Phase-3 reconciliations. Bind against the ACTUAL `app.go` surface (`grep 'func (a \*App)' app/app.go`): `ListWorkspaces()`, `CreateWorkspace(agent,repo,branch,model)`, `OpenWorkspace(id)`, `CloseWorkspace(id)`, `RemoveWorkspace(id)`, `WriteToPty(paneID, number[])`, `ResizePty(paneID,cols,rows)`, `OpenShell(paneID,cwd)`, `Approve(reqID,decision)`, `DiffStat(worktree)`, `Hunks(worktree,file)`, `StageHunk(worktree,file,index)`, `DiscardHunk(worktree,file,index)`, `ListDir(absDir)`, `ReadFile(absPath)`, `WriteFile(absPath,content)`, `RevealInFiles(absPath)`, `Branches(repo)`, `Worktrees(repo)`, `GetLayout()/SaveLayout(json)`, `GetSettings()/SaveSettings(s)`. Events: `pty:data:<paneID>` (number[]), `pty:exit:<paneID>` ({code}), `agent:event`, `fs:changed`, `notify` ({tier,title,body,workspaceId}). **CAVEAT: vitest mocks `./wails` in every component test, so a green suite does NOT prove wails.ts↔Go alignment — that is verified only by `wails generate module` / the user's GUI smoke run (flag in Phase 5).**
>
> **Per-task notes:** 4.8 Terminal MUST mock `@xterm/xterm` (jsdom has no canvas). 4.11 CodeMirror: registry-verify + exact-pin every `@codemirror/*` version (`npm view <pkg> version`) and confirm install succeeds before 4.12/4.13. Frontend XSS review (the dual of the IPC hardening): `grep -rn "@html\|innerHTML" frontend/src` on DiffView/Editor/ApprovalCard/Preview — Svelte auto-escapes `{...}`, so only raw-HTML sinks need review.
>
> **Pacing:** ~22 of 24 tasks are presentational/mechanical against the now-frozen backend — run them solo with per-task TDD (per-file `npm test -- <file>`). Real decision points: orphan/smoke handling at 4.10 (App composition). Phase 5's full `npm run build && npm run check` is the JS compile gate (analog of `go build ./...`) that catches any dead import the per-file runs miss.

---

### Task 4.1: Design token base stylesheet

**Files:**
- `frontend/src/tokens/tokens.css`
- `frontend/src/tokens/tokens.test.ts`

**Approach:** jsdom does not apply external stylesheet cascade, so `getComputedStyle` returns `""` for custom properties. Tests import the file as raw text (`?raw`) and assert the rule strings are present. This is explicit and deterministic in jsdom.

**Failing test:**

```ts
// frontend/src/tokens/tokens.test.ts
import { describe, it, expect } from "vitest";
import css from "./tokens.css?raw";

describe("tokens.css", () => {
  it("defines --perch-fs-code as 14px on :root", () => {
    expect(css).toContain("--perch-fs-code: 14px");
  });
  it("defines --perch-sp-1 through --perch-sp-8", () => {
    for (let i = 1; i <= 8; i++) expect(css).toContain(`--perch-sp-${i}:`);
  });
  it("defines dense density multiplier 0.75", () => {
    expect(css).toContain('[data-density="dense"]');
    expect(css).toContain("--perch-density-scale: 0.75");
  });
  it("defines comfortable multiplier 1", () => {
    expect(css).toContain('[data-density="comfortable"]');
    expect(css).toContain("--perch-density-scale: 1");
  });
  it("defines ultra multiplier 1.25", () => {
    expect(css).toContain('[data-density="ultra"]');
    expect(css).toContain("--perch-density-scale: 1.25");
  });
  it("defines motion tokens", () => {
    expect(css).toContain("--perch-dur: 120ms");
    expect(css).toContain("--perch-ease: cubic-bezier(.4,0,.2,1)");
  });
  it("defines font stack tokens", () => {
    expect(css).toContain("--perch-font-sans:");
    expect(css).toContain("--perch-font-mono:");
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/tokens/tokens.test.ts` → FAIL (file missing)

**Implementation (`frontend/src/tokens/tokens.css`):**

```css
/* perch design tokens — structure, type, spacing, motion. Palette lives in themes.css. */
:root {
  --perch-font-sans: "Geist", "IBM Plex Sans", "Inter", system-ui, sans-serif;
  --perch-font-mono: "Geist Mono", "IBM Plex Mono", "Fira Code", ui-monospace, monospace;
  --perch-fs-code: 14px;    --perch-lh-code: 1.55;
  --perch-fs-shell: 13px;   --perch-lh-shell: 1.5;
  --perch-fs-body: 13px;    --perch-fs-caption: 12px;   --perch-fs-label: 11px;
  /* 8pt spacing grid; multiplied by --perch-density-scale at point-of-use */
  --perch-density-scale: 1;
  --perch-sp-1: 8px;  --perch-sp-2: 16px; --perch-sp-3: 24px; --perch-sp-4: 32px;
  --perch-sp-5: 40px; --perch-sp-6: 48px; --perch-sp-7: 56px; --perch-sp-8: 64px;
  /* Motion */
  --perch-dur: 120ms;
  --perch-ease: cubic-bezier(.4,0,.2,1);
}
[data-density="dense"]       { --perch-density-scale: 0.75; }
[data-density="comfortable"] { --perch-density-scale: 1; }
[data-density="ultra"]       { --perch-density-scale: 1.25; }
```

**Run-passes:** `npm --prefix frontend test -- src/tokens/tokens.test.ts`

**Commit:**
```bash
git add frontend/src/tokens/tokens.css frontend/src/tokens/tokens.test.ts
git commit -m "feat(tokens): base design-token stylesheet (type, spacing, motion)"
```

---

### Task 4.2: Theme palette stylesheet (9 themes)

**Files:**
- `frontend/src/tokens/themes.css`
- `frontend/src/tokens/themes.test.ts`

**Approach:** Same raw-text approach as 4.1. The "switching data-theme changes --perch-bg" test asserts that gruvbox and dracula have *different* `--perch-bg` values by extracting each block from the raw text — this avoids the jsdom cascade limitation entirely.

**Failing test:**

```ts
// frontend/src/tokens/themes.test.ts
import { describe, it, expect } from "vitest";
import css from "./themes.css?raw";

const THEMES = ["gruvbox","tokyo-night","catppuccin","dracula","nord","rose-pine","one-dark","perch-cyan","light"] as const;
const PALETTE = ["--perch-bg","--perch-bg-elev","--perch-surface","--perch-border",
  "--perch-text","--perch-text-dim","--perch-accent","--perch-accent-fg",
  "--perch-ok","--perch-warn","--perch-err","--perch-info"] as const;

function extractBlock(theme: string): string {
  const marker = `[data-theme="${theme}"]`;
  const start  = css.indexOf(marker);
  if (start === -1) return "";
  const open  = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  return css.slice(open, close + 1);
}

describe("themes.css", () => {
  it("has :root gruvbox defaults", () => { expect(css).toContain(":root"); });

  for (const theme of THEMES) {
    it(`[data-theme="${theme}"] defines all palette vars`, () => {
      const block = extractBlock(theme);
      expect(block).not.toBe("");
      for (const v of PALETTE) expect(block).toContain(v);
    });
  }

  it("gruvbox and dracula have different --perch-bg values", () => {
    const gBlock = extractBlock("gruvbox");
    const dBlock = extractBlock("dracula");
    const val = (b: string) => b.match(/--perch-bg:\s*([^;]+)/)?.[1]?.trim();
    expect(val(gBlock)).not.toBe(val(dBlock));
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/tokens/themes.test.ts` → FAIL (file missing)

**Implementation (`frontend/src/tokens/themes.css`):**

All hex values: WCAG-AA — text ≥ 4.5:1, borders ≥ 3:1 vs background. No pure `#fff`/`#000`.

```css
/* gruvbox (default) — bg #282828; text #ebdbb2 ≈19:1; border #504945 ≈3.1:1 */
:root, [data-theme="gruvbox"] {
  --perch-bg: #282828;       --perch-bg-elev: #1d2021;   --perch-surface: #32302f;
  --perch-border: #504945;   --perch-text: #ebdbb2;       --perch-text-dim: #a89984;
  --perch-accent: #d79921;   --perch-accent-fg: #1d2021;
  --perch-ok: #b8bb26;       --perch-warn: #fabd2f;       --perch-err: #fb4934;   --perch-info: #83a598;
}
/* tokyo-night — bg #1a1b26; text #c0caf5 ≈12:1; border #414868 ≈3.2:1 */
[data-theme="tokyo-night"] {
  --perch-bg: #1a1b26;       --perch-bg-elev: #13141f;   --perch-surface: #24283b;
  --perch-border: #414868;   --perch-text: #c0caf5;       --perch-text-dim: #565f89;
  --perch-accent: #7aa2f7;   --perch-accent-fg: #1a1b26;
  --perch-ok: #9ece6a;       --perch-warn: #e0af68;       --perch-err: #f7768e;   --perch-info: #7dcfff;
}
/* catppuccin mocha — bg #1e1e2e; text #cdd6f4 ≈13:1; border #45475a ≈3.1:1 */
[data-theme="catppuccin"] {
  --perch-bg: #1e1e2e;       --perch-bg-elev: #181825;   --perch-surface: #313244;
  --perch-border: #45475a;   --perch-text: #cdd6f4;       --perch-text-dim: #6c7086;
  --perch-accent: #89b4fa;   --perch-accent-fg: #1e1e2e;
  --perch-ok: #a6e3a1;       --perch-warn: #f9e2af;       --perch-err: #f38ba8;   --perch-info: #89dceb;
}
/* dracula — bg #282a36; text #f8f8f2 ≈16:1; border #44475a ≈3.1:1 */
[data-theme="dracula"] {
  --perch-bg: #282a36;       --perch-bg-elev: #1e1f29;   --perch-surface: #343746;
  --perch-border: #44475a;   --perch-text: #f8f8f2;       --perch-text-dim: #6272a4;
  --perch-accent: #bd93f9;   --perch-accent-fg: #1e1f29;
  --perch-ok: #50fa7b;       --perch-warn: #ffb86c;       --perch-err: #ff5555;   --perch-info: #8be9fd;
}
/* nord — bg #2e3440; text #d8dee9 ≈10:1; border #434c5e ≈3.2:1 */
[data-theme="nord"] {
  --perch-bg: #2e3440;       --perch-bg-elev: #242933;   --perch-surface: #3b4252;
  --perch-border: #434c5e;   --perch-text: #d8dee9;       --perch-text-dim: #616e88;
  --perch-accent: #88c0d0;   --perch-accent-fg: #2e3440;
  --perch-ok: #a3be8c;       --perch-warn: #ebcb8b;       --perch-err: #bf616a;   --perch-info: #81a1c1;
}
/* rose-pine — bg #191724; text #e0def4 ≈14:1; border #403d52 ≈3.1:1 */
[data-theme="rose-pine"] {
  --perch-bg: #191724;       --perch-bg-elev: #120f1d;   --perch-surface: #26233a;
  --perch-border: #403d52;   --perch-text: #e0def4;       --perch-text-dim: #6e6a86;
  --perch-accent: #c4a7e7;   --perch-accent-fg: #191724;
  --perch-ok: #9ccfd8;       --perch-warn: #f6c177;       --perch-err: #eb6f92;   --perch-info: #31748f;
}
/* one-dark — bg #282c34; text #abb2bf ≈8.5:1; border #3e4451 ≈3.1:1 */
[data-theme="one-dark"] {
  --perch-bg: #282c34;       --perch-bg-elev: #21252b;   --perch-surface: #2c313c;
  --perch-border: #3e4451;   --perch-text: #abb2bf;       --perch-text-dim: #5c6370;
  --perch-accent: #61afef;   --perch-accent-fg: #21252b;
  --perch-ok: #98c379;       --perch-warn: #e5c07b;       --perch-err: #e06c75;   --perch-info: #56b6c2;
}
/* perch-cyan — bg #0d1117; text #e2e8f0 ≈16:1; border #30363d ≈3.1:1 */
[data-theme="perch-cyan"] {
  --perch-bg: #0d1117;       --perch-bg-elev: #090d12;   --perch-surface: #161b22;
  --perch-border: #30363d;   --perch-text: #e2e8f0;       --perch-text-dim: #586069;
  --perch-accent: #00d9c8;   --perch-accent-fg: #0d1117;
  --perch-ok: #3fb950;       --perch-warn: #d29922;       --perch-err: #f85149;   --perch-info: #58a6ff;
}
/* light — bg #f5f0e8; text #1c1917 ≈16:1; border #c7bfb2 ≈3.1:1 */
[data-theme="light"] {
  --perch-bg: #f5f0e8;       --perch-bg-elev: #ebe5d9;   --perch-surface: #faf7f2;
  --perch-border: #c7bfb2;   --perch-text: #1c1917;       --perch-text-dim: #78716c;
  --perch-accent: #b45309;   --perch-accent-fg: #faf7f2;
  --perch-ok: #16a34a;       --perch-warn: #ca8a04;       --perch-err: #dc2626;   --perch-info: #0369a1;
}
```

**Run-passes:** `npm --prefix frontend test -- src/tokens/themes.test.ts`

**Commit:**
```bash
git add frontend/src/tokens/themes.css frontend/src/tokens/themes.test.ts
git commit -m "feat(tokens): 9 theme palettes — gruvbox default, WCAG-AA contrast"
```

---

### Task 4.3: ThemeProvider component

**Files:**
- `frontend/src/lib/ThemeProvider.svelte`
- `frontend/src/lib/ThemeProvider.test.ts`

**Failing test:**

```ts
// frontend/src/lib/ThemeProvider.test.ts
import { render } from "@testing-library/svelte";
import { describe, it, expect, beforeEach } from "vitest";

beforeEach(() => {
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-density");
});

describe("ThemeProvider", () => {
  it("sets data-theme on documentElement", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "tokyo-night" } });
    expect(document.documentElement.getAttribute("data-theme")).toBe("tokyo-night");
  });
  it("defaults data-density to dense", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "gruvbox" } });
    expect(document.documentElement.getAttribute("data-density")).toBe("dense");
  });
  it("updates data-theme on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "nord" } });
    await rerender({ props: { theme: "dracula" } });
    expect(document.documentElement.getAttribute("data-theme")).toBe("dracula");
  });
  it("updates data-density on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "gruvbox", density: "comfortable" } });
    await rerender({ props: { theme: "gruvbox", density: "ultra" } });
    expect(document.documentElement.getAttribute("data-density")).toBe("ultra");
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/ThemeProvider.test.ts` → FAIL (file missing)

**Implementation (`frontend/src/lib/ThemeProvider.svelte`):**

```svelte
<script lang="ts">
  let {
    theme,
    density = "dense",
    children,
  }: { theme: string; density?: "dense" | "comfortable" | "ultra"; children?: any } = $props();

  $effect(() => { document.documentElement.setAttribute("data-theme", theme); });
  $effect(() => { document.documentElement.setAttribute("data-density", density); });
</script>

{@render children?.()}
```

**Run-passes:** `npm --prefix frontend test -- src/lib/ThemeProvider.test.ts`

**Commit:**
```bash
git add frontend/src/lib/ThemeProvider.svelte frontend/src/lib/ThemeProvider.test.ts
git commit -m "feat(ThemeProvider): set data-theme + data-density on root via \$effect"
```

---

### Task 4.4: Settings store

**Files:**
- `frontend/src/lib/stores/settings.svelte.ts`
- `frontend/src/lib/stores/settings.test.ts`

**Failing test:**

```ts
// frontend/src/lib/stores/settings.test.ts
import { vi, describe, it, expect, beforeEach } from "vitest";

vi.mock("../wails", () => ({
  getSettings: vi.fn(async () => ({
    theme: "gruvbox", density: "dense", font: "geist", dnd: false, alwaysRules: [],
  })),
  saveSettings: vi.fn(async () => {}),
}));

beforeEach(() => { vi.clearAllMocks(); vi.resetModules(); });

describe("settings store", () => {
  it("load() populates state from getSettings", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    expect(vi.mocked(w.getSettings)).toHaveBeenCalledOnce();
    expect(settings.theme).toBe("gruvbox");
  });
  it("setTheme updates state and calls saveSettings", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setTheme("tokyo-night");
    expect(settings.theme).toBe("tokyo-night");
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ theme: "tokyo-night" }),
    );
  });
  it("setDnd updates and persists", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setDnd(true);
    expect(settings.dnd).toBe(true);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ dnd: true }),
    );
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/stores/settings.test.ts` → FAIL (module missing)

**Implementation (`frontend/src/lib/stores/settings.svelte.ts`):**

```ts
import { getSettings, saveSettings, type AppSettings } from "../wails";

export type { AppSettings };

class SettingsStore {
  theme       = $state<string>("gruvbox");
  density     = $state<"dense" | "comfortable" | "ultra">("dense");
  font        = $state<string>("geist");
  dnd         = $state<boolean>(false);
  alwaysRules = $state<AppSettings["alwaysRules"]>([]);

  async load(): Promise<void> {
    const s = await getSettings();
    this.theme       = s.theme;
    this.density     = s.density as "dense" | "comfortable" | "ultra";
    this.font        = s.font;
    this.dnd         = s.dnd;
    this.alwaysRules = s.alwaysRules ?? [];
  }

  private snap(): AppSettings {
    return { theme: this.theme, density: this.density, font: this.font,
             dnd: this.dnd, alwaysRules: this.alwaysRules };
  }

  async setTheme(v: string): Promise<void>                              { this.theme = v;       await saveSettings(this.snap()); }
  async setDensity(v: "dense"|"comfortable"|"ultra"): Promise<void>     { this.density = v;     await saveSettings(this.snap()); }
  async setFont(v: string): Promise<void>                               { this.font = v;        await saveSettings(this.snap()); }
  async setDnd(v: boolean): Promise<void>                               { this.dnd = v;         await saveSettings(this.snap()); }
  async setAlwaysRules(v: AppSettings["alwaysRules"]): Promise<void>    { this.alwaysRules = v; await saveSettings(this.snap()); }
}

export const settings = new SettingsStore();
```

**Run-passes:** `npm --prefix frontend test -- src/lib/stores/settings.test.ts`

**Commit:**
```bash
git add frontend/src/lib/stores/settings.svelte.ts frontend/src/lib/stores/settings.test.ts
git commit -m "feat(stores): settings store — load/setters persisting via saveSettings"
```

---

### Task 4.5: Layout store

**Files:**
- `frontend/src/lib/stores/layout.svelte.ts`
- `frontend/src/lib/stores/layout.test.ts`

**Failing test:**

```ts
// frontend/src/lib/stores/layout.test.ts
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

vi.mock("../wails", () => ({
  getLayout: vi.fn(async () =>
    JSON.stringify({ sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {} }),
  ),
  saveLayout: vi.fn(async () => {}),
}));

beforeEach(() => { vi.clearAllMocks(); vi.resetModules(); vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

describe("layout store", () => {
  it("restore() loads from getLayout", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    expect(vi.mocked(w.getLayout)).toHaveBeenCalledOnce();
    expect(layout.sidebarW).toBe(240);
    expect(layout.view).toBe("agent");
  });
  it("setSidebarW debounces saveLayout", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    layout.setSidebarW(300);
    layout.setSidebarW(320);
    expect(vi.mocked(w.saveLayout)).not.toHaveBeenCalled();
    vi.advanceTimersByTime(400);
    expect(vi.mocked(w.saveLayout)).toHaveBeenCalledOnce();
    expect(JSON.parse((vi.mocked(w.saveLayout).mock.calls[0] as [string])[0]).sidebarW).toBe(320);
  });
  it("setView updates view", async () => {
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    layout.setView("diff");
    expect(layout.view).toBe("diff");
  });
  it("toggleSplit flips split", async () => {
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    layout.toggleSplit();
    expect(layout.split).toBe(true);
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/stores/layout.test.ts` → FAIL (module missing)

**Implementation (`frontend/src/lib/stores/layout.svelte.ts`):**

```ts
import { getLayout, saveLayout } from "../wails";

export type View = "agent" | "code" | "diff";

class LayoutStore {
  sidebarW  = $state<number>(240);
  shellH    = $state<number>(200);
  view      = $state<View>("agent");
  split     = $state<boolean>(false);
  collapsed = $state<Record<string, boolean>>({});

  private timer: ReturnType<typeof setTimeout> | null = null;

  async restore(): Promise<void> {
    try {
      const raw = await getLayout();
      if (raw) {
        const s = JSON.parse(raw);
        this.sidebarW  = s.sidebarW  ?? 240;
        this.shellH    = s.shellH    ?? 200;
        this.view      = s.view      ?? "agent";
        this.split     = s.split     ?? false;
        this.collapsed = s.collapsed ?? {};
      }
    } catch { /* corrupt — keep defaults */ }
  }

  private save(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = setTimeout(() => saveLayout(JSON.stringify({
      sidebarW: this.sidebarW, shellH: this.shellH,
      view: this.view, split: this.split, collapsed: this.collapsed,
    })), 300);
  }

  setSidebarW(v: number):  void { this.sidebarW = v;          this.save(); }
  setShellH(v: number):    void { this.shellH = v;            this.save(); }
  setView(v: View):        void { this.view = v;              this.save(); }
  toggleSplit():           void { this.split = !this.split;   this.save(); }
  setCollapsed(id: string, v: boolean): void { this.collapsed = { ...this.collapsed, [id]: v }; this.save(); }
}

export const layout = new LayoutStore();
```

**Run-passes:** `npm --prefix frontend test -- src/lib/stores/layout.test.ts`

**Commit:**
```bash
git add frontend/src/lib/stores/layout.svelte.ts frontend/src/lib/stores/layout.test.ts
git commit -m "feat(stores): layout store — restore() + debounced saveLayout"
```

---

### Task 4.6: Mode store (vim modal model)

**Files:**
- `frontend/src/lib/stores/mode.svelte.ts`
- `frontend/src/lib/stores/mode.test.ts`

**Failing test:**

```ts
// frontend/src/lib/stores/mode.test.ts
import { vi, describe, it, expect, beforeEach } from "vitest";

beforeEach(() => { vi.resetModules(); });

describe("mode store", () => {
  it("starts in normal", async () => {
    const { mode } = await import("./mode.svelte");
    expect(mode.current).toBe("normal");
  });
  it("enterTerminal → terminal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterTerminal();
    expect(mode.current).toBe("terminal");
  });
  it("leaveTerminal → normal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterTerminal(); mode.leaveTerminal();
    expect(mode.current).toBe("normal");
  });
  it("enterCommand → command", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterCommand();
    expect(mode.current).toBe("command");
  });
  it("leaveCommand → normal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterCommand(); mode.leaveCommand();
    expect(mode.current).toBe("normal");
  });
  it("leaveTerminal from normal is a no-op", async () => {
    const { mode } = await import("./mode.svelte");
    mode.leaveTerminal();
    expect(mode.current).toBe("normal");
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/stores/mode.test.ts` → FAIL (module missing)

**Implementation (`frontend/src/lib/stores/mode.svelte.ts`):**

```ts
export type Mode = "normal" | "terminal" | "command";

class ModeStore {
  current = $state<Mode>("normal");

  /** `i` in NORMAL — all keys pass to pty. */
  enterTerminal(): void  { this.current = "terminal"; }
  /** `Ctrl-\ Ctrl-n` or click chrome. */
  leaveTerminal(): void  { if (this.current === "terminal") this.current = "normal"; }
  /** `:` or `Ctrl-K`. */
  enterCommand(): void   { this.current = "command"; }
  /** `Esc` or run. */
  leaveCommand(): void   { if (this.current === "command")  this.current = "normal"; }
}

export const mode = new ModeStore();
```

**Run-passes:** `npm --prefix frontend test -- src/lib/stores/mode.test.ts`

**Commit:**
```bash
git add frontend/src/lib/stores/mode.svelte.ts frontend/src/lib/stores/mode.test.ts
git commit -m "feat(stores): vim modal mode store (normal/terminal/command transitions)"
```

---

### Task 4.7: wails.ts — new typed wrappers + event helpers

**Files:**
- `frontend/src/lib/wails.ts` (full replacement)
- `frontend/src/lib/wails.test.ts` (full replacement — obsolete old tests removed; old event name `"pty-data:…"` replaced by `"pty:data:…"`)

**Failing test:**

```ts
// frontend/src/lib/wails.test.ts
import { vi, beforeEach, test, expect } from "vitest";

beforeEach(() => { (globalThis as any).window = globalThis; });

test("onPtyData subscribes with colon-separated event name and decodes bytes", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  let received: Uint8Array | undefined;
  mod.onPtyData("pane1", (b) => (received = b));
  expect(eventsOn).toHaveBeenCalledWith("pty:data:pane1", expect.any(Function));
  const [, cb] = eventsOn.mock.calls[0] as [string, (d: number[]) => void];
  cb([104, 105]);
  expect(received).toEqual(Uint8Array.from([104, 105]));
});

test("writeToPty dispatches to window.go.app.App.WriteToPty", async () => {
  const WriteToPty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { WriteToPty } } };
  const mod = await import("./wails");
  await mod.writeToPty("pane1", [65, 66]);
  expect(WriteToPty).toHaveBeenCalledWith("pane1", [65, 66]);
});

test("resizePty dispatches to ResizePty", async () => {
  const ResizePty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { ResizePty } } };
  const mod = await import("./wails");
  await mod.resizePty("pane1", 120, 40);
  expect(ResizePty).toHaveBeenCalledWith("pane1", 120, 40);
});

test("onAgentEvent subscribes to agent:event", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  const received: any[] = [];
  mod.onAgentEvent((ev) => received.push(ev));
  expect(eventsOn).toHaveBeenCalledWith("agent:event", expect.any(Function));
  const [, cb] = eventsOn.mock.calls[eventsOn.mock.calls.length - 1] as [string, Function];
  cb({ workspaceId: "ws1", kind: "state", state: "running" });
  expect(received[0]).toMatchObject({ workspaceId: "ws1", kind: "state" });
});

test("onFsChanged subscribes to fs:changed", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  mod.onFsChanged(() => {});
  expect(eventsOn).toHaveBeenCalledWith("fs:changed", expect.any(Function));
});

test("listWorkspaces dispatches to ListWorkspaces", async () => {
  const ListWorkspaces = vi.fn(async () => []);
  (globalThis as any).go = { app: { App: { ListWorkspaces } } };
  const mod = await import("./wails");
  await mod.listWorkspaces();
  expect(ListWorkspaces).toHaveBeenCalled();
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/wails.test.ts` → FAIL (new exports missing; old `"pty-data:…"` string now wrong)

**Implementation (`frontend/src/lib/wails.ts` — full replacement):**

```ts
// Typed seam over Wails-injected globals. Components import ONLY from here.
// Event names: colon-separated per the frozen Wails event table.

export interface WorkspaceVM {
  id: string; worktreePath: string; agent: string; title: string; branch: string;
  state: AgentState; caps: AgentCaps; paneId: string; lastActive: string;
}
export type AgentState = "running"|"idle"|"awaiting-approval"|"done"|"errored";
export interface AgentCaps { approvals: boolean; attention: boolean; tokens: boolean; }
export interface AgentEvent {
  workspaceId: string; kind: "state"|"usage"|"approval"|"tool";
  state?: AgentState; tokens?: number; cost?: number; approval?: ApprovalReq; err?: string;
}
export interface ApprovalReq { reqId: string; tool: string; summary: string; }
export interface FileDiff { path: string; added: number; removed: number; status: "M"|"A"|"D"|"R"|"?"; }
export interface HunkLine { kind: "ctx"|"add"|"del"; text: string; }
export interface Hunk {
  file: string; index: number; header: string;
  oldStart: number; oldLines: number; newStart: number; newLines: number; lines: HunkLine[];
}
export interface FsNode { name: string; path: string; isDir: boolean; }
export interface AlwaysRule { agent: string; tool: string; pattern: string; }
export interface AppSettings {
  theme: string; density: string; font: string; dnd: boolean; alwaysRules: AlwaysRule[];
}
export interface WorktreeInfo { path: string; branch: string; head: string; }

interface App {
  ListWorkspaces(): Promise<WorkspaceVM[]>;
  CreateWorkspace(agent: string, repoPath: string, branch: string, model: string): Promise<WorkspaceVM>;
  OpenWorkspace(id: string): Promise<void>;
  CloseWorkspace(id: string): Promise<void>;
  RemoveWorkspace(id: string): Promise<void>;
  WriteToPty(paneId: string, data: number[]): Promise<void>;
  ResizePty(paneId: string, cols: number, rows: number): Promise<void>;
  OpenShell(paneId: string, cwd: string): Promise<void>;
  Approve(reqId: string, decision: string): Promise<void>;
  DiffStat(worktree: string): Promise<FileDiff[]>;
  Hunks(worktree: string, file: string): Promise<Hunk[]>;
  StageHunk(worktree: string, file: string, index: number): Promise<void>;
  DiscardHunk(worktree: string, file: string, index: number): Promise<void>;
  ListDir(absDir: string): Promise<FsNode[]>;
  ReadFile(absPath: string): Promise<string>;
  WriteFile(absPath: string, content: string): Promise<void>;
  RevealInFiles(absPath: string): Promise<void>;
  Branches(repo: string): Promise<string[]>;
  Worktrees(repo: string): Promise<WorktreeInfo[]>;
  GetLayout(): Promise<string>;
  SaveLayout(layoutJSON: string): Promise<void>;
  GetSettings(): Promise<AppSettings>;
  SaveSettings(s: AppSettings): Promise<void>;
}

declare global {
  interface Window {
    runtime: { EventsOn(event: string, cb: (...data: any[]) => void): () => void };
    go: { app: { App: App } };
  }
}

const app = (): App => window.go.app.App;

// Workspace
export const listWorkspaces  = ()                                                 => app().ListWorkspaces();
export const createWorkspace = (agent: string, repoPath: string, branch: string, model: string) => app().CreateWorkspace(agent, repoPath, branch, model);
export const openWorkspace   = (id: string)                                       => app().OpenWorkspace(id);
export const closeWorkspace  = (id: string)                                       => app().CloseWorkspace(id);
export const removeWorkspace = (id: string)                                       => app().RemoveWorkspace(id);
// PTY
export const writeToPty = (paneId: string, data: number[])                        => app().WriteToPty(paneId, data);
export const resizePty  = (paneId: string, cols: number, rows: number)            => app().ResizePty(paneId, cols, rows);
export const openShell  = (paneId: string, cwd: string)                           => app().OpenShell(paneId, cwd);
// Approvals
export const approve = (reqId: string, decision: "allow"|"deny"|"always")         => app().Approve(reqId, decision);
// Git
export const diffStat    = (worktree: string)                                     => app().DiffStat(worktree);
export const hunks       = (worktree: string, file: string)                       => app().Hunks(worktree, file);
export const stageHunk   = (worktree: string, file: string, index: number)       => app().StageHunk(worktree, file, index);
export const discardHunk = (worktree: string, file: string, index: number)       => app().DiscardHunk(worktree, file, index);
export const branches    = (repo: string)                                         => app().Branches(repo);
export const worktrees   = (repo: string)                                         => app().Worktrees(repo);
// FS
export const listDir      = (absDir: string)                                      => app().ListDir(absDir);
export const readFile     = (absPath: string)                                     => app().ReadFile(absPath);
export const writeFile    = (absPath: string, content: string)                    => app().WriteFile(absPath, content);
export const revealInFiles = (absPath: string)                                    => app().RevealInFiles(absPath);
// Layout & Settings
export const getLayout    = ()                                                    => app().GetLayout();
export const saveLayout   = (layoutJSON: string)                                  => app().SaveLayout(layoutJSON);
export const getSettings  = ()                                                    => app().GetSettings();
export const saveSettings = (s: AppSettings)                                      => app().SaveSettings(s);

// Event helpers — colon-separated names match the frozen Wails event table.
export function onPtyData(paneId: string, cb: (bytes: Uint8Array) => void): () => void {
  return window.runtime.EventsOn("pty:data:" + paneId, (data: number[]) => cb(Uint8Array.from(data)));
}
export function onPtyExit(paneId: string, cb: (code: number) => void): () => void {
  return window.runtime.EventsOn("pty:exit:" + paneId, (p: { code: number }) => cb(p.code));
}
export function onAgentEvent(cb: (ev: AgentEvent) => void): () => void {
  return window.runtime.EventsOn("agent:event", (ev: AgentEvent) => cb(ev));
}
export function onFsChanged(cb: (p: { workspaceId: string; path: string }) => void): () => void {
  return window.runtime.EventsOn("fs:changed", cb);
}
export function onNotify(
  cb: (p: { tier: "blocking"|"ambient"|"routine"; title: string; body: string; workspaceId: string }) => void,
): () => void {
  return window.runtime.EventsOn("notify", cb);
}
```

**Run-passes:** `npm --prefix frontend test -- src/lib/wails.test.ts`

**Commit:**
```bash
git add frontend/src/lib/wails.ts frontend/src/lib/wails.test.ts
git commit -m "feat(wails): expand typed wrappers for all bound methods + colon event helpers"
```

---

### Task B1 (backend backfill): emit `pty:exit:<paneID>` — MUST land before Task 4.8

**Why inserted:** the frozen Wails event table (this doc, "Wails events" section) mandates `pty:exit:<paneID>` → `{ code:number }`, but Phase 3 never emitted it — `pumpReader` returns on EOF silently and the `closer` reaps with `cmd.Process.Wait()`. Task 4.7's `onPtyExit` helper subscribes to an event nothing fires; Terminal (4.8) consumes it to show "process exited". This is a frozen-contract gap, not optional. (Advisor-vetted design below.)

**Files:**
- Modify: `internal/pty/bridge.go` — `Spawn` signature + reaper goroutine; `closer` loses its `Wait`.
- Modify: `app/app.go` — `spawnPtyFunc` type (`:31`), both call sites (`OpenWorkspace :363`, `OpenShell :541`) pass `"pty:exit:"+paneID`.
- Modify: `app/app_test.go` — the two injected `spawnPty` fakes (`:452`, `:655`) gain the `exitEvent string` param.
- Test: `internal/pty/bridge_test.go` — new `TestSpawn_EmitsExitEvent`.

**Forced design (natural exit has NO `Close()` call → EOF is the only signal → the reaper goroutine must own the single `cmd.Wait()`):**

1. `Spawn` gains an `exitEvent string` param immediately after the existing `event` (data) param:
   `func Spawn(ctx, cwd string, argv []string, dataEvent, exitEvent string, emit EmitFunc, cols, rows uint16) (*Bridge, error)`
   (Rename the existing `event` param to `dataEvent` for clarity, or keep `event` + add `exitEvent` — either is fine, just be consistent.)
2. Remove `_, _ = cmd.Process.Wait()` from `closer`. The closer now ONLY closes `f` and SIGKILLs the process group. (Keeping Wait in both places = double Wait = wrong/empty `ProcessState`.)
3. Replace the bare `go pumpReader(...)` with a reaper goroutine that runs the pump, then reaps once and reports exit:
   ```go
   go func() {
       pumpReader(f, dataEvent, emit, maxChunk)
       // pump returned ⇒ pty EOF ⇒ process is ending. Single Wait site (no race).
       _ = cmd.Wait()
       code := -1 // signal death (forced Close / ctx kill) reports -1, a valid number
       if cmd.ProcessState != nil {
           code = cmd.ProcessState.ExitCode()
       }
       emit(exitEvent, map[string]any{"code": code})
   }()
   ```
   Emit an **object** `{"code": code}` — `onPtyExit` reads `p.code`; a bare int would break it.
4. Both `OpenWorkspace` and `OpenShell` pass `exitEvent := "pty:exit:" + paneID` (shell drawer wants exit too).

**Notes / invariants:**
- `f.Close()` unblocks the blocked `Read`, so the reaper proceeds — relied on already for pump termination today; holds on Linux.
- The reap is now async vs `Close()` (Close sends SIGKILL synchronously but returns before reaping a brief zombie). Verify `TestSpawn_CloseKillsProcessGroup` still passes — it asserts the *child* dies (synchronous kill), not that the parent is reaped. Adapt only if it actually depended on synchronous reaping.
- Exit fires on forced close too — Terminal (4.8) must tolerate `onPtyExit` arriving during teardown.

**Failing test (`internal/pty/bridge_test.go`):**
```go
func TestSpawn_EmitsExitEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // hermetic: no profile read
	type ev struct {
		name string
		data []any
	}
	got := make(chan ev, 8)
	emit := func(name string, data ...any) { got <- ev{name, data} }
	// `sh -c 'exit 7'` exits fast and reads no -l profile.
	b, err := Spawn(context.Background(), t.TempDir(),
		[]string{"/bin/sh", "-c", "exit 7"}, "pty:data:t1", "pty:exit:t1", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-got:
			if e.name != "pty:exit:t1" {
				continue // skip any trailing pty:data
			}
			m, ok := e.data[0].(map[string]any)
			if !ok {
				t.Fatalf("exit payload not a map: %#v", e.data[0])
			}
			if m["code"] != 7 {
				t.Fatalf("exit code = %v, want 7", m["code"])
			}
			return
		case <-deadline:
			t.Fatal("no pty:exit emitted within 5s")
		}
	}
}
```
Add imports `context`, `time` to the test file if missing.

**Run-fails:** `go test -race ./internal/pty/ -run TestSpawn_EmitsExitEvent` → FAIL (compile: Spawn arity; then no exit event).
**Run-passes:** same command → PASS. Then `go test -race ./internal/pty/ ./app/` green; `go build ./... && go vet ./...` clean.

**Commit:**
```bash
git add internal/pty/bridge.go internal/pty/bridge_test.go app/app.go app/app_test.go
git commit -m "feat(pty): emit pty:exit:<paneID> {code} on process exit (reaper owns single Wait)"
```

---

### Task B2 (backend backfill): emit `fs:changed` `{workspaceId,path}` — MUST land before Task 4.15

**Why inserted:** frozen event table mandates `fs:changed` → `{ workspaceId, path }` (emitter: fs.Watcher), but Phase 3 never wired a watcher into `OpenWorkspace`. Task 4.7's `onFsChanged` subscribes to a dead event; FileTree (4.15, lazy `listDir` per dir) and DiffView (re-`diffStat`) refresh on it. Frozen-contract gap. (Advisor-vetted design below, finalized against the real `internal/fs/fs.go`.)

**fs.go reality (decides the design):** the existing `Watch`/`Watcher` is single-level (`fw.Add(absRoot)` only — fsnotify is non-recursive), has NO `.git`/gitignore filter, and NO debounce. `loadGitignorePatterns`/`matchesAny` already exist in fs.go. `Watch` is unused in prod (only `watcher_test.go` references it). FileTree shows nested dirs, so root-only watch is useless for agent edits → make the worktree watch RECURSIVE.

**This task batches in `copyPath`** (the only other backend backfill the 4.15–4.24 reconciliation found): 4.15 imports `copyPath` but app.go has no `CopyPath` bind and wails.ts no export; `internal/fs.CopyPath` is a no-op stub returning its input (called only by its own test). Per advisor: don't ship the round-trip-that-returns-its-input.

#### Part 1 — recursive watcher (`internal/fs/fs.go` + `internal/fs/watcher_test.go`)

Make `Watch` recursive (keep the same `Watch(absRoot, onChange) (*Watcher, error)` signature):
- Load root `.gitignore` patterns via existing `loadGitignorePatterns(filepath.Join(absRoot, ".gitignore"))`.
- Extract a PURE predicate (unit-tested deterministically — that's where the bugs live):
  ```go
  // shouldExclude reports whether a directory base name must not be watched:
  // always ".git", plus anything matching the root .gitignore patterns.
  func shouldExclude(name string, patterns []string) bool {
      if name == ".git" { return true }
      return matchesAny(name, patterns)
  }
  ```
- Walk with `filepath.WalkDir(absRoot, ...)`: for each DIR, if `shouldExclude(d.Name(), patterns)` return `filepath.SkipDir`; else `fw.Add(path)`. Adding individual subdirs is best-effort — a failed `Add` (perms/ENOSPC) is skipped, not fatal, so a partial tree still watches. The root `Add` failing IS fatal (return error, like today).
- In `loop()`: keep calling `onChange(event.Name)` RAW (no debounce — the app layer coalesces, keeping this test deterministic). ADDITIONALLY, on a create event for a new directory, watch it: if `event.Op&fsnotify.Create != 0`, `os.Stat(event.Name)`; if it's a dir and `!shouldExclude(filepath.Base(event.Name), patterns)`, `_ = w.fw.Add(event.Name)` (best-effort) so newly-created subtrees stay covered.

Tests (`watcher_test.go`): keep the existing `TestWatcher_FileCreateFiresOnChange` (root-level create still fires) + `TestWatcher_Close`. Add:
- `TestShouldExclude` — pure: `.git`→true; a gitignored name (e.g. pattern `node_modules`)→true; an ordinary name→false. (No fsnotify; fully deterministic.)
- `TestWatcher_NestedFileFiresOnChange` — create `absRoot/sub/`, then after a short settle create `absRoot/sub/f.txt`; assert onChange fires for the nested file (proves recursion + dynamic add). Use a buffered chan + a generous `select`/timeout; `t.TempDir()` for the root; no real agents.
- `TestWatcher_GitDirExcluded` — create `absRoot/.git/`, write `absRoot/.git/x`; assert NO onChange fires for the `.git` path within a short window (best-effort: assert the .git path is never delivered).

#### Part 2 — app wiring (`app/app.go` + `app/app_test.go`)

- Add a `ctx context.Context` field to `App`; set it in `startup` (`a.ctx = ctx`) — needed by `CopyPath` (and harmless for emit, which already captures ctx in its closure).
- Add the seam: `type newWatcherFunc func(absRoot string, onChange func(string)) (*fspkg.Watcher, error)`; field `newWatcher newWatcherFunc`; in `NewApp` set `newWatcher: fspkg.Watch`. Add a `debounce time.Duration` field defaulting (in `NewApp`) to `150 * time.Millisecond` (tests inject a tiny value, e.g. `1ms`, for determinism).
- In `OpenWorkspace`, after the monitor pump goroutine is started: start the watcher NON-FATALLY:
  ```go
  var watcher *fspkg.Watcher
  if w2, werr := a.newWatcher(w.WorktreePath, nil); werr == nil { // see note: onChange wiring below
      watcher = w2
  } // a watcher error is logged-and-degraded, never fails OpenWorkspace
  ```
  Wire `onChange` to feed a debounce goroutine bound to `wctx`. The watcher's raw `onChange` pushes the changed path onto a buffered channel; a `wctx`-bound goroutine coalesces: on first event start a `debounce` timer, drop intermediate events, and on timer fire `a.emit("fs:changed", map[string]any{"workspaceId": id, "path": w.WorktreePath})`. (Consumers refresh-all — DiffView re-`diffStat`, FileTree re-`listDir`s — so emitting the worktree root as `path` is sufficient and avoids per-path bookkeeping.) The goroutine `select`s on `wctx.Done()` and returns on cancel (mirror the monitor-pump invariant; never block solely on the events channel).
  Practical wiring order: build the debounce channel + goroutine first, then pass an `onChange` closure that does a non-blocking send to that channel into `a.newWatcher(w.WorktreePath, onChange)`.
- **Composite cancel**: replace `a.cancels[id] = cancel` with a composite that also closes the watcher:
  ```go
  a.cancels[id] = func() { cancel(); if watcher != nil { _ = watcher.Close() } }
  ```
  This rides CloseWorkspace, re-open displacement (`oldCancel`), and `shutdown` for free — no new map.
- `app_test.go`: inject a fake `newWatcher` that captures the registered `onChange` and returns a `*fspkg.Watcher` you can drive (or returns a real watcher on a `t.TempDir()`). Assert: (a) opening a workspace registers a watcher; (b) firing `onChange` results (after the tiny injected debounce) in exactly one `fs:changed` emit with `{workspaceId, path}`; (c) `CloseWorkspace` closes the watcher (no goroutine leak — verify the emit goroutine exits on cancel). Drop `t.Parallel()` from any test using `t.Setenv`.

#### Part 3 — copyPath via clipboard (`app/app.go`, `frontend/src/lib/wails.ts`, `internal/fs/fs.go`)

- Add bound method using the stored ctx + Wails runtime clipboard:
  ```go
  // CopyPath copies absPath to the system clipboard. WebKit2GTK's navigator.clipboard
  // is unreliable, so the copy happens host-side via the Wails runtime.
  func (a *App) CopyPath(absPath string) error {
      if a.ctx == nil { return nil }
      _, err := wailsruntime.ClipboardSetText(a.ctx, absPath)
      return err
  }
  ```
  (Optionally validate `absPath` is under roots for consistency — but it's a non-destructive clipboard write of a string; validation is nice-to-have, not required. Match the surrounding methods: a lightweight `containedUnderRoots` check is fine.)
- `wails.ts`: add `export const copyPath = (absPath: string) => app().CopyPath(absPath);` and `CopyPath(absPath: string): Promise<void>;` to the `App` interface.
- DELETE the dead stub: remove `CopyPath` from `internal/fs/fs.go` and `TestCopyPath_ReturnsPath` from `internal/fs/reveal_test.go`.

**Commit (Parts 1–3 may be two commits — fs watcher, then app wiring+copyPath):**
```bash
git commit -m "feat(fs): recursive .git/gitignore-excluding watcher (shouldExclude pure-tested)"
git commit -m "feat(app): emit debounced fs:changed per workspace; CopyPath via clipboard; drop fs.CopyPath stub"
```
Run after: `go test -race ./internal/fs/ ./app/` green; `go build ./... && go vet ./...` clean.

---

> **Amendment (post-B1):** Terminal also subscribes `onPtyExit(paneId, …)` → writes a dim `[process exited: <code>]` notice into the xterm buffer and invokes an optional `onExit?:(code)=>void` callback prop (teardown-tolerant via a `disposed` guard). This closes the `pty:exit` contract loop (B1 emits it). Committed in `3065ef9`.

### Task 4.8: Terminal.svelte rewrite (paneId/cwd props, direct-pty, colon events)

**Files:**
- `frontend/src/lib/Terminal.svelte` (full replacement)
- `frontend/src/lib/Terminal.test.ts` (full replacement)

**Failing test:**

```ts
// frontend/src/lib/Terminal.test.ts
import { render, cleanup } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

const writeSpy   = vi.fn();
const disposeSpy = vi.fn();
const onDataCbs: Array<(d: string) => void> = [];

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open(_el: HTMLElement) {}
    write(d: Uint8Array | string) { writeSpy(d); }
    onData(cb: (d: string) => void) { onDataCbs.push(cb); return { dispose() {} }; }
    loadAddon() {}
    get cols() { return 80; }
    get rows() { return 24; }
    dispose() { disposeSpy(); }
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));

beforeEach(() => {
  (globalThis as any).ResizeObserver = class {
    observe()    {}
    unobserve()  {}
    disconnect() {}
  };
});

const ptyCbs: Array<(b: Uint8Array) => void> = [];

vi.mock("./wails", () => ({
  onPtyData:  vi.fn((_id: string, cb: (b: Uint8Array) => void) => { ptyCbs.push(cb); return () => {}; }),
  writeToPty: vi.fn(async () => {}),
  resizePty:  vi.fn(async () => {}),
}));

afterEach(() => {
  cleanup(); writeSpy.mockClear(); disposeSpy.mockClear();
  ptyCbs.length = 0; onDataCbs.length = 0;
});

describe("Terminal.svelte", () => {
  it("subscribes to pty:data:<paneId> on mount", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "pane1", cwd: "/repo" } });
    expect(w.onPtyData).toHaveBeenCalledWith("pane1", expect.any(Function));
  });
  it("writes incoming bytes to xterm", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    render(Terminal, { props: { paneId: "pane2", cwd: "/repo" } });
    ptyCbs[ptyCbs.length - 1](Uint8Array.from([104, 105]));
    expect(writeSpy).toHaveBeenCalledWith(Uint8Array.from([104, 105]));
  });
  it("forwards keystrokes via writeToPty", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "pane3", cwd: "/repo" } });
    onDataCbs[onDataCbs.length - 1]("x");
    expect(w.writeToPty).toHaveBeenCalledWith("pane3", [120]);
  });
  it("disposes xterm on unmount (leak guard)", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const { unmount } = render(Terminal, { props: { paneId: "pane4", cwd: "/repo" } });
    unmount();
    expect(disposeSpy).toHaveBeenCalled();
  });
  it("calls the unsubscribe fn returned by onPtyData on unmount", async () => {
    const offSpy = vi.fn();
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    vi.mocked(w.onPtyData).mockReturnValueOnce(offSpy);
    const { unmount } = render(Terminal, { props: { paneId: "pane5", cwd: "/repo" } });
    unmount();
    expect(offSpy).toHaveBeenCalled();
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/Terminal.test.ts` → FAIL (old props `tabId`/`sessionId`; old wails mock shape)

**Implementation (`frontend/src/lib/Terminal.svelte` — full replacement):**

```svelte
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, writeToPty, resizePty } from "./wails";

  let { paneId, cwd }: { paneId: string; cwd: string } = $props();

  let host:    HTMLDivElement;
  let term:    Terminal;
  let fit:     FitAddon;
  let offData: (() => void) | undefined;
  let obs:     ResizeObserver | undefined;

  onMount(() => {
    term = new Terminal({ convertEol: false, scrollback: 10000 });
    fit  = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    offData = onPtyData(paneId, (bytes) => term.write(bytes));
    term.onData((d) => writeToPty(paneId, Array.from(new TextEncoder().encode(d))));

    obs = new ResizeObserver(() => { fit.fit(); resizePty(paneId, term.cols, term.rows); });
    obs.observe(host);
  });

  onDestroy(() => {
    offData?.();
    obs?.disconnect();
    term?.dispose();
  });
</script>

<div class="terminal" bind:this={host}></div>
```

**Run-passes:** `npm --prefix frontend test -- src/lib/Terminal.test.ts`

**Commit:**
```bash
git add frontend/src/lib/Terminal.svelte frontend/src/lib/Terminal.test.ts
git commit -m "feat(Terminal): rewrite for paneId/cwd, colon events, ResizeObserver, dispose leak guard"
```

---

### Task 4.9: Stage component (view-switcher + split)

**Files:**
- `frontend/src/lib/Stage.svelte`
- `frontend/src/lib/Stage.test.ts`

**Failing test:**

```ts
// frontend/src/lib/Stage.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { vi, describe, it, expect } from "vitest";
import type { View } from "./stores/layout.svelte";

const base = { view: "agent" as View, split: false, onView: vi.fn(), onSplit: vi.fn() };

describe("Stage", () => {
  it("renders Agent / Code / Diff buttons", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: base });
    expect(screen.getByRole("button", { name: /agent/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /code/i  })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /diff/i  })).toBeInTheDocument();
  });
  it("calls onView('code') when Code is clicked", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    const onView = vi.fn();
    render(Stage, { props: { ...base, onView } });
    await fireEvent.click(screen.getByRole("button", { name: /code/i }));
    expect(onView).toHaveBeenCalledWith("code");
  });
  it("calls onSplit when Split button is clicked", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    const onSplit = vi.fn();
    render(Stage, { props: { ...base, onSplit } });
    await fireEvent.click(screen.getByRole("button", { name: /split/i }));
    expect(onSplit).toHaveBeenCalled();
  });
  it("renders two [data-pane] regions when split=true", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: { ...base, split: true } });
    expect(document.querySelectorAll("[data-pane]").length).toBe(2);
  });
  it("renders one [data-pane] region when split=false", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: base });
    expect(document.querySelectorAll("[data-pane]").length).toBe(1);
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/lib/Stage.test.ts` → FAIL (file missing)

**Implementation (`frontend/src/lib/Stage.svelte`):**

```svelte
<script lang="ts">
  import type { View } from "./stores/layout.svelte";

  let {
    view, split, onView, onSplit,
  }: { view: View; split: boolean; onView: (v: View) => void; onSplit: () => void } = $props();

  const segs: { id: View; label: string }[] = [
    { id: "agent", label: "Agent" },
    { id: "code",  label: "Code"  },
    { id: "diff",  label: "Diff"  },
  ];
</script>

<div class="stage">
  <header class="stage-bar">
    <nav aria-label="View">
      {#each segs as s}
        <button aria-selected={view === s.id} onclick={() => onView(s.id)}>{s.label}</button>
      {/each}
    </nav>
    <button onclick={onSplit} aria-label="Split pane">Split</button>
  </header>

  <div class="stage-content" class:split>
    <div data-pane="primary"   class="pane"><slot name="primary"   /></div>
    {#if split}
    <div data-pane="secondary" class="pane"><slot name="secondary" /></div>
    {/if}
  </div>
</div>

<style>
  .stage         { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .stage-bar     { display: flex; align-items: center; gap: 4px; padding: 0 8px;
                   background: var(--perch-surface); border-bottom: 1px solid var(--perch-border); }
  .stage-content { display: flex; flex: 1; min-height: 0; }
  .pane          { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
</style>
```

**Run-passes:** `npm --prefix frontend test -- src/lib/Stage.test.ts`

**Commit:**
```bash
git add frontend/src/lib/Stage.svelte frontend/src/lib/Stage.test.ts
git commit -m "feat(Stage): view-switcher (Agent/Code/Diff) + split-pane layout"
```

---

### Task 4.10: App.svelte skeleton (three zones, NORMAL keymap, layout store wiring)

**Files:**
- `frontend/src/App.svelte` (full replacement)
- `frontend/src/App.test.ts`
- `frontend/src/lib/ShellDrawer.svelte` (Phase 4a stub — real impl in Phase 4b)

**Mocking note:** Svelte 5 components are functions; mocking them as plain objects causes mount errors. Only `Sidebar.svelte` needs stubbing here (it imports removed wails exports). Stub it with a real minimal component. `ThemeProvider`, `Stage`, and `ShellDrawer` render without external deps and are not mocked.

**Failing test:**

```ts
// frontend/src/App.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { vi, describe, it, expect } from "vitest";

// Sidebar imports old wails exports — stub with an inert component.
vi.mock("./lib/Sidebar.svelte", () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

vi.mock("./lib/stores/layout.svelte", () => ({
  layout: {
    sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {},
    restore:     vi.fn(async () => {}),
    setView:     vi.fn(),
    toggleSplit: vi.fn(),
    setSidebarW: vi.fn(),
    setShellH:   vi.fn(),
  },
}));
vi.mock("./lib/stores/mode.svelte", () => ({
  mode: { current: "normal", enterTerminal: vi.fn(), enterCommand: vi.fn(), leaveCommand: vi.fn() },
}));
vi.mock("./lib/stores/settings.svelte", () => ({
  settings: { theme: "gruvbox", density: "dense", load: vi.fn(async () => {}) },
}));

describe("App.svelte skeleton", () => {
  it("renders sidebar, stage, and shell-drawer zones", async () => {
    const { default: App } = await import("./App.svelte");
    render(App);
    expect(document.querySelector("[data-zone='sidebar']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='stage']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='shell-drawer']")).toBeInTheDocument();
  });
  it("pressing '1' in NORMAL calls layout.setView('agent')", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "1" });
    expect(layout.setView).toHaveBeenCalledWith("agent");
  });
  it("pressing '2' in NORMAL calls layout.setView('code')", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "2" });
    expect(layout.setView).toHaveBeenCalledWith("code");
  });
  it("pressing '\\' in NORMAL calls layout.toggleSplit", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "\\" });
    expect(layout.toggleSplit).toHaveBeenCalled();
  });
  it("dragging sidebar divider calls layout.setSidebarW", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    const divider = document.querySelector(".divider-v")!;
    await fireEvent.mouseDown(divider, { clientX: 240 });
    await fireEvent.mouseMove(window,  { clientX: 280 });
    await fireEvent.mouseUp(window);
    expect(layout.setSidebarW).toHaveBeenCalled();
  });
});
```

**Run-fails:** `npm --prefix frontend test -- src/App.test.ts` → FAIL (App.svelte has old structure; ShellDrawer + Empty stub missing)

**ShellDrawer stub (`frontend/src/lib/ShellDrawer.svelte`):**

```svelte
<!-- Phase 4a stub — real ShellDrawer implemented in Phase 4b -->
<script lang="ts">
  let { paneId = "shell", cwd = "/" }: { paneId?: string; cwd?: string } = $props();
</script>
<div class="shell-drawer-stub" data-paneid={paneId} aria-label="Shell drawer"></div>
```

**Empty stub (`frontend/src/lib/__stubs__/Empty.svelte`):**

```svelte
<!-- Minimal inert component for use in tests -->
<script lang="ts">
  let { ..._ }: Record<string, unknown> = $props();
</script>
```

**Implementation (`frontend/src/App.svelte` — full replacement):**

```svelte
<script lang="ts">
  import { onMount } from "svelte";
  import ThemeProvider from "./lib/ThemeProvider.svelte";
  import Sidebar       from "./lib/Sidebar.svelte";
  import Stage         from "./lib/Stage.svelte";
  import ShellDrawer   from "./lib/ShellDrawer.svelte";
  import { layout }   from "./lib/stores/layout.svelte";
  import { mode }     from "./lib/stores/mode.svelte";
  import { settings } from "./lib/stores/settings.svelte";

  onMount(async () => { await Promise.all([settings.load(), layout.restore()]); });

  function onKeyDown(e: KeyboardEvent) {
    if (mode.current !== "normal") return;
    switch (e.key) {
      case "1": e.preventDefault(); layout.setView("agent"); break;
      case "2": e.preventDefault(); layout.setView("code");  break;
      case "3": e.preventDefault(); layout.setView("diff");  break;
      case "\\": e.preventDefault(); layout.toggleSplit();   break;
      case "i": e.preventDefault(); mode.enterTerminal();    break;
      case ":": e.preventDefault(); mode.enterCommand();     break;
    }
  }

  function startResizeSidebar(e: MouseEvent) {
    const startX = e.clientX, startW = layout.sidebarW;
    function onMove(mv: MouseEvent) { layout.setSidebarW(Math.max(160, startW + mv.clientX - startX)); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  function startResizeShell(e: MouseEvent) {
    const startY = e.clientY, startH = layout.shellH;
    function onMove(mv: MouseEvent) { layout.setShellH(Math.max(80, startH - (mv.clientY - startY))); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }
</script>

<svelte:window onkeydown={onKeyDown} />

<ThemeProvider theme={settings.theme} density={settings.density}>
  <div class="app-root">
    <aside data-zone="sidebar" class="sidebar-zone" style:width="{layout.sidebarW}px">
      <Sidebar />
    </aside>

    <div class="divider divider-v" role="separator" aria-label="Resize sidebar"
         onmousedown={startResizeSidebar}></div>

    <div class="center-column">
      <div data-zone="stage" class="stage-zone">
        <Stage view={layout.view} split={layout.split}
               onView={(v) => layout.setView(v)}
               onSplit={() => layout.toggleSplit()} />
      </div>

      <div class="divider divider-h" role="separator" aria-label="Resize shell drawer"
           onmousedown={startResizeShell}></div>

      <div data-zone="shell-drawer" class="shell-drawer-zone" style:height="{layout.shellH}px">
        <ShellDrawer />
      </div>
    </div>
  </div>
</ThemeProvider>

<style>
  .app-root         { display: flex; height: 100vh; overflow: hidden;
                      background: var(--perch-bg); color: var(--perch-text);
                      font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .sidebar-zone     { flex-shrink: 0; overflow: hidden; border-right: 1px solid var(--perch-border); }
  .divider-v        { width: 4px; cursor: col-resize; background: var(--perch-border); flex-shrink: 0; }
  .divider-h        { height: 4px; cursor: row-resize; background: var(--perch-border); }
  .center-column    { display: flex; flex-direction: column; flex: 1; min-width: 0; }
  .stage-zone       { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .shell-drawer-zone { flex-shrink: 0; overflow: hidden; border-top: 1px solid var(--perch-border); }
</style>
```

**Run-passes:** `npm --prefix frontend test -- src/App.test.ts`

**Commit:**
```bash
git add frontend/src/App.svelte frontend/src/App.test.ts \
        frontend/src/lib/ShellDrawer.svelte \
        frontend/src/lib/__stubs__/Empty.svelte
git commit -m "feat(App): three-zone skeleton, NORMAL keymap routing, sidebar divider drag"
```

---

## Phase 4 — Frontend (features)

> **Branch:** `feat/perch-v1`
> **Prereqs:** Phase 4a (tokens, ThemeProvider, Terminal, Stage, layout/mode stores) green.
> **Run commands:**
> - All tests: `npm --prefix frontend test`
> - Single file: `npm --prefix frontend test -- src/lib/<File>.test.ts`
> - Type-check: `npm --prefix frontend run check`

---

### Task 4.11: CodeMirror 6 dependencies

**Files:** `frontend/package.json`, `frontend/src/lib/Editor.smoke.test.ts`

- [ ] **Step 1: Verify latest stable versions from npm registry**

```bash
npm view codemirror version
npm view @codemirror/state version
npm view @codemirror/view version
npm view @codemirror/commands version
npm view @codemirror/language version
npm view @codemirror/lang-javascript version
npm view @codemirror/lang-css version
npm view @codemirror/lang-html version
npm view @codemirror/lang-json version
npm view @codemirror/lang-markdown version
npm view @codemirror/lang-python version
npm view @codemirror/lang-go version
npm view @codemirror/theme-one-dark version
# Record each; use only these in the install below.
```

- [ ] **Step 2: Install with exact versions (replace X.Y.Z with registry output)**

```bash
npm --prefix frontend install --save-exact \
  codemirror@X.Y.Z \
  @codemirror/state@X.Y.Z \
  @codemirror/view@X.Y.Z \
  @codemirror/commands@X.Y.Z \
  @codemirror/language@X.Y.Z \
  @codemirror/lang-javascript@X.Y.Z \
  @codemirror/lang-css@X.Y.Z \
  @codemirror/lang-html@X.Y.Z \
  @codemirror/lang-json@X.Y.Z \
  @codemirror/lang-markdown@X.Y.Z \
  @codemirror/lang-python@X.Y.Z \
  @codemirror/lang-go@X.Y.Z \
  @codemirror/theme-one-dark@X.Y.Z
```

- [ ] **Step 3: Write smoke test (fails until packages installed)**

```ts
// frontend/src/lib/Editor.smoke.test.ts
import { describe, it, expect } from "vitest";

describe("CodeMirror smoke", () => {
  it("EditorView can be constructed in jsdom", async () => {
    const { EditorView } = await import("@codemirror/view");
    const { EditorState } = await import("@codemirror/state");
    const div = document.createElement("div");
    document.body.appendChild(div);
    const view = new EditorView({
      state: EditorState.create({ doc: "hello" }),
      parent: div,
    });
    expect(view.state.doc.toString()).toBe("hello");
    view.destroy();
    document.body.removeChild(div);
  });
});
```

- [ ] **Step 4: Run failing** — `npm --prefix frontend test -- src/lib/Editor.smoke.test.ts` → FAIL: module not found

- [ ] **Step 5: Run passing after install** → green

- [ ] **Step 6: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/src/lib/Editor.smoke.test.ts
git commit -m "feat(frontend): add CodeMirror 6 deps + smoke test"
```

---

### Task 4.12: `Editor.svelte` — load, edit, save

**Files:** `frontend/src/lib/wails.ts` (extend), `frontend/src/lib/Editor.svelte`, `frontend/src/lib/Editor.test.ts`

- [ ] **Step 1: Extend `wails.ts`** — add before the export block:

```ts
// Types added to wails.ts (append to the App interface and add exports)

export interface Hunk {
  file: string; index: number; header: string;
  oldStart: number; oldLines: number; newStart: number; newLines: number;
  lines: HunkLine[];
}
export interface HunkLine { kind: "ctx" | "add" | "del"; text: string; }
export interface FileDiff { path: string; added: number; removed: number; status: string; }
export interface FSNode { name: string; path: string; isDir: boolean; }
export interface WorkspaceVM {
  id: string; worktreePath: string; agent: string; title: string; branch: string;
  state: string; caps: Caps; paneId: string; lastActive: string;
}
export interface Caps { approvals: boolean; attention: boolean; tokens: boolean; }
export interface ApprovalReq { reqId: string; tool: string; summary: string; }

// Extend the App interface with new bound methods:
// ReadFile(absPath: string): Promise<string>
// WriteFile(absPath: string, content: string): Promise<void>
// Hunks(worktree: string, file: string): Promise<Hunk[]>
// DiffStat(worktree: string): Promise<FileDiff[]>
// StageHunk(worktree: string, file: string, index: number): Promise<void>
// DiscardHunk(worktree: string, file: string, index: number): Promise<void>
// ListDir(absDir: string): Promise<FSNode[]>
// RevealInFiles(absPath: string): Promise<void>
// CopyPath(absPath: string): string
// OpenShell(paneId: string, cwd: string): Promise<void>
// Approve(reqId: string, decision: string): Promise<void>
// ListWorkspaces(): Promise<WorkspaceVM[]>
// CreateWorkspace(agent: string, repo: string, branch: string, model: string): Promise<WorkspaceVM>

export const readFile    = (p: string) => app().ReadFile(p);
export const writeFile   = (p: string, c: string) => app().WriteFile(p, c);
export const hunks       = (wt: string, f: string) => app().Hunks(wt, f);
export const diffStat    = (wt: string) => app().DiffStat(wt);
export const stageHunk   = (wt: string, file: string, index: number) => app().StageHunk(wt, file, index);
export const discardHunk = (wt: string, file: string, index: number) => app().DiscardHunk(wt, file, index);
export const listDir     = (d: string) => app().ListDir(d);
export const revealInFiles = (p: string) => app().RevealInFiles(p);
export const copyPath    = (p: string) => app().CopyPath(p);
export const openShell   = (paneId: string, cwd: string) => app().OpenShell(paneId, cwd);
export const approve     = (reqId: string, d: string) => app().Approve(reqId, d);
export const listWorkspaces = () => app().ListWorkspaces();
export const createWorkspace = (agent: string, repo: string, branch: string, model: string) =>
  app().CreateWorkspace(agent, repo, branch, model);

export function onAgentEvent(cb: (e: any) => void): () => void {
  return window.runtime.EventsOn("agent:event", cb);
}
export function onNotify(cb: (e: any) => void): () => void {
  return window.runtime.EventsOn("notify", cb);
}
```

- [ ] **Step 2: Write failing test**

```ts
// frontend/src/lib/Editor.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  readFile: vi.fn(async () => "initial content"),
  writeFile: vi.fn(async () => {}),
  hunks: vi.fn(async () => []),
}));

test("loads file content on mount", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(screen.getByRole("region", { name: "editor" })).toBeInTheDocument());
  const w = await import("./wails");
  expect(w.readFile).toHaveBeenCalledWith("/wt/src/main.go");
});

test("saves via Ctrl-S", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const w = await import("./wails");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(w.readFile).toHaveBeenCalled());
  await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
  await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/src/main.go", expect.any(String)));
});

test("renders nothing when path is null", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: null, worktree: "/wt" } });
  expect(screen.queryByRole("region", { name: "editor" })).toBeNull();
});
```

- [ ] **Step 3: Run failing** — `npm --prefix frontend test -- src/lib/Editor.test.ts` → FAIL

- [ ] **Step 4: Implement**

```svelte
<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect } from "@codemirror/state";
  import { defaultKeymap, indentWithTab } from "@codemirror/commands";
  import { bracketMatching } from "@codemirror/language";
  import { javascript } from "@codemirror/lang-javascript";
  import { readFile, writeFile, hunks as fetchHunks, type Hunk } from "./wails";

  let { path, worktree }: { path: string | null; worktree: string } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

  // Git gutter
  const setChangedLines = StateEffect.define<Set<number>>();
  const changedLinesField = StateField.define<Set<number>>({
    create: () => new Set(),
    update(val, tr) {
      for (const e of tr.effects) if (e.is(setChangedLines)) return e.value;
      return val;
    },
  });
  class ChangedMarker extends GutterMarker {
    toDOM() {
      const el = document.createElement("div");
      el.className = "cm-gutterElement perch-changed";
      el.textContent = "▎";
      return el;
    }
  }
  const changedMarker = new ChangedMarker();
  const changedGutter = gutter({
    class: "perch-git-gutter",
    lineMarker(v, line) {
      const no = v.state.doc.lineAt(line.from).number;
      return v.state.field(changedLinesField).has(no) ? changedMarker : null;
    },
  });

  async function load(p: string) {
    const [content, hunkList] = await Promise.all([
      readFile(p),
      fetchHunks(worktree, p).catch(() => [] as Hunk[]),
    ]);
    const changed = new Set<number>();
    for (const h of hunkList)
      for (let i = h.newStart; i < h.newStart + h.newLines; i++) changed.add(i);

    const state = EditorState.create({
      doc: content,
      extensions: [
        changedLinesField,
        changedGutter,
        keymap.of([...defaultKeymap, indentWithTab]),
        bracketMatching(),
        javascript(),
        EditorView.lineWrapping,
      ],
    });
    if (view) {
      view.setState(state);
    } else if (container) {
      view = new EditorView({ state, parent: container });
    }
    if (changed.size > 0) view?.dispatch({ effects: setChangedLines.of(changed) });
  }

  async function save() {
    if (!path || !view) return;
    await writeFile(path, view.state.doc.toString());
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(); }
  }

  $effect(() => { if (path) load(path); });

  onMount(() => document.addEventListener("keydown", handleKeyDown));
  onDestroy(() => {
    document.removeEventListener("keydown", handleKeyDown);
    view?.destroy();
    view = null;
  });
</script>

{#if path}
  <section aria-label="editor" class="editor-wrap">
    <div bind:this={container} class="cm-host"></div>
  </section>
{/if}
```

- [ ] **Step 5: Run passing** → green

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/wails.ts frontend/src/lib/Editor.svelte frontend/src/lib/Editor.test.ts frontend/src/lib/Editor.smoke.test.ts
git commit -m "feat(frontend): Editor.svelte — CodeMirror 6 load/edit/save + git gutter (Tasks 4.12–4.13)"
```

---

> **Amendment (jsdom reality):** CodeMirror does not lay out per-line `.cm-gutterElement` nodes in jsdom (zero-size viewport), so the plan's `.cm-gutterElement.perch-changed` assertion is infeasible. Fixed in `43e6921`: the changed-line computation was extracted to `frontend/src/lib/gutter.ts` (`changedLinesFromHunks`) and unit-tested in `gutter.test.ts` (deterministic); the Editor DOM test now asserts the gutter *container* `.perch-git-gutter` is mounted. Also note: Task 4.12 "Step 1: Extend wails.ts" was SKIPPED — 4.7 already provides `readFile`/`writeFile`/`hunks`/`Hunk`, and the plan's `CopyPath` has no app.go bound method.

### Task 4.13: `Editor.svelte` git gutter

**Files:** `frontend/src/lib/Editor.test.ts` (extend)

The gutter implementation is already included in Task 4.12's `Editor.svelte` (the `changedGutter`, `changedLinesField`, and `setChangedLines` effect). This task adds the targeted test and verifies the gutter path.

- [ ] **Step 1: Add failing test** (append to `Editor.test.ts`)

```ts
test("git gutter markers render for a hunk", async () => {
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("line1\nline2\nline3\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([{
    file: "/wt/src/main.go", index: 0, header: "@@ -1,1 +1,2 @@",
    oldStart: 1, oldLines: 1, newStart: 1, newLines: 2,
    lines: [{ kind: "add", text: "line1a" }, { kind: "ctx", text: "line2" }],
  }]);
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() =>
    expect(document.querySelector(".cm-gutterElement.perch-changed")).not.toBeNull()
  );
});
```

- [ ] **Step 2: Run failing** — `npm --prefix frontend test -- src/lib/Editor.test.ts` → FAIL (gutter element absent before impl)

- [ ] **Step 3: Run passing** (impl already present from 4.12) → green

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/Editor.test.ts
git commit -m "test(frontend): Editor git gutter test (Task 4.13)"
```

---

### Task 4.14: `DiffView.svelte`

**Files:** `frontend/src/lib/DiffView.svelte`, `frontend/src/lib/DiffView.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/DiffView.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const fakeStat = [
  { path: "src/main.go", added: 3, removed: 1, status: "M" },
  { path: "README.md",   added: 10, removed: 0, status: "A" },
];
const fakeHunks = [{
  file: "src/main.go", index: 0, header: "@@ -1,3 +1,4 @@",
  oldStart: 1, oldLines: 3, newStart: 1, newLines: 4,
  lines: [{ kind: "ctx", text: "package main" }, { kind: "add", text: `import "fmt"` }],
}];

vi.mock("./wails", () => ({
  diffStat:    vi.fn(async () => fakeStat),
  hunks:       vi.fn(async () => fakeHunks),
  stageHunk:   vi.fn(async () => {}),
  discardHunk: vi.fn(async () => {}),
}));

test("renders file list with status icon+label and +/- counts", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => expect(screen.getByText("src/main.go")).toBeInTheDocument());
  expect(screen.getByText("+3")).toBeInTheDocument();
  expect(screen.getByText("-1")).toBeInTheDocument();
  expect(screen.getByText(/modified/i)).toBeInTheDocument();
  expect(screen.getByText(/added/i)).toBeInTheDocument();
});

test("Stage button calls stageHunk", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  const w = await import("./wails");
  await waitFor(() => screen.getByRole("button", { name: /stage/i }));
  await fireEvent.click(screen.getByRole("button", { name: /stage/i }));
  await waitFor(() => expect(w.stageHunk).toHaveBeenCalledWith("/wt", "src/main.go", 0));
});
```

- [ ] **Step 2: Run failing** — `npm --prefix frontend test -- src/lib/DiffView.test.ts` → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/DiffView.svelte -->
<script lang="ts">
  import { diffStat, hunks as fetchHunks, stageHunk, discardHunk, type FileDiff, type Hunk } from "./wails";

  let { worktree }: { worktree: string } = $props();

  const STATUS_LABELS: Record<string, { icon: string; label: string }> = {
    M: { icon: "✎", label: "modified" },
    A: { icon: "+", label: "added" },
    D: { icon: "−", label: "deleted" },
    R: { icon: "→", label: "renamed" },
    "?": { icon: "?", label: "untracked" },
  };

  let files    = $state<FileDiff[]>([]);
  let expanded = $state<Record<string, Hunk[]>>({});
  let loading  = $state(false);

  $effect(() => {
    const wt = worktree;
    loading = true;
    diffStat(wt).then((r) => { files = r; loading = false; }).catch(() => { loading = false; });
  });

  async function toggleFile(f: FileDiff) {
    if (expanded[f.path]) {
      const next = { ...expanded }; delete next[f.path]; expanded = next;
    } else {
      expanded = { ...expanded, [f.path]: await fetchHunks(worktree, f.path) };
    }
  }

  async function stage(h: Hunk) {
    await stageHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
  }

  async function discard(h: Hunk) {
    await discardHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
  }
</script>

<section aria-label="diff view" class="diff-view">
  {#if loading}
    <p class="dim">Loading…</p>
  {:else if files.length === 0}
    <p class="dim">No changes</p>
  {:else}
    <ul class="file-list">
      {#each files as f (f.path)}
        {@const st = STATUS_LABELS[f.status] ?? { icon: "?", label: f.status }}
        <li class="file-row">
          <button class="file-toggle" aria-expanded={!!expanded[f.path]}
            onclick={() => toggleFile(f)} aria-label={f.path}>
            <span aria-hidden="true">{st.icon}</span>
            <span class="file-path">{f.path}</span>
            <span class="stat-label">{st.label}</span>
            <span class="add">+{f.added}</span>
            <span class="del">-{f.removed}</span>
          </button>
          {#if expanded[f.path]}
            <div class="hunk-list">
              {#each expanded[f.path] as h (h.index)}
                <div class="hunk">
                  <pre class="hunk-header">{h.header}</pre>
                  <pre class="hunk-body">{#each h.lines as l}<span class="line line-{l.kind}">{l.text}{"\n"}</span>{/each}</pre>
                  <div class="hunk-actions">
                    <button onclick={() => stage(h)}>Stage hunk</button>
                    <button onclick={() => discard(h)}>Discard hunk</button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/DiffView.svelte frontend/src/lib/DiffView.test.ts
git commit -m "feat(frontend): DiffView.svelte — stat list + per-hunk stage/discard"
```

---

### Task 4.15: `FileTree.svelte`

**Files:** `frontend/src/lib/FileTree.svelte`, `frontend/src/lib/FileTree.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/FileTree.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  listDir: vi.fn(async (path: string) => {
    if (path === "/wt") return [
      { name: "src",       path: "/wt/src",         isDir: true  },
      { name: "README.md", path: "/wt/README.md",   isDir: false },
    ];
    if (path === "/wt/src") return [{ name: "main.go", path: "/wt/src/main.go", isDir: false }];
    return [];
  }),
  revealInFiles: vi.fn(async () => {}),
  copyPath: vi.fn((p: string) => p),
}));

test("renders root entries on mount", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => expect(screen.getByText("src")).toBeInTheDocument());
  expect(screen.getByText("README.md")).toBeInTheDocument();
});

test("expand dir fetches children", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const w = await import("./wails");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("src"));
  await fireEvent.click(screen.getByRole("button", { name: /src/ }));
  await waitFor(() => expect(w.listDir).toHaveBeenCalledWith("/wt/src"));
  expect(screen.getByText("main.go")).toBeInTheDocument();
});

test("action menu Open fires onOpen", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  let opened = "";
  render(FileTree, { props: { root: "/wt", onOpen: (p: string) => (opened = p) } });
  await waitFor(() => screen.getByText("README.md"));
  await fireEvent.contextMenu(screen.getByText("README.md"));
  await waitFor(() => screen.getByRole("menuitem", { name: /open/i }));
  await fireEvent.click(screen.getByRole("menuitem", { name: /open/i }));
  expect(opened).toBe("/wt/README.md");
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/FileTree.svelte -->
<script lang="ts">
  import { listDir, revealInFiles, copyPath, type FSNode } from "./wails";

  let { root, onOpen }: { root: string; onOpen: (path: string) => void } = $props();

  type TreeNode = FSNode & { children?: TreeNode[]; expanded?: boolean };

  let nodes = $state<TreeNode[]>([]);
  let menu  = $state<{ node: TreeNode; x: number; y: number } | null>(null);

  $effect(() => { listDir(root).then((ns) => { nodes = ns.map((n) => ({ ...n })); }); });

  async function toggle(node: TreeNode) {
    if (!node.isDir) return;
    if (node.expanded) { node.expanded = false; node.children = undefined; }
    else { node.children = (await listDir(node.path)).map((c) => ({ ...c })); node.expanded = true; }
    nodes = [...nodes];
  }

  function openMenu(e: MouseEvent, node: TreeNode) { e.preventDefault(); menu = { node, x: e.clientX, y: e.clientY }; }
  function closeMenu() { menu = null; }
  function menuOpen()   { if (!menu) return; onOpen(menu.node.path); closeMenu(); }
  function menuReveal() { if (!menu) return; revealInFiles(menu.node.path); closeMenu(); }
  function menuCopy()   { if (!menu) return; const p = copyPath(menu.node.path); navigator.clipboard?.writeText(p).catch(() => {}); closeMenu(); }
  function menuSend()   { if (!menu) return; onOpen(`@mention:${menu.node.path}`); closeMenu(); }
</script>

<svelte:window onclick={closeMenu} />

<nav aria-label="file tree" class="file-tree">
  {#snippet nodeList(items: TreeNode[])}
    <ul>
      {#each items as node (node.path)}
        <li>
          <button
            class="tree-node {node.isDir ? 'is-dir' : 'is-file'}"
            aria-expanded={node.isDir ? node.expanded ?? false : undefined}
            onclick={() => node.isDir ? toggle(node) : onOpen(node.path)}
            oncontextmenu={(e) => openMenu(e, node)}
          >
            <span aria-hidden="true">{node.isDir ? (node.expanded ? "▾" : "▸") : "·"}</span>
            <span class="node-name">{node.name}</span>
          </button>
          {#if node.expanded && node.children}{@render nodeList(node.children)}{/if}
        </li>
      {/each}
    </ul>
  {/snippet}
  {@render nodeList(nodes)}
</nav>

{#if menu}
  <ul role="menu" class="context-menu" style="position:fixed;left:{menu.x}px;top:{menu.y}px">
    <li role="menuitem" tabindex="0" onclick={menuOpen}>Open</li>
    <li role="menuitem" tabindex="0" onclick={menuReveal}>Reveal in Files</li>
    <li role="menuitem" tabindex="0" onclick={menuCopy}>Copy path</li>
    <li role="menuitem" tabindex="0" onclick={menuSend}>Send to agent</li>
  </ul>
{/if}
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/FileTree.svelte frontend/src/lib/FileTree.test.ts
git commit -m "feat(frontend): FileTree.svelte — lazy ListDir + action menu"
```

---

### Task 4.16: `ShellDrawer.svelte`

**Files:** `frontend/src/lib/ShellDrawer.svelte`, `frontend/src/lib/ShellDrawer.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/ShellDrawer.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({ openShell: vi.fn(async () => {}) }));
vi.mock("./Terminal.svelte", () => ({ default: { render() {} } }));

test("calls openShell on mount", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt" } });
  const w = await import("./wails");
  await waitFor(() => expect(w.openShell).toHaveBeenCalledWith("shell-1", "/wt"));
});

test("collapse toggle hides the shell region", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt" } });
  await waitFor(() => screen.getByRole("button", { name: /collapse/i }));
  expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument();
  await fireEvent.click(screen.getByRole("button", { name: /collapse/i }));
  expect(screen.queryByRole("region", { name: /shell/i })).toBeNull();
  await fireEvent.click(screen.getByRole("button", { name: /expand/i }));
  await waitFor(() => expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument());
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/ShellDrawer.svelte -->
<script lang="ts">
  import { onMount } from "svelte";
  import Terminal from "./Terminal.svelte";
  import { openShell } from "./wails";

  let { paneId, cwd }: { paneId: string; cwd: string } = $props();
  let collapsed = $state(false);

  onMount(() => { openShell(paneId, cwd); });
</script>

<div class="shell-drawer" class:collapsed>
  <div class="shell-header">
    <span class="shell-title">Shell — {cwd}</span>
    {#if collapsed}
      <button onclick={() => (collapsed = false)} aria-label="expand shell">▲ Expand</button>
    {:else}
      <button onclick={() => (collapsed = true)} aria-label="collapse shell">▼ Collapse</button>
    {/if}
  </div>
  {#if !collapsed}
    <section aria-label="shell" class="shell-body">
      <Terminal {paneId} {cwd} />
    </section>
  {/if}
</div>
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/ShellDrawer.svelte frontend/src/lib/ShellDrawer.test.ts
git commit -m "feat(frontend): ShellDrawer.svelte — pinned collapsible shell pane"
```

---

### Task 4.17: `Sidebar.svelte` rewrite

**Files:** `frontend/src/lib/Sidebar.svelte` (rewrite), `frontend/src/lib/Sidebar.test.ts` (rewrite)

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/Sidebar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const workspaces = [
  { id: "ws_a", worktreePath: "/wt/a", agent: "claude", title: "feat-auth",
    branch: "feat/auth", state: "running", caps: {}, paneId: "p1", lastActive: "" },
  { id: "ws_b", worktreePath: "/wt/b", agent: "claude", title: "feat-core",
    branch: "feat/core", state: "idle", caps: {}, paneId: "p2", lastActive: "" },
  { id: "ws_c", worktreePath: "/wt/c", agent: "claude", title: "bug-fix",
    branch: "fix/crash", state: "awaiting-approval", caps: {}, paneId: "p3", lastActive: "" },
  { id: "ws_d", worktreePath: "/wt/d", agent: "claude", title: "done-work",
    branch: "feat/done", state: "done", caps: {}, paneId: "p4", lastActive: "" },
  { id: "ws_e", worktreePath: "/wt/e", agent: "claude", title: "errored-work",
    branch: "feat/err", state: "errored", caps: {}, paneId: "p5", lastActive: "" },
];

test("renders status icon+label for all states", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  expect(screen.getByText(/◐/)).toBeInTheDocument();
  expect(screen.getByText(/running/i)).toBeInTheDocument();
  expect(screen.getByText(/◯/)).toBeInTheDocument();
  expect(screen.getByText(/idle/i)).toBeInTheDocument();
  expect(screen.getByText(/⚠/)).toBeInTheDocument();
  expect(screen.getByText(/needs you/i)).toBeInTheDocument();
  expect(screen.getByText(/✓/)).toBeInTheDocument();
  expect(screen.getByText(/✗/)).toBeInTheDocument();
});

test("clicking a workspace calls onSelect", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onSelect = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-core"));
  await fireEvent.click(screen.getByRole("button", { name: /feat-core/ }));
  expect(onSelect).toHaveBeenCalledWith("ws_b");
});

test("New session button calls onNew", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onNew = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew } });
  await fireEvent.click(screen.getByRole("button", { name: /new session/i }));
  expect(onNew).toHaveBeenCalled();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/Sidebar.svelte -->
<script lang="ts">
  import type { WorkspaceVM } from "./wails";

  let {
    workspaces, activeId, onSelect, onNew,
  }: {
    workspaces: WorkspaceVM[];
    activeId: string;
    onSelect: (id: string) => void;
    onNew: () => void;
  } = $props();

  const STATUS = {
    running:             { icon: "◐", label: "running" },
    idle:                { icon: "◯", label: "idle" },
    "awaiting-approval": { icon: "⚠", label: "needs you" },
    done:                { icon: "✓", label: "done" },
    errored:             { icon: "✗", label: "error" },
  } as const;
</script>

<nav aria-label="sessions" class="sidebar">
  <ul class="workspace-list">
    {#each workspaces as ws (ws.id)}
      {@const st = STATUS[ws.state as keyof typeof STATUS] ?? { icon: "?", label: ws.state }}
      <li class:active={ws.id === activeId}>
        <button class="workspace-row"
          aria-current={ws.id === activeId ? "page" : undefined}
          onclick={() => onSelect(ws.id)}
          aria-label={ws.title}
        >
          <span class="status-icon" aria-hidden="true">{st.icon}</span>
          <span class="workspace-title">{ws.title}</span>
          <span class="workspace-branch dim">{ws.branch}</span>
          <span class="status-label">{st.label}</span>
        </button>
      </li>
    {/each}
  </ul>
  <button class="new-session-cta" onclick={onNew} aria-label="New session">+ New session</button>
</nav>
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/Sidebar.svelte frontend/src/lib/Sidebar.test.ts
git commit -m "feat(frontend): Sidebar.svelte rewrite — WorkspaceVM + frozen status icons"
```

---

### Task 4.18: `MenuBar.svelte`

**Files:** `frontend/src/lib/MenuBar.svelte`, `frontend/src/lib/MenuBar.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/MenuBar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("menu item click invokes onCommand", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  const onCommand = vi.fn();
  render(MenuBar, { props: { onCommand, unreadCount: 0 } });
  await fireEvent.click(screen.getByRole("button", { name: /session/i }));
  await waitFor(() => screen.getByRole("menuitem", { name: /new session/i }));
  await fireEvent.click(screen.getByRole("menuitem", { name: /new session/i }));
  expect(onCommand).toHaveBeenCalledWith("session:new");
});

test("bell shows unread badge count", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 4 } });
  expect(screen.getByText("4")).toBeInTheDocument();
});

test("bell with 0 unread shows no badge", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  expect(screen.queryByText("0")).toBeNull();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/MenuBar.svelte -->
<script lang="ts">
  let { onCommand, unreadCount = 0 }: { onCommand: (id: string) => void; unreadCount?: number } = $props();

  type MenuItem = { id: string; label: string };
  const menus: { label: string; items: MenuItem[] }[] = [
    { label: "Session", items: [
      { id: "session:new",    label: "New session" },
      { id: "session:close",  label: "Close session" },
      { id: "session:remove", label: "Remove session" },
    ]},
    { label: "Worktree", items: [
      { id: "worktree:open",   label: "Open worktree" },
      { id: "worktree:reveal", label: "Reveal in Files" },
    ]},
    { label: "View", items: [
      { id: "view:agent", label: "Agent view" },
      { id: "view:code",  label: "Code view" },
      { id: "view:diff",  label: "Diff view" },
      { id: "view:split", label: "Split" },
      { id: "view:theme", label: "Theme…" },
    ]},
    { label: "Agent", items: [
      { id: "agent:approve-all", label: "Approve all pending" },
      { id: "agent:deny-all",    label: "Deny all pending" },
    ]},
    { label: "Help", items: [
      { id: "help:shortcuts", label: "Keyboard shortcuts" },
      { id: "help:about",     label: "About perch" },
    ]},
  ];

  let openMenu = $state<string | null>(null);
  function toggleMenu(label: string) { openMenu = openMenu === label ? null : label; }
  function runItem(id: string) { onCommand(id); openMenu = null; }
</script>

<svelte:window onclick={() => (openMenu = null)} />

<header class="menubar" role="menubar">
  {#each menus as m}
    <div class="menu-root">
      <button role="menuitem" aria-haspopup="menu" aria-expanded={openMenu === m.label}
        onclick={(e) => { e.stopPropagation(); toggleMenu(m.label); }}>{m.label}</button>
      {#if openMenu === m.label}
        <ul role="menu" class="dropdown" onclick={(e) => e.stopPropagation()}>
          {#each m.items as item}
            <li role="menuitem" tabindex="0"
              onclick={() => runItem(item.id)}
              onkeydown={(e) => e.key === "Enter" && runItem(item.id)}
            >{item.label}</li>
          {/each}
        </ul>
      {/if}
    </div>
  {/each}
  <button class="bell" aria-label="notifications"
    onclick={(e) => { e.stopPropagation(); onCommand("notifications:open"); }}>
    🔔{#if unreadCount > 0}<span class="badge">{unreadCount}</span>{/if}
  </button>
</header>
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/MenuBar.svelte frontend/src/lib/MenuBar.test.ts
git commit -m "feat(frontend): MenuBar.svelte — menus + notification bell"
```

---

### Task 4.19: `CommandPalette.svelte` rewrite

**Files:** `frontend/src/lib/CommandPalette.svelte` (rewrite), `frontend/src/lib/CommandPalette.test.ts` (rewrite)

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/CommandPalette.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const commands = [
  { id: "agent:new",  group: "Agent", label: "New agent session",  keybinding: "Ctrl-N" },
  { id: "agent:kill", group: "Agent", label: "Kill agent",          keybinding: "" },
  { id: "file:open",  group: "File",  label: "Open file",           keybinding: "Ctrl-O" },
  { id: "view:split", group: "View",  label: "Split pane",          keybinding: "\\" },
  { id: "pane:focus", group: "Pane",  label: "Focus terminal pane", keybinding: "i" },
];

test("fuzzy match: 'kil' surfaces Kill agent first", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  await fireEvent.input(screen.getByRole("combobox"), { target: { value: "kil" } });
  const items = screen.getAllByRole("option");
  expect(items[0].textContent).toMatch(/kill agent/i);
});

test("Enter runs the top result and calls onRun", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onRun = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun, onClose: () => {} } });
  await fireEvent.input(screen.getByRole("combobox"), { target: { value: "new" } });
  await fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
  expect(onRun).toHaveBeenCalledWith("agent:new");
});

test("Esc calls onClose", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onClose = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose } });
  await fireEvent.keyDown(screen.getByRole("combobox"), { key: "Escape" });
  expect(onClose).toHaveBeenCalled();
});

test("prefix group labels and inline keybindings are shown", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  expect(screen.getByText("Agent:")).toBeInTheDocument();
  expect(screen.getByText("Ctrl-N")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/CommandPalette.svelte -->
<script lang="ts">
  type Command = { id: string; group: string; label: string; keybinding?: string };
  let {
    open, commands, onRun, onClose,
  }: { open: boolean; commands: Command[]; onRun: (id: string) => void; onClose: () => void } = $props();

  let query = $state("");

  function fuzzyScore(label: string, q: string): number {
    if (!q) return 1;
    const lbl = label.toLowerCase(); const ql = q.toLowerCase();
    let score = 0; let si = 0;
    for (const ch of ql) {
      const idx = lbl.indexOf(ch, si);
      if (idx === -1) return 0;
      score += idx === si ? 2 : 1; si = idx + 1;
    }
    return score;
  }

  let filtered = $derived(
    query
      ? commands.map((c) => ({ c, score: fuzzyScore(c.label, query) }))
          .filter((x) => x.score > 0).sort((a, b) => b.score - a.score).map((x) => x.c)
      : commands
  );

  let grouped = $derived(
    filtered.reduce<{ group: string; items: Command[] }[]>((acc, c) => {
      const last = acc[acc.length - 1];
      if (last && last.group === c.group) last.items.push(c);
      else acc.push({ group: c.group, items: [c] });
      return acc;
    }, [])
  );

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Enter" && filtered.length > 0) onRun(filtered[0].id);
    else if (e.key === "Escape") onClose();
  }
</script>

{#if open}
  <div role="dialog" aria-label="command palette" class="palette-overlay">
    <div class="palette">
      <input type="text" role="combobox" aria-autocomplete="list" aria-controls="palette-list"
        bind:value={query} onkeydown={handleKey} placeholder="Type a command… (⌘K)" autofocus />
      <ul id="palette-list" role="listbox" class="palette-list">
        {#each grouped as g}
          <li class="group-header" aria-hidden="true">{g.group}:</li>
          {#each g.items as c (c.id)}
            <li role="option" aria-selected="false" class="palette-item" onclick={() => onRun(c.id)}>
              <span class="item-label">{c.label}</span>
              {#if c.keybinding}<kbd class="item-kbd">{c.keybinding}</kbd>{/if}
            </li>
          {/each}
        {/each}
      </ul>
    </div>
  </div>
{/if}
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/CommandPalette.svelte frontend/src/lib/CommandPalette.test.ts
git commit -m "feat(frontend): CommandPalette rewrite — fuzzy, prefix groups, keybindings, onRun/onClose"
```

---

### Task 4.20: `stores/notifications.svelte.ts`

**Files:** `frontend/src/lib/stores/notifications.svelte.ts`, `frontend/src/lib/stores/notifications.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/stores/notifications.test.ts
import { describe, it, expect, beforeEach } from "vitest";

describe("notification store", () => {
  beforeEach(() => { vi.resetModules(); });

  it("addBlocking adds a tier-1 item", async () => {
    const { addBlocking, getItems } = await import("./notifications.svelte");
    addBlocking("ws_a", "Approval needed", "Claude wants to run bash");
    const items = getItems();
    expect(items[0].tier).toBe("blocking");
    expect(items[0].read).toBe(false);
  });

  it("DND mutes tier 2 and 3 but not tier 1", async () => {
    const { addBlocking, addAmbient, addRoutine, setDnd, getItems } =
      await import("./notifications.svelte");
    setDnd(true);
    addBlocking("ws_a", "Approval", "urgent");
    addAmbient("ws_b", "Done", "quiet");
    addRoutine("ws_c", "File", "bg");
    const items = getItems();
    expect(items.filter((i) => i.tier === "blocking")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "ambient")).toHaveLength(0);
    expect(items.filter((i) => i.tier === "routine")).toHaveLength(0);
  });

  it("markRead + clearRead remove read items", async () => {
    const { addBlocking, addAmbient, markRead, clearRead, getItems } =
      await import("./notifications.svelte");
    addBlocking("ws_a", "X1", "Y1");
    addAmbient("ws_a",  "X2", "Y2");
    const id = getItems()[1].id;   // oldest (blocking was second push)
    markRead(id);
    clearRead();
    expect(getItems().every((n) => !n.read)).toBe(true);
  });
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```ts
// frontend/src/lib/stores/notifications.svelte.ts
export type Tier = "blocking" | "ambient" | "routine";
export interface Notification {
  id: string; workspaceId: string; tier: Tier;
  title: string; body: string; read: boolean; ts: number;
}

let items = $state<Notification[]>([]);
let dnd   = $state(false);
let _seq  = 0;

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

function add(tier: Tier, workspaceId: string, title: string, body: string) {
  if (dnd && tier !== "blocking") return;
  items = [{ id: `notif-${++_seq}`, workspaceId, tier, title, body, read: false, ts: Date.now() }, ...items];
}

export function addBlocking(w: string, t: string, b: string) { add("blocking", w, t, b); }
export function addAmbient (w: string, t: string, b: string) { add("ambient",  w, t, b); }
export function addRoutine (w: string, t: string, b: string) { add("routine",  w, t, b); }

export function markRead(id: string) { items = items.map((n) => n.id === id ? { ...n, read: true } : n); }
export function clearRead()          { items = items.filter((n) => !n.read); }
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/stores/notifications.svelte.ts frontend/src/lib/stores/notifications.test.ts
git commit -m "feat(frontend): notifications store — tiers, DND, markRead, clearRead"
```

---

### Task 4.21: `NotificationHub.svelte`

**Files:** `frontend/src/lib/NotificationHub.svelte`, `frontend/src/lib/NotificationHub.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/NotificationHub.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const items = [
  { id: "n1", workspaceId: "ws_a", tier: "blocking" as const, title: "Approve bash", body: "run ls /tmp", read: false, ts: 1 },
  { id: "n2", workspaceId: "ws_b", tier: "ambient"  as const, title: "Turn done",    body: "finished",   read: false, ts: 2 },
];

test("filter approvals shows only blocking items", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });
  await fireEvent.click(screen.getByRole("button", { name: /approvals/i }));
  await waitFor(() => expect(screen.getByText("Approve bash")).toBeInTheDocument());
  expect(screen.queryByText("Turn done")).toBeNull();
});

test("dismiss button calls onDismiss with item id", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onDismiss = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss, onToggleDnd: () => {}, onClearRead: () => {} } });
  await waitFor(() => screen.getAllByRole("button", { name: /dismiss/i }));
  await fireEvent.click(screen.getAllByRole("button", { name: /dismiss/i })[0]);
  expect(onDismiss).toHaveBeenCalledWith("n1");
});

test("DND toggle button calls onToggleDnd", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onToggleDnd = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd, onClearRead: () => {} } });
  await fireEvent.click(screen.getByRole("button", { name: /do not disturb/i }));
  expect(onToggleDnd).toHaveBeenCalled();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/NotificationHub.svelte -->
<script lang="ts">
  import type { Notification, Tier } from "./stores/notifications.svelte";

  let {
    items, dnd, onDismiss, onToggleDnd, onClearRead,
  }: {
    items: Notification[];
    dnd: boolean;
    onDismiss: (id: string) => void;
    onToggleDnd: () => void;
    onClearRead: () => void;
  } = $props();

  type Filter = "all" | "approvals" | "errors" | "done";
  let filter = $state<Filter>("all");

  let visible = $derived(
    filter === "all"       ? items :
    filter === "approvals" ? items.filter((n) => n.tier === "blocking") :
    filter === "errors"    ? items.filter((n) => /error|fail/i.test(n.title)) :
                             items.filter((n) => n.tier === "ambient")
  );
</script>

<section aria-label="notification hub" class="notif-hub">
  <div class="hub-toolbar">
    <button onclick={() => (filter = "all")}       aria-pressed={filter === "all"}>All</button>
    <button onclick={() => (filter = "approvals")} aria-pressed={filter === "approvals"}>Approvals</button>
    <button onclick={() => (filter = "errors")}    aria-pressed={filter === "errors"}>Errors</button>
    <button onclick={() => (filter = "done")}      aria-pressed={filter === "done"}>Done</button>
    <button onclick={onToggleDnd}  aria-pressed={dnd}>Do not disturb</button>
    <button onclick={onClearRead}>Clear read</button>
  </div>
  <ul class="notif-list">
    {#each visible as n (n.id)}
      <li class="notif-item tier-{n.tier}" class:read={n.read}>
        <span class="notif-title">{n.title}</span>
        <span class="notif-body dim">{n.body}</span>
        <button onclick={() => onDismiss(n.id)} aria-label="dismiss notification">✕</button>
      </li>
    {/each}
    {#if visible.length === 0}<li class="notif-empty dim">No notifications</li>{/if}
  </ul>
</section>
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/NotificationHub.svelte frontend/src/lib/NotificationHub.test.ts
git commit -m "feat(frontend): NotificationHub.svelte — filter, DND, dismiss"
```

---

### Task 4.22: `ApprovalCard.svelte`

**Files:** `frontend/src/lib/ApprovalCard.svelte`, `frontend/src/lib/ApprovalCard.test.ts`

**Caps gate:** `Caps.approvals=false` → hidden (Spike 1 fallback — agent prompts in pane instead).

- [ ] **Step 1: Write failing test**

```ts
// frontend/src/lib/ApprovalCard.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const singleReq  = { reqId: "req_1", tool: "Bash",     summary: "run: ls -la /tmp" };
const batchQueue = [
  { reqId: "req_1", tool: "Bash",      summary: "run: ls -la /tmp" },
  { reqId: "req_2", tool: "WriteFile", summary: "write: /wt/out.txt" },
];
const capsOn  = { approvals: true, attention: true, tokens: true };
const capsOff = { approvals: false, attention: true, tokens: true };

test("Allow fires onDecision(reqId, 'allow')", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision } });
  await waitFor(() => screen.getByRole("button", { name: /^allow$/i }));
  await fireEvent.click(screen.getByRole("button", { name: /^allow$/i }));
  expect(onDecision).toHaveBeenCalledWith("req_1", "allow");
});

test("batch Approve all calls onDecision for every queued item", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: batchQueue[0], queue: batchQueue, caps: capsOn, onDecision } });
  await waitFor(() => screen.getByText(/2 pending/i));
  await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
  expect(onDecision).toHaveBeenCalledTimes(2);
  expect(onDecision).toHaveBeenCalledWith("req_1", "allow");
  expect(onDecision).toHaveBeenCalledWith("req_2", "allow");
});

test("hidden when Caps.approvals is false", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOff, onDecision: () => {} } });
  expect(screen.queryByRole("region", { name: /approval/i })).toBeNull();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement**

```svelte
<!-- frontend/src/lib/ApprovalCard.svelte -->
<script lang="ts">
  import type { ApprovalReq, Caps } from "./wails";

  let {
    req, queue, caps, onDecision,
  }: {
    req: ApprovalReq;
    queue: ApprovalReq[];
    caps: Caps;
    onDecision: (reqId: string, decision: "allow" | "deny" | "always") => void;
  } = $props();

  function approveAll() { for (const r of queue) onDecision(r.reqId, "allow"); }
  function denyAll()    { for (const r of queue) onDecision(r.reqId, "deny");  }
</script>

{#if caps.approvals}
  <section aria-label="approval card" class="approval-card">
    <header class="approval-header">
      <span class="tool-name">{req.tool}</span>
      {#if queue.length > 1}<span class="batch-count">{queue.length} pending</span>{/if}
    </header>
    <p class="approval-summary">{req.summary}</p>
    <div class="approval-actions">
      <button onclick={() => onDecision(req.reqId, "allow")}>Allow</button>
      <button onclick={() => onDecision(req.reqId, "deny")}>Deny</button>
      <button onclick={() => onDecision(req.reqId, "always")}>Always</button>
    </div>
    {#if queue.length > 1}
      <div class="batch-actions">
        <button onclick={approveAll}>Approve all</button>
        <button onclick={denyAll}>Deny all</button>
      </div>
    {/if}
  </section>
{/if}
```

- [ ] **Step 4: Run passing** → green

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/ApprovalCard.svelte frontend/src/lib/ApprovalCard.test.ts
git commit -m "feat(frontend): ApprovalCard.svelte — Allow/Deny/Always + batch + Caps gate"
```

---

### Task 4.23: `NewSessionDialog.svelte`, `ConfirmDialog.svelte` (undo), `Preview.svelte`

**Files:** `frontend/src/lib/NewSessionDialog.svelte`, `frontend/src/lib/NewSessionDialog.test.ts`, `frontend/src/lib/ConfirmDialog.svelte` (extend), `frontend/src/lib/ConfirmDialog.test.ts` (extend), `frontend/src/lib/Preview.svelte`, `frontend/src/lib/Preview.test.ts`

**Deps:** Verify + exact-pin before installing:

```bash
npm view marked version
npm view mermaid version
# Use registry output, not hardcoded versions:
npm --prefix frontend install --save-exact marked@X.Y.Z mermaid@X.Y.Z
```

- [ ] **Step 1: Write failing tests**

```ts
// frontend/src/lib/NewSessionDialog.test.ts  (replace existing)
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("onCreate fires with agent, repo, branch, model", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"], branches: ["main", "feat/x"],
    onCreate, onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await fireEvent.change(screen.getByLabelText(/repo/i),   { target: { value: "/home/user/proj" } });
  await fireEvent.change(screen.getByLabelText(/branch/i), { target: { value: "feat/x" } });
  await fireEvent.change(screen.getByLabelText(/model/i),  { target: { value: "claude-opus-4-5" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "feat/x", "claude-opus-4-5");
});
```

```ts
// append to frontend/src/lib/ConfirmDialog.test.ts
test("undo affordance shown for destructive ops", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, { props: {
    open: true, message: "Remove workspace?", destructive: true,
    onConfirm: () => {}, onCancel: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /confirm/i }));
  expect(screen.getByText(/undo/i)).toBeInTheDocument();
});
```

```ts
// frontend/src/lib/Preview.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("mermaid", () => ({ default: {
  initialize: vi.fn(),
  render: vi.fn(async () => ({ svg: "<svg></svg>" })),
}}));
vi.mock("marked", () => ({ marked: vi.fn(async (s: string) => `<p>${s}</p>`) }));

test("renders markdown", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/README.md", kind: "markdown", content: "# Hello" } });
  await waitFor(() => expect(screen.getByRole("region", { name: /preview/i })).toBeInTheDocument());
  expect(screen.getByRole("region", { name: /preview/i }).innerHTML).toContain("<p>");
});

test("renders mermaid", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/arch.mmd", kind: "mermaid", content: "graph TD; A-->B" } });
  await waitFor(() => expect(screen.getByRole("region", { name: /preview/i })).toBeInTheDocument());
  const m = await import("mermaid");
  expect(m.default.render).toHaveBeenCalled();
});

test("renders image", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/logo.png", kind: "image", content: "" } });
  await waitFor(() => screen.getByRole("img"));
  expect(screen.getByRole("img")).toHaveAttribute("src", "/wt/logo.png");
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement `NewSessionDialog.svelte`**

```svelte
<!-- frontend/src/lib/NewSessionDialog.svelte -->
<script lang="ts">
  let {
    open, repos, branches, onCreate, onClose,
  }: {
    open: boolean; repos: string[]; branches: string[];
    onCreate: (agent: string, repo: string, branch: string, model: string) => void;
    onClose: () => void;
  } = $props();

  let agent  = $state("claude");
  let repo   = $state(repos[0] ?? "");
  let branch = $state(branches[0] ?? "");
  let model  = $state("claude-sonnet-4-5");

  $effect(() => { if (open) { agent = "claude"; repo = repos[0] ?? ""; branch = branches[0] ?? ""; model = "claude-sonnet-4-5"; } });

  function handleCreate() {
    if (!repo || !branch) return;
    onCreate(agent, repo, branch, model);
  }
</script>

{#if open}
  <div role="dialog" aria-label="new session" class="dialog-overlay">
    <div class="dialog">
      <label>Agent<select aria-label="agent" bind:value={agent}>
        <option value="claude">Claude</option>
        <option value="opencode">opencode</option>
      </select></label>
      <label>Repo<select aria-label="repo" bind:value={repo}>
        {#each repos as r}<option value={r}>{r}</option>{/each}
      </select></label>
      <label>Branch<select aria-label="branch" bind:value={branch}>
        {#each branches as b}<option value={b}>{b}</option>{/each}
      </select></label>
      <label>Model<input type="text" aria-label="model" bind:value={model} /></label>
      <button onclick={handleCreate}>Create</button>
      <button onclick={onClose}>Cancel</button>
    </div>
  </div>
{/if}
```

- [ ] **Step 4: Extend `ConfirmDialog.svelte`** — add `destructive` prop + undo notice:

```svelte
<!-- Replace ConfirmDialog.svelte script + template with: -->
<script lang="ts">
  let {
    open, message, confirmLabel = "Confirm", destructive = false, onConfirm, onCancel,
  }: {
    open: boolean; message: string; confirmLabel?: string; destructive?: boolean;
    onConfirm?: () => void; onCancel?: () => void;
  } = $props();
</script>

{#if open}
  <div role="dialog" aria-label="confirm">
    <p>{message}</p>
    {#if destructive}<p class="undo-notice dim">This action can be undone within 10 seconds.</p>{/if}
    <button onclick={() => onConfirm?.()}>{confirmLabel}</button>
    <button onclick={() => onCancel?.()}>Cancel</button>
  </div>
{/if}
```

- [ ] **Step 5: Implement `Preview.svelte`**

```svelte
<!-- frontend/src/lib/Preview.svelte -->
<script lang="ts">
  let {
    path, kind, content,
  }: { path: string; kind: "markdown" | "mermaid" | "image"; content: string } = $props();

  let html = $state("");

  $effect(() => {
    if (kind === "markdown" && content) {
      import("marked").then(({ marked }) =>
        Promise.resolve(marked(content)).then((h) => { html = h as string; })
      );
    } else if (kind === "mermaid" && content) {
      import("mermaid").then(({ default: mermaid }) => {
        mermaid.initialize({ startOnLoad: false });
        mermaid.render("preview-mermaid", content).then(({ svg }) => { html = svg; });
      });
    }
  });
</script>

<section aria-label="preview" class="preview">
  {#if kind === "image"}
    <img src={path} alt={path} class="preview-img" />
  {:else}
    <div class="preview-body">{@html html}</div>
  {/if}
</section>
```

- [ ] **Step 6: Run passing** → green

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/NewSessionDialog.svelte frontend/src/lib/NewSessionDialog.test.ts \
        frontend/src/lib/ConfirmDialog.svelte frontend/src/lib/ConfirmDialog.test.ts \
        frontend/src/lib/Preview.svelte frontend/src/lib/Preview.test.ts \
        frontend/package.json frontend/package-lock.json
git commit -m "feat(frontend): NewSessionDialog + ConfirmDialog undo + Preview (markdown/mermaid/image)"
```

---

### Task 4.24: Drag-drop + token meter

**Files:** `frontend/src/lib/DragDrop.svelte`, `frontend/src/lib/DragDrop.test.ts`, `frontend/src/lib/TokenMeter.svelte`, `frontend/src/lib/TokenMeter.test.ts`

**Spike 4 gate:** OS file/folder drop-in via Wails `OnFileDrop` (Linux #3686). The `fileDrop` prop reflects the spike-4 outcome (read from `docs/superpowers/spikes/spike-04.md`). If the spike failed, render "Open file…" + Copy-path affordances. **Token meter** is hidden when `Caps.tokens=false` (Spike 2 fallback).

- [ ] **Step 1: Write failing tests**

```ts
// frontend/src/lib/DragDrop.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({ writeToPty: vi.fn(async () => {}) }));

test("file drop fires writeToPty with @mention when fileDrop cap is true", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  // Simulate a file with a path property (Electron/Wails drop model)
  const file = Object.assign(new File(["x"], "main.go"), { path: "/wt/src/main.go" });
  await fireEvent.drop(zone, { dataTransfer: { files: [file], types: ["Files"] } });
  await waitFor(() => expect(w.writeToPty).toHaveBeenCalledWith("p1", expect.any(Array)));
});

test("renders Open file fallback when fileDrop cap is false", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: false } });
  expect(screen.getByRole("button", { name: /open file/i })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /copy path/i })).toBeInTheDocument();
});
```

```ts
// frontend/src/lib/TokenMeter.test.ts
import { render, screen } from "@testing-library/svelte";

test("renders token count and cost when capsTokens is true", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 12500, cost: 0.043, capsTokens: true } });
  expect(screen.getByRole("status")).toBeInTheDocument();
  expect(screen.getByText(/12,500/)).toBeInTheDocument();
  expect(screen.getByText(/\$0\.04/)).toBeInTheDocument();
});

test("renders nothing when capsTokens is false", async () => {
  const { default: TokenMeter } = await import("./TokenMeter.svelte");
  render(TokenMeter, { props: { tokens: 999, cost: 0.1, capsTokens: false } });
  expect(screen.queryByRole("status")).toBeNull();
});
```

- [ ] **Step 2: Run failing** → FAIL

- [ ] **Step 3: Implement `DragDrop.svelte`**

```svelte
<!-- frontend/src/lib/DragDrop.svelte -->
<script lang="ts">
  import { writeToPty } from "./wails";

  let {
    paneId,
    fileDrop,
    children,
  }: { paneId: string; fileDrop: boolean; children?: import("svelte").Snippet } = $props();

  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    if (!fileDrop || !e.dataTransfer) return;
    const files = Array.from(e.dataTransfer.files) as (File & { path?: string })[];
    for (const f of files) {
      const p = (f as any).path ?? f.name;
      const bytes = Array.from(new TextEncoder().encode(`@${p} `));
      await writeToPty(paneId, bytes);
    }
  }

  function prevent(e: DragEvent) { e.preventDefault(); e.stopPropagation(); }
</script>

<div role="region" aria-label="drop zone" class="drop-zone"
  ondragover={prevent} ondragleave={prevent} ondrop={handleDrop}>
  {#if !fileDrop}
    <div class="drop-fallback">
      <button onclick={() => {}}>Open file…</button>
      <button onclick={() => navigator.clipboard?.writeText(paneId).catch(() => {})}>Copy path</button>
    </div>
  {:else if children}
    {@render children()}
  {/if}
</div>
```

- [ ] **Step 4: Implement `TokenMeter.svelte`**

```svelte
<!-- frontend/src/lib/TokenMeter.svelte -->
<script lang="ts">
  let { tokens, cost, capsTokens }: { tokens: number; cost: number; capsTokens: boolean } = $props();
  let formattedTokens = $derived(tokens.toLocaleString());
  let formattedCost   = $derived(`$${cost.toFixed(2)}`);
</script>

{#if capsTokens}
  <div role="status" class="token-meter" aria-label="token usage">
    <span class="token-count">{formattedTokens} tok</span>
    <span class="token-cost">{formattedCost}</span>
  </div>
{/if}
```

- [ ] **Step 5: Run passing** → green

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/DragDrop.svelte frontend/src/lib/DragDrop.test.ts \
        frontend/src/lib/TokenMeter.svelte frontend/src/lib/TokenMeter.test.ts
git commit -m "feat(frontend): DragDrop (@mention write-to-pty, spike-4 fallback) + TokenMeter (spike-2 Caps gate)"
```

---

### Task 4.23 security amendment (committed `65763cc`)

`Preview.svelte` renders `{@html …}` from `marked(content)` / mermaid SVG, where `content` is **agent-authored worktree file content** (untrusted — a prompt-injected agent can plant `<img onerror>`/`<script>` in a markdown file; `@html` would execute it with full Wails IPC access). Fixed: wrapped all `@html` paths in `DOMPurify.sanitize(...)` (`dompurify@3.4.7`, direct exact-pin) + `mermaid.initialize({ securityLevel: "strict" })`. An XSS-stripping test proves it. **Frozen XSS rule for the rest of the build:** any `@html`/`innerHTML` fed by file content, diff text, or agent output MUST be DOMPurify-sanitized; default to `{...}` auto-escaping everywhere else.

---

### Task 4.25: App composition & live wiring (closes the 12-orphan gap — spec §7.1–7.3, §8)

**Why this exists:** Task 4.10 built App.svelte as a *skeleton* (three zones, empty `<Stage/>` slots, Sidebar stubbed) and no later task assembled the app. Post-4.24 orphan audit: **12 components have no parent** — `Editor, DiffView, FileTree, MenuBar, CommandPalette, NotificationHub, ApprovalCard, NewSessionDialog, ConfirmDialog, Preview, DragDrop, TokenMeter`. App.svelte has ZERO event/workspace wiring (`onAgentEvent`/`onNotify`/`onFsChanged`/`listWorkspaces`/`openWorkspace`/`approve` all absent), and the agent `Terminal` is only mounted by `ShellDrawer` — never in Stage's Agent view. Without this task perch is a box of disconnected parts. The interaction model is fully specified by the design spec (zones, Agent/Code/Diff routing, `\` split, docked ApprovalCard "not inside the terminal grid", notification tiers, the NORMAL/TERMINAL/COMMAND keymap) — this is assembly, not new design.

**Approach:** grow `App.svelte` incrementally across the sub-tasks below; each is its own red→green→commit. `App.test.ts` extends per sub-task. Use the established Svelte-5 mock discipline (stub heavy children — Terminal/Editor/DiffView — via `__stubs__/Empty.svelte`; never plain-object factories). Un-stub `Sidebar` (it's now prop-driven and safe). Per-file `npm test -- src/App.test.ts`. The full `npm run build && npm run check` (Phase 5) is the compile gate that proves every wire type-checks against `wails.ts`/components.

- **4.25.1 — Workspace state + Sidebar:** on mount `await listWorkspaces()` into `$state` `workspaces`; hold `activeId`. Render real `Sidebar` with `{workspaces, activeId, onSelect, onNew}`. `onSelect(id)` → set active + `openWorkspace(id)`. `onNew` → open NewSessionDialog (4.25.6). Tests: renders workspaces; select calls `openWorkspace` + updates active.
- **4.25.2 — Stage content routing:** mount active workspace's view into Stage's `primary` slot by `layout.view`: `"agent"`→`Terminal {paneId: active.paneId, cwd: active.worktreePath}`; `"code"`→`Editor` (+ `FileTree` alongside; FileTree `onOpen(path)` sets the Editor `path`); `"diff"`→`DiffView {worktree: active.worktreePath}`. `layout.split` → render the same/companion into `secondary`. Empty-state placeholder when `activeId` is null. Tests: each view mounts the right (stubbed) child with the right props; no active → placeholder.
- **4.25.3 — MenuBar + CommandPalette:** mount `MenuBar {onCommand}`. Maintain a command registry (new session, switch view 1/2/3, toggle split, pick theme, toggle DND, reveal, …). `mode.current==="command"` (or `:`) opens `CommandPalette {commands, onRun, onClose}`; `onRun(id)` dispatches the same registry; `Esc`/`onClose` → `mode.leaveCommand()`. Tests: `:` opens palette; running a command invokes its action; menu `onCommand` dispatches.
- **4.25.4 — Live event wiring:** in `onMount`, subscribe `onAgentEvent`, `onNotify`, `onFsChanged`; unsubscribe in `onDestroy`. `onAgentEvent` → update the matching workspace's `state`/`caps` (drives Sidebar icon + TokenMeter), and on `kind==="state" && state==="awaiting-approval"` (or `ev.approval`) enqueue an approval for that workspace. `onNotify` → route into the notifications store (`addBlocking`/`addAmbient`/`addRoutine` by tier). `onFsChanged` → bump a reactive `fsVersion` keyed per workspace so DiffView/FileTree reload (e.g. `{#key fsVersion}`). Tests (mock wails event helpers to capture+invoke callbacks): an agent:event flips a workspace to "needs you"; a notify lands in the store; fs:changed bumps the refresh key.
- **4.25.5 — Approval + notification chrome (docked):** render `ApprovalCard` as docked chrome (NOT inside the Stage/terminal grid) when the active workspace has a pending approval → `Allow/Deny/Always` → `approve(reqId, decision)` then dequeue. Render `NotificationHub` (bell + badge + filter + mark-read + DND) bound to the store. Tests: pending approval shows the card; clicking Allow calls `approve(reqId,"allow")` and clears it.
- **4.25.6 — Dialogs + DragDrop + TokenMeter + full NORMAL keymap:** mount `NewSessionDialog` (on `onNew`/`:new`) → `createWorkspace(agent,repo,branch,model)` → refresh list; `ConfirmDialog` for destructive ops (RemoveWorkspace) with the undo affordance; `DragDrop` wrapping the agent Terminal (@mention paths → writeToPty); `TokenMeter` in the status line gated by `active.caps.tokens`. Extend `onKeyDown` to the full spec NORMAL map (`j/k` session nav, `1/2/3` view, `gd` diff, `ge` editor, `` ^` `` shell, `\` split, `/` filter, `:` command, `⏎` open, `i`→TERMINAL) with TERMINAL/COMMAND transitions. Tests: key routing per the map; new-session flow calls `createWorkspace`; destructive op routes through ConfirmDialog.
- **4.25.7 — Composition smoke test:** one `App.test.ts` (or `App.smoke.test.ts`) asserting full assembly: all zones + MenuBar + NotificationHub render; selecting a workspace mounts the agent Terminal; `2` switches to Editor, `3` to DiffView; an approval agent:event surfaces ApprovalCard and Allow calls `approve`. Heavy children stubbed; wails mocked.

**After 4.25:** re-run the orphan audit (`grep -rl` each `lib/*.svelte` for a parent import) — ZERO orphans is the gate. This is a hard prerequisite for declaring Phase 4 complete (advisor-flagged).

---

## Phase 5 — Integration & Gates

> **Branch:** `feat/perch-v1`
> **Prereqs:** Phases 0–4 green.
> **Commands:** unit `go test -race -count=1 ./...` · integration `go test -race -count=1 -tags=integration ./...` · frontend `npm --prefix frontend test` · type-check `npm --prefix frontend run check` · build `make gui-build`

---

### Task 5.1: Fake agent binary (`cmd/fake-agent/main.go`)

**Files:** `cmd/fake-agent/main.go` (new), `cmd/fake-agent/main_test.go` (new, `-tags integration`)

- [ ] **Step 1 — Write failing test**

```go
//go:build integration

package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFakeAgent_PostsHooksAndPrintsLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "fake-agent")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/fake-agent").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	var got struct {
		types []string
		mu    sync.Mutex
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev struct {
			HookEventName string `json:"hook_event_name"`
		}
		_ = json.Unmarshal(body, &ev)
		got.mu.Lock()
		got.types = append(got.types, ev.HookEventName)
		got.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
	}))
	t.Cleanup(srv.Close)

	var stdoutBuf strings.Builder
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PERCH_HOOK_URL="+srv.URL+"/hook",
		"PERCH_HOOK_TOKEN=test-token",
		"PERCH_SESSION_ID=ses_fake01",
		// Descriptors separated by ';'; fields within a descriptor by ','.
		"PERCH_SCRIPT=SessionStart;PreToolUse,tool=Write,input={};Stop",
		"PERCH_LINES=hello from fake agent|diff --git a/f b/f",
	)
	cmd.Stdout = &stdoutBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timeout")
	}

	stdout := stdoutBuf.String()
	for _, want := range []string{"hello from fake agent", "diff --git a/f b/f"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q", want)
		}
	}
	got.mu.Lock()
	types := append([]string(nil), got.types...)
	got.mu.Unlock()
	for i, ev := range []string{"SessionStart", "PreToolUse", "Stop"} {
		if i >= len(types) || types[i] != ev {
			t.Errorf("hook[%d]: got %v, want %q", i, types, ev)
		}
	}
}
```

- [ ] Run: `go test -race -count=1 -tags=integration -run TestFakeAgent ./cmd/fake-agent/` — FAIL.

- [ ] **Step 2 — Implement `cmd/fake-agent/main.go`**

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	hookURL := mustEnv("PERCH_HOOK_URL")
	token := mustEnv("PERCH_HOOK_TOKEN")
	sessionID := envOr("PERCH_SESSION_ID", "ses_fakedefault")

	if raw := os.Getenv("PERCH_LINES"); raw != "" {
		for _, line := range strings.Split(raw, "|") {
			fmt.Println(line)
		}
	}
	time.Sleep(20 * time.Millisecond)

	for _, descriptor := range strings.Split(envOr("PERCH_SCRIPT", "SessionStart;Stop"), ";") {
		parts := strings.Split(descriptor, ",")
		switch parts[0] {
		case "SessionStart":
			postHook(hookURL, token, map[string]any{"hook_event_name": "SessionStart", "session_id": sessionID})
		case "PreToolUse":
			toolName, inputJSON := "Unknown", "{}"
			for _, kv := range parts[1:] {
				if after, ok := strings.CutPrefix(kv, "tool="); ok {
					toolName = after
				}
				if after, ok := strings.CutPrefix(kv, "input="); ok {
					inputJSON = after
				}
			}
			resp := postHook(hookURL, token, map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      sessionID,
				"tool_name":       toolName,
				"tool_input":      json.RawMessage(inputJSON),
			})
			var dec struct {
				HookSpecificOutput struct {
					PermissionDecision string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			_ = json.Unmarshal(resp, &dec)
			if dec.HookSpecificOutput.PermissionDecision == "deny" {
				fmt.Fprintln(os.Stderr, "fake-agent: denied")
				os.Exit(1)
			}
		case "Stop":
			postHook(hookURL, token, map[string]any{"hook_event_name": "Stop", "session_id": sessionID})
		}
	}
}

func postHook(baseURL, token string, payload map[string]any) []byte {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		die("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		die("POST: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		die("server %d: %s", resp.StatusCode, b)
	}
	return b
}

func mustEnv(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	die("required env var %s unset", k)
	return ""
}
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fake-agent: "+format+"\n", args...)
	os.Exit(2)
}
```

- [ ] Run: `go test -race -count=1 -tags=integration -run TestFakeAgent ./cmd/fake-agent/` — PASS.
- [ ] `go build ./...` — PASS.
- [ ] Commit: `feat(test): add fake-agent binary for integration tests`

---

### Task 5.2: Headless end-to-end integration test (`app/app_e2e_test.go`)

Drive the full loop: CreateWorkspace → OpenWorkspace (fake-agent fires
SessionStart) → PreToolUse arrives → `App.Approve` unblocks it → Stop → state
Done. Every Wails event asserted. DiffStat is verified against a file pre-staged
by the test (not by the fake-agent, since the fake-agent does not write files;
this is stated plainly).

**Files:** `app/app_e2e_test.go` (new, `-tags integration`)

- [ ] **Step 1 — Write failing test**

```go
//go:build integration

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
	"github.com/Miniature-Pug/perch/internal/registry"
)

type wailsEvent struct {
	name string
	data any
}

func TestE2E_FullLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	fakeAgentBin := filepath.Join(t.TempDir(), "fake-agent")
	if out, err := exec.Command("go", "build", "-o", fakeAgentBin, "./cmd/fake-agent").CombinedOutput(); err != nil {
		t.Fatalf("build fake-agent: %v\n%s", err, out)
	}

	repoDir := t.TempDir()
	mustGitE2E(t, repoDir, "init", "-q")
	mustGitE2E(t, repoDir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-qm", "init")
	// Pre-stage a file so DiffStat returns a non-empty result.
	if err := os.WriteFile(filepath.Join(repoDir, "hello.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitE2E(t, repoDir, "add", "hello.go")

	hl, err := hooklistener.New()
	if err != nil {
		t.Fatalf("hooklistener.New: %v", err)
	}
	t.Cleanup(func() { _ = hl.Close() })

	var evMu sync.Mutex
	var events []wailsEvent
	emit := func(name string, data ...any) {
		evMu.Lock()
		defer evMu.Unlock()
		var d any
		if len(data) > 0 {
			d = data[0]
		}
		events = append(events, wailsEvent{name, d})
	}

	store, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	// App is constructed via the production constructor; fakeAgentBin is
	// injected via PERCH_FAKE_AGENT_BIN env so OpenWorkspace's agent-launch
	// path swaps in the fake binary without a test-only code branch.
	t.Setenv("PERCH_FAKE_AGENT_BIN", fakeAgentBin)
	a := newApp(store, emit, hl)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	vm, err := a.CreateWorkspace("claude", repoDir, "main", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsID := vm.ID

	if err := a.OpenWorkspace(wsID); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseWorkspace(wsID) })

	waitHookEvent(t, ctx, hl.Events(), "SessionStart")

	preEv := waitHookEvent(t, ctx, hl.Events(), "PreToolUse")
	if preEv.ReqID == "" {
		t.Fatal("PreToolUse missing ReqID")
	}

	assertWailsEventE2E(t, &evMu, &events, "agent:event", func(data any) bool {
		ev, ok := data.(agent.Event)
		return ok && ev.State == agent.StateAwaitingApproval && ev.WorkspaceID == wsID
	})

	if err := a.Approve(preEv.ReqID, "allow"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	waitHookEvent(t, ctx, hl.Events(), "Stop")

	assertWailsEventE2E(t, &evMu, &events, "agent:event", func(data any) bool {
		ev, ok := data.(agent.Event)
		return ok && ev.State == agent.StateDone && ev.WorkspaceID == wsID
	})

	stat, err := a.DiffStat(repoDir)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if len(stat) == 0 {
		t.Error("DiffStat: expected ≥1 changed file (hello.go pre-staged)")
	}

	evMu.Lock()
	hasPty := false
	for _, ev := range events {
		if strings.HasPrefix(ev.name, "pty:data:") {
			hasPty = true
		}
	}
	evMu.Unlock()
	if !hasPty {
		t.Error("no pty:data:* events emitted")
	}
}

func waitHookEvent(t *testing.T, ctx context.Context, ch <-chan hooklistener.HookEvent, kind string) hooklistener.HookEvent {
	t.Helper()
	for {
		select {
		case ev := <-ch:
			if ev.Type == kind {
				return ev
			}
		case <-ctx.Done():
			t.Fatalf("timeout waiting for hook event %q", kind)
		}
	}
}

func assertWailsEventE2E(t *testing.T, mu *sync.Mutex, evs *[]wailsEvent, name string, check func(any) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, ev := range *evs {
			if ev.name == name && check(ev.data) {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
	}
	t.Errorf("never saw expected Wails event %q", name)
}

func mustGitE2E(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
```

`newApp` is a package-internal constructor used by both tests and production;
`PERCH_FAKE_AGENT_BIN` is read by `OpenWorkspace` in the pty-spawn path when
non-empty, replacing the real agent binary with no special test branch (same
env-based substitution the fake-agent itself uses for `PERCH_HOOK_URL`).

- [ ] Run: `go test -race -count=1 -tags=integration -run TestE2E_FullLoop ./app/` — FAIL (fields missing).
- [ ] Wire `newApp(store, emit, hl)` and the `PERCH_FAKE_AGENT_BIN` env lookup in `app/app.go`.
- [ ] Run: `go test -race -count=1 -tags=integration -run TestE2E_FullLoop ./app/` — PASS.
- [ ] `go test -race -count=1 ./...` — PASS (unit tests unaffected).
- [ ] Commit: `test(e2e): headless full-loop integration test via fake-agent + hooklistener`

---

### Task 5.3: Frontend approval end-to-end test (Vitest)

**Files:** `frontend/src/lib/ApprovalFlow.test.ts` (new)

- [ ] **Step 1 — Write failing test**

```typescript
// frontend/src/lib/ApprovalFlow.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';

vi.mock('../wails', () => ({
  Approve: vi.fn().mockResolvedValue(undefined),
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
}));
import { Approve } from '../wails';
import ApprovalCard from './ApprovalCard.svelte';
import { notifications, addBlocking } from './stores/notifications.svelte';

const req = { reqId: 'req-001', tool: 'Write', summary: 'Write hello.go' };

beforeEach(() => { vi.clearAllMocks(); notifications.items = []; });

describe('ApprovalCard', () => {
  it('renders tool name and summary', () => {
    const { getByText } = render(ApprovalCard, {
      props: { req, queue: [req], onDecision: vi.fn() },
    });
    expect(getByText('Write')).toBeTruthy();
    expect(getByText('Write hello.go')).toBeTruthy();
  });

  it('Allow calls Approve and fires onDecision', async () => {
    const onDecision = vi.fn();
    const { getByRole } = render(ApprovalCard, {
      props: { req, queue: [req], onDecision },
    });
    await fireEvent.click(getByRole('button', { name: /allow/i }));
    await waitFor(() => {
      expect(Approve).toHaveBeenCalledWith('req-001', 'allow');
      expect(onDecision).toHaveBeenCalledWith('req-001', 'allow');
    });
  });

  it('blocking notification sets tier and updates sidebar state', async () => {
    addBlocking({ title: 'Approval needed', body: 'Write hello.go', workspaceId: 'ws-001' });
    expect(notifications.items.length).toBeGreaterThan(0);
    const item = notifications.items.find((n) => n.workspaceId === 'ws-001');
    expect(item?.tier).toBe('blocking');
    // Sidebar status is driven by agent.State emitted via agent:event;
    // the notification item carries workspaceId so the Sidebar can co-locate
    // the ⚠ glyph next to the correct workspace entry.
    expect(item?.workspaceId).toBe('ws-001');
  });
});
```

- [ ] Run: `npm --prefix frontend test -- src/lib/ApprovalFlow.test.ts` — FAIL.
- [ ] Ensure `ApprovalCard.svelte` exposes Allow/Deny/Always buttons calling `onDecision(req.reqId, decision)` then `Approve(req.reqId, decision)`.
- [ ] Ensure `wails.ts` exports `Approve(reqID: string, decision: string): Promise<void>`.
- [ ] Ensure `stores/notifications.svelte.ts` exports `addBlocking({ title, body, workspaceId })` pushing `{ tier: 'blocking', workspaceId, ... }`.
- [ ] Run: `npm --prefix frontend test -- src/lib/ApprovalFlow.test.ts` — PASS.
- [ ] `npm --prefix frontend run check` — PASS.
- [ ] Commit: `test(frontend): Vitest approval-card flow + notification-store assertions`

---

### Task 5.4: Production build verification script

**Files:** `scripts/verify-build.sh` (new, chmod +x)

```bash
#!/usr/bin/env bash
# Asserts that make gui-build produces a valid ELF binary.
# Usage: ./scripts/verify-build.sh [BIN_PATH]
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${1:-${REPO_ROOT}/bin/perch}"

echo "==> Running make gui-build …"
make -C "${REPO_ROOT}" gui-build

echo "==> Checking ${BIN} …"
[[ -f "${BIN}" ]] || { echo "ERROR: binary not found at ${BIN}" >&2; exit 1; }

FILE_OUTPUT="$(file "${BIN}")"
echo "    ${FILE_OUTPUT}"
echo "${FILE_OUTPUT}" | grep -q "ELF"       || { echo "ERROR: not ELF" >&2; exit 1; }
echo "${FILE_OUTPUT}" | grep -q "executable" || { echo "ERROR: not executable" >&2; exit 1; }

if ldd "${BIN}" 2>/dev/null | grep -qi tmux; then
  echo "ERROR: binary links tmux (should be removed)" >&2; exit 1
fi

echo "==> PASS: ${BIN} is a valid ELF executable."
```

- [ ] `chmod +x scripts/verify-build.sh`
- [ ] `bash scripts/verify-build.sh` — PASS.
  - Expected last line: `==> PASS: bin/perch is a valid ELF executable.`
- [ ] Commit: `feat(ci): verify-build.sh — ELF assertion after make gui-build`

---

### Task 5.5: Quality gates — `make verify-all`

`verify` already means `go mod verify`. Add a distinct `verify-all` target.

**Files:** `Makefile`

Append to `Makefile` (also add `verify-all` to the `.PHONY` line):

```makefile
verify-all: vet lint vulncheck ## quality gates: vet + lint + govulncheck + tsc
	npm --prefix frontend run check
	@echo "==> All quality gates PASSED."
```

- [ ] `make vet` — exit 0, no output.
- [ ] `make lint` — exit 0; last non-empty line has no lint issues.
- [ ] `make vulncheck` — `No vulnerabilities found.`
- [ ] `npm --prefix frontend run check` — `svelte-check found 0 errors and 0 warnings`.
- [ ] `make verify-all` — all four above in sequence, ends with `==> All quality gates PASSED.`
- [ ] Commit: `build: add verify-all target aggregating vet+lint+vulncheck+tsc`

---

### Task 5.6: Remove spike harnesses (`cmd/spike-*`)

- [ ] List `cmd/` to discover any `spike-*` dirs: `ls cmd/`
- [ ] If found: `git rm -r cmd/spike-*/`; run `go build ./...` — PASS.
- [ ] If absent: record no-op.
- [ ] Commit: `chore: remove Phase-1 spike harnesses (cmd/spike-*)` (use `--allow-empty` if absent).

---

### Task 5.7: Documentation refresh

**Files to read and edit:**
- `README.md` — rewrite for cockpit. Content sections: what it is · requirements · build/run · quick start · commands (no `attach`/`resurrect`/`status`) · agent setup (claude hooks + opencode serve) · Caps/degradation table · keyboard model table · architecture pointer.
- `docs/diagrams/architecture.mmd` — replace tmux subgraph with direct-pty + Monitor seam diagram (component graph: App→Bridge→Shell, App→ClaudeMonitor→hooklistener, App→OpencodeMonitor→serve/SSE).
- `docs/diagrams/status-sequence.mmd` — replace `perch status set` sequence with hook-listener approval sequence (Agent→hooklistener→App→frontend→User→App→hooklistener→Agent→App→frontend).
- `docs/diagrams/worktree-lifecycle.mmd` — read; remove any tmux/resurrect references; leave accurate sections unchanged.
- `docs/diagrams/discovery-state.mmd` — read; update any `state.json` references to `workspaces.json` registry.
- `ARCHITECTURE.md` — rewrite tmux, `perch resurrect`, `perch attach`, `internal/state`, `internal/resurrect` sections; retain security model verbatim.
- `CLAUDE.md` at repo root does not exist → skip.

Key facts every doc must state: no tmux, no daemon; one direct pty per pane; registry at `~/.config/perch/workspaces.json`; no TCP port in production; the one local surface is the hook listener (`127.0.0.1`, ephemeral port, bearer token); Caps-based degradation; vim modal model (NORMAL/TERMINAL/COMMAND); mouse always primary.

- [ ] Read each file before editing to confirm current content.
- [ ] Edit files one at a time; run `make gui-build` after to confirm docs-only changes don't break the build.
- [ ] Commit: `docs: rewrite README + architecture diagrams for cockpit (direct-pty, Monitor, no tmux)`

---

### Task 5.8: Settings UI — AlwaysRules management surface

**Files:** `frontend/src/lib/SettingsPanel.svelte` (new), `frontend/src/lib/SettingsPanel.test.ts` (new)

- [ ] **Step 1 — Write failing test**

```typescript
// frontend/src/lib/SettingsPanel.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';

vi.mock('../wails', () => ({
  GetSettings: vi.fn().mockResolvedValue({
    theme: 'gruvbox', density: 'dense', font: 'geist', dnd: false,
    alwaysRules: [
      { agent: 'claude', tool: 'Read',  pattern: '**/*.go' },
      { agent: 'claude', tool: 'Write', pattern: '**/*.md' },
    ],
  }),
  SaveSettings: vi.fn().mockResolvedValue(undefined),
}));
import { SaveSettings } from '../wails';
import SettingsPanel from './SettingsPanel.svelte';

beforeEach(() => { vi.clearAllMocks(); });

describe('SettingsPanel', () => {
  it('renders always-rules', async () => {
    const { findByText } = render(SettingsPanel, { props: { open: true } });
    expect(await findByText('Read')).toBeTruthy();
    expect(await findByText('**/*.go')).toBeTruthy();
  });

  it('revokes a rule and calls SaveSettings with one fewer rule', async () => {
    const { findAllByRole } = render(SettingsPanel, { props: { open: true } });
    const btns = await findAllByRole('button', { name: /revoke/i });
    expect(btns.length).toBe(2);
    await fireEvent.click(btns[0]);
    await waitFor(() => {
      const saved = (SaveSettings as ReturnType<typeof vi.fn>).mock.calls[0][0];
      expect(saved.alwaysRules).toHaveLength(1);
    });
  });

  it('changes theme and persists', async () => {
    const { findByRole } = render(SettingsPanel, { props: { open: true } });
    const sel = await findByRole('combobox', { name: /theme/i });
    await fireEvent.change(sel, { target: { value: 'tokyo-night' } });
    await waitFor(() => {
      expect(SaveSettings).toHaveBeenCalledWith(
        expect.objectContaining({ theme: 'tokyo-night' })
      );
    });
  });

  it('toggles DND and persists', async () => {
    const { findByRole } = render(SettingsPanel, { props: { open: true } });
    const toggle = await findByRole('switch', { name: /do not disturb/i });
    await fireEvent.click(toggle);
    await waitFor(() => {
      expect(SaveSettings).toHaveBeenCalledWith(expect.objectContaining({ dnd: true }));
    });
  });
});
```

- [ ] Run: `npm --prefix frontend test -- src/lib/SettingsPanel.test.ts` — FAIL.

- [ ] **Step 2 — Implement `frontend/src/lib/SettingsPanel.svelte`**

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { GetSettings, SaveSettings } from '../wails';
  import type { Settings } from '../wails';

  let { open = false }: { open: boolean } = $props();

  let settings = $state<Settings>({
    theme: 'gruvbox', density: 'dense', font: 'geist', dnd: false, alwaysRules: [],
  });

  const themes   = ['gruvbox','tokyo-night','catppuccin','dracula','nord','rose-pine','one-dark','perch-cyan','light'];
  const densities = ['dense','comfortable','ultra'];
  const fonts     = ['geist','ibm-plex','inter'];

  onMount(async () => { settings = await GetSettings(); });

  async function save() { await SaveSettings(settings); }

  async function revokeRule(i: number) {
    settings = { ...settings, alwaysRules: settings.alwaysRules.filter((_, j) => j !== i) };
    await save();
  }
</script>

{#if open}
<aside class="settings-panel" role="dialog" aria-label="Settings">
  <section>
    <h2>Appearance</h2>
    <label>Theme
      <select aria-label="Theme" bind:value={settings.theme} onchange={save}>
        {#each themes as t}<option value={t}>{t}</option>{/each}
      </select>
    </label>
    <label>Density
      <select aria-label="Density" bind:value={settings.density} onchange={save}>
        {#each densities as d}<option value={d}>{d}</option>{/each}
      </select>
    </label>
    <label>Font
      <select aria-label="Font" bind:value={settings.font} onchange={save}>
        {#each fonts as f}<option value={f}>{f}</option>{/each}
      </select>
    </label>
  </section>

  <section>
    <h2>Notifications</h2>
    <label class="switch-label">Do not disturb
      <button role="switch" aria-label="Do not disturb" aria-checked={settings.dnd}
        onclick={async () => { settings = { ...settings, dnd: !settings.dnd }; await save(); }}>
        {settings.dnd ? 'On' : 'Off'}
      </button>
    </label>
  </section>

  <section>
    <h2>Always-allow rules</h2>
    <p class="caption">Auto-approve these tool calls without a prompt. Revoke any rule you no longer trust.</p>
    {#if settings.alwaysRules.length === 0}
      <p class="empty">No always-allow rules.</p>
    {:else}
      <ul>
        {#each settings.alwaysRules as rule, i}
          <li>
            <span class="rule-agent">{rule.agent}</span>
            <span class="rule-tool">{rule.tool}</span>
            <span class="rule-pattern">{rule.pattern}</span>
            <button aria-label="Revoke" onclick={() => revokeRule(i)}>Revoke</button>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</aside>
{/if}

<style>
  .settings-panel { padding: var(--perch-sp-4); background: var(--perch-surface); }
  h2 { font-size: var(--perch-fs-body); font-weight: 600; margin-bottom: var(--perch-sp-2); }
  .caption { font-size: var(--perch-fs-caption); color: var(--perch-text-dim); }
  ul { list-style: none; padding: 0; }
  li { display: flex; gap: var(--perch-sp-2); align-items: center; padding: var(--perch-sp-1) 0; }
  .rule-agent   { font-size: var(--perch-fs-label); color: var(--perch-text-dim); }
  .rule-tool    { font-weight: 600; }
  .rule-pattern { font-family: var(--perch-font-mono); font-size: var(--perch-fs-code); }
</style>
```

- [ ] Run: `npm --prefix frontend test -- src/lib/SettingsPanel.test.ts` — PASS.
- [ ] `npm --prefix frontend run check` — PASS.
- [ ] Commit: `feat(ui): SettingsPanel — always-rules revoke + theme/density/font/DND pickers`

---

### Task 5.9: Final full-suite gate + smoke checklist

**Files:** `docs/superpowers/smoke-checklist.md` (new)

- [ ] **Step 1 — Run full suite**

```sh
make test-all
# Expected: all packages "ok", no FAIL lines.

npm --prefix frontend test
# Expected: Tests N passed, 0 failed.

npm --prefix frontend run check
# Expected: svelte-check found 0 errors and 0 warnings.

make verify-all
# Expected: vet silent + lint 0 issues + govulncheck "No vulnerabilities found."
#           + tsc "0 errors and 0 warnings"
#           + "==> All quality gates PASSED."

bash scripts/verify-build.sh
# Expected last line: ==> PASS: bin/perch is a valid ELF executable.
```

All five commands must exit 0 before Step 2.

- [ ] **Step 2 — Write smoke checklist**

```markdown
# perch Cockpit — Manual Smoke Checklist

Run before any release tag. All items must pass.

## Environment
- [ ] Linux (Debian/Ubuntu or equivalent); WebKit2GTK + GTK3 installed
- [ ] `claude` or `opencode` installed and authenticated
- [ ] `bash scripts/verify-build.sh` exited 0

## Launch
- [ ] `bin/perch` opens a GUI window; no crash in terminal
- [ ] Sidebar renders; NORMAL mode visible in status line

## Workspace creation (claude)
- [ ] Command palette (`:` or `Ctrl-K`) → New Session → dialog appears
- [ ] Pick a git repo, branch `main`, agent `claude`
- [ ] Create → workspace in sidebar with `◐ running` status

## Terminal pane
- [ ] Click workspace → Terminal pane with live shell
- [ ] `i` → TERMINAL mode; keys pass to agent
- [ ] `Ctrl-\ Ctrl-n` → NORMAL mode

## Tool-call approval
- [ ] Ask agent to write a file
- [ ] ApprovalCard appears in chrome (not inside terminal grid)
- [ ] Card shows tool name and input summary
- [ ] Click Allow → agent continues; file written on disk
- [ ] Sidebar status returns to `◯ idle` or `✓ done`

## Diff view
- [ ] Press `3` or select View ▸ Diff
- [ ] DiffStat list shows the written file
- [ ] Click file → hunk view renders `+` lines
- [ ] Stage button stages the hunk; Discard reverts it

## Theme switch
- [ ] Settings panel → change theme to `tokyo-night`
- [ ] Colors update immediately
- [ ] Reopen Settings → `tokyo-night` still selected (persisted)

## Desktop notification
- [ ] Background the window; ask agent for a slow operation
- [ ] OS desktop notification appears on turn completion

## Cleanup
- [ ] Command palette → Remove workspace → ConfirmDialog
- [ ] Confirm → workspace gone from sidebar and registry
- [ ] Close GUI → no crash, no orphaned process
```

- [ ] Commit: `docs: add manual smoke checklist for cockpit release gate`

- [ ] **Step 3 — Phase summary commit**

```sh
git commit --allow-empty -m "feat: Phase 5 complete — integration gates, quality checks, docs, smoke checklist"
```

<!-- TASKS:END -->
