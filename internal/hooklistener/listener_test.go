// internal/hooklistener/listener_test.go
package hooklistener_test

import (
	"context"
	"fmt"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListenerAddrAndToken(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()
	if !strings.HasPrefix(l.Addr(), "127.0.0.1:") {
		t.Errorf("want 127.0.0.1:PORT, got %q", l.Addr())
	}
	if len(l.Token()) != 64 { // 256-bit = 32 bytes = 64 hex chars
		t.Errorf("want 64 hex token, got len=%d", len(l.Token()))
	}
}

func TestListenerUnauthorized(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()
	resp, err := http.Post(fmt.Sprintf("http://%s/hook", l.Addr()),
		"application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

// TestListenerWrongTokenNoEnqueue asserts that a /hook POST carrying a WRONG
// Bearer token is rejected with 401 AND does not enqueue an event. This is the
// auth boundary: a forged hook (e.g. another local process probing the loopback
// port) must never reach the monitor's event channel. We post a fully-valid Stop
// payload so the only thing standing between it and the queue is the token check.
func TestListenerWrongTokenNoEnqueue(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()

	body := `{"hook_event_name":"Stop","session_id":"s1","transcript_path":"/t.jsonl","cwd":"/p"}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer not-the-real-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong token, got %d", resp.StatusCode)
	}

	// The rejected request must NOT have produced an event.
	select {
	case ev := <-l.Events():
		t.Fatalf("wrong-token POST must not enqueue an event; got %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// no event — correct.
	}
}

func TestStopEventArrives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()

	body := `{"hook_event_name":"Stop","session_id":"s1","transcript_path":"/t.jsonl","cwd":"/p"}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	select {
	case ev := <-l.Events():
		if ev.Type != "Stop" || ev.SessionID != "s1" || ev.TranscriptPath != "/t.jsonl" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestPreToolUse_ClientCancelDoesNotHang(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	payload := `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		close(done)
	}()

	// Wait until the approval is parked (event delivered), then cancel.
	select {
	case ev := <-l.Events():
		if ev.Type != "PreToolUse" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event never delivered (was it dropped?)")
	}
	cancel()

	select {
	case <-done:
		// handler/client unwound — no hang
	case <-time.After(2 * time.Second):
		t.Fatal("request hung after client cancel — handler not cancellable")
	}
}

// TestStopEventNotDroppedUnderBackpressure verifies that a Stop event is NOT
// silently dropped when the 64-slot event buffer is full.
//
// How it distinguishes pre-fix (drop) from post-fix (block):
//
//	Pre-fix:  non-blocking send with `default:`. When buffer is full the Stop
//	          handler returns 200 immediately, silently discarding the event.
//	Post-fix: blocking send on r.Context(). The handler blocks until the test
//	          drains a slot, then delivers the Stop event.
//
// The test uses FillEventsBuffer (an internal test helper) to fill the channel
// to capacity atomically — no HTTP races — then posts the Stop via HTTP in a
// goroutine. Draining starts after the POST goroutine is running; the drain
// frees a slot which (post-fix) unblocks the handler and delivers Stop.
func TestStopEventNotDroppedUnderBackpressure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = l.Close() }()

	// Fill the channel to capacity directly (no HTTP, no race).
	bufCap := l.EventsCap()
	l.FillEventsBuffer(bufCap)

	// POST Stop in a goroutine. Pre-fix: handler sees a full buffer, takes the
	// `default:` branch (drops event), writes 200, and the goroutine finishes.
	// Post-fix: handler blocks on the channel send until a slot is freed.
	stopBody := `{"hook_event_name":"Stop","session_id":"marker","transcript_path":"/t.jsonl","cwd":"/p"}`
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(stopBody))
		req.Header.Set("Authorization", "Bearer "+l.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()

	// Give the Stop handler time to reach the channel-send decision point. In
	// the pre-fix code it drops and returns in <1 ms; in the post-fix code it
	// blocks (stopDone stays open). 50 ms is a generous but still fast budget.
	time.Sleep(50 * time.Millisecond)

	// Drain Events() until we find Stop or exhaust the buffer. The drain frees
	// slots; post-fix that unblocks the handler so Stop is delivered. Pre-fix:
	// the handler already dropped Stop (it returned within 50 ms), so Stop never
	// appears in the channel and the loop exhausts bufCap reads without finding it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found := false
	for n := 0; n < bufCap && !found; n++ {
		select {
		case ev := <-l.Events():
			if ev.Type == "Stop" && ev.SessionID == "marker" {
				found = true
			}
		case <-ctx.Done():
			t.Fatal("Stop event never arrived — dropped under backpressure (timeout 5s)")
		}
	}
	if !found {
		// Drain any remaining events including potentially a late Stop.
		timeout := time.NewTimer(200 * time.Millisecond)
		defer timeout.Stop()
	drainRest:
		for {
			select {
			case ev := <-l.Events():
				if ev.Type == "Stop" && ev.SessionID == "marker" {
					found = true
					break drainRest
				}
			case <-timeout.C:
				break drainRest
			}
		}
	}
	if !found {
		t.Fatal("Stop event not found after draining all buffer slots — dropped under backpressure")
	}

	// Handler must have returned now that Stop was received.
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop POST goroutine never returned after event was delivered")
	}
}

func TestPreToolUseAllowDeny(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow bool
		want  string
	}{
		{"allow", true, "allow"}, {"deny", false, "deny"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			l, err := hooklistener.New()
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer func() { _ = l.Close() }()

			payload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`
			req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+l.Token())
			req.Header.Set("Content-Type", "application/json")

			type result struct {
				body string
				code int
			}
			ch := make(chan result, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					ch <- result{code: -1}
					return
				}
				defer func() { _ = resp.Body.Close() }()
				b, _ := io.ReadAll(resp.Body)
				ch <- result{body: strings.TrimSpace(string(b)), code: resp.StatusCode}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			select {
			case ev := <-l.Events():
				if ev.Type != "PreToolUse" || ev.ReqID == "" {
					t.Errorf("bad event: %+v", ev)
				}
				l.Decide(ev.ReqID, hooklistener.Decision{Allow: tc.allow})
			case <-ctx.Done():
				t.Fatal("timeout waiting for event")
			}

			select {
			case r := <-ch:
				if r.code != http.StatusOK {
					t.Errorf("want 200, got %d", r.code)
				}
				want := `"permissionDecision":"` + tc.want + `"`
				if !strings.Contains(r.body, want) {
					t.Errorf("body %q missing %q", r.body, want)
				}
			case <-ctx.Done():
				t.Fatal("timeout waiting for response")
			}
		})
	}
}
