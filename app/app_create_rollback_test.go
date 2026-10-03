//go:build integration

// app_create_rollback_test.go: integration guard for CreateWorkspace's
// worktree rollback on a persist failure. This test uses real git against a
// throwaway repo. The app package's real-git tests carry the integration
// tag. Plain unit tests use fakes.
package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitpkg "github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/registry"
)

// TestCreateWorkspace_RollsBackWorktreeOnPersistFailure is the regression
// guard for the orphaned-worktree bug. When store.Upsert fails after `git
// worktree add -b` creates the tree and branch, CreateWorkspace must roll
// back the just-created worktree and the new branch. This way nothing is
// orphaned, and a retry does not hit ErrBranchExists forever. The test
// injects the persist failure by making workspaces.json a directory, so the
// store's atomic rename fails.
func TestCreateWorkspace_RollsBackWorktreeOnPersistFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// ── real git repo under a configured root ────────────────────────────────
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t",
			"commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// ── registry + App ───────────────────────────────────────────────────────
	cfgDir := t.TempDir()
	store, err := registry.Load(cfgDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	a := NewApp(store, []string{root})
	a.settingsPath = filepath.Join(cfgDir, "settings.json")
	a.layoutPath = filepath.Join(cfgDir, "layout.json")

	// ── inject the persist failure: make workspaces.json a DIRECTORY so the
	//    store's atomic temp-file-to-rename flush cannot succeed. ─────────────
	if err := os.Mkdir(filepath.Join(cfgDir, "workspaces.json"), 0o755); err != nil {
		t.Fatalf("inject upsert failure: %v", err)
	}

	const branch = "feat/rollback"
	wantTree, err := gitpkg.WorktreePath(repo, gitpkg.SlugifyBranch(branch), "")
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}

	// ── CreateWorkspace: new-branch mode (baseRef "main") uses git worktree add -b ──
	_, err = a.CreateWorkspace("claude", repo, "main", branch, "", true)
	if err == nil {
		t.Fatal("CreateWorkspace must fail when the store cannot persist the record")
	}
	// The surfaced error must be the persist failure, not masked by rollback.
	if got := err.Error(); !strings.Contains(got, "persist workspace") {
		t.Errorf("error %q does not surface the persist failure", got)
	}

	// ── assertion 1: the worktree dir must NOT be orphaned on disk ────────────
	if _, statErr := os.Stat(wantTree); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("worktree %q was not rolled back (stat err = %v); orphaned tree left behind", wantTree, statErr)
	}

	// ── assertion 2: the new branch must be gone (else a retry hits ErrBranchExists) ──
	// `git rev-parse --verify --quiet refs/heads/<branch>` exits non-zero when the
	// branch does not exist.
	if out, verifyErr := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet",
		"refs/heads/"+branch).Output(); verifyErr == nil {
		t.Errorf("branch %q still exists after rollback (rev-parse gave %q); a retry would hit ErrBranchExists forever",
			branch, strings.TrimSpace(string(out)))
	}

	// ── assertion 3: no phantom registry record references the removed tree ───
	if id, found := a.workspaceForBranch(repo, branch); found {
		t.Errorf("phantom workspace record %q still tracks branch %q after a failed create", id, branch)
	}

	// ── assertion 4 (retry-safe): fix the store, retry the SAME branch, succeed ──
	if err := os.RemoveAll(filepath.Join(cfgDir, "workspaces.json")); err != nil {
		t.Fatalf("clear injected failure: %v", err)
	}
	vm, err := a.CreateWorkspace("claude", repo, "main", branch, "", true)
	if err != nil {
		t.Fatalf("retry after rollback must succeed (branch was cleaned), got: %v", err)
	}
	if _, statErr := os.Stat(vm.WorktreePath); statErr != nil {
		t.Errorf("retry worktree %q not present: %v", vm.WorktreePath, statErr)
	}
	t.Cleanup(func() { _ = a.CloseWorkspace(vm.ID) })
}
