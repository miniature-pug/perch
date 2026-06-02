package resurrect

import "context"

// Deps stub — tmux and state removed; package deleted in Task 0.2.
type Deps struct{}

// Report stub.
type Report struct {
	Restored []string
	Pruned   []string
	Kept     []string
	Skipped  []struct{ PaneKey, Tree, Reason string }
}

// Reconcile stub — always returns an empty report.
func Reconcile(_ context.Context, _ Deps) (Report, error) { return Report{}, nil }
