// internal/agent/claude_monitor.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

type ClaudeMonitor struct {
	adapter  Adapter
	listener *hooklistener.Listener
	ownedLn  bool // true when we created listener; Teardown closes it
	events   chan Event
	cwd      string

	mu       sync.Mutex
	state    State
	lastTool string
}

func newClaudeMonitor(a Adapter) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, events: make(chan Event, 64)}
}
func NewClaudeMonitorWithListener(a Adapter, l *hooklistener.Listener) *ClaudeMonitor {
	return &ClaudeMonitor{adapter: a, listener: l, events: make(chan Event, 64)}
}
func (m *ClaudeMonitor) Events() <-chan Event { return m.events }
func (m *ClaudeMonitor) Capabilities() Caps  { return Caps{Approvals: true, Attention: true, Tokens: true} }

func (m *ClaudeMonitor) StartTranslating(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case he, ok := <-m.listener.Events():
				if !ok {
					return
				}
				m.translateAndEmit(he)
			}
		}
	}()
}

func (m *ClaudeMonitor) translateAndEmit(he hooklistener.HookEvent) {
	var ev Event
	switch he.Type {
	case "SessionStart":
		ev = Event{Kind: "state", State: StateRunning}
	case "Stop":
		ev = Event{Kind: "state", State: StateIdle}
	case "StopFailure":
		ev = Event{Kind: "state", State: StateErrored, Err: he.ErrorType}
	case "Notification":
		ev = Event{Kind: "state", State: StateIdle}
	case "PreToolUse":
		sum := he.ToolName
		if len(he.ToolInput) > 0 && len(he.ToolInput) < 120 {
			sum += ": " + string(he.ToolInput)
		}
		ev = Event{Kind: "approval", State: StateAwaitingApproval,
			Approval: &ApprovalReq{ReqID: he.ReqID, Tool: he.ToolName, Summary: sum}}
	default:
		return
	}
	// Track state/lastTool (mutex-guarded) BEFORE emitting, so a reader that
	// observes the event on the channel also observes the updated state.
	m.mu.Lock()
	if ev.State != "" {
		m.state = ev.State
	}
	if ev.Approval != nil && ev.Approval.Tool != "" {
		m.lastTool = ev.Approval.Tool
	}
	m.mu.Unlock()
	m.events <- ev
}

func (m *ClaudeMonitor) Approve(reqID string, d Decision) error {
	m.listener.Decide(reqID, hooklistener.Decision{Allow: d.Allow, Always: d.Always})
	return nil
}

func (m *ClaudeMonitor) CurrentState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == "" {
		return StateIdle
	}
	return m.state
}

func (m *ClaudeMonitor) LastApprovalTool() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastTool
}

const perchMonitorSentinel = "perch-monitor-hook"

var perchMonitorEvents = []string{"PreToolUse", "Stop", "StopFailure", "SessionStart"}

func (m *ClaudeMonitor) Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (string, error) {
	m.cwd = cwd
	if m.listener == nil {
		l, err := hooklistener.New()
		if err != nil {
			return "", fmt.Errorf("ClaudeMonitor.Prepare: listener: %w", err)
		}
		m.listener = l
		m.ownedLn = true
	}
	if err := m.writeHooks(cwd); err != nil {
		return "", err
	}
	var args []string
	if resumeID != "" {
		args = m.adapter.ResumeArgs(resumeID)
	} else {
		args = m.adapter.NewArgs(NewOpts{})
	}
	return strings.Join(append([]string{m.adapter.Name()}, args...), " "), nil
}

func (m *ClaudeMonitor) writeHooks(cwd string) error {
	dir := filepath.Join(cwd, ".claude")
	path := filepath.Join(dir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	merged, err := mergeMonitorHooks(existing, m.listener.Addr(), m.listener.Token())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(path, merged)
}

// mergeMonitorHooks is pure (no I/O). It uses the same additive-append+sentinel
// idiom as mergeClaudeHooks in claude.go: each perch group has a command
// containing perchMonitorSentinel so it can be identified on Teardown.
func mergeMonitorHooks(existing []byte, addr, token string) ([]byte, error) {
	var s map[string]any
	if tr := strings.TrimSpace(string(existing)); tr != "" {
		if err := json.Unmarshal(existing, &s); err != nil {
			return nil, err
		}
	}
	if s == nil {
		s = map[string]any{}
	}
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		hm = map[string]any{}
	}
	s["hooks"] = hm

	url := "http://" + addr + "/hook"
	cmd := fmt.Sprintf(`curl -sf -X POST -H "Authorization: Bearer %s" -H "Content-Type: application/json" -d @- %s # %s`,
		token, url, perchMonitorSentinel)
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		if isPerchMonitorGroupPresent(arr) {
			continue // idempotent
		}
		arr = append(arr, map[string]any{
			"matcher": "",
			"hooks":   []any{map[string]any{"type": "command", "command": cmd}},
		})
		hm[ev] = arr
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func isPerchMonitorGroupPresent(arr []any) bool {
	for _, item := range arr {
		g, _ := item.(map[string]any)
		if isPerchMonitorGroup(g) {
			return true
		}
	}
	return false
}

func isPerchMonitorGroup(g map[string]any) bool {
	hs, _ := g["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if strings.Contains(fmt.Sprint(hm["command"]), perchMonitorSentinel) {
			return true
		}
	}
	return false
}

func (m *ClaudeMonitor) Teardown() error {
	if m.cwd != "" {
		path := filepath.Join(m.cwd, ".claude", "settings.json")
		if data, err := os.ReadFile(path); err == nil {
			_ = removeMonitorHooks(path, data)
		}
	}
	if m.ownedLn && m.listener != nil {
		return m.listener.Close()
	}
	return nil
}

func removeMonitorHooks(path string, data []byte) error {
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		return nil
	}
	for _, ev := range perchMonitorEvents {
		arr, _ := hm[ev].([]any)
		var kept []any
		for _, item := range arr {
			g, _ := item.(map[string]any)
			if !isPerchMonitorGroup(g) {
				kept = append(kept, item)
			}
		}
		if len(kept) == 0 {
			delete(hm, ev)
		} else {
			hm[ev] = kept
		}
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	return atomicWrite(path, append(out, '\n'))
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	_ = os.Chmod(name, mode)
	return os.Rename(name, path)
}
