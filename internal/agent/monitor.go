// internal/agent/monitor.go
package agent

import (
	"context"
	"fmt"

	modelpkg "github.com/Miniature-Pug/perch/internal/model"
)

type State string

const (
	StateRunning          State = "running"
	StateIdle             State = "idle"
	StateAwaitingApproval State = "awaiting-approval"
	// StateAwaitingInput: the agent is asking the USER a question/choice (claude
	// AskUserQuestion, opencode question.asked) — distinct from awaiting-approval
	// (a tool-run permission). The user answers in the agent's own pane TUI; perch
	// only surfaces it as a glanceable signal, it does not render the question.
	StateAwaitingInput State = "awaiting-input"
	StateDone          State = "done"
	StateErrored       State = "errored"
	// StateExited: the agent PROCESS is gone (graceful /exit, or SIGKILL/OOM/
	// segfault) while the pane's login shell is still alive — so no pty:exit fires.
	// The shell exit sentinel (see exit_sentinel.go) reports it. DISTINCT from
	// StateErrored: a graceful /exit must not read as a red error. It is terminal;
	// the session is reopened via the existing "session ended / Reopen" affordance.
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
	// Input is the (length-capped) raw tool input shown in the approval card
	// and stored as the human-readable Pattern in an AlwaysRule.
	// Matching is NOT done on Input — it is done on InputHash (sha256 of the
	// full, untruncated input) so that two inputs sharing the same 4096-byte
	// prefix cannot collide (a privilege-escalation fix).
	Input string `json:"input"`
	// InputHash is the hex-encoded sha256 of the FULL (untruncated) tool input.
	// Computed by the monitor before truncating Input for display. Used as the
	// authoritative always-rule match key in maybeAutoApprove.
	InputHash string `json:"inputHash,omitempty"`
}

// MaxApprovalInputLen caps the ApprovalReq.Input length so a pathological tool
// input cannot bloat an event or a persisted always-rule. Matching is exact, so
// both the stored rule and the incoming request are capped identically.
const MaxApprovalInputLen = 4096

type Event struct {
	WorkspaceID string       `json:"workspaceId"`
	Kind        string       `json:"kind"`
	State       State        `json:"state,omitempty"`
	Approval    *ApprovalReq `json:"approval,omitempty"`
	Err         string       `json:"err,omitempty"`
	// SessionID is populated whenever the agent reports its session id (claude
	// SessionStart; opencode session.status) so the app layer can persist it for
	// resume on the next OpenWorkspace.
	SessionID string `json:"sessionId,omitempty"`
}

type Monitor interface {
	Prepare(ctx context.Context, workspaceID, cwd, resumeID string) (launchCmd string, err error)
	// PaneEnv returns extra KEY=VALUE entries to inject into the pane shell's
	// PROCESS environment at pty spawn (merged onto os.Environ() by OpenWorkspace).
	// It carries the exit sentinel's PERCH_EXIT_TOKEN/PERCH_EXIT_URL so the launch
	// line can reference them by name instead of inlining the token (which the
	// interactive shell would echo). Empty when the monitor has no exit listener.
	// Must be called AFTER Prepare (which creates the listener).
	PaneEnv() []string
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
// app.ListWorkspaces reads CurrentState(); app.Approve reads LastApprovalTool()
// to persist an AlwaysRule. These accessors are part of the Monitor contract.

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
