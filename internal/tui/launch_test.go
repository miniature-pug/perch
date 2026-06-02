package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── adapterFor ────────────────────────────────────────────────────────────────

func TestAdapterFor_Claude(t *testing.T) {
	a, ok := adapterFor("claude")
	if !ok {
		t.Fatal("adapterFor(claude): want ok=true")
	}
	if a == nil {
		t.Fatal("adapterFor(claude): want non-nil adapter")
	}
	if a.Name() != "claude" {
		t.Errorf("adapter Name() = %q, want claude", a.Name())
	}
}

func TestAdapterFor_Opencode(t *testing.T) {
	a, ok := adapterFor("opencode")
	if !ok {
		t.Fatal("adapterFor(opencode): want ok=true")
	}
	if a.Name() != "opencode" {
		t.Errorf("adapter Name() = %q, want opencode", a.Name())
	}
}

func TestAdapterFor_Unknown(t *testing.T) {
	_, ok := adapterFor("bogus")
	if ok {
		t.Error("adapterFor(bogus): want ok=false")
	}
}

// ── newSessionID ──────────────────────────────────────────────────────────────

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewSessionID_Format(t *testing.T) {
	id, err := newSessionID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !uuidV4Re.MatchString(id) {
		t.Errorf("newSessionID() = %q, want v4 UUID format", id)
	}
}

func TestNewSessionID_TwoCallsDiffer(t *testing.T) {
	a, _ := newSessionID()
	b, _ := newSessionID()
	if a == b {
		t.Error("two newSessionID calls returned the same value")
	}
}

// ── test helpers ──────────────────────────────────────────────────────────────

// fakeTmuxInside returns a Tmux configured with TMUX set (inside-tmux).
func fakeTmuxInside(r *proc.FakeRunner) tmux.Tmux {
	return tmux.Tmux{
		Runner: r,
		Bin:    "tmux",
		Getenv: func(key string) string {
			if key == "TMUX" {
				return "/tmp/tmux-1000/default,1234,0"
			}
			return ""
		},
	}
}

// fakeTmuxOutside returns a Tmux with TMUX unset (outside-tmux).
func fakeTmuxOutside(r *proc.FakeRunner) tmux.Tmux {
	return tmux.Tmux{
		Runner: r,
		Bin:    "tmux",
		Getenv: func(string) string { return "" },
	}
}

// baseItem builds an idle item with fixed paths for predictable SessionName/WindowName.
func baseItem(tool, branch, sessionID string) item {
	return item{
		tool:        tool,
		tree:        branch,
		id:          sessionID,
		projectPath: "/proj/myrepo",
		treePath:    "/proj/myrepo",
		isSession:   true,
		live:        false,
	}
}

// registerLaunchCalls adds the standard FakeRunner responses for a cold-start
// launch (has-session exit 1 → new-session) with arbitrary send-keys and
// set-option. display-message for BootID must be registered separately when
// the exact literal matters.
func registerLaunchCalls(r *proc.FakeRunner, session, window, dir, paneID string) {
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "has-session", "-t", "="+session)
	r.Respond(proc.FakeResult{Stdout: []byte(paneID + "\n")},
		"tmux", "new-session", "-d", "-s", session, "-n", window, "-c", dir, "-P", "-F", "#{pane_id}")
	// Send-keys literal and Enter have unpredictable content (UUID) so they are
	// matched via the Default response; only pane-specific calls are registered above.
}

// expectedSession returns the tmux session name derived from the fixed projectPath.
func expectedSession() string {
	return tmux.SessionName("/proj/myrepo")
}

// expectedWindow returns the tmux window name derived from the given branch.
func expectedWindow(branch string) string {
	return tmux.WindowName(branch)
}

// ── Resume idle claude ────────────────────────────────────────────────────────

func TestLaunch_ResumeClaude(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%1"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	// Use Default for send-keys and set-option (predictable args but simpler setup).
	ok := proc.FakeResult{}
	r.Default = &ok
	// BootID via display-message.
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")
	// switch-client after launchedMsg.
	r.Respond(proc.FakeResult{},
		"tmux", "switch-client", "-t", tmux.WindowTarget(sess, win))

	sessID := "claude-session-abc"
	it := baseItem("claude", "feat", sessID)
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Press Enter → triggers launchCmd.
	updated1, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated1.(Model)
	if cmd == nil {
		t.Fatal("Enter on idle session: want a launch cmd, got nil")
	}

	// Execute the launch cmd.
	msg := cmd()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// Assert send-keys literal contains --resume and the session id.
	var sendKeysLiteral string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
			break
		}
	}
	if !strings.Contains(sendKeysLiteral, "--resume") {
		t.Errorf("send-keys literal %q: want --resume", sendKeysLiteral)
	}
	if !strings.Contains(sendKeysLiteral, sessID) {
		t.Errorf("send-keys literal %q: want session id %q", sendKeysLiteral, sessID)
	}

	// Assert set-option stamped @perch_session with the session id.
	var setOptVal string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "set-option" {
			for i, a := range c.Args {
				if a == "@perch_session" && i+1 < len(c.Args) {
					setOptVal = c.Args[i+1]
				}
			}
		}
	}
	if setOptVal != sessID {
		t.Errorf("set-option @perch_session = %q, want %q", setOptVal, sessID)
	}

	// Feed launchedMsg → triggers attachTo → switch-client cmd.
	_, cmd2 := m.Update(lm)
	if cmd2 == nil {
		t.Fatal("launchedMsg: want a switch-client cmd, got nil")
	}

	// Execute switch-client cmd.
	msg2 := cmd2()
	sm, ok3 := msg2.(switchedMsg)
	if !ok3 {
		t.Fatalf("want switchedMsg, got %T", msg2)
	}
	if sm.err != nil {
		t.Fatalf("switchedMsg error: %v", sm.err)
	}

	// Assert switch-client was called with the right target.
	wantTarget := tmux.WindowTarget(sess, win)
	found := false
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "switch-client" {
			if len(c.Args) >= 3 && c.Args[2] == wantTarget {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("want switch-client -t %q, not found in calls: %v", wantTarget, r.Calls)
	}
}

// ── New session claude ────────────────────────────────────────────────────────

func TestLaunch_NewClaude(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%2"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	it := baseItem("claude", "feat", "")
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Press n → new session.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("New on item: want a launch cmd, got nil")
	}

	msg := cmd()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// Extract the send-keys literal and the set-option value.
	var sendKeysLiteral string
	var setOptVal string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
		}
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "set-option" {
			for i, a := range c.Args {
				if a == "@perch_session" && i+1 < len(c.Args) {
					setOptVal = c.Args[i+1]
				}
			}
		}
	}

	if !strings.Contains(sendKeysLiteral, "--session-id") {
		t.Errorf("send-keys literal %q: want --session-id", sendKeysLiteral)
	}
	if !uuidV4Re.MatchString(setOptVal) {
		t.Errorf("set-option @perch_session = %q: want a v4 UUID", setOptVal)
	}
	// The same UUID must appear in both send-keys and set-option.
	if !strings.Contains(sendKeysLiteral, setOptVal) {
		t.Errorf("send-keys literal %q does not contain UUID %q from set-option", sendKeysLiteral, setOptVal)
	}
}

// ── New session opencode ──────────────────────────────────────────────────────

func TestLaunch_NewOpencode(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%3"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	it := baseItem("opencode", "feat", "")
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("New opencode: want launch cmd")
	}

	msg := cmd()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// No set-option call must be recorded (opencode sid is empty).
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "set-option" {
			t.Errorf("unexpected set-option call for opencode-new: %v", c.Args)
		}
	}

	// Send-keys literal must be just 'opencode' (no flags from NewArgs).
	var sendKeysLiteral string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
			break
		}
	}
	if sendKeysLiteral != "'opencode'" {
		t.Errorf("send-keys literal = %q, want 'opencode'", sendKeysLiteral)
	}
}

// ── Live session Enter → switch, no relaunch ──────────────────────────────────

func TestLaunch_LiveEnterSwitchesOnly(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	wantTarget := "=sess:=win"
	r.Respond(proc.FakeResult{}, "tmux", "switch-client", "-t", wantTarget)

	liveIt := item{
		tool:          "claude",
		tree:          "feat",
		id:            "live-sess-id",
		projectPath:   "/proj/myrepo",
		treePath:      "/proj/myrepo",
		isSession:     true,
		live:          true,
		liveTarget:    wantTarget,
		captureTarget: "%99",
	}
	m := New([]list.Item{liveIt}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Press Enter on a live item.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on live item: want a switch cmd, got nil")
	}

	// Execute: should be a switch-client directly (not a launch).
	msg := cmd()
	sm, ok := msg.(switchedMsg)
	if !ok {
		t.Fatalf("want switchedMsg, got %T: %v", msg, msg)
	}
	if sm.err != nil {
		t.Fatalf("switchedMsg error: %v", sm.err)
	}

	// The ONLY tmux call must be switch-client — no new-session/send-keys.
	if len(r.Calls) != 1 {
		t.Fatalf("want exactly 1 tmux call (switch-client), got %d: %v", len(r.Calls), r.Calls)
	}
	if r.Calls[0].Args[0] != "switch-client" {
		t.Errorf("want switch-client as the only call, got %v", r.Calls[0].Args)
	}
	if r.Calls[0].Args[2] != wantTarget {
		t.Errorf("switch-client target = %q, want %q", r.Calls[0].Args[2], wantTarget)
	}
}

// ── Frecency + shadow record ──────────────────────────────────────────────────

func TestLaunch_FrecencyAndShadowRecord(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%5"
	sessID := "claude-track-id"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("99999\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	it := baseItem("claude", "feat", sessID)
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("want launch cmd")
	}
	cmd() // execute the blocking work

	// Frecency: project path must be bumped.
	st, err := state.LoadState(baseDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	ps, present := st.Projects["/proj/myrepo"]
	if !present {
		t.Error("project path not in frecency state after launch")
	}
	if ps.Rank <= 0 {
		t.Errorf("project rank = %v, want > 0", ps.Rank)
	}

	// Shadow record: windows/<paneKey>.json must exist with expected fields.
	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("want 1 window record, got %d", len(windows))
	}
	w := windows[0]
	if w.SessionID != sessID {
		t.Errorf("window SessionID = %q, want %q", w.SessionID, sessID)
	}
	if w.Tree != "/proj/myrepo" {
		t.Errorf("window Tree = %q, want /proj/myrepo", w.Tree)
	}
	if w.TmuxSession != sess {
		t.Errorf("window TmuxSession = %q, want %q", w.TmuxSession, sess)
	}
	if w.TmuxWindow != win {
		t.Errorf("window TmuxWindow = %q, want %q", w.TmuxWindow, win)
	}
	if w.BootID != "99999" {
		t.Errorf("window BootID = %q, want 99999", w.BootID)
	}
	if w.PaneKey != paneID {
		t.Errorf("window PaneKey = %q, want %q", w.PaneKey, paneID)
	}
}

// ── Fork claude session ───────────────────────────────────────────────────────

func TestLaunch_ForkClaude(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%10"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	m := New(nil).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})

	spec := launchSpec{
		tool:        "claude",
		sessionID:   "seed-abc",
		fork:        true,
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	var sendKeysLiteral string
	var setOptVal string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
		}
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "set-option" {
			for i, a := range c.Args {
				if a == "@perch_session" && i+1 < len(c.Args) {
					setOptVal = c.Args[i+1]
				}
			}
		}
	}

	// The literal must carry ForkInto args + the pinned --session-id.
	if !strings.Contains(sendKeysLiteral, "--resume") {
		t.Errorf("send-keys literal %q: want --resume", sendKeysLiteral)
	}
	if !strings.Contains(sendKeysLiteral, "seed-abc") {
		t.Errorf("send-keys literal %q: want seed id seed-abc", sendKeysLiteral)
	}
	if !strings.Contains(sendKeysLiteral, "--fork-session") {
		t.Errorf("send-keys literal %q: want --fork-session", sendKeysLiteral)
	}
	if !strings.Contains(sendKeysLiteral, "--session-id") {
		t.Errorf("send-keys literal %q: want --session-id", sendKeysLiteral)
	}
	// @perch_session is stamped with the pre-minted fork id.
	if !uuidV4Re.MatchString(setOptVal) {
		t.Errorf("set-option @perch_session = %q: want a v4 UUID", setOptVal)
	}
	// The same UUID appears in send-keys (pinned id == stamped id).
	if !strings.Contains(sendKeysLiteral, setOptVal) {
		t.Errorf("send-keys literal %q does not contain pinned UUID %q", sendKeysLiteral, setOptVal)
	}
}

// ── Fork opencode → fresh-session fallback ────────────────────────────────────

func TestLaunch_ForkOpencodeFallback(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%11"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	m := New(nil).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})

	spec := launchSpec{
		tool:        "opencode",
		sessionID:   "ses_seed",
		fork:        true,
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// ErrForkUnsupported → no @perch_session stamp (sid stays empty).
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 1 && c.Args[0] == "set-option" {
			t.Errorf("unexpected set-option call for opencode fork fallback: %v", c.Args)
		}
	}

	// send-keys literal must be bare 'opencode' (NewArgs{} returns nil).
	var sendKeysLiteral string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
			break
		}
	}
	if sendKeysLiteral != "'opencode'" {
		t.Errorf("send-keys literal = %q, want 'opencode'", sendKeysLiteral)
	}
}

// ── New with empty list (no selection) ───────────────────────────────────────

func TestLaunch_NewWithNoSelection(t *testing.T) {
	r := proc.NewFakeRunner()
	m := New([]list.Item{}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: t.TempDir(),
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd != nil {
		t.Error("New with no selection: want nil cmd, got non-nil")
	}
	if len(r.Calls) != 0 {
		t.Errorf("want 0 tmux calls, got %d: %v", len(r.Calls), r.Calls)
	}
}

// ── attachTo outside tmux returns non-nil cmd ─────────────────────────────────

func TestAttachTo_OutsideTmuxReturnsCmd(t *testing.T) {
	r := proc.NewFakeRunner()
	m := New(nil).WithLoader(loader{
		Tmux:    fakeTmuxOutside(r),
		BaseDir: t.TempDir(),
		Now:     1000,
	})

	// attachTo with TMUX unset → attach-session path → tea.ExecProcess cmd.
	// We assert it's non-nil but do NOT execute it (that would spawn a real process).
	target := tmux.WindowTarget("sess", "win")
	_, cmd := m.attachTo(target)
	if cmd == nil {
		t.Error("attachTo outside tmux: want non-nil cmd for attach-session path")
	}
}

// TestLaunch_FrecencyAndShadowRecord_WindowPath verifies the shadow record file
// exists under the expected path (using filepath logic, not guessing encoding).
func TestLaunch_ShadowRecordFileExists(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%7"
	sessID := "shadow-check-id"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	it := baseItem("claude", "feat", sessID)
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cmd()

	// The windows dir must contain exactly one .json file.
	entries, err := filepath.Glob(filepath.Join(baseDir, "windows", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 window json file, got %d", len(entries))
	}
}

// ── resolveTool ───────────────────────────────────────────────────────────────

// TestResolveTool_ItemToolWins asserts that a non-empty itemTool is always
// returned unchanged, regardless of config.
func TestResolveTool_ItemToolWins(t *testing.T) {
	m := New(nil)
	m.cfg = &config.Config{
		Agent:     model.Tool("opencode"),
		Wildcards: []config.WildcardRule{{Pattern: "**/experiments/**", Agent: model.Tool("opencode")}},
	}
	got := m.resolveTool("/any/path", "opencode")
	if got != "opencode" {
		t.Errorf("resolveTool with itemTool=%q: want %q, got %q", "opencode", "opencode", got)
	}
}

// TestResolveTool_WildcardWins asserts that a matching wildcard rule is used
// when itemTool is empty, overriding the default agent.
func TestResolveTool_WildcardWins(t *testing.T) {
	m := New(nil)
	m.cfg = &config.Config{
		Agent: model.Tool("opencode"),
		Wildcards: []config.WildcardRule{
			{Pattern: "**/experiments/**", Agent: model.Tool("claude")},
		},
	}
	got := m.resolveTool("/home/user/projects/experiments/foo", "")
	if got != "claude" {
		t.Errorf("resolveTool with wildcard match: want %q, got %q", "claude", got)
	}
}

// TestResolveTool_DefaultAgentWins asserts that cfg.Agent is used when itemTool
// is empty and no wildcard matches.
func TestResolveTool_DefaultAgentWins(t *testing.T) {
	m := New(nil)
	m.cfg = &config.Config{
		Agent: model.Tool("opencode"),
	}
	got := m.resolveTool("/home/user/projects/normal/foo", "")
	if got != "opencode" {
		t.Errorf("resolveTool with cfg.Agent: want %q, got %q", "opencode", got)
	}
}

// TestResolveTool_FallbackClaude asserts that when cfg is nil and itemTool is
// empty, "claude" is returned as the unconditional fallback.
func TestResolveTool_FallbackClaude(t *testing.T) {
	m := New(nil) // no cfg
	got := m.resolveTool("/any/path", "")
	if got != "claude" {
		t.Errorf("resolveTool with nil cfg + empty itemTool: want %q, got %q", "claude", got)
	}
}

// ── Agent binary ──────────────────────────────────────────────────────────────

// loadConfigWithAgents writes a temporary global config file with the given
// [agents] table and loads it via config.Load, so the unexported agentBins
// field is populated from TOML rather than through direct struct initialisation.
func loadConfigWithAgents(t *testing.T, agents map[string]string) *config.Config {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("[agents]\n")
	for name, path := range agents {
		sb.WriteString(name + " = " + `"` + path + `"` + "\n")
	}
	gp := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(gp, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(gp, "")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestLaunch_AgentBinaryFromConfig verifies that when [agents] maps claude to
// an absolute path, that path is used as argv[0] in the send-keys literal.
func TestLaunch_AgentBinaryFromConfig(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%20"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	cfg := loadConfigWithAgents(t, map[string]string{"claude": "/opt/claude"})

	it := baseItem("claude", "feat", "")
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	m.cfg = cfg

	spec := launchSpec{
		tool:        "claude",
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
		resume:      false,
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// The send-keys literal must start with the configured binary path.
	var sendKeysLiteral string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
			break
		}
	}
	if !strings.HasPrefix(sendKeysLiteral, "'/opt/claude'") {
		t.Errorf("send-keys literal %q: want prefix '/opt/claude'", sendKeysLiteral)
	}
}

// TestLaunch_AgentBinaryFallbackNoCfg verifies that when no config is set the
// bare tool name ("claude") is used as argv[0].
func TestLaunch_AgentBinaryFallbackNoCfg(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%21"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	it := baseItem("claude", "feat", "")
	// No cfg set: m.cfg remains nil.
	m := New([]list.Item{it}).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})

	spec := launchSpec{
		tool:        "claude",
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
		resume:      false,
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// The send-keys literal must start with 'claude' (bare name).
	var sendKeysLiteral string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			sendKeysLiteral = c.Args[4]
			break
		}
	}
	if !strings.HasPrefix(sendKeysLiteral, "'claude'") {
		t.Errorf("send-keys literal %q: want prefix 'claude' (bare fallback)", sendKeysLiteral)
	}
}

// ── startup_command ───────────────────────────────────────────────────────────

// TestLaunch_StartupCommandSent verifies that when cfg.StartupCommand is set,
// a send-keys call carrying the startup command is issued after the agent launch.
func TestLaunch_StartupCommandSent(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%30"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	m := New(nil).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	m.cfg = &config.Config{StartupCommand: "echo hi"}

	spec := launchSpec{
		tool:        "claude",
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
		resume:      false,
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// Collect all send-keys -l literals.
	var literals []string
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			literals = append(literals, c.Args[4])
		}
	}
	// At least two send-keys -l calls: one for the agent cmd, one for startup_command.
	foundStartup := false
	for _, lit := range literals {
		if lit == "echo hi" {
			foundStartup = true
		}
	}
	if !foundStartup {
		t.Errorf("startup_command send-keys not found in calls: literals=%v", literals)
	}
}

// TestLaunch_StartupCommandNotSentWhenEmpty verifies that when StartupCommand
// is empty, no extra send-keys is issued beyond the agent launch.
func TestLaunch_StartupCommandNotSentWhenEmpty(t *testing.T) {
	r := proc.NewFakeRunner()
	baseDir := t.TempDir()

	sess := expectedSession()
	win := expectedWindow("feat")
	paneID := "%31"

	registerLaunchCalls(r, sess, win, "/proj/myrepo", paneID)
	ok := proc.FakeResult{}
	r.Default = &ok
	r.Respond(proc.FakeResult{Stdout: []byte("12345\n")},
		"tmux", "display-message", "-p", "#{start_time}")

	m := New(nil).WithLoader(loader{
		Tmux:    fakeTmuxInside(r),
		BaseDir: baseDir,
		Now:     1000,
	})
	m.cfg = &config.Config{} // StartupCommand is ""

	spec := launchSpec{
		tool:        "claude",
		branch:      "feat",
		treePath:    "/proj/myrepo",
		projectPath: "/proj/myrepo",
		resume:      false,
	}
	msg := m.launchCmd(spec)()
	lm, ok2 := msg.(launchedMsg)
	if !ok2 {
		t.Fatalf("want launchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("launchedMsg error: %v", lm.err)
	}

	// Count send-keys -l calls: only 1 is expected (the agent launch command).
	var count int
	for _, c := range r.Calls {
		if c.Name == "tmux" && len(c.Args) >= 5 && c.Args[0] == "send-keys" && c.Args[3] == "-l" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 send-keys -l call (agent cmd), got %d", count)
	}
}
