package agent

import (
	"errors"
	"reflect"
	"testing"
)

// ── arg builders, Detect, Name ─────────────────────────────────────────────────

func TestOpencode_ResumeArgs(t *testing.T) {
	got := NewOpencode().ResumeArgs("ses_x")
	want := []string{"--session", "ses_x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResumeArgs = %v, want %v", got, want)
	}
}

func TestOpencode_NewArgs(t *testing.T) {
	c := NewOpencode()
	tests := []struct {
		name string
		opts NewOpts
		want []string
	}{
		{"none", NewOpts{}, nil},
		{"model", NewOpts{Model: "anthropic/claude"}, []string{"--model", "anthropic/claude"}},
		{"agent", NewOpts{Agent: "build"}, []string{"--agent", "build"}},
		{"prompt", NewOpts{Prompt: "do x"}, []string{"--prompt", "do x"}},
		{
			"all (SessionID ignored)",
			NewOpts{Model: "m", Agent: "a", Prompt: "p", SessionID: "ignored"},
			[]string{"--model", "m", "--agent", "a", "--prompt", "p"},
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

func TestOpencode_Detect(t *testing.T) {
	ok := NewOpencode()
	ok.LookPath = func(string) (string, error) { return "/usr/bin/opencode", nil }
	if !ok.Detect() {
		t.Error("Detect() = false, want true when LookPath succeeds")
	}

	no := NewOpencode()
	no.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if no.Detect() {
		t.Error("Detect() = true, want false when LookPath fails")
	}
}

func TestOpencode_Name(t *testing.T) {
	if got := NewOpencode().Name(); got != "opencode" {
		t.Errorf("Name() = %q, want opencode", got)
	}
}

// TestOpencode_ZeroValueDefaults exercises the nil-seam fallbacks so a
// zero-value Opencode never panics on the pure paths.
func TestOpencode_ZeroValueDefaults(t *testing.T) {
	var o Opencode
	if o.bin() != "opencode" {
		t.Errorf("zero-value bin() = %q, want opencode", o.bin())
	}
	_ = o.Detect()           // default exec.LookPath; PATH-dependent, just no panic
	_ = o.NewArgs(NewOpts{}) // pure
}
