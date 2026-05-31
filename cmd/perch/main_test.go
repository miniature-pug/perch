package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper executes run and returns stdout, stderr, and the exit code.
func callRun(args []string) (stdout, stderr string, code int) {
	var out, errBuf strings.Builder
	code = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// ── No-args: TUI stub ─────────────────────────────────────────────────────────

func TestRun_NoArgs_TUIStub(t *testing.T) {
	out, _, code := callRun([]string{})
	if code != 0 {
		t.Errorf("expected exit 0 for no-args, got %d", code)
	}
	if !strings.Contains(out, "TUI") {
		t.Errorf("expected TUI stub message, got: %q", out)
	}
}

// ── Valid directory path ───────────────────────────────────────────────────────

func TestRun_ValidDirPath_TUIStub(t *testing.T) {
	dir := t.TempDir()
	out, _, code := callRun([]string{dir})
	if code != 0 {
		t.Errorf("expected exit 0 for valid dir, got %d", code)
	}
	if !strings.Contains(out, "TUI") {
		t.Errorf("expected TUI stub message, got: %q", out)
	}
	if !strings.Contains(out, dir) {
		t.Errorf("expected root path in output, got: %q", out)
	}
}

// ── Non-existent path → exit 2 ────────────────────────────────────────────────

func TestRun_NonExistentPath_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"/does/not/exist/perch-test"})
	if code != 2 {
		t.Errorf("expected exit 2 for non-existent path, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── File path (not a directory) → exit 2 ─────────────────────────────────────

func TestRun_FilePath_Exit2(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "somefile.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := callRun([]string{f})
	if code != 2 {
		t.Errorf("expected exit 2 for file path, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── setup stub ────────────────────────────────────────────────────────────────

func TestRun_Setup_Exit0(t *testing.T) {
	out, _, code := callRun([]string{"setup"})
	if code != 0 {
		t.Errorf("expected exit 0 for setup, got %d", code)
	}
	if !strings.Contains(out, "setup") {
		t.Errorf("expected setup message; got: %q", out)
	}
}

// ── resurrect stub ────────────────────────────────────────────────────────────

func TestRun_Resurrect_Exit0(t *testing.T) {
	out, _, code := callRun([]string{"resurrect"})
	if code != 0 {
		t.Errorf("expected exit 0 for resurrect, got %d", code)
	}
	if !strings.Contains(out, "resurrect") {
		t.Errorf("expected resurrect message; got: %q", out)
	}
}

// ── status set stubs ──────────────────────────────────────────────────────────

func TestRun_StatusSetWorking_Exit0(t *testing.T) {
	out, _, code := callRun([]string{"status", "set", "working"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set working, got %d", code)
	}
	if !strings.Contains(out, "working") {
		t.Errorf("expected 'working' in output; got: %q", out)
	}
}

func TestRun_StatusSetWaiting_Exit0(t *testing.T) {
	_, _, code := callRun([]string{"status", "set", "waiting"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set waiting, got %d", code)
	}
}

func TestRun_StatusSetDone_Exit0(t *testing.T) {
	_, _, code := callRun([]string{"status", "set", "done"})
	if code != 0 {
		t.Errorf("expected exit 0 for status set done, got %d", code)
	}
}

func TestRun_StatusSetInvalid_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status", "set", "invalid"})
	if code != 2 {
		t.Errorf("expected exit 2 for invalid status value, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

func TestRun_StatusNoArgs_Exit2(t *testing.T) {
	_, errOut, code := callRun([]string{"status"})
	if code != 2 {
		t.Errorf("expected exit 2 for bare status, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}

// ── version ───────────────────────────────────────────────────────────────────

func TestRun_Version_Exit0(t *testing.T) {
	out, _, code := callRun([]string{"version"})
	if code != 0 {
		t.Errorf("expected exit 0 for version, got %d", code)
	}
	// version var is "dev" in tests.
	if !strings.Contains(out, "dev") {
		t.Errorf("expected version string in output; got: %q", out)
	}
}

func TestRun_Version_ContainsPlatformInfo(t *testing.T) {
	out, _, _ := callRun([]string{"version"})
	if !strings.Contains(out, "go") {
		t.Errorf("expected Go version in output; got: %q", out)
	}
}

// ── doctor verb routes to doctor.Run ─────────────────────────────────────────

func TestRun_Doctor_Routes(t *testing.T) {
	// We don't fully control doctor's environment in this test, but we can assert
	// that the verb "doctor" actually dispatches into doctor.Run (not a stub).
	// doctor.Run always emits a "\nperch <version>\n" header and then tool rows
	// (tmux, git, etc.) regardless of whether those tools are present.
	// A routing regression to an unimplemented stub would print none of these.
	var out strings.Builder
	var errBuf strings.Builder
	code := run([]string{"doctor"}, &out, &errBuf)
	if code != 0 && code != 1 {
		t.Errorf("doctor returned unexpected code %d", code)
	}
	output := out.String()
	// The doctor report header always contains the version string.
	if !strings.Contains(output, "perch") {
		t.Errorf("expected doctor report header ('perch ...') in output; got:\n%s", output)
	}
	// doctor.Run always emits rows for every tool in the descriptor table.
	if !strings.Contains(output, "tmux") {
		t.Errorf("expected 'tmux' row in doctor output (proves real routing); got:\n%s", output)
	}
	if !strings.Contains(output, "git") {
		t.Errorf("expected 'git' row in doctor output (proves real routing); got:\n%s", output)
	}
}

// ── unknown verb (typo) → exit 2 ──────────────────────────────────────────────

func TestRun_UnknownVerb_Exit2(t *testing.T) {
	// "doctr" is not a known verb and not an existing directory.
	_, errOut, code := callRun([]string{"doctr"})
	if code != 2 {
		t.Errorf("expected exit 2 for unknown verb, got %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Errorf("expected usage on stderr; got: %q", errOut)
	}
}
