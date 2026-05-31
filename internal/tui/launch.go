package tui

import (
	"context"
	"crypto/rand"
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
}

// launchedMsg is delivered after the blocking launch work completes.
type launchedMsg struct {
	session string
	window  string
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
		ctx := context.Background() // program-ctx wiring is M5-5; match data.go's load()

		adapter, ok := adapterFor(spec.tool)
		if !ok {
			return launchedMsg{err: fmt.Errorf("unknown tool: %q", spec.tool)}
		}

		var sid string
		var argv []string
		if spec.resume {
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

		return launchedMsg{session: session, window: window}
	}
}

// attachTo switches the terminal client to target (a pre-built WindowTarget).
// Inside tmux ($TMUX set) it dispatches SwitchClient as a plain tea.Cmd;
// outside tmux it falls back to tea.ExecProcess for a full terminal handover.
func (m Model) attachTo(target string) (tea.Model, tea.Cmd) {
	argv := m.loader.Tmux.AttachTargetArgs(target)
	if len(argv) > 0 && argv[0] == "switch-client" {
		t := m.loader.Tmux
		return m, func() tea.Msg {
			return switchedMsg{err: t.SwitchClient(context.Background(), target)}
		}
	}
	// attach-session: genuine terminal handover; use tea.ExecProcess.
	full := m.loader.Tmux.ExecArgs(argv...)
	c := exec.Command(full[0], full[1:]...) //nolint:gosec
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
}
