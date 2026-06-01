package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// Config holds the production dependencies for the perch TUI.
type Config struct {
	Tmux      tmux.Tmux
	Runner    proc.Runner
	Claude    agent.Claude
	Root      string
	BaseDir   string
	Now       int64
	RefreshMs int // status-tick interval; 0 → default 1 s

	// FrameSession and PlaceholderPane are non-empty when the TUI runs inside a
	// persistent perch frame (M11-0). When both are set, inFrame() returns true
	// and Enter dispatches swapInCmd instead of the old attachTo handoff.
	// When empty (default), the TUI runs in the old direct mode — handleTUI and
	// the fallback path in handleBootstrap rely on this.
	FrameSession    string
	PlaceholderPane string

	// ExecPath is the absolute path to the running perch binary (os.Executable),
	// used by the ':' command bar to run setup/doctor/resurrect via ExecProcess.
	ExecPath string
}

// Run starts the TUI program wired to the given dependencies and blocks until
// the user quits. ctx is propagated to background data loads and cancels them
// when the program exits.
func Run(ctx context.Context, cfg Config) error {
	ldr := loader{ctx: ctx, Tmux: cfg.Tmux, Runner: cfg.Runner, Claude: cfg.Claude, Root: cfg.Root, BaseDir: cfg.BaseDir, Now: cfg.Now}
	m := New(nil).WithLoader(ldr).WithRefresh(time.Duration(cfg.RefreshMs) * time.Millisecond).WithExecPath(cfg.ExecPath)
	// Seed the frame context when running inside a persistent frame.
	if cfg.FrameSession != "" && cfg.PlaceholderPane != "" {
		m.frameSession = cfg.FrameSession
		m.placeholderPaneID = cfg.PlaceholderPane
	}
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
