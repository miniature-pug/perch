package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

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
			wantArgs: []string{"-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "/repos/proj__worktrees/feat-x", "HEAD"},
		},
		{
			name:     "base main",
			base:     "main",
			wantArgs: []string{"-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "/repos/proj__worktrees/feat-x", "main"},
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

			if len(r.Calls) != 1 {
				t.Fatalf("want 1 call, got %d", len(r.Calls))
			}
			want := proc.Call{Name: "git", Args: tt.wantArgs}
			if !reflect.DeepEqual(r.Calls[0], want) {
				t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], want)
			}
		})
	}
}

func TestAddWorktree_BranchExists(t *testing.T) {
	// Real git stderr: "fatal: a branch named 'feat/test' already exists"
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: a branch named 'feat-x' already exists"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "add", "-b", "feat-x", "/repos/proj__worktrees/feat-x", "HEAD")

	err := AddWorktree(context.Background(), r, "/repos/proj", "feat-x",
		"/repos/proj__worktrees/feat-x", "HEAD")

	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrBranchExists) {
		t.Errorf("errors.Is(err, ErrBranchExists) = false; err = %v", err)
	}
}

// ── RemoveWorktree ────────────────────────────────────────────────────────────

func TestRemoveWorktree_CleanCall(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := RemoveWorktree(context.Background(), r, "/repos/proj", "/repos/proj__worktrees/feat-x", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := proc.Call{Name: "git", Args: wantArgs}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], want)
	}
}

func TestRemoveWorktree_ForceCall(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x", "--force"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := RemoveWorktree(context.Background(), r, "/repos/proj", "/repos/proj__worktrees/feat-x", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := proc.Call{Name: "git", Args: wantArgs}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], want)
	}
}

func TestRemoveWorktree_DirtyNoForce(t *testing.T) {
	// Real git stderr: "fatal: '...' contains modified or untracked files, use --force to delete it"
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: '/repos/proj__worktrees/feat-x' contains modified or untracked files, use --force to delete it"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x")

	err := RemoveWorktree(context.Background(), r, "/repos/proj", "/repos/proj__worktrees/feat-x", false)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrWorktreeDirty) {
		t.Errorf("errors.Is(err, ErrWorktreeDirty) = false; err = %v", err)
	}
}

func TestRemoveWorktree_LockedNoForce(t *testing.T) {
	// Real git stderr: "fatal: cannot remove a locked working tree;\nuse 'remove -f -f' to override or unlock first"
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: cannot remove a locked working tree;\nuse 'remove -f -f' to override or unlock first"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x")

	err := RemoveWorktree(context.Background(), r, "/repos/proj", "/repos/proj__worktrees/feat-x", false)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrWorktreeDirty) {
		t.Errorf("errors.Is(err, ErrWorktreeDirty) = false for locked stderr; err = %v", err)
	}
}

func TestRemoveWorktree_ForceDoesNotClassifyDirty(t *testing.T) {
	// When force=true, dirty stderr must NOT yield ErrWorktreeDirty — wrap verbatim.
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: some other error with locked in message"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "remove", "/repos/proj__worktrees/feat-x", "--force")

	err := RemoveWorktree(context.Background(), r, "/repos/proj", "/repos/proj__worktrees/feat-x", true)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrWorktreeDirty) {
		t.Errorf("force=true must not classify error as ErrWorktreeDirty; err = %v", err)
	}
}

// ── PruneWorktrees ────────────────────────────────────────────────────────────

func TestPruneWorktrees_Call(t *testing.T) {
	r := proc.NewFakeRunner()
	wantArgs := []string{"-C", "/repos/proj", "worktree", "prune"}
	r.Respond(proc.FakeResult{}, "git", wantArgs...)

	err := PruneWorktrees(context.Background(), r, "/repos/proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(r.Calls))
	}
	want := proc.Call{Name: "git", Args: wantArgs}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Errorf("Calls[0] = %+v, want %+v", r.Calls[0], want)
	}
}

func TestPruneWorktrees_RunnerError(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{
		Stderr: []byte("fatal: not a git repository"),
		Err:    proc.FakeExitError{Code: 128},
	}, "git", "-C", "/repos/proj", "worktree", "prune")

	err := PruneWorktrees(context.Background(), r, "/repos/proj")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// ── RemoveLock ────────────────────────────────────────────────────────────────

func TestRemoveLock_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Construct fake repo structure but don't create the locked file.
	internalName := "feat-x"
	worktreesDir := filepath.Join(dir, ".git", "worktrees", internalName)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := RemoveLock(dir, internalName)
	if err != nil {
		t.Errorf("want nil for missing file, got %v", err)
	}
}

func TestRemoveLock_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	internalName := "feat-x"
	worktreesDir := filepath.Join(dir, ".git", "worktrees", internalName)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	lockPath := filepath.Join(worktreesDir, "locked")
	if err := os.WriteFile(lockPath, []byte("reason"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := RemoveLock(dir, internalName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(lockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("locked file should be gone after RemoveLock")
	}
}

// ── InternalName ──────────────────────────────────────────────────────────────

func TestInternalName_FromGitFile(t *testing.T) {
	dir := t.TempDir()
	treePath := filepath.Join(dir, "feat-x-wt")
	if err := os.Mkdir(treePath, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Write a .git pointer file as git does for linked worktrees.
	content := "gitdir: /abs/.git/worktrees/feat-x\n"
	if err := os.WriteFile(filepath.Join(treePath, ".git"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	name, err := InternalName(treePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "feat-x" {
		t.Errorf("InternalName = %q, want %q", name, "feat-x")
	}
}

func TestInternalName_MissingGitFile(t *testing.T) {
	dir := t.TempDir()
	treePath := filepath.Join(dir, "mywt")
	if err := os.Mkdir(treePath, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// No .git file written — should fall back to filepath.Base(treePath).

	name, err := InternalName(treePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "mywt" {
		t.Errorf("InternalName fallback = %q, want %q", name, "mywt")
	}
}

func TestInternalName_MalformedGitFile(t *testing.T) {
	dir := t.TempDir()
	treePath := filepath.Join(dir, "weirdwt")
	if err := os.Mkdir(treePath, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Content that doesn't match "gitdir: ..." prefix.
	if err := os.WriteFile(filepath.Join(treePath, ".git"), []byte("not a gitdir pointer"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	name, err := InternalName(treePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "weirdwt" {
		t.Errorf("InternalName malformed fallback = %q, want %q", name, "weirdwt")
	}
}
