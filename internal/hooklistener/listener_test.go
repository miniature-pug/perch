// internal/hooklistener/listener_test.go
package hooklistener_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"github.com/Miniature-Pug/perch/internal/hooklistener"
)

func TestListenerAddrAndToken(t *testing.T) {
	t.Parallel()
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("New: %v", err) }
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
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()
	resp, err := http.Post(fmt.Sprintf("http://%s/hook", l.Addr()),
		"application/json", strings.NewReader(`{}`))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestStopEventArrives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := hooklistener.New()
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()

	body := `{"hook_event_name":"Stop","session_id":"s1","transcript_path":"/t.jsonl","cwd":"/p"}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatalf("POST: %v", err) }
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK { t.Fatalf("want 200, got %d", resp.StatusCode) }

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
	if err != nil { t.Fatalf("New: %v", err) }
	defer func() { _ = l.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	payload := `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+l.Token())
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil { _ = resp.Body.Close() }
		close(done)
	}()

	// Wait until the approval is parked (event delivered), then cancel.
	select {
	case ev := <-l.Events():
		if ev.Type != "PreToolUse" { t.Errorf("unexpected event: %+v", ev) }
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

func TestPreToolUseAllowDeny(t *testing.T) {
	for _, tc := range []struct{ name string; allow bool; want string }{
		{"allow", true, "allow"}, {"deny", false, "deny"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			l, err := hooklistener.New()
			if err != nil { t.Fatalf("New: %v", err) }
			defer func() { _ = l.Close() }()

			payload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`
			req, _ := http.NewRequest(http.MethodPost, "http://"+l.Addr()+"/hook", strings.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+l.Token())
			req.Header.Set("Content-Type", "application/json")

			type result struct{ body string; code int }
			ch := make(chan result, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err != nil { ch <- result{code: -1}; return }
				defer func() { _ = resp.Body.Close() }()
				b, _ := io.ReadAll(resp.Body)
				ch <- result{body: strings.TrimSpace(string(b)), code: resp.StatusCode}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			select {
			case ev := <-l.Events():
				if ev.Type != "PreToolUse" || ev.ReqID == "" { t.Errorf("bad event: %+v", ev) }
				l.Decide(ev.ReqID, hooklistener.Decision{Allow: tc.allow})
			case <-ctx.Done():
				t.Fatal("timeout waiting for event")
			}

			select {
			case r := <-ch:
				if r.code != http.StatusOK { t.Errorf("want 200, got %d", r.code) }
				want := `"permissionDecision":"` + tc.want + `"`
				if !strings.Contains(r.body, want) { t.Errorf("body %q missing %q", r.body, want) }
			case <-ctx.Done():
				t.Fatal("timeout waiting for response")
			}
		})
	}
}
