package hooklistener_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/hooklistener"
)

type hookResult struct {
	body string
	code int
}

// postAsync POSTs payload to l's /hook in a goroutine, bound to ctx.
func postAsync(ctx context.Context, l *hooklistener.Listener, payload string) <-chan hookResult {
	ch := make(chan hookResult, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- hookResult{code: -1}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		ch <- hookResult{body: strings.TrimSpace(string(b)), code: resp.StatusCode}
	}()
	return ch
}

func newListener(t *testing.T) *hooklistener.Listener {
	t.Helper()
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func nextEvent(t *testing.T, l *hooklistener.Listener) hooklistener.HookEvent {
	t.Helper()
	select {
	case ev := <-l.Events():
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for hook event")
	}
	return hooklistener.HookEvent{}
}

// TestPreToolUse_IsNonBlockingNoDecision checks that a PreToolUse (perch only
// uses it as the AskUserQuestion signal) returns at once with an empty body,
// i.e. no permission decision, so it never overrides claude's own
// permission flow (AGT-8, AGT-23).
func TestPreToolUse_IsNonBlockingNoDecision(t *testing.T) {
	l := newListener(t)
	res := postAsync(context.Background(), l,
		`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{}}`)
	select {
	case r := <-res:
		if r.code != http.StatusOK || r.body != "" {
			t.Errorf("PreToolUse: got %d %q, want 200 with empty body", r.code, r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PreToolUse blocked")
	}
	if ev := nextEvent(t, l); ev.Type != "PreToolUse" || ev.ToolName != "AskUserQuestion" {
		t.Errorf("event = %+v", ev)
	}
}

// TestPermissionRequest_AbstainReturnsNoDecision checks the no-decision
// verdict: an empty 200 body, so claude shows its own dialog.
func TestPermissionRequest_AbstainReturnsNoDecision(t *testing.T) {
	l := newListener(t)
	res := postAsync(context.Background(), l,
		`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{}}`)
	ev := nextEvent(t, l)
	if !l.Decide(ev.ReqID, hooklistener.Decision{Abstain: true}) {
		t.Fatal("Decide on a parked request returned false")
	}
	r := <-res
	if r.code != http.StatusOK || r.body != "" {
		t.Errorf("abstain: got %d %q, want 200 with empty body", r.code, r.body)
	}
}

// TestPermissionRequest_DecisionSchema pins the documented PermissionRequest
// output: hookSpecificOutput.hookEventName + decision.behavior.
func TestPermissionRequest_DecisionSchema(t *testing.T) {
	l := newListener(t)
	res := postAsync(context.Background(), l,
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	ev := nextEvent(t, l)
	l.Decide(ev.ReqID, hooklistener.Decision{Allow: false})
	r := <-res
	for _, want := range []string{`"hookEventName":"PermissionRequest"`, `"decision":{`, `"behavior":"deny"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("body %q missing %s", r.body, want)
		}
	}
}

// TestPermissionRequest_CancelEmitsRetraction is the AGT-16/APP-16
// regression: when the parked hook's client goes away without a verdict,
// the listener must emit EventRequestCancelled with the same ReqID, and a
// later Decide must report false.
func TestPermissionRequest_CancelEmitsRetraction(t *testing.T) {
	l := newListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	res := postAsync(ctx, l, `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{}}`)
	ev := nextEvent(t, l)
	cancel()
	<-res
	got := nextEvent(t, l)
	if got.Type != hooklistener.EventRequestCancelled || got.ReqID != ev.ReqID {
		t.Fatalf("want cancellation for %s, got %+v", ev.ReqID, got)
	}
	if l.Decide(ev.ReqID, hooklistener.Decision{Allow: true}) {
		t.Error("Decide on a cancelled request returned true")
	}
}

// TestDecideUnknownReqID checks Decide's bool result for an unknown id.
func TestDecideUnknownReqID(t *testing.T) {
	l := newListener(t)
	if l.Decide("nope", hooklistener.Decision{Allow: true}) {
		t.Error("Decide(unknown) = true, want false")
	}
}

// TestCloseIdempotent checks that Close can be called twice.
func TestCloseIdempotent(t *testing.T) {
	l, err := hooklistener.New()
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	_ = l.Close()
}
