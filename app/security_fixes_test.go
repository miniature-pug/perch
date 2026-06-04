// security_fixes_test.go — RED/GREEN TDD tests for security fixes:
// M-12, M-13, L-5, L-8, L-10, L-11, L-12.
//
// Each test is written to FAIL before the corresponding fix and PASS after.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	internalpty "github.com/Miniature-Pug/perch/internal/pty"
	"github.com/Miniature-Pug/perch/internal/registry"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newSecurityTestApp builds a minimal App wired for security tests.
func newSecurityTestApp(t *testing.T, roots []string) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	return &App{
		store:        store,
		roots:        roots,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{},
		pending:      map[string]agent.ApprovalReq{},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
	}
}

// hashInput computes the sha256 hex of an input string (same as production).
func hashInput(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// L-8: CopyPath must reject paths outside roots.
// ---------------------------------------------------------------------------

func TestSecFix_L8_CopyPath_RejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "secret.txt")

	a := newSecurityTestApp(t, []string{root})

	// Before fix: CopyPath has no root check, so it returns nil.
	// After fix: returns a validation error.
	err := a.CopyPath(outsidePath)
	if err == nil {
		t.Fatal("CopyPath with path outside roots must return an error")
	}
}

func TestSecFix_L8_CopyPath_AllowsInsideRoots(t *testing.T) {
	root := t.TempDir()
	insidePath := filepath.Join(root, "file.txt")
	// Create the file so validateWorktreeUnderRoots (which uses EvalSymlinks) can resolve it.
	if err := os.WriteFile(insidePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set ctx to nil — CopyPath returns nil early if ctx is nil (Wails not started).
	// The validation must occur BEFORE the ctx guard so inside-root also returns
	// nil (as a no-op write to clipboard) but does NOT return a validation error.
	a := newSecurityTestApp(t, []string{root})
	a.ctx = nil // headless, no Wails runtime

	err := a.CopyPath(insidePath)
	// Inside root with nil ctx: should return nil (clipboard no-op, not an error).
	if err != nil {
		t.Fatalf("CopyPath with path inside roots returned unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// L-10/L-20: SessionID from event pump must be validated before persistence.
// ---------------------------------------------------------------------------

func TestSecFix_L10_ShellMetacharSessionID_NotPersisted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	const workspaceID = "ws-l10"
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: workspaceID, WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:        store,
		roots:        []string{wt},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{workspaceID: fm},
		pending:      map[string]agent.ApprovalReq{},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
		spawnPty: func(_ context.Context, _ string, _ []string, _, _ string,
			_ internalpty.EmitFunc, _, _ uint16) (*internalpty.Bridge, error) {
			return internalpty.NewBridgeForTest(func() error { return nil }), nil
		},
		newMonitor: func(_ string, _ agent.Adapter) (agent.Monitor, error) {
			return fm, nil
		},
	}

	if err := a.OpenWorkspace(workspaceID); err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	// Replay an event with a shell-metachar session ID.
	maliciousID := "abc; rm -rf /"
	fm.Replay(agent.Event{Kind: "state", State: agent.StateRunning, SessionID: maliciousID})

	// Wait for the event to be processed.
	time.Sleep(100 * time.Millisecond)

	// The malicious ID must NOT be persisted.
	w, ok := store.Get(workspaceID)
	if !ok {
		t.Fatal("workspace disappeared from registry")
	}
	if w.LastSessionID == maliciousID {
		t.Fatalf("malicious SessionID %q was persisted to registry; must be dropped", maliciousID)
	}
}

// ---------------------------------------------------------------------------
// L-11: OpenWorkspace, CloseWorkspace, RemoveWorkspace must validate the id.
// ---------------------------------------------------------------------------

func TestSecFix_L11_OpenWorkspace_RejectsMalformedID(t *testing.T) {
	a := newSecurityTestApp(t, []string{t.TempDir()})
	err := a.OpenWorkspace("bad;id")
	if err == nil {
		t.Fatal("OpenWorkspace with malformed id must return an error")
	}
	// Must be the session-id validation error, not "unknown workspace".
	if !strings.Contains(err.Error(), "session id") && !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected session-id validation error, got %q", err.Error())
	}
}

func TestSecFix_L11_CloseWorkspace_RejectsMalformedID(t *testing.T) {
	a := newSecurityTestApp(t, []string{t.TempDir()})
	err := a.CloseWorkspace("bad;id")
	if err == nil {
		t.Fatal("CloseWorkspace with malformed id must return an error")
	}
}

func TestSecFix_L11_RemoveWorkspace_RejectsMalformedID(t *testing.T) {
	a := newSecurityTestApp(t, []string{t.TempDir()})
	err := a.RemoveWorkspace("bad;id")
	if err == nil {
		t.Fatal("RemoveWorkspace with malformed id must return an error")
	}
}

// ---------------------------------------------------------------------------
// L-12: CloseWorkspace must clean up pending approvals for that workspace.
// ---------------------------------------------------------------------------

func TestSecFix_L12_CloseWorkspace_ClearsPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	const wsID = "ws-l12"
	wt := t.TempDir()
	_ = store.Upsert(registry.Workspace{ID: wsID, WorktreePath: wt, Agent: "claude"})

	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:        store,
		roots:        []string{wt},
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{wsID: fm},
		cancels:      map[string]context.CancelFunc{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
		// Pre-inject a pending approval belonging to ws-l12.
		pending: map[string]agent.ApprovalReq{
			"req-abc:" + wsID: {ReqID: "req-abc", Tool: "Bash", Input: "ls"},
			// An entry for a different workspace — must NOT be cleared.
			"req-xyz:other-ws": {ReqID: "req-xyz", Tool: "Read", Input: "/etc"},
		},
	}

	if err := a.CloseWorkspace(wsID); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Entry for ws-l12 must be gone.
	if _, found := a.pending["req-abc:"+wsID]; found {
		t.Error("pending entry for closed workspace was NOT cleared (L-12 bug)")
	}
	// Entry for other workspace must survive.
	if _, found := a.pending["req-xyz:other-ws"]; !found {
		t.Error("pending entry for OTHER workspace was incorrectly cleared")
	}
}

func TestSecFix_L12_Shutdown_ClearsPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)

	a := &App{
		store:    store,
		roots:    []string{t.TempDir()},
		emit:     func(string, ...any) {},
		bridges:  map[string]*internalpty.Bridge{},
		monitors: map[string]agent.Monitor{},
		cancels:  map[string]context.CancelFunc{},
		pending: map[string]agent.ApprovalReq{
			"req-1:ws1": {ReqID: "req-1", Tool: "Bash", Input: "ls"},
		},
	}

	a.shutdown(context.Background())

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pending) != 0 {
		t.Errorf("shutdown must reset pending map; got %d entries", len(a.pending))
	}
}

// ---------------------------------------------------------------------------
// L-5: WriteFile must validate absPath (not just its Dir) under roots.
// ---------------------------------------------------------------------------

func TestSecFix_L5_WriteFile_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// Attack: absPath = root/subdir/evil-link where evil-link is a symlink to outside/secret.txt.
	// Dir(absPath) = root/subdir → resolves inside root (old code passes this check).
	// absPath itself via EvalSymlinks → outside/secret.txt → outside root (must be rejected).
	// Before fix: WriteFile validated only Dir → no error.
	// After fix: detects absPath exists as a symlink → error.
	realSubdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(realSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "secret.txt")
	// Create the outside target so the symlink is non-dangling (EvalSymlinks succeeds).
	if err := os.WriteFile(outsideTarget, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	evilLink := filepath.Join(realSubdir, "evil-link.txt")
	if err := os.Symlink(outsideTarget, evilLink); err != nil {
		t.Fatal(err)
	}

	a := newSecurityTestApp(t, []string{root})

	err := a.WriteFile(evilLink, "injected content")
	if err == nil {
		t.Fatal("WriteFile targeting a symlink that resolves outside root must return an error")
	}
}

func TestSecFix_L5_WriteFile_AllowsNewFileUnderRoot(t *testing.T) {
	root := t.TempDir()
	newFile := filepath.Join(root, "new-file.txt")

	a := newSecurityTestApp(t, []string{root})

	// A new (nonexistent) file under root must be allowed.
	err := a.WriteFile(newFile, "hello")
	if err != nil {
		t.Fatalf("WriteFile for new file inside root should succeed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// M-12: Concurrent Approve(always) must persist BOTH always-rules (no lost update).
// ---------------------------------------------------------------------------

func TestSecFix_M12_ConcurrentApproveAlways_BothRulesPersisted(t *testing.T) {
	// Run many iterations because the bug is a file-backed lost-update (not a
	// data race that -race detects). Sufficient iterations reliably reproduce loss.
	const iterations = 60
	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iter%d", i), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfgDir := t.TempDir()
			store, _ := registry.Load(cfgDir)
			wt := t.TempDir()
			_ = store.Upsert(registry.Workspace{ID: "ws-m12", WorktreePath: wt, Agent: "claude"})

			fm := agent.NewFakeMonitor(nil)

			settingsPath := filepath.Join(cfgDir, "settings.json")
			a := &App{
				store:   store,
				roots:   []string{wt},
				emit:    func(string, ...any) {},
				bridges: map[string]*internalpty.Bridge{},
				monitors: map[string]agent.Monitor{"ws-m12": fm},
				pending: map[string]agent.ApprovalReq{
					"req-A:ws-m12": {ReqID: "req-A", Tool: "Bash", Input: "echo alpha"},
					"req-B:ws-m12": {ReqID: "req-B", Tool: "Read", Input: "/tmp/beta"},
				},
				settingsPath: settingsPath,
			}

			// Two goroutines, each calling Approve with always=true for distinct rules.
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = a.Approve("req-A:ws-m12", "always")
			}()
			go func() {
				defer wg.Done()
				_ = a.Approve("req-B:ws-m12", "always")
			}()
			wg.Wait()

			s, err := a.GetSettings()
			if err != nil {
				t.Fatalf("GetSettings: %v", err)
			}
			if len(s.AlwaysRules) != 2 {
				t.Errorf("iter %d: expected 2 always-rules, got %d: %+v", i, len(s.AlwaysRules), s.AlwaysRules)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M-13: Hash-based match — distinct inputs sharing a 4096-byte prefix must NOT auto-approve.
// ---------------------------------------------------------------------------

func TestSecFix_M13_TruncationCollision_DistinctHashRejects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	store, _ := registry.Load(cfgDir)
	_ = store.Upsert(registry.Workspace{ID: "ws-m13", WorktreePath: t.TempDir(), Agent: "claude"})
	fm := agent.NewFakeMonitor(nil)

	a := &App{
		store:        store,
		emit:         func(string, ...any) {},
		bridges:      map[string]*internalpty.Bridge{},
		monitors:     map[string]agent.Monitor{"ws-m13": fm},
		pending:      map[string]agent.ApprovalReq{},
		settingsPath: filepath.Join(cfgDir, "settings.json"),
	}

	// Construct two inputs that share the first 4096 bytes but differ after.
	prefix := strings.Repeat("x", agent.MaxApprovalInputLen) // exactly 4096 bytes
	fullInputA := prefix + "-alpha"
	fullInputB := prefix + "-beta"

	hashA := hashInput(fullInputA)
	hashB := hashInput(fullInputB)

	// Truncated inputs are identical (both == prefix[:4096]).
	truncA := fullInputA
	if len(truncA) > agent.MaxApprovalInputLen {
		truncA = truncA[:agent.MaxApprovalInputLen]
	}
	truncB := fullInputB
	if len(truncB) > agent.MaxApprovalInputLen {
		truncB = truncB[:agent.MaxApprovalInputLen]
	}
	// Verify the truncations are identical (the collision condition).
	if truncA != truncB {
		t.Fatal("test setup: truncated inputs must be identical to simulate collision")
	}

	// Persist a rule for input A (with hash of full A, pattern = truncated display).
	_ = a.SaveSettings(Settings{AlwaysRules: []AlwaysRule{
		{Agent: "claude", Tool: "Bash", Pattern: truncA, Hash: hashA},
	}})

	// Build approval request for input B: same truncated form, different hash.
	reqB := agent.ApprovalReq{
		ReqID:     "req-b",
		Tool:      "Bash",
		Input:     truncB, // truncated — same as A
		InputHash: hashB,  // different hash → must NOT match
	}

	// Before fix: match is on truncated Input → returns TRUE (privilege escalation).
	// After fix:  match is on hash         → returns FALSE.
	result := a.maybeAutoApprove("ws-m13", "req-b", reqB, fm)
	if result {
		t.Fatal("maybeAutoApprove returned true for input B despite hash mismatch — " +
			"truncation collision privilege escalation not fixed")
	}

	// Sanity: same hash should match.
	reqA := agent.ApprovalReq{
		ReqID:     "req-a",
		Tool:      "Bash",
		Input:     truncA,
		InputHash: hashA, // same hash → must match
	}
	if !a.maybeAutoApprove("ws-m13", "req-a", reqA, fm) {
		t.Fatal("maybeAutoApprove returned false for input A with matching hash — false rejection")
	}
}
