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
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
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

	cmd, err := m.Prepare(context.Background(), "ws1", worktree, "")
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

func TestClaudeMonitorEventTranslation(t *testing.T) {
	m, l, cleanup := newMonitorWithTestListener(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.StartTranslating(ctx)

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
	if got[1].Kind != "state" || got[1].State != agent.StateIdle { t.Errorf("ev[1]: %+v", got[1]) }

	// State tracking: after the Stop event drained, CurrentState reflects idle.
	// (translateAndEmit sets m.state BEFORE the channel send, so this is race-free.)
	if m.CurrentState() != agent.StateIdle {
		t.Errorf("CurrentState after Stop = %q, want %q", m.CurrentState(), agent.StateIdle)
	}
}
