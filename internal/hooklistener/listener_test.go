// internal/hooklistener/listener_test.go
package hooklistener_test

import (
	"fmt"
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
