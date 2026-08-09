// opencode_budget_test.go: white-box guard (package agent) for the opencode
// attach-timing budget. Kept internal so it can read the unexported deadline and
// poll constants directly.
package agent

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestOpencodeServePollBudgetCoversConnectDeadline is the regression guard for the
// attach-timing gap: the shell readiness poll baked into the launch incantation and
// the monitor's firstConnectDeadline must share ONE budget. Before the fix the poll
// gave up at ~10s while the monitor waited 30s, so a serve that bound between the
// poll budget and the deadline left the poll to exec `attach` into a not-yet-bound
// server (attach makes a single non-retrying connection and exits 1), killing the
// pane with no error. With the poll count derived from firstConnectDeadline, a serve
// binding at ~15s (< the 30s deadline) is still inside the poll budget, so attach
// runs against a live server and the pane is not killed early.
func TestOpencodeServePollBudgetCoversConnectDeadline(t *testing.T) {
	iters, err := strconv.Atoi(opencodeServePollMaxIters)
	if err != nil {
		t.Fatalf("poll iteration count is not an integer: %q (%v)", opencodeServePollMaxIters, err)
	}
	if iters <= 0 {
		t.Fatalf("poll iteration count must be positive, got %d", iters)
	}

	budget := time.Duration(iters) * opencodeServePollInterval
	if budget < firstConnectDeadline {
		t.Fatalf("shell readiness-poll budget %v < firstConnectDeadline %v: a serve that binds "+
			"between the poll budget and the deadline exec's attach into a dead server and kills "+
			"the pane with no error", budget, firstConnectDeadline)
	}

	// The finding's concrete case: a serve that binds at ~15s is inside the 30s
	// connect deadline, so it MUST also be inside the poll budget (else the poll
	// abandons a serve that would have come up, killing the pane before the deadline).
	const bindAt = 15 * time.Second
	if bindAt < firstConnectDeadline && budget < bindAt {
		t.Fatalf("a serve binding at %v is within the %v connect deadline but OUTSIDE the %v poll "+
			"budget — the pane would be killed before the deadline", bindAt, firstConnectDeadline, budget)
	}
}

// TestOpencodeServePollIntervalSecFormat guards the shell-token format of the
// interval: it must be a bare decimal `sleep` argument (e.g. "0.2"), not Go's
// duration string ("200ms"), which /bin/sh's sleep does not accept.
func TestOpencodeServePollIntervalSecFormat(t *testing.T) {
	if strings.ContainsAny(opencodeServePollIntervalSec, "ms µn h") {
		t.Fatalf("interval token %q is not a bare decimal seconds value for shell `sleep`",
			opencodeServePollIntervalSec)
	}
	secs, err := strconv.ParseFloat(opencodeServePollIntervalSec, 64)
	if err != nil {
		t.Fatalf("interval token %q does not parse as a float: %v", opencodeServePollIntervalSec, err)
	}
	if want := opencodeServePollInterval.Seconds(); secs != want {
		t.Errorf("interval token = %v seconds, want %v", secs, want)
	}
}
