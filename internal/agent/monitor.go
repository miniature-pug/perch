// internal/agent/monitor.go
package agent

import (
	"context"
	"fmt"

	modelpkg "github.com/miniature-pug/perch/internal/model"
)

type State string

const (
	StateRunning          State = "running"
	StateIdle             State = "idle"
	StateAwaitingApproval State = "awaiting-approval"
	// StateAwaitingInput means the agent is asking the USER a question or a
	// choice (claude AskUserQuestion, opencode question.asked). This is
	// distinct from awaiting-approval, which is a tool-run permission. The
	// user answers in the agent's own pane TUI. perch only shows this as a
	// glanceable signal; perch does not render the question itself.
	StateAwaitingInput State = "awaiting-input"
	StateDone          State = "done"
	StateErrored       State = "errored"
	// StateExited means the agent PROCESS is gone (a graceful /exit, or a
	// SIGKILL, OOM, or segfault), while the pane's login shell is still
	// alive, so no pty:exit event fires. The shell exit sentinel (see
	// exit_sentinel.go) reports the exit instead. StateExited is DISTINCT
	// from StateErrored: a graceful /exit must not read as a red error.
	// StateExited is terminal. The user reopens the session with the
	// existing "session ended / Reopen" control.
	StateExited State = "exited"
)

type Caps struct {
	Approvals bool `json:"approvals"`
	Attention bool `json:"attention"`
}

type Decision struct{ Allow, Always bool }

type ApprovalReq struct {
	ReqID   string `json:"reqId"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	// Input is the raw tool input shown in the approval card, with its
	// length capped. perch also stores it as the human-readable Pattern in
	// an AlwaysRule. Matching does NOT use Input. Matching uses InputHash
	// instead (the sha256 of the full, untruncated input), so that two
	// inputs sharing the same 4096-byte prefix cannot collide. This closes
	// a privilege-escalation bug.
	Input string `json:"input"`
	// InputHash is the hex-encoded sha256 of the FULL, untruncated tool
	// input. The monitor computes it before it truncates Input for display.
	// perch uses InputHash as the authoritative always-rule match key in
	// maybeAutoApprove.
	InputHash string `json:"inputHash,omitempty"`
}

// MaxApprovalInputLen caps the length of ApprovalReq.Input, so a
// pathological tool input cannot bloat an event or a persisted always-rule.
// Matching is exact, so perch caps the stored rule and the incoming request
// to the same length.
const MaxApprovalInputLen = 4096

type Event struct {
	WorkspaceID string       `json:"workspaceId"`
	Kind        string       `json:"kind"`
	State       State        `json:"state,omitempty"`
	Approval    *ApprovalReq `json:"approval,omitempty"`
	Err         string       `json:"err,omitempty"`
	// SessionID holds the agent's session id, whenever the agent reports one
	// (claude SessionStart; opencode session.status). The app layer
	// persists it, so perch can resume the session on the next
	// OpenWorkspace call.
	SessionID string `json:"sessionId,omitempty"`
}

type Monitor interface {
	Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
	// PaneEnv returns extra KEY=VALUE entries to inject into the pane
	// shell's PROCESS environment at pty spawn. OpenWorkspace merges these
	// entries onto os.Environ(). PaneEnv carries the exit sentinel's
	// PERCH_EXIT_TOKEN and PERCH_EXIT_URL, so the launch line can reference
	// them by name instead of inlining the token, which the interactive
	// shell would echo. PaneEnv is empty when the monitor has no exit
	// listener. Call Prepare first: Prepare creates the listener.
	PaneEnv() []string
	// Start launches the monitor's event pump (hook-event translation for
	// claude, SSE consumption for opencode) bound to ctx. The pump runs
	// until ctx is cancelled. Call Prepare first, or no events ever flow.
	Start(ctx context.Context)
	Events() <-chan Event
	Approve(reqID string, d Decision) error
	Capabilities() Caps
	Teardown() error
	CurrentState() State      // the last observed lifecycle state, mutex-guarded; StateIdle before the first event
	LastApprovalTool() string // the tool name of the most recent PreToolUse approval request; empty if none yet
}

// IMPLEMENTATION NOTE (applies to FakeMonitor, ClaudeMonitor,
// OpencodeMonitor): every Monitor tracks two mutex-guarded fields, and each
// one updates as the monitor produces Events. The field `state State` is set
// from each emitted Event.State. CurrentState() returns it, and defaults to
// StateIdle when unset. The field `lastTool string` is set from
// Event.Approval.Tool on each approval event. LastApprovalTool() returns it.
// app.ListWorkspaces reads CurrentState(). app.Approve reads
// LastApprovalTool() to persist an AlwaysRule. These accessors are part of
// the Monitor contract.

func NewMonitor(tool string, adapter Adapter) (Monitor, error) {
	switch tool {
	case string(modelpkg.ToolClaude):
		return newClaudeMonitor(adapter), nil
	case string(modelpkg.ToolOpencode):
		return newOpencodeMonitor(adapter), nil
	default:
		return nil, fmt.Errorf("agent.NewMonitor: unknown tool %q", tool)
	}
}
