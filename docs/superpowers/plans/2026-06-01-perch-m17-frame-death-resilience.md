# perch M17 — Frame Death-Resilience

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Fresh implementer per task + two-stage review (spec, then quality). Steps use checkbox (`- [ ]`) syntax.

**Goal:** The persistent frame must survive an agent exiting while displayed. Today, exiting/Ctrl-C'ing a displayed agent destroys the frame's main pane, permanently breaking the frame (every later launch silently drops to full-screen/legacy mode). Fix it so the frame self-heals and reopening resumes cleanly.

**Architecture:** Three mechanisms, all verified empirically on tmux 3.6 (private socket):
1. **Prevent collapse** — `set-option -w -t <frameWindow> remain-on-exit on`. When a displayed agent exits, its pane stays as a dead pane (`pane_dead=1`) instead of vanishing; the frame keeps both panes.
2. **Recover on death** — when the displayed agent's pane is dead, run `planSwapHome` (swap the live placeholder back into the frame slot — it returns under its original pane id, so `placeholderPaneID` is unchanged), which exiles the dead pane to the agent's home window, then `kill-pane` the dead pane (topology-safe: removes only that pane/window, never sibling agents). Reset `displayedPaneID=""`, focus the sidebar. The agent then shows idle → reopening relaunches `--resume` in a fresh session (resume is confirmed working).
3. **Self-heal on reuse** — `reuseFrame` repairs a damaged frame: a 1-pane frame (legacy damage, e.g. a frame broken by the pre-M17 binary) gets its placeholder re-split; a dead main pane gets respawned.

**The orphan trap (do NOT regress this):** `swap-pane` to display an agent EXILES the placeholder into the agent's home session, where it keeps running. When the agent dies, only the frame-slot pane dies; the placeholder is orphaned ALIVE in the home session. A respawn-only "fix" leaks it AND breaks that agent's resume (perch's `Connect` would find the orphaned `sleep` pane and type `claude --resume` into it — keystrokes to nowhere). The recovery MUST reconcile the orphan. `planSwapHome` does exactly this: it brings the orphaned placeholder back and exiles the dead pane, which we then `kill-pane`.

**Verified facts (tmux 3.6, private socket — do not re-litigate):**
- No `remain-on-exit`: displayed agent exits → frame window drops to 1 pane (placeholder destroyed). CONFIRMED.
- `set-option -w -t frame:frame remain-on-exit on`: dead agent pane persists (`dead=1`), frame keeps 2 panes. CONFIRMED. Window scope also guards the sidebar pane (acceptable — a TUI crash leaves a recoverable dead pane instead of collapsing the window).
- After death, the placeholder `%P` is STILL ALIVE in the agent's home session; that session still exists. CONFIRMED via `list-panes -a`.
- `swap-pane -s %P -t %A` SUCCEEDS when `%A` is `dead=1` (exit 0): live placeholder returns to the frame, dead pane exiled. CONFIRMED.
- `respawn-pane -k -t %X 'sleep infinity'` revives a dead pane with a STABLE pane id. CONFIRMED.
- Post-recovery the frame is reusable (another swap works). CONFIRMED.

**Tech stack:** Go, tmux 3.6. §20.1: all tmux via `proc.Runner`; unit tests use `FakeRunner` asserting `.Calls`; integration tests (`//go:build integration`) use a PRIVATE tmux socket. `internal/tui` coverage-exempt but still behaviorally tested.

---

## File Structure
- `internal/tmux/connect.go` — new primitives `KillPane`, `RespawnPane`, `SetWindowOption`; confirm `ListPanes` exposes `Dead`.
- `internal/frame/frame.go` — `createFrame` sets `remain-on-exit on`; `reuseFrame` self-heals.
- `internal/tui/launch.go` — death-recovery cmd; rewrite `closeWindowCmd` dead-path; `swapInCmd` records the displayed agent's home session if needed.
- `internal/tui/app.go` — tick detection of a dead displayed pane → recovery; Model field for the displayed home (if needed for kill — but prefer pane-id-only).
- `internal/tui/data.go` / refresh tick — surface `pane_dead` for the displayed pane.
- Docs: `CHANGELOG.md`, `ARCHITECTURE.md` (frame lifecycle), `docs/diagrams/frame-swap.mmd` if it asserts lifecycle.

---

## Task 1: tmux primitives — KillPane, RespawnPane, SetWindowOption

**Files:** `internal/tmux/connect.go`; tests in the connect test file (mirror `SetSessionOption`/`SelectPane` tests).

- [ ] **Step 1 — failing tests** (FakeRunner, assert `.Calls`):
  - `KillPane(ctx, "%3")` → `kill-pane -t %3`.
  - `RespawnPane(ctx, "%3", "sleep infinity")` → `respawn-pane -k -t %3 sleep infinity` (the command is a SINGLE argv element passed through; confirm how the existing code passes a command string to split-window/Launch and mirror it — `sleep infinity` is ONE shell command. Pass it as one arg: `respawn-pane -k -t %3 "sleep infinity"`).
  - `SetWindowOption(ctx, "perch:frame", "remain-on-exit", "on")` → `set-option -w -t perch:frame remain-on-exit on`.
  - Error path for each (wrapped error).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement**, mirroring `SetSessionOption`/`SelectPane` (Runner routing, error wrap). Signatures:
  ```go
  func (o Tmux) KillPane(ctx context.Context, target string) error
  func (o Tmux) RespawnPane(ctx context.Context, target, cmd string) error
  func (o Tmux) SetWindowOption(ctx context.Context, windowTarget, key, val string) error
  ```
  argv: `["kill-pane","-t",target]`; `["respawn-pane","-k","-t",target,cmd]`; `["set-option","-w","-t",windowTarget,key,val]`.
  Also confirm `ListPanes` returns a `Dead bool` per pane (it's used in `Connect`); if there's no exported way to get a single pane's dead state, add `PaneDead(ctx, target) (bool, error)` via `display-message -t <target> -p '#{pane_dead}'` (returns "1"/"0") + test.
- [ ] **Step 4 — run, confirm pass.** Full `internal/tmux` green + vet + gofmt.
- [ ] **Step 5 — commit:** `refactor(tmux): add KillPane, RespawnPane, SetWindowOption primitives (M17-1)`

---

## Task 2: Frame window gets remain-on-exit

**Files:** `internal/frame/frame.go` (`createFrame`); test `frame_test.go`.

In `createFrame`, immediately after the split that creates the main pane (frame.go ~152) and before the session-option block, add (best-effort `_ =`):
```go
// remain-on-exit: when a displayed agent exits, its pane stays as a dead pane
// instead of destroying the frame's main slot. M17 death-recovery respawns/reclaims it.
_ = t.SetWindowOption(ctx, frameTarget, "remain-on-exit", "on")
```
`frameTarget` is the existing `tmux.WindowTarget(session, frameWindow)` local.

- [ ] **Step 1 — failing test:** extend the createFrame call-recording test to assert `set-option -w -t perch:frame remain-on-exit on` is issued (after the split, before/around the other session options).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement.**
- [ ] **Step 4 — run, confirm pass.** `internal/frame` + `internal/tmux` green.
- [ ] **Step 5 — commit:** `feat(frame): set remain-on-exit on the frame window so a dying agent can't collapse it (M17-2)`

---

## Task 3: reuseFrame self-heals a damaged frame

**Files:** `internal/frame/frame.go` (`reuseFrame`); test `frame_test.go`.

Today `reuseFrame` (frame.go ~91) finds the sidebar (FrameMarker=="1") and sets `mainID` to "whichever pane is not the sidebar" — leaving `mainID=""` if the frame has only 1 pane, and happily returning a dead main pane. Make it repair:

After identifying `sidebarID`, determine the main pane among the remaining panes:
- If there is a non-sidebar pane that is NOT dead → that's the main pane (`mainID`).
- If the only non-sidebar pane is DEAD → respawn it: `t.RespawnPane(ctx, deadID, placeholderCmd)`; `mainID = deadID` (id stable).
- If there is NO non-sidebar pane (1-pane frame — legacy damage) → re-split to recreate the placeholder: `mainID, err = t.SplitWindow(ctx, tmux.WindowTarget(session, frameWindow), <root/dir>, true, placeholderCmd)`. (Use the session's working dir; if not readily available, the sidebar pane's `#{pane_current_path}` or fall back to "."—match how createFrame sources `root`. If root isn't available in reuseFrame, derive a sensible cwd; document the choice.)
- Also (defensive) ensure `remain-on-exit on` is set on reuse too (idempotent): `_ = t.SetWindowOption(ctx, tmux.WindowTarget(session, frameWindow), "remain-on-exit", "on")` — so a frame created by an old binary gains the guard.

Return `Info{... MainPane: mainID, Created:false}` with a NON-empty `mainID` in all repaired cases.

- [ ] **Step 1 — failing tests** (FakeRunner-driven `reuseFrame`/`Ensure` on an existing session):
  - 2-pane healthy frame → returns the live non-sidebar pane as MainPane, NO split/respawn issued.
  - 1-pane frame (only sidebar) → issues `split-window` to recreate the placeholder; MainPane is the new pane (non-empty).
  - 2-pane frame with a DEAD main pane → issues `respawn-pane -k` on it; MainPane = that id.
  - reuse sets `remain-on-exit on` (idempotent).
  Mirror the existing reuseFrame/Ensure test harness (it already fakes ListPanes/GetPaneOption).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement.**
- [ ] **Step 4 — run, confirm pass.** `internal/frame` green.
- [ ] **Step 5 — commit:** `feat(frame): reuseFrame self-heals a 1-pane or dead-main frame (M17-3)`

---

## Task 4: Death recovery in the TUI (the core)

**Files:** `internal/tui/launch.go` (recovery cmd; rewrite `closeWindowCmd` dead-path), `internal/tui/app.go` (tick detection; Model wiring), `internal/tui/frame.go` (reuse `planSwapHome`); tests `launch_test.go`, `app_test.go`, and an integration test using the private tmux socket for kill-pane-on-dead-pane.

### Recovery semantics (pane-id-only — topology safe)
A displayed agent's pane (`displayedPaneID`) is DEAD when its process exited. Recovery:
1. `planSwapHome(displayedPaneID, placeholderPaneID)` → swap the live placeholder back into the frame slot (placeholder returns under its original id → `placeholderPaneID` UNCHANGED). The dead displayed pane is exiled to the agent's home window.
2. `KillPane(ctx, displayedPaneID)` → remove the now-exiled dead pane (its window closes; sibling agents/windows untouched; the agent's project session survives if it has other windows, else closes — either way the agent shows idle and reopens via `--resume`).
3. `SelectPane(ctx, "-L"-equivalent)` or focus the sidebar pane so the user lands on the list (not a blank slot). (Use the same select-pane the F12 binding uses, or target the sidebar pane id if tracked.)
4. Reset `displayedPaneID = ""`.

### Two entry points
- **Tick detection (primary):** the periodic refresh already reloads pane state. When `inFrame() && displayedPaneID != ""` AND that pane is dead (`PaneDead` / the reloaded pane list marks it dead), emit the recovery cmd. This catches the user exiting/Ctrl-C'ing the agent while focused in it — the frame self-heals on the next tick without any keypress.
- **closeWindowCmd (rewrite the M16 dead-path):** M16 currently, on a dead displayed pane, just resets `displayedPaneID` (leaves the orphan + a dead frame slot — INCOMPLETE). Replace with: if the displayed pane is ALIVE → swap home only (existing, agent keeps running, no kill); if DEAD → run the full recovery above (swap home + kill-pane + reset). Remove the old "skip swap-home, just reset" behavior.

### Notes
- Use only pane ids perch already tracks (`displayedPaneID`, `placeholderPaneID`); do NOT kill by session name (a project session may hold multiple agent windows). `kill-pane` on the specific dead pane is topology-safe.
- After recovery, the agent's window record is stale (points at the killed pane). The live-reload will show it idle; reopen → `launchCmd(resume:true)`. Confirm no code path treats the stale record as live after the pane is gone. If a stale-record bug surfaces, prune the record for that PaneKey as part of recovery (state.RemoveWindow or equivalent) — verify whether needed.

- [ ] **Step 1 — failing tests** (FakeRunner asserting `.Calls`):
  - Recovery on a dead displayed pane → issues `swap-pane` (planSwapHome) THEN `kill-pane -t <displayedPaneID>`, resets `displayedPaneID=""`, leaves `placeholderPaneID` unchanged, focuses the sidebar.
  - Tick with a dead displayed pane → triggers recovery (assert the recovery cmd/calls). Tick with a LIVE displayed pane → no recovery, no kill.
  - closeWindowCmd with a LIVE displayed pane → swap home only, NO kill-pane (agent survives). closeWindowCmd with a DEAD displayed pane → full recovery (swap + kill-pane). 
  - Regression: quitFrameCmd still swaps home then kills the FRAME session (order preserved); a dead displayed pane doesn't break quit.
  - **Integration test** (`//go:build integration`, private socket): build a real frame + agent, swap in, exit the agent (shell `exit`), confirm `kill-pane` on the dead pane succeeds and the frame is left with [sidebar, live placeholder] reusable. Mirror existing integration-test setup.
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement.**
- [ ] **Step 4 — run, confirm pass:** `go test ./internal/tui/` and `go test -tags=integration ./internal/tui/` (or wherever the frame integration test lives).
- [ ] **Step 5 — commit:** `feat(tui): recover the frame when a displayed agent dies (swap-home + kill dead pane) (M17-4)`

---

## Task 5: Docs

**Files:** `CHANGELOG.md`, `ARCHITECTURE.md`, `docs/diagrams/frame-swap.mmd` (if it documents lifecycle).

- [ ] **Step 1:** CHANGELOG `0.1.0 - Unreleased` — `Fixed`: the frame no longer collapses when a displayed agent exits; it self-heals (remain-on-exit + dead-pane recovery), and damaged frames repair on next launch.
- [ ] **Step 2:** ARCHITECTURE.md — document the frame lifecycle invariant ("the frame window always has exactly 2 panes: sidebar + main slot; the main slot is a live placeholder, a displayed agent, or a dead pane pending recovery") and the orphan-reconciliation rule (recovery brings the exiled placeholder back, then kills the dead pane).
- [ ] **Step 3:** If `docs/diagrams/frame-swap.mmd` shows swap-in/out, add the death→recover transition. Keep accurate.
- [ ] **Step 4:** Build + gofmt + `internal/tui`/`internal/frame` tests green.
- [ ] **Step 5 — commit:** `docs(m17): document frame death-resilience + lifecycle invariant`

---

## Closing gate
```bash
go build ./... && go vet ./... && test -z "$(gofmt -l internal/ cmd/)"
go test -race -tags=integration ./...
make lint && make vulncheck && go mod verify
make build && ls -lh bin/perch
```

## Self-review checklist
1. Displayed agent exits (Ctrl-C/`exit`) → frame keeps 2 panes (remain-on-exit), then recovers to [sidebar, live placeholder]. ✅
2. Recovery brings the EXILED placeholder back (no orphan, no leaked session) and kills only the dead pane (topology-safe `kill-pane`, never `kill-session`). ✅
3. After recovery the agent shows idle and reopening relaunches `--resume` cleanly (resume confirmed working). ✅
4. `reuseFrame` repairs a 1-pane (legacy-damaged) frame and a dead-main frame; sets remain-on-exit idempotently. ✅
5. `placeholderPaneID` is unchanged by recovery (placeholder returns under its original id). ✅
6. closeWindowCmd: alive → swap home only (agent survives); dead → full recovery. M16's incomplete dead-path is replaced. ✅
7. No `kill-session` of an agent's home session anywhere in recovery. ✅
8. Integration test proves kill-pane-on-dead-pane + reusable frame on a private socket. ✅
