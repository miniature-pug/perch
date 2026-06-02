// Package frame manages the persistent perch tmux frame: a two-pane session
// with a sidebar (the TUI) and a main slot showing the selected agent live.
// It is the bootstrap layer for the M11-0 persistent-frame switcher.
package frame

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miniature-Pug/perch/internal/tmux"
)

const (
	// FrameMarker is the tmux pane option set on the sidebar pane to identify
	// a perch frame. Its presence distinguishes a perch frame from an unrelated
	// tmux session with the same name (FD-03 guard).
	FrameMarker = "@perch_frame"

	// DefaultFrameSession is the default tmux session name for the perch frame.
	DefaultFrameSession = "perch"

	// frameWindow is the window name within the frame session.
	frameWindow = "frame"

	// sidebarWidth is the number of columns the sidebar pane is resized to after
	// the frame is created. The main slot gets the remainder.
	sidebarWidth = 50

	// placeholderCmd is the command run in the main (placeholder) pane at bootstrap.
	// It keeps the pane alive without consuming resources until an agent is swapped in.
	placeholderCmd = "sleep infinity"

	// defaultPaneHeight is the fallback height (rows) used when PaneSize fails during
	// frame creation. Matches a standard 24-row terminal.
	defaultPaneHeight = 24

	// navKeyTable is the tmux key-table name for the perch session-scoped navigation
	// bindings. A non-tmux-user can press focusListKey to return focus to the sidebar
	// without learning tmux prefix sequences.
	navKeyTable = "perchnav"

	// focusListKey is the key bound in navKeyTable to return focus to the sidebar pane.
	focusListKey = "F12"

	// statusLeft is the content shown in the tmux status bar for the perch frame.
	// It hints at the available key bindings so users know how to navigate.
	statusLeft = " perch │ F12/click ▸ list   ↵ ▸ open/resume   esc ▸ close window   q ▸ quit (agents live) "
)

// Info describes the current state of a perch frame after Ensure returns.
type Info struct {
	// Session is the tmux session name of the frame.
	Session string
	// SidebarPane is the pane id of the sidebar (TUI) pane.
	SidebarPane string
	// MainPane is the pane id of the main/placeholder slot.
	MainPane string
	// Created is true when the frame was newly created; false when it was reused.
	Created bool
}

// Ensure guarantees that a perch frame session named session exists and is
// properly structured. The caller passes the project root directory and the
// sidebar command as argv (typically {"<perch-binary>", "--sidebar"}); passing
// argv rather than a joined string keeps a binary path containing spaces intact.
//
// Behaviour:
//   - If the session does not exist: creates it, stamps the sidebar pane with
//     the FrameMarker option, splits the window to add the placeholder main pane
//     running placeholderCmd, and resizes the sidebar to sidebarWidth columns.
//     Returns Info{Created:true}.
//   - If the session exists and has a pane bearing FrameMarker=="1": returns the
//     existing sidebar+main pane ids without any mutation. Info{Created:false}.
//   - If the session exists but no pane bears FrameMarker: returns an error
//     describing the FD-03 conflict so the caller can surface it to the user.
func Ensure(ctx context.Context, t tmux.Tmux, session, root string, sidebarArgv []string) (Info, error) {
	exists, err := t.HasSession(ctx, session)
	if err != nil {
		return Info{}, fmt.Errorf("frame.Ensure: has-session: %w", err)
	}

	if exists {
		return reuseFrame(ctx, t, session)
	}
	return createFrame(ctx, t, session, root, sidebarArgv)
}

// reuseFrame verifies that the existing session is a perch frame (FD-03) and
// returns the sidebar+main pane ids. It self-heals a damaged frame:
//   - 2-pane frame, live main → return as-is (no action).
//   - 2-pane frame, dead main → respawn-pane in place; id is stable.
//   - 1-pane frame (only sidebar) → split-window to recreate the main slot.
//
// In all paths a defensive SetWindowOption(remain-on-exit=on) is issued so that
// frames created by an older binary gain the guard retroactively.
func reuseFrame(ctx context.Context, t tmux.Tmux, session string) (Info, error) {
	target := tmux.WindowTarget(session, frameWindow)
	panes, err := t.ListPanes(ctx, target)
	if err != nil || len(panes) == 0 {
		return Info{}, fmt.Errorf("frame.Ensure: a tmux session %q exists and is not a perch frame", session)
	}

	// Find the pane bearing FrameMarker="1"; that's the sidebar.
	var sidebarID string
	var sidebarPath string
	for _, p := range panes {
		val, gerr := t.GetPaneOption(ctx, p.ID, FrameMarker)
		if gerr != nil {
			continue
		}
		if strings.TrimSpace(val) == "1" {
			sidebarID = p.ID
			sidebarPath = p.Path
		}
	}
	if sidebarID == "" {
		return Info{}, fmt.Errorf("frame.Ensure: a tmux session %q exists and is not a perch frame", session)
	}

	// Defensive: stamp remain-on-exit on the frame window so older-binary frames
	// gain the guard retroactively. Best-effort.
	_ = t.SetWindowOption(ctx, target, "remain-on-exit", "on")

	// Determine mainID, healing the frame if necessary.
	// Strategy:
	//   - Use the sidebar pane's current path as dir for any re-split, falling
	//     back to "." when unavailable. This avoids an extra tmux round-trip
	//     because pane_current_path is already populated by ListPanes.
	dir := sidebarPath
	if dir == "" {
		dir = "."
	}

	var mainID string
	for _, p := range panes {
		if p.ID == sidebarID {
			continue
		}
		if !p.Dead {
			// Healthy non-sidebar pane — no repair needed.
			mainID = p.ID
			break
		}
		// Dead non-sidebar pane — respawn in place; pane id is stable.
		deadID := p.ID
		_ = t.RespawnPane(ctx, deadID, placeholderCmd)
		mainID = deadID
		break
	}

	if mainID == "" {
		// No non-sidebar pane at all (1-pane / legacy-damage): re-split.
		newID, serr := t.SplitWindow(ctx, target, dir, true, placeholderCmd)
		if serr != nil {
			return Info{}, fmt.Errorf("frame.Ensure: re-split main pane: %w", serr)
		}
		mainID = newID
	}

	return Info{
		Session:     session,
		SidebarPane: sidebarID,
		MainPane:    mainID,
		Created:     false,
	}, nil
}

// createFrame builds a new perch frame: launches sidebarCmd in a new session,
// stamps the sidebar pane, splits to add the placeholder, and sizes the sidebar.
func createFrame(ctx context.Context, t tmux.Tmux, session, root string, sidebarArgv []string) (Info, error) {
	// argv is passed by the caller already tokenised so a binary path containing
	// spaces is preserved (no string-splitting).
	argv := sidebarArgv
	if len(argv) == 0 {
		argv = []string{DefaultFrameSession, "--sidebar"}
	}

	// Launch creates the session via Connect → NewSession (no cmd), then sends
	// the command via SendKeys — the same mechanism used for agent sessions.
	sidebarPane, err := t.Launch(ctx, session, frameWindow, root, argv)
	if err != nil {
		return Info{}, fmt.Errorf("frame.Ensure: launch sidebar: %w", err)
	}

	// Stamp the sidebar pane with FrameMarker so future Ensure calls can identify it.
	if err := t.SetPaneOption(ctx, sidebarPane, FrameMarker, "1"); err != nil {
		return Info{}, fmt.Errorf("frame.Ensure: set frame marker: %w", err)
	}

	// Split horizontally to create the main (placeholder) pane.
	frameTarget := tmux.WindowTarget(session, frameWindow)
	mainPane, err := t.SplitWindow(ctx, frameTarget, root, true, placeholderCmd)
	if err != nil {
		return Info{}, fmt.Errorf("frame.Ensure: split main pane: %w", err)
	}

	// remain-on-exit: a displayed agent that exits leaves a dead pane instead of
	// destroying the frame's main slot; M17 recovery reclaims it.
	_ = t.SetWindowOption(ctx, frameTarget, "remain-on-exit", "on")

	// Resize the sidebar to sidebarWidth columns. Use the current sidebar height
	// so we only change the width, not the height.
	_, h, err := t.PaneSize(ctx, sidebarPane)
	if err != nil {
		// Non-fatal: skip resize rather than aborting the whole bootstrap.
		h = defaultPaneHeight // safe default
	}
	// Best-effort: a headless server may reject resize; the frame is still usable.
	// NOTE: status bar consumes 1 row; agent reflow sizing uses the measured main-pane height (see swapInCmd).
	_ = t.ResizePane(ctx, sidebarPane, sidebarWidth, h)

	// Apply session-level options: navigation key binding, mouse support, and
	// status bar. All are best-effort — a cosmetic option failing must not abort
	// frame creation. Bind the key-table entry BEFORE pointing the session at it.
	_ = t.BindKey(ctx, navKeyTable, focusListKey, "select-pane", "-L")
	_ = t.SetSessionOption(ctx, session, "key-table", navKeyTable)
	_ = t.SetSessionOption(ctx, session, "mouse", "on")
	_ = t.SetSessionOption(ctx, session, "status", "on")
	_ = t.SetSessionOption(ctx, session, "status-left-length", "200")
	_ = t.SetSessionOption(ctx, session, "status-left", statusLeft)
	_ = t.SetSessionOption(ctx, session, "status-right", "")

	return Info{
		Session:     session,
		SidebarPane: sidebarPane,
		MainPane:    mainPane,
		Created:     true,
	}, nil
}

// SidebarContext resolves the frame context from within a running sidebar pane.
// It reads the sidebar's own pane id from getenv("TMUX_PANE"), queries
// CurrentClientWindow to find the session and window, then calls ListPanes on
// that window to identify the sibling (non-sidebar) pane — the main/placeholder
// slot. Returns the frame session name and the placeholder pane id.
//
// This is called by handleSidebar at startup to inject frameSession and
// placeholderPaneID into the TUI Config.
func SidebarContext(ctx context.Context, t tmux.Tmux, getenv func(string) string) (frameSession, placeholderPane string, err error) {
	ownPane := getenv("TMUX_PANE")
	if ownPane == "" {
		return "", "", fmt.Errorf("frame.SidebarContext: TMUX_PANE not set (not running inside tmux)")
	}

	session, window, err := t.CurrentClientWindow(ctx)
	if err != nil {
		return "", "", fmt.Errorf("frame.SidebarContext: CurrentClientWindow: %w", err)
	}

	target := tmux.WindowTarget(session, window)
	panes, err := t.ListPanes(ctx, target)
	if err != nil {
		return "", "", fmt.Errorf("frame.SidebarContext: list-panes: %w", err)
	}

	for _, p := range panes {
		if p.ID != ownPane {
			return session, p.ID, nil
		}
	}

	return "", "", fmt.Errorf("frame.SidebarContext: no sibling pane found in window %s", target)
}
