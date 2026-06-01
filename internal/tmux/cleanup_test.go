package tmux

import (
	"reflect"
	"strings"
	"testing"
)

// ── shellQuote ────────────────────────────────────────────────────────────────

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"simple", "'simple'"},
		{"/path/to/dir", "'/path/to/dir'"},
		{"with space", "'with space'"},
		// Single quote is escaped by ending the token, inserting \', reopening.
		{"o'brien", `'o'\''brien'`},
		// Double single-quote: two escapes back to back.
		{"it''s", `'it'\'''\''s'`},
		{"", "''"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := shellQuote(tc.in)
			if got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ── RunShellArgs ──────────────────────────────────────────────────────────────

func TestRunShellArgs(t *testing.T) {
	got := RunShellArgs("X")
	want := []string{"run-shell", "X"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RunShellArgs(%q) = %v, want %v", "X", got, want)
	}
}

func TestRunShellArgs_EmptyScript(t *testing.T) {
	got := RunShellArgs("")
	want := []string{"run-shell", ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RunShellArgs(%q) = %v, want %v", "", got, want)
	}
}

// ── CleanupScript ─────────────────────────────────────────────────────────────

// fullOpts is the representative case with all fields populated.
func fullOpts() CleanupOpts {
	return CleanupOpts{
		SourceWindowTarget: "=proj:=feat",
		SwitchToTarget:     "=proj",
		Tree:               "/home/user/proj__worktrees/feat",
		Branch:             "feat",
		RepoDir:            "/home/user/proj",
	}
}

func TestCleanupScript_FullOpts_ExactString(t *testing.T) {
	o := fullOpts()
	got := CleanupScript(o, 1700000000, "abc123")

	want := "sleep 0.3" +
		" && tmux switch-client -t '=proj' || true" +
		" && tmux kill-window -t '=proj:=feat' || true" +
		" && mv '/home/user/proj__worktrees/feat' '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'" +
		" && git -C '/home/user/proj' worktree prune || true" +
		" && git -C '/home/user/proj' branch -d -- 'feat' || true" +
		" && rm -rf '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'"

	if got != want {
		t.Errorf("CleanupScript mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestCleanupScript_NoBranch_NoBranchDeleteSegment(t *testing.T) {
	o := fullOpts()
	o.Branch = ""
	got := CleanupScript(o, 1700000000, "abc123")

	if strings.Contains(got, "branch -d") {
		t.Errorf("CleanupScript with empty Branch contains 'branch -d': %s", got)
	}
	// Exact string check for no-branch case.
	want := "sleep 0.3" +
		" && tmux switch-client -t '=proj' || true" +
		" && tmux kill-window -t '=proj:=feat' || true" +
		" && mv '/home/user/proj__worktrees/feat' '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'" +
		" && git -C '/home/user/proj' worktree prune || true" +
		" && rm -rf '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'"
	if got != want {
		t.Errorf("CleanupScript (no branch) mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestCleanupScript_NoSwitchTarget_NoSwitchClientSegment(t *testing.T) {
	o := fullOpts()
	o.SwitchToTarget = ""
	got := CleanupScript(o, 1700000000, "abc123")

	if strings.Contains(got, "switch-client") {
		t.Errorf("CleanupScript with empty SwitchToTarget contains 'switch-client': %s", got)
	}
	// Exact string check for no-switch case.
	want := "sleep 0.3" +
		" && tmux kill-window -t '=proj:=feat' || true" +
		" && mv '/home/user/proj__worktrees/feat' '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'" +
		" && git -C '/home/user/proj' worktree prune || true" +
		" && git -C '/home/user/proj' branch -d -- 'feat' || true" +
		" && rm -rf '/home/user/proj__worktrees/.perch_trash_abc123_1700000000'"
	if got != want {
		t.Errorf("CleanupScript (no switch) mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// TestCleanupScript_PathWithSpace verifies a tree path containing a space is
// properly single-quote-escaped so the shell does not split the argument.
func TestCleanupScript_PathWithSpace(t *testing.T) {
	o := CleanupOpts{
		SourceWindowTarget: "=proj:=feat",
		SwitchToTarget:     "=proj",
		Tree:               "/home/u/my repo",
		Branch:             "feat",
		RepoDir:            "/home/u/proj",
	}
	got := CleanupScript(o, 1000, "x")

	// The mv step must quote the space-containing path.
	if !strings.Contains(got, "mv '/home/u/my repo'") {
		t.Errorf("mv step does not contain properly quoted path with space: %s", got)
	}
	// trashDir parent is /home/u (no space), so trash path has no space.
	if !strings.Contains(got, "'/home/u/.perch_trash_x_1000'") {
		t.Errorf("trash dir not found or not quoted correctly in: %s", got)
	}
}

// TestCleanupScript_BranchWithSingleQuote verifies a branch name containing a
// single quote is escaped via the '\” POSIX sequence.
func TestCleanupScript_BranchWithSingleQuote(t *testing.T) {
	o := CleanupOpts{
		SourceWindowTarget: "=proj:=feat",
		SwitchToTarget:     "=proj",
		Tree:               "/home/u/proj__worktrees/feat",
		Branch:             "o'brien",
		RepoDir:            "/home/u/proj",
	}
	got := CleanupScript(o, 2000, "y")

	// o'brien → 'o'\''brien'
	if !strings.Contains(got, `'o'\''brien'`) {
		t.Errorf("branch single-quote not escaped correctly in: %s", got)
	}
}

// TestCleanupScript_TrashDirUsesNowAndSuffix verifies that both the injected
// now timestamp and trashSuffix appear in the trash directory name.
func TestCleanupScript_TrashDirUsesNowAndSuffix(t *testing.T) {
	o := fullOpts()
	got := CleanupScript(o, 9999, "myhash")

	if !strings.Contains(got, ".perch_trash_myhash_9999") {
		t.Errorf("trash dir does not contain expected suffix/timestamp in: %s", got)
	}
}

// TestCleanupScript_MandatoryOrder asserts that mv precedes prune, prune
// precedes branch-delete, and branch-delete precedes rm — locking the §7.2
// mandatory order by index position in the returned string.
func TestCleanupScript_MandatoryOrder(t *testing.T) {
	o := fullOpts()
	got := CleanupScript(o, 1700000000, "ord")

	idxMv := strings.Index(got, "mv ")
	idxPrune := strings.Index(got, "worktree prune")
	idxBranch := strings.Index(got, "branch -d")
	idxRm := strings.Index(got, "rm -rf")

	if idxMv < 0 || idxPrune < 0 || idxBranch < 0 || idxRm < 0 {
		t.Fatalf("one or more mandatory steps missing in: %s", got)
	}
	if idxMv >= idxPrune {
		t.Errorf("mv (%d) must precede prune (%d)", idxMv, idxPrune)
	}
	if idxPrune >= idxBranch {
		t.Errorf("prune (%d) must precede branch-delete (%d)", idxPrune, idxBranch)
	}
	if idxBranch >= idxRm {
		t.Errorf("branch-delete (%d) must precede rm (%d)", idxBranch, idxRm)
	}
}

// TestCleanupScript_MandatoryOrder_NoBranch verifies the order when Branch is
// empty: mv precedes prune, prune precedes rm.
func TestCleanupScript_MandatoryOrder_NoBranch(t *testing.T) {
	o := fullOpts()
	o.Branch = ""
	got := CleanupScript(o, 1700000000, "ord")

	idxMv := strings.Index(got, "mv ")
	idxPrune := strings.Index(got, "worktree prune")
	idxRm := strings.Index(got, "rm -rf")

	if idxMv < 0 || idxPrune < 0 || idxRm < 0 {
		t.Fatalf("one or more mandatory steps missing in: %s", got)
	}
	if idxMv >= idxPrune {
		t.Errorf("mv (%d) must precede prune (%d)", idxMv, idxPrune)
	}
	if idxPrune >= idxRm {
		t.Errorf("prune (%d) must precede rm (%d)", idxPrune, idxRm)
	}
}

// TestCleanupScript_SleepIsFirst asserts sleep 0.3 is always the first token.
func TestCleanupScript_SleepIsFirst(t *testing.T) {
	o := fullOpts()
	got := CleanupScript(o, 1, "s")
	if !strings.HasPrefix(got, "sleep 0.3") {
		t.Errorf("script does not start with 'sleep 0.3': %s", got)
	}
}

// TestCleanupScript_BranchDeleteHasDDash is the exploit test for V3-B.
// The generated script must contain "branch -d --" so that a branch name
// beginning with "-" cannot be parsed as a flag by git.
// This test MUST FAIL on un-fixed code (which emits "branch -d" without "--").
func TestCleanupScript_BranchDeleteHasDDash(t *testing.T) {
	o := fullOpts()
	got := CleanupScript(o, 1700000000, "v3b")
	if !strings.Contains(got, "branch -d --") {
		t.Errorf("V3-B exploit: script must contain 'branch -d --'; got: %s", got)
	}
}
