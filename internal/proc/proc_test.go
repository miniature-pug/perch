package proc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// ── FakeRunner ────────────────────────────────────────────────────────────────

func TestFakeRunner_RecordsCalls(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Responses["echo hello"] = proc.FakeResult{Stdout: []byte("hello\n")}
	r.Responses["git status"] = proc.FakeResult{Stdout: []byte("clean\n")}

	ctx := context.Background()
	_, _, _ = r.Run(ctx, "echo", "hello")
	_, _, _ = r.Run(ctx, "git", "status")

	if len(r.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.Calls))
	}
	if r.Calls[0].Name != "echo" || len(r.Calls[0].Args) != 1 || r.Calls[0].Args[0] != "hello" {
		t.Errorf("call[0]: got %+v", r.Calls[0])
	}
	if r.Calls[1].Name != "git" || len(r.Calls[1].Args) != 1 || r.Calls[1].Args[0] != "status" {
		t.Errorf("call[1]: got %+v", r.Calls[1])
	}
}

func TestFakeRunner_MultipleCallsAccumulate(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{Stdout: []byte("ok\n")}

	ctx := context.Background()
	for i := range 5 {
		_, _, err := r.Run(ctx, "cmd", "arg")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	if len(r.Calls) != 5 {
		t.Errorf("expected 5 accumulated calls, got %d", len(r.Calls))
	}
}

func TestFakeRunner_ReturnsCannedResult(t *testing.T) {
	tests := []struct {
		name       string
		cmdName    string
		args       []string
		wantStdout string
		wantStderr string
		wantErr    bool
	}{
		{
			name:       "matched stdout",
			cmdName:    "git",
			args:       []string{"rev-parse", "HEAD"},
			wantStdout: "abc1234\n",
		},
		{
			name:       "matched stderr",
			cmdName:    "git",
			args:       []string{"push"},
			wantStderr: "To github.com\n",
		},
		{
			name:    "matched error",
			cmdName: "false",
			args:    nil,
			wantErr: true,
		},
	}

	r := proc.NewFakeRunner()
	r.Responses["git rev-parse HEAD"] = proc.FakeResult{Stdout: []byte("abc1234\n")}
	r.Responses["git push"] = proc.FakeResult{Stderr: []byte("To github.com\n")}
	r.Responses["false"] = proc.FakeResult{Err: errors.New("exit status 1")}

	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := r.Run(ctx, tc.cmdName, tc.args...)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if string(stdout) != tc.wantStdout {
				t.Errorf("stdout: got %q, want %q", stdout, tc.wantStdout)
			}
			if string(stderr) != tc.wantStderr {
				t.Errorf("stderr: got %q, want %q", stderr, tc.wantStderr)
			}
		})
	}
}

func TestFakeRunner_DefaultFallback(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{Stdout: []byte("default\n")}

	stdout, _, err := r.Run(context.Background(), "any", "command")
	if err != nil {
		t.Fatalf("unexpected error with default set: %v", err)
	}
	if string(stdout) != "default\n" {
		t.Errorf("stdout: got %q, want %q", stdout, "default\n")
	}
}

func TestFakeRunner_UnmatchedNoDefault_ReturnsError(t *testing.T) {
	// Verify no panic and a descriptive error is returned.
	r := proc.NewFakeRunner()

	var gotErr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("FakeRunner panicked: %v", p)
			}
		}()
		_, _, gotErr = r.Run(context.Background(), "nonexistent", "arg")
	}()

	if gotErr == nil {
		t.Fatal("expected an error for unmatched call with no default, got nil")
	}
	if !strings.Contains(gotErr.Error(), "no canned response") {
		t.Errorf("error message should mention 'no canned response', got: %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), `"nonexistent arg"`) {
		t.Errorf("error message should include the command line, got: %v", gotErr)
	}
}

// ── ExecRunner ────────────────────────────────────────────────────────────────

func TestExecRunner_HappyPath(t *testing.T) {
	// Use `go env GOOS` — the go binary is guaranteed present on any Go build host.
	var r proc.ExecRunner
	stdout, _, err := r.Run(context.Background(), "go", "env", "GOOS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(string(stdout))
	if out == "" {
		t.Error("expected non-empty stdout from 'go env GOOS'")
	}
	// On this host we expect "linux".
	if out != "linux" {
		t.Logf("GOOS = %q (not linux, but not necessarily wrong)", out)
	}
}

func TestExecRunner_NonZeroExit_ReturnsErrorAndOutput(t *testing.T) {
	// `go env BOGUS_VAR_THAT_DOES_NOT_EXIST` exits non-zero and writes to stderr.
	// This tests that ExecRunner returns the error AND the captured output.
	var r proc.ExecRunner
	_, stderr, err := r.Run(context.Background(), "go", "env", "--bogus-flag-that-does-not-exist")
	if err == nil {
		t.Fatal("expected non-zero exit error, got nil")
	}
	// stderr should be non-empty (go env writes usage/error there).
	if len(stderr) == 0 {
		t.Error("expected non-empty stderr for invalid go env flag")
	}
}
