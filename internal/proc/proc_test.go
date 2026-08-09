package proc_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/proc"
)

// ── ExitCode ──────────────────────────────────────────────────────────────────

func TestExitCode_NilIsMinusOne(t *testing.T) {
	if got := proc.ExitCode(nil); got != -1 {
		t.Errorf("ExitCode(nil) = %d, want -1", got)
	}
}

func TestExitCode_PlainErrorIsMinusOne(t *testing.T) {
	if got := proc.ExitCode(errors.New("x")); got != -1 {
		t.Errorf("ExitCode(plain error) = %d, want -1", got)
	}
}

func TestExitCode_FakeExitError(t *testing.T) {
	tests := []struct {
		code int
	}{
		{1}, {2}, {127},
	}
	for _, tc := range tests {
		got := proc.ExitCode(proc.FakeExitError{Code: tc.code})
		if got != tc.code {
			t.Errorf("ExitCode(FakeExitError{%d}) = %d, want %d", tc.code, got, tc.code)
		}
	}
}

func TestExitCode_RealExecExitError(t *testing.T) {
	// Verify the *exec.ExitError path: sh exits 3, ExitCode must return 3.
	var r proc.ExecRunner
	_, _, err := r.Run(context.Background(), "sh", "-c", "exit 3")
	if err == nil {
		t.Fatal("expected non-zero exit error, got nil")
	}
	if got := proc.ExitCode(err); got != 3 {
		t.Errorf("ExitCode(sh -c 'exit 3') = %d, want 3", got)
	}
}

func TestFakeExitError_ErrorString(t *testing.T) {
	e := proc.FakeExitError{Code: 42}
	if !strings.Contains(e.Error(), "42") {
		t.Errorf("FakeExitError.Error() = %q, want to contain 42", e.Error())
	}
}

// ── FakeRunner ────────────────────────────────────────────────────────────────

func TestFakeRunner_RecordsCalls(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("hello\n")}, "echo", "hello")
	r.Respond(proc.FakeResult{Stdout: []byte("clean\n")}, "git", "status")

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
	r.Respond(proc.FakeResult{Stdout: []byte("abc1234\n")}, "git", "rev-parse", "HEAD")
	r.Respond(proc.FakeResult{Stderr: []byte("To github.com\n")}, "git", "push")
	r.Respond(proc.FakeResult{Err: errors.New("exit status 1")}, "false")

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

func TestExecRunner_RunInDir_UsesGivenCwd(t *testing.T) {
	tmp := t.TempDir()
	want, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", tmp, err)
	}

	var r proc.ExecRunner
	stdout, _, err := r.RunInDir(context.Background(), tmp, "pwd")
	if err != nil {
		t.Fatalf("RunInDir: unexpected error: %v", err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(stdout)))
	if err != nil {
		t.Fatalf("EvalSymlinks(stdout): %v", err)
	}
	if got != want {
		t.Errorf("RunInDir cwd: got %q, want %q", got, want)
	}
}

// ── FakeRunner RunInDir ───────────────────────────────────────────────────────

func TestFakeRunner_RunInDir_RecordsDir(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("ok\n")}, "ls", "-la")

	ctx := context.Background()
	_, _, err := r.RunInDir(ctx, "/some/dir", "ls", "-la")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.Calls))
	}
	if r.Calls[0].Dir != "/some/dir" {
		t.Errorf("Call.Dir: got %q, want %q", r.Calls[0].Dir, "/some/dir")
	}
}

func TestFakeRunner_Run_RecordsDirEmpty(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("hi\n")}, "echo", "hi")

	_, _, err := r.Run(context.Background(), "echo", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.Calls))
	}
	if r.Calls[0].Dir != "" {
		t.Errorf("Run should record Dir as empty string, got %q", r.Calls[0].Dir)
	}
}

func TestFakeRunner_RunInDir_CannedLookupIgnoresCwd(t *testing.T) {
	// The response key is name+args only; cwd does not affect routing.
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("result\n")}, "git", "log")

	ctx := context.Background()
	stdout, _, err := r.RunInDir(ctx, "/project/a", "git", "log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout) != "result\n" {
		t.Errorf("stdout: got %q, want %q", stdout, "result\n")
	}
	// Same response returned for a different dir.
	stdout2, _, err := r.RunInDir(ctx, "/project/b", "git", "log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout2) != "result\n" {
		t.Errorf("stdout: got %q, want %q", stdout2, "result\n")
	}
}

func TestFakeRunner_Run_DelegatesViaRunInDir(t *testing.T) {
	// Run delegates to RunInDir; the canned lookup must still work.
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("delegated\n")}, "cat", "file")

	stdout, _, err := r.Run(context.Background(), "cat", "file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout) != "delegated\n" {
		t.Errorf("stdout: got %q, want %q", stdout, "delegated\n")
	}
}

// ── RunStdin ──────────────────────────────────────────────────────────────────

func TestFakeRunner_RunStdin_RecordsCallAndStdin(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{}, "git", "apply", "--cached", "-")

	stdinData := []byte("--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n")
	_, _, err := r.RunStdin(context.Background(), "/repo", stdinData, "git", "apply", "--cached", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.Calls))
	}
	c := r.Calls[0]
	if c.Name != "git" {
		t.Errorf("Call.Name = %q, want %q", c.Name, "git")
	}
	if c.Dir != "/repo" {
		t.Errorf("Call.Dir = %q, want %q", c.Dir, "/repo")
	}
	if string(c.Stdin) != string(stdinData) {
		t.Errorf("Call.Stdin = %q, want %q", c.Stdin, stdinData)
	}
}

func TestExecRunner_RunStdin_PipesData(t *testing.T) {
	// Use `cat` to echo stdin back on stdout; verifies the pipe is wired correctly.
	var r proc.ExecRunner
	input := []byte("hello from stdin\n")
	stdout, _, err := r.RunStdin(context.Background(), "", input, "cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout) != string(input) {
		t.Errorf("stdout = %q, want %q", stdout, input)
	}
}

func TestExecRunner_RunStdin_UsesDir(t *testing.T) {
	tmp := t.TempDir()
	want, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	var r proc.ExecRunner
	stdout, _, err := r.RunStdin(context.Background(), tmp, nil, "pwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(stdout)))
	if err != nil {
		t.Fatalf("EvalSymlinks(stdout): %v", err)
	}
	if got != want {
		t.Errorf("RunStdin dir: got %q, want %q", got, want)
	}
}
