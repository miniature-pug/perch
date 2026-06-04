// internal/agent/claude_monitor_test.go
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
	"github.com/Miniature-Pug/perch/internal/pty"
)

// newMonitorWithTestListener creates a ClaudeMonitor backed by a real in-process
// hooklistener. Caller defers cleanup().
func newMonitorWithTestListener(t *testing.T) (*agent.ClaudeMonitor, *hooklistener.Listener, func()) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("listener: %v", err) }
	m := agent.NewClaudeMonitorWithListener(agent.NewClaude(), l)
	return m, l, func() { _ = l.Close() }
}

func TestClaudeMonitorPrepare(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo")
	_ = os.MkdirAll(worktree, 0o755)

	// Pre-seed a foreign Stop hook so we can assert it survives.
	claudeDir := filepath.Join(worktree, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	foreign := `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"foreign-tool notify"}]}]}}`
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(foreign), 0o644)

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "", "")
	if err != nil { t.Fatalf("Prepare: %v", err) }
	if !strings.HasPrefix(cmd, "claude") { t.Errorf("unexpected cmd: %q", cmd) }

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s map[string]any
	_ = json.Unmarshal(data, &s)
	hooks, _ := s["hooks"].(map[string]any)
	for _, ev := range []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"} {
		arr, _ := hooks[ev].([]any)
		if len(arr) == 0 { t.Errorf("hooks[%q] missing after Prepare", ev) }
	}
	// Foreign Stop hook must still be present.
	stopArr, _ := hooks["Stop"].([]any)
	found := false
	for _, item := range stopArr {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") { found = true }
		}
	}
	if !found { t.Error("foreign Stop hook was removed by Prepare") }

	// Teardown must remove perch hooks but leave foreign ones.
	_ = m.Teardown()
	data2, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s2 map[string]any
	_ = json.Unmarshal(data2, &s2)
	hooks2, _ := s2["hooks"].(map[string]any)
	stopArr2, _ := hooks2["Stop"].([]any)
	foreignStillThere := false
	for _, item := range stopArr2 {
		g, _ := item.(map[string]any)
		hs, _ := g["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if strings.Contains(fmt.Sprint(hm["command"]), "foreign-tool") { foreignStillThere = true }
			if strings.Contains(fmt.Sprint(hm["command"]), "perch-monitor-hook") {
				t.Error("perch hook survived Teardown")
			}
		}
	}
	if !foreignStillThere { t.Error("foreign Stop hook missing after Teardown") }
}

func TestClaudeMonitorSettingsFileMode(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo-mode")
	if err := os.MkdirAll(worktree, 0o755); err != nil { t.Fatal(err) }
	// No pre-existing .claude/settings.json → Prepare creates it fresh.
	if _, err := m.Prepare(context.Background(), "wsM", worktree, "", ""); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	fi, err := os.Stat(filepath.Join(worktree, ".claude", "settings.json"))
	if err != nil { t.Fatalf("stat: %v", err) }
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json mode = %o, want 600 (carries bearer token)", perm)
	}
}

// TestClaudeMonitorSettingsFileModePreExisting asserts that Prepare forces the
// settings.json to 0600 even when a pre-existing file is world-readable (0644).
// The Bearer token is the sole defence against other local users; it must not
// be leaked via a permissive file mode, regardless of what mode the file had
// before Prepare ran.
func TestClaudeMonitorSettingsFileModePreExisting(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	worktree := filepath.Join(os.Getenv("HOME"), "repo-mode-preexisting")
	claudeDir := filepath.Join(worktree, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Create a settings.json at 0644, then chmod explicitly to defeat umask.
	if err := os.WriteFile(settingsPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(settingsPath, 0o644); err != nil {
		t.Fatal(err)
	}
	// Verify the pre-condition: file really is 0644 before Prepare.
	fi, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("pre-condition stat: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("pre-condition: expected 0644, got %o", fi.Mode().Perm())
	}

	// Run Prepare — must force the file down to 0600.
	if _, err := m.Prepare(context.Background(), "wsMPE", worktree, "", ""); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	fi2, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("post-Prepare stat: %v", err)
	}
	if perm := fi2.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json mode = %o, want 600 (pre-existing 0644 must be forced down; carries bearer token)", perm)
	}
}

func TestClaudeMonitorTranscriptTail(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	abs, _ := filepath.Abs(filepath.Join("testdata", "claude", "transcript-usage.jsonl"))
	m.TailTranscript(ctx, abs)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-m.Events():
			if ev.Kind == "usage" {
				if ev.Tokens != 15 { t.Errorf("want tokens=15 (10+5), got %d", ev.Tokens) }
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for usage event")
		}
	}
}

// TestClaudeMonitorTranscriptTail_FollowsAppends verifies the tail actually
// follows the growing transcript (tail -f). SessionStart fires when the file is
// near-empty, so a one-shot read-to-EOF would miss every token produced during
// the session. This appends a record AFTER tailing starts and asserts its usage
// reaches the meter.
func TestClaudeMonitorTranscriptTail_FollowsAppends(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path := filepath.Join(t.TempDir(), "live.jsonl")
	rec1 := `{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n"
	if err := os.WriteFile(path, []byte(rec1), 0o600); err != nil {
		t.Fatal(err)
	}
	m.TailTranscript(ctx, path)

	awaitTokens := func(want int) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-m.Events():
				if ev.Kind == "usage" && ev.Tokens == want {
					return
				}
			case <-deadline:
				t.Fatalf("timeout waiting for usage event tokens=%d", want)
			}
		}
	}
	awaitTokens(15) // initial record read

	// Append a second assistant turn after the tail has reached EOF.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := `{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":100,"output_tokens":20}}}` + "\n"
	if _, err := f.WriteString(rec2); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	awaitTokens(120) // appended record must be followed and emitted
}

// TestClaudeMonitorSessionStart_WiresTailTranscript verifies H-1: when a
// SessionStart hook event carries a TranscriptPath, the monitor automatically
// starts tailing that transcript and emits a usage event for assistant records
// with token counts — without the caller ever calling TailTranscript directly.
//
// RED (before fix): the SessionStart case in translateAndEmit ignores
// TranscriptPath, so no usage event ever flows.
// GREEN (after fix): translateAndEmit calls m.TailTranscript(ctx, path) on the
// first SessionStart with a non-empty TranscriptPath.
func TestClaudeMonitorSessionStart_WiresTailTranscript(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Build the absolute path to the testdata transcript (10+5=15 tokens).
	abs, err := filepath.Abs(filepath.Join("testdata", "claude", "transcript-usage.jsonl"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	// Fire a SessionStart hook event with the transcript path, just as claude does.
	post := func(payload string) {
		req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	post(`{"hook_event_name":"SessionStart","session_id":"sid-wired","transcript_path":"` + abs + `","cwd":"/p"}`)

	// Wait for: first a state:running event, then a usage event with tokens=15.
	deadline := time.After(5 * time.Second)
	sawRunning := false
	for {
		select {
		case ev := <-m.Events():
			if ev.Kind == "state" && ev.State == agent.StateRunning {
				sawRunning = true
			}
			if ev.Kind == "usage" {
				if !sawRunning {
					t.Error("usage event arrived before state:running event")
				}
				if ev.Tokens != 15 {
					t.Errorf("want tokens=15 (10+5), got %d", ev.Tokens)
				}
				return // success
			}
		case <-deadline:
			t.Fatal("timeout waiting for usage event from auto-wired TailTranscript")
		}
	}
}

func TestClaudeMonitorEventTranslation(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	post := func(payload string) {
		req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil { _ = resp.Body.Close() }
	}
	post(`{"hook_event_name":"SessionStart","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)
	post(`{"hook_event_name":"Stop","session_id":"sid-A","transcript_path":"/t.jsonl","cwd":"/p"}`)

	deadline := time.After(3 * time.Second)
	var got []agent.Event
	for len(got) < 2 {
		select {
		case ev := <-m.Events(): got = append(got, ev)
		case <-deadline: t.Fatalf("timeout after %d events", len(got))
		}
	}
	if got[0].Kind != "state" || got[0].State != agent.StateRunning { t.Errorf("ev[0]: %+v", got[0]) }
	if got[1].Kind != "state" || got[1].State != agent.StateDone { t.Errorf("ev[1]: %+v", got[1]) }

	// State tracking: after the Stop event drained, CurrentState reflects done.
	// (translateAndEmit sets m.state BEFORE the channel send, so this is race-free.)
	if m.CurrentState() != agent.StateDone {
		t.Errorf("CurrentState after Stop = %q, want %q", m.CurrentState(), agent.StateDone)
	}
}

// TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell is the falsifying guard
// for the core agent-launch loop. The string Prepare() returns is written
// VERBATIM into the pane's pty (app.OpenWorkspace → pty.Bridge.Write, a raw
// passthrough), and a shell only runs a line once it is terminated by a
// newline. Earlier code returned the launch command without a trailing "\n",
// so the agent never started — a bug invisible to every mock-bounded test
// because they assert the returned string, not that a shell executes it.
//
// This test exercises Prepare()'s REAL output through a REAL /bin/sh: a fake
// `claude` on PATH prints a sentinel, and we assert the command actually runs.
// It regresses the instant the submitting newline is dropped from Prepare().
func TestClaudeMonitorPrepare_LaunchCommandSubmitsToShell(t *testing.T) {
	m, _, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	// Fake `claude` on PATH that prints a sentinel. Prepare()'s command name is
	// the literal "claude" (Adapter.Name()), so a real shell resolving and
	// running it via PATH is exactly the production path minus the real binary.
	binDir := t.TempDir()
	const sentinel = "PERCH_SUBMIT_OK"
	script := "#!/bin/sh\nprintf '" + sentinel + "\\n'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cwd := t.TempDir()
	cmd, err := m.Prepare(context.Background(), "ws-submit", cwd, "", "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(cmd, "\n") {
		t.Fatalf("Prepare() command %q lacks the trailing newline that submits it to the shell", cmd)
	}

	var mu sync.Mutex
	var out []byte
	emit := func(event string, data ...any) {
		if event != "data" || len(data) != 1 {
			return
		}
		if chunk, ok := data[0].([]int); ok {
			mu.Lock()
			for _, v := range chunk {
				out = append(out, byte(v))
			}
			mu.Unlock()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	br, err := pty.Spawn(ctx, cwd, []string{"/bin/sh"}, "data", "exit", emit, 80, 24)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = br.Close() }()

	if _, err := br.Write([]byte(cmd)); err != nil {
		t.Fatalf("Write launch command: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := strings.Contains(string(out), sentinel)
		mu.Unlock()
		if seen {
			return // command executed: the launch loop's real contract holds
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := string(out)
	mu.Unlock()
	t.Errorf("launch command never executed in the shell: sentinel %q absent from pty output %q "+
		"(command written but not submitted — missing trailing newline?)", sentinel, got)
}
