# M5 — TUI open flow (sub-plan)

Status: **IN PROGRESS**
Branch: `feat/perch-v1`
Master plan: `docs/superpowers/plans/2026-05-30-perch-v1-implementation.md` (M5 block)
Spec refs: `plan.md §1, §2, §11, §12, §13, §16.5, §18.3, §20.1, §20.4, §20.5`

## Objective (verbatim, master plan M5)

> unified two-pane list (project→tree→session), fuzzy filter, live preview, `↵`
> runs the right resume/new command in a tmux window.

Deliverables: `internal/tui` (`app.go`, `list.go`, `keys.go`, `styles.go`, viewport
preview via `tmux capture-pane`). `tea.ExecProcess` for attach; plain `tea.Cmd`
goroutines for non-interactive tmux queries (never block Update/View — §1).

DoD (`plan.md §16.5`): resume a real claude and a real opencode session from the list.

## Scope boundary (M5 vs later)

`plan.md §11` describes the full interaction model; **M5 implements only the open
subset** — selector, fuzzy filter (`/`), live preview, `↵` resume/attach, `n` new.
Deferred to later milestones (NOT M5, do not build):
- `w` new-worktree session, `d` remove worktree → M6 (worktree lifecycle + cleanup execution).
- `x` kill → M6.
- `:` command bar, debounce, `?` help overlay, confirm modals, `z`/`Z` screen modes, theme pass → M9.

## Evidence base

All versions verified against the Go module proxy on 2026-05-31. tmux behaviour
carried from M4 evidence (`docs/superpowers/plans/2026-05-30-perch-m4-tmux.md`).

| ID | Fact | Source |
|----|------|--------|
| E1 | `bubbletea v1.3.10` (released 2025-09-17, min Go 1.24.0); module path has **no** v2 — v1 only. | proxy `/@latest` |
| E2 | `lipgloss v1.1.0` (2025-03-12, min Go 1.18); v1 only. | proxy `/@latest` |
| E3 | `bubbles v1.0.0` (2026-02-09, min Go 1.24.2); v1 only. `bubbles v1.0.0`'s own go.mod requires exactly the bubbletea/lipgloss pins above — the trio is **mutually pinned**, do not bump independently. | proxy `/@latest` + plan.md §2 |
| E4 | All three pins are >30 days old (oldest-released bubbles is ~111 days), stable, satisfy the §0/§2 supply-chain rule. Go 1.26.2 toolchain exceeds every min-Go. | proxy + .tool-versions |
| E5 | `teatest` = `github.com/charmbracelet/x/exp/teatest`; version is an `x/exp` **pseudo-version**, resolved via `go get` at M5 and pinned to the exact string `go get` returns. The ≥30d-stable rule does NOT apply to `x/exp` pseudo-versions (test-only, plan §20.5 carved it out). | plan.md §20.5, master M5 |
| E6 | `tea.ExecProcess(c *exec.Cmd, fn ExecCallback) tea.Cmd` **blocks** the program, hands the terminal to `c`, resumes on exit, then delivers `fn(err)`'s return value as a `tea.Msg`. `ExecCallback func(error) tea.Msg`. **The callback MUST return a non-nil `tea.Msg` — returning `nil` panics.** | bubbletea v1.3.10 `exec.go` / pkg.go.dev |
| E7 | Non-interactive shell-out (e.g. `capture-pane`) = a plain `tea.Cmd` (a `func() tea.Msg`) that runs the command off the UI goroutine and returns a result `Msg`. Never blocks Update/View. | bubbletea v1.3.10 docs |
| E8 | `attach-session` blocks until detach → genuine terminal handover → **`tea.ExecProcess`**. `switch-client` is a fire-and-forget server command that returns instantly → **plain `tea.Cmd`**, same class as `capture-pane`. Wrapping switch-client in ExecProcess does a pointless renderer teardown and restores into a pane the client already left. | tmux semantics + advisor; plan.md §12 dichotomy |
| E9 | `Tmux.AttachArgs(session)` returns `switch-client -t =<s>` when `$TMUX` set, else `attach-session -t =<s>` (subcommand+flags only). Full argv = `Tmux.ExecArgs(AttachArgs(session)...)` (binary + `-L socket` + sub). `AttachArgs` doc forbids exec'ing its result directly. | internal/tmux/connect.go:97-116, tmux.go:80-86 |
| E10 | `new-window`/`new-session` (no `-d` on new-window in `Connect`) make the new window the session's **current** window, so `attach`/`switch-client -t =session` lands on the just-created window. Holds for the single-launch flow; revisit if window-reuse (deferred) lands. | internal/tmux/connect.go:25-32,123-132 |
| E11 | Launch order: `Connect` (window+pane id) → `SendKeys` agent argv (`-l` literal + separate `Enter`) → attach. send-keys into a freshly-spawned shell relies on the pty buffering input before shell init completes — robust in practice (claude-squad does this); add a one-line comment at the call site. | M4 evidence E6, connect.go SendKeys |
| E12 | `shellQuote(s)` (POSIX single-quote, `'`→`'\''`) already exists in `internal/tmux/cleanup.go:27`; M5-1 reuses it to quote each agent-argv token. | internal/tmux/cleanup.go:27 |
| E13 | `bubbles/list` has built-in fuzzy filter (`sahilm/fuzzy`): `SetFilterText`, `VisibleItems()`, custom `ItemDelegate`. `Model.View() string`, keys are `tea.KeyMsg`, `lipgloss.AdaptiveColor` is a self-detecting struct literal (v1 API). | plan.md §12 |
| E14 | Existing consumables: `tmux.{Connect,CapturePane,AttachArgs,ExecArgs,SendKeys,SessionName,WindowName}`; `agent.Adapter.{ResumeArgs,NewArgs,ListSessions,ForkInto}`, `agent.NewOpts`; `state.{LoadState,SaveState,SortedPaths,BumpProject,AgeProjects,SaveWindow,StateDir}`; `discover.Projects`, `git.{ListWorktrees,ToTrees}`; `model.{Project,Tree,Session,Window,Tool}`. | scoping report §4 |
| E15 | Deferred items M5 closes, **co-located with their consumer task**: (a) ctx-threading through `Claude.ListSessions(_ ctx)` → data-loading task; (b) opencode `session list` is project-scoped → set `Opencode.Dir` per tree via `proc.RunInDir` (plan §18.3) → data-loading task; (c) frecency bump on select (`BumpProject`/`AgeProjects`/`SaveState`) → ↵-handler task. | master plan deferred notes; plan.md §18.3 |
| E16 | TUI is **excluded** from the ≥80% coverage gate (`internal/tui/` carve-out, plan §20.1). TUI tested via teatest **model-state assertions, never rendered strings** (§20.5): key seq → model state; `/foo` reduces `VisibleItems()`, `Esc` clears. No golden/screenshot tests. | plan.md §20.1, §20.5 |
| E17 | `newTestServer(t)` integration harness already exists (`internal/tmux/integration_test.go`, `//go:build integration`), shared M5–M7. M5's real-launch integration test reuses it. | plan.md §20.4, M4 |
| E18 | **No direct pane→session key exists today.** `model.Window{PaneKey(=tmux pane id "%17"), SessionID, Tool, Tree, TmuxSession, TmuxWindow, BootID, Updated}`. `@perch_session` is currently written ONLY by `debug tmux` as the synthetic `"perch-debug-"+paneID` — NOT a real agent session id. `Pane.PerchSession` = field 7 of `paneFormat` (`#{@perch_session}`). `EncodePaneKey`=`url.PathEscape` of the raw pane id. `model.Session.ID` = claude transcript-basename UUID / opencode JSON `id`. The only existing bridge is the shadow record: `Pane.ID==Window.PaneKey → Window.SessionID==Session.ID`. | investigation, internal/model/model.go:54-93, cmd/perch/main.go:186-195, state.go:121-136 |

## Design decisions (load-bearing)

- **D1 — attach primitive split (E8):** ↵ handler branches on `$TMUX`. Inside tmux → build `switch-client` argv via `ExecArgs(AttachArgs(session)...)` and run as a **plain `tea.Cmd`** (fire-and-forget, returns a `switchedMsg`). Outside tmux → run the `attach-session` argv via **`tea.ExecProcess`** (blocking handover), callback returns a non-nil `attachFinishedMsg{err}`. One code path decides the primitive; never wrap switch-client in ExecProcess.
- **D2 — launch builder is tmux-package, agent-agnostic:** `tmux.Launch(ctx, session, window, dir string, argv []string)` does `Connect` → quote argv (`shellQuote` per token, joined by space) → `SendKeys`. It takes a pre-built `[]string` argv so the tmux package never imports `agent` (no import cycle). The ↵ handler obtains argv from the adapter (`ResumeArgs`/`NewArgs`) and passes it in.
- **D3 — no standalone deps step (E5, advisor):** the Charm pins are added by the **scaffold task** (M5-2) — the first code that imports them — so `go mod tidy` does not strip them; vendor + `go.sum` committed in that same task.
- **D4 — honest DoD (advisor):** the automated integration test proves the **launch path** (Connect → quote → SendKeys → command executes) with a sentinel command via `newTestServer` + `CapturePane` poll (M4 pattern). The "resume a **real** claude/opencode session" clause is **manual**: requires the agent binaries installed AND ≥1 pre-existing session of each. Closeout reports automated-vs-manual-vs-deferred explicitly; the DoD is not marked green off the sentinel test alone.
- **D5 — Model takes injected data:** the scaffold (M5-2) accepts items via constructor (testable with fixtures, no real processes). Live data loading (discover/git/agent/frecency) lands in M5-3 as `tea.Cmd`s. Keeps the scaffold's teatest unit tests process-free per §20.1.
- **D6 — live/idle join via `@perch_session`, live-server-only (advisor + E18):** M5-3 determines "running" **solely from the live server** — `ListPanesAll`, filtering out `Dead` panes. The pane→session join is **direct**: a session is live iff a live pane's `PerchSession` equals the session's `ID`; that pane's `ID` is the capture target. This requires the launch path to stamp the **real** agent session id into `@perch_session` — so **M5-4's handler calls `SetPaneOption(target,"@perch_session",sessionID)` after `Launch`** (the generic `Launch`/M5-1 stays argv-only; it does NOT set the option). `LoadWindows`/shadow records are **NOT** consulted for liveness (that, plus boot_id/staleness/pruning/resurrect, is M7). Status is **binary: live vs idle** — no working/waiting/done glyphs (those need `@perch_status`, M8). Documented M5 limitation: sessions not launched by perch (no `@perch_session`) fall back to idle/static; external-session adoption is later. For a `new` (`n`) launch the agent assigns its id at runtime, so `@perch_session` may be deferred/empty for new sessions — resume (the DoD path) always knows the id.

## Tasks

### M5-1 — agent-argv shell-quoting + `tmux.Launch` builder
- **Files:** `internal/tmux/launch.go`, `internal/tmux/launch_test.go`.
- **Build:** `func (o Tmux) Launch(ctx, session, window, dir string, argv []string) (paneID string, err error)` — `Connect` to get the window+pane id, join `shellQuote`-d argv tokens into one literal, `SendKeys` it into the pane (one-line comment re E11 pty buffering). Reject empty `argv` (return error, never panic). Reuse the existing `shellQuote` (E12).
- **Tests (FakeRunner, no real proc):** records the exact `Connect` + `SendKeys` `Call.Args`; verifies argv with spaces/quotes/`$`/`;` is single-quoted so tmux cannot reinterpret it; empty-argv error path; Connect-error short-circuits before SendKeys.
- **DoD:** builder pure + seamed; ≥80% coverage on the new file; `make fmt vet lint test` green. No new deps.

### M5-2 — TUI scaffold + Charm deps (selector + preview + filter, injected data)
- **Deps (D3):** `go get` the exact pins `github.com/charmbracelet/bubbletea@v1.3.10`, `github.com/charmbracelet/lipgloss@v1.1.0`, `github.com/charmbracelet/bubbles@v1.0.0`; `go get` teatest and **record the exact pseudo-version** it resolves (E5). `go mod tidy` (safe now — code imports them), `go mod vendor`, commit `go.mod`/`go.sum`/`vendor/`.
- **Files:** `internal/tui/app.go` (`Model`, `New(...)`, `Init`, `Update`, `View`), `internal/tui/list.go` (item type + `ItemDelegate`: status glyph + tool + relative time; fuzzy filter wiring), `internal/tui/keys.go` (`key.Binding` set: `j/k`, `/`, `Esc`, `↵`, `n`, `q`), `internal/tui/styles.go` (lipgloss `AdaptiveColor` struct literals, two-pane + footer layout via `JoinHorizontal/JoinVertical`).
- **Behaviour:** two panes — left `bubbles/list` (grouped, `▸` highlight), right `bubbles/viewport` detail/preview. Navigation, `/` fuzzy filter, `Esc` clears filter, `q` quits. **No launch, no live data** — items injected via `New`. View returns `string` (E13).
- **Tests (teatest, model-state only — E16):** key seq → model state; `/foo` reduces `VisibleItems()`; `Esc` restores; `j/k` moves selection. **Never assert rendered strings.** No golden tests.
- **DoD:** builds with `-mod=vendor`; teatest model tests green; `make fmt vet lint test` green; `go mod verify` clean; deps exact-pinned + vendored.

### M5-3 — live data loading + capture-pane preview
- **Files:** extend `internal/tui/app.go`; add `internal/tui/data.go` (load `tea.Cmd`s) + tests.
- **Build:** `tea.Cmd`s (E7) that load projects/trees/sessions — `discover.Projects` + `git.ToTrees` + adapter `ListSessions` — into list items, sorted by `state.SortedPaths` frecency (E14). **Live/idle join (D6):** one `tmux.ListPanesAll` snapshot → drop `Dead` panes → index live panes by `PerchSession`; mark an item **live** iff its session `ID` is in that index, storing that pane's `ID` as the capture target. **Liveness comes ONLY from the live pane set — do NOT call `LoadWindows`** (E18/D6). Status is **binary** (one live glyph, one idle glyph) — no working/waiting/done. Preview: on selection-change, a `tea.Cmd` runs `tmux.CapturePane(captureTarget,...)` for a **live** item only (idle → static detail from item fields), result → `previewMsg` → `viewport.SetContent`. Gate in-flight capture with a bool; never block Update/View.
- **Deferred closures (E15):** thread `context.Context` through `Claude.ListSessions` (drop the `_ ctx`); set `Opencode.Dir` per tree so `session list` is project-scoped (plan §18.3). Both land here, with their consumer.
- **Tests (FakeRunner, hit the join hardest — advisor):** loader `tea.Cmd`s tested with FakeRunner-backed adapters/discover (no real proc). Join test cases: canned `ListPanesAll` where some panes' `PerchSession` match fixture session IDs (→ live + correct capture target), a `Dead` pane (→ not live), a session with no matching pane (→ idle), and cold-server/empty `ListPanesAll` (→ everything idle). Preview-message handling asserted on model state, never rendered strings.
- **DoD:** live data populates the list; live/idle join correct against the live-pane snapshot; preview updates on highlight for live items; ctx + Opencode.Dir closures done; `make fmt vet lint test` green.

### M5-4 — ↵ launch / attach + `n` new + frecency bump
- **Files:** extend `internal/tui/app.go`; `internal/tui/launch.go` (handler glue) + tests.
- **Build:** `↵` on a session → `adapter.ResumeArgs(id)`; `n` → `adapter.NewArgs(NewOpts{...})` (implement **both** — advisor). Handler: `tmux.Launch(...)` (M5-1) to spawn+send (returns the pane id), **then stamp `@perch_session` (D6):** `SetPaneOption(paneID,"@perch_session",sessionID)` so the M5-3 live/idle join + preview can match this pane to the agent session. For **resume** the session id is known and always stamped; for **new** the agent assigns its id at runtime → stamping may be deferred/empty (documented, D6). Then attach per **D1/E8** — `$TMUX` set → `switch-client` argv as a plain `tea.Cmd` returning `switchedMsg`; unset → `attach-session` argv via `tea.ExecProcess`, callback returns **non-nil** `attachFinishedMsg{err}` (E6). Argv for attach built as `ExecArgs(AttachArgs(session)...)` (E9) — never exec AttachArgs directly. On select, frecency bump: `state.LoadState` → `BumpProject` + `AgeProjects` → `SaveState`; write the §6.2 window shadow record via `state.SaveWindow` (E15c).
- **Tests (teatest + FakeRunner):** ↵ on a running session issues `Launch` then the correct attach primitive for `$TMUX` set vs unset (assert the chosen argv / Cmd shape via injected `Getenv`); `n` issues `NewArgs`-derived launch; frecency `SaveState` called with bumped project; ExecProcess callback returns non-nil. Assert model state, not rendered output.
- **DoD:** ↵ resume + `n` new wired with correct attach primitive both in/out of tmux; frecency persisted; shadow record written; `make fmt vet lint test` green.

### M5-5 — cmd wiring + integration test + closeout
- **Files:** `cmd/perch/main.go` (route bare `perch` / default to the TUI, `tmux.New()`, real data sources); `internal/tui/integration_test.go` (`//go:build integration`).
- **Build:** running `perch` with no subcommand starts the TUI program (`tea.NewProgram(...).Run()`), wired to real `discover`/`git`/`agent`/`state`/`tmux`. Errors → stderr + non-zero, never panic.
- **Integration test (D4, reuse `newTestServer` E17):** drive `tmux.Launch` with a **sentinel** argv (`["sh","-c","echo PERCH_$((6*7))"]` or similar arithmetic marker), poll `CapturePane` ≤2s for the computed marker (`PERCH_42`) — proves the launch path **executes**, not just that keys landed. Also `SetPaneOption(@perch_session, <fixtureID>)` then verify `ListPanesAll` reports that pane with `PerchSession==fixtureID` and the M5-3 join marks it live (proves the D6 live/idle path end-to-end against a real server, not just FakeRunner fixtures). (Mirrors the M4 SendKeys integration test.)
- **Closeout (D4 honesty):**
  - Run `make fmt vet lint test` and `make test-integration`; confirm both green, `go mod verify` clean, gofmt clean, coverage ≥80% on `internal/` excluding `internal/tui/`.
  - **Verify before claiming the real-agent DoD:** are `claude` / `opencode` binaries actually installed? Are there ≥1 pre-existing sessions of each to resume? Report exactly what was automated (sentinel launch path), what was manually verified (real resume, if binaries+sessions present), and what remains manual/deferred. Do NOT mark the DoD green off the sentinel alone.
  - Correct `plan.md` from any M5 evidence divergences; update the master plan M5 block to ✅ DONE with a verification note (commit chain, coverage, gates, deferred items closed: frecency bump, ctx-threading, Opencode.Dir, attach execution + argv quoting).
  - Update `.tool-versions`/README/Makefile/install.sh only if affected.

## Deferred AFTER M5 (record, do not build)
- `w` worktree-new, `d` remove-worktree, `x` kill → M6 (cleanup execution wires `CleanupScript`/`RunShellArgs`).
- `:` command bar, `?` help overlay, confirm modals, `z`/`Z` screen modes, theme/polish → M9.
- Window-reuse on `Connect` (attach to existing window instead of always-new) → M6.
- resurrect/reconcile (boot_id two-track), live-agent `#{pane_current_command}` semantic → M7.
- `@perch_status` window-vs-pane reconciliation + status pipeline → M8.

## Closeout checklist
- [ ] M5-1 launch builder + tests (green, reviewed)
- [ ] M5-2 scaffold + Charm deps vendored + teatest nav/filter (green, reviewed)
- [ ] M5-3 live data + preview + ctx/Opencode.Dir closures (green, reviewed)
- [x] M5-4 ↵/`n` launch + attach split + frecency bump (green, reviewed)
- [ ] M5-5 cmd wiring + sentinel integration test + closeout docs (green, reviewed)
- [ ] Final whole-milestone review
- [ ] `make fmt vet lint test` + `make test-integration` green; `go mod verify` clean
- [ ] plan.md + master plan corrected; M5 marked DONE; deferred items recorded
