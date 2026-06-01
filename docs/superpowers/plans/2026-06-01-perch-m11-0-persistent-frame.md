# M11-0 — Persistent-Frame Switcher (the core build)

> Sub-plan of `2026-06-01-perch-v1-finalization.md`; topology proven in
> `2026-06-01-perch-core-model-spike.md` (swap-pane + per-agent sessions, SOUND on tmux 3.6).
> Execute via subagent-driven-development. Branch `feat/perch-v1`. Every commit must build +
> test green; **keep the old picker/`attachTo` path until the swap path is integration-green**
> (each commit revertable). Default tmux socket only (NOT FD-01 private socket — it breaks
> `perch status set`). Test seam: §20.1 Runner/FakeRunner + private-socket real-tmux integration
> (`Tmux{Socket:…}` like `internal/tmux/integration_test.go`); model-state teatest for TUI state.

**Goal:** replace the one-shot picker (`↵` = `switch-client`/`attach`, "everything else is gone")
with a persistent frame: a `perch` tmux session = sidebar pane (the TUI) + main pane showing the
selected agent LIVE; switching = swap-pane (no re-resume); `q` swaps the displayed agent home then
kills the frame; agent sessions persist, independently attachable.

## ⚠️ Hard constraints / traps (from advisor + audit)
1. **NEVER reintroduce `capture-pane -e`.** Spike §6 (line 530) suggests `-e` for non-focused
   previews — DO NOT. `TestCapturePane_NeverUsesEscapeFlag` forbids it; `-e` re-emits untrusted
   agent escapes (V5). Keep `-p` only.
2. **Quit MUST swap the displayed agent home before `kill-session perch`** — otherwise the agent
   process dies with the frame (DATA LOSS; spike Q8). Integration-test the survival.
3. **Frame-identity guard (FD-03):** an existing session named `perch` must not be clobbered. Mark
   the frame with a `@perch_frame` option; on bootstrap reuse only if the marker is present, else
   error clearly.
4. **Agent reflow on swap is the top unverified risk.** Make "pre-resize the parked agent session
   to the frame-main dimensions before swap-in" MANDATORY, and issue a post-swap refresh nudge
   (`refresh-client`). Headless-verify with a REAL curses app (vim/top), not cat/sleep.
5. **Address every post-swap tmux op by `pane_id` (`%N`), never `session:window`** (spike Risk 2).
6. **Exactly ONE placeholder pane** for the frame's lifetime (created at bootstrap; never a 2nd).
7. Tests must be hermetic; integration tests use a private socket; NEVER touch the user's default
   tmux server; NEVER run the real `perch setup` / write real `$HOME`.

## Swap state machine (the heart)
Model tracks: `mainPaneID` (frame main slot, stable), `placeholderPaneID` (the one disposable
pane; lives in the frame when nothing is displayed, else in the displayed agent's home session),
`displayedPaneID` (agent pane currently in the frame main slot, or "" if none).

`swapIn(agentHomePaneID)`:
- if `agentHomePaneID == displayedPaneID` → **no-op** (already showing).
- if `displayedPaneID == ""` (nothing shown): pre-size agent session → `swap-pane -s
  agentHomePaneID -t placeholderPaneID`. Now agent pane is in the frame; placeholder migrated to
  the agent's home session. Set `displayedPaneID = agentHomePaneID`. (Note: after swap the agent's
  pane id is unchanged — pane ids are stable across swap; only window/session ownership changes.)
- else (agent X displayed, switch to Y): **two-step** — (1) `swap-pane -s displayedPaneID -t
  placeholderPaneID` (X home, placeholder back to frame); (2) pre-size Y; `swap-pane -s
  agentHomePaneID(Y) -t placeholderPaneID` (Y into frame, placeholder to Y's home). Set
  `displayedPaneID = Y`.
- after any swap-in: `refresh-client` nudge so the agent repaints.

`swapHome()` (quit / before kill): if `displayedPaneID != ""` → `swap-pane -s displayedPaneID -t
placeholderPaneID` (agent back home, placeholder back to frame). Then frame can be killed safely.

Guard: a `swapping bool` (mirrors existing `capturing`/`polling`) serializes swaps in the Update
loop so concurrent selection changes can't cross.

---

## Tasks

### T1 — tmux frame/swap primitives (`internal/tmux`)
**Files:** `internal/tmux/connect.go` (or a new `frame.go`), `internal/tmux/*_test.go`,
`internal/tmux/integration_test.go`.

Add (argv via `ExecArgs`, no shell):
- `SwapPane(ctx, srcPaneID, dstPaneID string) error` → `swap-pane -s <src> -t <dst>` (pane ids,
  `%`-prefixed; validate non-empty).
- `SplitWindow(ctx, target, dir string, horizontal bool, cmd string) (paneID string, error)` →
  `split-window -d {-h|-v} -P -F '#{pane_id}' -t <target> -c <dir> [cmd]` (used to build the frame
  main pane; returns its pane id).
- `ResizeWindow(ctx, session string, w, h int) error` → `resize-window -t <session> -x W -y H`.
- `ResizePane(ctx, paneID string, w, h int) error` → `resize-pane -t <pane> -x W -y H`.
- `RefreshClient(ctx) error` → `refresh-client` (post-swap repaint nudge).
- `PaneSize(ctx, paneID string) (w, h int, err error)` → `display-message -p -t <pane>
  '#{pane_width}\x1f#{pane_height}'` (to pre-size parked sessions to the frame main size).

**Unit (FakeRunner):** each builds the exact argv (assert `.Calls`); empty/`-`-leading ids rejected.
**Integration (private socket):**
- swap-pane across two sessions keeps BOTH alive and BOTH pids alive (mirror spike Q3).
- **reflow gate:** start a real curses app (`vim` or `top`) in an 80×24 detached session; pre-size
  + swap it into a 120-wide frame main pane; `capture-pane -p`; assert lines are wider than 80 (the
  app reflowed). Skip the test if `vim`/`top` is absent (`t.Skip`), but prefer to run it.

### T2 — swap state machine in the Model (pure state)
**Files:** `internal/tui/frame.go` (new), `internal/tui/frame_test.go`; small additions to
`app.go` Model (fields `mainPaneID`, `placeholderPaneID`, `displayedPaneID`, `swapping bool`,
`frameSession string`).

Pure planner: `planSwap(displayed, placeholder, mainPaneID, targetHome string) -> []swapOp` (a
swapOp = {src,dst}) implementing the three cases above; and `planSwapHome(displayed, placeholder)`.
No tmux calls — returns the op list. Model-state teatest: each case yields the right op sequence;
no-op when target == displayed; swap-home empty when nothing displayed.

### T3 — wire Enter → swapIn; quit → swapHome+kill (`internal/tui`)
**Files:** `internal/tui/launch.go` (add `swapCmd` running the planned ops via the Tmux dep +
pre-size + refresh; keep `attachTo` for now), `internal/tui/app.go` (Enter dispatches `swapInCmd`
for a live agent instead of `attachTo`; new `swappedMsg`; `q` dispatches `swapHomeCmd` then
`tea.Quit`/`kill-session`), `internal/tui/run.go` (thread frame pane ids in — see T4).

**FakeRunner unit:** Enter on a live session → emits the expected `swap-pane` argv (case-correct);
quit with an agent displayed → emits `swap-pane` (home) then `kill-session perch`.
**Integration (private socket) — the data-loss gate:** display an agent, run the quit sequence,
assert the agent session SURVIVES and its `pane_pid` is still alive (NOT killed with the frame).

### T4 — frame bootstrap + `--sidebar` entrypoint (`cmd/perch/main.go`, `internal/tui/run.go`,
maybe `internal/frame`)
The default `perch` invocation becomes the bootstrapper; `perch --sidebar` runs the TUI inside the
frame's sidebar pane.

Bootstrap (`perch`, no subcommand):
1. Resolve frame session name (`perch`, or `perch_<hash>`? default `perch`).
2. If `HasSession("perch")`: verify the `@perch_frame` marker (FD-03). Present → reuse (attach/
   switch). Absent → error: "a tmux session named 'perch' exists and is not a perch frame".
3. Else build: `new-session -d -s perch -n frame -c <root> "perch --sidebar"`; mark sidebar pane
   `@perch_frame=1`; `SplitWindow` main pane with `sleep infinity` placeholder; `resize-pane`
   sidebar to ~50 cols.
4. Attach: outside tmux → `exec tmux attach-session -t perch`; inside tmux → `switch-client -t
   perch` (same default server). Reuse existing `AttachArgs`/`SwitchClient`.

`perch --sidebar` (inner): run the existing TUI (`tui.Run`) but inject the frame context — the
sidebar reads `$TMUX_PANE` (its own id) and finds the sibling main pane id via `ListPanes` on the
current window (the non-sidebar pane). Pass `mainPaneID`/`placeholderPaneID` into the Model.
Placeholder pane id at bootstrap == main pane id (the placeholder starts in the frame main slot).

**Unit:** bootstrap argv builders (new-session/split/resize/attach) FakeRunner-tested; FD-03 guard
(marker present → reuse path; absent → error) tested. The live attach handshake itself is
tty-dependent → morning smoke-run (document; not headless-testable).
**Keep `perch` (old direct-TUI) reachable** via a hidden flag or until T6 so nothing breaks
mid-series; flip the default to bootstrap only once T3 is integration-green.

### T5 — resurrect crash-stranded-pane reconcile (`internal/resurrect`)
If perch crashed while an agent was displayed, the agent pane was in the frame; killing the dead
frame later destroys it. New reconcile rule: a shadow-record pane absent from `list-panes -a` whose
home session is also gone → mark stranded; attempt re-launch (or at least report distinctly from
the boot-id path). Hermetic + private-socket integration test.

### T6 — remove dead picker/handoff code
Once T3 is integration-green and T4 flips the default: delete the now-dead `attachTo` handoff
branch (the `switch-client`/`attach` "everything else is gone" path) and any `liveTarget`-by-coord
usage superseded by pane-id swaps. Keep `AttachArgs`/`SwitchClient` (used by bootstrap). Update
help/keys/footer text (Enter = "open in main"; add the prefix-nav hint). Update README §11 layout +
keybindings (M13 will expand). Full gate green.

## DoD M11-0
Persistent frame works: sidebar stays, selecting shows the agent live in the main pane, switching
swaps without re-resume, `q` swaps-home + kills frame, agents survive (integration-proven).
M5/M6/M8/M9 still pass. All gates green (build, `go test`, `-race`+integration on real git/tmux 3.6,
lint 0, gofmt, `go mod verify`, `make vulncheck`). Old handoff code removed. **Morning report**
carries the human smoke-run script + the prefix-to-refocus-sidebar FD for user sign-off.
