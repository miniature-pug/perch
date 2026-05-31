// Package proc provides a shared command Runner interface for all shell-outs
// in perch. Every subprocess invocation goes through the Runner interface so
// that production code uses ExecRunner and unit tests use FakeRunner — no
// process is ever spawned in a unit test.
package proc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes external commands. All subprocess calls in perch must go
// through this interface so tests can inject a FakeRunner without spawning
// real processes.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ── ExecRunner ────────────────────────────────────────────────────────────────

// ExecRunner is the production Runner. It delegates to os/exec and is only
// used in the binary and integration tests. Use the zero value directly:
//
//	var r proc.ExecRunner
type ExecRunner struct{}

// Run executes name with args under ctx. Stdout and stderr are captured into
// separate buffers — callers need stderr distinct from stdout for diagnostics
// (e.g. git writes progress to stderr and the requested data to stdout). The
// command's error is returned verbatim so callers can inspect *exec.ExitError
// exit codes; partial output is always returned regardless of error.
func (e ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// ── FakeRunner ────────────────────────────────────────────────────────────────

// Call records a single invocation of FakeRunner.Run.
type Call struct {
	Name string
	Args []string
}

// FakeResult is the canned response returned by FakeRunner for a matched command.
type FakeResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

// FakeRunner is a test double for Runner. It records every call and returns
// canned results keyed by the full command line ("name arg1 arg2 …"). An
// optional Default is consulted when no keyed response matches. An unmatched
// call with no Default returns a clear error rather than panicking.
//
// Because Run records state into Calls, always use a *FakeRunner:
//
//	r := proc.NewFakeRunner()
type FakeRunner struct {
	// Calls holds every invocation in order, with the name and args as passed.
	Calls []Call

	// Responses maps the full command line ("name arg1 arg2 …") to a FakeResult.
	Responses map[string]FakeResult

	// Default, when non-nil, is returned for any command that has no entry in
	// Responses. When nil, an unmatched command returns an error.
	Default *FakeResult
}

// NewFakeRunner returns an initialised *FakeRunner ready for use in tests.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		Responses: make(map[string]FakeResult),
	}
}

// cmdline builds the map key from a command invocation.
func cmdline(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

// Run records the call, looks up a canned response, and returns it. If no
// response is found and Default is nil, it returns a descriptive error.
func (f *FakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.Calls = append(f.Calls, Call{Name: name, Args: args})
	key := cmdline(name, args)
	if res, ok := f.Responses[key]; ok {
		return res.Stdout, res.Stderr, res.Err
	}
	if f.Default != nil {
		return f.Default.Stdout, f.Default.Stderr, f.Default.Err
	}
	return nil, nil, fmt.Errorf("proc: FakeRunner: no canned response for %q", key)
}
