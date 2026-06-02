package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/trust"
)

const (
	// borderSize is the number of cells consumed by a rounded border on one side.
	borderSize = 1
	// footerHeight is the number of terminal rows reserved for the footer hint bar.
	footerHeight = 1
	// messageBarHeight is the row reserved above the panes for the transient
	// toast / load-error message line, so adding a message never overflows height.
	messageBarHeight = 1
	// listPanePercent is the percentage of the total width allocated to the list
	// pane in wide (side-by-side) mode. The preview pane takes the remainder.
	listPanePercent = 30
	// cmdBarCharLimit is the maximum number of characters accepted by the command
	// input bar (textinput CharLimit). Prevents unbounded input accumulation.
	cmdBarCharLimit = 256
)

// Model is the root Bubble Tea model for the perch TUI.
// It composes a list (left pane) and a viewport (right pane).
type Model struct {
	list    list.Model
	preview viewport.Model
	keys    keyMap
	width   int
	height  int
	ready   bool

	// loader is optional; when set, Init returns its load Cmd.
	loader *loader

	// cfg is the globally-loaded config, threaded from loader.GlobalCfg in WithLoader.
	// nil in test/scaffold mode (New with no loader, or loader.GlobalCfg == nil).
	// Every read site must nil-guard so the zero-value fallback is preserved.
	cfg *config.Config

	// theme holds the accent-derived instance-level styles resolved from cfg.Theme.Accent
	// (or defaultAccent when unset). Built in New and rebuilt in WithLoader.
	theme theme

	// root is the discovery scan root, captured from the loader for the
	// empty-state message. Empty in test/scaffold mode.
	root string

	// mode is the current screen layout (normal / full-list / full-preview).
	mode screenMode

	// help renders the footer short-help and the ? full-help overlay.
	help help.Model
	// showHelp toggles the ? full-help overlay.
	showHelp bool

	// loadErr holds the last whole-load failure message for display in the UI.
	// Empty string means no error. Cleared on successful reload.
	loadErr string

	// toast holds a transient, auto-dismissing status message ("" = none).
	// Used for user-action rejections and launch/switch/attach failures.
	toast string
	// toastSeq increments per toast so a stale clear tick can't wipe a newer one.
	toastSeq int

	// previewContent holds the current text shown in the preview pane.
	// Stored separately from the viewport so tests can assert without rendering.
	previewContent string

	// capturing is true while a CapturePane call is in-flight.
	// It prevents overlapping capture commands.
	capturing bool

	// polling is true while a statusPoll call is in-flight.
	// It prevents overlapping status polls (mirrors the capturing guard).
	polling bool

	// refresh is the status-tick interval. Seeded to 1 s by New; overridable
	// via WithRefresh. Never zero in production — zero would hot-loop tea.Tick.
	refresh time.Duration

	// modal holds the currently-active modal prompt. Zero value (kind==modalNone)
	// means no modal is visible.
	modal modalState

	// ── persistent-frame fields (M11-0 T3) ──────────────────────────────────
	// These are empty in the old direct-TUI mode; inFrame() checks placeholderPaneID.

	// frameSession is the name of the perch tmux session that owns this sidebar.
	frameSession string

	// placeholderPaneID is the disposable placeholder pane that lives in the frame
	// main slot when no agent is displayed, or in the displayed agent's home session
	// when an agent occupies the main slot. Non-empty iff the TUI is running inside
	// a persistent frame.
	placeholderPaneID string

	// displayedPaneID is the agent pane currently occupying the frame main slot,
	// or "" when the placeholder is there (nothing displayed yet).
	displayedPaneID string

	// swapping is true while a swapInCmd or quitFrameCmd is in-flight, serialising
	// concurrent selection changes in the Update loop.
	swapping bool

	// cmdline is the ':' command-bar text input; receives keys only while cmdActive.
	cmdline textinput.Model
	// cmdActive is true while the ':' command bar owns keyboard input.
	cmdActive bool
	// execPath is the absolute path to the running perch binary, used to run
	// `perch setup|doctor|resurrect` via tea.ExecProcess. Empty in test mode →
	// those commands toast instead of exec'ing.
	execPath string
}

// New returns a Model with the given items pre-loaded.
// Width and height start at zero; they are updated by the first tea.WindowSizeMsg.
func New(items []list.Item) Model {
	// Build the default theme before the list so the delegate carries the accent.
	th := newTheme(defaultAccent)

	l := list.New(items, itemDelegate{selectedRow: th.selectedRow}, 0, 0)
	// Disable the built-in quit binding so our own Quit key is the only exit.
	l.KeyMap.Quit.SetEnabled(false)
	l.KeyMap.ForceQuit.SetEnabled(false)
	// Use a plain title so the height calculation stays simple.
	l.Title = "Sessions"

	ti := textinput.New()
	ti.Prompt = ":"
	ti.CharLimit = cmdBarCharLimit

	return Model{
		list:    l,
		preview: viewport.New(0, 0),
		keys:    defaultKeys(),
		help:    help.New(),
		cmdline: ti,
		refresh: time.Second, // default; overridable via WithRefresh
		theme:   th,
	}
}

// WithRefresh returns a copy of m with the status-tick interval set to d.
// When d ≤ 0 the interval defaults to 1 s so tea.Tick never hot-loops.
func (m Model) WithRefresh(d time.Duration) Model {
	if d <= 0 {
		d = time.Second
	}
	m.refresh = d
	return m
}

// WithExecPath returns a copy of m with the perch binary path set, enabling the
// command bar's :setup/:doctor/:resurrect commands.
func (m Model) WithExecPath(p string) Model {
	m.execPath = p
	return m
}

// WithLoader returns a copy of m with the given loader wired in.
// Init will then return the load Cmd automatically.
// m.cfg is set from l.GlobalCfg; it remains nil when l.GlobalCfg is nil (test/scaffold mode).
// When a non-empty Theme.Accent is configured, the accent-derived theme styles
// are rebuilt from it and the list delegate is updated accordingly.
func (m Model) WithLoader(l loader) Model {
	m.loader = &l
	m.root = l.Root
	m.cfg = l.GlobalCfg

	// Resolve the accent from config; fall back to the TUI default.
	accent := defaultAccent
	if m.cfg != nil && m.cfg.Theme.Accent != "" {
		accent = m.cfg.Theme.Accent
	}
	m.theme = newTheme(accent)
	// Propagate the accent-aware selectedRow style to the list delegate.
	m.list.SetDelegate(itemDelegate{selectedRow: m.theme.selectedRow})

	return m
}

// inFrame reports whether the TUI is running inside a persistent perch frame.
// When true, Enter on a live session swaps the agent into the frame main slot
// instead of handing off the terminal with switch-client/attach-session.
func (m Model) inFrame() bool {
	return m.placeholderPaneID != ""
}

// Init satisfies tea.Model. When a loader is configured it fires the initial
// data load and arms the status-tick; otherwise it does nothing (scaffold /
// test mode).
func (m Model) Init() tea.Cmd {
	if m.loader != nil {
		return tea.Batch(m.loader.load(), m.tickCmd())
	}
	return nil
}

// preflightRemoveMsg is delivered by preflightRemoveCmd after checking whether
// the current client is focused in the target worktree window.
type preflightRemoveMsg struct {
	spec    modalState
	focused bool
	err     error
}

// removeResultMsg is delivered after removeCmd completes (success or failure).
type removeResultMsg struct {
	spec  modalState
	dirty bool
	err   error
}

// killResultMsg is delivered after killCmd completes.
type killResultMsg struct{}

// swappedMsg is delivered after swapInCmd or quitFrameCmd completes.
type swappedMsg struct {
	target string // agent pane id that is now displayed (empty for no-op)
	noop   bool   // true when the target was already displayed — no tmux calls made
	err    error
}

// worktreePreflightMsg is delivered by worktreePreflightCmd after checking
// the existing session→worktree mapping.
// When prompt==true the picker modal should open (openModal is ready to assign).
// When prompt==false the spec is fully resolved and launchCmd should fire immediately.
type worktreePreflightMsg struct {
	prompt    bool
	openModal modalState
	spec      launchSpec
}

// worktreeCreatedMsg is delivered after worktreeCreateCmd (or runHereCmd /
// runMainCmd) has finished its synchronous work. On success spec carries the
// launchSpec to hand off to launchCmd.
type worktreeCreatedMsg struct {
	spec  launchSpec
	err   error
	trust *trustReq // non-nil when hooks are pending and require user approval
}

// trustNeededMsg is delivered by removeCmd when pre_remove hooks need approval.
type trustNeededMsg struct {
	trust *trustReq
}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case itemsLoadedMsg:
		if msg.err == nil {
			m.loadErr = ""
			m.list.SetItems(msg.items)
		} else {
			m.loadErr = msg.err.Error()
		}
		// Refresh the preview for the newly-selected item.
		return m, m.previewCmd()

	case launchedMsg:
		if msg.err != nil {
			return m.withToast("launch failed: " + msg.err.Error())
		}
		if m.inFrame() {
			// Frame mode: swap the newly-launched pane into the main slot instead
			// of handing off the terminal. swapInCmd sets m.swapping = true via
			// pointer receiver. Hoisted out of return tuple to guarantee mutation
			// order (Go spec leaves multi-expr return order unspecified).
			cmd := m.swapInCmd(msg.pane)
			return m, cmd
		}
		target := tmux.WindowTarget(msg.session, msg.window)
		return m.attachTo(target)

	case switchedMsg:
		if msg.err != nil {
			return m.withToast("switch failed: " + msg.err.Error())
		}
		return m, nil

	case attachFinishedMsg:
		if msg.err != nil {
			return m.withToast("attach failed: " + msg.err.Error())
		}
		// Reloading the list after detach is deferred.
		return m, nil

	case windowClosedMsg:
		// Non-destructive close-window: the displayed agent has been swapped home
		// (or was already gone); reset the view so the placeholder is shown again.
		m.displayedPaneID = ""
		return m, nil

	case swappedMsg:
		m.swapping = false
		if msg.err != nil {
			return m.withToast("swap failed: " + msg.err.Error())
		}
		if !msg.noop {
			m.displayedPaneID = msg.target
		}
		return m, nil

	case preflightRemoveMsg:
		// Ignore stale messages if a different modal is already open.
		if m.modal.kind != modalNone {
			return m, nil
		}
		if msg.focused {
			return m.withToast("cannot remove the worktree you're focused in — switch away first")
		}
		// err means no attached client — treat as not focused; proceed to confirm.
		m.showHelp = false // an async-opened modal must not hide behind the help overlay
		m.modal = msg.spec
		m.modal.kind = modalRemoveConfirm
		return m, nil

	case removeResultMsg:
		if msg.dirty {
			// Promote to force-confirm modal, keeping spec.
			m.showHelp = false // an async-opened modal must not hide behind the help overlay
			m.modal = msg.spec
			m.modal.kind = modalForceConfirm
			return m, nil
		}
		m.modal = modalState{}
		if msg.err != nil {
			return m.withToast("remove failed: " + msg.err.Error())
		}
		// Success — reload list.
		return m, m.reloadCmd()

	case killResultMsg:
		m.modal = modalState{}
		// Reload list so the killed window no longer shows as live.
		return m, m.reloadCmd()

	case worktreePreflightMsg:
		// Ignore stale messages if a different modal is already open.
		if m.modal.kind != modalNone {
			return m, nil
		}
		if msg.prompt {
			m.showHelp = false // an async-opened modal must not hide behind the help overlay
			m.modal = msg.openModal
			return m, nil
		}
		return m, m.launchCmd(msg.spec)

	case worktreeCreatedMsg:
		if msg.err != nil {
			return m.withToast("worktree failed: " + msg.err.Error())
		}
		// Hook trust approval pending: open the trust modal instead of launching.
		if msg.trust != nil {
			m.showHelp = false
			m.modal = modalState{kind: modalTrustConfirm, trust: msg.trust}
			return m, nil
		}
		return m, m.launchCmd(msg.spec)

	case trustNeededMsg:
		// Pre-remove hooks need approval.
		if msg.trust != nil {
			m.showHelp = false
			m.modal = modalState{kind: modalTrustConfirm, trust: msg.trust}
		}
		return m, nil

	case statusTickMsg:
		// Always re-arm the tick; fire a poll only when not already in-flight
		// and the list is not in filter mode.
		cmds := []tea.Cmd{m.tickCmd()}
		if c := m.statusPollCmd(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case execFinishedMsg:
		if msg.err != nil {
			return m.withToast("command failed: " + msg.err.Error())
		}
		if m.loader != nil {
			return m, m.reloadCmd()
		}
		return m, nil

	case clearToastMsg:
		if msg.seq == m.toastSeq {
			m.toast = ""
		}
		return m, nil

	case statusPollMsg:
		m.polling = false
		// Apply statuses to live items while preserving the current selection.
		idx := m.list.Index()
		current := m.list.Items()
		updated := make([]list.Item, len(current))
		for i, li := range current {
			it, ok := li.(item)
			if ok && it.live {
				it.status = statusFromOption(msg.statuses[it.id], true)
			}
			updated[i] = it
		}
		// SetItems returns a re-filter cmd when a filter is applied; capture it so
		// the filtered view refreshes immediately rather than waiting for a keystroke.
		cmd := m.list.SetItems(updated)
		m.list.Select(idx)
		return m, cmd

	case previewMsg:
		m.capturing = false
		sel, ok := m.selectedItem()
		if ok && msg.target == sel.captureTarget {
			// Matching target: apply the content and stop.
			m.previewContent = msg.content
			m.preview.SetContent(msg.content)
			return m, nil
		}
		// Stale target: the user navigated during the in-flight capture.
		// Re-fire a capture for the now-current selection.
		return m, m.previewCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		m.ready = true
		return m, m.previewCmd()

	case tea.KeyMsg:
		// When the list is in filter mode let it handle all keys first so the
		// text input receives characters and the filter can be accepted/cancelled.
		if m.list.SettingFilter() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			m.refreshStaticPreview()
			return m, cmd
		}

		if m.showHelp {
			switch {
			case key.Matches(msg, m.keys.Help),
				key.Matches(msg, m.keys.ClearFilter),
				key.Matches(msg, m.keys.Quit):
				m.showHelp = false
			}
			return m, nil
		}

		// While a modal is open it owns all keys.
		if m.modal.kind != modalNone {
			return m.updateModal(msg)
		}

		// While the ':' command bar is active it owns all keys.
		if m.cmdActive {
			return m.updateCmdline(msg)
		}

		switch {
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, m.keys.CmdBar):
			m.cmdActive = true
			m.cmdline.SetValue("")
			return m, m.cmdline.Focus()

		case key.Matches(msg, m.keys.Quit):
			if m.inFrame() {
				return m, m.quitFrameCmd()
			}
			return m, tea.Quit

		case key.Matches(msg, m.keys.Filter):
			// Delegate '/' to the list so it enters filtering mode.
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd

		case key.Matches(msg, m.keys.ClearFilter):
			// Esc: priority order —
			//   1. If a filter is applied (FilterApplied state), clear it.
			//      (The Filtering state is handled above before the switch, routing
			//      keys to the list's text input so this branch never sees it.)
			//   2. If in a frame with a displayed pane, close the window (detach view).
			//   3. Otherwise no-op.
			if m.list.FilterState() == list.FilterApplied {
				m.list.ResetFilter()
				return m, m.previewCmd()
			}
			if m.inFrame() && m.displayedPaneID != "" {
				return m, m.closeWindowCmd()
			}
			return m, nil

		case key.Matches(msg, m.keys.Enter):
			return m.activateSelected()

		case key.Matches(msg, m.keys.New):
			it, ok := m.selectedItem()
			if !ok {
				// Need a tree context; no-op without a selection
				// (tool/model picker is deferred to M9).
				return m, nil
			}
			return m, m.launchCmd(launchSpec{
				tool:        m.resolveTool(it.treePath, it.tool),
				branch:      it.tree,
				treePath:    it.treePath,
				projectPath: it.projectPath,
				resume:      false,
			})

		case key.Matches(msg, m.keys.Remove):
			it, ok := m.selectedItem()
			if !ok || !it.isSession {
				return m.withToast("not a session — nothing to remove")
			}
			if it.isMain {
				return m.withToast("cannot remove the main checkout")
			}
			if it.live {
				return m, m.preflightRemoveCmd(it)
			}
			m.modal = modalState{
				kind:        modalRemoveConfirm,
				treePath:    it.treePath,
				branch:      it.tree,
				projectPath: it.projectPath,
				target:      it.liveTarget,
				paneKey:     it.captureTarget,
				tool:        it.tool,
				sessionID:   it.id,
			}
			return m, nil

		case key.Matches(msg, m.keys.Kill):
			it, ok := m.selectedItem()
			if !ok || !it.live || it.liveTarget == "" {
				return m.withToast("no live session to kill")
			}
			m.modal = modalState{
				kind:    modalKillConfirm,
				target:  it.liveTarget,
				paneKey: it.captureTarget,
				branch:  it.tree,
			}
			return m, nil

		case key.Matches(msg, m.keys.Worktree):
			it, ok := m.selectedItem()
			if !ok || !it.isSession {
				return m.withToast("worktree actions need a session row")
			}
			return m, m.worktreePreflightCmd(it)

		case key.Matches(msg, m.keys.ScreenFwd):
			m.mode = m.mode.next()
			m.relayout()
			return m, m.previewCmd()

		case key.Matches(msg, m.keys.ScreenBack):
			m.mode = m.mode.prev()
			m.relayout()
			return m, m.previewCmd()

		case key.Matches(msg, m.keys.CollapseSidebar):
			// Dedicated alias: toggle between the two-pane view and a collapsed
			// sidebar (preview full-width). Discoverability over the z/Z cycle.
			if m.mode == modeFullPreview {
				m.mode = modeNormal
			} else {
				m.mode = modeFullPreview
			}
			m.relayout()
			return m, m.previewCmd()
		}
	}

	// Delegate all other messages (j/k navigation, pagination, filter ticking…)
	// to the list component, then refresh the preview for the new selection.
	var cmd tea.Cmd
	prevIdx := m.list.Index()
	m.list, cmd = m.list.Update(msg)

	// If the selection changed, update the preview.
	if m.list.Index() != prevIdx {
		return m, tea.Batch(cmd, m.previewCmd())
	}
	return m, cmd
}

// View renders the full TUI as a single string.
func (m Model) View() string {
	if !m.ready {
		return "Initialising…"
	}

	body := m.bodyView()

	if txt := m.emptyStateText(); txt != "" {
		body = styles.emptyState.Render(txt)
	}

	// Footer hint changes based on the current mode.
	var footerText string
	switch {
	case m.cmdActive:
		footerText = m.cmdline.View()
	case m.modal.kind != modalNone:
		footerText = modalFooterHint(m.modal.kind)
	default:
		m.help.Width = m.width
		footerText = m.help.ShortHelpView(m.ShortHelp())
	}
	footer := styles.footer.MaxWidth(m.width).Render(footerText)

	// messageLine occupies the reserved top row (relayout always budgets one row
	// for it). toast takes priority over the load-error bar; when neither is set it
	// renders blank so the layout never reflows as messages come and go.
	var messageLine string
	switch {
	case m.toast != "":
		messageLine = styles.toast.MaxWidth(m.width).Render(m.toast)
	case m.loadErr != "":
		messageLine = styles.errorBar.MaxWidth(m.width).Render("Error loading sessions: " + m.loadErr)
	}

	// bodyRegionHeight is the space between the reserved message row and the footer.
	// Modal and help overlays are centred within it so the total never exceeds height.
	bodyRegionHeight := max(0, m.height-lipgloss.Height(footer)-messageBarHeight)

	overlayBox := ""
	switch {
	case m.showHelp:
		overlayBox = m.theme.helpOverlay.Render(m.help.FullHelpView(m.FullHelp()))
	case m.modal.kind != modalNone:
		overlayBox = renderModal(m.modal, m.theme.modalBox)
	}

	if overlayBox != "" {
		// Dim the body: strip its own SGR and re-render muted so the overlay box
		// stands out. (Body colors are intentionally dropped while a modal is up.)
		dimmed := styles.dimmedBody.Render(ansi.Strip(body))
		dimmed = lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(bodyRegionHeight).Render(dimmed)

		box := lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(bodyRegionHeight).Render(overlayBox)
		boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
		x := max(0, (m.width-boxW)/2)
		yOff := max(0, (bodyRegionHeight-boxH)/2)

		// Pad the dimmed body to the full region so composite has rows to write on.
		region := lipgloss.NewStyle().Width(m.width).Height(bodyRegionHeight).Render(dimmed)
		composited := composite(region, box, x, yOff)
		return lipgloss.JoinVertical(lipgloss.Left, messageLine, composited, footer)
	}

	body = lipgloss.NewStyle().MaxWidth(m.width).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, messageLine, body, footer)
}

// activateSelected opens the highlighted row: in frame mode it swaps a live agent
// into the main slot; in legacy mode it hands off the terminal; for an idle
// session it launches a resume. It is the shared implementation behind the Enter
// key and the ':attach' command, so the frame-vs-legacy decision lives in one
// place.
func (m Model) activateSelected() (tea.Model, tea.Cmd) {
	it, ok := m.selectedItem()
	if !ok || !it.isSession {
		return m.withToast("not a session — nothing to open")
	}
	if it.live && it.liveTarget != "" {
		if m.inFrame() {
			// Already displayed: the selected agent's home pane is the one
			// currently occupying the frame main slot — just focus it.
			// displayedPaneID holds the %N home pane id (set by swappedMsg
			// handler), which is the same value as it.captureTarget.
			// it.liveTarget is a "=sess:=win" window-target token — a different
			// id space that is never stored in displayedPaneID.
			if it.captureTarget != "" && it.captureTarget == m.displayedPaneID {
				return m, m.focusAgentCmd()
			}
			// Frame mode: swap the agent into the main slot.
			// swapInCmd sets m.swapping = true via pointer receiver; hoisted
			// out of the return tuple to guarantee mutation order.
			cmd := m.swapInCmd(it.captureTarget)
			return m, cmd
		}
		// Legacy mode: switch-client / attach-session terminal handover.
		// NEVER relaunch a live session: concurrent --resume can corrupt
		// the shared transcript.
		return m.attachTo(it.liveTarget)
	}
	return m, m.launchCmd(launchSpec{
		tool:        it.tool,
		sessionID:   it.id,
		branch:      it.tree,
		treePath:    it.treePath,
		projectPath: it.projectPath,
		resume:      true,
	})
}

// updateModal handles all key events when a modal is open. It is called
// exclusively from the KeyMsg case when m.modal.kind != modalNone.
func (m Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// modalNewSession has its own navigation keys before the generic handler.
	if m.modal.kind == modalNewSession {
		switch {
		case key.Matches(msg, m.keys.ClearFilter) || // esc
			(msg.Type == tea.KeyRunes && string(msg.Runes) == "n"):
			m.modal = modalState{}
			return m, nil

		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			if m.modal.action > 0 {
				m.modal.action--
			}
			return m, nil

		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			if m.modal.action < 2 {
				m.modal.action++
			}
			return m, nil

		case msg.Type == tea.KeyRunes && string(msg.Runes) == "1":
			m.modal.action = 0
			return m, nil

		case msg.Type == tea.KeyRunes && string(msg.Runes) == "2":
			m.modal.action = 1
			return m, nil

		case msg.Type == tea.KeyRunes && string(msg.Runes) == "3":
			m.modal.action = 2
			return m, nil

		case key.Matches(msg, m.keys.Enter):
			ms := m.modal
			m.modal = modalState{}
			switch ms.action {
			case 0:
				return m, m.worktreeCreateCmd(ms, nil)
			case 1:
				return m, m.runHereCmd(ms)
			default: // 2
				return m, m.runMainCmd(ms)
			}
		}
		// Swallow all other keys while newSession modal is open.
		return m, nil
	}

	// Trust confirm modal has its own distinct key set.
	if m.modal.kind == modalTrustConfirm {
		req := m.modal.trust
		if req == nil {
			m.modal = modalState{}
			return m, nil
		}
		switch {
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "a":
			// Approve always: persist to the trust store, then re-dispatch.
			store, err := trust.Load(filepath.Join(m.loader.BaseDir, trust.TrustFile))
			if err == nil {
				if serr := store.Approve(req.configPath, req.hash); serr != nil {
					m.modal = modalState{}
					return m.withToast("trust: save failed: " + serr.Error())
				}
			} else {
				// Fail closed: if the trust store can't be loaded, abort the action
				// (modal cleared, no re-dispatch) so hooks never run unapproved.
				m.modal = modalState{}
				return m.withToast("trust: load failed: " + err.Error())
			}
			dec := &trustDecision{allow: true, approvedHash: req.hash}
			return m.resumeAfterTrust(req, dec)

		case msg.Type == tea.KeyRunes && string(msg.Runes) == "o":
			// Approve once: re-dispatch without persisting.
			dec := &trustDecision{allow: true, approvedHash: req.hash}
			return m.resumeAfterTrust(req, dec)

		case msg.Type == tea.KeyRunes && (string(msg.Runes) == "d" || string(msg.Runes) == "n"),
			key.Matches(msg, m.keys.ClearFilter): // esc
			// Deny: re-dispatch with hooks skipped; create/remove still proceeds.
			dec := &trustDecision{allow: false}
			return m.resumeAfterTrust(req, dec)
		}
		// Swallow all other keys while trust modal is open.
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.ClearFilter) || // esc
		(msg.Type == tea.KeyRunes && string(msg.Runes) == "n"):
		// Cancel: close modal without any destructive action.
		m.modal = modalState{}
		return m, nil

	case key.Matches(msg, m.keys.Enter) ||
		(msg.Type == tea.KeyRunes && string(msg.Runes) == "y"):
		// Confirm.
		switch m.modal.kind {
		case modalRemoveConfirm:
			spec := m.modal
			m.modal = modalState{}
			return m, m.removeCmd(spec, false, false, nil)
		case modalForceConfirm:
			spec := m.modal
			m.modal = modalState{}
			return m, m.removeCmd(spec, true, true, nil)
		case modalKillConfirm:
			target := m.modal.target
			paneKey := m.modal.paneKey
			m.modal = modalState{}
			return m, m.killCmd(target, paneKey)
		}
	}
	// Swallow all other keys while modal is open.
	return m, nil
}

// resumeAfterTrust closes the trust modal and re-dispatches the pending Cmd
// with the resolved trustDecision.
func (m Model) resumeAfterTrust(req *trustReq, dec *trustDecision) (tea.Model, tea.Cmd) {
	m.modal = modalState{}
	if req.create != nil {
		return m, m.worktreeCreateCmd(*req.create, dec)
	}
	if req.remove != nil {
		r := req.remove
		return m, m.removeCmd(r.spec, r.force, r.skipPrep, dec)
	}
	return m, nil
}

// emptyStateText returns the empty-state message to display when discovery
// found no sessions, or "" when there are items or a load error is showing
// (the load-error bar takes precedence).
func (m Model) emptyStateText() string {
	if m.loadErr != "" || len(m.list.Items()) > 0 {
		return ""
	}
	if m.root != "" {
		return "No git repositories found under " + m.root
	}
	return "No git repositories found"
}

// relayout recomputes the list and preview dimensions from the current width,
// height and screen mode. Called from the WindowSizeMsg handler and whenever the
// screen mode changes (z/Z). All dimensions are clamped to ≥ 0.
func (m *Model) relayout() {
	paneHeight := max(0, m.height-footerHeight-messageBarHeight-2*borderSize)
	switch {
	case m.mode == modeFullList || m.mode == modeFullPreview:
		// Full modes show one pane; size both so the hidden pane is valid the
		// instant the mode flips (list keeps selection bookkeeping either way).
		full := max(0, m.width-2*borderSize)
		m.list.SetWidth(full)
		m.list.SetHeight(paneHeight)
		m.preview.Width = full
		m.preview.Height = paneHeight
	case m.width < minWideWidth:
		// Narrow: stack vertically, splitting the available height.
		topH := max(0, paneHeight/2-borderSize)
		botH := max(0, paneHeight-paneHeight/2-borderSize)
		m.list.SetWidth(max(0, m.width-2*borderSize))
		m.list.SetHeight(topH)
		m.preview.Width = max(0, m.width-2*borderSize)
		m.preview.Height = botH
	default:
		listWidth := m.width * listPanePercent / 100
		m.list.SetWidth(max(0, listWidth-2*borderSize))
		m.list.SetHeight(paneHeight)
		m.preview.Width = max(0, m.width-listWidth-2*borderSize)
		m.preview.Height = paneHeight
	}
}

// bodyView renders the pane area per the current screen mode.
func (m Model) bodyView() string {
	left := styles.leftPane.Render(m.list.View())
	right := styles.rightPane.Render(m.preview.View())
	switch {
	case m.mode == modeFullList:
		return left
	case m.mode == modeFullPreview:
		return right
	case m.width < minWideWidth:
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	default:
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
}

// reloadCmd returns a tea.Cmd that reloads the item list. Used after a
// successful remove or kill to refresh the display.
func (m Model) reloadCmd() tea.Cmd {
	if m.loader == nil {
		return nil
	}
	return m.loader.load()
}

// tickCmd returns a tea.Cmd that fires statusTickMsg after m.refresh elapses.
func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.refresh, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// statusPollCmd fires a status poll if no poll is already in-flight and the
// loader is set and the list is not in filter mode. Sets m.polling = true when
// it fires. Returns nil (drop) otherwise — mirrors the capturing guard pattern.
func (m *Model) statusPollCmd() tea.Cmd {
	if m.polling || m.loader == nil || m.list.SettingFilter() {
		return nil
	}
	m.polling = true
	return m.loader.statusPoll()
}

// selectedItem returns the currently selected list item as an item, or false.
func (m *Model) selectedItem() (item, bool) {
	sel := m.list.SelectedItem()
	if sel == nil {
		return item{}, false
	}
	it, ok := sel.(item)
	return it, ok
}

// previewCmd returns the appropriate tea.Cmd for the currently selected item:
//   - live item with a capture target: fires a CapturePane call (gated by capturing).
//   - idle item or no selection: refreshes the static detail view inline (no cmd).
func (m *Model) previewCmd() tea.Cmd {
	sel, ok := m.selectedItem()
	if !ok {
		m.refreshStaticPreview()
		return nil
	}

	if sel.live && sel.captureTarget != "" {
		if m.capturing {
			// An in-flight capture is already running; don't stack another.
			return nil
		}
		if m.loader == nil {
			// No loader (test / scaffold mode): show static detail.
			m.refreshStaticPreview()
			return nil
		}
		m.capturing = true
		target := sel.captureTarget
		ldr := m.loader
		ctx := ldr.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return func() tea.Msg {
			content, err := ldr.Tmux.CapturePane(ctx, target, 0)
			if err != nil {
				// Degrade to empty on error; don't abort or panic.
				content = ""
			}
			return previewMsg{content: content, target: target}
		}
	}

	// Idle item: static detail, no async call.
	m.refreshStaticPreview()
	return nil
}

// refreshStaticPreview updates the preview viewport with static detail content
// for the current selection. Called for idle items and when no loader is set.
func (m *Model) refreshStaticPreview() {
	content := m.detailContent()
	m.previewContent = content
	m.preview.SetContent(content)
}

// detailContent builds the preview pane text for the currently selected item.
func (m *Model) detailContent() string {
	sel := m.list.SelectedItem()
	if sel == nil {
		return "(no selection)"
	}
	it, ok := sel.(item)
	if !ok {
		return "(unknown item type)"
	}
	return fmt.Sprintf(
		"Title:   %s\nTool:    %s\nStatus:  %s\nUpdated: %s\n",
		it.title,
		it.tool,
		it.status.glyph(),
		it.relTime,
	)
}
