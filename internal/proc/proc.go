// Package proc gives perch a shared command Runner interface for every
// subprocess call. Every subprocess call goes through the Runner interface.
// Production code uses ExecRunner, and unit tests use FakeRunner instead. A
// unit test never spawns a real process.
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner executes external commands. Every subprocess call in perch must go
// through this interface. Tests can then inject a FakeRunner without
// spawning a real process.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
	// RunInDir works like Run, but it runs the command with its working
	// directory set to dir. When dir is empty, the command inherits the
	// parent process cwd unchanged. This makes RunInDir(ctx, "", ...)
	// identical to Run(ctx, ...).
	RunInDir(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
	// RunStdin works like RunInDir, but it also pipes stdin into the
	// command's standard input. RunStdin is the only way to send patch data
	// to `git apply -` through the runner seam. This lets tests intercept the
	// call through FakeRunner.
	RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error)
}

// ── ExecRunner ────────────────────────────────────────────────────────────────

// ExecRunner is the production Runner. It delegates to os/exec. Only the
// binary and integration tests use ExecRunner. Use the zero value directly:
//
//	var r proc.ExecRunner
type ExecRunner struct{}

// RunInDir executes name with args under ctx, with its working directory set
// to dir. When dir is empty, the command inherits the parent cwd. RunInDir
// captures stdout and stderr into separate buffers: callers need stderr
// distinct from stdout for diagnostics (for example, git writes progress to
// stderr and the requested data to stdout). RunInDir returns the command's
// error verbatim, so callers can inspect *exec.ExitError exit codes.
// RunInDir always returns partial output, regardless of the error.
//
// See run for how ctx cancellation, process groups and the git environment
// are handled.
func (e ExecRunner) RunInDir(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	return e.run(ctx, dir, nil, name, args...)
}

// Run executes name with args. Run inherits the parent process working directory.
func (e ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return e.RunInDir(ctx, "", name, args...)
}

// RunStdin executes name with args in dir, and pipes stdin into the command's
// standard input. When dir is empty, the command inherits the parent cwd.
// RunStdin captures stdout and stderr separately, and returns the command's
// error verbatim.
func (e ExecRunner) RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	if stdin == nil {
		stdin = []byte{}
	}
	return e.run(ctx, dir, stdin, name, args...)
}

// pipeDrainDelay bounds how long run waits for the stdout and stderr pipes
// to reach EOF after the command exits or ctx is done. A grandchild that
// inherited the pipes (a backgrounded git hook, a credential helper) would
// otherwise hold Wait open for as long as it lives, so ctx alone could not
// bound the call.
const pipeDrainDelay = 2 * time.Second

// gitEnv is appended to the environment of every git command ExecRunner runs.
//
//   - GIT_OPTIONAL_LOCKS=0: perch runs read-only git commands (status, diff)
//     in the background on every file change. Without this, `git status`
//     refreshes the index and writes it back under index.lock, which makes
//     the agent's own `git add` or `git commit` fail with "index.lock: File
//     exists". The variable only drops OPTIONAL locks; commands that must
//     write the index (apply --cached, worktree add) still take the lock.
//   - GIT_TERMINAL_PROMPT=0: perch has no terminal to answer a credential
//     prompt, so git fails fast instead of hanging until the timeout.
//   - LANGUAGE=C, LC_MESSAGES=C: callers match a few git error messages
//     (index lock contention, "already exists"). Forcing untranslated
//     messages keeps those matches valid under any user locale. LC_CTYPE is
//     left alone, so hooks still see the user's character set.
var gitEnv = []string{
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
	"LANGUAGE=C",
	"LC_MESSAGES=C",
}

// run is the shared body of RunInDir and RunStdin. A nil stdin means no
// standard input.
//
// The command runs in its own process group. When ctx is done, run kills the
// whole group, not just the direct child, so hook processes and other
// helpers that git started die with it. After the command exits (or ctx is
// done), run waits at most pipeDrainDelay for the output pipes to close. If
// the command itself succeeded and only a lingering grandchild held the pipes
// past that delay, run reports success with the output it collected.
func (e ExecRunner) run(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// os/exec treats Dir=="" as the parent cwd. This guard makes that intent explicit.
	if dir != "" {
		cmd.Dir = dir
	}
	if filepath.Base(name) == "git" {
		cmd.Env = append(os.Environ(), gitEnv...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	setProcessGroup(cmd)
	cmd.WaitDelay = pipeDrainDelay
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// The command exited successfully; only an inherited pipe outlived it.
		err = nil
	}
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// ── FakeRunner ────────────────────────────────────────────────────────────────

// Call records a single invocation of FakeRunner.Run, FakeRunner.RunInDir,
// or FakeRunner.RunStdin. Stdin is non-nil only for RunStdin calls.
type Call struct {
	Name  string
	Args  []string
	Dir   string
	Stdin []byte // non-nil only for RunStdin invocations
}

// FakeResult is the canned response FakeRunner returns for a matched command.
type FakeResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

// FakeRunner is a test double for Runner. It records every call, and returns
// canned results that Respond registered. When no registered response
// matches, FakeRunner consults an optional Default instead. An unmatched
// call with no Default returns a clear error, and never panics.
//
// Because Run records state into Calls, always use a *FakeRunner:
//
//	r := proc.NewFakeRunner()
//	r.Respond(proc.FakeResult{Stdout: out}, "git", "status")
//
// FakeRunner is NOT safe for concurrent use. It records calls without
// synchronization. Each test should use its own instance, from NewFakeRunner().
type FakeRunner struct {
	// Calls holds every invocation in order, with the name and args as passed.
	Calls []Call

	// Responses maps an internal command-line key to a FakeResult. Register
	// entries with Respond, rather than writing this map directly. The key
	// format is an implementation detail (see cmdline).
	Responses map[string]FakeResult

	// Default, when non-nil, is the response for any command that has no
	// entry in Responses. When nil, an unmatched command returns an error.
	Default *FakeResult
}

// NewFakeRunner returns an initialised *FakeRunner ready for use in tests.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		Responses: make(map[string]FakeResult),
	}
}

// Respond registers the canned result for a call to Run with the given name
// and args. Respond hides the internal key format, so callers never build
// command-line keys by hand.
func (f *FakeRunner) Respond(res FakeResult, name string, args ...string) {
	f.Responses[cmdline(name, args)] = res
}

// cmdline builds the map key from a command invocation.
// NUL separators ensure distinct arg boundaries never collide (for example, "a b","c" vs "a","b c").
func cmdline(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + "\x00" + strings.Join(args, "\x00")
}

// RunInDir records the call, including dir, and returns the canned response
// keyed by name and args only. RunInDir deliberately excludes cwd from the
// response key. Tests assert the working directory through Call.Dir, instead
// of through response routing.
func (f *FakeRunner) RunInDir(_ context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	f.Calls = append(f.Calls, Call{Name: name, Args: args, Dir: dir})
	key := cmdline(name, args)
	if res, ok := f.Responses[key]; ok {
		return res.Stdout, res.Stderr, res.Err
	}
	if f.Default != nil {
		return f.Default.Stdout, f.Default.Stderr, f.Default.Err
	}
	human := name
	if len(args) > 0 {
		human = name + " " + strings.Join(args, " ")
	}
	return nil, nil, fmt.Errorf("proc: FakeRunner: no canned response for %q", human)
}

func (f *FakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return f.RunInDir(ctx, "", name, args...)
}

// RunStdin records the call, including dir and stdin, and returns the canned
// response keyed by name and args only. RunStdin records stdin in Call.Stdin
// for tests to assert on. stdin does not affect response routing.
func (f *FakeRunner) RunStdin(_ context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	f.Calls = append(f.Calls, Call{Name: name, Args: args, Dir: dir, Stdin: stdin})
	key := cmdline(name, args)
	if res, ok := f.Responses[key]; ok {
		return res.Stdout, res.Stderr, res.Err
	}
	if f.Default != nil {
		return f.Default.Stdout, f.Default.Stderr, f.Default.Err
	}
	human := name
	if len(args) > 0 {
		human = name + " " + strings.Join(args, " ")
	}
	return nil, nil, fmt.Errorf("proc: FakeRunner: no canned response for %q", human)
}

// ── ExitCode ──────────────────────────────────────────────────────────────────

// exitCoder is the interface that *exec.ExitError and FakeExitError satisfy.
// An interface, rather than a concrete type, keeps the check version-stable,
// and lets tests inject a non-zero exit without spawning a real process.
type exitCoder interface {
	ExitCode() int
}

// ExitCode returns the process exit code that err carries: 0 when err is nil
// (the command succeeded), and -1 when err exposes no exit code (for
// example, the command could not start). Callers can then branch on exit
// status, without matching version-fragile stderr text.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// FakeExitError lets a FakeRunner response simulate a specific process exit
// status in a unit test, without spawning a real process. ExitCode
// recognises FakeExitError.
type FakeExitError struct{ Code int }

func (e FakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }
func (e FakeExitError) ExitCode() int { return e.Code }
