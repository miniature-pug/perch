package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// launchSpec carries the parameters needed to open or resume an agent session.
type launchSpec struct {
	tool        string
	sessionID   string // non-empty for resume; empty for new opencode
	branch      string // branch name → WindowName basis
	treePath    string // absolute working dir — Launch dir + SaveWindow.Tree
	projectPath string // absolute repo root — SessionName + frecency key
	resume      bool
	fork        bool // fork an existing session into spec.treePath (vs resume in place)
}

// launchedMsg is delivered after the blocking launch work completes.
type launchedMsg struct {
	session string
	window  string
	pane    string // pane id of the newly created pane (non-empty on success)
	err     error
}

// switchedMsg is delivered after SwitchClient completes (inside-tmux path).
type switchedMsg struct{ err error }

// attachFinishedMsg is delivered after tea.ExecProcess completes (outside-tmux path).
type attachFinishedMsg struct{ err error }

// adapterFor returns the Adapter for the given tool string.
// Returns nil,false for unknown tool values.
func adapterFor(tool string) (agent.Adapter, bool) {
	switch model.Tool(tool) {
	case model.ToolClaude:
		return agent.NewClaude(), true
	case model.ToolOpencode:
		return agent.NewOpencode(), true
	default:
		return nil, false
	}
}

// newSessionID generates a random v4 UUID string. Used so claude-new sessions
// get a perch-assigned id, closing the concurrent-resume hazard: without it a
// freshly launched session would appear idle on the next load and ↵ would
// relaunch it rather than switching to the live pane.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("newSessionID: %w", err)
	}
	// Set version 4 and variant bits (RFC 4122).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// launchCmd returns a tea.Cmd that performs the full blocking launch off the
// UI goroutine and delivers a launchedMsg when done.
func (m Model) launchCmd(spec launchSpec) tea.Cmd {
	if m.loader == nil {
		return func() tea.Msg { return launchedMsg{err: fmt.Errorf("no loader configured")} }
	}
	l := m.loader
	return func() tea.Msg {
		// Program-scoped ctx so an in-flight launch cancels when the TUI exits;
		// nil in tests → context.Background() (matches load() and previewCmd).
		ctx := l.ctx
		if ctx == nil {
			ctx = context.Background()
		}

		adapter, ok := adapterFor(spec.tool)
		if !ok {
			return launchedMsg{err: fmt.Errorf("unknown tool: %q", spec.tool)}
		}

		var sid string
		var argv []string
		if spec.fork {
			forkArgs, ferr := adapter.ForkInto(spec.sessionID, spec.treePath)
			if errors.Is(ferr, agent.ErrForkUnsupported) {
				// opencode has no native fork → start a fresh session; opencode
				// assigns its own id so sid stays "" and @perch_session is not stamped.
				argv = append([]string{adapter.Name()}, adapter.NewArgs(agent.NewOpts{})...)
			} else if ferr != nil {
				return launchedMsg{err: ferr}
			} else {
				// claude forks natively and honours --session-id, so pre-mint the
				// forked id: it is known at launch, stamped into @perch_session, and
				// written to the shadow record — the correct session is tracked from
				// the first load cycle (D6).
				var err error
				sid, err = newSessionID()
				if err != nil {
					return launchedMsg{err: err}
				}
				argv = append([]string{adapter.Name()}, forkArgs...)
				argv = append(argv, adapter.NewArgs(agent.NewOpts{SessionID: sid})...)
			}
		} else if spec.resume {
			sid = spec.sessionID
			argv = append([]string{adapter.Name()}, adapter.ResumeArgs(sid)...)
		} else if model.Tool(spec.tool) == model.ToolClaude {
			var err error
			sid, err = newSessionID()
			if err != nil {
				return launchedMsg{err: err}
			}
			argv = append([]string{adapter.Name()}, adapter.NewArgs(agent.NewOpts{SessionID: sid})...)
		} else {
			// opencode assigns its own session ids — do not pass one.
			argv = append([]string{adapter.Name()}, adapter.NewArgs(agent.NewOpts{})...)
		}

		session := tmux.SessionName(spec.projectPath)
		windowBasis := spec.branch
		if windowBasis == "" {
			windowBasis = filepath.Base(spec.treePath)
		}
		window := tmux.WindowName(windowBasis)

		paneID, err := l.Tmux.Launch(ctx, session, window, spec.treePath, argv)
		if err != nil {
			return launchedMsg{err: err}
		}

		if sid != "" {
			// resume always has sid; opencode-new never does (D6).
			_ = l.Tmux.SetPaneOption(ctx, paneID, "@perch_session", sid)
		}

		// Frecency bump for the project root.
		st, _ := state.LoadState(l.BaseDir)
		state.BumpProject(st.Projects, spec.projectPath, l.Now)
		state.AgeProjects(st.Projects)
		_ = state.SaveState(l.BaseDir, st)

		// Capture the live server's start_time now: an empty BootID would make M7
		// reconcile treat this window as boot-mismatched, so we record the real value.
		bootID, _ := l.Tmux.BootID(ctx)

		_ = state.SaveWindow(l.BaseDir, model.Window{
			PaneKey:     paneID,
			Tool:        model.Tool(spec.tool),
			SessionID:   sid,
			Tree:        spec.treePath,
			TmuxSession: session,
			TmuxWindow:  window,
			BootID:      bootID,
			Updated:     l.Now,
		})

		return launchedMsg{session: session, window: window, pane: paneID}
	}
}

// swapInCmd returns a tea.Cmd that displays the agent whose home pane id is
// targetHome in the frame main slot. It uses planSwapIn to compute the minimal
// op sequence, pre-sizes the agent session to the frame main dimensions (via
// PaneSize + ResizeWindow) to avoid reflow shock, then calls SwapPane for each
// op and nudges all clients to repaint with RefreshClient.
//
// Guard conditions that produce a no-op nil result (so the Update loop stays clean):
//   - loader == nil (test/scaffold mode without a loader)
//   - targetHome == "" (no target)
//   - m.swapping is already true (a swap is in-flight)
//
// When the guard passes, swapInCmd sets m.swapping = true before returning the
// Cmd, mirroring the capturing/polling guard pattern used by previewCmd and
// statusPollCmd.
func (m *Model) swapInCmd(targetHome string) tea.Cmd {
	if m.loader == nil || targetHome == "" || m.swapping {
		return func() tea.Msg { return swappedMsg{noop: true} }
	}
	ops := planSwapIn(m.displayedPaneID, m.placeholderPaneID, targetHome)
	if len(ops) == 0 {
		// targetHome == displayedPaneID → already showing.
		return func() tea.Msg { return swappedMsg{target: targetHome, noop: true} }
	}

	m.swapping = true

	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		for _, op := range ops {
			if op.src == targetHome {
				// Bring-in op: pre-size the agent session to the frame main slot
				// dimensions to avoid reflow shock on the app running inside it.
				// op.dst is the placeholder, which is currently in the frame main slot.
				if w, h, err := t.PaneSize(ctx, op.dst); err == nil {
					// Best-effort: ignore resize errors (headless server may not
					// support resize; the swap still proceeds).
					_ = t.ResizeWindow(ctx, targetHome, w, h)
				}
			}
			if err := t.SwapPane(ctx, op.src, op.dst); err != nil {
				return swappedMsg{err: err}
			}
		}
		// Auto-clear the status badge on focus (M11-3): an agent the user is now
		// looking at is no longer "waiting"/"done". The status hook re-sets it on
		// the next agent state change.
		_ = t.SetPaneOption(ctx, targetHome, "@perch_pane_status", "")
		// Nudge all clients to repaint so the newly-displayed agent reflows.
		_ = t.RefreshClient(ctx)
		return swappedMsg{target: targetHome}
	}
}

// quitFrameCmd returns a tea.Cmd that safely tears down the frame:
//  1. Swaps the displayed agent back to its home session (planSwapHome) so the
//     agent process is not killed with the frame.
//  2. Kills the frame session (KillSession).
//  3. Returns tea.QuitMsg to stop the Bubble Tea runtime.
//
// ORDER IS CRITICAL: swap-home must complete before kill-session, or the agent
// pane that currently occupies the frame main slot is destroyed with the session.
func (m Model) quitFrameCmd() tea.Cmd {
	if m.loader == nil {
		return tea.Quit
	}
	ops := planSwapHome(m.displayedPaneID, m.placeholderPaneID)
	frameSession := m.frameSession
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		// Step 1: swap displayed agent home (best-effort — if it fails the user
		// is losing data regardless; still proceed to kill the frame).
		for _, op := range ops {
			_ = t.SwapPane(ctx, op.src, op.dst)
		}
		// Step 2: kill the frame session.
		_ = t.KillSession(ctx, frameSession)
		// Step 3: stop the TUI.
		return tea.QuitMsg{}
	}
}

// attachTo switches the terminal client to target (a pre-built WindowTarget).
// Inside tmux ($TMUX set) it dispatches SwitchClient as a plain tea.Cmd;
// outside tmux it falls back to tea.ExecProcess for a full terminal handover.
func (m Model) attachTo(target string) (tea.Model, tea.Cmd) {
	if m.loader == nil {
		// No loader (test / scaffold mode): nothing to attach to.
		return m, func() tea.Msg { return switchedMsg{err: fmt.Errorf("tui: attach: no loader configured")} }
	}
	argv := m.loader.Tmux.AttachTargetArgs(target)
	if len(argv) > 0 && argv[0] == "switch-client" {
		t := m.loader.Tmux
		ctx := m.loader.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return m, func() tea.Msg {
			return switchedMsg{err: t.SwitchClient(ctx, target)}
		}
	}
	// attach-session: genuine terminal handover; use tea.ExecProcess.
	full := m.loader.Tmux.ExecArgs(argv...)
	c := exec.Command(full[0], full[1:]...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
}
