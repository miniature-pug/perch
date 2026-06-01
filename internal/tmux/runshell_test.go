package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestRunShell_ArgvNoSocket(t *testing.T) {
	r := proc.NewFakeRunner()
	script := "echo hello"
	r.Respond(proc.FakeResult{}, "tmux", "run-shell", "-b", script)

	o := Tmux{Runner: r, Bin: "tmux"}
	if err := o.RunShell(context.Background(), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"run-shell", "-b", script}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestRunShell_ArgvWithSocket(t *testing.T) {
	r := proc.NewFakeRunner()
	script := "echo hello"
	r.Respond(proc.FakeResult{}, "tmux", "-L", "perchtest", "run-shell", "-b", script)

	o := Tmux{Runner: r, Bin: "tmux", Socket: "perchtest"}
	if err := o.RunShell(context.Background(), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"-L", "perchtest", "run-shell", "-b", script}
	if !reflect.DeepEqual(r.Calls[0].Args, want) {
		t.Errorf("Args = %v, want %v", r.Calls[0].Args, want)
	}
}

func TestRunShell_ErrorWrapped(t *testing.T) {
	r := proc.NewFakeRunner()
	script := "bad script"
	r.Respond(proc.FakeResult{
		Err:    proc.FakeExitError{Code: 1},
		Stderr: []byte("script failed"),
	}, "tmux", "run-shell", "-b", script)

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.RunShell(context.Background(), script)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "tmux run-shell") {
		t.Errorf("error should contain 'tmux run-shell': %v", err)
	}
	if !strings.Contains(err.Error(), "script failed") {
		t.Errorf("error should contain stderr text: %v", err)
	}
}

func TestRunShell_ExecLayerError(t *testing.T) {
	execErr := errors.New("exec: tmux not found")
	r := proc.NewFakeRunner()
	script := "echo hi"
	r.Respond(proc.FakeResult{Err: execErr, Stderr: []byte("tmux not found")},
		"tmux", "run-shell", "-b", script)

	o := Tmux{Runner: r, Bin: "tmux"}
	err := o.RunShell(context.Background(), script)
	if err == nil {
		t.Fatal("expected error for exec failure")
	}
	if !errors.Is(err, execErr) {
		t.Errorf("error should wrap execErr: %v", err)
	}
}
