package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// itemsLoadedMsg carries the fully built item slice returned by the loader.
type itemsLoadedMsg struct {
	items []list.Item
	err   error
}

// previewMsg carries the capture-pane result for a specific live pane.
type previewMsg struct {
	content string
	target  string // pane ID that was captured
}

// loader holds injected dependencies for live data loading.
// All fields are set by the caller; zero values are not used in production.
type loader struct {
	Tmux    tmux.Tmux
	Runner  proc.Runner  // used by discover + opencode adapters
	Claude  agent.Claude // global session lister
	Root    string       // discover scan root
	BaseDir string       // state base dir for frecency
	Now     int64        // injected clock (no time.Now in logic)
}

// load returns a tea.Cmd that performs the full data fetch off the UI goroutine
// and delivers an itemsLoadedMsg.
func (l loader) load() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		// Load frecency state; cold-start returns empty maps (no error).
		st, _ := state.LoadState(l.BaseDir)

		// Discover projects + trees, already frecency-ordered by discover.Projects.
		pts, err := discover.Projects(ctx, l.Runner, l.Root, discover.Options{}, st.Projects, l.Now)
		if err != nil {
			return itemsLoadedMsg{err: fmt.Errorf("tui: discover projects: %w", err)}
		}

		// Snapshot live panes once; build sessionID → pane ID index.
		panes, _ := l.Tmux.ListPanesAll(ctx) // degrade on error: no live status
		liveBySession := buildLiveIndex(panes)

		// Claude sessions are global (not scoped per directory). We call once and
		// group by directory so the per-tree matching below is O(1).
		claudeSessions, _ := l.Claude.ListSessions(ctx) // degrade: skip tool
		claudeByDir := agent.GroupByDirectory(claudeSessions)

		// Build items in frecency order (discover.Projects returns ordered pts).
		var items []list.Item
		for _, pt := range pts {
			proj := &pt.Project
			for i := range pt.Trees {
				tree := &pt.Trees[i]

				// Claude sessions bound to this tree's directory.
				for _, s := range claudeByDir[tree.Path] {
					it := buildItemFromSession(s, proj.Name, tree.Branch, l.Now, liveBySession)
					items = append(items, it)
				}

				// Opencode sessions are scoped per-tree: one subprocess per tree.
				oc := agent.Opencode{
					Runner: l.Runner,
					Bin:    "opencode",
					Dir:    tree.Path,
				}
				ocSessions, _ := oc.ListSessions(ctx) // degrade: skip tree on error
				for _, s := range ocSessions {
					it := buildItemFromSession(s, proj.Name, tree.Branch, l.Now, liveBySession)
					items = append(items, it)
				}
			}
		}

		return itemsLoadedMsg{items: items}
	}
}

// buildLiveIndex returns a map of session ID → live pane ID from the pane
// snapshot. Panes where Dead == true are excluded. Panes with an empty
// PerchSession are skipped so a spurious empty-string key never matches
// sessions that have no live pane.
func buildLiveIndex(panes []tmux.Pane) map[string]string {
	idx := make(map[string]string, len(panes))
	for _, p := range panes {
		if p.Dead || p.PerchSession == "" {
			continue
		}
		idx[p.PerchSession] = p.ID
	}
	return idx
}

// buildItemFromSession constructs one list.Item from a model.Session plus its
// project/tree metadata and the live-pane index.
// Status is binary: StatusWorking (live pane exists) or StatusIdle.
func buildItemFromSession(
	s model.Session,
	projectName, branch string,
	now int64,
	liveBySession map[string]string,
) list.Item {
	paneID, isLive := liveBySession[s.ID]
	status := StatusIdle
	if isLive {
		status = StatusWorking
	}
	return item{
		id:            s.ID,
		project:       projectName,
		tree:          branch,
		title:         s.Title,
		tool:          string(s.Tool),
		status:        status,
		relTime:       relativeTime(s.Updated, now),
		isSession:     true,
		live:          isLive,
		captureTarget: paneID,
	}
}

// relativeTime formats the duration between updated (unix seconds) and now as a
// compact human-readable string (e.g. "2m ago", "1h ago", "3d ago").
func relativeTime(updated, now int64) string {
	if updated <= 0 || now <= 0 {
		return ""
	}
	d := time.Duration(now-updated) * time.Second
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
