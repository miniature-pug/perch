# perch M16 — Idiot-Proof Navigation + Non-Destructive Close/Reopen

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Fresh implementer per task + two-stage review (spec, then quality). Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make perch's frame navigable by a non-tmux-user: you can always get back to the list, closing a window never kills the agent, and reopening shows the session cleanly — never the post-Ctrl-C wreckage.

**Architecture:** Three session-scoped tmux options on the frame (verified safe, zero global pollution, die with the session) + one new in-process perch action. Navigation is intentionally **asymmetric**: sidebar→agent and "close" are perch actions (perch has focus in the sidebar, owns the state); agent→sidebar is the *only* path needing a tmux-level key, because perch isn't running in the agent pane. We do NOT inject keystrokes into the TUI — the tmux key does plain `select-pane`, leaving `displayedPaneID` valid and the live-preview model intact.

**Tech stack:** Go, bubbletea v1.3.10, tmux 3.6. All shell-outs via `proc.Runner`; unit tests use `FakeRunner` and assert `.Calls` (§20.1) — never spawn tmux. `internal/tui` is coverage-exempt but still gets behavioral tests where pure.

**Verified facts this plan rests on (do not re-litigate):**
- tmux 3.6 session-scoped custom key-table: `bind-key -T perchnav F12 select-pane -L` + `set-option -t perch key-table perchnav` → F12 fires only in `perch`, **all other keys pass through to the app**, other sessions unaffected. (Empirically confirmed on a private socket.)
- `set-option -t perch mouse on` is per-session (does not bleed). Caveat: mouse-on means text drag-select needs Shift — document it.
- `set-option -t perch status on` consumes exactly **1 row** of window height — account for it in the agent-pane resize.

**Out of scope / gated follow-up (do NOT build here):** repeated `--resume` id-stability hardening. Only the dead-agent recovery edge case touches `--resume`; the dominant close→reopen loop swaps a *live* agent and never resumes. Gate: during smoke-run, `ls ~/.claude/projects/<proj>/` — if no `<perch-generated-uuid>.jsonl` exists, claude isn't honoring `--session-id` and a hook-based id re-capture becomes a separate task. Not now.

---

## File Structure

- `internal/tmux/connect.go` — add `SetSessionOption` and `BindKey` (Runner-routed, mirror `SetPaneOption`).
- `internal/frame/frame.go` — in `createFrame`, after the split/resize, apply the three session options (status, mouse, key-table+bind). New consts for the nav key, key-table name, status text.
- `internal/tui/keys.go` — repurpose `ClearFilter`/add close semantics; help text.
- `internal/tui/app.go` — `closeWindowCmd`; extend the `esc` handler and `activateSelected` (focus-already-displayed case); help/footer.
- `internal/tui/launch.go` — `closeWindowCmd` swap-home logic (reuse `planSwapHome`); graceful dead-pane handling shared with `quitFrameCmd`.
- `internal/tui/frame.go` — (if needed) a `planClose` mirroring `planSwapHome` semantics, or reuse directly.
- Docs: `README.md`, `internal/tui/help.go`, `CHANGELOG.md`.

---

## Task 1: tmux session-option + bind-key primitives

**Files:** Modify `internal/tmux/connect.go`; Test `internal/tmux/connect_test.go` (or the existing tmux test file that exercises `SetPaneOption`).

Mirror the existing `SetPaneOption` (connect.go:57) exactly — same Runner routing, same error wrapping, same context handling.

- [ ] **Step 1 — failing tests** (FakeRunner, assert `.Calls`):
  - `SetSessionOption(ctx, "perch", "mouse", "on")` issues exactly `set-option -t perch mouse on`.
  - `SetSessionOption(ctx, "perch", "status", "on")` issues `set-option -t perch status on`.
  - `BindKey(ctx, "perchnav", "F12", "select-pane", "-L")` issues `bind-key -T perchnav F12 select-pane -L`.
  - Error propagation: a FakeRunner returning an error surfaces a wrapped error (match the `SetPaneOption` style).
- [ ] **Step 2 — run, confirm fail** (`go test ./internal/tmux/ -run 'SessionOption|BindKey' -v` → undefined funcs).
- [ ] **Step 3 — implement** both methods next to `SetPaneOption`. Signatures:
  ```go
  // SetSessionOption sets a session-scoped tmux option (set-option -t <session>).
  func (o Tmux) SetSessionOption(ctx context.Context, session, key, val string) error
  // BindKey binds a key in a named key-table (bind-key -T <table> <key> <cmd...>).
  func (o Tmux) BindKey(ctx context.Context, table, keyName string, cmd ...string) error
  ```
  Build argv as `[]string{"set-option", "-t", session, key, val}` and `append([]string{"bind-key", "-T", table, keyName}, cmd...)`. Route through the same Runner the package already uses.
- [ ] **Step 4 — run, confirm pass.**
- [ ] **Step 5 — commit:** `refactor(tmux): add SetSessionOption + BindKey primitives (M16-1)`

---

## Task 2: Frame wires status bar, mouse, and the focus-return key-table

**Files:** Modify `internal/frame/frame.go` (`createFrame`, consts); Test `internal/frame/frame_test.go`.

New consts in `internal/frame` (centralized — M15 discipline):
```go
const (
	navKeyTable   = "perchnav"   // session-scoped custom key-table name
	focusListKey  = "F12"        // prefix-less key: return focus to the sidebar
	// statusFormat: left-aligned hint line shown on the frame's status bar.
	statusLeft    = " perch │ F12/click ▸ list   ↵ ▸ open/resume   esc ▸ close window   q ▸ quit (agents live) "
)
```

In `createFrame`, AFTER the sidebar resize (frame.go:153) and BEFORE returning `Info`, apply (order: bind the table key first, then point the session at it):
1. `t.BindKey(ctx, navKeyTable, focusListKey, "select-pane", "-L")`
2. `t.SetSessionOption(ctx, frameSession, "key-table", navKeyTable)`
3. `t.SetSessionOption(ctx, frameSession, "mouse", "on")`
4. `t.SetSessionOption(ctx, frameSession, "status", "on")`
5. `t.SetSessionOption(ctx, frameSession, "status-left-length", "200")`
6. `t.SetSessionOption(ctx, frameSession, "status-left", statusLeft)`
7. `t.SetSessionOption(ctx, frameSession, "status-right", "")`

All best-effort (`_ =`), consistent with the existing `_ = t.ResizePane(...)` — frame must still come up if a cosmetic option fails. Do NOT fail `createFrame` on these.

**Status-row height:** because status is now on, the main pane is 1 row shorter than the raw window. The agent-pane sizing in `swapInCmd` (Task 4 territory) must size to the **measured** main-pane height, not arithmetic — verify in Task 4. Here, just leave a `// NOTE: status bar consumes 1 row; agent reflow sizing uses measured pane height (see swapInCmd).`

- [ ] **Step 1 — failing test:** Drive `createFrame` (or `Ensure` on a fresh session) with a FakeRunner/recording tmux double and assert the recorded calls include, in order: the `bind-key -T perchnav F12 select-pane -L`, then `set-option -t perch key-table perchnav`, then the `mouse on` / `status on` / `status-left ...` calls. Reuse the existing frame_test harness pattern (it already asserts `new-session`/`split-window`/`resize-pane`).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement** in `createFrame`.
- [ ] **Step 4 — run, confirm pass.** Also run the full `internal/frame` + `internal/tmux` suites green.
- [ ] **Step 5 — commit:** `feat(frame): status bar + mouse + F12 focus-return key on the perch frame (M16-2)`

---

## Task 3: Non-destructive close-window action

**Files:** Modify `internal/tui/launch.go` (new `closeWindowCmd`), `internal/tui/app.go` (esc handler), `internal/tui/frame.go` (reuse `planSwapHome`); Test `internal/tui/launch_test.go`, `internal/tui/app_test.go`.

`closeWindowCmd` is `quitFrameCmd` minus the kill and minus the quit:
- If `displayedPaneID == ""` → no-op (nothing displayed); return `nil`.
- Else compute `planSwapHome(m.displayedPaneID, m.placeholderPaneID)` and issue the swap so the agent returns to its home session (alive). Then return a `tea.Msg` that resets `m.displayedPaneID = ""` (so the frame shows the placeholder again and reopen math is correct).
- **Graceful dead-pane:** if the swap-home errors because the displayed pane no longer exists (agent died while displayed), do NOT propagate as fatal — still reset `displayedPaneID=""` and continue. Extract the "swap home if the pane is alive, else just reset" logic into a shared helper so `quitFrameCmd` uses it too (a dead displayed pane must not block quit either).

esc handler in `app.go` (extend the existing `ClearFilter` case — keep the key `esc`):
```
case key.Matches(msg, m.keys.ClearFilter):
    if m.list.FilterState() == list.Filtering || m.list.IsFiltered() {
        // existing: clear/reset the filter
    } else if m.inFrame() && m.displayedPaneID != "" {
        return m, m.closeWindowCmd()
    }
    // else: no-op
```
(Confirm the exact filter-state predicate against the current code; preserve current clear-filter behavior unchanged.)

Update the `ClearFilter` help to read contextually — keep `esc` with help `"close window / clear filter"`.

- [ ] **Step 1 — failing tests:**
  - `closeWindowCmd` with a displayed pane issues a `swap-pane` (assert via FakeRunner `.Calls`) matching `planSwapHome`, and the resulting msg resets `displayedPaneID` to `""`.
  - `closeWindowCmd` with `displayedPaneID == ""` issues no tmux calls and is a no-op.
  - Dead-pane: swap-home returns an error → state still resets to `displayedPaneID=""`, no panic, no fatal.
  - esc with no active filter + a displayed pane → triggers close; esc while filtering → clears filter (unchanged); esc with nothing displayed and no filter → no-op.
  - **Regression:** `quitFrameCmd` still swaps home then kills, and now also survives a dead displayed pane (shared helper).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement** `closeWindowCmd` + shared swap-home-if-alive helper + esc wiring.
- [ ] **Step 4 — run, confirm pass** (`go test ./internal/tui/ -v`).
- [ ] **Step 5 — commit:** `feat(tui): non-destructive close-window (esc) — detach view, keep agent alive (M16-3)`

---

## Task 4: Reopen routes cleanly — focus-if-displayed, swap-if-live, resume-if-dead; redraw

**Files:** Modify `internal/tui/app.go` (`activateSelected`), `internal/tui/launch.go` (`swapInCmd` redraw/height); Test `internal/tui/app_test.go`, `internal/tui/launch_test.go`.

Extend `activateSelected` (app.go:675) decision tree, in frame mode:
1. Selected session **is already the displayed one** (`it.captureTarget`/pane == `m.displayedPaneID`) → do NOT re-swap. Issue `select-pane -R` (focus the agent that's already shown). Add a tiny `focusAgentCmd()` that runs `select-pane -R` (or targets the displayed pane id). This is the "go back into the agent I was just looking at" path.
2. Selected is a **different live** session → existing `swapInCmd(it.captureTarget)`.
3. Selected is **idle/dead** → existing `launchCmd(resume:true)` (unchanged — this is the gated `--resume` path; leave as-is).

**Redraw / status-row height in `swapInCmd`:** it currently calls `ResizeWindow(targetHome, w, h)` then `RefreshClient`. Ensure `h` is the **measured** main-pane height (the frame's main pane, status row already excluded by tmux), NOT a raw window height — otherwise the agent reflows by one row under the new status bar. Use the existing pane-size measurement path; if `w/h` are computed arithmetically anywhere, switch to the measured main-pane dims. Do NOT add `send-keys C-l` — a real size change already generates SIGWINCH and the TUI repaints; `RefreshClient` nudges the client. Only if a manual test still shows residual garbage, force SIGWINCH with a 1-row resize nudge (`h-1` then `h`), never a keystroke.

- [ ] **Step 1 — failing tests:**
  - Activate the already-displayed session → issues `select-pane` (focus), NOT a `swap-pane`. Assert via `.Calls`.
  - Activate a different live session → still `swap-pane` (existing behavior intact).
  - Activate an idle session → still goes through the resume launch path (existing behavior intact).
  - `swapInCmd` sizes the agent window to the measured main-pane height (assert the `resize-window`/`resize-pane` target height equals the measured value, not a hardcoded one).
- [ ] **Step 2 — run, confirm fail.**
- [ ] **Step 3 — implement** the focus-if-displayed branch + height fix.
- [ ] **Step 4 — run, confirm pass.**
- [ ] **Step 5 — commit:** `feat(tui): reopen focuses the already-shown agent; status-row-aware resize (M16-4)`

---

## Task 5: Help, footer, README, CHANGELOG

**Files:** `internal/tui/help.go`, `internal/tui/app.go` (footer if it lists keys), `README.md`, `CHANGELOG.md`.

- [ ] **Step 1:** `help.go` ShortHelp/FullHelp:
  - `esc` → `"close window / clear filter"`.
  - Add a documentation-only synthetic binding: `key.WithHelp("F12 / click", "focus list (from agent)")` (never matched in Go; matches the existing `prefix ←/→` doc-binding pattern). Keep or drop the old `prefix ←/→` line — F12 supersedes it as the primary; keep prefix as a secondary doc line for power users.
  - Note `↵` already reads "open" — update to "open / resume / focus".
- [ ] **Step 2:** README "Navigating the frame" section — rewrite to lead with the idiot-proof model:
  - "Click any pane to focus it (mouse is on)." / "Press **F12** from inside an agent to jump back to the list." / "Press **esc** in the list to close the open agent's window — the agent keeps running; reopen it any time with `↵`." / "`q` quits perch; your agent sessions survive." / Note: text selection with the mouse needs **Shift** (tmux mouse mode). / Power users: the tmux prefix + arrows still work.
  - Remove any now-wrong "use the tmux prefix to navigate" framing as the *primary* instruction.
- [ ] **Step 3:** `CHANGELOG.md` under `0.1.0 - Unreleased`: add the close-window / focus-return / status-bar / mouse entries.
- [ ] **Step 4:** Build + `test -z "$(gofmt -l internal/ cmd/)"`; run `internal/tui` tests green.
- [ ] **Step 5 — commit:** `docs(m16): document idiot-proof frame navigation (close/F12/mouse/status bar)`

---

## Closing gate

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l internal/ cmd/)"
go test -race -tags=integration ./...
make lint && make vulncheck && go mod verify
make build && ls -lh bin/perch
```

## Self-review checklist

1. Focus-return (F12) and mouse work from inside the agent; typing inside the agent is NOT swallowed (verified mechanic; key-table only intercepts F12). ✅
2. `esc` closes the displayed window **without** killing the agent; the agent is reachable again via `↵`. ✅
3. Reopen of an already-displayed agent focuses it (no double-swap); a different live agent swaps in; an idle one resumes. ✅
4. Status bar shows the keys; agent reflow accounts for the status row (no 1-row glitch). ✅
5. A dead displayed pane breaks neither close nor quit (shared swap-home-if-alive helper). ✅
6. No keystroke injection into the TUI anywhere. ✅
7. All session options are session-scoped (`-t perch`) — no global tmux pollution; verified per-session. ✅
8. Smoke-run gate noted for the `--resume` id question; no speculative resume fix built. ✅
```
