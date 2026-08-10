package agent

import (
	"errors"
	"reflect"
	"testing"
)

// ── arg builders, Detect, Name ─────────────────────────────────────────────────

func TestResumeArgs(t *testing.T) {
	got := NewClaude().ResumeArgs("abc")
	want := []string{"--resume", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs = %v, want %v", got, want)
	}
}

func TestNewArgs(t *testing.T) {
	c := NewClaude()
	if got := c.NewArgs(); got != nil {
		t.Errorf("NewArgs() = %v, want nil", got)
	}
}

func TestDetect(t *testing.T) {
	ok := NewClaude()
	ok.LookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	if !ok.Detect() {
		t.Errorf("Detect() = false, want true when LookPath succeeds")
	}

	no := NewClaude()
	no.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if no.Detect() {
		t.Errorf("Detect() = true, want false when LookPath fails")
	}
}

func TestName(t *testing.T) {
	if got := NewClaude().Name(); got != "claude" {
		t.Errorf("Name() = %q, want claude", got)
	}
}

// TestZeroValueDefaults exercises the nil-seam fallback helpers, so a
// zero-value Claude never panics. This backs the var _ Adapter = Claude{}
// guarantee.
func TestZeroValueDefaults(t *testing.T) {
	var c Claude // all seams nil, Bin empty

	// bin() falls back to "claude". Detect uses the default exec.LookPath.
	// This test does not check the boolean result, because it depends on
	// PATH. This test only checks that no panic occurs.
	_ = c.Detect()
}
