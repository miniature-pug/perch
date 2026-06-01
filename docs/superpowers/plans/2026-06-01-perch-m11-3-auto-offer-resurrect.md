# M11-3 — Auto-Offer Resurrect on Restart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** When `perch` starts and a tmux-server restart/crash has stranded agent sessions, offer (y/N prompt, default N) to restore them — running `resurrect.Reconcile` once, *before* the frame is built.

**Architecture:** A read-only detector (`resurrect.StrandedCount`) counts windows that would enter Reconcile's RESTORE branch, sharing a single extracted classifier (`classify`) with `Reconcile` so the two can never drift. The offer lives in `bootstrap()` *before* `frame.Ensure` — at restart time no perch frame exists yet, so reconcile sees normal tmux topology and restoring is safe (this is the load-bearing correctness argument; the same reason `:resurrect` is gated *inside* the frame). The prompt is tty-gated: non-tty stdin (pipes/CI) skips the offer and proceeds, never blocking.

**Tech Stack:** Go; `resurrect`, `state`, `tmux`, `config` packages; stdlib `bufio`/`os` for the tty-gated prompt.

---

## Design decisions (locked)

- **Shared classifier, no `dryRun`.** Extract Reconcile's keep/prune/restore switch into `classify(w, livePanes, currentBoot) reconcileAction`. `Reconcile` and `StrandedCount` both call it. `dryRun` on `Reconcile` is rejected: it would impose a permanent "mutates nothing" invariant on the most dangerous function in the codebase for only decorative exactness.
- **Detect on pane/session/boot signals, never BootID arithmetic.** `BootID != currentBoot` alone mis-handles the cold-server case (server down → currentBoot == "" → everything stranded). `classify` already encodes the correct signals.
- **Acceptance criterion: clean relaunch must NOT offer.** User quits perch cleanly (agent sessions survive, server up), relaunches → `StrandedCount` MUST return 0. A pane that was cleanly closed (pane dead, same boot, home session alive) is a PRUNE, not a RESTORE — `classify` excludes it. This is the first test.
- **tty-gate is a hard requirement.** Non-tty stdin → skip (same as decline): run nothing, no reconcile, no prune — a pure no-op; the offer simply recurs next launch (idempotent).
- **Default N.** Spawning N agent processes on a stray Enter violates "confirm resource-heavy actions." Explicit `y`/`yes` required.
- **Coverage gap (documented, not an omission):** the offer lives only in `handleBootstrap`. `perch --sidebar` (direct) and the `handleTUI` fallback do not get it. Acceptable: `handleBootstrap` is the entry point, and if `frame.Ensure` fails the reconcile has already run pre-frame.

---

## File Structure

- **Modify:** `internal/resurrect/resurrect.go` — add `reconcileAction` + `classify`; refactor `Reconcile`'s switch to use `classify`; add `StrandedCount`.
- **Test:** `internal/resurrect/resurrect_test.go` — `classify` table test + `StrandedCount` tests (clean-relaunch=0 acceptance, restore, cold-server, mixed, no-records).
- **Modify:** `cmd/perch/main.go` — add `strandedCount`/`confirm`/`reconcile` seams to `bootstrapDeps`; wire the offer in `bootstrap()` before `frame.Ensure`; add production impls + `confirmRestore` + `resurrectDeps` helper.
- **Test:** `cmd/perch/main_test.go` — offer accept/decline/clean/error paths (existing bootstrap tests stay green via nil-guarded seams).

---

## Task 1: Shared classifier + read-only stranded detector

**Files:**
- Modify: `internal/resurrect/resurrect.go`
- Test: `internal/resurrect/resurrect_test.go`

- [ ] **Step 1: Write the failing `classify` test**

Append to `resurrect_test.go`:

```go
func TestClassify(t *testing.T) {
	const (
		boot    = "111"
		paneKey = "%5"
		sess    = "proj"
		win     = "feat"
	)
	w := model.Window{PaneKey: paneKey, TmuxSession: sess, TmuxWindow: win, BootID: boot}

	live := func(lines ...string) []tmux.Pane {
		var ps []tmux.Pane
		for _, l := range lines {
			_ = l
		}
		return ps
	}
	_ = live

	// Build panes directly (avoid parsing): KEEP needs the record's own pane live.
	keepPanes := []tmux.Pane{{ID: paneKey, Session: sess, Window: win}}
	// PRUNE: record's pane gone, but a sibling pane keeps the session alive.
	prunePanes := []tmux.Pane{{ID: "%99", Session: sess, Window: "tui"}}

	tests := []struct {
		name        string
		panes       []tmux.Pane
		currentBoot string
		want        reconcileAction
	}{
		{"keep: pane live + boot match", keepPanes, boot, actionKeep},
		{"prune: pane gone, boot match, session alive", prunePanes, boot, actionPrune},
		{"restore: boot mismatch", keepPanes, "999", actionRestore},
		{"restore: cold server (no panes, empty boot)", nil, "", actionRestore},
		{"restore: pane gone and session gone", nil, boot, actionRestore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(w, tt.panes, tt.currentBoot); got != tt.want {
				t.Fatalf("classify = %v, want %v", got, tt.want)
			}
		})
	}
}
```

> NOTE: verify `tmux.Pane`'s field names (`ID`, `Session`, `Window`) against `internal/tmux` before finalizing — adapt the literals if they differ. The `live`/`_ = live` scaffold above is dead; delete it, it's only here to remind you panes can be built as literals.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/resurrect/ -run TestClassify`
Expected: FAIL — `undefined: classify` / `reconcileAction` / `actionKeep`.

- [ ] **Step 3: Add `classify` + refactor `Reconcile` (`resurrect.go`)**

Add above `Reconcile`:

```go
// reconcileAction is the top-level keep/prune/restore decision for one window.
type reconcileAction int

const (
	// actionRestore: the pane is not live under the current boot and this is not a
	// clean intentional close — the agent was stranded by a restart or crash.
	actionRestore reconcileAction = iota
	// actionKeep: the pane is live and the boot ids match — leave it alone.
	actionKeep
	// actionPrune: the pane is gone but the boot matches and the home session is
	// still alive — the user intentionally closed this agent window.
	actionPrune
)

// classify is the top-level discriminator shared by Reconcile and StrandedCount,
// so the read-only detector can never drift from the reconciler's own decision.
// It evaluates only the cheap pane/session/boot signals; the restore-time guards
// (tree-gone, out-of-root, …) live in Reconcile's RESTORE branch and may still
// downgrade an actionRestore to a skip — so StrandedCount is an upper bound.
func classify(w model.Window, livePanes []tmux.Pane, currentBoot string) reconcileAction {
	paneByID := hasLivePaneByID(livePanes, w.PaneKey)
	bootMatch := currentBoot != "" && w.BootID == currentBoot
	switch {
	case paneByID && bootMatch:
		return actionKeep
	case !paneByID && bootMatch && hasLiveSession(livePanes, w.TmuxSession):
		return actionPrune
	default:
		return actionRestore
	}
}
```

In `Reconcile`, replace the inline `switch { case paneByID && bootMatch: ... case !paneByID && bootMatch && hasLiveSession(...): ... default: ... }` (the block starting at the current `paneByID := ...` / `bootMatch := ...` lines inside the `for _, w := range records` loop) with a switch on `classify`:

```go
		switch classify(w, livePanes, currentBoot) {
		case actionKeep:
			// KEEP: the pane is alive and boot ids match — no I/O needed.
			report.Kept = append(report.Kept, w.PaneKey)

		case actionPrune:
			// PRUNE: same server, home session alive, pane intentionally closed.
			if rerr := state.RemoveWindow(deps.BaseDir, w.PaneKey); rerr != nil {
				report.Skipped = append(report.Skipped, SkipNote{
					PaneKey: w.PaneKey,
					Tree:    w.Tree,
					Reason:  "prune-failed",
				})
			} else {
				report.Pruned = append(report.Pruned, w.PaneKey)
			}

		default: // actionRestore
			// RESTORE branch: boot mismatch or server is cold.
			// FD5 guards in order — cheap to expensive.
			// ... (LEAVE THE ENTIRE EXISTING RESTORE BODY UNCHANGED) ...
		}
```

> CRITICAL: the RESTORE branch body (all the FD5 guards + the Launch/SaveWindow/RemoveWindow logic, currently everything after `default:`) must be copied VERBATIM. The only change is the switch head: from `switch { case ...: case ...: default: }` to `switch classify(...) { case actionKeep: case actionPrune: default: }`. Delete the now-unused local `paneByID`/`bootMatch` declarations from the loop body (they moved into `classify`) — `go vet`/lint will flag them if left.

- [ ] **Step 4: Run the full resurrect suite (behavior-preserving refactor)**

Run: `go test ./internal/resurrect/ -v`
Expected: PASS — `TestClassify` green AND all existing `TestReconcile_*` (Keep/Prune/RestoreHappy/ColdServer/Skip*) still green.

- [ ] **Step 5: Write the failing `StrandedCount` tests**

Append to `resurrect_test.go`:

```go
func TestStrandedCount_CleanRelaunchIsZero(t *testing.T) {
	// ACCEPTANCE: pane cleanly closed (boot match, session alive) → PRUNE, not
	// RESTORE → count 0 → no offer on a normal relaunch.
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()
	const (
		boot         = "12345"
		paneKey      = "%30"
		sess         = "proj"
		survivorPane = "%31"
	)
	seedWindow(t, baseDir, model.Window{
		PaneKey: paneKey, Tool: model.ToolClaude, SessionID: "sid", Tree: baseDir,
		TmuxSession: sess, TmuxWindow: "feat", BootID: boot,
	})
	fake.Respond(proc.FakeResult{Stdout: []byte(boot + "\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte(paneLine(survivorPane, sess, "tui", false))},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	n, err := StrandedCount(context.Background(), newDeps(t, baseDir, fake))
	if err != nil {
		t.Fatalf("StrandedCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("clean relaunch StrandedCount = %d, want 0 (PRUNE, not RESTORE)", n)
	}
	// Read-only: the record must NOT be pruned by the detector.
	wins, _ := state.LoadWindows(baseDir)
	if len(wins) != 1 {
		t.Fatalf("StrandedCount must be read-only; record count = %d, want 1", len(wins))
	}
}

func TestStrandedCount_BootMismatchCounts(t *testing.T) {
	baseDir := t.TempDir()
	fake := proc.NewFakeRunner()
	const paneKey = "%40"
	seedWindow(t, baseDir, model.Window{
		PaneKey: paneKey, Tool: model.ToolClaude, SessionID: "sid", Tree: baseDir,
		TmuxSession: "proj", TmuxWindow: "feat", BootID: "old-boot",
	})
	fake.Respond(proc.FakeResult{Stdout: []byte("new-boot\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	fake.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormat)

	n, err := StrandedCount(context.Background(), newDeps(t, baseDir, fake))
	if err != nil {
		t.Fatalf("StrandedCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("boot-mismatch StrandedCount = %d, want 1", n)
	}
}

func TestStrandedCount_NoRecordsIsZero(t *testing.T) {
	baseDir := t.TempDir()
	n, err := StrandedCount(context.Background(), newDeps(t, baseDir, proc.NewFakeRunner()))
	if err != nil {
		t.Fatalf("StrandedCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("no records StrandedCount = %d, want 0", n)
	}
}
```

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/resurrect/ -run TestStrandedCount`
Expected: FAIL — `undefined: StrandedCount`.

- [ ] **Step 7: Add `StrandedCount` (`resurrect.go`)**

```go
// StrandedCount reports how many recorded windows would enter Reconcile's RESTORE
// branch — agent sessions stranded by a tmux server restart or crash. It is
// strictly read-only: no Launch, no state writes. It shares classify with
// Reconcile, so a cleanly-closed window (a PRUNE) is never counted. The
// restore-time guards are not evaluated, so the result is an upper bound, which
// is the right granularity for an "offer to restore" prompt.
func StrandedCount(ctx context.Context, deps Deps) (int, error) {
	records, err := state.LoadWindows(deps.BaseDir)
	if err != nil {
		return 0, fmt.Errorf("resurrect: load windows: %w", err)
	}
	if len(records) == 0 {
		return 0, nil
	}
	currentBoot, _ := deps.Tmux.BootID(ctx)
	livePanes, _ := deps.Tmux.ListPanesAll(ctx)
	n := 0
	for _, w := range records {
		if classify(w, livePanes, currentBoot) == actionRestore {
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 8: Run + lint**

```bash
go test ./internal/resurrect/ -v
go vet ./internal/resurrect/
golangci-lint run ./internal/resurrect/...
```
Expected: all PASS, lint clean.

- [ ] **Step 9: Commit**

```bash
git add internal/resurrect/resurrect.go internal/resurrect/resurrect_test.go
git commit -m "feat(resurrect): shared classify + read-only StrandedCount detector (M11-3)"
```

---

## Task 2: Bootstrap auto-offer wiring

**Files:**
- Modify: `cmd/perch/main.go` (`bootstrapDeps`, `bootstrapProduction`, `bootstrap`, + helpers)
- Test: `cmd/perch/main_test.go`

- [ ] **Step 1: Write the failing offer tests**

Append to `main_test.go` (mirror the existing `TestBootstrap_*` style — `frame.Ensure` is driven to succeed so `attach` is reached):

```go
// bootstrapSuccessRunner returns a FakeRunner that makes frame.Ensure succeed.
func bootstrapSuccessRunner() *proc.FakeRunner {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "=perch")
	r.Default = &proc.FakeResult{Stdout: []byte("%1\n")}
	return r
}

func TestBootstrap_OfferAcceptRunsReconcile(t *testing.T) {
	dir := t.TempDir()
	reconcileCalled := false
	confirmArg := -1

	deps := bootstrapDeps{
		tmuxClient: tmux.Tmux{Runner: bootstrapSuccessRunner(), Bin: "tmux"},
		executable: func() (string, error) { return "/usr/local/bin/perch", nil },
		fallback:   func(string, io.Writer, io.Writer) int { return 0 },
		attach:     func(context.Context, tmux.Tmux, string) int { return 0 },
		strandedCount: func(context.Context) (int, error) { return 3, nil },
		confirm:       func(n int) bool { confirmArg = n; return true },
		reconcile: func(context.Context) (resurrect.Report, error) {
			reconcileCalled = true
			return resurrect.Report{Restored: []string{"a", "b"}}, nil
		},
	}
	var out, errBuf strings.Builder
	bootstrap(deps, dir, &out, &errBuf)
	if confirmArg != 3 {
		t.Errorf("confirm called with n=%d, want 3", confirmArg)
	}
	if !reconcileCalled {
		t.Error("reconcile must run when the user accepts")
	}
	if !strings.Contains(out.String(), "restored") {
		t.Errorf("expected a restore summary on stdout; got %q", out.String())
	}
}

func TestBootstrap_OfferDeclineSkipsReconcile(t *testing.T) {
	dir := t.TempDir()
	reconcileCalled := false
	deps := bootstrapDeps{
		tmuxClient:    tmux.Tmux{Runner: bootstrapSuccessRunner(), Bin: "tmux"},
		executable:    func() (string, error) { return "/usr/local/bin/perch", nil },
		fallback:      func(string, io.Writer, io.Writer) int { return 0 },
		attach:        func(context.Context, tmux.Tmux, string) int { return 0 },
		strandedCount: func(context.Context) (int, error) { return 2, nil },
		confirm:       func(int) bool { return false },
		reconcile: func(context.Context) (resurrect.Report, error) {
			reconcileCalled = true
			return resurrect.Report{}, nil
		},
	}
	var out, errBuf strings.Builder
	bootstrap(deps, dir, &out, &errBuf)
	if reconcileCalled {
		t.Error("reconcile must NOT run when the user declines")
	}
}

func TestBootstrap_NoStrandedNoConfirm(t *testing.T) {
	dir := t.TempDir()
	confirmCalled := false
	deps := bootstrapDeps{
		tmuxClient:    tmux.Tmux{Runner: bootstrapSuccessRunner(), Bin: "tmux"},
		executable:    func() (string, error) { return "/usr/local/bin/perch", nil },
		fallback:      func(string, io.Writer, io.Writer) int { return 0 },
		attach:        func(context.Context, tmux.Tmux, string) int { return 0 },
		strandedCount: func(context.Context) (int, error) { return 0, nil },
		confirm:       func(int) bool { confirmCalled = true; return true },
		reconcile:     func(context.Context) (resurrect.Report, error) { return resurrect.Report{}, nil },
	}
	var out, errBuf strings.Builder
	bootstrap(deps, dir, &out, &errBuf)
	if confirmCalled {
		t.Error("confirm must NOT be called when nothing is stranded")
	}
}
```

(Add `"github.com/Miniature-Pug/perch/internal/resurrect"` to the test imports if not present.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/perch/ -run TestBootstrap`
Expected: FAIL — `bootstrapDeps` has no field `strandedCount`/`confirm`/`reconcile`.

- [ ] **Step 3: Extend `bootstrapDeps` + wire the offer (`main.go`)**

Add fields to `bootstrapDeps`:

```go
	// strandedCount reports how many agent sessions a restart stranded (read-only).
	// nil in tests that don't exercise the offer → the offer is skipped.
	strandedCount func(ctx context.Context) (int, error)
	// confirm prompts the user to restore n stranded sessions. Production gates on
	// a tty and returns false on a non-tty (never blocks). nil → offer skipped.
	confirm func(n int) bool
	// reconcile runs resurrect.Reconcile. nil → offer skipped.
	reconcile func(ctx context.Context) (resurrect.Report, error)
```

Add the `"github.com/Miniature-Pug/perch/internal/resurrect"` import to `main.go` if not present.

In `bootstrap()`, immediately after `ctx := context.Background()` and BEFORE `frame.Ensure`:

```go
	// Auto-offer resurrect when a server restart stranded agent sessions. This
	// runs BEFORE frame.Ensure so reconcile sees normal (non-frame) topology — at
	// restart time no perch frame exists yet, so restoring is safe here. Seams are
	// nil in tests that don't exercise the offer.
	if deps.strandedCount != nil && deps.confirm != nil && deps.reconcile != nil {
		if n, derr := deps.strandedCount(ctx); derr == nil && n > 0 && deps.confirm(n) {
			if rep, rerr := deps.reconcile(ctx); rerr != nil {
				_, _ = fmt.Fprintf(stderr, "perch: resurrect: %v\n", rerr)
			} else {
				_, _ = fmt.Fprintf(stdout, "perch: resurrect — %d restored, %d pruned, %d kept\n",
					len(rep.Restored), len(rep.Pruned), len(rep.Kept))
			}
		}
	}
```

- [ ] **Step 4: Add production seams (`main.go`)**

Add the `"bufio"` import. Add helpers and wire them into `bootstrapProduction()`:

```go
// resurrectDeps builds resurrect.Deps from the real state dir + global config
// roots. ok is false when the state dir is unavailable (the caller then skips
// the offer rather than erroring out the launch).
func resurrectDeps(t tmux.Tmux) (resurrect.Deps, bool) {
	baseDir, err := state.StateDir()
	if err != nil {
		return resurrect.Deps{}, false
	}
	var roots []string
	if globalPath, gerr := config.DefaultGlobalPath(); gerr == nil {
		if cfg, cerr := config.Load(globalPath, ""); cerr == nil {
			roots = cfg.Roots
		}
	}
	return resurrect.Deps{
		Tmux:    t,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
		Roots:   roots,
	}, true
}

// confirmRestore is the production confirm seam: it gates on a tty so a
// non-interactive launch (piped/CI stdin) never blocks on a prompt — it returns
// false and the offer is skipped. Only an explicit y/yes restores.
func confirmRestore(n int) bool {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return false // non-tty: never block on a prompt
	}
	_, _ = fmt.Fprintf(os.Stderr,
		"perch: %d session(s) were stranded by a restart. Restore them? [y/N] ", n)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
```

In `bootstrapProduction()`, after `t := tmux.New()`:

```go
	rdeps, rok := resurrectDeps(t)
```

and add the three seams to the returned struct literal:

```go
		strandedCount: func(ctx context.Context) (int, error) {
			if !rok {
				return 0, nil
			}
			return resurrect.StrandedCount(ctx, rdeps)
		},
		confirm: confirmRestore,
		reconcile: func(ctx context.Context) (resurrect.Report, error) {
			if !rok {
				return resurrect.Report{}, nil
			}
			return resurrect.Reconcile(ctx, rdeps)
		},
```

- [ ] **Step 5: Run tests + build + lint**

```bash
go test ./cmd/perch/ -run TestBootstrap -v
go build ./... && go vet ./...
golangci-lint run ./cmd/perch/...
```
Expected: new offer tests PASS; existing `TestBootstrap_FallbackOnEnsureError`/`TestBootstrap_AttachOnSuccess` still PASS (nil seams → offer skipped); build + lint clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/perch/main.go cmd/perch/main_test.go
git commit -m "feat(bootstrap): tty-gated auto-offer resurrect on restart (M11-3)"
```

---

## Self-review checklist (run before declaring done)

1. **Spec coverage:** restart detection (read-only `StrandedCount`), offer (tty-gated y/N, default N), reconcile-on-accept, pre-frame placement, coverage-gap documented. ✅
2. **Acceptance test present:** `TestStrandedCount_CleanRelaunchIsZero` proves clean relaunch does not offer AND the detector does not mutate state. ✅
3. **No drift:** `Reconcile` and `StrandedCount` share `classify`; the refactor preserves all existing `TestReconcile_*`. ✅
4. **tty-gate:** non-tty → `confirmRestore` returns false (skip), never blocks. ✅
5. **Hermetic tests:** all three seams faked in `main_test.go`; resurrect tests use FakeRunner + `t.TempDir()`; no real `$HOME`, tmux, or binary. ✅
6. **Default N:** only explicit `y`/`yes` restores. ✅
