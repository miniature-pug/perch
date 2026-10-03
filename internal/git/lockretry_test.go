// internal/git/lockretry_test.go
package git_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

// lockContendingRunner delegates every call to a real ExecRunner except the
// mutating `git apply` (RunStdin), which it fails with an index.lock-contention
// stderr for the first failUntil attempts, then lets succeed. This models the
// agent briefly holding .git/index.lock while the UI stages a hunk.
type lockContendingRunner struct {
	inner     proc.Runner
	failUntil int

	mu      sync.Mutex
	applies int
}

func (r *lockContendingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return r.inner.Run(ctx, name, args...)
}

func (r *lockContendingRunner) RunInDir(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	return r.inner.RunInDir(ctx, dir, name, args...)
}

func (r *lockContendingRunner) RunStdin(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	r.mu.Lock()
	r.applies++
	n := r.applies
	r.mu.Unlock()
	if n <= r.failUntil {
		stderr := []byte("fatal: Unable to create '/repo/.git/index.lock': File exists.\n")
		return nil, stderr, proc.FakeExitError{Code: 128}
	}
	return r.inner.RunStdin(ctx, dir, stdin, name, args...)
}

// TestStageHunk_RetriesOnIndexLockContention proves StageHunk retries while the
// index lock is held and eventually succeeds once it clears.
func TestStageHunk_RetriesOnIndexLockContention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := &lockContendingRunner{inner: proc.ExecRunner{}, failUntil: 3}
	ctx := context.Background()

	if err := stageAt(ctx, r, repo, "target.txt", 0); err != nil {
		t.Fatalf("StageHunk should have succeeded after lock cleared: %v", err)
	}
	if r.applies <= r.failUntil {
		t.Fatalf("expected more than %d apply attempts, got %d", r.failUntil, r.applies)
	}

	// The staged change must actually be present in the index.
	cachedOut, _, runErr := r.Run(ctx, "git", "-C", repo, "diff", "--cached", "--", "target.txt")
	if runErr != nil {
		t.Fatalf("git diff --cached: %v", runErr)
	}
	if len(cachedOut) == 0 {
		t.Fatal("expected a staged hunk in the index, found none")
	}
}

// TestDiscardHunk_RetriesOnIndexLockContention mirrors the stage test for the
// reverse-apply path.
func TestDiscardHunk_RetriesOnIndexLockContention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := &lockContendingRunner{inner: proc.ExecRunner{}, failUntil: 2}
	ctx := context.Background()

	if err := discardAt(ctx, r, repo, "target.txt", 0); err != nil {
		t.Fatalf("DiscardHunk should have succeeded after lock cleared: %v", err)
	}
	if r.applies <= r.failUntil {
		t.Fatalf("expected more than %d apply attempts, got %d", r.failUntil, r.applies)
	}
}

// TestStageHunk_ExhaustsRetriesThenFails proves the retry budget is bounded: a
// lock that never clears surfaces the error rather than looping forever.
func TestStageHunk_ExhaustsRetriesThenFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	// failUntil is larger than the retry budget, so every attempt fails.
	r := &lockContendingRunner{inner: proc.ExecRunner{}, failUntil: 1000}
	ctx := context.Background()
	git.SetLockRetryBudgetForTest(t, time.Second)

	start := time.Now()
	err := stageAt(ctx, r, repo, "target.txt", 0)
	if err == nil {
		t.Fatal("StageHunk should fail when the lock never clears")
	}
	// The budget is time-based; assert we stopped near it rather than hanging.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("retry loop took too long: %v", elapsed)
	}
	if r.applies < 2 {
		t.Fatalf("expected several apply attempts within the budget, got %d", r.applies)
	}
}

// TestStageHunk_LockRetryOutlastsAttemptCount covers GFS-26: a lock held for
// longer than the old fixed attempt budget (8 tries, about 3 s) by a
// pre-commit hook must still be waited out, because the budget is time-based.
func TestStageHunk_LockRetryOutlastsAttemptCount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	// 10 failed attempts take about 0.05+0.1+0.2+0.4+0.8*6 = 5.55 s.
	r := &lockContendingRunner{inner: proc.ExecRunner{}, failUntil: 10}
	if err := stageAt(context.Background(), r, repo, "target.txt", 0); err != nil {
		t.Fatalf("StageHunk should outlast a lock held for ~5 s: %v", err)
	}
}

// TestStageHunk_NonLockErrorDoesNotRetry proves a non-contention error returns
// immediately (exactly one apply attempt).
func TestStageHunk_NonLockErrorDoesNotRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := twoHunkFile(t)
	r := &nonLockFailRunner{inner: proc.ExecRunner{}}
	ctx := context.Background()

	if err := stageAt(ctx, r, repo, "target.txt", 0); err == nil {
		t.Fatal("StageHunk should surface the non-lock error")
	}
	if r.applies != 1 {
		t.Fatalf("expected exactly 1 apply attempt for a non-lock error, got %d", r.applies)
	}
}

type nonLockFailRunner struct {
	inner   proc.Runner
	applies int
}

func (r *nonLockFailRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return r.inner.Run(ctx, name, args...)
}

func (r *nonLockFailRunner) RunInDir(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	return r.inner.RunInDir(ctx, dir, name, args...)
}

func (r *nonLockFailRunner) RunStdin(_ context.Context, _ string, _ []byte, _ string, _ ...string) ([]byte, []byte, error) {
	r.applies++
	return nil, []byte("error: patch does not apply\n"), proc.FakeExitError{Code: 1}
}
