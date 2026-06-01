# M7 — Recovery: `perch resurrect` (boot_id reconcile) (sub-plan)

Status: **DONE**
Branch: `feat/perch-v1`
Master plan: `docs/superpowers/plans/2026-05-30-perch-v1-implementation.md` (M7 block, lines 307-314)
Spec refs: `plan.md §7.4` (reconcile algorithm), `§6.2` (`windows/<paneKey>.json` shadow + `boot_id`), `§17` (no-daemon / one-shot), `§20.1/§20.4` (testing), `§10` (CLI verb).

## Objective (verbatim, master plan M7)

> boot_id reconcile rebuilds windows after a tmux server restart.

DoD (master M7 / `plan.md` item 7 / `§20.4`): after killing the test tmux server, `perch resurrect`
rebuilds the windows from shadow records (integration test: write shadow record → kill server
(new socket) → `perch resurrect` → window recreated, stale record removed/recreated properly).

## Scope boundary (M7 vs later)

M7 ships the **CLI-only** `perch resurrect`: the reconcile engine (`internal/resurrect`) + the
`cmd/perch` handler + unit (FakeRunner) + integration (real git + private-socket tmux) tests.
Explicitly **deferred** (record at checkpoint, do NOT build now):

- **Auto-offer resurrect on TUI start when stale records are detected** (`plan.md §7.4` last para,
  `§17`) → **M9** (UX layer). Master item 7's *Done when* is the CLI only; the TUI offer needs the
  M9 modal/confirm surface. Mirrors M6's deferral of generalized modals.
- `@perch_status` / live status reconcile (`§9`, the *other* `list-panes -a` reconcile at line 587)
  → **M8**. M7 touches only the window-shadow reconcile, not the status pipeline.
- No background process / watcher — one-shot reconcile only (`§17`, non-negotiable).

---

## Fixed decisions (M7)

Resolved by advisor before authoring; treat as constraints, do NOT re-litigate.

- **FD1 — single snapshot, then decide, then mutate.** Capture `currentBoot` (via `Tmux.BootID`,
  `""` when the server is down) and `livePanes` (`Tmux.ListPanesAll`, `nil` when down) **once** at
  the top. Classify every record against that frozen snapshot; only then execute prunes/restores.
  A cold/absent server makes `currentBoot==""` → every record lands in the RESTORE branch and **no
  prune happens** (prune requires `bootMatch`), which is exactly correct after a reboot.
- **FD2 — match the live pane by `pane_id`, not `@perch_session`.** The shadow file is keyed by
  `pane_id`; `pane_id` is stable within one server lifetime and is reassigned across a restart, so
  "no `pane_id` match" *is* the restart signal. `@perch_session` is used only for the live-window
  guard (FD4), never for the keep/prune decision.
- **FD3 — three-case classification.** Per record `w`, with `paneByID` = a live **non-dead** pane
  whose `ID == w.PaneKey`, and `bootMatch = currentBoot != "" && w.BootID == currentBoot`:
  - `paneByID && bootMatch` → **KEEP** (healthy; no I/O).
  - `!paneByID && bootMatch` → **PRUNE** (intentionally closed; `RemoveWindow`). Spec-mandated delete.
  - `!bootMatch` → **RESTORE branch** (subject to FD5 guards).
- **FD4 — live-window guard is REQUIRED (not spec-tidiness).** `Tmux.Launch` → `Connect`, which
  **reuses an existing live pane** in the target window. So before launching a restore, scan the
  `livePanes` snapshot for a **non-dead** pane with `Session == w.TmuxSession && Window ==
  w.TmuxWindow`; if found, **skip** (do not Launch) — otherwise the resume command would be
  `send-keys`-typed into a running pane. No extra tmux call (reuses the snapshot). This is what
  `§7.4` step-4 "skip restore if the handle already exists" protects.
- **FD5 — RESTORE-branch guards, in order (cheap → expensive):**
  1. `w.SessionID == ""` (opencode-new; never `@perch_session`-stamped) → **skip:empty-sid**. Cannot
     deterministically resume; the opencode session persists in opencode's own store and is reachable
     via the normal TUI discovery path. Do **NOT** fresh-launch — that would start an unrelated
     session while claiming to "restore."
  2. live-window guard (FD4) hits → **skip:window-live**.
  3. `os.Stat(w.Tree)` is `ErrNotExist` → **skip:tree-gone** (worktree dir removed).
  4. `git -C w.Tree worktree list --porcelain` (via `git.ListWorktrees`):
     - runner error while the dir exists → **skip:git-error** (treated as *transient*, see FD6).
     - find the worktree whose `Path` is an ancestor-or-equal of `w.Tree` (descendant match — the
       agent may have `cd`'d into a subdir). If that match is the `MainWorktree` → **skip:main**
       ("run in main" windows are not auto-resurrected — too intrusive; the session is still
       reachable via the TUI). No descendant match at all → **skip:no-worktree-match** (*transient*).
  5. else → **RESTORE**: `Launch(w.TmuxSession, w.TmuxWindow, w.Tree, adapter.ResumeArgs(w.SessionID))`
     → new `paneID`; `SetPaneOption(paneID, "@perch_session", w.SessionID)`; `RemoveWindow(w.PaneKey)`;
     `SaveWindow` with the **new** `PaneKey`, `BootID =` the boot read *after* the server is up,
     `Updated = Now`. The adapter is resolved from `w.Tool` (`agent.NewClaude()` / `NewOpencode()`).
- **FD6 — record disposition (idempotency + cruft policy).** Documented so the checkpoint review sees
  the call:
  - **KEEP** → no change.
  - **PRUNE** → delete record (spec-mandated).
  - **RESTORE success** → delete old (`RemoveWindow(w.PaneKey)`) + write new record (FD5.5). This is
    what makes a re-run idempotent: the new record has `BootID == currentBoot` and the live pane id,
    so the second pass classifies it KEEP. **A double-run MUST be a no-op on the second pass — assert
    this in a test.**
  - **Definitive skip** (`empty-sid`, `window-live`, `tree-gone`, `main`) → **delete** the dead-boot
    record. Its `pane_id` is from a dead server and can never become valid again, so keeping it is
    permanent cruft; the underlying session (where one exists) is rediscovered via the TUI. On a
    cold-server run this deletes opencode-new and main-checkout records — acceptable: shadow records
    are a resurrect/live-index artifact, **not** the session-discovery source (that is the adapters'
    `ListSessions`).
  - **Transient skip** (`git-error`, `no-worktree-match`) and **RESTORE failure** (`Launch` errored)
    → **keep** the record, record the reason in the report, **continue** to the next record (§4
    fault-isolation: one bad record never aborts the reconcile). Retried on the next run.
- **FD7 — `adapter.ResumeArgs(w.SessionID)` is the only resume composition.** No raw flag strings in
  the engine; the adapter owns the flags (claude `--resume <id>`, opencode `--session <id>`). Same
  discipline as M5/M6 launch.

### Advisor-resolved loose ends

- **L1 — two runners, one fake.** tmux calls route through `deps.Tmux.Runner`; git calls through
  `deps.Runner`. `proc.FakeRunner` keys responses by `name+args`, so a **single** `*FakeRunner`
  instance can back both seams in a unit test (tmux argv vs git argv never collide). Production wires
  both to `proc.ExecRunner{}`.
- **L2 — descendant match is path logic, kept local.** Unexported `isDescendant(parent, child
  string) bool` in `internal/resurrect` (clean both with `filepath.Clean`; equal, or `child` has
  `parent + string(os.PathSeparator)` prefix). Do not add to `internal/git`.
- **L3 — restored record's `BootID`.** Read `Tmux.BootID` **after** the first successful `Launch`
  (the server is guaranteed up by then). Cache it for the rest of the run; a cold-server start
  establishes a fresh boot on the first restore and all restored records share it.
- **L4 — integration test needs no agent binary.** `Launch` `send-keys` the resume command into a
  shell; "command not found" is fine — the assertion is window+pane+rewritten-record, not that the
  agent actually resumed. Mirrors the M6 integration approach (local `newTestServer`/`newGitRepo`
  helpers per the M6 precedent, since cross-package `_test` helpers aren't importable).

---

## Tasks

### M7-1 — reconcile engine (`internal/resurrect`)

- **Files:** `internal/resurrect/resurrect.go` (new), `internal/resurrect/resurrect_test.go` (new).
- **Build (all shell-out via the injected seams — no direct exec):**
  - `type Deps struct { Tmux tmux.Tmux; Runner proc.Runner; BaseDir string; Now int64 }`.
  - `type Report struct { Kept, Pruned, Restored []string; Skipped []SkipNote }` where each slice
    element identifies the record (e.g. `PaneKey` / `Tree`); `type SkipNote struct { PaneKey, Tree,
    Reason string }` (reason ∈ `empty-sid|window-live|tree-gone|main|git-error|no-worktree-match|
    launch-failed`).
  - `func Reconcile(ctx context.Context, deps Deps) (Report, error)` — implements FD1–FD7. Hard error
    return reserved for an unreadable state dir (`LoadWindows` error); every per-record fault is
    captured in `Report.Skipped`, never returned (§4).
  - `func adapterFor(tool model.Tool) (agent.Adapter, bool)` — `claude`→`NewClaude()`,
    `opencode`→`NewOpencode()`, else `false`. (Engine-local; the tui copy stays separate.)
  - `func isDescendant(parent, child string) bool` (L2).
- **Tests (single shared `*FakeRunner`; assert resulting tmux `Calls` + state-file mutations):**
  KEEP (pane present + boot match → zero Launch calls, record untouched); PRUNE (pane gone + boot
  match → `RemoveWindow`, no Launch); RESTORE happy path (boot differs → exact `Connect`/`Launch`
  argv with `adapter.ResumeArgs`, `set-option @perch_session`, old record removed + new record
  written with new pane_id + new boot); cold-server (BootID errors → all restore, no prune); each
  skip reason (empty-sid, window-live, tree-gone, main-checkout, git-error transient-keep); **double-
  run idempotency** (second `Reconcile` over the rewritten records issues zero Launch calls);
  per-record fault isolation (one Launch error → reason `launch-failed`, record kept, others still
  processed). Use a `tmux.Tmux{Runner: fake, Getenv: ...}` and a temp `BaseDir` with seeded
  `windows/*.json`.
- **Green gate:** `make fmt vet lint test`; `go mod verify`; ≥80% on `internal/resurrect`. No new deps.

### M7-2 — CLI wiring (`perch resurrect`)

- **Files:** `cmd/perch/main.go` (replace the `case "resurrect"` stub), `cmd/perch/main_test.go`
  (replace the stub test).
- **Build:**
  - `func handleResurrect(stdout, stderr io.Writer) int` — resolve `state.StateDir()`, build
    `resurrect.Deps{Tmux: tmux.New(), Runner: proc.ExecRunner{}, BaseDir: baseDir, Now:
    time.Now().Unix()}`, call `resurrect.Reconcile(context.Background(), deps)`, format the `Report`
    to stdout (counts + per-line detail: restored windows, pruned, skipped+reason). Exit `0` on
    success; `1` only on the hard-error return (unreadable state dir). Keep the testable
    `run(args, stdout, stderr)` contract — handler takes writers, no direct `os.Stdout`.
  - Dispatch: `case "resurrect": return handleResurrect(stdout, stderr)`.
  - `printUsage` already lists `perch resurrect` — leave it.
- **Tests:** drive `run([]string{"resurrect"}, &outBuf, &errBuf)` with a state dir that has no
  `windows/` (empty reconcile → exit 0, sensible "nothing to do" output). Real tmux is **not**
  required for the empty case (LoadWindows returns empty before any tmux call). Keep it hermetic.
- **Green gate:** `make fmt vet lint test`; `go mod verify`.

### M7-3 — integration test + closeout [x]

- **Files:** `internal/resurrect/integration_test.go` (new, `//go:build integration`), local
  `newTestServer`/`newGitRepo` helpers per the M6 precedent. Update `.gitignore` if any new transient
  artifact appears (none expected). Mark master plan + this sub-plan DONE.
- **Build (real git + private-socket tmux; no agent binary, L4):**
  - **RestoreAfterServerRestart:** create a real git repo + a linked worktree; start a private-socket
    server; `Launch` a window in the worktree and `SaveWindow` a shadow record stamped with that
    server's `BootID`; **kill the server** (`KillServer`) and start a **fresh** private-socket server
    (new boot); run `Reconcile` against the new server → assert the window is recreated
    (`ListPanesAll`/`HasSession` shows it) and the shadow record was rewritten (new `pane_id` file
    present, old removed, `BootID` == new server boot).
  - **PruneIntentionallyClosed:** same server (boot matches), record points at a `pane_id` that no
    longer exists → `Reconcile` deletes the record, creates no window.
  - **DoubleRunNoOp:** run `Reconcile` twice after a restart → second run issues no new window
    (assert window count stable / report `Restored` empty on pass 2).
  - **SkipMainCheckout:** shadow record whose tree is the repo's main checkout → not resurrected;
    record deleted (definitive skip).
- **Green gate:** `go test -race -count=1 -tags=integration ./...` green on real git + tmux; gofmt
  clean; `make lint` 0; `go mod verify`; coverage non-`tui` ≥80%.

---

## Honesty / open items (fill at closeout)

### Demonstrated (integration proves via real git + private-socket tmux)

- **Server-restart restore:** shadow record + killed server → `Reconcile` rebuilds the window,
  rewrites the record with the live post-restore boot id, `HasSession` confirms the session exists.
- **Intentional-close prune:** record with a non-existent `pane_id` but live-server boot → record
  deleted, no new window created.
- **Double-run idempotency:** two consecutive `Reconcile` calls after a restart → pass 1 restores,
  pass 2 KEEPs the rebuilt window and issues zero launches.
- **Main-checkout skip:** shadow record whose tree is the repo main checkout → `skip:main`, record
  deleted, no window created.
- **Same-pane-key regression (engine bugfix):** when the restarted server reuses the same pane id
  as the old one (tmux resets its counter; `%0 → %0`), `RemoveWindow(oldKey)` would delete the
  just-written record. Fixed in `resurrect.go` (guard `paneID != w.PaneKey` before remove);
  pinned by `TestReconcile_DoubleRunIdempotent_SamePaneKey` in the unit suite.

### Manual / deferred

- **TUI auto-offer** (`plan.md §7.4` last para): deferred to M9. The CLI path is fully exercised;
  the TUI offer needs the M9 modal/confirm surface.
- **opencode resume restore**: exercised only by the unit tests (FakeRunner). The integration tests
  use `["sh"]` as the launch command (a harmless shell) — the assertion is window+pane+record, not
  that the agent actually resumed. The opencode `--session <id>` argv path is covered by
  `TestReconcile_RestoreOpencode`.
- **`save-failed` RESTORE branch**: not triggered by any test (unit or integration) — would require
  an unwritable state directory mid-run, which is impractical to inject without unsafe filesystem
  tricks. Intentionally left uncovered; the code path is straightforward (return SkipNote,
  continue to next record without removing the old record).
- **`@perch_session` stamp reachability**: `SetPaneOption` is called in the engine and verified
  in the unit tests via FakeRunner call inspection; not re-checked in integration (the window is
  launched with `sh`, not an agent, so the option is set on a shell pane — harmless).
