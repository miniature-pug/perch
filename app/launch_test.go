package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/agent"
	fspkg "github.com/miniature-pug/perch/internal/fs"
	internalpty "github.com/miniature-pug/perch/internal/pty"
	"github.com/miniature-pug/perch/internal/registry"
)

type launchEmit struct {
	event string
	data  any
}

// launchHarness is an App wired with fake monitors and capturing seams.
type launchHarness struct {
	a  *App
	fm *agent.FakeMonitor
	wt string

	mu      sync.Mutex
	written []byte
	emitted []launchEmit
	envs    [][]string // env of every spawnPty call
	// block, when non-nil, makes the test bridge's write wait for it.
	block chan struct{}
	// shell, when set, replaces the test bridge with a real pty running it.
	shell []string
}

func (h *launchHarness) writes() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.written)
}

func (h *launchHarness) notifies() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]any
	for _, e := range h.emitted {
		if m, ok := e.data.(map[string]any); ok && e.event == "notify" {
			out = append(out, m)
		}
	}
	return out
}

func (h *launchHarness) waitWrites(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if h.writes() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("written %q, want %q", h.writes(), want)
}

func newLaunchHarness(t *testing.T) *launchHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, _ := registry.Load(t.TempDir())
	h := &launchHarness{wt: t.TempDir()}
	_ = store.Upsert(registry.Workspace{ID: "ws-l", WorktreePath: h.wt, Agent: "claude", Title: "t"})
	h.fm = agent.NewFakeMonitor(nil)
	h.fm.SetLaunchCmd("claude --resume abc\n")
	h.a = &App{
		store:    store,
		roots:    []string{h.wt},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		emit: func(event string, data ...any) {
			var d any
			if len(data) > 0 {
				d = data[0]
			}
			h.mu.Lock()
			h.emitted = append(h.emitted, launchEmit{event: event, data: d})
			h.mu.Unlock()
		},
		spawnPty: func(ctx context.Context, cwd string, _ []string, env []string, dataEvent, exitEvent string,
			ef internalpty.EmitFunc, cols, rows uint16) (*internalpty.Bridge, error) {
			h.mu.Lock()
			h.envs = append(h.envs, env)
			shell, block := h.shell, h.block
			h.mu.Unlock()
			var b *internalpty.Bridge
			if shell != nil {
				var err error
				if b, err = internalpty.Spawn(ctx, cwd, shell, env, dataEvent, exitEvent, ef, cols, rows); err != nil {
					return nil, err
				}
			} else {
				b = internalpty.NewBridgeForTest(func() error { return nil })
			}
			b.OverrideWriteForTest(func(p []byte) (int, error) {
				if block != nil {
					<-block
				}
				h.mu.Lock()
				h.written = append(h.written, p...)
				h.mu.Unlock()
				return len(p), nil
			})
			return b, nil
		},
		newMonitor: func(string, agent.Adapter) (agent.Monitor, error) { return h.fm, nil },
		newAdapter: fakeAdapterSeam(&fakeAdapter{name: "claude", detect: true}),
	}
	t.Cleanup(func() { h.a.shutdown(context.Background()) })
	return h
}

func TestWithTerm(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"unset", []string{"A=1"}, []string{"A=1", "TERM=xterm-256color", "COLORTERM=truecolor"}},
		{"empty", []string{"TERM=", "A=1"}, []string{"TERM=xterm-256color", "A=1", "COLORTERM=truecolor"}},
		{"dumb", []string{"TERM=dumb"}, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{"dumb keeps COLORTERM", []string{"TERM=dumb", "COLORTERM=24bit"}, []string{"TERM=xterm-256color", "COLORTERM=24bit"}},
		{"set", []string{"TERM=screen-256color"}, []string{"TERM=screen-256color"}},
		{"unusual but set", []string{"TERM=xterm-kitty", "A=1"}, []string{"TERM=xterm-kitty", "A=1"}},
	} {
		if got := withTerm(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("%s: withTerm(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func lastEnv(h *launchHarness, key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.envs) == 0 {
		return "", false
	}
	env := h.envs[len(h.envs)-1]
	if env == nil {
		return "<inherited>", true
	}
	v := ""
	found := false
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v, found = val, true
		}
	}
	return v, found
}

func TestPaneEnvDefaultsTerm(t *testing.T) {
	for _, tc := range []struct{ name, term, want string }{
		{"unset", "", "xterm-256color"},
		{"dumb", "dumb", "xterm-256color"},
		{"kept", "screen-256color", "screen-256color"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLaunchHarness(t)
			t.Setenv("TERM", tc.term)
			if err := h.a.OpenWorkspace("ws-l"); err != nil {
				t.Fatal(err)
			}
			if got, _ := lastEnv(h, "TERM"); got != tc.want {
				t.Errorf("agent pane TERM = %q, want %q", got, tc.want)
			}
			if err := h.a.OpenShell("shell-ws-l", h.wt); err != nil {
				t.Fatal(err)
			}
			if got, _ := lastEnv(h, "TERM"); got != tc.want {
				t.Errorf("drawer shell TERM = %q, want %q", got, tc.want)
			}
			if err := h.a.OpenShell(homeShellPaneID, ""); err != nil {
				t.Fatal(err)
			}
			got, _ := lastEnv(h, "TERM")
			if tc.term == "screen-256color" {
				if got != "<inherited>" {
					t.Errorf("home shell with a usable TERM should inherit the environment, got TERM=%q", got)
				}
			} else if got != tc.want {
				t.Errorf("home shell TERM = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenWorkspace_TypesLaunchAfterReady(t *testing.T) {
	h := newLaunchHarness(t)
	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	h.waitWrites(t, "\x15claude --resume abc\n")
	if n := h.notifies(); len(n) != 0 {
		t.Errorf("unexpected notifications: %v", n)
	}
}

// The launch write happens off OpenWorkspace's goroutine: a blocked write
// must not hold the caller.
func TestOpenWorkspace_DoesNotWaitForLaunchWrite(t *testing.T) {
	h := newLaunchHarness(t)
	h.block = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.a.OpenWorkspace("ws-l") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OpenWorkspace blocked on the launch write")
	}
	close(h.block)
	h.waitWrites(t, "\x15claude --resume abc\n")
}

// A busy shell (here a foreground child of sh, which holds the tty) gets no
// launch line and the user a blocking notification offering Retype launch.
func TestTypeLaunchWhenReady_BusyNotifiesAndDoesNotType(t *testing.T) {
	h := newLaunchHarness(t)
	h.shell = []string{"sh", "-c", "sleep 3600; true"}
	prev := launchReadyMaxWait
	launchReadyMaxWait = 400 * time.Millisecond
	t.Cleanup(func() { launchReadyMaxWait = prev })
	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(h.notifies()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	n := h.notifies()
	if len(n) != 1 {
		t.Fatalf("notifications = %v, want one", n)
	}
	if n[0]["tier"] != "blocking" || n[0]["title"] != "Agent didn't start" || n[0]["workspaceId"] != "ws-l" ||
		n[0]["action"] != "retype-launch" || !strings.Contains(n[0]["body"].(string), "Retype launch") {
		t.Errorf("notification = %v", n[0])
	}
	if w := h.writes(); w != "" {
		t.Errorf("a busy shell was typed into: %q", w)
	}
	// Retype launch types it anyway, after retypeMaxWait.
	if err := h.a.RetypeLaunch("ws-l"); err != nil {
		t.Fatal(err)
	}
	h.waitWrites(t, "\x15claude --resume abc\n")
}

func TestTypeLaunchWhenReady_NotTypedWhenBridgeReplaced(t *testing.T) {
	h := newLaunchHarness(t)
	h.block = make(chan struct{}) // the first open's write must not happen
	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	old := h.a.currentLaunch("ws-l")
	if old == nil {
		t.Fatal("no launch state after OpenWorkspace")
	}
	// A reopen registered a new bridge; the old open's context is not
	// cancelled yet (the window typeLaunchWhenReady guards against).
	other := internalpty.NewBridgeForTest(func() error { return nil })
	h.a.mu.Lock()
	h.a.bridges[paneIDFor("ws-l")] = other
	h.a.mu.Unlock()
	// Reset what the first goroutine may already have queued.
	close(h.block)
	time.Sleep(50 * time.Millisecond)
	h.mu.Lock()
	h.written = nil
	h.mu.Unlock()

	h.a.typeLaunchWhenReady(old, "ws-l", time.Second)
	if w := h.writes(); w != "" {
		t.Errorf("launch typed into a replaced bridge: %q", w)
	}
	if n := h.notifies(); len(n) != 0 {
		t.Errorf("unexpected notifications: %v", n)
	}
}

func TestTypeLaunchWhenReady_CancelledOpenIsSilent(t *testing.T) {
	h := newLaunchHarness(t)
	h.shell = []string{"sh", "-c", "sleep 3600; true"}
	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	l := h.a.currentLaunch("ws-l")
	done := make(chan struct{})
	go func() {
		h.a.typeLaunchWhenReady(l, "ws-l", time.Minute)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	if err := h.a.CloseWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("typeLaunchWhenReady outlived its workspace")
	}
	if n := h.notifies(); len(n) != 0 || h.writes() != "" {
		t.Errorf("a closed workspace got notifications %v or writes %q", n, h.writes())
	}
}

func TestRetypeLaunch_Refusals(t *testing.T) {
	h := newLaunchHarness(t)
	if err := h.a.RetypeLaunch("bad:id"); err == nil {
		t.Error("an invalid id was accepted")
	}
	if err := h.a.RetypeLaunch("ws-l"); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Errorf("unopened session: err = %v", err)
	}

	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	h.waitWrites(t, "\x15claude --resume abc\n")

	// The agent reports in: its first event closes the window.
	h.fm.Replay(agent.Event{WorkspaceID: "ws-l", Kind: "state", State: agent.StateRunning})
	deadline := time.Now().Add(3 * time.Second)
	for h.a.currentLaunch("ws-l") != nil && !h.a.currentLaunch("ws-l").hasStarted() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.a.RetypeLaunch("ws-l"); err == nil || !strings.Contains(err.Error(), "already started") {
		t.Errorf("after the agent started: err = %v", err)
	}

	if err := h.a.CloseWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	if err := h.a.RetypeLaunch("ws-l"); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Errorf("closed session: err = %v", err)
	}
	h.mu.Lock()
	n := len(h.written)
	h.mu.Unlock()
	if n != len("\x15claude --resume abc\n") {
		t.Errorf("a refused retype wrote to the pty: %q", h.writes())
	}
}

func TestRetypeLaunch_TypesAgainBeforeTheAgentStarts(t *testing.T) {
	h := newLaunchHarness(t)
	if err := h.a.OpenWorkspace("ws-l"); err != nil {
		t.Fatal(err)
	}
	line := "\x15claude --resume abc\n"
	h.waitWrites(t, line)
	if err := h.a.RetypeLaunch("ws-l"); err != nil {
		t.Fatal(err)
	}
	h.waitWrites(t, line+line)
}

// The Terminal's first ResizePty must find the pane even while the fs
// watcher's initial walk is still running.
func TestOpenWorkspace_PaneRegisteredBeforeWatcherWalk(t *testing.T) {
	h := newLaunchHarness(t)
	release := make(chan struct{})
	inWalk := make(chan struct{})
	h.a.newWatcher = func(root string, onChange func(string)) (*fspkg.Watcher, error) {
		close(inWalk)
		<-release
		return fspkg.Watch(t.TempDir(), func(string) {})
	}
	done := make(chan error, 1)
	go func() { done <- h.a.OpenWorkspace("ws-l") }()
	select {
	case <-inWalk:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher never started")
	}
	if err := h.a.ResizePty(paneIDFor("ws-l"), 100, 30); err != nil {
		t.Errorf("ResizePty during the watcher walk: %v", err)
	}
	// The launch does not wait for the walk either.
	h.waitWrites(t, "\x15claude --resume abc\n")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Closing the workspace while the watcher walk runs must still close the
// watcher the walk produces: a live watcher reports a new file, a closed
// one does not.
func TestOpenWorkspace_WatcherClosedWhenPaneClosedDuringWalk(t *testing.T) {
	for _, cancelDuringWalk := range []bool{false, true} {
		h := newLaunchHarness(t)
		release := make(chan struct{})
		inWalk := make(chan struct{})
		dir := t.TempDir()
		var hits atomic.Int32
		h.a.newWatcher = func(string, func(string)) (*fspkg.Watcher, error) {
			close(inWalk)
			<-release
			return fspkg.Watch(dir, func(string) { hits.Add(1) })
		}
		done := make(chan error, 1)
		go func() { done <- h.a.OpenWorkspace("ws-l") }()
		<-inWalk
		if cancelDuringWalk {
			h.a.mu.Lock()
			cancel := h.a.cancels["ws-l"]
			h.a.mu.Unlock()
			if cancel == nil {
				t.Fatal("no canceller registered before the walk finished")
			}
			cancel()
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if got := hits.Load() > 0; got == cancelDuringWalk {
			t.Errorf("cancelDuringWalk=%v: watcher reported a change = %v", cancelDuringWalk, got)
		}
	}
}
