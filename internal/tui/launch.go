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
	"github.com/Miniature-Pug/perch/internal/match"
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

// resolveTool determines the agent tool for a new session following the
// config-defined priority order:
//  1. itemTool (existing session metadata) always wins when non-empty.
//  2. The first [[wildcard]] rule whose Pattern matches treePath (via **-aware globs).
//  3. The [default_session].agent / project agent from the global config.
//  4. "claude" as the unconditional final fallback.
//
// resolveTool is intentionally nil-safe: when m.cfg is nil it returns itemTool
// unchanged (non-empty) or "claude" (empty itemTool). Only affects NEW launches;
// callers must not use it for resume paths where the session's own tool is authoritative.
func (m Model) resolveTool(treePath, itemTool string) string {
	if itemTool != "" {
		return itemTool
	}
	if m.cfg != nil {
		for _, w := range m.cfg.Wildcards {
			if match.MatchAny([]string{w.Pattern}, treePath) {
				return string(w.Agent)
			}
		}
		if m.cfg.Agent != "" {
			return string(m.cfg.Agent)
		}
	}
	return "claude"
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

		// Resolve the binary path: prefer the configured absolute path for this
		// tool (from [agents] in the global config), falling back to the bare name
		// when no config is set or no entry exists for the tool.
		bin := adapter.Name()
		if m.cfg != nil {
			bin = m.cfg.AgentBinary(model.Tool(spec.tool))
		}

		var sid string
		var argv []string
		if spec.fork {
			forkArgs, ferr := adapter.ForkInto(spec.sessionID, spec.treePath)
			if errors.Is(ferr, agent.ErrForkUnsupported) {
				// opencode has no native fork → start a fresh session; opencode
				// assigns its own id so sid stays "" and @perch_session is not stamped.
				argv = append([]string{bin}, adapter.NewArgs(agent.NewOpts{})...)
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
				argv = append([]string{bin}, forkArgs...)
				argv = append(argv, adapter.NewArgs(agent.NewOpts{SessionID: sid})...)
			}
		} else if spec.resume {
			sid = spec.sessionID
			argv = append([]string{bin}, adapter.ResumeArgs(sid)...)
		} else if model.Tool(spec.tool) == model.ToolClaude {
			var err error
			sid, err = newSessionID()
			if err != nil {
				return launchedMsg{err: err}
			}
			argv = append([]string{bin}, adapter.NewArgs(agent.NewOpts{SessionID: sid})...)
		} else {
			// opencode assigns its own session ids — do not pass one.
			argv = append([]string{bin}, adapter.NewArgs(agent.NewOpts{})...)
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
			_ = l.Tmux.SetPaneOption(ctx, paneID, tmux.OptionPerchSession, sid)
		}

		// Send the startup_command to the newly launched agent pane when configured.
		// Best-effort: ignore send errors so a misconfigured command never blocks the
		// launch. The command is sent as-is (raw) so the user's shell interprets it.
		if m.cfg != nil && m.cfg.StartupCommand != "" {
			_ = l.Tmux.SendKeys(ctx, paneID, m.cfg.StartupCommand)
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

// focusAgentCmd returns a tea.Cmd that focuses the agent pane already displayed
// in the frame main slot (select-pane -t displayedPaneID). Used when the user
// presses Enter on the item that is already shown — avoiding a needless double-swap.
//
// Guard conditions (consistent with swapInCmd / closeWindowCmd):
//   - loader == nil → no-op nil cmd
//   - displayedPaneID == "" → no-op nil cmd
//
// Error handling is best-effort: SelectPane failures are silently swallowed so a
// transient tmux hiccup never blocks the focus action (mirrors RefreshClient usage).
func (m Model) focusAgentCmd() tea.Cmd {
	if m.loader == nil || m.displayedPaneID == "" {
		return nil
	}
	target := m.displayedPaneID
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		_ = t.SelectPane(ctx, target)
		return nil
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
				// PaneSize queries #{pane_height} of op.dst (the actual main pane),
				// which is already status-row-aware: tmux's pane_height excludes the
				// 1-row status bar, so h is the true usable height and no arithmetic
				// adjustment is needed.
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
		_ = t.SetPaneOption(ctx, targetHome, tmux.OptionPerchPaneStatus, "")
		// Nudge all clients to repaint so the newly-displayed agent reflows.
		_ = t.RefreshClient(ctx)
		return swappedMsg{target: targetHome}
	}
}

// swapDisplayedHome executes the planSwapHome ops for the given displayed/placeholder
// pane ids, ignoring SwapPane errors. This is the shared best-effort helper used by
// both closeWindowCmd (detach view) and quitFrameCmd (quit-time cleanup).
// A dead displayed pane produces a swap-pane error that is silently swallowed so
// neither caller is blocked by a pane that has already exited.
func swapDisplayedHome(ctx context.Context, t tmux.Tmux, displayed, placeholder string) {
	for _, op := range planSwapHome(displayed, placeholder) {
		_ = t.SwapPane(ctx, op.src, op.dst)
	}
}

// windowClosedMsg is delivered by closeWindowCmd / recoverDeadDisplayedCmd after
// the swap-home (and, for the dead path, kill-pane) attempt completes — whether
// or not the underlying tmux ops succeeded. Applying it to the model resets
// displayedPaneID to "" so the placeholder reclaims the frame main slot;
// placeholderPaneID is left untouched (the placeholder pane id is stable across
// swap-pane — only its location moves).
type windowClosedMsg struct{}

// recoverFrameDead performs the dead-displayed-pane recovery sequence on the
// given tmux server, best-effort (all errors swallowed). ORDER IS CRITICAL:
//
//  1. swap-home: the LIVE placeholder returns to the frame main slot under its
//     ORIGINAL pane id (placeholder id unchanged); this EXILES the dead displayed
//     pane into the agent's home window. Killing before this swap would destroy
//     the dead pane while it still occupies the frame main slot, stranding the
//     placeholder exiled-alive.
//  2. kill-pane the now-exiled DEAD displayed pane (ids are stable across swap, so
//     `displayed` still addresses it). Topology-safe: only that one pane is
//     removed — a shared project session keeping sibling agent windows survives.
//  3. focus the sidebar (select-pane -L): after swap-home the active pane is the
//     placeholder in the main slot; -L moves left to the sidebar, matching the F12
//     navigation binding.
func recoverFrameDead(ctx context.Context, t tmux.Tmux, displayed, placeholder string) {
	swapDisplayedHome(ctx, t, displayed, placeholder)
	_ = t.KillPane(ctx, displayed)
	_ = t.SelectPaneLeft(ctx)
}

// recoverDeadDisplayedCmd returns a tea.Cmd that recovers the frame after the
// displayed agent's process has exited (its pane is dead under remain-on-exit).
// It runs recoverFrameDead (swap-home → kill the exiled dead pane → focus sidebar)
// and returns a windowClosedMsg that resets displayedPaneID to "".
//
// Guard conditions:
//   - loader == nil → no-op (delivers windowClosedMsg immediately).
//   - displayedPaneID == "" → no-op (nothing to recover; still delivers
//     windowClosedMsg so callers need no nil check on the returned cmd).
//
// All tmux errors are swallowed (best-effort, consistent with the other frame
// cmds): the view reset must always happen so the frame is never wedged.
func (m Model) recoverDeadDisplayedCmd() tea.Cmd {
	if m.loader == nil || m.displayedPaneID == "" {
		return func() tea.Msg { return windowClosedMsg{} }
	}

	displayed := m.displayedPaneID
	placeholder := m.placeholderPaneID
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		recoverFrameDead(ctx, t, displayed, placeholder)
		return windowClosedMsg{}
	}
}

// closeWindowCmd returns a tea.Cmd that detaches the view of the currently
// displayed agent, branching on whether its pane is still alive:
//
//   - ALIVE: swap the agent pane back to its home session (so the process keeps
//     running, resumable) — NO kill. This is the normal close (esc).
//   - DEAD (agent exited while displayed, under remain-on-exit): full recovery —
//     swap home + kill the exiled dead pane + focus the sidebar. Killing only the
//     dead frame pane without swap-home would leak the orphaned-alive placeholder
//     and corrupt the agent's resume; the full recovery avoids both (M17-4).
//
// Aliveness is probed with PaneDead(displayed); a probe error is treated as ALIVE
// (fail safe: prefer keeping the agent over an unintended kill).
//
// Guard conditions:
//   - loader == nil → no-op (delivers windowClosedMsg immediately).
//   - displayedPaneID == "" → no-op (no probe, no tmux calls; still delivers
//     windowClosedMsg so callers need no nil check on the returned cmd).
//
// In all paths a windowClosedMsg is returned so the view reset always happens.
func (m Model) closeWindowCmd() tea.Cmd {
	if m.loader == nil || m.displayedPaneID == "" {
		return func() tea.Msg { return windowClosedMsg{} }
	}

	displayed := m.displayedPaneID
	placeholder := m.placeholderPaneID
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		dead, err := t.PaneDead(ctx, displayed)
		if err == nil && dead {
			recoverFrameDead(ctx, t, displayed, placeholder)
		} else {
			// Alive (or probe failed → fail safe): swap home only, agent survives.
			swapDisplayedHome(ctx, t, displayed, placeholder)
		}
		return windowClosedMsg{}
	}
}

// displayedPaneCheckedMsg carries the result of a periodic deadness probe of the
// displayed frame pane. When dead is true the statusTickMsg handler dispatches
// recoverDeadDisplayedCmd so the frame self-heals without any keypress.
type displayedPaneCheckedMsg struct{ dead bool }

// checkDisplayedDeadCmd returns a tea.Cmd that probes whether the displayed frame
// pane has exited (PaneDead) and reports the result via displayedPaneCheckedMsg.
//
// statusPoll's reload data cannot answer this: ListPanesAll excludes dead panes
// and keys by @perch_session, whereas displayedPaneID is a raw %N pane id — a dead
// displayed pane simply drops out of the poll, so a targeted PaneDead probe is
// used instead. A probe error is reported as NOT dead (fail safe: never recover —
// and thus never kill — on an inconclusive read).
//
// Guard conditions return nil (the caller omits the probe from the tick batch):
//   - loader == nil
//   - displayedPaneID == ""
func (m Model) checkDisplayedDeadCmd() tea.Cmd {
	if m.loader == nil || m.displayedPaneID == "" {
		return nil
	}
	target := m.displayedPaneID
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		dead, err := t.PaneDead(ctx, target)
		if err != nil {
			dead = false
		}
		return displayedPaneCheckedMsg{dead: dead}
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
// A dead displayed pane (swap-home errors) is handled gracefully: kill and quit
// still proceed regardless.
func (m Model) quitFrameCmd() tea.Cmd {
	if m.loader == nil {
		return tea.Quit
	}
	displayed := m.displayedPaneID
	placeholder := m.placeholderPaneID
	frameSession := m.frameSession
	t := m.loader.Tmux
	ctx := m.loader.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		// Step 1: swap displayed agent home (best-effort via shared helper).
		// A dead displayed pane is silently swallowed; kill must still run.
		swapDisplayedHome(ctx, t, displayed, placeholder)
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
