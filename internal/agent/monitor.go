// internal/agent/monitor.go
package agent

import (
	"context"
	"fmt"
)

type State string

const (
	StateRunning          State = "running"
	StateIdle             State = "idle"
	StateAwaitingApproval State = "awaiting-approval"
	StateDone             State = "done"
	StateErrored          State = "errored"
)

type Caps struct{ Approvals, Attention, Tokens bool }
type Decision struct{ Allow, Always bool }
type ApprovalReq struct{ ReqID, Tool, Summary string }

type Event struct {
	WorkspaceID string       `json:"workspaceId"`
	Kind        string       `json:"kind"`
	State       State        `json:"state"`
	Tokens      int          `json:"tokens"`
	Cost        float64      `json:"cost"`
	Approval    *ApprovalReq `json:"approval,omitempty"`
	Err         string       `json:"err,omitempty"`
}

type Monitor interface {
	Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
	// Start launches the monitor's event pump (hook-event translation for claude,
	// SSE consumption for opencode) bound to ctx. The pump runs until ctx is
	// cancelled. Must be called after Prepare or no events ever flow.
	Start(ctx context.Context)
	Events() <-chan Event
	Approve(reqID string, d Decision) error
	Capabilities() Caps
	Teardown() error
	CurrentState() State      // last observed lifecycle state (mutex-guarded); StateIdle before the first event
	LastApprovalTool() string // tool name of the most recent PreToolUse approval request ("" if none yet)
}

// IMPLEMENTATION NOTE (applies to FakeMonitor, ClaudeMonitor, OpencodeMonitor):
// every Monitor tracks two mutex-guarded fields updated as Events are produced —
// `state State` (set from each emitted Event.State; CurrentState() returns it,
// defaulting to StateIdle when unset) and `lastTool string` (set from
// Event.Approval.Tool on each approval event; LastApprovalTool() returns it).
// app.ListWorkspaces (Task 3.2) reads CurrentState(); app.Approve (Task 3.7)
// reads LastApprovalTool() to persist an AlwaysRule. These accessors are part of
// the frozen Monitor contract — Tasks 2.15/2.17/2.19 must implement them.

func NewMonitor(tool string, adapter Adapter) (Monitor, error) {
	switch tool {
	case "claude":
		return newClaudeMonitor(adapter), nil
	case "opencode":
		return newOpencodeMonitor(adapter), nil
	default:
		return nil, fmt.Errorf("agent.NewMonitor: unknown tool %q", tool)
	}
}
