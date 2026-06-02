package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSessionID_AllowlistCharset(t *testing.T) {
	good := []string{"ses_18593fc84ffeg4oyInzAG2eLOL", "2b96f5bc-43ef-454d-a12d-791ad68da8dd", "win-1"}
	for _, s := range good {
		if err := validateSessionID(s); err != nil {
			t.Errorf("validateSessionID(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", "a b", "a;b", "../x", "a\x1fb", "a\nb", "a/b"}
	for _, s := range bad {
		if err := validateSessionID(s); err == nil {
			t.Errorf("validateSessionID(%q) = nil, want error", s)
		}
	}
}

func TestValidateSessionID_AdversarialCases(t *testing.T) {
	rejected := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"overlong", strings.Repeat("a", 129)},
		{"control NUL", "ses\x00id"},
		{"control LF", "ses\nid"},
		{"shell semicolon", "ses;id"},
		{"shell dollar-paren", "ses$(id)"},
		{"shell backtick", "ses`id`"},
		{"shell pipe", "ses|id"},
		{"path separator slash", "ses/id"},
		{"path traversal dotdot", "../etc/passwd"},
		{"unicode lookalike", "ses‐id"}, // U+2010 HYPHEN, not ASCII '-'
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSessionID(tc.input); err == nil {
				t.Errorf("validateSessionID(%q) = nil, want error", tc.input)
			}
		})
	}
	t.Run("normal allowlisted id", func(t *testing.T) {
		if err := validateSessionID("ses_abc-123"); err != nil {
			t.Errorf("validateSessionID(\"ses_abc-123\") = %v, want nil", err)
		}
	})
}

func TestValidateWorktreeUnderRoots(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "perch", "wt")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}
	// ACCEPT: real path under root, and the root itself.
	if err := validateWorktreeUnderRoots(sub, roots); err != nil {
		t.Errorf("path under root rejected: %v", err)
	}
	if err := validateWorktreeUnderRoots(root, roots); err != nil {
		t.Errorf("path at root rejected: %v", err)
	}
	// REJECT.
	outside := t.TempDir() // a real directory NOT under root → tests containment
	for _, p := range []string{
		"/etc/passwd",                         // real, outside root (containment)
		outside,                               // real, outside root (containment)
		root + "/../secret",                   // non-clean (rejected before resolve)
		"relative/path",                       // not absolute
		"",                                    // empty
		filepath.Join(root, "does-not-exist"), // in-root but missing → resolve error
	} {
		if err := validateWorktreeUnderRoots(p, roots); err == nil {
			t.Errorf("validateWorktreeUnderRoots(%q) = nil, want error", p)
		}
	}
}

func TestApp_PtyRegistry_AddGetRemove(t *testing.T) {
	a := &App{bridges: map[string]*ptyEntry{}}

	a.putBridge("t1", &ptyEntry{})
	if _, ok := a.getBridge("t1"); !ok {
		t.Fatal("expected t1 present after put")
	}
	a.removeBridge("t1")
	if _, ok := a.getBridge("t1"); ok {
		t.Fatal("expected t1 absent after remove")
	}
}

func TestApp_Emit_UsesSeam(t *testing.T) {
	var gotEvent string
	a := &App{emit: func(event string, _ ...any) { gotEvent = event }}
	a.emit("sessions-changed")
	if gotEvent != "sessions-changed" {
		t.Fatalf("emit seam event = %q, want sessions-changed", gotEvent)
	}
}

// TestValidateWorktreeUnderRoots_SymlinkEscape uses REAL on-disk symlinks so the
// EvalSymlinks containment check is actually exercised (not skipped). The escape
// link points at a real directory OUTSIDE the root, so EvalSymlinks resolves
// successfully and it is the prefix check — not a resolve error — that rejects it.
func TestValidateWorktreeUnderRoots_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	roots := []string{root}

	// ESCAPE → must be REJECTED by containment (target resolves successfully).
	outsideTarget := t.TempDir() // real dir outside root
	escapeLink := filepath.Join(root, "evil-link")
	if err := os.Symlink(outsideTarget, escapeLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(escapeLink, roots); err == nil {
		t.Error("symlink whose target escapes root must be rejected by containment")
	}

	// INSIDE → must be ACCEPTED.
	realInside := filepath.Join(root, "real")
	if err := os.MkdirAll(realInside, 0o755); err != nil {
		t.Fatal(err)
	}
	goodLink := filepath.Join(root, "good-link")
	if err := os.Symlink(realInside, goodLink); err != nil {
		t.Fatal(err)
	}
	if err := validateWorktreeUnderRoots(goodLink, roots); err != nil {
		t.Errorf("symlink resolving inside root must be accepted: %v", err)
	}
}
