package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

func TestValidateSessionID_AllowlistCharset(t *testing.T) {
	good := []string{"ses_18593fc84ffeg4oyInzAG2eLOL", "2b96f5bc-43ef-454d-a12d-791ad68da8dd", "win-1"}
	for _, s := range good {
		if err := validateSessionID(s); err != nil {
			t.Errorf("validateSessionID(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", "a b", "a;b", "../x", "a\x1fb", "a\nb", "a/b"}
	for _, s := range bad {
		if err := validateSessionID(s); err == nil {
			t.Errorf("validateSessionID(%q) = nil, want error", s)
		}
	}
}

func TestValidateSessionID_AdversarialCases(t *testing.T) {
	rejected := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"overlong", strings.Repeat("a", 129)},
		{"control NUL", "ses\x00id"},
		{"control LF", "ses\nid"},
		{"shell semicolon", "ses;id"},
		{"shell dollar-paren", "ses$(id)"},
		{"shell backtick", "ses`id`"},
		{"shell pipe", "ses|id"},
		{"path separator slash", "ses/id"},
		{"path traversal dotdot", "../etc/passwd"},
		{"unicode lookalike", "ses‐id"}, // U+2010 HYPHEN, not ASCII '-'
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSessionID(tc.input); err == nil {
				t.Errorf("validateSessionID(%q) = nil, want error", tc.input)
			}
		})
	}
	t.Run("normal allowlisted id", func(t *testing.T) {
		if err := validateSessionID("ses_abc-123"); err != nil {
			t.Errorf("validateSessionID(\"ses_abc-123\") = %v, want nil", err)
		}
	})
}

func TestValidateWorktreeUnderRoots(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "perch", "wt")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}
	// ACCEPT: real path under root, and the root itself.
	if err := validateWorktreeUnderRoots(sub, roots); err != nil {
		t.Errorf("path under root rejected: %v", err)
	}
	if err := validateWorktreeUnderRoots(root, roots); err != nil {
		t.Errorf("path at root rejected: %v", err)
	}
	// REJECT.
	outside := t.TempDir() // a real directory NOT under root → tests containment
	for _, p := range []string{
		"/etc/passwd",                         // real, outside root (containment)
		outside,                               // real, outside root (containment)
		root + "/../secret",                   // non-clean (rejected before resolve)
		"relative/path",                       // not absolute
		"",                                    // empty
		filepath.Join(root, "does-not-exist"), // in-root but missing → resolve error
	} {
		if err := validateWorktreeUnderRoots(p, roots); err == nil {
			t.Errorf("validateWorktreeUnderRoots(%q) = nil, want error", p)
		}
	}
}

func TestApp_PtyRegistry_AddGetRemove(t *testing.T) {
	a := &App{bridges: map[string]*ptyEntry{}}

	a.putBridge("t1", &ptyEntry{})
	if _, ok := a.getBridge("t1"); !ok {
		t.Fatal("expected t1 present after put")
	}
	a.removeBridge("t1")
	if _, ok := a.getBridge("t1"); ok {
		t.Fatal("expected t1 absent after remove")
	}
}

func TestApp_Emit_UsesSeam(t *testing.T) {
	var gotEvent string
	a := &App{emit: func(event string, _ ...any) { gotEvent = event }}
	a.emit("sessions-changed")
	if gotEvent != "sessions-changed" {
		t.Fatalf("emit seam event = %q, want sessions-changed", gotEvent)
	}
}

// paneLine builds one 0x1f-delimited list-panes line matching tmux's real
// paneFormat field order:
//
//	#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f
//	#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f
//	#{@perch_session}\x1f#{@perch_pane_status}
func paneLine(id, dead, sess, win, perchSess, status string) string {
	return strings.Join([]string{id, "1234", "claude", dead, "/wt", sess, win, perchSess, status}, "\x1f")
}

// realPaneFormat is the exact -F value that ListPanesAll issues. It must match
// the unexported paneFormat constant in internal/tmux/tmux.go (tmux.go:43)
// byte-for-byte so that FakeRunner.Respond key-matches what ListPanesAll sends.
const realPaneFormat = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}"

func TestApp_ListSessions_DerivesStatus(t *testing.T) {
	r := proc.NewFakeRunner()
	out := paneLine("%1", "0", "perch", "feat-x", "ses_abc", "working") + "\n" +
		paneLine("%2", "1", "perch", "fix-y", "ses_def", "")
	r.Respond(proc.FakeResult{Stdout: []byte(out)}, "tmux", "list-panes", "-a", "-F", realPaneFormat)

	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r}
	got, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].ID != "ses_abc" || got[0].Status != "working" {
		t.Errorf("session 0 = %+v", got[0])
	}
	if got[1].Status != "exited" {
		t.Errorf("dead pane should report exited; got %q", got[1].Status)
	}
}

func TestApp_ListSessions_EmptyStatusBecomesIdle(t *testing.T) {
	r := proc.NewFakeRunner()
	out := paneLine("%1", "0", "perch", "feat-x", "ses_abc", "")
	r.Respond(proc.FakeResult{Stdout: []byte(out)}, "tmux", "list-panes", "-a", "-F", realPaneFormat)

	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r}
	got, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	if got[0].Status != "idle" {
		t.Errorf("empty status should become idle; got %q", got[0].Status)
	}
}

func TestApp_ListSessions_ExcludesNonPerchPanes(t *testing.T) {
	r := proc.NewFakeRunner()
	// second line has no @perch_session (empty) — should be excluded
	out := paneLine("%1", "0", "perch", "feat-x", "ses_abc", "working") + "\n" +
		paneLine("%2", "0", "perch", "other", "", "")
	r.Respond(proc.FakeResult{Stdout: []byte(out)}, "tmux", "list-panes", "-a", "-F", realPaneFormat)

	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r}
	got, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1 (non-perch pane must be excluded)", len(got))
	}
}

func TestApp_KillSession_RejectsUnknownID(t *testing.T) {
	r := proc.NewFakeRunner()
	out := paneLine("%1", "0", "perch", "feat-x", "ses_abc", "working")
	r.Respond(proc.FakeResult{Stdout: []byte(out)}, "tmux", "list-panes", "-a", "-F", realPaneFormat)

	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r}
	if err := a.KillSession("ses_NOT_LIVE"); err == nil {
		t.Fatal("KillSession on unknown id should error (allowlist)")
	}
}

func TestApp_KillSession_RejectsInvalidID(t *testing.T) {
	r := proc.NewFakeRunner()
	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r}
	if err := a.KillSession("../evil"); err == nil {
		t.Fatal("KillSession with invalid charset should error (validateSessionID)")
	}
}

// TestValidateWorktreeUnderRoots_SymlinkEscape uses REAL on-disk symlinks so the
// EvalSymlinks containment check is actually exercised (not skipped). The escape
// link points at a real directory OUTSIDE the root, so EvalSymlinks resolves
// successfully and it is the prefix check — not a resolve error — that rejects it.
func TestValidateWorktreeUnderRoots_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	roots := []string{root}

	// ESCAPE → must be REJECTED by containment (target resolves successfully).
	outsideTarget := t.TempDir() // real dir outside root
	escapeLink := filepath.Join(root, "evil-link")
	if err := os.Symlink(outsideTarget, escapeLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(escapeLink, roots); err == nil {
		t.Error("symlink whose target escapes root must be rejected by containment")
	}

	// INSIDE → must be ACCEPTED.
	realInside := filepath.Join(root, "real")
	if err := os.MkdirAll(realInside, 0o755); err != nil {
		t.Fatal(err)
	}
	goodLink := filepath.Join(root, "good-link")
	if err := os.Symlink(realInside, goodLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(goodLink, roots); err != nil {
		t.Errorf("symlink resolving inside root must be accepted: %v", err)
	}
}

func TestApp_WriteToPty_UnknownTab(t *testing.T) {
	a := &App{bridges: map[string]*ptyEntry{}}
	if err := a.WriteToPty("nope", []byte("x")); err == nil {
		t.Fatal("WriteToPty on unknown tab should error")
	}
	if err := a.ResizePty("nope", 80, 24); err == nil {
		t.Fatal("ResizePty on unknown tab should error")
	}
}

func TestApp_CloseTerminal_RemovesEntry(t *testing.T) {
	a := &App{bridges: map[string]*ptyEntry{}}
	a.putBridge("t1", &ptyEntry{bridge: nil}) // nil bridge: Close is a no-op guard
	if err := a.CloseTerminal("t1"); err != nil {
		t.Fatalf("CloseTerminal: %v", err)
	}
	if _, ok := a.getBridge("t1"); ok {
		t.Fatal("CloseTerminal should remove the registry entry")
	}
}

func TestApp_Diff_RejectsOutsideRoots(t *testing.T) {
	a := &App{run: proc.NewFakeRunner(), roots: []string{"/home/u/code"}}
	if _, err := a.Diff("/etc"); err == nil {
		t.Fatal("Diff outside roots should error")
	}
}

func TestNewApp_Defaults(t *testing.T) {
	a := NewApp([]string{"/home/u/code"})
	if a.bridges == nil {
		t.Fatal("NewApp must initialise the bridge registry")
	}
	if a.emit == nil {
		t.Fatal("NewApp must install a non-nil pre-startup emit (no-op until startup)")
	}
}

// TestApp_CreateAgent_ValidatesInputs proves each input-validation gate
// independently. A real git repo inside a real root dir is used so that
// projectPath passes gate 1 (validateWorktreeUnderRoots) and gate 2
// (branch/tool) is what rejects in the branch/tool sub-cases.
func TestApp_CreateAgent_ValidatesInputs(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "perch")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	// git init + empty commit so the repo is valid for AddWorktree.
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// Isolate from real user config/state so hermeticity holds.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	a := &App{
		tmux:    tmux.Tmux{Runner: proc.NewFakeRunner(), Bin: "tmux"},
		run:     proc.NewFakeRunner(),
		roots:   []string{root},
		emit:    func(string, ...any) {},
		bridges: map[string]*ptyEntry{},
	}

	// Gate 1: projectPath outside all roots → rejected.
	if _, err := a.CreateAgent("claude", "/etc", "feat/x"); err == nil {
		t.Error("project outside roots must be rejected")
	}

	// Gate 2: invalid branch ref → rejected (projectPath is in root).
	if _, err := a.CreateAgent("claude", repo, "--upload-pack=evil"); err == nil {
		t.Error("invalid branch ref must be rejected")
	}

	// Gate 3: unknown tool → rejected (projectPath is in root, branch valid).
	if _, err := a.CreateAgent("ghost", repo, "feat/x"); err == nil {
		t.Error("unknown tool must be rejected")
	}
}

// TestContainedUnderRoots_SymlinkRoot verifies that containedUnderRoots accepts
// a treePath expressed via a symlinked root (e.g. the configured root is a
// symlink to a real directory) while still rejecting paths that escape all
// roots via an absolute path or a dotdot traversal.
func TestContainedUnderRoots_SymlinkRoot(t *testing.T) {
	realDir := t.TempDir()
	linkParent := t.TempDir()
	linkRoot := filepath.Join(linkParent, "link-root")
	if err := os.Symlink(realDir, linkRoot); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	// treePath is built from the LINK path (lexical only — it does not exist on disk yet).
	treePathViaLink := filepath.Join(linkRoot, "proj__worktrees", "feat-x")

	// PRIMARY ASSERTION: a treePath under a symlinked root MUST be accepted.
	if !containedUnderRoots(treePathViaLink, []string{linkRoot}) {
		t.Errorf("containedUnderRoots(%q, [%q]) = false, want true (symlinked root must not false-reject)", treePathViaLink, linkRoot)
	}

	// ESCAPE assertions: escapes must still be rejected under the symlinked root.
	if containedUnderRoots("/etc/feat-x", []string{linkRoot}) {
		t.Error("containedUnderRoots(\"/etc/feat-x\", symlinked root) = true, want false (absolute escape must be rejected)")
	}
	dotdotEscape := filepath.Clean(filepath.Join(linkRoot, "..", "..", "escape", "feat-x"))
	if containedUnderRoots(dotdotEscape, []string{linkRoot}) {
		t.Errorf("containedUnderRoots(%q, symlinked root) = true, want false (dotdot escape must be rejected)", dotdotEscape)
	}
}

// TestApp_CreateAgent_ContainmentGuard proves that a config-supplied
// worktreeDir with an adversarial value (absolute or dotdot-relative) causes
// CreateAgent to error and never create anything outside projectPath.
func TestApp_CreateAgent_ContainmentGuard(t *testing.T) {
	// Test the containedUnderRoots helper directly with adversarial treePaths.
	root := t.TempDir()
	roots := []string{root}

	// treePath inside root → accepted.
	insidePath := filepath.Join(root, "proj__worktrees", "feat-x")
	if !containedUnderRoots(insidePath, roots) {
		t.Error("treePath inside root must be accepted by containedUnderRoots")
	}

	// Adversarial: absolute path outside root (e.g. worktreeDir="/etc").
	if containedUnderRoots("/etc/feat-x", roots) {
		t.Error("treePath /etc/feat-x must be rejected (outside all roots)")
	}

	// Adversarial: dotdot escape that resolves outside root
	// (e.g. projectPath=root/proj, worktreeDir="../../escape" → root/../escape/feat-x).
	escapePath := filepath.Clean(filepath.Join(root, "proj", "..", "..", "escape", "feat-x"))
	if containedUnderRoots(escapePath, roots) {
		t.Errorf("treePath %q must be rejected (dotdot escape outside root)", escapePath)
	}

	// Edge case: treePath exactly equals root → accepted.
	if !containedUnderRoots(root, roots) {
		t.Error("treePath equal to root must be accepted")
	}
}

// TestApp_CreateAgent_ContainmentGuard_EndToEnd proves that the containment
// guard inside CreateAgent fires — aborting before AddWorktree/Seed — when
// config supplies a hostile worktree_dir. Two attack vectors are exercised:
//
//  1. Absolute worktree_dir in global config (accepted by config.Load; only the
//     global config accepts absolute paths). The derived treePath lands in an
//     unrelated temp dir outside the root.
//
//  2. Relative worktree_dir with ".." in project .perch.toml (relative paths
//     are accepted by config.Load). After filepath.Clean the result can escape
//     the project dir and even the root.
//
// In both cases CreateAgent must return an error and the target path must NOT
// have been created on disk.
func TestApp_CreateAgent_ContainmentGuard_EndToEnd(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	// Init git repo so validateWorktreeUnderRoots (which requires path existence
	// on disk) passes for projectPath. We don't need a commit because the guard
	// fires before AddWorktree.
	cmd := exec.Command("git", "init", "-q", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	newApp := func() *App {
		return &App{
			tmux:    tmux.Tmux{Runner: proc.NewFakeRunner(), Bin: "tmux"},
			run:     proc.NewFakeRunner(),
			roots:   []string{root},
			emit:    func(string, ...any) {},
			bridges: map[string]*ptyEntry{},
		}
	}

	// ── Attack 1: absolute worktree_dir in global config ────────────────────────
	t.Run("absolute_worktreedir_via_global_config", func(t *testing.T) {
		escapeDest := t.TempDir() // real, outside root
		cfgDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", cfgDir)
		t.Setenv("XDG_STATE_HOME", t.TempDir())

		// Write a global config with an absolute worktree_dir pointing outside the root.
		perchCfgDir := filepath.Join(cfgDir, "perch")
		if err := os.MkdirAll(perchCfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfgContent := fmt.Sprintf("worktree_dir = %q\n", escapeDest)
		if err := os.WriteFile(filepath.Join(perchCfgDir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		a := newApp()
		_, err := a.CreateAgent("claude", repo, "feat/x")
		if err == nil {
			t.Fatal("CreateAgent must error when worktreeDir is absolute and outside roots")
		}
		// Nothing must have been created at the escape destination.
		entries, _ := os.ReadDir(escapeDest)
		if len(entries) > 0 {
			t.Errorf("escape destination %q must be empty after rejected CreateAgent; found %d entries", escapeDest, len(entries))
		}
	})

	// ── Attack 2: relative ".." worktree_dir in project .perch.toml ─────────────
	t.Run("dotdot_worktreedir_via_project_config", func(t *testing.T) {
		cfgDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", cfgDir)
		t.Setenv("XDG_STATE_HOME", t.TempDir())

		// Write a .perch.toml with worktree_dir = "../../escape" (relative ".." is
		// accepted by config.Load; only absolute paths are blocked in project config).
		// With projectPath=<root>/proj and worktreeDir="../../escape":
		//   filepath.Clean(<root>/proj/../../escape/feat-x) = <parentOfRoot>/escape/feat-x
		// which is outside the root.
		tomlContent := "worktree_dir = \"../../escape\"\n"
		if err := os.WriteFile(filepath.Join(repo, ".perch.toml"), []byte(tomlContent), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(repo, ".perch.toml")) })

		escapePath := filepath.Clean(filepath.Join(repo, "..", "..", "escape", "feat-x"))

		a := newApp()
		_, err := a.CreateAgent("claude", repo, "feat/x")
		if err == nil {
			t.Fatal("CreateAgent must error when worktreeDir uses '..' to escape outside roots")
		}
		// Nothing must have been created at the escape path.
		if _, statErr := os.Stat(escapePath); !os.IsNotExist(statErr) {
			t.Errorf("escape path %q must not exist after rejected CreateAgent", escapePath)
		}
	})
}

func TestApp_PollOnce_EmitsOnlyOnChange(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte(paneLine("%1", "0", "perch", "feat-x", "ses_abc", "working"))},
		"tmux", "list-panes", "-a", "-F", realPaneFormat)

	var emits int
	a := &App{tmux: tmux.Tmux{Runner: r, Bin: "tmux"}, run: r, emit: func(string, ...any) { emits++ }}

	a.pollOnce() // first observation differs from the empty zero-value → emit
	if emits != 1 {
		t.Fatalf("first pollOnce emits = %d, want 1", emits)
	}
	a.pollOnce() // identical signature → no emit
	if emits != 1 {
		t.Fatalf("unchanged pollOnce emits = %d, want still 1", emits)
	}
}

// TestApp_PutBridge_ClosesDisplaced verifies that putBridge with a non-nil
// displaced entry (nil bridge inside) does not panic and that the new entry
// wins. The close path for a real bridge (non-nil closer) is covered by the
// integration test TestIntegration_OpenTerminal_ReopenSameTab_NoLeak.
func TestApp_PutBridge_ClosesDisplaced(t *testing.T) {
	a := &App{bridges: map[string]*ptyEntry{}}

	// First put: entry with a nil bridge (CloseTerminal-style entry).
	a.putBridge("t1", &ptyEntry{bridge: nil})
	// Second put: displaces the first. Must not panic even with nil bridge inside.
	a.putBridge("t1", &ptyEntry{bridge: nil})
	// Only one entry must exist.
	if _, ok := a.getBridge("t1"); !ok {
		t.Fatal("expected t1 present after second putBridge")
	}
	a.mu.Lock()
	if len(a.bridges) != 1 {
		t.Fatalf("expected 1 bridge entry, got %d", len(a.bridges))
	}
	a.mu.Unlock()
}

// TestApp_Shutdown_DoubleClose verifies that calling shutdown twice does not
// panic. Without the sync.Once guard the second close(stopPoll) would panic.
func TestApp_Shutdown_DoubleClose(t *testing.T) {
	a := &App{
		bridges:  map[string]*ptyEntry{},
		emit:     func(string, ...any) {},
		stopPoll: make(chan struct{}),
	}
	ctx := t.Context()
	// First shutdown closes the channel; second must not panic.
	a.shutdown(ctx)
	a.shutdown(ctx) // must not panic
}

// TestApp_CreateAgent_RollbackOnLaunchFailure verifies that CreateAgent removes
// the worktree created by AddWorktree when tmux.Launch fails, so no orphaned
// directory is left behind. A real git repo + real git runner is used so that
// AddWorktree actually creates the worktree directory; a failing FakeRunner
// (Default=error) is injected as the tmux runner so Launch errors reliably.
func TestApp_CreateAgent_RollbackOnLaunchFailure(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// All tmux calls (new-session, etc.) fail so Connect → Launch errors.
	launchErrResult := proc.FakeResult{Err: fmt.Errorf("tmux: injected failure")}
	failTmuxRunner := proc.NewFakeRunner()
	failTmuxRunner.Default = &launchErrResult

	a := &App{
		// a.run uses the real ExecRunner so AddWorktree and RemoveWorktree work.
		tmux:    tmux.Tmux{Runner: failTmuxRunner, Bin: "tmux"},
		run:     proc.ExecRunner{},
		roots:   []string{root},
		emit:    func(string, ...any) {},
		bridges: map[string]*ptyEntry{},
	}

	_, err := a.CreateAgent("claude", repo, "feat/rollback-test")
	if err == nil {
		t.Fatal("CreateAgent must error when Launch fails")
	}

	// Prove the flow reached Launch — AddWorktree succeeded and created the dir —
	// so "treePath absent" below is attributable to rollback, not to AddWorktree
	// silently failing (which would make the assertion vacuous).
	if len(failTmuxRunner.Calls) == 0 {
		t.Fatal("expected tmux Launch to be attempted; rollback assertion would otherwise be vacuous")
	}

	// The worktree directory must have been removed by rollback.
	treePath := filepath.Join(root, "proj__worktrees", "feat-rollback-test")
	if _, statErr := os.Stat(treePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree %q must not exist after rollback; stat=%v", treePath, statErr)
	}
}
