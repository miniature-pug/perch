// opencode_budget_test.go is a white-box guard (package agent) for the
// opencode attach-timing budget. It stays internal to the package, so it can
// read the unexported deadline and poll constants directly.
package agent

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestOpencodeServePollBudgetCoversConnectDeadline is the regression guard
// for the attach-timing gap. The shell readiness poll baked into the launch
// incantation, and the monitor's firstConnectDeadline, must share ONE
// budget. Before the fix, the poll gave up at about 10 seconds while the
// monitor waited 30 seconds. So a serve that bound between the poll budget
// and the deadline left the poll to exec `attach` into a not-yet-bound
// server. attach makes a single, non-retrying connection and exits 1, which
// kills the pane with no error. Now the poll count derives from
// firstConnectDeadline. A serve that binds at about 15 seconds, inside the
// 30-second deadline, is still inside the poll budget. So attach runs
// against a live server, and the pane is not killed early.
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

	// The concrete case behind this finding: a serve that binds at about 15
	// seconds is inside the 30-second connect deadline. So it MUST also be
	// inside the poll budget. Otherwise the poll abandons a serve that would
	// have come up, and kills the pane before the deadline.
	const bindAt = 15 * time.Second
	if bindAt < firstConnectDeadline && budget < bindAt {
		t.Fatalf("a serve binding at %v is within the %v connect deadline but OUTSIDE the %v poll "+
			"budget — the pane would be killed before the deadline", bindAt, firstConnectDeadline, budget)
	}
}

// TestOpencodeServePollIntervalSecFormat guards the shell-token format of
// the interval. The interval must be a bare decimal `sleep` argument, for
// example "0.2". It must NOT be Go's duration string, for example "200ms",
// because /bin/sh's sleep does not accept that format.
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
