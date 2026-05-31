package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// Config holds the production dependencies for the perch TUI.
type Config struct {
	Tmux    tmux.Tmux
	Runner  proc.Runner
	Claude  agent.Claude
	Root    string
	BaseDir string
	Now     int64
}

// Run starts the TUI program wired to the given dependencies and blocks until
// the user quits. ctx is propagated to background data loads and cancels them
// when the program exits.
func Run(ctx context.Context, cfg Config) error {
	ldr := loader{ctx: ctx, Tmux: cfg.Tmux, Runner: cfg.Runner, Claude: cfg.Claude, Root: cfg.Root, BaseDir: cfg.BaseDir, Now: cfg.Now}
	m := New(nil).WithLoader(ldr)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
