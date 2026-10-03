package git

import (
	"testing"
	"time"
)

// SetLockRetryBudgetForTest shortens the index-lock retry budget for the
// duration of t, so tests of an exhausted budget do not wait the full
// production value.
func SetLockRetryBudgetForTest(t *testing.T, d time.Duration) {
	t.Helper()
	old := lockRetryBudget
	lockRetryBudget = d
	t.Cleanup(func() { lockRetryBudget = old })
}
