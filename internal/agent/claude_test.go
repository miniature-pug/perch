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
	tests := []struct {
		name string
		opts NewOpts
		want []string
	}{
		{"none", NewOpts{}, nil},
		{"model", NewOpts{Model: "opus"}, []string{"--model", "opus"}},
		{"sessionid", NewOpts{SessionID: "uuid"}, []string{"--session-id", "uuid"}},
		{"prompt", NewOpts{Prompt: "do x"}, []string{"do x"}},
		{
			"all",
			NewOpts{Model: "opus", SessionID: "uuid", Prompt: "do x", Agent: "ignored"},
			[]string{"--model", "opus", "--session-id", "uuid", "do x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.NewArgs(tt.opts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewArgs(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
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

// TestZeroValueDefaults exercises the nil-seam fallback helpers so a
// zero-value Claude (the var _ Adapter = Claude{} guarantee) never panics.
func TestZeroValueDefaults(t *testing.T) {
	var c Claude // all seams nil, Bin empty

	// bin() falls back to "claude"; Detect uses default exec.LookPath. We do
	// not assert the boolean (PATH-dependent) — only that no panic occurs.
	_ = c.Detect()
}
