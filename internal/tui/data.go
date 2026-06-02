package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/match"
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

// statusTickMsg is the self-re-arming tick that drives the status poll cycle.
type statusTickMsg struct{}

// statusPollMsg carries the latest @perch_pane_status values keyed by session ID.
type statusPollMsg struct {
	statuses map[string]string // sessionID → @perch_pane_status value
}

// loader holds injected dependencies for live data loading.
// All fields are set by the caller; zero values are not used in production.
type loader struct {
	// ctx is the program-scoped context, cancelled when the TUI exits.
	// nil in tests → treated as context.Background().
	ctx     context.Context
	Tmux    tmux.Tmux
	Runner  proc.Runner  // used by discover + opencode adapters
	Claude  agent.Claude // global session lister
	Root    string       // discover scan root
	BaseDir string       // state base dir for frecency
	Now     int64        // injected clock (no time.Now in logic)
	// GlobalCfg is the globally-loaded *config.Config, threaded from tui.Config.GlobalCfg
	// through Run. nil in test/scaffold mode — callers nil-guard before use.
	// Distinct from Config (below), which is a per-project test override used by
	// worktree_actions.go's projectConfig helper.
	GlobalCfg *config.Config
	// Config is an optional test override for per-project config loading.
	// nil → load per-project on demand via config.Load in action Cmds.
	Config *config.Config
}

// load returns a tea.Cmd that performs the full data fetch off the UI goroutine
// and delivers an itemsLoadedMsg.
func (l loader) load() tea.Cmd {
	return func() tea.Msg {
		ctx := l.ctx
		if ctx == nil {
			ctx = context.Background()
		}

		// Load frecency state; cold-start returns empty maps (no error).
		st, _ := state.LoadState(l.BaseDir)

		// Discover projects + trees, already frecency-ordered by discover.Projects.
		pts, err := discover.Projects(ctx, l.Runner, l.Root, discover.Options{}, st.Projects, l.Now)
		if err != nil {
			return itemsLoadedMsg{err: fmt.Errorf("tui: discover projects: %w", err)}
		}

		// Blacklist: drop any project/tree whose path matches a configured pattern.
		// This is a post-discovery UI filter — discover's own walk-pruning is unchanged.
		if l.GlobalCfg != nil && len(l.GlobalCfg.Blacklist) > 0 {
			pts = filterBlacklist(pts, l.GlobalCfg.Blacklist)
		}

		// Snapshot live panes once; build sessionID → pane ID index.
		panes, _ := l.Tmux.ListPanesAll(ctx) // degrade on error: no live status
		liveBySession := buildLiveIndex(panes)

		// Claude sessions are global (not scoped per directory). We call once and
		// group by directory so the per-tree matching below is O(1).
		claudeSessions, _ := l.Claude.ListSessions(ctx) // degrade: skip tool
		claudeByDir := agent.GroupByDirectory(claudeSessions)

		// Fetch opencode sessions per tree.
		// FakeRunner keys responses by name+args only (not Dir), so in tests a
		// single canned response is shared across all trees. Production code
		// naturally scopes per tree via Dir.
		ocByTree := make(map[string][]model.Session, len(pts))
		for _, pt := range pts {
			for i := range pt.Trees {
				tree := &pt.Trees[i]
				oc := agent.Opencode{
					Runner: l.Runner,
					Bin:    string(model.ToolOpencode),
					Dir:    tree.Path,
				}
				sessions, _ := oc.ListSessions(ctx) // degrade: skip tree on error
				ocByTree[tree.Path] = sessions
			}
		}

		items := assembleItems(pts, claudeByDir, ocByTree, liveBySession, l.Now)

		// sort_order: apply the configured priority list (running, frecency).
		// Unknown tokens (e.g. "pinned") are skipped silently.
		// When cfg is nil or SortOrder is empty, use the default ["running","frecency"].
		sortTokens := defaultLoaderSortOrder
		if l.GlobalCfg != nil && len(l.GlobalCfg.SortOrder) > 0 {
			sortTokens = l.GlobalCfg.SortOrder
		}
		items = applySortOrder(items, sortTokens)

		return itemsLoadedMsg{items: items}
	}
}

// statusPoll returns a tea.Cmd that snapshots all pane statuses in one
// ListPanesAll call and delivers a statusPollMsg. The map is keyed by
// PerchSession (session ID) with the pane's PerchStatus as the value.
// Dead panes and panes with empty PerchSession are excluded; first-write-wins
// for any duplicate PerchSession tags (mirrors buildLiveIndex).
func (l loader) statusPoll() tea.Cmd {
	return func() tea.Msg {
		ctx := l.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		panes, _ := l.Tmux.ListPanesAll(ctx) // degrade on error: empty map
		statuses := make(map[string]string, len(panes))
		for _, p := range panes {
			if p.Dead || p.PerchSession == "" {
				continue
			}
			if _, exists := statuses[p.PerchSession]; exists {
				continue // first-write-wins: skip duplicate PerchSession tags
			}
			statuses[p.PerchSession] = p.PerchStatus
		}
		return statusPollMsg{statuses: statuses}
	}
}

// assembleItems is a pure function that builds the ordered []list.Item slice
// from pre-fetched data. It is separated from load() so tests can drive the
// join and ordering logic without touching the filesystem or spawning processes.
//
// pts is already in frecency order (as returned by discover.Projects). The
// output preserves that order: for each project, all trees, then all sessions
// per tree.
func assembleItems(
	pts []*discover.ProjectTrees,
	claudeByDir map[string][]model.Session,
	ocByTree map[string][]model.Session,
	liveBySession map[string]tmux.Pane,
	now int64,
) []list.Item {
	var items []list.Item
	for _, pt := range pts {
		proj := &pt.Project
		for i := range pt.Trees {
			tree := &pt.Trees[i]

			// Claude sessions bound to this tree's directory.
			for _, s := range claudeByDir[tree.Path] {
				items = append(items, buildItemFromSession(s, proj.Name, tree.Branch, proj.Path, tree.Path, now, liveBySession, tree.IsMain))
			}

			// Opencode sessions pre-fetched for this tree.
			for _, s := range ocByTree[tree.Path] {
				items = append(items, buildItemFromSession(s, proj.Name, tree.Branch, proj.Path, tree.Path, now, liveBySession, tree.IsMain))
			}
		}
	}
	return items
}

// buildLiveIndex returns a map of session ID → live Pane from the pane
// snapshot. Panes where Dead == true are excluded. Panes with an empty
// PerchSession are skipped so a spurious empty-string key never matches
// sessions that have no live pane.
//
// First-write-wins: by convention one live pane exists per session; if that
// invariant is violated (duplicate PerchSession tags), we keep the first pane
// encountered and ignore subsequent ones.
func buildLiveIndex(panes []tmux.Pane) map[string]tmux.Pane {
	idx := make(map[string]tmux.Pane, len(panes))
	for _, p := range panes {
		if p.Dead || p.PerchSession == "" {
			continue
		}
		if _, exists := idx[p.PerchSession]; exists {
			continue // first-write-wins: skip duplicate PerchSession tags
		}
		idx[p.PerchSession] = p
	}
	return idx
}

// buildItemFromSession constructs one list.Item from a model.Session plus its
// project/tree metadata and the live-pane index.
// Status is binary: StatusLive (live pane attached) or StatusIdle.
// Real Working/Waiting/Done detection requires @perch_status and is deferred.
func buildItemFromSession(
	s model.Session,
	projectName, branch, projectPath, treePath string,
	now int64,
	liveBySession map[string]tmux.Pane,
	isMain bool,
) list.Item {
	pane, isLive := liveBySession[s.ID]
	status := StatusIdle
	var captureTarget, liveTarget string
	if isLive {
		status = StatusLive
		captureTarget = pane.ID
		liveTarget = tmux.WindowTarget(pane.Session, pane.Window)
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
		captureTarget: captureTarget,
		projectPath:   projectPath,
		treePath:      treePath,
		liveTarget:    liveTarget,
		isMain:        isMain,
	}
}

// defaultLoaderSortOrder is the fallback token list used when cfg is nil or
// SortOrder is empty. Mirrors config.defaultSortOrder (which is unexported).
var defaultLoaderSortOrder = []string{"running", "frecency"}

// filterBlacklist removes any project whose path matches any of the patterns,
// and removes matching trees from projects that are themselves kept.
// Projects with no remaining trees are also removed.
// A nil or empty patterns slice is a no-op.
func filterBlacklist(pts []*discover.ProjectTrees, patterns []string) []*discover.ProjectTrees {
	if len(patterns) == 0 {
		return pts
	}
	out := pts[:0:len(pts)]
	for _, pt := range pts {
		// Drop the whole project if its root path matches.
		if match.MatchAny(patterns, pt.Project.Path) {
			continue
		}
		// Filter individual trees within the project.
		kept := pt.Trees[:0:len(pt.Trees)]
		for i := range pt.Trees {
			if !match.MatchAny(patterns, pt.Trees[i].Path) {
				kept = append(kept, pt.Trees[i])
			}
		}
		if len(kept) == 0 {
			// All trees were blacklisted — drop the project too.
			continue
		}
		out = append(out, &discover.ProjectTrees{
			Project: pt.Project,
			Trees:   kept,
		})
	}
	return out
}

// orderedItem wraps a list.Item with its original assembly index, preserving
// discover's frecency+alphabetical order as the "frecency" sort key.
type orderedItem struct {
	it   list.Item
	ord  int  // original index in the assembled slice (frecency rank)
	live bool // true when the item is a live session row
}

// applySortOrder stably re-orders items according to the priority token list.
// Supported tokens: "running" (live rows first), "frecency" (original index order).
// Unknown tokens (e.g. "pinned") are skipped silently.
// The original index is always the final tiebreak, which preserves discover's
// built-in alphabetical cold-start ordering without recomputing it.
func applySortOrder(items []list.Item, tokens []string) []list.Item {
	if len(items) == 0 {
		return items
	}

	wrapped := make([]orderedItem, len(items))
	for i, it := range items {
		live := false
		if it, ok := it.(item); ok {
			live = it.live
		}
		wrapped[i] = orderedItem{it: it, ord: i, live: live}
	}

	sort.SliceStable(wrapped, func(a, b int) bool {
		wa, wb := wrapped[a], wrapped[b]
		for _, tok := range tokens {
			switch tok {
			case "running":
				if wa.live != wb.live {
					return wa.live // live first
				}
			case "frecency":
				if wa.ord != wb.ord {
					return wa.ord < wb.ord
				}
				// unknown tokens (e.g. "pinned") are skipped — no panic
			}
		}
		// Final tiebreak: original index (preserves discover's ordering).
		return wa.ord < wb.ord
	})

	out := make([]list.Item, len(items))
	for i, w := range wrapped {
		out[i] = w.it
	}
	return out
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
