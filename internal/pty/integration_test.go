//go:build integration

package pty

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// newTestServer returns a Tmux on a PRIVATE socket, killed on cleanup so the
// user's default tmux server is never touched.
func newTestServer(t *testing.T) tmux.Tmux {
	t.Helper()
	socket := fmt.Sprintf("perch-pty-test-%d", os.Getpid())
	tmx := tmux.Tmux{Runner: proc.ExecRunner{}, Bin: "tmux", Socket: socket}
	t.Cleanup(func() {
		_ = tmx.KillServer(context.Background())
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
		}
		_ = os.Remove(filepath.Join(dir, socket))
	})
	return tmx
}

// fakeAgentCmd is the command run inside the test session in place of a real
// claude/opencode binary, so no real $HOME/auth is ever touched. It echoes a
// sentinel, then idles forever so the pane stays alive for the attach.
const fakeAgentCmd = "printf 'PERCH_FAKE_READY\\n'; while :; do sleep 1; done"

func TestIntegration_Spawn_BytesFlow(t *testing.T) {
	tmx := newTestServer(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Create the agent session running the FAKE agent (not claude/opencode).
	if _, err := tmx.Launch(ctx, "agent", "win", dir, []string{"sh", "-c", fakeAgentCmd}); err != nil {
		t.Fatalf("Launch fake agent: %v", err)
	}

	var mu sync.Mutex
	var sb strings.Builder
	emit := func(_ string, data ...any) {
		if len(data) == 1 {
			if b, ok := data[0].([]int); ok {
				mu.Lock()
				for _, v := range b {
					sb.WriteByte(byte(v))
				}
				mu.Unlock()
			}
		}
	}

	br, err := Spawn(ctx, tmx, "agent", "pty-data:t1", emit)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer br.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := strings.Contains(sb.String(), "PERCH_FAKE_READY")
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	out := sb.String()
	mu.Unlock()
	if !strings.Contains(out, "PERCH_FAKE_READY") {
		t.Fatalf("expected fake-agent output to flow to emit; got %q", out)
	}

	if err := br.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
}
