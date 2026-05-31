# perch M4 — tmux control (sub-plan)

**Status:** DONE
**Milestone:** M4 (master plan `docs/superpowers/plans/2026-05-30-perch-v1-implementation.md` §M4).
**Branch:** `feat/perch-v1`.
**Execution:** subagent-driven-development — fresh implementer per task, two-stage review (spec then quality), fix loops, final whole-milestone review.

## Objective (master plan, verbatim)

> Create session/window in a dir, run a command, switch/attach, set/read `@perch_*`,
> write the window shadow record.

**DoD:** debug command opens a shell window in a chosen tree and round-trips a pane
option + shadow record; `make test-integration` green. §0 gate.

**Spec refs:** `plan.md §3` (tmux layout/conventions), `§6.2` (window shadow record —
**already implemented** in `internal/state`), `§7.2` (deferred cleanup), `§9` (option
storage), `§16.4`, `§20.4` (integration harness on `-L perch-test-<pid>`).

## What already exists (do NOT rebuild)

- `internal/state`: `SaveWindow`/`LoadWindows`/`RemoveWindow`, `EncodePaneKey`/`DecodePaneKey`
  (url.PathEscape), `StateDir()` (XDG only, no `$PERCH_*`). `model.Window` carries
  `PaneKey, Tool, SessionID, Tree, TmuxSession, TmuxWindow, BootID, Updated`. M4 *consumes*
  this layer — it does not touch it.
- `internal/proc`: `Runner{Run, RunInDir}`, `ExecRunner`, `FakeRunner` (records `Call{Name,Args,Dir}`,
  `Respond`, `FakeResult{Stdout,Stderr,Err}`). ExecRunner returns the process error verbatim
  (`*exec.ExitError`).
- `cmd/perch/main.go`: stdlib `switch` dispatch; `handleDebug` already routes `debug discover`.
  Output via injected `io.Writer`; real runner constructed at the handler.
- `Makefile`: `test`, `test-integration` (`-tags=integration`, already wired), `lint`
  (`go run …golangci-lint@v2.11.4`), `fmt`, `vet`, `coverage`. `internal/tmux/` exists but is **empty**.

## Evidence base (all probed on this host, tmux 3.6, private `-L` sockets)

Every claim below was verified empirically; no assumptions. Format-field names, exit
codes, and stderr shapes are from live `tmux 3.6` probes.

| # | Finding | Consequence for the build |
|---|---------|---------------------------|
| E1 | `list-sessions`/`has-session`/`display-message` against an absent socket → **exit 1**, stderr `error connecting to <socket> (No such file or directory)`. After `kill-server` → exit 1, stderr `no server running on <socket>`. | Cold-start is exit-1, not a special "no server" code. Classify by **exit code**, never stderr text. |
| E2 | `has-session -t=<name>`: hit → exit 0; miss (server up) → exit 1 `can't find session: <n>`; miss (no server) → exit 1 (connect error). | `HasSession`: exit 0 → true; exit ≥1 → `(false, nil)`; non-exit (exec) error → `(false, err)`. |
| E3 | `new-session -d -s <s> -n <w> -c <dir> -P -F '#{pane_id}'` → prints `%N`, exit 0. `new-window -t '=<s>' -n <w> -c <dir> -P -F '#{pane_id}'` → prints `%N`, exit 0. `-n` names the first window. | Capture pane id from create stdout. **Always pass `-n` and `-P -F '#{pane_id}'`.** |
| E4 | Window target: only **`=session:=window`** (anchor `=` on *both* parts) addresses a window deterministically. Prefix/fnmatch fallback is non-deterministic across name sets. | Window target builder = `"=" + session + ":=" + window`. Session target = `"=" + session`. |
| E5 | tmux **silently rewrites `.` and `:` → `_` in session names** (invisible collisions: `a.b` and `a:b` both become `a_b`, second create → `duplicate session`). Window names keep `.`/`:` literally but become **untargetable** (`.`=pane-index sep, `:`=session:window sep). `/` is safe. | Name derivation MUST sanitize: keep `[A-Za-z0-9_/-]`, map everything else (incl. `.` `:` whitespace) → `-`. Collapse repeats. |
| E6 | `send-keys`: a bare `;` arg or a trailing `;` in a keys string → tmux parses the rest as tmux commands (`unknown command: …`, exit 1). `-l` sends the string literally; send `Enter` as a **separate** call. | `SendKeys` = `send-keys -t <tgt> -l <literal>` then `send-keys -t <tgt> Enter`. Caller pre-quotes shell argv (M5). |
| E7 | Liveness: `#{pane_pid}` is always the shell pid (does not change when an agent runs); `#{pane_current_command}` changes (`bash`→`claude`/`node`/`bun`). `#{pane_dead}` only meaningful with `remain-on-exit on`. | Expose `#{pane_current_command}` in the `Pane` struct; the live-agent *semantic* is M7. |
| E8 | Pane-option round-trip: `set-option -p -t <tgt> @perch_session <v>` then `show-options -p -t <tgt> -v @perch_session` / `list-panes -a -F '#{@perch_session}'` → `<v>`. | Generic `SetPaneOption`/`GetPaneOption`; M4 round-trips `@perch_session`. |
| E9 | `kill-session` on the **last** session → server exits; a follow-up `kill-session` → exit 1 `no server running on …` (not `can't find session`). `kill-server` exits 0 but **leaves the socket file** on disk. | `KillSession`/`KillServer` tolerate "no server". Integration teardown also `os.Remove`s the socket. |
| E10 | `#{start_time}` (= BootID) requires a live server; cold call exits 1. | `BootID` documents/tests the cold-error path; callers ensure a server first. |
| E11 | `#{session_path}` (list-sessions) reflects `-c`; `#{pane_current_path}` is correct only in **list-panes**. | Read paths from `list-panes`, not `list-sessions`. |

## Design — `internal/tmux`

```go
type Tmux struct {
    Runner proc.Runner          // default proc.ExecRunner{}
    Bin    string               // default "tmux"
    Socket string               // "" → default server; non-empty → prepends "-L <socket>"
    Getenv func(string) string  // default os.Getenv; AttachArgs reads $TMUX
}
```

- `args(sub ...string)` prepends `-L <socket>` when `Socket != ""` (private-server support
  for the integration harness; this is the §20.4 isolation mechanism).
- Zero-value-safe seam helpers (`runner()`, `bin()`, `getenv()`) mirroring the adapter
  pattern; `var _` compile guards are not needed (no interface), but methods must not panic
  on the zero value.

Files (one responsibility each, §14):
- `tmux.go` — struct, `New()`, seams, `args()`, target builders (`SessionTarget`,
  `WindowTarget`), and **read/query** primitives + `Pane`.
- `names.go` — name derivation + `sanitize`.
- `connect.go` — **mutation** primitives + connect/attach.
- `cleanup.go` — deferred-cleanup builder (pure; not wired).

### proc prerequisite (M4-A, step 1)

Add to `internal/proc`:
```go
// ExitCode returns the process exit code from an *exec.ExitError (or anything
// exposing ExitCode() int), else -1. Lets callers branch on exit status
// (e.g. tmux has-session) without matching version-fragile stderr text.
func ExitCode(err error) int
```
and a test seam so unit tests can simulate an exit code:
```go
// FakeExitError is a proc.ExitCode-recognised error for FakeRunner responses.
type FakeExitError struct{ Code int }
func (e FakeExitError) Error() string  { ... }
func (e FakeExitError) ExitCode() int  { return e.Code }
```
`ExitCode` uses `errors.As` against an `interface{ ExitCode() int }` so both
`*exec.ExitError` (production) and `FakeExitError` (tests) satisfy it.

---

## Tasks

### M4-A — proc.ExitCode + tmux package core (reads, names, targets)

**Files:** `internal/proc/proc.go` (+ `proc_test.go`), `internal/tmux/tmux.go`,
`internal/tmux/names.go`, `internal/tmux/tmux_test.go`, `internal/tmux/names_test.go`,
`internal/tmux/testdata/tmux/{list-panes-with-options.txt,list-panes-empty.txt}`.

1. `proc.ExitCode` + `proc.FakeExitError` (above). Unit-test: `*exec.ExitError` path
   (construct via a real failing command in a test helper, or assert `FakeExitError`),
   `FakeExitError` path, and non-exit error → `-1`.
2. `Tmux` struct + `New()` + seams + `args()` (socket prefixing — assert via FakeRunner
   `Call.Args` that `-L <socket>` is prepended when set, absent when not).
3. Target builders: `SessionTarget(s) = "=" + s`; `WindowTarget(s, w) = "=" + s + ":=" + w` (E4).
4. `names.go`: `sanitize(string)` (keep `[A-Za-z0-9_/-]`, map others → `-`, collapse
   repeat `-`, trim leading/trailing `-`); `SessionName(projectPath)` =
   `sanitize(filepath.Base(projectPath))`; `WindowName(treeOrBranch)` = `sanitize(...)`.
   Table-test the E5 cases: `next.js → next-js`, `release-1.2 → release-1-2`, `a:b → a-b`,
   names with `/`, spaces, empty/dot-only. Document the residual collision risk (distinct
   inputs can sanitize to the same name → tmux `duplicate session`, surfaced by Connect).
5. Read primitives via `Runner`:
   - `HasSession(ctx, name) (bool, error)` — `has-session -t=<name>`; classify by
     `proc.ExitCode` (E2): exit 0 → true; exit ≥1 → `(false, nil)`; `ExitCode == -1`
     (exec failure) → `(false, err)`.
   - `BootID(ctx) (string, error)` — `display-message -p '#{start_time}'`; trim; wrap
     error with stderr; document cold-error (E10).
   - `Pane` struct `{ID, PID, Command, Dead bool, Path, Session, Window, PerchSession string}`.
   - `paneFormat` constant using `\x1f` (unit separator) between fields and `\n` between
     panes (robust against spaces in paths, E11). `parsePanes([]byte) []Pane` — skip blank
     / short lines, never panic.
   - `ListPanes(ctx, sessionTarget) ([]Pane, error)` — `list-panes -t <tgt> -F <paneFormat>`.
   - `ListPanesAll(ctx) ([]Pane, error)` — `list-panes -a -F <paneFormat>` (resurrect/admin
     batched read, §7.4/§9). Empty output → `nil, nil`.
   - `CapturePane(ctx, target string, scrollback int) (string, error)` —
     `capture-pane -t <tgt> -p`; when `scrollback > 0` add `-S -<n>` (E: verified).
   - `GetPaneOption(ctx, target, key) (string, error)` — `show-options -p -t <tgt> -v <key>`; trim.
6. Fixtures: `list-panes-with-options.txt` (≥2 panes, `\x1f`-delimited, includes a populated
   `@perch_session`); `list-panes-empty.txt` (zero bytes). Tests assert exact `Call.Args`
   (command form) AND parsed structs.

**Acceptance:** `make test vet lint` green; `internal/tmux` + `internal/proc` ≥80%; zero-value no panic.

`[DIVERGENCE D1]` plan.md §3 "does not require you to pre-start a tmux server" is functionally
true but implies graceful cold probing — actual cold-start is exit-1 with two distinct stderr
strings. Resolved by exit-code classification (E1/E2). → correct §3 at closeout.

`[DIVERGENCE D2]` plan.md never specifies name sanitization; E5 proves `.`/`:` break both
session storage and window targeting. → add the sanitize rule to §3 at closeout.

---

### M4-B — mutations + connect/attach

**Files:** `internal/tmux/connect.go`, `internal/tmux/connect_test.go`.

- `NewSession(ctx, session, window, dir) (paneID string, err error)` —
  `new-session -d -s <session> -n <window> -c <dir> -P -F '#{pane_id}'`; trim stdout → pane id (E3).
- `NewWindow(ctx, session, window, dir) (paneID string, err error)` —
  `new-window -t <SessionTarget> -n <window> -c <dir> -P -F '#{pane_id}'`; trim (E3).
- `SendKeys(ctx, target, literal string) error` — two calls: `send-keys -t <tgt> -l <literal>`
  then `send-keys -t <tgt> Enter` (E6). Doc: caller passes already-shell-quoted command text
  (argv quoting is M5); `-l` defeats tmux key-name/`;` parsing.
- `SetPaneOption(ctx, target, key, val) error` — `set-option -p -t <tgt> <key> <val>` (E8).
- `KillSession(ctx, name) error` — `kill-session -t <SessionTarget>`; tolerate "no server"
  via `proc.ExitCode` (E9): treat exit ≥1 as already-gone → nil (document), exec error → err.
- `KillServer(ctx) error` — `kill-server`; tolerate already-down.
- `AttachArgs(session string) []string` — **pure** builder: `Getenv("TMUX") != ""` →
  `["switch-client", "-t", SessionTarget]` else `["attach-session", "-t", SessionTarget]`
  (executed via `tea.ExecProcess` in M5, not here).
- `Connect(ctx, session, window, dir string) (paneID string, err error)` — `HasSession` →
  absent: `NewSession`; present: `NewWindow`. Returns the created pane id. (Window-already-exists
  reuse is M5/M6 — document the M4 scope: Connect always creates a window.)

Tests (FakeRunner): assert exact `Call.Args` for every command form (esp. the **two** SendKeys
calls and the `-l` flag, E6; `=`-anchored targets, E4; `-P -F '#{pane_id}'`, E3); pane-id parse;
`AttachArgs` both branches via injected `Getenv`; `Connect` create-session vs create-window paths;
Kill* "no server" tolerance.

**Acceptance:** `make test vet lint` green; ≥80%.

`[DIVERGENCE D3]` plan.md §3 connect sequence says "launch via send-keys" without escaping;
E6 proves bare/trailing `;` breaks it. → note the `-l`+separate-Enter rule in §3 at closeout.

---

### M4-C — deferred-cleanup builder (§7.2, built not wired)

**Files:** `internal/tmux/cleanup.go`, `internal/tmux/cleanup_test.go`.

Pure builder for the §7.2 ordered self-close one-liner dispatched (later, M6) via
`tmux run-shell`. Order is **mandatory**: `sleep 0.3 → switch away → kill source window →
mv tree to .perch_trash_<h>_<ts> sibling → git worktree prune → (optional) git branch -d →
rm -rf trash`.

```go
type CleanupOpts struct {
    SourceWindowTarget string // "=session:=window" to kill
    SwitchToTarget     string // session to switch the client to first
    Tree               string // absolute worktree path to remove
    Branch             string // "" → skip `git branch -d`
}
// CleanupScript returns the /bin/sh one-liner. now and trashSuffix are injected
// for determinism (§20.1 no-clock). The script is one shell-command string, so
// internal ';' are shell separators (run-shell takes a single command arg) — the
// E6 send-keys hazard does not apply here.
func CleanupScript(o CleanupOpts, now int64, trashSuffix string) string
// RunShellArgs wraps a script as the tmux argv: []string{"run-shell", script}.
func RunShellArgs(script string) []string
```

- `<h>` = `trashSuffix` (caller passes a short fnv-of-tree hash or paneKey; do NOT compute
  time/random inside — keep pure/testable).
- Trash path = `filepath.Join(filepath.Dir(Tree), ".perch_trash_"+trashSuffix+"_"+ts)`.
- Table-test: order preserved; branch omitted when empty; the rename-to-trash precedes prune;
  no bare/trailing `;` arg boundaries (it is a single string, but assert the assembled order).

**Not wired** — no method executes it in M4 (M6 owns Remove). State so in the doc comment.

**Acceptance:** `make test vet lint` green; 100% (pure).

---

### M4-D — `debug tmux` command + integration tests

**Files:** `cmd/perch/main.go` (+ `cmd/perch/*_test.go` if a pure formatter is added),
`internal/tmux/integration_test.go` (`//go:build integration`).

1. `handleDebug`: route `args[0] == "tmux"` → `handleDebugTmux(args[1:], stdout, stderr)`.
   Update the `handleDebug` usage line. Keep `debug` out of `printUsage` (diagnostic).
2. `handleDebugTmux(args, stdout, stderr)`:
   - Resolve tree path (arg or `os.Getwd`), `os.Stat` it is a dir (else exit 2).
   - Real `tmux.New()` (default socket), real `proc.ExecRunner{}`.
   - Derive `SessionName(<project>)` / `WindowName(<tree base>)`; `Connect` → pane id.
   - `SetPaneOption(WindowTarget, "@perch_session", "<synthetic-id>")`; `GetPaneOption` →
     assert equal; on mismatch exit 1.
   - `BootID`; build `model.Window{PaneKey:paneID, Tool:..., SessionID:syntheticID, Tree:tree,
     TmuxSession:session, TmuxWindow:window, BootID:bootID, Updated:now}`; `state.SaveWindow`
     into the real `state.StateDir()`; `state.LoadWindows` → confirm the record reads back.
   - Print pane id, option round-trip result, and the shadow-record path. Exit 0.
   - Defensive: any tmux/state error → print to stderr, exit 1 (never panic). This is the DoD demo.
3. Integration tests (`internal/tmux/integration_test.go`, `//go:build integration`):
   - **Reusable harness** `newTestServer(t) *tmux.Tmux` — socket `perch-test-<pid>`; sets
     `Socket`; `t.Cleanup`: `KillServer` then `os.Remove(socketPath)` (E9). M5–M7 reuse it.
   - `TestConnect_OptionRoundTrip` — `t.TempDir`; `Connect` → pane id non-empty;
     `SetPaneOption`(`@perch_session`) → `GetPaneOption` matches; `ListPanesAll` contains the pane.
   - `TestNewWindow_SecondAgent` — `NewSession` then `NewWindow` in the same session →
     two distinct pane ids, both addressable via `WindowTarget` (E4).
   - `TestShadowRecord_RoundTrip` — `SaveWindow` into `t.TempDir` baseDir →
     `LoadWindows` returns the record with matching fields.
   - `TestBootID_Live` — after `NewSession`, `BootID` returns a parseable integer string.
   Keep strictly within M4 DoD: **no** resurrect (M7) / remove (M6) cases.
4. Verify `make test` (no tmux needed) AND `make test-integration` (real tmux) both green.

**Acceptance:** `make test test-integration vet lint` green; debug command demonstrates the DoD.

`[DIVERGENCE D4]` plan.md §20.4 teardown is `kill-server` only; E9 shows the socket file
persists → harness also `os.Remove`s it. → add to §20.4 at closeout.

---

## Closeout (after final whole-milestone review)

1. Correct `plan.md`:
   - §3: cold-start is exit-1 classified by exit code (D1); name sanitization rule for `.`/`:`
     and the safe set (D2); window target form `=session:=window` (E4); pass `-n` + `-P -F
     '#{pane_id}'` on create (E3); `send-keys -l` + separate `Enter` (D3); liveness key is
     `#{pane_current_command}` not `pane_pid` (E7); read paths from `list-panes` not
     `list-sessions` (E11).
   - §7.2: cleanup is built-not-wired in M4 (M6 executes).
   - §9: **flag** the internal `@perch_status` window-vs-pane contradiction for M8 to resolve
     (M4 round-trips `@perch_session`, pane-scoped, unambiguous — does not touch the contradiction).
   - §20.4: integration teardown also removes the leftover socket file (D4).
2. Mark M4 `✅ DONE` / `[x]` in the master plan with a verification note (commit chain, coverage,
   integration result, plan↔reality corrections, deferred items).
3. Update `.tool-versions`? — no change (tmux 3.6 already pinned). Confirm no version string
   duplicated outside `.tool-versions` (§0 supply-chain gate).
4. README — deferred per `project_perch_deferred_decisions` (~M5/pre-release); do not add.
5. Checkpoint: report green gates, then stop for review before M5.

## Deferred (documented — do NOT pull into M4)

- Live-agent detection semantic (`#{pane_current_command}` matching) → M7 resurrect.
- `switch-client`/`attach` **execution** (`tea.ExecProcess`) → M5.
- Agent-argv shell quoting before `SendKeys` → M5.
- Window-reuse on Connect (don't always create) → M5/M6.
- Cleanup builder **execution** + worktree Remove flow → M6.
- `@perch_status`/`@perch_pane_status` window-vs-pane reconciliation + status pipeline → M8.
- resurrect reconcile (boot_id two-track) → M7.
