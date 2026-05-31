package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestRunHooks_Empty(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{} // succeed on anything

	err := RunHooks(context.Background(), r, "/tree", "post_create", nil, HookEnv{})
	if err != nil {
		t.Fatalf("RunHooks(empty): unexpected error: %v", err)
	}
	if len(r.Calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(r.Calls))
	}
}

func TestRunHooks_CallsInOrder(t *testing.T) {
	env := HookEnv{
		Handle:       "feat-login",
		WorktreePath: "/trees/feat-login",
		ProjectRoot:  "/repo",
		Branch:       "feat/login",
	}
	cmds := []string{"echo hi", "pnpm install"}

	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{} // all calls succeed

	err := RunHooks(context.Background(), r, "/trees/feat-login", "post_create", cmds, env)
	if err != nil {
		t.Fatalf("RunHooks: %v", err)
	}
	if len(r.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.Calls))
	}

	for i, cmd := range cmds {
		c := r.Calls[i]
		if c.Name != "sh" {
			t.Errorf("call[%d].Name = %q; want %q", i, c.Name, "sh")
		}
		if c.Dir != "/trees/feat-login" {
			t.Errorf("call[%d].Dir = %q; want %q", i, c.Dir, "/trees/feat-login")
		}
		if len(c.Args) < 2 || c.Args[0] != "-c" {
			t.Errorf("call[%d].Args = %v; want [\"-c\", ...]", i, c.Args)
		}
		script := c.Args[1]
		// Script must contain all four PERCH_* exports.
		for _, want := range []string{
			"PERCH_HANDLE=",
			"PERCH_WORKTREE_PATH=",
			"PERCH_PROJECT_ROOT=",
			"PERCH_BRANCH=",
		} {
			if !strings.Contains(script, want) {
				t.Errorf("call[%d] script missing %q:\n%s", i, want, script)
			}
		}
		// Script must end with the original command.
		if !strings.HasSuffix(script, "; "+cmd) {
			t.Errorf("call[%d] script does not end with %q:\n%s", i, "; "+cmd, script)
		}
	}
}

func TestRunHooks_EnvValuesQuoted(t *testing.T) {
	// Values with single quotes and spaces must be shell-quoted correctly.
	env := HookEnv{
		Handle:       "it's a handle",
		WorktreePath: "/path/with spaces",
		ProjectRoot:  "/repo",
		Branch:       "feat/it's-fine",
	}
	r := proc.NewFakeRunner()
	r.Default = &proc.FakeResult{}

	if err := RunHooks(context.Background(), r, "/tree", "test", []string{"true"}, env); err != nil {
		t.Fatalf("RunHooks: %v", err)
	}

	script := r.Calls[0].Args[1]
	// Verify the escaped single-quote sequence appears in the script.
	if !strings.Contains(script, `'\''`) {
		t.Errorf("expected shell-escaped single quotes in script:\n%s", script)
	}
}

func TestRunHooks_FailureAbortsRemaining(t *testing.T) {
	env := HookEnv{Handle: "h", WorktreePath: "/t", ProjectRoot: "/r", Branch: "b"}
	cmds := []string{"echo hi", "pnpm install"}

	r := proc.NewFakeRunner()
	// First call succeeds (default), second is registered to fail.
	r.Default = &proc.FakeResult{}

	// Build the exact script for the second command so we can key the response.
	failScript := buildScript("pnpm install", env)
	r.Respond(proc.FakeResult{Err: errors.New("exit status 1")}, "sh", "-c", failScript)

	err := RunHooks(context.Background(), r, "/t", "post_create", cmds, env)
	if err == nil {
		t.Fatal("RunHooks: expected error on second hook, got nil")
	}
	if !strings.Contains(err.Error(), "pnpm install") {
		t.Errorf("error should name the failing command; got: %v", err)
	}

	// First hook ran, second ran (and failed), no third call.
	if len(r.Calls) != 2 {
		t.Errorf("expected exactly 2 calls (1 success + 1 fail), got %d", len(r.Calls))
	}
}

func TestRunHooks_PhaseInError(t *testing.T) {
	env := HookEnv{}
	r := proc.NewFakeRunner()
	failScript := buildScript("bad-cmd", env)
	r.Respond(proc.FakeResult{Err: errors.New("exit 1")}, "sh", "-c", failScript)

	err := RunHooks(context.Background(), r, "/t", "pre_remove", []string{"bad-cmd"}, env)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pre_remove") {
		t.Errorf("error should mention phase; got: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, tc := range cases {
		got := shellQuote(tc.in)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
