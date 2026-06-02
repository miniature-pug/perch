package worktree

import "context"

// DeferredRemove is a no-op stub. Teardown is handled by the registry (Phase 3).
func DeferredRemove(_ context.Context, _ string, _ string, _ int64, _ string) error {
	return nil
}
