package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

// TestNewApp_PtyDataIsBase64 is the APP-20 regression guard: the production
// spawn seam emits pty:data payloads as one base64 string per chunk, not as
// a JSON number array.
func TestNewApp_PtyDataIsBase64(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(store, []string{t.TempDir()})
	var mu sync.Mutex
	var payloads []any
	emit := func(event string, data ...any) {
		if event == "pty:data:p" && len(data) > 0 {
			mu.Lock()
			payloads = append(payloads, data[0])
			mu.Unlock()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	br, err := a.spawnPty(ctx, t.TempDir(), []string{"printf", "hi"}, nil, "pty:data:p", "pty:exit:p", emit, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) == 0 {
		t.Fatal("no pty:data emitted")
	}
	var joined []string
	for _, p := range payloads {
		s, ok := p.(string)
		if !ok {
			t.Fatalf("pty:data payload is %T, want a base64 string", p)
		}
		joined = append(joined, s)
	}
	var got []byte
	for _, s := range joined {
		var b []byte
		if err := json.Unmarshal([]byte(`"`+s+`"`), &b); err != nil {
			t.Fatalf("payload %q is not base64: %v", s, err)
		}
		got = append(got, b...)
	}
	if !strings.Contains(string(got), "hi") {
		t.Errorf("decoded pty output = %q, want it to contain hi", got)
	}
}

// TestApp_WriteToPty_DecodesBase64Arg checks the binding contract: the
// frontend sends a base64 string, which Wails decodes into the []byte
// parameter with encoding/json before WriteToPty runs.
func TestApp_WriteToPty_DecodesBase64Arg(t *testing.T) {
	var written []byte
	br := internalpty.NewBridgeForTest(func() error { return nil })
	br.OverrideWriteForTest(func(p []byte) (int, error) { written = append(written, p...); return len(p), nil })
	a := &App{bridges: map[string]*internalpty.Bridge{"pane-x": br}, monitors: map[string]agent.Monitor{}}
	var arg []byte
	if err := json.Unmarshal([]byte(`"bHMgLWxhCg=="`), &arg); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteToPty("pane-x", arg); err != nil {
		t.Fatal(err)
	}
	if string(written) != "ls -la\n" {
		t.Errorf("pty received %q, want %q", written, "ls -la\n")
	}
}
