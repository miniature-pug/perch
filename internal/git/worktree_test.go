package git

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/miniature-pug/perch/internal/proc"
)

// ── ValidRef ──────────────────────────────────────────────────────────────────

// TestValidRef_Rejects verifies that ValidRef rejects all inputs that git
// check-ref-format would consider invalid or that would be parsed as flags.
// These tests MUST FAIL on code that lacks ValidRef (compile error or missing function).
func TestValidRef_Rejects(t *testing.T) {
	bad := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"leading dash flag injection", "--upload-pack=x"},
		{"leading dash no-checkout", "--no-checkout"},
		{"leading dash single", "-n"},
		{"double dot", "feat..main"},
		{"double dot at start", "..main"},
		{"double dot at end", "main.."},
		{"space", "feat x"},
		{"tilde", "feat~1"},
		{"caret", "feat^"},
		{"colon", "feat:main"},
		{"question mark", "feat?"},
		{"asterisk", "feat*"},
		{"open bracket", "feat[x"},
		{"backslash", `feat\x`},
		{"trailing slash", "feat/"},
		{"leading slash", "/feat"},
		{"dot lock suffix", "feat.lock"},
		{"at-brace sequence", "feat@{0}"},
		{"control char tab", "feat\tx"},
		{"trailing dot", "feat."},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidRef(tc.input); err == nil {
				t.Errorf("ValidRef(%q) = nil, want error", tc.input)
			}
		})
	}
}

// TestValidRef_Accepts verifies that ValidRef permits well-formed ref names.
func TestValidRef_Accepts(t *testing.T) {
	good := []struct {
		name  string
		input string
	}{
		{"simple", "main"},
		{"HEAD", "HEAD"},
		{"feature slash", "feature/x"},
		{"perch slug", "perch/foo-abcd1234"},
		{"version tag", "v1.2.3"},
		{"dotfile-like (leading dot in component)", ".hidden"},
		{"underscore", "feat_x"},
		{"digits", "feat123"},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidRef(tc.input); err != nil {
				t.Errorf("ValidRef(%q) = %v, want nil", tc.input, err)
			}
		})
	}
}

// ── AddWorktree flag-injection exploit tests ──────────────────────────────────

// TestAddWorktree_FlagInjection_BaseBranch is the flag-injection exploit test.
// AddWorktree must reject a base_branch value of "--upload-pack=x" or
// "--no-checkout" BEFORE it issues any git argv. FakeRunner.Calls must be
// empty on rejection. This test MUST FAIL on un-fixed code (AddWorktree
// would call git with the bad value).
func TestAddWorktree_FlagInjection_BaseBranch(t *testing.T) {
	exploits := []string{
		"--upload-pack=x",
		"--no-checkout",
	}
	for _, exploit := range exploits {
		t.Run(exploit, func(t *testing.T) {
			r := proc.NewFakeRunner()
			// No canned response registered. If something calls git, it returns an error
			// from the FakeRunner. But the guard must fire BEFORE any call.
			err := AddWorktree(context.Background(), r,
				"/repos/proj", "perch/feat-x", "/repos/proj__worktrees/feat-x", exploit)
			if err == nil {
				t.Fatalf("AddWorktree with base=%q must return an error, got nil", exploit)
			}
			if len(r.Calls) != 0 {
				t.Errorf("AddWorktree with base=%q must not issue any git call; got %d call(s): %+v",
					exploit, len(r.Calls), r.Calls)
			}
		})
	}
}

// TestAddWorktree_ValidBase_StillWorks verifies that AddWorktree accepts
// valid base values ("main", "HEAD") and calls git worktree add normally.
func TestAddWorktree_ValidBase_StillWorks(t *testing.T) {
	validBases := []string{"main", "HEAD"}
	for _, base := range validBases {
		t.Run(base, func(t *testing.T) {
			r := proc.NewFakeRunner()
			wantArgs := []string{"-C", "/repos/proj", "worktree", "add", "-b", "perch/feat-x", "--", "/repos/proj__worktrees/feat-x", base}
			r.Respond(proc.FakeResult{}, "git", wantArgs...)

			err := AddWorktree(context.Background(), r,
				"/repos/proj", "perch/feat-x", "/repos/proj__worktrees/feat-x", base)
			if err != nil {
				t.Fatalf("AddWorktree with valid base=%q returned unexpected error: %v", base, err)
			}
			// One branch-existence probe, then the worktree add.
			if len(r.Calls) != 2 {
				t.Fatalf("want exactly 2 git calls for valid base=%q, got %d", base, len(r.Calls))
			}
		})
	}
}

// TestAddWorktree_FlagInjection_Branch verifies that a branch value beginning
// with "-" is also rejected before any git call.
func TestAddWorktree_FlagInjection_Branch(t *testing.T) {
	r := proc.NewFakeRunner()
	err := AddWorktree(context.Background(), r,
		"/repos/proj", "--evil-branch", "/repos/proj__worktrees/x", "HEAD")
	if err == nil {
		t.Fatal("AddWorktree with leading-dash branch must error")
	}
	if len(r.Calls) != 0 {
		t.Errorf("AddWorktree with invalid branch must not call git; got %d call(s)", len(r.Calls))
	}
}

// ── SlugifyBranch ─────────────────────────────────────────────────────────────

func TestSlugifyBranch(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// Slash mapped to dash.
		{"feat/aligner", "feat-aligner"},
		// Uppercase lowercased, space mapped to dash.
		{"Feat/Bar Baz", "feat-bar-baz"},
		// Leading/trailing dashes trimmed, consecutive dashes collapsed.
		{"--weird--", "weird"},
		// Empty input → sentinel.
		{"", "worktree"},
		// Unicode/symbol: multibyte runes collapsed to one dash each.
		{"feat/énigme@2.0", "feat-nigme-2.0"},
		// All safe chars preserved.
		{"v1.2_patch-3", "v1.2_patch-3"},
		// Only stripped chars → sentinel.
		{"///@@@", "worktree"},
	}

	for _, tt := range tests {
		got := SlugifyBranch(tt.in)
		if got != tt.want {
			t.Errorf("SlugifyBranch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── WorktreePath ──────────────────────────────────────────────────────────────

func TestWorktreePath(t *testing.T) {
	tests := []struct {
		name        string
		projectRoot string
		handle      string
		worktreeDir string
		want        string
		wantErr     bool
	}{
		{
			name:        "default sibling",
			projectRoot: "/home/me/proj",
			handle:      "aligner",
			worktreeDir: "",
			want:        "/home/me/proj__worktrees/aligner",
		},
		{
			name:        "relative worktreeDir",
			projectRoot: "/home/me/proj",
			handle:      "aligner",
			worktreeDir: "../wt",
			want:        "/home/me/wt/aligner",
		},
		{
			name:        "absolute worktreeDir",
			projectRoot: "/home/me/proj",
			handle:      "aligner",
			worktreeDir: "/srv/wt",
			want:        "/srv/wt/aligner",
		},
		{
			name:        "empty projectRoot is an error",
			projectRoot: "",
			handle:      "aligner",
			worktreeDir: "",
			wantErr:     true,
		},
		{
			name:        "empty handle is an error",
			projectRoot: "/home/me/proj",
			handle:      "",
			worktreeDir: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WorktreePath(tt.projectRoot, tt.handle, tt.worktreeDir)
			if tt.wantErr {
				if err == nil {
					t.Errorf("want error, got nil (path=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("WorktreePath(%q,%q,%q) = %q, want %q",
					tt.projectRoot, tt.handle, tt.worktreeDir, got, tt.want)
			}
		})
	}
}

// ── AddWorktree ───────────────────────────────────────────────────────────────

func TestAddWorktree_CallArgs(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		wantArgs []string
	}{
		{
			name:     "base HEAD",
			base:     "HEAD",
			wantArgs: []string{"-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "--", "/repos/proj__worktrees/feat-x", "HEAD"},
		},
		{
			name:     "base main",
			base:     "main",
			wantArgs: []string{"-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "--", "/repos/proj__worktrees/feat-x", "main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := proc.NewFakeRunner()
			r.Respond(proc.FakeResult{}, "git", tt.wantArgs...)

			err := AddWorktree(context.Background(), r, "/repos/proj", "feat-x",
				"/repos/proj__worktrees/feat-x", tt.base)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(r.Calls) != 2 {
				t.Fatalf("want 2 calls (branch probe, worktree add), got %d", len(r.Calls))
			}
			probe := proc.Call{Name: "git", Args: []string{"-C", "/repos/proj", "rev-parse", "--verify", "--quiet", "refs/heads/feat-x"}}
			if !reflect.DeepEqual(r.Calls[0], probe) {
				t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], probe)
			}
			want := proc.Call{Name: "git", Args: tt.wantArgs}
			if !reflect.DeepEqual(r.Calls[1], want) {
				t.Errorf("Calls[1] = %+v, want %+v", r.Calls[1], want)
			}
		})
	}
}

func TestAddWorktree_BranchExists(t *testing.T) {
	// The probe finds refs/heads/feat-x, so AddWorktree never runs worktree add.
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("abc123\n")},
		"git", "-C", "/repos/proj", "rev-parse", "--verify", "--quiet", "refs/heads/feat-x")

	err := AddWorktree(context.Background(), r, "/repos/proj", "feat-x",
		"/repos/proj__worktrees/feat-x", "HEAD")

	if !errors.Is(err, ErrBranchExists) {
		t.Errorf("errors.Is(err, ErrBranchExists) = false; err = %v", err)
	}
	if len(r.Calls) != 1 {
		t.Errorf("want only the probe call, got %+v", r.Calls)
	}
}

func TestAddWorktree_BranchCreatedConcurrently(t *testing.T) {
	// The probe misses the branch, but git then reports it exists: another
	// process created it in between. That branch is not ours to delete.
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"git", "-C", "/repos/proj", "rev-parse", "--verify", "--quiet", "refs/heads/feat-x")
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: a branch named 'feat-x' already exists"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "--", "/repos/proj__worktrees/feat-x", "HEAD")

	err := AddWorktree(context.Background(), r, "/repos/proj", "feat-x",
		"/repos/proj__worktrees/feat-x", "HEAD")
	if !errors.Is(err, ErrBranchExists) {
		t.Errorf("errors.Is(err, ErrBranchExists) = false; err = %v", err)
	}
	for _, c := range r.Calls {
		if len(c.Args) > 2 && c.Args[2] == "branch" {
			t.Errorf("AddWorktree must not delete a branch it did not create: %+v", c)
		}
	}
}
