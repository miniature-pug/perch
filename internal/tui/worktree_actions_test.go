package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/trust"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makeLoader builds a loader with FakeRunner and a temp state dir, ready for
// worktree action tests. fakeTmuxInside is used so switch-client paths work.
func makeLoader(t *testing.T, r *proc.FakeRunner) loader {
	t.Helper()
	return loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: t.TempDir(),
		Now:     1000,
	}
}

// worktreeItem builds an item suitable for worktree-action tests.
// When live=true it gets a liveTarget and captureTarget.
func worktreeItem(branch, treePath, projectPath string, isMain, live bool) item {
	it := item{
		tree:        branch,
		treePath:    treePath,
		projectPath: projectPath,
		isMain:      isMain,
		isSession:   true,
		live:        live,
		id:          "test-session-id",
		tool:        "claude",
	}
	if live {
		it.liveTarget = tmux.WindowTarget("perch", branch)
		it.captureTarget = "%55"
	}
	return it
}

// mustUpdate sends a KeyMsg to m and returns the updated model. It panics if
// the returned tea.Model is not a Model.
func mustUpdate(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

// pressKey is a convenience for sending a rune key.
func pressKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// ── Test 1: d on main item → toast set, no modal, no git calls ─────────

func TestRemove_MainItemBlocked(t *testing.T) {
	r := proc.NewFakeRunner()
	it := worktreeItem("main", "/proj/main", "/proj", true, false)
	m := New([]list.Item{it}).WithLoader(makeLoader(t, r))
	m, _ = mustUpdate(t, m, windowMsg)

	m, _ = mustUpdate(t, m, pressKey('d'))

	if m.toast == "" {
		t.Error("want toast set for d on main item, got empty")
	}
	if m.modal.kind != modalNone {
		t.Errorf("want modal=modalNone, got %v", m.modal.kind)
	}
	if len(r.Calls) != 0 {
		t.Errorf("want 0 runner calls for blocked d, got %d: %v", len(r.Calls), r.Calls)
	}
}

// ── Test 2: d on idle non-main → modalRemoveConfirm, no git calls ────────────

func TestRemove_IdleNonMain_OpensModal(t *testing.T) {
	r := proc.NewFakeRunner()
	it := worktreeItem("feat", "/proj/worktrees/feat", "/proj", false, false)
	m := New([]list.Item{it}).WithLoader(makeLoader(t, r))
	m, _ = mustUpdate(t, m, windowMsg)

	m, _ = mustUpdate(t, m, pressKey('d'))

	if m.modal.kind != modalRemoveConfirm {
		t.Errorf("want modal=modalRemoveConfirm, got %v", m.modal.kind)
	}
	if len(r.Calls) != 0 {
		t.Errorf("want 0 runner calls before confirmation, got %d: %v", len(r.Calls), r.Calls)
	}
}

// ── Test 3: d on live non-main → preflight; focused=true → toast, no modal;
//            focused=false → modalRemoveConfirm ──────────────────────────────

func TestRemove_LiveNonMain_Preflight(t *testing.T) {
	r := proc.NewFakeRunner()
	ldr := makeLoader(t, r)

	it := worktreeItem("feat", "/proj/worktrees/feat", "/proj", false, true)
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Press d: should return a preflight cmd (non-nil).
	m2, cmd := mustUpdate(t, m, pressKey('d'))
	if cmd == nil {
		t.Fatal("d on live non-main: want preflight cmd, got nil")
	}
	// Modal must not be set yet.
	if m2.modal.kind != modalNone {
		t.Errorf("want modal=modalNone before preflight resolves, got %v", m2.modal.kind)
	}

	// Sub-test A: focused=true → toast, no modal.
	specA := modalState{
		kind:        modalRemoveConfirm,
		treePath:    it.treePath,
		branch:      it.tree,
		projectPath: it.projectPath,
		target:      it.liveTarget,
		paneKey:     it.captureTarget,
		tool:        it.tool,
		sessionID:   it.id,
	}
	mA, _ := mustUpdate(t, m, preflightRemoveMsg{spec: specA, focused: true})
	if mA.toast == "" {
		t.Error("focused=true: want toast set, got empty")
	}
	if mA.modal.kind != modalNone {
		t.Errorf("focused=true: want modal=modalNone, got %v", mA.modal.kind)
	}

	// Sub-test B: focused=false → modalRemoveConfirm.
	mB, _ := mustUpdate(t, m, preflightRemoveMsg{spec: specA, focused: false})
	if mB.modal.kind != modalRemoveConfirm {
		t.Errorf("focused=false: want modal=modalRemoveConfirm, got %v", mB.modal.kind)
	}
}

// ── Test 4: removeConfirm + y → git worktree remove, kill-window, state cleared

func TestRemove_Confirm_SuccessFlow(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
	}

	treePath := "/proj/worktrees/feat"
	projectPath := "/proj"
	paneKey := "%55"
	target := tmux.WindowTarget("perch", "feat")

	// Pre-write a shadow record so we can verify it gets removed.
	if err := state.SaveWindow(baseDir, model.Window{PaneKey: paneKey, Tree: treePath}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	// Register FakeRunner responses.
	ok := proc.FakeResult{}
	r.Default = &ok // git worktree remove + kill-window all succeed
	r.Respond(proc.FakeResult{},
		"git", "-C", projectPath, "worktree", "remove", treePath)
	r.Respond(proc.FakeResult{},
		"tmux", "kill-window", "-t", target)

	it := worktreeItem("feat", treePath, projectPath, false, true)
	it.liveTarget = target
	it.captureTarget = paneKey
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Open remove confirm modal directly (idle path).
	m.modal = modalState{
		kind:        modalRemoveConfirm,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
		target:      target,
		paneKey:     paneKey,
	}

	// Press y.
	_, cmd := mustUpdate(t, m, pressKey('y'))
	if cmd == nil {
		t.Fatal("y on removeConfirm: want removeCmd, got nil")
	}

	// Execute the cmd.
	resultMsg := cmd()
	rm, ok2 := resultMsg.(removeResultMsg)
	if !ok2 {
		t.Fatalf("want removeResultMsg, got %T", resultMsg)
	}
	if rm.err != nil {
		t.Fatalf("removeResultMsg error: %v", rm.err)
	}
	if rm.dirty {
		t.Error("want dirty=false on success, got true")
	}

	// Assert git worktree remove was called without --force.
	found := false
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 &&
			c.Args[0] == "-C" && c.Args[2] == "worktree" && c.Args[3] == "remove" {
			// Verify --force is NOT in the args.
			for _, a := range c.Args {
				if a == "--force" {
					t.Error("git worktree remove must not have --force on first attempt")
				}
			}
			// Verify the treePath is present.
			hasPath := false
			for _, a := range c.Args {
				if a == treePath {
					hasPath = true
				}
			}
			if !hasPath {
				t.Errorf("git worktree remove args %v: want treePath %q", c.Args, treePath)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("no git worktree remove call found in: %v", r.Calls)
	}

	// Assert kill-window was called.
	foundKill := false
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "kill-window" {
			foundKill = true
		}
	}
	if !foundKill {
		t.Errorf("kill-window call not found in: %v", r.Calls)
	}

	// Assert shadow record was removed.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("want 0 window records after remove, got %d", len(windows))
	}

	// Feed removeResultMsg back → modal should close.
	mFinal, _ := mustUpdate(t, m, rm)
	if mFinal.modal.kind != modalNone {
		t.Errorf("after success removeResultMsg: want modal=modalNone, got %v", mFinal.modal.kind)
	}
}

// ── Test 5: dirty → force flow ─────────────────────────────────────────────

func TestRemove_DirtyForceFlow(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
	}

	treePath := "/proj/worktrees/feat"
	projectPath := "/proj"

	// First call (no --force) fails with dirty message.
	r.Respond(proc.FakeResult{
		Err:    proc.FakeExitError{Code: 1},
		Stderr: []byte("fatal: '" + treePath + "' contains modified or untracked files"),
	}, "git", "-C", projectPath, "worktree", "remove", treePath)

	// Force call succeeds.
	r.Respond(proc.FakeResult{},
		"git", "-C", projectPath, "worktree", "remove", treePath, "--force")

	m := New(nil).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	spec := modalState{
		kind:        modalRemoveConfirm,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}
	m.modal = spec

	// Execute removeCmd(false, false, nil) — no hooks in this test's config.
	cmd := m.removeCmd(spec, false, false, nil)
	resultMsg := cmd()
	rm, ok := resultMsg.(removeResultMsg)
	if !ok {
		t.Fatalf("want removeResultMsg, got %T", resultMsg)
	}
	if !rm.dirty {
		t.Error("want dirty=true for ErrWorktreeDirty, got false")
	}

	// Feed dirty result → model should set modalForceConfirm.
	m2, _ := mustUpdate(t, m, rm)
	if m2.modal.kind != modalForceConfirm {
		t.Errorf("want modal=modalForceConfirm after dirty result, got %v", m2.modal.kind)
	}

	// Press y on forceConfirm → removeCmd(true, true).
	m2.modal = modalState{
		kind:        modalForceConfirm,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}
	_, cmd2 := mustUpdate(t, m2, pressKey('y'))
	if cmd2 == nil {
		t.Fatal("y on forceConfirm: want removeCmd, got nil")
	}
	resultMsg2 := cmd2()
	rm2, ok2 := resultMsg2.(removeResultMsg)
	if !ok2 {
		t.Fatalf("want removeResultMsg, got %T", resultMsg2)
	}
	if rm2.err != nil {
		t.Fatalf("force removeResultMsg error: %v", rm2.err)
	}

	// Assert the FIRST git worktree remove call did NOT contain --force.
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 &&
			c.Args[0] == "-C" && c.Args[2] == "worktree" && c.Args[3] == "remove" {
			for _, a := range c.Args {
				if a == "--force" {
					t.Errorf("first git worktree remove must not have --force: args=%v", c.Args)
				}
			}
			break // only check the first matching call
		}
	}

	// Assert --force was used in the second call.
	foundForce := false
	forceCallCount := 0
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 &&
			c.Args[0] == "-C" && c.Args[2] == "worktree" && c.Args[3] == "remove" {
			forceCallCount++
			for _, a := range c.Args {
				if a == "--force" {
					foundForce = true
				}
			}
		}
	}
	if !foundForce {
		t.Errorf("force git worktree remove: --force not found in calls: %v", r.Calls)
	}

	// Feed success result → modal none.
	mFinal, _ := mustUpdate(t, m2, rm2)
	if mFinal.modal.kind != modalNone {
		t.Errorf("after force success: want modal=modalNone, got %v", mFinal.modal.kind)
	}
}

// ── Test 6: x on live → killConfirm; y → kill-window, no worktree remove ────

func TestKill_LiveItem_Flow(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
	}

	treePath := "/proj/worktrees/feat"
	projectPath := "/proj"
	paneKey := "%55"
	target := tmux.WindowTarget("perch", "feat")

	// Pre-write shadow record.
	if err := state.SaveWindow(baseDir, model.Window{PaneKey: paneKey, Tree: treePath}); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	ok := proc.FakeResult{}
	r.Default = &ok

	it := worktreeItem("feat", treePath, projectPath, false, true)
	it.liveTarget = target
	it.captureTarget = paneKey
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Press x.
	m, _ = mustUpdate(t, m, pressKey('x'))
	if m.modal.kind != modalKillConfirm {
		t.Errorf("want modal=modalKillConfirm after x, got %v", m.modal.kind)
	}
	// The kill confirm must name its target (M12), not say a generic "window".
	if m.modal.branch != "feat" {
		t.Errorf("kill modal must name the target branch; got branch=%q", m.modal.branch)
	}

	// Press y.
	_, cmd := mustUpdate(t, m, pressKey('y'))
	if cmd == nil {
		t.Fatal("y on killConfirm: want killCmd, got nil")
	}
	resultMsg := cmd()
	_, ok2 := resultMsg.(killResultMsg)
	if !ok2 {
		t.Fatalf("want killResultMsg, got %T", resultMsg)
	}

	// Assert kill-window was called.
	foundKill := false
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "kill-window" {
			foundKill = true
		}
	}
	if !foundKill {
		t.Errorf("kill-window not found in calls: %v", r.Calls)
	}

	// Assert NO git worktree remove was called.
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 3 && c.Args[2] == "remove" {
			t.Errorf("unexpected git worktree remove call for kill: %v", c.Args)
		}
	}

	// Assert shadow record removed.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("want 0 window records after kill, got %d", len(windows))
	}

	// Feed killResultMsg → modal none.
	mFinal, _ := mustUpdate(t, m, resultMsg)
	if mFinal.modal.kind != modalNone {
		t.Errorf("after killResultMsg: want modal=modalNone, got %v", mFinal.modal.kind)
	}
}

// ── Test 7: Esc on each modal → closes modal, no destructive calls ────────────

func TestModal_EscCancels(t *testing.T) {
	kinds := []modalKind{modalRemoveConfirm, modalForceConfirm, modalKillConfirm, modalNewSession}
	for _, kind := range kinds {
		t.Run(kind.String(), func(t *testing.T) {
			r := proc.NewFakeRunner()
			m := New(nil).WithLoader(makeLoader(t, r))
			m, _ = mustUpdate(t, m, windowMsg)

			m.modal = modalState{
				kind:     kind,
				treePath: "/proj/worktrees/feat",
				branch:   "feat",
				target:   tmux.WindowTarget("perch", "feat"),
				paneKey:  "%55",
			}

			m, _ = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.modal.kind != modalNone {
				t.Errorf("esc: want modal=modalNone, got %v", m.modal.kind)
			}
			if len(r.Calls) != 0 {
				t.Errorf("esc: want 0 runner calls, got %d: %v", len(r.Calls), r.Calls)
			}
		})
	}
}

// ── Test 7b: n key cancels modalNewSession (spec's primary cancel key) ────────

func TestModal_NKeyCancelsNewSession(t *testing.T) {
	r := proc.NewFakeRunner()
	m := New(nil).WithLoader(makeLoader(t, r))
	m, _ = mustUpdate(t, m, windowMsg)

	m.modal = modalState{
		kind:        modalNewSession,
		treePath:    "/proj/worktrees/feat",
		branch:      "feat",
		projectPath: "/proj",
	}

	m, _ = mustUpdate(t, m, pressKey('n'))
	if m.modal.kind != modalNone {
		t.Errorf("n: want modal=modalNone, got %v", m.modal.kind)
	}
	if len(r.Calls) != 0 {
		t.Errorf("n: want 0 runner calls, got %d: %v", len(r.Calls), r.Calls)
	}
}

// String implements fmt.Stringer for modalKind to improve test output.
func (k modalKind) String() string {
	switch k {
	case modalNone:
		return "modalNone"
	case modalNewSession:
		return "modalNewSession"
	case modalRemoveConfirm:
		return "modalRemoveConfirm"
	case modalForceConfirm:
		return "modalForceConfirm"
	case modalKillConfirm:
		return "modalKillConfirm"
	case modalTrustConfirm:
		return "modalTrustConfirm"
	default:
		return "unknown"
	}
}

// ── Test 8: pre_remove hooks run BEFORE git worktree remove ──────────────────

func TestRemove_PreRemoveHooksRunFirst(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config: &config.Config{
			PreRemove: []string{"echo hi"},
		},
	}

	treePath := "/proj/worktrees/feat"
	projectPath := "/proj"

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)

	spec := modalState{
		kind:        modalRemoveConfirm,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}

	// Pass dec with allow=true and approvedHash="" (matching Config.ProjectConfigHash=="").
	dec := &trustDecision{allow: true, approvedHash: ""}
	cmd := m.removeCmd(spec, false, false, dec)
	resultMsg := cmd()
	rm, okR := resultMsg.(removeResultMsg)
	if !okR {
		t.Fatalf("want removeResultMsg, got %T", resultMsg)
	}
	if rm.err != nil {
		t.Fatalf("removeResultMsg error: %v", rm.err)
	}

	// Find the hook call and the git worktree remove call, assert hook comes first.
	hookIdx := -1
	removeIdx := -1
	for i, c := range r.Calls {
		if c.Name == "sh" {
			hookIdx = i
		}
		if c.Name == "git" && len(c.Args) >= 4 &&
			c.Args[0] == "-C" && c.Args[2] == "worktree" && c.Args[3] == "remove" {
			removeIdx = i
		}
	}
	if hookIdx == -1 {
		t.Fatalf("sh -c hook call not found in: %v", r.Calls)
	}
	if removeIdx == -1 {
		t.Fatalf("git worktree remove call not found in: %v", r.Calls)
	}
	if hookIdx >= removeIdx {
		t.Errorf("hook (index %d) must run before git worktree remove (index %d)", hookIdx, removeIdx)
	}
}

// ── Test 8b: skipPrep=true → no hooks, no RemoveLock, only git worktree remove --force ──

func TestRemove_SkipPrep_NoHooksNoLock(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config: &config.Config{
			PreRemove: []string{"echo hi"},
		},
	}

	treePath := "/proj/worktrees/feat"
	projectPath := "/proj"

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)

	spec := modalState{
		kind:        modalForceConfirm,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}

	// Execute removeCmd with force=true, skipPrep=true (the retry path); dec irrelevant.
	cmd := m.removeCmd(spec, true, true, nil)
	resultMsg := cmd()
	rm, okR := resultMsg.(removeResultMsg)
	if !okR {
		t.Fatalf("want removeResultMsg, got %T", resultMsg)
	}
	if rm.err != nil {
		t.Fatalf("removeResultMsg error: %v", rm.err)
	}

	// Assert NO sh call (hook) was made.
	for _, c := range r.Calls {
		if c.Name == "sh" {
			t.Errorf("skipPrep=true: unexpected sh (hook) call: %v", c.Args)
		}
	}

	// Assert the only git call is worktree remove --force.
	gitCallCount := 0
	for _, c := range r.Calls {
		if c.Name == "git" {
			gitCallCount++
			hasWorktree := false
			hasRemove := false
			hasForce := false
			for _, a := range c.Args {
				switch a {
				case "worktree":
					hasWorktree = true
				case "remove":
					hasRemove = true
				case "--force":
					hasForce = true
				}
			}
			if !hasWorktree || !hasRemove {
				t.Errorf("skipPrep=true: unexpected git call (not worktree remove): %v", c.Args)
			}
			if !hasForce {
				t.Errorf("skipPrep=true: git worktree remove missing --force: %v", c.Args)
			}
		}
	}
	if gitCallCount == 0 {
		t.Errorf("skipPrep=true: expected git worktree remove --force call, got none; calls: %v", r.Calls)
	}
}

// ── W9: w with NO mapping → preflight → modalNewSession opens (action 0) ──────

func TestWorktree_NoPriorMapping_OpensModal(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := makeLoader(t, r)
	ldr.BaseDir = baseDir

	it := worktreeItem("feat", "/proj/worktrees/feat", "/proj", false, false)
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Press w → returns preflight cmd.
	m2, cmd := mustUpdate(t, m, pressKey('w'))
	if cmd == nil {
		t.Fatal("w on session item: want preflight cmd, got nil")
	}
	// Modal must not be set before the cmd executes.
	if m2.modal.kind != modalNone {
		t.Errorf("want modal=modalNone before preflight resolves, got %v", m2.modal.kind)
	}

	// Execute preflight cmd.
	msg := cmd()
	pm, ok := msg.(worktreePreflightMsg)
	if !ok {
		t.Fatalf("want worktreePreflightMsg, got %T", msg)
	}
	if !pm.prompt {
		t.Error("want prompt=true for no-mapping case, got false")
	}

	// Feed the message — modal should open.
	m3, _ := mustUpdate(t, m2, pm)
	if m3.modal.kind != modalNewSession {
		t.Errorf("want modal=modalNewSession, got %v", m3.modal.kind)
	}
	if m3.modal.action != 0 {
		t.Errorf("want action=0 on open, got %d", m3.modal.action)
	}
}

// ── W10: modalNewSession navigation keys ──────────────────────────────────────

func TestWorktree_ModalNav(t *testing.T) {
	r := proc.NewFakeRunner()
	m := New(nil).WithLoader(makeLoader(t, r))
	m, _ = mustUpdate(t, m, windowMsg)

	// Open the modal directly.
	m.modal = modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        "claude",
		sessionID:   "test-sess",
		treePath:    "/proj/worktrees/feat",
		branch:      "feat",
		projectPath: "/proj",
	}

	// Down twice → action==2.
	m, _ = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modal.action != 1 {
		t.Errorf("after first down: want action=1, got %d", m.modal.action)
	}
	m, _ = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modal.action != 2 {
		t.Errorf("after second down: want action=2, got %d", m.modal.action)
	}
	// Down past 2 → clamps at 2.
	m, _ = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modal.action != 2 {
		t.Errorf("down past max: want action=2, got %d", m.modal.action)
	}

	// Press '1' → action==0.
	m, _ = mustUpdate(t, m, pressKey('1'))
	if m.modal.action != 0 {
		t.Errorf("after '1': want action=0, got %d", m.modal.action)
	}

	// Press '3' → action==2.
	m, _ = mustUpdate(t, m, pressKey('3'))
	if m.modal.action != 2 {
		t.Errorf("after '3': want action=2, got %d", m.modal.action)
	}

	// Up → action==1.
	m, _ = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.modal.action != 1 {
		t.Errorf("after up: want action=1, got %d", m.modal.action)
	}

	// 'j' → action==2.
	m, _ = mustUpdate(t, m, pressKey('j'))
	if m.modal.action != 2 {
		t.Errorf("after j: want action=2, got %d", m.modal.action)
	}

	// 'k' → action==1.
	m, _ = mustUpdate(t, m, pressKey('k'))
	if m.modal.action != 1 {
		t.Errorf("after k: want action=1, got %d", m.modal.action)
	}
}

// ── W11: action 0 create → git worktree add + mapping + worktreeCreatedMsg ────

func TestWorktree_Action0_Create(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	// Use a real temp dir as the project path so worktree.Seed's EvalSymlinks succeeds.
	projectPath := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config:  &config.Config{},
	}

	sessID := "test-session-create"
	treeBranch := "feat"
	treePath := t.TempDir()

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)
	m.modal = modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        "claude",
		sessionID:   sessID,
		treePath:    treePath,
		branch:      treeBranch,
		projectPath: projectPath,
	}

	// Press Enter → worktreeCreateCmd dispatched.
	_, cmd := mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on action 0: want worktreeCreateCmd, got nil")
	}

	// Execute the cmd (synchronous closure).
	resultMsg := cmd()
	wm, ok2 := resultMsg.(worktreeCreatedMsg)
	if !ok2 {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}
	if wm.err != nil {
		t.Fatalf("worktreeCreatedMsg error: %v", wm.err)
	}

	// Assert a "worktree add -b" call was made.
	var addCall *proc.Call
	for i := range r.Calls {
		c := &r.Calls[i]
		if c.Name == "git" && len(c.Args) >= 4 &&
			c.Args[2] == "worktree" && c.Args[3] == "add" && c.Args[4] == "-b" {
			addCall = c
			break
		}
	}
	if addCall == nil {
		t.Fatalf("git worktree add -b not found in calls: %v", r.Calls)
	}
	branchArg := addCall.Args[5]
	if !strings.HasPrefix(branchArg, "perch/") {
		t.Errorf("branch arg %q: want prefix perch/", branchArg)
	}
	// Should contain the source slug.
	if !strings.Contains(branchArg, "feat") {
		t.Errorf("branch arg %q: want source slug 'feat'", branchArg)
	}
	// Should have a "-<8char>" suffix after the slug.
	parts := strings.Split(branchArg, "-")
	if len(parts) < 2 {
		t.Errorf("branch arg %q: expected at least one '-' separator", branchArg)
	}
	shortSuffix := parts[len(parts)-1]
	if len(shortSuffix) != 8 {
		t.Errorf("branch suffix %q: want 8 hex chars, got %d", shortSuffix, len(shortSuffix))
	}
	// base arg must be "HEAD" (empty cfg.BaseBranch).
	baseArg := addCall.Args[len(addCall.Args)-1]
	if baseArg != "HEAD" {
		t.Errorf("base arg = %q, want HEAD", baseArg)
	}

	// Assert spec is a fork.
	if !wm.spec.fork {
		t.Error("want spec.fork=true for action 0")
	}
	if !strings.HasPrefix(wm.spec.branch, "perch/") {
		t.Errorf("spec.branch = %q: want prefix perch/", wm.spec.branch)
	}

	// Assert state mapping was recorded.
	st, err := state.LoadState(baseDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	mp, found := state.LookupMapping(st, sessID)
	if !found {
		t.Fatal("mapping not recorded in state")
	}
	if mp.Choice != state.ChoiceWorktree {
		t.Errorf("mapping Choice = %q, want %q", mp.Choice, state.ChoiceWorktree)
	}
	if mp.Tree == "" {
		t.Error("mapping Tree is empty")
	}
}

// ── W12: action 1 run-here → resume spec + ChoiceNone mapping ─────────────────

func TestWorktree_Action1_RunHere(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config:  &config.Config{},
	}

	sessID := "sess-run-here"
	treePath := "/proj/worktrees/feat"
	projectPath := "/proj/myrepo"

	m := New(nil).WithLoader(ldr)
	m.modal = modalState{
		kind:        modalNewSession,
		action:      1,
		tool:        "claude",
		sessionID:   sessID,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}

	_, cmd := mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on action 1: want cmd, got nil")
	}

	resultMsg := cmd()
	wm, ok := resultMsg.(worktreeCreatedMsg)
	if !ok {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}
	if wm.err != nil {
		t.Fatalf("worktreeCreatedMsg error: %v", wm.err)
	}

	// Must be a resume in treePath, no fork.
	if wm.spec.fork {
		t.Error("run-here: want fork=false")
	}
	if !wm.spec.resume {
		t.Error("run-here: want resume=true")
	}
	if wm.spec.treePath != treePath {
		t.Errorf("run-here: spec.treePath = %q, want %q", wm.spec.treePath, treePath)
	}

	// No git worktree add call.
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "add" {
			t.Errorf("unexpected git worktree add call for run-here: %v", c.Args)
		}
	}

	// Mapping: ChoiceNone, Tree==treePath.
	st, _ := state.LoadState(baseDir)
	mp, found := state.LookupMapping(st, sessID)
	if !found {
		t.Fatal("mapping not recorded for run-here")
	}
	if mp.Choice != state.ChoiceNone {
		t.Errorf("mapping Choice = %q, want %q", mp.Choice, state.ChoiceNone)
	}
	if mp.Tree != treePath {
		t.Errorf("mapping Tree = %q, want %q", mp.Tree, treePath)
	}
}

// ── W13: action 2 run-main → resume in projectPath + ChoiceNone mapping ───────

func TestWorktree_Action2_RunMain(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config:  &config.Config{},
	}

	sessID := "sess-run-main"
	treePath := "/proj/worktrees/feat"
	projectPath := "/proj/myrepo"

	m := New(nil).WithLoader(ldr)
	m.modal = modalState{
		kind:        modalNewSession,
		action:      2,
		tool:        "claude",
		sessionID:   sessID,
		treePath:    treePath,
		branch:      "feat",
		projectPath: projectPath,
	}

	_, cmd := mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on action 2: want cmd, got nil")
	}

	resultMsg := cmd()
	wm, ok := resultMsg.(worktreeCreatedMsg)
	if !ok {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}
	if wm.err != nil {
		t.Fatalf("worktreeCreatedMsg error: %v", wm.err)
	}

	// Must be resume in projectPath.
	if wm.spec.fork {
		t.Error("run-main: want fork=false")
	}
	if !wm.spec.resume {
		t.Error("run-main: want resume=true")
	}
	if wm.spec.treePath != projectPath {
		t.Errorf("run-main: spec.treePath = %q, want %q", wm.spec.treePath, projectPath)
	}

	// No git worktree add call.
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "add" {
			t.Errorf("unexpected git worktree add call for run-main: %v", c.Args)
		}
	}

	// Mapping: ChoiceNone, Tree==projectPath.
	st, _ := state.LoadState(baseDir)
	mp, found := state.LookupMapping(st, sessID)
	if !found {
		t.Fatal("mapping not recorded for run-main")
	}
	if mp.Choice != state.ChoiceNone {
		t.Errorf("mapping Choice = %q, want %q", mp.Choice, state.ChoiceNone)
	}
	if mp.Tree != projectPath {
		t.Errorf("mapping Tree = %q, want %q", mp.Tree, projectPath)
	}
}

// ── W14: no-reprompt — existing ChoiceNone mapping → skip modal ───────────────

func TestWorktree_NoReprompt_ChoiceNone(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
	}

	sessID := "test-session-id"
	treePath := "/proj/myrepo"

	// Pre-seed mapping.
	st, _ := state.LoadState(baseDir)
	state.SetMapping(&st, sessID, state.Mapping{
		Tool:   model.ToolClaude,
		Tree:   treePath,
		Choice: state.ChoiceNone,
	})
	if err := state.SaveState(baseDir, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	it := worktreeItem("feat", "/proj/worktrees/feat", "/proj/myrepo", false, false)
	it.id = sessID
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Press w.
	_, cmd := mustUpdate(t, m, pressKey('w'))
	if cmd == nil {
		t.Fatal("w: want preflight cmd, got nil")
	}

	// Execute preflight.
	msg := cmd()
	pm, ok := msg.(worktreePreflightMsg)
	if !ok {
		t.Fatalf("want worktreePreflightMsg, got %T", msg)
	}
	if pm.prompt {
		t.Error("want prompt=false for existing ChoiceNone mapping, got true")
	}
	if pm.spec.treePath != treePath {
		t.Errorf("spec.treePath = %q, want %q", pm.spec.treePath, treePath)
	}

	// Feed the message → no modal opened, launchCmd fires.
	m2, launchCmdResult := mustUpdate(t, m, pm)
	if m2.modal.kind != modalNone {
		t.Errorf("want modal=modalNone for no-reprompt, got %v", m2.modal.kind)
	}
	// A launch cmd should have been returned.
	if launchCmdResult == nil {
		t.Error("want non-nil launchCmd for no-reprompt path, got nil")
	}
}

// ── W15: no-reprompt stale worktree → re-open modal ──────────────────────────

func TestWorktree_NoReprompt_StaleWorktree(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
	}

	sessID := "test-session-id"
	stalePath := "/nonexistent/path/that/does/not/exist"

	// Pre-seed mapping pointing to a non-existent path.
	st, _ := state.LoadState(baseDir)
	state.SetMapping(&st, sessID, state.Mapping{
		Tool:   model.ToolClaude,
		Tree:   stalePath,
		Choice: state.ChoiceWorktree,
	})
	if err := state.SaveState(baseDir, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	it := worktreeItem("feat", "/proj/worktrees/feat", "/proj", false, false)
	it.id = sessID
	m := New([]list.Item{it}).WithLoader(ldr)
	m, _ = mustUpdate(t, m, windowMsg)

	// Press w.
	_, cmd := mustUpdate(t, m, pressKey('w'))
	if cmd == nil {
		t.Fatal("w: want preflight cmd, got nil")
	}

	msg := cmd()
	pm, ok := msg.(worktreePreflightMsg)
	if !ok {
		t.Fatalf("want worktreePreflightMsg, got %T", msg)
	}
	if !pm.prompt {
		t.Error("want prompt=true when worktree path is stale (os.Stat fails), got false")
	}

	// Feed the message → modal opens.
	m2, _ := mustUpdate(t, m, pm)
	if m2.modal.kind != modalNewSession {
		t.Errorf("want modal=modalNewSession for stale worktree, got %v", m2.modal.kind)
	}
}

// ── W16: config validate failure → error in worktreeCreatedMsg ────────────────

func TestWorktree_ConfigValidateFailure(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	// A WorktreeDir that resolves into .git → Validate fails.
	// We use a relative path ".git" so it resolves to <projectPath>/.git.
	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config:  &config.Config{WorktreeDir: ".git"},
	}

	m := New(nil).WithLoader(ldr)
	ms := modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        "claude",
		sessionID:   "sess-validate-fail",
		treePath:    "/proj/worktrees/feat",
		branch:      "feat",
		projectPath: "/proj/myrepo",
	}

	// No PostCreate hooks → trust gate doesn't fire; dec=nil is fine.
	cmd := m.worktreeCreateCmd(ms, nil)
	resultMsg := cmd()
	wm, ok := resultMsg.(worktreeCreatedMsg)
	if !ok {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}
	if wm.err == nil {
		t.Fatal("want error from config.Validate, got nil")
	}

	// No git worktree add should have been called.
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "add" {
			t.Errorf("unexpected git worktree add call after validate failure: %v", c.Args)
		}
	}

	// Feed error message → toast is set.
	m2, _ := mustUpdate(t, m, wm)
	if m2.toast == "" {
		t.Error("want toast set after worktreeCreatedMsg{err!=nil}, got empty")
	}
}

// ── EXPLOIT TESTS (T1) ────────────────────────────────────────────────────────
// These tests encode the V1 vulnerability: an untrusted repo's post_create hooks
// must NEVER spawn sh -c before the user approves the .perch.toml. They must
// FAIL on un-fixed code (before the trust gate) and PASS after.

// TestExploit_UntrustedHooks_CreateOpensModal asserts that:
//  1. An untrusted repo with PostCreate hooks → worktreeCreateCmd returns a
//     worktreeCreatedMsg with trust!=nil (opens modalTrustConfirm) and ZERO
//     sh -c calls in FakeRunner.
//  2. Pressing 'd' (deny) → worktree is still created (git worktree add IS called)
//     but still ZERO sh -c calls.
func TestExploit_UntrustedHooks_CreateOpensModal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	projectPath := t.TempDir()

	// The injected config has a post_create hook and a stable path+hash.
	configPath := filepath.Join(projectPath, ".perch.toml")
	configContent := []byte(`post_create = ["touch /tmp/pwned"]`)
	configHashVal := trust.Hash(configContent)

	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config: &config.Config{
			PostCreate:        []string{"touch /tmp/pwned"},
			ProjectConfigPath: configPath,
			ProjectConfigHash: configHashVal,
		},
	}

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)
	ms := modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        "claude",
		sessionID:   "exploit-sess",
		treePath:    filepath.Join(projectPath, "worktrees", "feat"),
		branch:      "feat",
		projectPath: projectPath,
	}

	// Phase 1: untrusted, no trust entry → must stop before git work.
	cmd := m.worktreeCreateCmd(ms, nil)
	resultMsg := cmd()
	wm, ok2 := resultMsg.(worktreeCreatedMsg)
	if !ok2 {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}

	// The trust field must be non-nil (gate fired).
	if wm.trust == nil {
		t.Fatal("EXPLOIT: untrusted repo with PostCreate hooks must return trust!=nil to prompt approval; got nil (hooks would run without consent)")
	}

	// No sh -c must have been called.
	for _, c := range r.Calls {
		if c.Name == "sh" {
			t.Errorf("EXPLOIT: sh -c invoked before trust approval: %v", c.Args)
		}
	}

	// No git worktree add must have been called (gate stops before FS mutation).
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "add" {
			t.Errorf("EXPLOIT: git worktree add called before trust approval: %v", c.Args)
		}
	}

	// Feed the trustNeededMsg into Update → modal opens.
	m2, _ := mustUpdate(t, m, wm)
	if m2.modal.kind != modalTrustConfirm {
		t.Errorf("want modal=modalTrustConfirm after trust-blocked create, got %v", m2.modal.kind)
	}

	// Phase 2: user presses 'd' (deny) → re-dispatches with dec.allow=false.
	// Worktree IS created (git worktree add called), but hooks NOT run.
	r.Calls = nil // reset call log

	decDeny := &trustDecision{allow: false}
	cmd2 := m.worktreeCreateCmd(ms, decDeny)
	resultMsg2 := cmd2()
	wm2, ok3 := resultMsg2.(worktreeCreatedMsg)
	if !ok3 {
		t.Fatalf("deny phase: want worktreeCreatedMsg, got %T", resultMsg2)
	}
	if wm2.err != nil {
		t.Fatalf("deny phase: unexpected error: %v", wm2.err)
	}
	if wm2.trust != nil {
		t.Error("deny phase: trust must be nil (already decided)")
	}

	// git worktree add MUST have been called (worktree creation is benign).
	foundAdd := false
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "add" {
			foundAdd = true
		}
	}
	if !foundAdd {
		t.Error("deny phase: git worktree add must be called even when hooks are denied")
	}

	// Still ZERO sh -c calls.
	for _, c := range r.Calls {
		if c.Name == "sh" {
			t.Errorf("deny phase: sh -c must NOT be called when hooks are denied: %v", c.Args)
		}
	}
}

// TestExploit_PreseededTrust_HooksRun asserts that when the trust store already
// has a matching path@hash entry, hooks run immediately (no modal, sh -c present).
func TestExploit_PreseededTrust_HooksRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()
	projectPath := t.TempDir()

	configPath := filepath.Join(projectPath, ".perch.toml")
	configContent := []byte(`post_create = ["echo trusted"]`)
	configHashVal := trust.Hash(configContent)

	// Pre-seed the trust store.
	store, err := trust.Load(filepath.Join(baseDir, "trust.json"))
	if err != nil {
		t.Fatalf("Load trust store: %v", err)
	}
	if err := store.Approve(configPath, configHashVal); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config: &config.Config{
			PostCreate:        []string{"echo trusted"},
			ProjectConfigPath: configPath,
			ProjectConfigHash: configHashVal,
		},
	}

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)
	ms := modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        "claude",
		sessionID:   "trusted-sess",
		treePath:    filepath.Join(projectPath, "worktrees", "feat"),
		branch:      "feat",
		projectPath: projectPath,
	}

	cmd := m.worktreeCreateCmd(ms, nil)
	resultMsg := cmd()
	wm, ok2 := resultMsg.(worktreeCreatedMsg)
	if !ok2 {
		t.Fatalf("want worktreeCreatedMsg, got %T", resultMsg)
	}
	if wm.err != nil {
		t.Fatalf("unexpected error: %v", wm.err)
	}

	// Trust must be nil (no modal needed — already trusted).
	if wm.trust != nil {
		t.Error("pre-seeded trust: trust field must be nil (no modal needed)")
	}

	// sh -c MUST have been called (hook ran).
	foundSh := false
	for _, c := range r.Calls {
		if c.Name == "sh" {
			foundSh = true
		}
	}
	if !foundSh {
		t.Errorf("pre-seeded trust: sh -c must be called when trust is pre-approved; calls: %v", r.Calls)
	}
}

// TestExploit_UntrustedHooks_RemoveOpensModal asserts that an untrusted repo with
// pre_remove hooks → removeCmd returns a trustNeededMsg (zero sh -c). Deny still
// removes the worktree.
func TestExploit_UntrustedHooks_RemoveOpensModal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	configPath := "/proj/.perch.toml"
	configContent := []byte(`pre_remove = ["touch /tmp/pwned"]`)
	configHashVal := trust.Hash(configContent)

	ldr := loader{
		Tmux:    fakeTmuxInside(r),
		Runner:  r,
		BaseDir: baseDir,
		Now:     1000,
		Config: &config.Config{
			PreRemove:         []string{"touch /tmp/pwned"},
			ProjectConfigPath: configPath,
			ProjectConfigHash: configHashVal,
		},
	}

	ok := proc.FakeResult{}
	r.Default = &ok

	m := New(nil).WithLoader(ldr)

	spec := modalState{
		kind:        modalRemoveConfirm,
		treePath:    "/proj/worktrees/feat",
		branch:      "feat",
		projectPath: "/proj",
	}

	// Phase 1: untrusted → must return trustNeededMsg, zero sh -c.
	cmd := m.removeCmd(spec, false, false, nil)
	resultMsg := cmd()

	tn, ok2 := resultMsg.(trustNeededMsg)
	if !ok2 {
		t.Fatalf("EXPLOIT: want trustNeededMsg for untrusted pre_remove, got %T (hooks would run without consent)", resultMsg)
	}
	if tn.trust == nil {
		t.Fatal("EXPLOIT: trustNeededMsg.trust must be non-nil")
	}
	if tn.trust.phase != "pre_remove" {
		t.Errorf("trust.phase = %q; want pre_remove", tn.trust.phase)
	}

	// Zero sh -c calls.
	for _, c := range r.Calls {
		if c.Name == "sh" {
			t.Errorf("EXPLOIT: sh -c invoked before trust approval: %v", c.Args)
		}
	}

	// Phase 2: feed into Update → modal opens.
	m2, _ := mustUpdate(t, m, tn)
	if m2.modal.kind != modalTrustConfirm {
		t.Errorf("want modal=modalTrustConfirm after trustNeededMsg, got %v", m2.modal.kind)
	}

	// Phase 3: deny → worktree still removed (git worktree remove called), zero sh -c.
	r.Calls = nil

	decDeny := &trustDecision{allow: false}
	cmd2 := m.removeCmd(spec, false, false, decDeny)
	resultMsg2 := cmd2()
	rm, ok3 := resultMsg2.(removeResultMsg)
	if !ok3 {
		t.Fatalf("deny phase: want removeResultMsg, got %T", resultMsg2)
	}
	if rm.err != nil {
		t.Fatalf("deny phase: unexpected error: %v", rm.err)
	}

	// git worktree remove MUST have been called.
	foundRemove := false
	for _, c := range r.Calls {
		if c.Name == "git" && len(c.Args) >= 4 && c.Args[2] == "worktree" && c.Args[3] == "remove" {
			foundRemove = true
		}
	}
	if !foundRemove {
		t.Error("deny phase: git worktree remove must be called even when hooks are denied")
	}

	// Still zero sh -c.
	for _, c := range r.Calls {
		if c.Name == "sh" {
			t.Errorf("deny phase: sh -c must NOT be called when hooks are denied: %v", c.Args)
		}
	}
}
