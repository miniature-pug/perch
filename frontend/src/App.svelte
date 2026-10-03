<script lang="ts">
  import { onMount, onDestroy, untrack } from "svelte";
  import ThemeProvider      from "./lib/ThemeProvider.svelte";
  import Sidebar            from "./lib/Sidebar.svelte";
  import Stage              from "./lib/Stage.svelte";
  import ShellDrawer        from "./lib/ShellDrawer.svelte";
  import ShellPanel         from "./lib/ShellPanel.svelte";
  import { initShellState, addShell, addSplitPartner, selectShell, removeShell, planToggleSplit, type ShellState } from "./lib/shellPanes";
  import Terminal           from "./lib/Terminal.svelte";
  import Editor             from "./lib/Editor.svelte";
  import Preview            from "./lib/Preview.svelte";
  import FileTree           from "./lib/FileTree.svelte";
  import { isPreviewable, previewKind } from "./lib/preview";
  import { focusOnMount, countUp } from "./lib/actions";
  import { keepHome, adoptInto } from "./lib/portal";
  import DiffView           from "./lib/DiffView.svelte";
  import { shouldFocusAwaitingInput, isViewingAgentPane } from "./lib/engagement";
  import MenuBar            from "./lib/MenuBar.svelte";
  import CommandPalette     from "./lib/CommandPalette.svelte";
  import NewSessionDialog   from "./lib/NewSessionDialog.svelte";
  import ConfirmDialog      from "./lib/ConfirmDialog.svelte";
  import HelpDialog         from "./lib/HelpDialog.svelte";
  import SettingsPanel      from "./lib/SettingsPanel.svelte";
  import DragDrop           from "./lib/DragDrop.svelte";
  import { registerOsFileDrop } from "./lib/osFileDrop";
  import { SvelteSet }      from "svelte/reactivity";
  import { layout }         from "./lib/stores/layout.svelte";
  import { mode }           from "./lib/stores/mode.svelte";
  import { settings, sameAlwaysRule } from "./lib/stores/settings.svelte";
  import ApprovalCard       from "./lib/ApprovalCard.svelte";
  import NotificationHub    from "./lib/NotificationHub.svelte";
  import { getDnd, setDnd, addBlocking, addAmbient, addRoutine, getItems, markRead, clearRead, markAllRead, markReadForWorkspace, dropForWorkspace, dropAwaitingInputForWorkspace } from "./lib/stores/notifications.svelte";
  import CleanupPanel from "./lib/CleanupPanel.svelte";
  import { listWorkspaces, createWorkspace, setWorkspaceTitle, workspaceForBranch, removeWorkspace, openWorkspace, closeWorkspace, closeShell, revealInFiles, onAgentEvent, onNotify, onFsChanged, onWorkspaceAttach, onWorkspaceRelaunch, approve, pendingApprovals, branches, readFile, setWindowFocus, writeToPty, discoverRepos, diffStat, listStaleSessions, forceRemoveWorkspace, clipboardSetText, getSettings, homeShellCwd as fetchHomeShellCwd } from "./lib/wails";
  import type { WorkspaceVM, ApprovalReq, StaleSessionVM, AgentState, RepoInfo, AlwaysRule } from "./lib/wails";
  import { UNDO_REMOVE_DELAY_MS, GCHORD_TIMEOUT_MS, SIDEBAR_MIN_W, SIDEBAR_MAX_W, SHELL_MIN_H, SHELL_MAX_H, RESIZE_STEP_PX, THEMES, MIME_SESSION, MENTION_PREFIX, AGENT_CLAUDE, AGENT_OPENCODE } from "./lib/constants";

  let workspaces      = $state<WorkspaceVM[]>([]);
  let activeId        = $state<string | null>(null);
  // Sessions with a live pty in this app run. A session is "open" once openWorkspace
  // succeeds. It stays open until it closes or the user removes it. openIds drives
  // three things: the focus-vs-reopen routing in onSelect, the sidebar "closed" dim
  // cue, and the close/remove cleanup.
  // openIds is a SvelteSet, so .add(), .delete(), and .has() are reactive. The
  // in-pane reopen overlay and the Sidebar "closed" cue both read openIds.has() directly.
  let openIds         = new SvelteSet<string>();
  // Sessions opened at least once in this app run. A COLD persisted session (never
  // opened this run) shows the resume preview on a sidebar click. The user confirms
  // its branch, agent, and last-active time before perch spawns a pty. A DORMANT
  // session (opened, then closed, this run) reopens DIRECTLY on click. This matches
  // the Enter key, a notification click, and the in-pane Reopen overlay: the user
  // just had it live, so the extra modal would add friction with no benefit (F22).
  // everOpened prunes an id only on a genuine removal. A closed-but-kept session
  // must stay "dormant" on close or exit, not lose its entry.
  let everOpened      = new SvelteSet<string>();
  // Per-session terminal epoch. It bumps on every genuine (re)open, so the agent
  // terminal-zone {#key} remounts a fresh xterm. A respawned pty must not write
  // over a stale buffer. A view switch or a focus change does NOT bump the epoch,
  // so the scroll buffer survives those.
  let termEpoch       = $state<Record<string, number>>({});
  // Per-session shell terminal state: the tab list, the active (primary) shell, and
  // the split partner (right pane, null means no split). This state is per-run and
  // in-memory, because ptys die on restart. It is keyed by session id. The effect
  // below seeds and prunes it. Only the shell* callbacks mutate it, and they drive
  // the pure transitions in shellPanes.ts.
  let shellStatesFor  = $state<Record<string, ShellState>>({});
  // Per-session code-view file selection. Each open session keeps its own
  // FileTree, Editor, and Preview mounted (hidden with display), so perch must
  // track the open file for each session on its own. A single top-level
  // value would load one session's path against another session's worktree, and
  // would reset on every switch. This map is keyed by session id, like termEpoch
  // and fsVersion.
  let codePaths       = $state<Record<string, string | null>>({});
  let previewContent  = $state<string>("");
  // Per-session approval QUEUE. A session's agent can have more than one tool call
  // waiting at once, because each is a distinct blocking hook. A single-valued map
  // would drop all but the last one and hang those hooks forever. The head of each
  // queue (index 0) is the request shown for that session. Resolving it pops the
  // queue, so the next request in line surfaces. Each reqId is unique per request,
  // so a push deduplicates on reqId.
  let approvals       = $state<Record<string, ApprovalReq[]>>({});
  // Per-session fs version: bumps on ANY file change in that session's worktree.
  // This drives the FileTree re-list and the DiffView refresh, since both cover
  // every file.
  let fsVersion  = $state<Record<string, number>>({});
  // Per-path fs version: bumps only when that exact absolute path changes on disk.
  // This drives the Editor reload and the Preview content refresh, so an agent
  // write to an unrelated file never disturbs the file the user is editing or
  // previewing (F15). It is keyed by absolute path and shared across sessions,
  // because a path is unique.
  let fsPathVersion = $state<Record<string, number>>({});
  let wsDiffStats = $state<Record<string, { added: number; removed: number; files: number }>>({});

  // Acknowledged "awaiting-input" attention signals. The left-pane "asking you a
  // question" signal is a BACKGROUND cue. It pulls the user's eye to a session the
  // user is NOT looking at. Once a session is active and its agent pane is the
  // visible view, the user has seen the question. App then adds its id here, and
  // the Sidebar suppresses the signal for it. A fresh awaiting-input event deletes
  // the id, so a NEW question raises the signal again. This ack set stays
  // independent of the polled ws.state on purpose: a ListWorkspaces re-fetch can
  // overwrite a locally-mutated state, but it never touches this set. attnAck is a
  // SvelteSet, so .add(), .delete(), and .has() are reactive.
  let attnAck = new SvelteSet<string>();

  // Acknowledged done/errored attention signals. The left-pane row-level finish
  // signal (bar and glow) for a finished or failed session is a background
  // eye-pull. Once the user OPENS that session and makes it active, the signal has
  // done its job. App then adds its id here, and the Sidebar stops showing the
  // signal for it, while the persistent check-mark or cross status word stays.
  // Unlike the awaiting-input signal, just VIEWING the row acknowledges a finish:
  // a finish needs no agent-pane interaction to count as seen. A FRESH done or
  // errored event in the agent:event handler deletes the id, so a NEW finish
  // raises the signal again, even on a session the user already looked at.
  // attnDoneAck is a SvelteSet, so .add(), .delete(), and .has() are reactive.
  let attnDoneAck = new SvelteSet<string>();

  // Awaiting-input auto-focus. termRefs is a per-session ref map to each open agent
  // terminal. App uses it to route the keyboard to the ACTIVE terminal without a
  // click. emphasizeInput is a transient flag that pulses when the ACTIVE agent
  // asks for input. termRefs is keyed by session id, because every open session
  // now keeps its own Terminal mounted.
  let termRefs       = $state<Record<string, { focus: () => void }>>({});
  let emphasizeInput = $state(false);
  // Per-session ref to the agent terminal-zone DOM node. The split session's
  // Terminal mounts ONCE, in the primary keep-alive loop. When it becomes the
  // secondary pane, App moves its node into the secondary host (see lib/portal.ts).
  // This keeps a split toggle from rebuilding a blank xterm (F10a).
  let termZoneEls    = $state<Record<string, HTMLElement | undefined>>({});

  // Repo discovery. App fills this lazily, the FIRST time the New Session dialog
  // opens. discoverRepos walks the filesystem, so it must not re-run on every open
  // (F21). The discoveredReposScanned flag guards it, and a failure resets the flag
  // so a later open can retry. App keeps full RepoInfo records, not just paths, so
  // the New Session repo picker can show friendly names. See repoInfoByPath below.
  let discoveredRepos = $state<RepoInfo[]>([]);
  let discoveredReposScanned = false;

  // Pending removals. Each entry is an optimistically-hidden session with a
  // scheduled, real removeWorkspace call. An array lets App handle several
  // concurrent removals with no special-case logic.
  interface PendingRemoval {
    ws: WorkspaceVM;
    timer: ReturnType<typeof setTimeout>;
    // The selection at schedule time. This lets Undo re-select the removed
    // session if it was the active pane, the split pane, or both.
    prevActiveId: string | null;
    prevSplitId: string | null;
  }
  let pendingRemovals = $state<PendingRemoval[]>([]);

  // Keymap state machine helpers
  let pendingG     = $state(false);
  // Auto-clear timer for the `g` chord prefix. A stray `g` must not silently
  // swallow the next key forever, and the transient "g…" indicator must not
  // linger (F54). This timer is set when `g` is armed, and cleared when the
  // chord resolves.
  let pendingGTimer: ReturnType<typeof setTimeout> | undefined;
  let pendingLeave = $state(false);
  let filtering    = $state(false);
  let filterQuery  = $state("");

  // Load file content for the ACTIVE session's previewable, non-image file. Only
  // the active session's Preview is on screen, so a single previewContent value
  // tracks the active codePath. This effect re-reads the file when it changes on
  // disk, that is, when its fsPathVersion bumps, and never on an unrelated write
  // (F15). The cancellation guard stops a stale readFile resolve from overwriting
  // newer content.
  $effect(() => {
    const p = activeCodePath;
    if (p) fsPathVersion[p]; // track: re-read when THIS file changes on disk
    if (!p || !isPreviewable(p) || previewKind(p) === "image") { previewContent = ""; return; }
    let cancelled = false;
    readFile(p)
      .then((c) => { if (!cancelled) previewContent = c; })
      .catch(() => { if (!cancelled) previewContent = ""; });
    return () => { cancelled = true; };
  });

  // Acknowledge the active session's awaiting-input signal once the user is
  // actually looking at its agent pane. This effect tracks activeId, layout.view,
  // and the active session's state. A fresh awaiting-input event first DELETES the
  // id from attnAck, in the agent:event handler, so the eye-pull can raise it again
  // for a BACKGROUND session. But if that session is the one the user is actively
  // viewing, this effect re-runs on the state change and re-acks the id right away.
  // The left-pane signal is redundant while the pane is on screen, because the
  // in-pane auto-focus pulse already draws the eye. The effect untracks the
  // mutation, so writing attnAck does not feed back into the effect.
  $effect(() => {
    const id = activeId;
    active?.state; // track: re-ack a fresh awaiting-input on the viewed session
    const viewing = id != null && isViewingAgentPane(id, activeId, layout.view);
    if (viewing) untrack(() => attnAck.add(id!));
  });

  // Acknowledge a done or errored session the moment the user opens it and makes
  // it active. Unlike the awaiting-input ack, just VIEWING the row counts here: a
  // finished or failed turn needs no agent-pane interaction to count as "seen".
  // This effect tracks activeId and the active session's state. When that state is
  // done or errored, it records the id so the Sidebar drops the row's finish
  // signal, while the check-mark or cross status word stays. A fresh done or
  // errored event deletes the id first, in the agent:event handler. So if the user
  // is watching a session finish, this effect re-acks it right away and the row
  // never shows the finish signal after the user leaves. A finish that lands on a
  // BACKGROUND session stays un-acked and keeps the signal until the user opens it.
  // The effect untracks the mutation, so it does not feed back into itself.
  $effect(() => {
    const id = activeId;
    const st = active?.state;
    if (id != null && (st === "done" || st === "errored")) {
      untrack(() => attnDoneAck.add(id));
    }
  });

  // Auto-read the hub notifications for the session the user is now looking at.
  // Switching to a session, or refocusing the window while it is already active,
  // counts as catching up on its events, so they stop bumping the bell badge. This
  // matches the user's request for "auto-read on switch". It mirrors the attnAck
  // effect above, the same pattern used for the sidebar's own awaiting-input
  // signal. The effect untracks the call, so markReadForWorkspace's internal
  // `items` read does not make this effect re-run on every unrelated notification.
  // It should fire only on an active-session or focus CHANGE. A notification that
  // arrives while its session is ALREADY on screen stays unread on purpose, and
  // still bumps the bell: auto-read catches up a CHANGE of session or focus, not
  // a live event on the session the user is already watching (see onNotify below).
  // The effect gates on windowFocused, so notifications that land while the user
  // has switched to another app still accumulate, and still send an OS toast,
  // until the user returns.
  $effect(() => {
    const id = activeId;
    if (id != null && windowFocused) untrack(() => markReadForWorkspace(id));
  });

  // Seed a default shell for every mounted session, so its ShellPanel has a tab to
  // render. The cell's mount is what calls openShell. This effect also prunes shell
  // state for sessions that no longer exist, because the user removed them. It
  // tracks mountedWorkspaces and workspaces. The mutations run untracked, so writing
  // shellStatesFor never re-invalidates this effect. The effect converges once every
  // mounted session is seeded and no stale ids remain.
  $effect(() => {
    const mountedIds = mountedWorkspaces.map(w => w.id);
    const liveIds = new Set(workspaces.map(w => w.id));
    untrack(() => {
      for (const id of mountedIds) if (!shellStatesFor[id]) shellStatesFor[id] = initShellState(id);
      for (const id of Object.keys(shellStatesFor)) if (!liveIds.has(id)) delete shellStatesFor[id];
    });
  });

  // Shell-tab actions, the ShellPanel callbacks. The pure shellPanes.ts transitions
  // hold all the list, active, and split logic. These functions apply the new state
  // and run the pty side effects: closeShell on close, and openShell driven by the
  // freshly mounted cell. When a closed tab would empty the drawer, App replaces it
  // with a fresh shell. The drawer is never left empty.
  function shellNew(wsId: string) {
    const st = shellStatesFor[wsId];
    if (st) shellStatesFor[wsId] = addShell(st, wsId).state;
  }
  function shellSelect(wsId: string, id: string) {
    const st = shellStatesFor[wsId];
    if (st) shellStatesFor[wsId] = selectShell(st, id);
  }
  function shellClose(wsId: string, id: string) {
    const st = shellStatesFor[wsId];
    if (!st) return;
    closeShell(id).catch(() => {}); // reap the pty; idempotent even if it already exited
    let { state, empty } = removeShell(st, id);
    if (empty) state = addShell(state, wsId).state; // replacement cell mounts, which calls openShell
    shellStatesFor[wsId] = state;
  }
  function shellToggleSplit(wsId: string) {
    const st = shellStatesFor[wsId];
    if (!st) return;
    const plan = planToggleSplit(st);
    if (plan.off) shellStatesFor[wsId] = { ...st, splitId: null };
    else if (plan.partnerId) shellStatesFor[wsId] = { ...st, splitId: plan.partnerId };
    else shellStatesFor[wsId] = addSplitPartner(st, wsId).state; // new partner cell mounts, which calls openShell
  }

  // Invariant: the active session is never ALSO the split session. Each open
  // session keeps exactly one Terminal, bound to its paneId, mounted once in the
  // primary keep-alive loop. App relocates the split session's node into the
  // secondary pane (see lib/portal.ts), and never creates a second instance. If
  // navigation makes the split session active, that one node would need to sit in
  // the primary pane and the secondary pane at once. This effect clears the split
  // id in that case, so the secondary pane shows its picker instead.
  $effect(() => {
    if (layout.split && layout.splitId !== null && layout.splitId === activeId) layout.setSplitId(null);
  });

  // Dialog / overlay state
  let newSessionOpen        = $state(false);
  let newSessionInitialAgent = $state<string | null>(null);
  // Inline create error shown in the New Session dialog. A genuine user error, such
  // as a branch-name collision, must read as actionable copy, never raw git output.
  // App feeds the human-readable message here after a failed create (F26a).
  let createError           = $state<string | null>(null);
  let confirmRemove         = $state<WorkspaceVM | null>(null);
  let notifOpen             = $state(false);
  let helpOpen              = $state(false);
  // Which Help panel to show. The Help menu's two entries open distinct views, one
  // for shortcuts and one for about. The `?` and F1 keys open the full help
  // (F24, F53).
  let helpSection           = $state<"shortcuts" | "about" | "all">("all");
  let settingsOpen          = $state(false);
  let staleSessions         = $state<StaleSessionVM[]>([]);
  let homeShellCwdValue     = $state<string>("");
  let staleBannerDismissed  = $state(false);
  let cleanupOpen           = $state(false);
  let confirmDirty          = $state<WorkspaceVM | null>(null);

  const active          = $derived(workspaces.find(w => w.id === activeId) ?? null);
  // The active session's open code-view file. This is per-session, and null when
  // no file is open.
  const activeCodePath  = $derived(activeId ? (codePaths[activeId] ?? null) : null);
  const unreadCount     = $derived(getItems().filter(n => !n.read).length);
  // Every session with a live pty in this app run. The agent terminals and shell
  // drawers loop over this list, so each open session keeps its own kept-alive
  // xterm, hidden with display and never keyed away. Switching sessions toggles
  // visibility instead of rebuilding a blank pane. openIds is a SvelteSet and
  // workspaces is $state, so openWorkspaces stays reactive to both.
  const openWorkspaces  = $derived(workspaces.filter(w => openIds.has(w.id)));
  // Sessions whose agent Terminal stays MOUNTED. This list holds every open
  // session, plus the active session if its pty has just exited and it is not in
  // openIds, plus the split session. The split session's Terminal mounts here
  // ONCE and App relocates it into the secondary pane. See the primary {#each} and
  // lib/portal.ts. Keeping the exited-but-active pane mounted preserves its final
  // xterm buffer, the "[process exited]" line, under the Reopen overlay, instead
  // of unmounting the xterm the instant the pty dies. chooseSplit always opens the
  // split session first, so it is normally already in openWorkspaces. The explicit
  // union keeps the two lists consistent even when splitId is assigned with no
  // open, as a backstop for tests and edge cases.
  // Note: this list keys on splitId, not on split being ON, so the split session
  // stays mounted while it is *assigned* to the secondary pane, even when the user
  // toggles the pane off and keeps the assignment. That is what lets a split
  // off-to-on toggle show the SAME live terminal again, instead of rebuilding a
  // blank one.
  const mountedWorkspaces = $derived((() => {
    let mounted = active && !openIds.has(active.id)
      ? [...openWorkspaces, active]
      : openWorkspaces;
    if (layout.splitId) {
      const splitWs = workspaces.find(w => w.id === layout.splitId);
      if (splitWs && !mounted.some(w => w.id === splitWs.id)) mounted = [...mounted, splitWs];
    }
    return mounted;
  })());

  // The split session's terminal-zone node, once mounted in the primary loop.
  // The secondary pane host adopts this exact node, with use:adoptInto, so the
  // split terminal keeps its live xterm buffer across split on and off toggles.
  const splitZoneEl = $derived(layout.splitId ? termZoneEls[layout.splitId] : undefined);

  // Visible sessions. This excludes any session pending an optimistic removal.
  const pendingRemovalIds = $derived(new Set(pendingRemovals.map(p => p.ws.id)));
  const visibleWorkspaces = $derived(workspaces.filter(w => !pendingRemovalIds.has(w.id)));

  // Apply the user-defined order. Ids in layout.order come first, in that order.
  // The remaining sessions, not yet in the order, follow in backend order.
  const orderedWorkspaces = $derived((() => {
    const order = layout.order;
    if (!order.length) return visibleWorkspaces;
    const indexed = new Map(visibleWorkspaces.map((w, i) => [w.id, { w, i }]));
    const head = order.map(id => indexed.get(id)?.w).filter(Boolean) as typeof visibleWorkspaces;
    const headSet = new Set(order);
    const tail = visibleWorkspaces.filter(w => !headSet.has(w.id));
    return [...head, ...tail];
  })());

  // Filtered session list for Sidebar. The j and k keys also operate on this list
  // when filtering is on. It uses orderedWorkspaces, so an optimistically removed
  // item drops out right away.
  const shownWorkspaces = $derived(
    filtering && filterQuery
      ? orderedWorkspaces.filter(w => w.title.toLowerCase().includes(filterQuery.toLowerCase()))
      : orderedWorkspaces
  );

  // Reorder callback from Sidebar. It moves draggedId to the position of targetId.
  function handleReorder(draggedId: string, targetId: string) {
    const ids = orderedWorkspaces.map(w => w.id);
    const from = ids.indexOf(draggedId);
    const to   = ids.indexOf(targetId);
    if (from === -1 || to === -1 || from === to) return;
    const next = [...ids];
    next.splice(from, 1);
    next.splice(to, 0, draggedId);
    layout.setOrder(next);
  }

  // Derived repo list for NewSessionDialog. It is the union of session-derived
  // paths and any paths discoverRepos() returns. discoverRepos() fills lazily,
  // on dialog open.
  const repos = $derived([...new Set([
    ...workspaces.map(w => w.worktreePath),
    ...discoveredRepos.map(r => r.path),
  ])]);

  // Path-to-RepoInfo lookup for the New Session repo picker's friendly-name labels
  // (F#5). A session-derived worktree path has no discoverRepos() entry. The
  // dialog falls back to the raw path for those.
  const repoInfoByPath = $derived<Record<string, RepoInfo>>(
    Object.fromEntries(discoveredRepos.map(r => [r.path, r]))
  );

  // Off-functions captured from wails event subscriptions. onMount subscribes to
  // these synchronously.
  let offAgentEvent:        (() => void) | null = null;
  let offNotify:            (() => void) | null = null;
  let offFsChanged:         (() => void) | null = null;
  let offWorkspaceAttach:   (() => void) | null = null;
  let offWorkspaceRelaunch: (() => void) | null = null;
  let offOsFileDrop:        (() => void) | null = null;

  // Whether the OS window has focus now. App reports this to the backend,
  // which gates OS desktop notifications, and also reads it locally, to auto-read
  // a session's hub notifications only while the user is actually looking at the
  // app.
  let windowFocused = $state(true);

  // Window focus and blur handlers. These report focus state to the backend, so
  // it can gate OS desktop notifications and fire them only when the window is
  // unfocused.
  function onWindowFocus() { windowFocused = true;  setWindowFocus(true).catch(() => {}); }
  function onWindowBlur()  { windowFocused = false; setWindowFocus(false).catch(() => {}); }

  // Aggregate the +N/-N diff stat for one session.
  // A missing or non-git worktree must not throw. The catch block suppresses
  // errors silently.
  async function refreshDiffStat(ws: WorkspaceVM) {
    try {
      const files = await diffStat(ws.worktreePath);
      let added = 0, removed = 0;
      for (const f of files) { added += f.added; removed += f.removed; }
      wsDiffStats = { ...wsDiffStats, [ws.id]: { added, removed, files: files.length } };
    } catch {
      // non-git or missing worktree: leave any existing entry untouched
    }
  }

  // The active session just asked for input. This routes the user to its pane, so
  // the user can answer right away, since the question is answered in the agent's
  // own TUI. App calls this only for the active session on the agent view. See
  // shouldFocusAwaitingInput.
  function focusAwaitingInput() {
    if (mode.current === "normal") mode.enterTerminal();
    emphasizeInput = false; // reset so the pulse restarts even on a rapid re-ask
    requestAnimationFrame(() => {
      emphasizeInput = true;
      termRefs[activeId ?? ""]?.focus();
    });
  }

  // Capture-phase pointerdown on the app root. When in terminal mode, if the
  // click target is NOT inside a .terminal or [data-terminal-zone] element, this
  // handler leaves terminal mode and returns to NORMAL. It uses capture, so it
  // fires before any child handler, but it never calls preventDefault or
  // stopPropagation, so other handlers such as menus, buttons, and xterm still
  // receive the event.
  function onAppPointerDown(e: PointerEvent) {
    if (mode.current !== "terminal") return;
    const target = e.target as Element | null;
    if (!target) return;
    // Stay in terminal mode if the click is inside the terminal zone.
    if (target.closest("[data-terminal-zone]")) return;
    mode.leaveTerminal();
  }

  // General copy on non-terminal surfaces (B3). WebKit2GTK's navigator.clipboard
  // is unreliable, so the DiffView, dialogs, and general page text have no
  // dependable copy route, even though the user can still select their text. The
  // terminal owns Ctrl-Shift-C for its own copy, through xterm or the terminal
  // context menu, so this handler skips events that start inside a
  // [data-terminal-zone]. Otherwise, on Ctrl-Shift-C with a non-empty document
  // selection, it routes the selection through the host clipboard binding, which
  // is WebKit2GTK-native and not navigator.clipboard.
  function onCopyKeydown(e: KeyboardEvent) {
    if (!(e.ctrlKey && e.shiftKey) || (e.key !== "c" && e.key !== "C")) return;
    if ((e.target as HTMLElement)?.closest?.("[data-terminal-zone]")) return;
    const sel = window.getSelection()?.toString() ?? "";
    if (!sel) return;
    clipboardSetText(sel).catch(() => {});
    e.preventDefault();
  }

  onMount(async () => {
    // Report initial focus state and register focus/blur listeners.
    windowFocused = document.hasFocus();
    setWindowFocus(document.hasFocus()).catch(() => {});
    window.addEventListener("focus", onWindowFocus);
    window.addEventListener("blur",  onWindowBlur);
    // General Ctrl-Shift-C copy for non-terminal surfaces (B3).
    document.addEventListener("keydown", onCopyKeydown);

    // Register the single global OS file-drop handler. It routes absolute paths
    // from the Wails native OnFileDrop event to the pane under the drop point (see
    // lib/osFileDrop.ts). It is a no-op when the Wails runtime is absent, as in
    // tests.
    offOsFileDrop = registerOsFileDrop();

    // Subscribe synchronously BEFORE any await, so the off-functions are always
    // captured.
    offAgentEvent = onAgentEvent((ev) => {
      // Enqueue an approval BEFORE the session-listed guard below. The queue is
      // keyed only by ev.workspaceId and needs no `ws` object. App must still
      // capture a one-shot approval frame that arrives before the session is
      // listed, on a fresh open, or after a webview reload. Dropping it here would
      // wedge the agent forever, because its hook blocks and waits for a decision
      // that can never come.
      if (ev.approval) {
        // PUSH onto the session's queue. This dedupes by reqId, so a re-delivered
        // event never enqueues the same request twice.
        const q = approvals[ev.workspaceId] ?? [];
        if (!q.some(r => r.reqId === ev.approval!.reqId)) {
          approvals[ev.workspaceId] = [...q, ev.approval];
        }
      }
      const ws = workspaces.find(w => w.id === ev.workspaceId);
      if (!ws) return;
      const prev = ws.state;
      if (ev.state) ws.state = ev.state;
      // The AGENT process exited, by a graceful /exit or a crash, while its login
      // shell is still alive, so there is no pty:exit event (F32). This routes
      // through the same session-ended path as pty:exit: it prunes approvals, drops
      // the id from openIds, and shows the existing "session ended / Reopen"
      // overlay. It keeps ws.state at "exited", so the sidebar shows the distinct
      // terminal state. The handler returns early here, because none of the
      // running/idle/done edge handling below applies to a terminal exit.
      if (ev.state === "exited") {
        handleAgentExit(ev.workspaceId, "exited");
        return;
      }
      // A state transition that RESOLVES a blocking condition leaves its blocking
      // notification lit forever if App does not drop it. Two kinds of transition
      // resolve a blocking condition: the user answers a question in the agent's
      // own TUI, so perch auto-allows with no decideOne call, or an error clears.
      // This drops the session's blocking notification on the edge into a resolved
      // state, but NOT on "errored", because an unresolved error must keep its
      // notification. This is a harmless backstop for approval notifications too:
      // the decideOne queue-empty clear already covers those.
      if (ev.state && prev !== ev.state &&
          (ev.state === "running" || ev.state === "idle" || ev.state === "done")) {
        // Heal the openIds latch (B1). A live agent event on this edge proves the
        // pane's pty exists. openIds is a one-way latch: openSession adds an id,
        // and close, exit, or an open-reject deletes it. So a late or stale
        // "exited" event, from an old shell's exit during a reopen teardown, or
        // from an openWorkspace reject, re-latches the "session ended / Reopen"
        // overlay with no way to self-heal. This re-adds the id here, so a live
        // event heals the latch. The add is idempotent when the id is already
        // present. It is scoped to the running/idle/done edge, never to
        // awaiting-approval or awaiting-input, so a pre-open approval or question
        // event for a COLD session never marks it open or suppresses its resume
        // preview. The overlay stays gated on openIds.
        openIds.add(ev.workspaceId);
        dropForWorkspace(ev.workspaceId);
      }
      // Leaving awaiting-input for any OTHER state supersedes this session's
      // still-unread "Question" blocking notification. The question is moot once
      // the agent moves on. The agent typically moves straight into
      // awaiting-approval for its next tool, so dropForWorkspace above, scoped to
      // running/idle/done, never fires, and the stale Question notification would
      // otherwise linger next to the fresh "Approval needed" notification. This
      // drops ONLY the awaiting-input-sourced notification. A pending "Approval
      // needed" notification stays untouched; claude may have SEVERAL queued, all
      // in state awaiting-approval. This runs on the LEAVING edge: the new
      // state's own notification rides a later "notify" event, so it is added
      // AFTER this drop and is never caught by it. The check gates on
      // ev.state !== "awaiting-input", so a SECOND question in the same turn,
      // with the state unchanged, keeps its notification.
      if (prev === "awaiting-input" && ev.state && ev.state !== "awaiting-input") {
        dropAwaitingInputForWorkspace(ev.workspaceId);
      }
      // A FRESH finish, done or errored, un-acknowledges the session, so its row
      // shows the finish signal again, even if the user had already seen a
      // PREVIOUS finish. This gates on a real transition, prev !== ev.state, so a
      // redundant done frame, such as a reconnect snapshot, never re-raises the
      // signal for an already-seen finish. If the session is the one the user is
      // actively viewing, the ack $effect above re-acks it right away, because
      // watching it finish counts as seeing it. A backgrounded finish stays
      // un-acked and keeps the signal until the user opens it.
      if ((ev.state === "done" || ev.state === "errored") && prev !== ev.state) {
        attnDoneAck.delete(ev.workspaceId);
      }
      // A FRESH question un-acknowledges the session, so its left-pane
      // "asking you" signal raises again, even on a backgrounded, already-acked
      // session. This keys on the QUESTION EVENT itself, kind === "question", not
      // on the state-value edge. claude can emit a SECOND AskUserQuestion in the
      // same turn with no intervening Stop or running state, so prev would still
      // be "awaiting-input" and an edge check would skip the delete, leaving the
      // signal suppressed for the new question. Every question event carries a
      // distinct ask, so this deletes the id unconditionally. If the session is
      // active and viewed, the ack $effect re-acks it right away, because the
      // in-pane pulse already draws the eye. A backgrounded session raises the
      // signal again.
      if (ev.kind === "question") {
        attnAck.delete(ev.workspaceId);
      }
      // This focuses the agent pane only for the ACTIVE session, only on the
      // agent view, only on the edge, and only when no App modal or overlay and
      // no command palette owns the keyboard. Without that last guard, a
      // background awaiting-input would flip to terminal mode and pull focus into
      // the hidden pty while Settings or the palette is open. That would route the
      // user's keystrokes, Escape included, into the agent instead (F16). The
      // left-pane sidebar pulse from the attnAck path above still fires either
      // way. Only the focus theft is suppressed.
      if (ev.state && shouldFocusAwaitingInput(prev, ev.state, ev.workspaceId, activeId, layout.view)
          && !modalOpen && mode.current !== "command") {
        focusAwaitingInput();
      }
    });

    offNotify = onNotify((n) => {
      if      (n.tier === "blocking") addBlocking(n.workspaceId, n.title, n.body, undefined, n.state);
      else if (n.tier === "ambient")  addAmbient (n.workspaceId, n.title, n.body, undefined, n.state);
      else                            addRoutine (n.workspaceId, n.title, n.body, undefined, n.state);
      // This deliberately does NOT auto-read on arrival, even for the session on
      // screen. A turn completing, or an approval landing, while the user is
      // watching SHOULD still bump the bell, so the live signal is never swallowed.
      // Auto-read happens only on an active-session or focus CHANGE, in the
      // $effect above. That is, it fires when the user switches TO a session and
      // catches up on what accumulated while elsewhere. Marking a fresh event read
      // here once silently ate the opencode "Turn complete" signal for the session
      // being watched. Do not re-add that behavior.
    });

    offFsChanged = onFsChanged((p) => {
      // Session-wide bump. This re-lists the FileTree and refreshes the DiffView,
      // since both span every file in the worktree.
      fsVersion[p.workspaceId] = (fsVersion[p.workspaceId] ?? 0) + 1;
      // Per-path bump. Only the Editor or Preview showing THIS exact file reloads,
      // so an agent write to an unrelated file never disturbs the edited buffer
      // (F15).
      if (p.path) fsPathVersion[p.path] = (fsPathVersion[p.path] ?? 0) + 1;
      const ws = workspaces.find(w => w.id === p.workspaceId);
      if (ws) refreshDiffStat(ws);
    });

    offWorkspaceAttach = onWorkspaceAttach((p) => {
      // A background `perch attach` must not hijack the app while a modal or
      // overlay is open. Routing it through onSelect would swap the active
      // session, or the target of an open resume preview, out from under the
      // user. This drops the event instead, because the app behind a modal is
      // inert (F18).
      if (modalOpen) return;
      // Find by exact worktreePath first, then fall back to a fuzzy match on
      // title, branch, or path.
      const q = p.query;
      const exact = workspaces.find(w => w.worktreePath === q);
      const fuzzy = workspaces.find(w =>
        w.worktreePath.toLowerCase().includes(q.toLowerCase()) ||
        w.title.toLowerCase().includes(q.toLowerCase()) ||
        w.branch.toLowerCase().includes(q.toLowerCase())
      );
      const target = exact ?? fuzzy ?? null;
      if (target) onSelect(target.id);
    });

    offWorkspaceRelaunch = onWorkspaceRelaunch((p) => {
      // A backend-initiated, conversation-preserving relaunch, from perch reload
      // or an env-to-agent hop, respawned the agent pty under the same paneId.
      // This bumps the terminal epoch, so the agent terminal-zone {#key} remounts
      // a FRESH xterm. Otherwise the new `claude --resume` redraws over the old
      // buffer, causing garbled text, and keeps a stale grid size. It mirrors
      // openSession's remount, but makes no active-selection or openIds change,
      // since the session is already open and active; the reload came from its
      // own drawer. The fresh mount's deferred fit re-sends resizePty, so the pty
      // resizes to the pane's real dimensions as the agent redraws.
      if (p.workspaceId) termEpoch[p.workspaceId] = (termEpoch[p.workspaceId] ?? 0) + 1;
    });

    await Promise.all([settings.load(), layout.restore()]);
    workspaces = await listWorkspaces();
    // Seed the approval queue from the backend's authoritative pending set. An
    // approval frame delivered before this mount, or before a webview reload, is
    // otherwise a lost one-shot, which wedges the agent's blocked hook forever.
    // This call fires and forgets, like refreshDiffStat, so it never delays the
    // rest of startup.
    seedPendingApprovals();
    // Refresh diff stats for all loaded sessions. This fires and forgets; event-
    // driven updates follow after startup.
    for (const ws of workspaces) refreshDiffStat(ws);
    try {
      staleSessions = (await listStaleSessions()) ?? [];
    } catch {
      // non-fatal: never block startup
    }
    try {
      homeShellCwdValue = await fetchHomeShellCwd();
    } catch {
      homeShellCwdValue = "";
    }
  });

  onDestroy(() => {
    offAgentEvent?.();
    offNotify?.();
    offFsChanged?.();
    offWorkspaceAttach?.();
    offWorkspaceRelaunch?.();
    offOsFileDrop?.();
    window.removeEventListener("focus", onWindowFocus);
    window.removeEventListener("blur",  onWindowBlur);
    document.removeEventListener("keydown", onCopyKeydown);
    // Cancel any pending deferred removals, to avoid use-after-unmount calls.
    for (const p of pendingRemovals) clearTimeout(p.timer);
    clearTimeout(pendingGTimer);
    clearTimeout(alwaysToastTimer);
  });

  // Resume preview state: the session pending confirmation before opening.
  let previewWs = $state<WorkspaceVM | null>(null);

  function onSelect(id: string) {
    // Already the active session. If its pty is live, this is a no-op, and never
    // reopens the live pane. But if the active session is DEAD, not in openIds,
    // its dimmed sidebar row is the only way to bring it back on the code or diff
    // view, since the in-pane Reopen overlay shows only on the agent view. So this
    // reopens it.
    if (id === activeId) {
      if (!openIds.has(id)) openSession(id);
      return;
    }
    const ws = workspaces.find(w => w.id === id) ?? null;
    if (!ws) return;
    // Open but not active: just FOCUS it. No preview and no reopen, because the
    // pty is live, and re-running openWorkspace would respawn it and re-type the
    // launch command over the running xterm.
    if (openIds.has(id)) { activeId = id; return; }
    // Not open, but DORMANT, meaning opened then closed this run: reopen DIRECTLY,
    // the same as the Enter key, a notification click, and the in-pane Reopen
    // overlay. The user just had it live, so the resume preview would add friction
    // with no benefit (F22).
    if (everOpened.has(id)) { openSession(id); return; }
    // A COLD persisted session, never opened this run, shows the resume preview,
    // so the user can confirm its branch, agent, and last-active time before perch
    // spawns the pty.
    previewWs = ws;
  }

  // The single open path. It bumps the terminal epoch for a fresh xterm on the
  // respawned pty, marks the session active and open, then spawns the pty. On
  // failure, it rolls the open flag back, so the row does not falsely read as
  // live.
  async function openSession(id: string) {
    termEpoch[id] = (termEpoch[id] ?? 0) + 1;
    activeId = id;
    openIds.add(id);
    everOpened.add(id); // opened this run, so a later click reopens directly (F22)
    try {
      await openWorkspace(id);
      // Refresh caps and state now that the monitor is live. openWorkspace
      // registers the backend Monitor, so a fresh ListWorkspaces call returns real
      // Capabilities for approvals and attention. Without this refetch, the session
      // keeps its pre-open, all-false caps snapshot, and the approval card's
      // caps.approvals gate hides a real, queued approval request.
      workspaces = await listWorkspaces();
      // A one-shot approval frame may have arrived while this session was still
      // spawning, before its monitor was live or before it was listed. This
      // reconciles the backend's authoritative pending set, so a card surfaces.
      await seedPendingApprovals();
    } catch (e) {
      openIds.delete(id);
      // A genuinely failed OpenWorkspace call, for example from a missing
      // worktree that fails to spawn a pty, was once silently swallowed here,
      // leaving the row dimmed with no explanation. This surfaces the error, so
      // the user knows the reopen did not work (F35).
      addBlocking(id, "Could not open session", String(e), "error");
    }
  }

  // Rebuild the approval queue from the backend's authoritative pending set. The
  // agent:event that carries an approval is a one-shot event, so a frame that
  // arrived before the session was listed, or before a webview reload, would
  // otherwise be lost, wedging the agent's blocked hook forever. This function
  // MERGES the queue, and never replaces it, deduping by reqId, so an
  // already-queued request is not duplicated.
  async function seedPendingApprovals() {
    try {
      const pending = await pendingApprovals();
      for (const { workspaceId, req } of pending) {
        const q = approvals[workspaceId] ?? [];
        if (!q.some(r => r.reqId === req.reqId)) {
          approvals[workspaceId] = [...q, req];
        }
      }
    } catch {
      // non-fatal: the queue simply stays as-is
    }
  }

  // The agent session ended. This drops it from the open set, so the reopen
  // overlay shows and the row dims, clears stale attention, and sets its final
  // sidebar state. It KEEPS activeId, so the "session ended / Reopen" overlay
  // stays reachable.
  //
  // Two triggers share one path (F32). The pty:exit callback passes finalState
  // "idle", for when the shell itself closed. The agent-event "exited" path
  // passes "exited", so the sidebar shows a distinct terminal state for a crashed
  // or exited AGENT while its login shell is still alive. This function gives up
  // keyboard or terminal mode only when the exiting session is the one the user is
  // actively driving. A BACKGROUND session's exit, such as a crashed agent in
  // another pane, must never pull the ACTIVE pane out of terminal mode.
  function handleAgentExit(id: string, finalState: AgentState = "idle") {
    if (id === activeId) mode.leaveTerminal();
    openIds.delete(id);
    attnAck.delete(id);
    // Prune any pending approval for the now-dead session. Its ApprovalCard points
    // at a reqId whose agent process is gone, so approve() would reject the call
    // and the card would be undismissable. This drops the whole queue for the
    // session (F19).
    if (approvals[id]) { const { [id]: _drop, ...rest } = approvals; approvals = rest; }
    const ws = workspaces.find(w => w.id === id);
    if (ws) ws.state = finalState;
  }

  // Assign a session to the secondary split pane. This rejects the same session
  // if it is already in the primary pane, activeId, or already in the secondary
  // pane, splitId, because the same pty in two panes corrupts the shared buffer.
  // If the chosen session has no live pty, not in openIds, this spawns it through
  // openSession, so the secondary pane is not a dead xterm. openSession focuses
  // and opens the session, so this restores the active session afterwards, so the
  // split assignment does not hijack the primary pane.
  //
  // Order matters here. This spawns and restores the pty FIRST, then sets
  // splitId LAST. openSession makes `id` transiently the active session, and the
  // active-not-split invariant $effect would clear splitId the instant it equaled
  // activeId. Assigning splitId only after activeId is restored to the primary
  // keeps that transient state invisible.
  async function chooseSplit(id: string) {
    if (!id || id === activeId || id === layout.splitId) return;
    layout.setSplit(true);
    if (!openIds.has(id)) {
      const prevActive = activeId;
      await openSession(id);
      if (prevActive) activeId = prevActive;
    }
    layout.setSplitId(id);
  }

  async function confirmPreview() {
    if (!previewWs) return;
    const id = previewWs.id;
    previewWs = null;
    await openSession(id);
  }

  function cancelPreview() { previewWs = null; }

  // Clicking a notification focuses its session. It focuses the session if
  // already open. It opens the session directly, with no preview, if closed,
  // since the user's intent is clear.
  function onNotificationSelect(wsId: string) {
    if (!wsId) return;
    // Ignore a click that targets a session already scheduled for removal. Its
    // row is optimistically hidden, so focusing it would be a dead click.
    if (pendingRemovalIds.has(wsId)) return;
    const ws = workspaces.find(w => w.id === wsId);
    if (!ws) return;
    notifOpen = false;
    if (openIds.has(wsId)) {
      activeId = wsId;
    } else {
      openSession(wsId);
    }
  }

  // Route all hub open and close calls through here, so opening always marks the
  // backlog read. Seeing the hub counts as the catch-up, so the unread badge
  // clears.
  function openNotif(open: boolean) {
    notifOpen = open;
    if (open) markAllRead();
  }

  function openNewSession(initialAgent?: string) {
    newSessionOpen = true;
    createError = null; // start clean, with no stale inline error from a prior attempt
    // Discover repos ONCE, on the first open. The scan walks the filesystem, so
    // re-running it on every open would waste work (F21). This flag is set
    // synchronously, so a rapid second open before the scan resolves cannot
    // double-scan. A failed scan resets the flag, so a later open retries. The
    // results merge with session-derived paths, deduped in the repos $derived.
    if (!discoveredReposScanned) {
      discoveredReposScanned = true;
      discoverRepos()
        .then((list) => { discoveredRepos = list; })
        .catch(() => { discoveredReposScanned = false; }); // non-fatal: retry on next open
    }
    // Guard: accept only a genuine string. Sidebar passes this as onclick, which
    // injects a MouseEvent, so this must not treat a MouseEvent as an agent name.
    newSessionInitialAgent = typeof initialAgent === "string" ? initialAgent : null;
  }

  async function handleCreate(agent: string, repo: string, baseRef: string, branch: string, title: string, worktree: boolean) {
    createError = null; // clear any prior inline error on a fresh attempt
    // Guard: if the branch already belongs to a perch session, offer resume instead.
    const existing = await workspaceForBranch(repo, branch);
    if (existing.found) {
      // The branch is already in use. Resume that session instead of creating a
      // duplicate.
      newSessionOpen = false;
      newSessionInitialAgent = null;
      await onSelect(existing.id);
      return;
    }
    try {
      const vm = await createWorkspace(agent, repo, baseRef, branch, title, worktree);
      workspaces = await listWorkspaces();
      newSessionOpen = false;
      // Creating a session spawns its pty right away. onSelect sets activeId
      // and opens the session in one step, so the new session comes up live,
      // instead of sitting as a selected-but-dead row.
      await onSelect(vm.id);
    } catch (e) {
      const msg = String(e);
      // Never show the raw error to the user, since it is git output noise, for
      // example "exit status 128: fatal: ...". Log it for diagnosis, and show a
      // human-readable message instead.
      console.error("create session failed:", e);
      if (msg.includes("uncommitted changes")) {
        // ErrWorktreeDirty: a non-worktree session cannot switch to a different
        // branch while the working tree has uncommitted changes.
        addBlocking("", "Cannot switch branch",
          "Your working tree has uncommitted changes. Commit or stash them before switching to a different branch.", "error");
      } else if (/already exists/i.test(msg)) {
        // ErrBranchExists at the git layer, from a new-branch-mode name collision.
        // This is a genuine user error, so it MUST surface as actionable copy,
        // inline in the dialog, not as raw git output (F26a).
        createError = `A branch named "${branch}" already exists. Choose a different name, or turn on "Use existing branch" to resume it.`;
      } else {
        createError = "Could not create the session. Check the repo and branch, then try again.";
      }
      // Keep the dialog open, so the user can correct their choice.
    }
  }

  function requestRemove(ws: WorkspaceVM) {
    confirmRemove = ws;
  }

  // Drop ALL per-session frontend state for a gone session, so nothing dangles.
  // This mirrors the approvals and fsVersion pruning, and adds termEpoch and
  // diff stats.
  function pruneWorkspaceState(id: string) {
    const { [id]: _a, ...restA } = approvals;   approvals   = restA;
    const { [id]: _f, ...restF } = fsVersion;   fsVersion   = restF;
    const { [id]: _e, ...restE } = termEpoch;   termEpoch   = restE;
    const { [id]: _c, ...restC } = codePaths;   codePaths   = restC;
    const { [id]: _t, ...restT } = termRefs;    termRefs    = restT;
    const { [id]: _d, ...restD } = wsDiffStats; wsDiffStats = restD;
    attnAck.delete(id);
  }

  function handleConfirmRemove() {
    if (!confirmRemove) return;
    const wsToRemove = confirmRemove;
    confirmRemove = null;

    // Finalize any existing pending removal for the same id, as an edge-case
    // guard.
    finalizePendingRemoval(wsToRemove.id);

    // Capture the selection BEFORE App clears it, so Undo can restore it.
    const prevActiveId = activeId;
    const prevSplitId  = layout.splitId;

    // Optimistically hide the session right away. The visibleWorkspaces $derived
    // filters by pendingRemovalIds, so no listWorkspaces() refresh is needed yet.
    if (activeId === wsToRemove.id) {
      const remaining = visibleWorkspaces.filter(w => w.id !== wsToRemove.id);
      activeId = remaining[0]?.id ?? null;
    }
    if (layout.splitId === wsToRemove.id) layout.setSplitId(null);

    const timer = setTimeout(async () => {
      // Time is up. Commit the removal for real.
      pendingRemovals = pendingRemovals.filter(p => p.ws.id !== wsToRemove.id);
      try {
        await removeWorkspace(wsToRemove.id);
        openIds.delete(wsToRemove.id);
        everOpened.delete(wsToRemove.id);
        dropForWorkspace(wsToRemove.id);
        pruneWorkspaceState(wsToRemove.id);
        workspaces = await listWorkspaces();
        if (activeId === wsToRemove.id) activeId = workspaces[0]?.id ?? null;
      } catch (err) {
        // If the backend call fails, put the session back.
        workspaces = await listWorkspaces();
        if (String(err).includes("uncommitted changes")) {
          confirmDirty = wsToRemove;
        }
      }
    }, UNDO_REMOVE_DELAY_MS);

    pendingRemovals = [...pendingRemovals, { ws: wsToRemove, timer, prevActiveId, prevSplitId }];
  }

  /** Cancel a pending deferred removal and return the session to the visible list. */
  function handleUndoRemove(id: string) {
    const entry = pendingRemovals.find(p => p.ws.id === id);
    if (!entry) return;
    clearTimeout(entry.timer);
    pendingRemovals = pendingRemovals.filter(p => p.ws.id !== id);
    // The session is already in `workspaces`. The visibleWorkspaces $derived
    // restores it to view.
    // Restore the selection that was cleared when the removal was scheduled, so
    // undoing a remove of the active or split session re-selects it.
    if (entry.prevActiveId === id) activeId = id;
    if (entry.prevSplitId === id) layout.setSplitId(id);
  }

  /** Force-commit a pending removal without waiting for the timer. */
  function finalizePendingRemoval(id: string) {
    const entry = pendingRemovals.find(p => p.ws.id === id);
    if (!entry) return;
    clearTimeout(entry.timer);
    pendingRemovals = pendingRemovals.filter(p => p.ws.id !== id);
    if (layout.splitId === id) layout.setSplitId(null);
    openIds.delete(id);
    everOpened.delete(id);
    dropForWorkspace(id);
    pruneWorkspaceState(id);
    // This fires and forgets, and does not await, so it does not block the caller.
    removeWorkspace(id).then(() => listWorkspaces()).then(ws => { workspaces = ws; }).catch(() => {});
  }

  function handleCancelRemove() {
    confirmRemove = null;
  }

  async function handleForceRemove() {
    if (!confirmDirty) return;
    const ws = confirmDirty;
    confirmDirty = null;
    try {
      await forceRemoveWorkspace(ws.id);
      openIds.delete(ws.id);
      everOpened.delete(ws.id);
      dropForWorkspace(ws.id);
      pruneWorkspaceState(ws.id);
      workspaces = await listWorkspaces();
      if (activeId === ws.id) activeId = workspaces[0]?.id ?? null;
    } catch {
      workspaces = await listWorkspaces();
    }
  }

  // ---------------------------------------------------------------------------
  // Command registry, keyed by the ids MenuBar actually emits.
  // ---------------------------------------------------------------------------
  // reqIds with an in-flight decide() call. A double-click, or an approve-all
  // call racing a single Allow, must not send the same reqId twice.
  const decidingReqs = new Set<string>();

  // Backstop: on a successful decision, locally clear a session's attention if it
  // was awaiting-approval, in case the Go "state cleared" event is missed. This
  // demotes only awaiting-approval, and never overwrites awaiting-input or a
  // fresh running state.
  function clearAttentionBackstop(wsId: string) {
    const ws = workspaces.find(w => w.id === wsId);
    if (ws && ws.state === "awaiting-approval") ws.state = "idle";
  }

  // Resolve ONE queued request by reqId. This calls approve(), then pops the
  // request from its owning session's queue and runs the attention backstop. It
  // guards against a concurrent in-flight decide of the same reqId.
  async function decideOne(reqId: string, decision: "allow" | "deny" | "always"): Promise<boolean> {
    if (decidingReqs.has(reqId)) return false;
    // Locate the owning session, the queue that holds this reqId.
    const ownerEntry = Object.entries(approvals).find(([, q]) => q.some(r => r.reqId === reqId));
    const ownerWsId = ownerEntry?.[0] ?? activeId;
    if (!ownerWsId) return false;
    decidingReqs.add(reqId);
    try {
      await approve(reqId, decision);
      // Pop this reqId from the owner's queue, and leave any siblings so the next
      // request surfaces.
      const q = (approvals[ownerWsId] ?? []).filter(r => r.reqId !== reqId);
      if (q.length) approvals[ownerWsId] = q;
      else {
        const { [ownerWsId]: _drop, ...rest } = approvals; approvals = rest;
        // This session's last pending approval is resolved. This clears its
        // blocking notification, so the stale "approve this" banner does not
        // linger after the request it referred to is gone. It fires only when
        // the queue is now empty.
        dropForWorkspace(ownerWsId);
      }
      clearAttentionBackstop(ownerWsId);
      return true;
    } catch (e) {
      addBlocking(ownerWsId, "Approval failed", String(e), "error");
      return false;
    } finally {
      decidingReqs.delete(reqId);
    }
  }

  // ---------------------------------------------------------------------------
  // Bulk approval. "Approve all" and "Deny all" resolve every pending request for
  // the ACTIVE session only. The card the user is looking at belongs to the active
  // session, so a batch action there must never reach into backgrounded sessions
  // and silently approve their tools. Each item is resolved by its own reqId. The
  // owner lookup is reqId-keyed, so this is safe. A per-item await and guard stop
  // a double-click from double-sending a request.
  // ---------------------------------------------------------------------------
  async function decideAll(decision: "allow" | "deny") {
    // Snapshot the active session's pending reqIds up front, since the queue
    // mutates as each request resolves.
    const reqIds = (approvals[activeId ?? ""] ?? []).map(r => r.reqId);
    for (const reqId of reqIds) await decideOne(reqId, decision);
  }

  type Command = { id: string; group: string; label: string; keybinding?: string; run: () => void | Promise<void> };

  const commands: Command[] = [
    // Session
    { id: "session:new",    group: "Session", label: "New session",        run: () => openNewSession() },
    { id: "session:close",  group: "Session", label: "Close session",      run: () => {
        if (!active) return;
        const id = active.id;
        closeWorkspace(id).then(() => {
          // Clean up per-session frontend state on close: the approvals queue,
          // fsVersion, termEpoch, and diff stats.
          pruneWorkspaceState(id);
          // The pty is gone. This drops the id from the open set, so the row
          // dims and a later click routes through the resume-preview reopen path.
          openIds.delete(id);
          // Clear any stuck attention on the now-dead session.
          const ws = workspaces.find(w => w.id === id);
          if (ws) ws.state = "idle";
          // Keep activeId, so the in-pane "This session has ended / Reopen"
          // overlay stays reachable, since the session left openIds above.
        }).catch(() => {});
      } },
    { id: "session:remove", group: "Session", label: "Remove session",     run: () => { if (active) requestRemove(active); } },
    // Worktree
    { id: "worktree:open",   group: "Worktree", label: "Open worktree",    keybinding: "Enter",       run: () => { if (active && !openIds.has(active.id)) openSession(active.id); } },
    { id: "worktree:reveal", group: "Worktree", label: "Reveal in Files",  run: () => { if (active) revealInFiles(active.worktreePath); } },
    // View
    { id: "view:agent", group: "View", label: "Agent view",  keybinding: "1",  run: () => layout.setView("agent") },
    { id: "view:code",  group: "View", label: "Code view",   keybinding: "2",  run: () => layout.setView("code")  },
    { id: "view:diff",  group: "View", label: "Diff view",   keybinding: "3",  run: () => layout.setView("diff")  },
    { id: "view:split", group: "View", label: "Split",       keybinding: "\\", run: () => layout.toggleSplit()   },
    { id: "view:theme", group: "View", label: "Cycle theme",                   run: () => {
        const idx = (THEMES as readonly string[]).indexOf(settings.theme);
        settings.setTheme(THEMES[(idx + 1) % THEMES.length]);
      },
    },
    // Agent bulk actions
    { id: "agent:approve-all", group: "Agent", label: "Approve all pending", run: () => decideAll("allow") },
    { id: "agent:deny-all",    group: "Agent", label: "Deny all pending",    run: () => decideAll("deny")  },
    // Notifications
    { id: "notifications:open", group: "Notifications", label: "Open notifications",    run: () => { openNotif(!notifOpen); } },
    { id: "notifications:dnd",  group: "Notifications", label: "Toggle Do Not Disturb", run: () => setDnd(!getDnd()) },
    // Help: the two entries open genuinely distinct panels (F53).
    { id: "help:shortcuts", group: "Help", label: "Keyboard shortcuts", run: () => { helpSection = "shortcuts"; helpOpen = true; } },
    { id: "help:about",     group: "Help", label: "About perch",        run: () => { helpSection = "about";     helpOpen = true; } },
    // Settings
    { id: "settings:open", group: "Settings", label: "Settings…", run: () => { settingsOpen = true; } },
  ];

  function runCommand(id: string) {
    const cmd = commands.find(c => c.id === id);
    if (cmd) cmd.run();
  }

  // ---------------------------------------------------------------------------
  // Keymap: full state machine.
  // ---------------------------------------------------------------------------
  // True when any App-rendered modal or overlay that should trap the keyboard is
  // open. The command palette is handled separately, in its own mode. notifOpen
  // is a non-trapping dock, a dismissable panel and not a modal, so it is NOT
  // included.
  const modalOpen = $derived(
    newSessionOpen || confirmRemove !== null || confirmDirty !== null ||
    helpOpen || settingsOpen || cleanupOpen || previewWs !== null
  );

  function onKeyDown(e: KeyboardEvent) {
    // COMMAND mode: let the CommandPalette handle everything.
    if (mode.current === "command") return;

    // MODAL or OVERLAY open: the app behind it must be inert. This handles ONLY
    // Escape, to dismiss the topmost overlay, and swallows everything else, so
    // NORMAL or TERMINAL navigation never drives the app behind the dialog.
    if (modalOpen) {
      if (e.key === "Escape") {
        e.preventDefault();
        // Close the topmost overlay. previewWs is the only App-owned overlay with
        // no close chrome of its own here. The dialog components trap and close
        // themselves, but this handler backs them up, so Escape always dismisses.
        if (previewWs !== null)            previewWs = null;
        else if (cleanupOpen)              cleanupOpen = false;
        else if (settingsOpen)             settingsOpen = false;
        else if (helpOpen)                 helpOpen = false;
        else if (confirmDirty !== null)    confirmDirty = null;
        else if (confirmRemove !== null)   confirmRemove = null;
        else if (newSessionOpen)         { newSessionOpen = false; newSessionInitialAgent = null; }
      }
      return;
    }

    // Editable target: a keystroke aimed at an <input>, <textarea>, <select>, or a
    // contentEditable element must reach the field. This handler must never run a
    // single-letter shortcut or call preventDefault over the user's typing.
    // The xterm terminal uses a hidden <textarea> that holds focus whenever a
    // session is open, so this excludes anything inside a terminal zone. In
    // NORMAL mode the terminal is passive and shortcuts must still run; TERMINAL
    // mode is handled below. Only real app-chrome fields, such as dialog inputs,
    // the filter, and the rename box, are guarded here.
    const t = e.target as HTMLElement | null;
    const tag = t?.tagName?.toUpperCase();
    if ((tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t?.isContentEditable)
        && !t?.closest("[data-terminal-zone]")) {
      return;
    }

    // TERMINAL mode: this intercepts only the Ctrl-\ Ctrl-n leave sequence.
    // Everything else, including Ctrl-K and `:`, passes straight to the pty on
    // purpose. Ctrl-K is readline kill-line and `:` is ordinary input, so the
    // command palette is intentionally NOT reachable from TERMINAL mode. Leave
    // TERMINAL mode first, with Ctrl-\ Ctrl-n, to open the palette. This behavior
    // is documented here and unchanged (F55).
    if (mode.current === "terminal") {
      if (e.ctrlKey && e.key === "\\") {
        pendingLeave = true;
        e.preventDefault();
        return;
      }
      if (pendingLeave && e.ctrlKey && e.key === "n") {
        mode.leaveTerminal();
        pendingLeave = false;
        e.preventDefault();
        return;
      }
      // Any other key cancels the pending leave prefix. This does NOT call
      // preventDefault, so the key still reaches the pty.
      pendingLeave = false;
      return;
    }

    // NORMAL mode -----------------------------------------------------------

    // Ctrl-K or Cmd-K opens the command palette. This check runs before the
    // switch, so the plain "k" session-navigation case does not fire when Ctrl
    // is held. It does NOT intercept when focus is inside an input or textarea,
    // since that would hijack typing.
    if ((e.ctrlKey || e.metaKey) && e.key === "k") {
      const tag = (e.target as HTMLElement)?.tagName?.toUpperCase();
      if (tag !== "INPUT" && tag !== "TEXTAREA") {
        e.preventDefault();
        mode.enterCommand();
        return;
      }
    }

    // g-prefix resolution must come first so gd/ge/gt/gT work correctly.
    if (pendingG) {
      pendingG = false;
      clearTimeout(pendingGTimer); // chord resolved: stop the auto-clear (F54)
      if (e.key === "d") { e.preventDefault(); layout.setView("diff"); }
      else if (e.key === "e") { e.preventDefault(); layout.setView("code"); }
      else if (e.key === "t") {
        // gt cycles to the next view: agent, then code, then diff, then agent again.
        e.preventDefault();
        const views: import("./lib/stores/layout.svelte").View[] = ["agent", "code", "diff"];
        const idx = views.indexOf(layout.view);
        layout.setView(views[(idx + 1) % views.length]);
      }
      else if (e.key === "T") {
        // gT cycles to the previous view: agent, then diff, then code, then agent again.
        e.preventDefault();
        const views: import("./lib/stores/layout.svelte").View[] = ["agent", "code", "diff"];
        const idx = views.indexOf(layout.view);
        layout.setView(views[(idx - 1 + views.length) % views.length]);
      }
      // Any other key cancels the prefix silently, with no action.
      return;
    }

    switch (e.key) {
      case "j": {
        e.preventDefault();
        const list = shownWorkspaces;
        const idx  = list.findIndex(w => w.id === activeId);
        if (idx === -1) {
          // Nothing active: select the first item.
          if (list.length > 0) activeId = list[0].id;
        } else {
          // Clamp at the end.
          activeId = list[Math.min(idx + 1, list.length - 1)].id;
        }
        break;
      }
      case "k": {
        e.preventDefault();
        const list = shownWorkspaces;
        const idx  = list.findIndex(w => w.id === activeId);
        if (idx === -1) {
          if (list.length > 0) activeId = list[0].id;
        } else {
          activeId = list[Math.max(idx - 1, 0)].id;
        }
        break;
      }
      case "1": e.preventDefault(); layout.setView("agent"); break;
      case "2": e.preventDefault(); layout.setView("code");  break;
      case "3": e.preventDefault(); layout.setView("diff");  break;
      case "g": {
        e.preventDefault();
        pendingG = true;
        // Arm an auto-clear, so a stray `g` shows its transient "g…" indicator
        // briefly and then resets, instead of silently eating the next key (F54).
        clearTimeout(pendingGTimer);
        pendingGTimer = setTimeout(() => { pendingG = false; }, GCHORD_TIMEOUT_MS);
        return;
      }
      case "\\": e.preventDefault(); layout.toggleSplit(); break;
      case "`": {
        if (e.ctrlKey) {
          e.preventDefault();
          layout.setCollapsed("shell", !layout.collapsed["shell"]);
        }
        break;
      }
      case "b": {
        if (e.ctrlKey) {
          e.preventDefault();
          layout.setCollapsed("sidebar", !layout.collapsed["sidebar"]);
        }
        break;
      }
      case "/": {
        e.preventDefault();
        filtering    = true;
        filterQuery  = "";
        break;
      }
      case "Enter": {
        e.preventDefault();
        // Only (re)open a session whose pty is not already live. Reopening a live
        // session displaces its backend pty, which SIGKILLs the running agent's
        // process group, and re-types the launch command over a blanked xterm
        // (F13). This mirrors onSelect's liveness guard.
        if (activeId && !openIds.has(activeId)) openSession(activeId);
        break;
      }
      case "i": {
        e.preventDefault();
        mode.enterTerminal();
        // enterTerminal only flips the mode flag. This moves DOM focus into the
        // active pty, so keystrokes actually route to the agent (F17). It is
        // guarded to the active session; Terminal exposes a focus() export.
        if (activeId) termRefs[activeId]?.focus();
        break;
      }
      // New session: the one-key entry point matching the sidebar CTA and menu (F23).
      case "n": e.preventDefault(); openNewSession(); break;
      // Remove the active session. This opens the CANCELABLE confirm dialog
      // instead of acting right away, so a stray keypress can never destroy a
      // worktree, since tmux-style `x` means kill or remove. The lone destructive
      // key is deliberately NOT bound to session:close, which would SIGKILL a
      // live agent with no undo.
      case "x": e.preventDefault(); if (active) requestRemove(active); break;
      // Help: `?` and F1 open the full shortcuts and about panel (F24).
      case "?":
      case "F1": e.preventDefault(); helpSection = "all"; helpOpen = true; break;
      case ":": e.preventDefault(); mode.enterCommand();  break;
    }
  }

  function startResizeSidebar(e: MouseEvent) {
    const startX = e.clientX, startW = layout.sidebarW;
    function onMove(mv: MouseEvent) { layout.setSidebarW(Math.max(SIDEBAR_MIN_W, startW + mv.clientX - startX)); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  function keyResizeSidebar(e: KeyboardEvent) {
    if (e.key === "ArrowRight") { e.preventDefault(); layout.setSidebarW(Math.max(SIDEBAR_MIN_W, layout.sidebarW + RESIZE_STEP_PX)); }
    else if (e.key === "ArrowLeft") { e.preventDefault(); layout.setSidebarW(Math.max(SIDEBAR_MIN_W, layout.sidebarW - RESIZE_STEP_PX)); }
  }

  function startResizeShell(e: MouseEvent) {
    const startY = e.clientY, startH = layout.shellH;
    function onMove(mv: MouseEvent) { layout.setShellH(Math.max(SHELL_MIN_H, startH - (mv.clientY - startY))); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  function keyResizeShell(e: KeyboardEvent) {
    if (e.key === "ArrowUp") { e.preventDefault(); layout.setShellH(Math.max(SHELL_MIN_H, layout.shellH + RESIZE_STEP_PX)); }
    else if (e.key === "ArrowDown") { e.preventDefault(); layout.setShellH(Math.max(SHELL_MIN_H, layout.shellH - RESIZE_STEP_PX)); }
  }

  // ---------------------------------------------------------------------------
  // Send text to the active agent pane through writeToPty.
  //
  // @mention convention, matching DragDrop.svelte:
  //   - A file or path reference arrives as '@'+path+' '. The leading '@' and
  //     trailing space are already present in the string the caller passes.
  //   - Arbitrary selected text, from Editor.onSendToAgent, is sent as-is.
  //
  // Callers must format the text themselves. sendToAgent is a raw pass-through.
  // ---------------------------------------------------------------------------
  function sendToAgent(text: string) {
    if (!active?.paneId) return;
    const bytes = Array.from(new TextEncoder().encode(text));
    writeToPty(active.paneId, bytes);
  }

  // ---------------------------------------------------------------------------
  // Approval decision handler, called by the ApprovalCard docked chrome. This
  // resolves the single request the card is showing, the head of the owning
  // session's queue, pops it, and runs the attention backstop. The deletion is
  // keyed by the session that owns reqId, not by activeId, since the queue may
  // hold non-active entries, so an activeId change mid-await never clears the
  // wrong session.
  // ---------------------------------------------------------------------------
  async function onDecision(reqId: string, decision: "allow" | "deny" | "always") {
    if (decision !== "always") { await decideOne(reqId, decision); return; }
    // "Always allow" adds a standing rule in the backend. App hosts the Undo
    // toast, because the card unmounts or moves to the next request as soon
    // as the grant lands (FEC-15, FEX-9). The rule the grant added is the
    // difference between the rule list before and after it, so Undo removes
    // exactly that rule, matched by identity on a fresh read.
    const tool = Object.values(approvals).flat().find(r => r.reqId === reqId)?.tool ?? "this tool";
    const before = await getSettings().then(s => s.alwaysRules ?? []).catch(() => null);
    if (!(await decideOne(reqId, "always"))) return; // the grant failed: no rule, nothing to undo
    const after = await getSettings().then(s => s.alwaysRules ?? []).catch(() => null);
    const added = before && after ? after.filter(r => !before.some(b => sameAlwaysRule(b, r))) : [];
    showAlwaysToast(tool, added);
  }

  // Post-grant "Always allow" toast. `rules` is empty when the added rule
  // could not be identified; the toast then points at Settings instead of
  // offering an Undo that would do nothing.
  let alwaysToast = $state<{ tool: string; rules: AlwaysRule[] } | null>(null);
  let alwaysToastTimer: ReturnType<typeof setTimeout> | undefined;
  function showAlwaysToast(tool: string, rules: AlwaysRule[]) {
    clearTimeout(alwaysToastTimer);
    alwaysToast = { tool, rules };
    alwaysToastTimer = setTimeout(() => { alwaysToast = null; }, UNDO_REMOVE_DELAY_MS);
  }
  async function undoAlways() {
    const t = alwaysToast;
    clearTimeout(alwaysToastTimer);
    alwaysToast = null;
    if (!t || t.rules.length === 0) return;
    try {
      await settings.removeAlwaysRules(t.rules);
    } catch (e) {
      addBlocking("", "Could not undo the always-allow rule", `${String(e)}. Remove it in Settings.`, "error");
    }
  }
</script>

<svelte:window onkeydown={onKeyDown} />

<ThemeProvider theme={settings.theme} density={settings.density} font={settings.font} glass={settings.glass}>
  <div class="app-root" onpointerdowncapture={onAppPointerDown}>
    <MenuBar onCommand={(id) => runCommand(id)} {unreadCount} />

    {#if staleSessions.length > 0 && !staleBannerDismissed}
      <div class="stale-banner" data-testid="stale-banner">
        <span>{staleSessions.length} session{staleSessions.length !== 1 ? 's' : ''} unused — review</span>
        <button class="stale-banner-link" onclick={() => { cleanupOpen = true; }}>Review</button>
        <button class="stale-banner-dismiss" onclick={() => { staleBannerDismissed = true; }} aria-label="dismiss">✕</button>
      </div>
    {/if}

    <div class="main-area">
      <!-- Sidebar toggle rail. It is always visible and survives the collapsed state. -->
      <button
        class="sidebar-toggle-rail"
        class:sidebar-collapsed={layout.collapsed["sidebar"]}
        aria-expanded={!layout.collapsed["sidebar"]}
        aria-label="Toggle sidebar"
        onclick={() => layout.setCollapsed("sidebar", !layout.collapsed["sidebar"])}
      >{layout.collapsed["sidebar"] ? "▶" : "◀"}</button>

      <aside data-zone="sidebar" class="sidebar-zone"
             style:width={layout.collapsed["sidebar"] ? "0" : `${layout.sidebarW}px`}
             inert={layout.collapsed["sidebar"] ? true : undefined}>
        {#if filtering}
          <input
            class="filter-input"
            type="text"
            aria-label="filter sessions"
            use:focusOnMount
            value={filterQuery}
            oninput={(e) => { filterQuery = (e.currentTarget as HTMLInputElement).value; }}
            onkeydown={(e) => {
              e.stopPropagation();
              if (e.key === "Escape") { filtering = false; filterQuery = ""; }
            }}
          />
        {/if}
        <Sidebar workspaces={shownWorkspaces} {activeId} onSelect={onSelect} onNew={openNewSession} onReorder={handleReorder} diffStats={wsDiffStats} openIds={openIds} ackedInputIds={attnAck} ackedDoneIds={attnDoneAck}
          onRename={(id, title) => { const ws = workspaces.find(w => w.id === id); if (ws) ws.title = title; setWorkspaceTitle(id, title); }}
          onEditStart={() => { previewWs = null; }}
          requestRemove={(id) => { const ws = workspaces.find(w => w.id === id); if (ws) requestRemove(ws); }} />
      </aside>

      <div class="divider divider-v" role="slider" aria-label="Resize sidebar"
           aria-orientation="vertical" aria-valuenow={layout.sidebarW} aria-valuemin={SIDEBAR_MIN_W} aria-valuemax={SIDEBAR_MAX_W}
           tabindex="0"
           onmousedown={startResizeSidebar}
           onkeydown={keyResizeSidebar}
           style:display={layout.collapsed["sidebar"] ? "none" : undefined}></div>

      <div class="center-column">
        <div data-zone="stage" class="stage-zone" role="region" aria-label="stage"
             ondragenter={(e) => {
               if (typeof e.dataTransfer?.types?.includes === "function" &&
                   e.dataTransfer.types.includes(MIME_SESSION)) {
                 e.preventDefault();
               }
             }}
             ondragover={(e) => {
               if (typeof e.dataTransfer?.types?.includes === "function" &&
                   e.dataTransfer.types.includes(MIME_SESSION)) {
                 e.preventDefault();
               }
             }}
             ondrop={(e) => {
               if (typeof e.dataTransfer?.getData !== "function") return;
               // Ignore drops while a modal or overlay is open, since the app
               // behind it is inert.
               if (modalOpen) return;
               const id = e.dataTransfer.getData(MIME_SESSION);
               if (!id) return;
               e.preventDefault();
               chooseSplit(id);
             }}
        >
          <Stage view={layout.view} split={layout.split}
                 onView={(v) => layout.setView(v)}
                 onSplit={() => layout.toggleSplit()}>
            {#snippet primary()}
              <!-- One agent Terminal per MOUNTED session, kept mounted (hidden with
                   display) so switching sessions never rebuilds a blank xterm or
                   loses scrollback. This is the same keep-alive pattern as the home
                   shell. The mounted set is every open session, plus a just-exited
                   active session, plus the split session. An exited pty's Terminal
                   must NOT unmount on the exit event, or its final buffer, the
                   "[process exited]" line, is destroyed. Instead, its "session ended
                   / Reopen" overlay lies on top of the still-mounted, dimmed pane.
                   The list is keyed by session id plus epoch, so only a genuine
                   reopen, an epoch bump, respawns a pane. A session or view switch
                   just toggles visibility.
                   The split session mounts HERE too, and is not filtered out. App
                   relocates its node into the secondary pane with use:adoptInto on
                   that host, so toggling split never destroys and recreates its
                   xterm (F10a). While split, the split session's zone shows
                   unconditionally, because it lives in the secondary pane, which
                   ignores the primary view. Otherwise the usual active-and-agent
                   visibility rule applies. -->
              {#each mountedWorkspaces as ws (ws.id + ":" + (termEpoch[ws.id] ?? 0))}
                {@const ended = !openIds.has(ws.id)}
                {@const isSplit = layout.split && layout.splitId === ws.id}
                {@const vis = isSplit || (ws.id === activeId && layout.view === "agent")}
                <div class="terminal-zone" class:input-emphasis={ws.id === activeId && emphasizeInput}
                     bind:this={termZoneEls[ws.id]}
                     use:keepHome
                     data-terminal-zone role="group" aria-label="agent terminal"
                     style:display={vis ? "" : "none"}
                     onanimationend={(e) => { if (e.animationName === "perch-emphasis") emphasizeInput = false; }}
                     onpointerdown={() => { if (mode.current === "normal") mode.enterTerminal(); }}>
                  {#if ended}
                    <div class="pane-ended" data-testid="pane-ended">
                      <p>This session has ended.</p>
                      <button class="btn btn-primary" onclick={() => openSession(ws.id)}>Reopen</button>
                    </div>
                  {/if}
                  <DragDrop paneId={ws.paneId} fileDrop={true}>
                    <Terminal bind:this={termRefs[ws.id]} paneId={ws.paneId} cwd={ws.worktreePath} visible={vis} onExit={() => handleAgentExit(ws.id)} />
                  </DragDrop>
                </div>
              {/each}
              <!-- One code layout per MOUNTED session, kept mounted (hidden with
                   display) so switching sessions preserves each session's open
                   file, folder expansion, scroll position, and unsaved Editor
                   buffer, instead of destroying them. This is the same keep-alive
                   pattern as the agent terminals. A session or view switch just
                   toggles visibility. Only a genuine remove or close destroys a
                   layout; Editor.onDestroy then saves a dirty buffer. Per-session
                   codePaths keep each file selection independent, and the
                   `visible` prop freezes a hidden pane, so it does no background
                   listing, reloading, or rendering. -->
              {#each mountedWorkspaces as ws (ws.id)}
                {@const codePath = codePaths[ws.id] ?? null}
                {@const showing  = ws.id === activeId && layout.view === "code"}
                <div class="code-layout" style:display={showing ? "" : "none"}>
                  <!-- FileTree re-lists IN PLACE on a file write, through the
                       monotonic refresh signal. It is never remounted, since that
                       would collapse open folders. The `visible` guard freezes it
                       while hidden, so no listing fires on a hide or on a
                       background write. -->
                  <FileTree root={ws.worktreePath}
                    refresh={fsVersion[ws.id] ?? 0}
                    visible={showing}
                    selectedPath={codePath}
                    onOpen={(p) => {
                      // FileTree may send '@mention:'+path for "Send to agent".
                      // This routes it to sendToAgent, and otherwise treats it as
                      // a regular file open.
                      if (p.startsWith(MENTION_PREFIX)) {
                        const path = p.slice(MENTION_PREFIX.length);
                        // This format matches DragDrop: '@'+path+' '
                        sendToAgent("@" + path + " ");
                      } else {
                        codePaths[ws.id] = p;
                      }
                    }} />
                  {#if isPreviewable(codePath)}
                    <!-- Keyed only by the file path, the same key on every view, so
                         a file switch gives a fresh render but an fs change does
                         not remount. The render side effect is frozen with
                         `visible`. -->
                    {#key codePath}
                      <Preview path={codePath ?? ""} kind={previewKind(codePath ?? "")}
                               content={previewContent} visible={showing} />
                    {/key}
                  {:else}
                    <!-- reloadToken is the monotonic per-file fs version, so the
                         editor reloads only when ITS file changes on disk, and only
                         when it has no unsaved edits, never on an unrelated write
                         or a view toggle. Visibility is handled by `visible`. -->
                    <Editor path={codePath} worktree={ws.worktreePath} workspaceId={ws.id}
                            reloadToken={fsPathVersion[codePath ?? ""] ?? 0}
                            visible={showing}
                            onSendToAgent={sendToAgent} />
                  {/if}
                </div>
              {/each}
              {#if active}
                <!-- DiffView stays mounted while a session is active, and is hidden
                     on the agent and code views with a display toggle, never
                     unmounted with {#if}, so switching views preserves its
                     expanded hunks and scroll position (F3). An agent file write
                     refreshes the file list IN PLACE, through the refresh prop,
                     instead of remounting the view (F4), so expanded hunks and
                     scroll position survive the refresh. -->
                <div class="diff-host" style:display={layout.view === "diff" ? "" : "none"}>
                  <DiffView worktree={active.worktreePath} refresh={fsVersion[active.id] ?? 0}
                            onSendToAgent={sendToAgent}
                            onDiffChanged={() => { if (active) refreshDiffStat(active); }} />
                </div>
              {:else}
                <div class="empty-state home-view" data-testid="empty-state">
                  <div class="home-welcome">
                    <div class="empty-state-card">
                      <h2 class="empty-state-title">Welcome to perch</h2>
                      <p class="empty-state-hint">Start an AI coding session in any local git repo.</p>
                      <button
                        class="empty-state-btn empty-state-btn-primary"
                        onclick={() => openNewSession()}
                      >
                        New Session
                      </button>
                      <div class="empty-state-templates">
                        <span class="empty-state-templates-label">Quick start</span>
                        <button
                          class="empty-state-btn empty-state-btn-template"
                          onclick={() => openNewSession(AGENT_CLAUDE)}
                        >
                          Claude session
                        </button>
                        <button
                          class="empty-state-btn empty-state-btn-template"
                          onclick={() => openNewSession(AGENT_OPENCODE)}
                        >
                          Opencode session
                        </button>
                      </div>
                      <div class="empty-state-note">
                        <p>
                          A session needs a git repository and either
                          <code>claude</code> or <code>opencode</code> on your PATH.
                        </p>
                        <p>
                          Each session opens in its own worktree, a separate checkout
                          of the repository on its own branch, so several agents can
                          work at once without treading on each other's files.
                        </p>
                        <p>
                          If something looks missing, run <code>perch doctor</code>.
                          The usage guide at <code>docs/usage.md</code> covers the rest.
                        </p>
                      </div>
                    </div>
                  </div>
                </div>
              {/if}
              <!-- Home shell: lives OUTSIDE the active/home conditional, so the
                   xterm instance, and its pty and scroll buffer, are never
                   unmounted when a session opens. It is hidden with an inline
                   display style when a session is active. The inline style is
                   required, because jsdom reflects only inline styles in
                   visibility assertions. -->
              {#if homeShellCwdValue}
                <div class="home-shell-zone" data-terminal-zone role="group" aria-label="home shell"
                     onpointerdown={() => { if (mode.current === "normal") mode.enterTerminal(); }}
                     style:display={active ? 'none' : ''}>
                  <ShellDrawer
                    paneId="shell-home"
                    cwd={homeShellCwdValue}
                    collapsed={layout.collapsed["shell-home"] ?? false}
                    onToggleCollapse={() => layout.setCollapsed("shell-home", !layout.collapsed["shell-home"])}
                  />
                </div>
              {/if}
            {/snippet}
            {#snippet secondary()}
              {#if layout.split}
                {@const splitWs = workspaces.find(w => w.id === layout.splitId) ?? null}
                {#if splitWs}
                  <!-- The split session's agent Terminal mounts ONCE, in the
                       primary keep-alive loop above. This host adopts that
                       already-live node, so toggling split, or re-picking the
                       secondary session, never rebuilds a blank xterm. The DOM
                       node and its scroll buffer are preserved (F10a,
                       lib/portal.ts). adoptInto returns the node to its primary
                       home when this host unmounts, so the toggle-off loses
                       nothing either. -->
                  <div class="split-secondary-host" data-split-host use:adoptInto={splitZoneEl}></div>
                {:else}
                  <div class="split-picker" data-testid="split-picker">
                    <p class="split-picker-hint">Pick a session for this pane</p>
                    <select
                      class="split-picker-select"
                      aria-label="secondary session"
                      value=""
                      onchange={(e) => {
                        const v = (e.currentTarget as HTMLSelectElement).value;
                        if (v) chooseSplit(v);
                      }}
                    >
                      <option value="" disabled>— choose a session —</option>
                      {#each workspaces.filter(w => w.id !== activeId) as ws (ws.id)}
                        <option value={ws.id}>{ws.title}</option>
                      {/each}
                    </select>
                  </div>
                {/if}
              {/if}
            {/snippet}
          </Stage>
        </div>

        <div class="divider divider-h" role="slider" aria-label="Resize shell drawer"
             aria-orientation="horizontal" aria-valuenow={layout.shellH} aria-valuemin={SHELL_MIN_H} aria-valuemax={SHELL_MAX_H}
             tabindex="0"
             onmousedown={startResizeShell}
             onkeydown={keyResizeShell}
             style:display={layout.collapsed["shell"] ? "none" : undefined}></div>

        <div data-zone="shell-drawer" class="shell-drawer-zone" data-terminal-zone role="group" aria-label="shell drawer"
             onpointerdown={() => { if (mode.current === "normal") mode.enterTerminal(); }}
             style:height={layout.collapsed["shell"] ? undefined : `${layout.shellH}px`}>
          <!-- One ShellPanel per MOUNTED session, holding that session's shell
               terminals, kept mounted, hidden with display, so each shell's
               openShell call runs once and switching sessions never displaces or
               SIGKILLs another session's shells. display:contents keeps the
               ShellPanel a direct flex child of the zone, so its layout stays
               unchanged. This list is gated on mountedWorkspaces, not
               openWorkspaces, so an AGENT pty exit does not unmount the
               independent shell ptys: the shells are their own ptys and outlive
               the agent (F20a). The panel renders only once its shell state is
               seeded, in the effect above, so the {#if} guards that. -->
          {#each mountedWorkspaces as ws (ws.id)}
            <div style:display={ws.id === activeId ? "contents" : "none"}>
              {#if shellStatesFor[ws.id]}
                <ShellPanel
                  cwd={ws.worktreePath}
                  panes={shellStatesFor[ws.id].panes}
                  activeId={shellStatesFor[ws.id].activeId}
                  splitId={shellStatesFor[ws.id].splitId}
                  collapsed={layout.collapsed["shell"] ?? false}
                  onSelect={(id) => shellSelect(ws.id, id)}
                  onNew={() => shellNew(ws.id)}
                  onClose={(id) => shellClose(ws.id, id)}
                  onToggleSplit={() => shellToggleSplit(ws.id)}
                  onToggleCollapse={() => layout.setCollapsed("shell", !layout.collapsed["shell"])} />
              {/if}
            </div>
          {/each}
        </div>
        <div data-zone="status-line" class="status-line">
          <span class="status-mode">{mode.current.toUpperCase()}</span>
          {#if pendingG}
            <span class="status-chord" data-testid="pending-chord" aria-hidden="true">g…</span>
          {/if}
          {#if active}
            <span class="status-sep" aria-hidden="true">·</span>
            <span class="status-session" title={active.title}>{active.title}</span>
            <span class="status-sep" aria-hidden="true">·</span>
            <span class="status-branch" title={active.branch}>{active.branch}</span>
            <span class="status-sep" aria-hidden="true">·</span>
            <span class="status-state">{active.state}</span>
            {@const ds = wsDiffStats[active.id]}
            {#if ds && (ds.added > 0 || ds.removed > 0)}
              <span class="status-sep" aria-hidden="true">·</span>
              <span class="status-diffstat" aria-label="+{ds.added} minus {ds.removed}">
                <span class="status-diff-added">+<span use:countUp={ds.added}></span></span>
                <span class="status-diff-removed">&minus;<span use:countUp={ds.removed}></span></span>
              </span>
            {/if}
            {#if ds && ds.files > 0}
              <span class="status-sep" aria-hidden="true">·</span>
              <span class="status-review-pill" aria-label="{ds.files} files to review"><span use:countUp={ds.files}></span> files</span>
            {/if}
          {/if}
          <span class="status-spacer"></span>
        </div>
      </div>
    </div>

    <CommandPalette
      open={mode.current === "command"}
      {commands}
      onRun={(id) => { runCommand(id); mode.leaveCommand(); }}
      onClose={() => mode.leaveCommand()}
    />

    {#if active && approvals[active.id]?.[0]}
      {@const headReq = approvals[active.id][0]}
      <div data-zone="approval-dock" class="approval-dock">
        <!-- Keyed by request, so each request gets a fresh card: a fresh
             keyboard arming delay, and no focus or pending keypress carried
             over from the request before it (FEC-16). -->
        {#key headReq.reqId}
          <ApprovalCard
            req={headReq}
            sessionCount={approvals[active.id].length}
            queue={approvals[active.id] ?? []}
            caps={active.caps}
            {onDecision}
            onApproveAll={() => decideAll("allow")}
            onDenyAll={() => decideAll("deny")}
          />
        {/key}
      </div>
    {/if}

    {#if notifOpen}
      <div data-zone="notification-hub" class="notification-hub-dock">
        <NotificationHub
          items={getItems()}
          dnd={getDnd()}
          onDismiss={(id) => markRead(id)}
          onToggleDnd={() => setDnd(!getDnd())}
          onClearRead={clearRead}
          onSelect={onNotificationSelect}
          onClose={() => openNotif(false)}
        />
      </div>
    {/if}

    <NewSessionDialog
      open={newSessionOpen}
      {repos}
      repoInfo={repoInfoByPath}
      loadBranches={(repo) => branches(repo)}
      onCreate={handleCreate}
      onClose={() => { newSessionOpen = false; newSessionInitialAgent = null; createError = null; }}
      initialAgent={newSessionInitialAgent}
      error={createError}
    />

    <ConfirmDialog
      open={confirmRemove !== null}
      message={confirmRemove ? `Remove workspace "${confirmRemove.title}"?` : ""}
      confirmLabel="Remove"
      destructive={true}
      note="Removes this session and its worktree from disk. The branch is kept."
      onConfirm={handleConfirmRemove}
      onCancel={handleCancelRemove}
    />

    <ConfirmDialog
      open={confirmDirty !== null}
      message={confirmDirty ? `"${confirmDirty.title}" has uncommitted changes. Force remove and discard them?` : ""}
      confirmLabel="Force remove"
      destructive={true}
      note="Uncommitted changes in the worktree will be permanently discarded."
      onConfirm={handleForceRemove}
      onCancel={() => { confirmDirty = null; }}
    />

    {#if previewWs}
      <div class="modal-overlay" role="presentation"
           onclick={(e) => { if (e.target === e.currentTarget) cancelPreview(); }}>
        <div class="resume-preview" data-testid="resume-preview" role="dialog" aria-modal="true" aria-label="Resume session">
          <h2 class="resume-preview-title">Resume: {previewWs.title}</h2>
          <dl class="resume-preview-meta">
            <dt>Branch</dt><dd>{previewWs.branch}</dd>
            {#if previewWs.baseRef}
              <dt>Forked from</dt><dd>{previewWs.baseRef}</dd>
            {/if}
            <dt>Agent</dt><dd>{previewWs.agent}</dd>
            <dt>Last active</dt><dd>{previewWs.lastActive ? new Date(previewWs.lastActive).toLocaleString() : "—"}</dd>
            <dt>Resume</dt><dd>{previewWs.willResume ? "Continues the previous conversation" : "Starts fresh"}</dd>
            {#if wsDiffStats[previewWs.id] && ((wsDiffStats[previewWs.id]?.added ?? 0) > 0 || (wsDiffStats[previewWs.id]?.removed ?? 0) > 0)}
              <dt>Changes</dt><dd class="diff-inline">+{wsDiffStats[previewWs.id].added} &minus;{wsDiffStats[previewWs.id].removed}</dd>
            {/if}
          </dl>
          <div class="resume-preview-actions">
            <button class="btn btn-primary" onclick={confirmPreview}>Open</button>
            <button class="btn" onclick={cancelPreview}>Cancel</button>
          </div>
        </div>
      </div>
    {/if}

    {#if cleanupOpen}
      <div class="modal-overlay" role="presentation">
        <CleanupPanel
          sessions={staleSessions}
          onClose={async () => {
            cleanupOpen = false;
            try {
              staleSessions = (await listStaleSessions()) ?? [];
              const fresh = await listWorkspaces();
              // Cleanup may have removed sessions. This prunes their open-set
              // entries and notifications, so nothing dangles for a gone session.
              const freshIds = new Set(fresh.map(w => w.id));
              for (const id of [...openIds]) {
                if (!freshIds.has(id)) { openIds.delete(id); everOpened.delete(id); dropForWorkspace(id); }
              }
              // Prune per-session state for any session cleanup removed. This may
              // include sessions that were never open, so it iterates the tracked
              // keys instead.
              const tracked = new Set([
                ...Object.keys(approvals), ...Object.keys(fsVersion),
                ...Object.keys(termEpoch), ...Object.keys(codePaths),
                ...Object.keys(wsDiffStats),
              ]);
              for (const id of tracked) if (!freshIds.has(id)) pruneWorkspaceState(id);
              workspaces = fresh;
            } catch { /* non-fatal */ }
          }}
          onOpen={(id) => { cleanupOpen = false; onSelect(id); }}
        />
      </div>
    {/if}

    <HelpDialog open={helpOpen} section={helpSection} onClose={() => { helpOpen = false; }} />

    <SettingsPanel open={settingsOpen} onClose={() => { settingsOpen = false; }} />

    {#if alwaysToast || pendingRemovals.length > 0}
      <div class="undo-toast-stack" aria-live="polite">
        {#if alwaysToast}
          <div class="undo-toast" role="status" data-testid="always-toast">
            {#if alwaysToast.rules.length > 0}
              <span class="undo-toast-msg">Always-allow rule added for {alwaysToast.tool}</span>
              <button class="undo-toast-btn" onclick={undoAlways}>Undo</button>
            {:else}
              <span class="undo-toast-msg">Always-allow rule added for {alwaysToast.tool}. Manage it in Settings.</span>
            {/if}
          </div>
        {/if}
        {#each pendingRemovals as pending (pending.ws.id)}
          <div class="undo-toast" role="status" data-testid="undo-toast">
            <span class="undo-toast-msg">Session removed</span>
            <button
              class="undo-toast-btn"
              onclick={() => handleUndoRemove(pending.ws.id)}
            >
              Undo
            </button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</ThemeProvider>

<style>
  .app-root         { display: flex; flex-direction: column; height: 100vh; overflow: hidden;
                      background: var(--perch-bg); color: var(--perch-text);
                      font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .main-area        { display: flex; flex: 1; min-height: 0; }
  .sidebar-zone     { flex-shrink: 0; overflow: hidden; border-right: 1px solid var(--perch-border);
                      transition: outline-color var(--perch-dur) var(--perch-ease); }
  .divider-v        { width: 4px; cursor: col-resize; background: var(--perch-border); flex-shrink: 0; }
  .divider-h        { height: 4px; cursor: row-resize; background: var(--perch-border); }
  .center-column    { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
  .stage-zone       { flex: 1; min-height: 0; display: flex; flex-direction: column;
                      transition: outline-color var(--perch-dur) var(--perch-ease); }
  .code-layout      { display: flex; flex-direction: row; flex: 1; min-height: 0; min-width: 0; }
  .diff-host        { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  .terminal-zone    { position: relative; display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  /* In-pane "session ended" overlay. It covers the dead xterm on the agent view
     only, since it lives inside the terminal-zone, which is hidden on the code
     and diff views. The semi-transparent backdrop keeps the pane legible in
     every theme. */
  .pane-ended {
    position: absolute; inset: 0;
    z-index: var(--perch-z-drop-overlay);
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    gap: var(--perch-sp-2);
    background: color-mix(in srgb, var(--perch-bg) 82%, transparent);
    color: var(--perch-text);
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
    text-align: center;
  }
  .pane-ended p { margin: 0; color: var(--perch-text-dim); }
  /* Transient ring pulse that draws the eye when the active agent wants input. */
  .terminal-zone.input-emphasis { animation: perch-emphasis var(--perch-dur-pop) var(--perch-ease); }
  @media (prefers-reduced-motion: reduce) {
    .terminal-zone.input-emphasis { animation: none; }
  }
  /* display:flex plus column gives the JS-driven height a flex context to
     distribute, so the ShellDrawer inside, a display:contents wrapper's child,
     stretches to the zone's real height. This keeps its xterm sized to the
     visible window and its scroll position correct. */
  .shell-drawer-zone { display: flex; flex-direction: column; flex-shrink: 0; overflow: hidden;
                       border-top: 1px solid var(--perch-border);
                       transition: outline-color var(--perch-dur) var(--perch-ease); }
  /* Active-zone accent ring: a you-are-here cue, not a focus indicator.
     It uses outline, not an inset box-shadow, so it paints over opaque child
     panes and is not clipped by the zones' overflow:hidden. A negative offset
     draws it inside the zone. */
  [data-zone="sidebar"]:focus-within,
  [data-zone="stage"]:focus-within,
  [data-zone="shell-drawer"]:focus-within {
    outline: var(--perch-ring-w) solid var(--perch-ring-color);
    outline-offset: calc(-1 * var(--perch-ring-w));
  }

  .filter-input      { display: block; width: 100%; box-sizing: border-box;
                       padding: 0.25rem 0.5rem; border: none; border-bottom: 1px solid var(--perch-border-strong);
                       background: var(--perch-bg); color: var(--perch-text);
                       font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .filter-input:focus { outline: 1px solid var(--perch-accent); }
  .approval-dock     { position: absolute; bottom: 2rem; left: 50%; transform: translateX(-50%);
                       z-index: var(--perch-z-approval); min-width: 320px; max-width: 560px; }
  .notification-hub-dock { position: absolute; top: 2.5rem; right: 0; z-index: var(--perch-z-notify);
                            width: 320px; max-height: 60vh; overflow-y: auto;
                            border-left: 1px solid var(--perch-border); }
  /* No background here: the hub owns its own solid surface, so the dock is a
     bare positioning wrapper. */
  /* Status line: spans the full bottom of the center column, and is always in
     the DOM. */
  .status-line       { display: flex; align-items: center; flex-shrink: 0;
                       height: 24px; padding: 0 var(--perch-sp-1);
                       border-top: 1px solid var(--perch-border);
                       background: var(--perch-surface);
                       font-size: var(--perch-fs-caption);
                       color: var(--perch-text-dim);
                       gap: var(--perch-sp-1);
                       font-family: var(--perch-font-sans); }
  .status-mode       { font-size: var(--perch-fs-label); font-weight: 600;
                       letter-spacing: 0.06em; text-transform: uppercase;
                       color: var(--perch-accent); }
  /* Transient chord indicator, for example "g…" while the g-prefix is armed. */
  .status-chord      { font-family: var(--perch-font-mono); font-size: var(--perch-fs-caption);
                       color: var(--perch-accent); font-weight: 600; }
  .status-sep        { color: var(--perch-border); }
  .status-session    { color: var(--perch-text); font-weight: 500; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status-branch     { font-family: var(--perch-font-mono); font-size: var(--perch-fs-caption); max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status-state      { color: var(--perch-text-dim); }
  .status-diffstat   { display: flex; gap: var(--perch-sp-1);
                       font-family: var(--perch-font-mono); font-size: var(--perch-fs-caption); }
  .status-diff-added   { color: var(--perch-ok); }
  .status-diff-removed { color: var(--perch-err); }
  /* Goal-gradient "files to review" pill. It shrinks as the user stages files. */
  .status-review-pill { padding: 0 var(--perch-sp-1);
                        border-radius: var(--perch-radius-sm);
                        background: color-mix(in srgb, var(--perch-accent) 18%, transparent);
                        color: var(--perch-text); font-size: var(--perch-fs-caption); }
  .status-spacer     { flex: 1; }

  /* Sidebar collapse toggle rail */
  .sidebar-toggle-rail {
    display: flex; align-items: center; justify-content: center;
    width: var(--perch-sp-2);
    flex-shrink: 0;
    background: var(--perch-surface);
    border: none;
    border-right: 1px solid var(--perch-border);
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    transition: color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
    z-index: var(--perch-z-sidebar-rail);
  }
  .sidebar-toggle-rail:hover { color: var(--perch-text); background: color-mix(in srgb, var(--perch-accent) 10%, transparent); }
  .sidebar-toggle-rail:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: -2px; }
  /* When the sidebar is collapsed, the rail keeps its border, but the aside
     element becomes width:0. */
  .sidebar-toggle-rail.sidebar-collapsed { border-right: 1px solid var(--perch-border); }

  /* First-run empty state */
  .empty-state {
    display: flex; align-items: center; justify-content: center;
    flex: 1; height: 100%;
    background: var(--perch-bg);
  }
  .empty-state-card {
    display: flex; flex-direction: column; align-items: center; gap: var(--perch-sp-3);
    padding: var(--perch-sp-4);
    border: 1px solid var(--perch-border);
    border-radius: 8px;
    background: var(--perch-surface);
    max-width: 360px; text-align: center;
  }
  .empty-state-title {
    margin: 0;
    font-size: var(--perch-fs-body);
    font-weight: 600;
    color: var(--perch-text);
  }
  .empty-state-hint {
    margin: 0;
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
  }
  /* First-run guidance: calm, secondary copy under the call-to-action buttons. */
  .empty-state-note {
    display: flex;
    flex-direction: column;
    gap: var(--perch-sp-2);
    width: 100%;
    margin-top: var(--perch-sp-1);
    padding-top: var(--perch-sp-3);
    border-top: 1px solid var(--perch-border);
    text-align: left;
    font-size: var(--perch-fs-caption);
    line-height: 1.5;
    color: var(--perch-text-dim);
  }
  .empty-state-note p { margin: 0; }
  .empty-state-note code {
    font-family: var(--perch-font-mono);
    color: var(--perch-text);
  }
  .empty-state-btn {
    display: inline-flex; align-items: center; justify-content: center;
    padding: 6px 20px;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: filter var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease);
  }
  .empty-state-btn-primary {
    background: var(--perch-accent); color: var(--perch-accent-fg);
    border: 1px solid var(--perch-accent);
    font-weight: 600;
  }
  .empty-state-btn-primary:hover { filter: brightness(1.1); }
  .empty-state-btn-primary:active { filter: brightness(0.92); }
  .empty-state-templates {
    display: flex; flex-direction: column; align-items: center; gap: var(--perch-sp-1);
    width: 100%;
  }
  .empty-state-templates-label {
    font-size: var(--perch-fs-caption); color: var(--perch-text-dim); text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .empty-state-btn-template {
    background: var(--perch-bg); color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    width: 100%;
  }
  .empty-state-btn-template:hover {
    border-color: var(--perch-accent); color: var(--perch-accent);
  }
  .empty-state-btn-template:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: 2px;
  }

  /* Home view: a vertical split, with the welcome card above and the persistent
     shell below. */
  .home-view { display: flex; flex-direction: column; align-items: stretch; justify-content: flex-start; width: 100%; height: 100%; overflow: hidden; }
  .home-welcome { flex: 1; display: flex; align-items: center; justify-content: center; overflow: hidden; }
  /* Same flex-column context as .shell-drawer-zone, so the home ShellDrawer fills
     the fixed height. This keeps its xterm sized to the visible window and its
     scroll position correct. */
  .home-shell-zone { display: flex; flex-direction: column; flex: none; height: 220px; border-top: 1px solid var(--perch-border); overflow: hidden; }

  /* Split secondary host: adopts the split session's relocated terminal-zone.
     See lib/portal.ts. It mirrors the primary pane's flex, so the adopted zone
     fills the secondary pane exactly as it would in the primary pane. */
  .split-secondary-host {
    display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0;
  }

  /* Split pane session picker */
  .split-picker {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    flex: 1; height: 100%; gap: var(--perch-sp-2);
    background: var(--perch-bg);
  }
  .split-picker-hint {
    margin: 0;
    font-size: var(--perch-fs-caption); color: var(--perch-text-dim);
  }
  .split-picker-select {
    padding: 4px 8px;
    background: var(--perch-surface); color: var(--perch-text);
    border: 1px solid var(--perch-border-strong); border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
    cursor: pointer;
  }
  .split-picker-select:focus { outline: 1px solid var(--perch-accent); }

  /* Undo toast, stacked at the bottom right. */
  .undo-toast-stack {
    position: fixed; bottom: var(--perch-sp-3); right: var(--perch-sp-3);
    z-index: var(--perch-z-undo-toast);
    display: flex; flex-direction: column; gap: var(--perch-sp-1);
  }
  .undo-toast {
    display: flex; align-items: center; gap: var(--perch-sp-2);
    padding: var(--perch-sp-1) var(--perch-sp-2);
    /* Solid, never glass: this toast floats over the composited agent terminal,
       where WebKitGTK paints backdrop-filter surfaces transparent and the
       terminal bleeds through. This mirrors the ApprovalCard and NotificationHub
       fix (F59a). */
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-glass-shadow);
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
    color: var(--perch-text);
    min-width: 220px;
    animation: toast-in var(--perch-dur) var(--perch-ease);
  }
  @keyframes toast-in {
    from { opacity: 0; transform: translateY(8px); }
    to   { opacity: 1; transform: translateY(0); }
  }
  @media (prefers-reduced-motion: reduce) {
    .undo-toast { animation: none; }
  }
  .undo-toast-msg { flex: 1; }
  .undo-toast-btn {
    padding: 3px 10px;
    background: var(--perch-accent); color: var(--perch-accent-fg);
    border: none; border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: filter var(--perch-dur) var(--perch-ease);
  }
  .undo-toast-btn:hover { filter: brightness(1.1); }
  .undo-toast-btn:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: 2px; }

  .stale-banner { display: flex; align-items: center; gap: var(--perch-sp-2); padding: 6px var(--perch-sp-3); background: color-mix(in srgb, var(--perch-warn) 15%, var(--perch-bg)); border-bottom: 1px solid color-mix(in srgb, var(--perch-warn) 40%, transparent); font-size: var(--perch-fs-caption); color: var(--perch-text); flex-shrink: 0; }
  .stale-banner-link { background: transparent; border: none; color: var(--perch-accent); cursor: pointer; font-size: var(--perch-fs-caption); text-decoration: underline; }
  .stale-banner-dismiss { margin-left: auto; background: transparent; border: none; color: var(--perch-text-dim); cursor: pointer; font-size: 14px; }
  .modal-overlay { position: fixed; inset: 0; background: var(--perch-scrim); display: flex; align-items: center; justify-content: center; z-index: var(--perch-z-modal); }

  /* Resume preview modal. */
  /* Solid, never glass: the resume preview can float over the composited agent
     terminal, where WebKitGTK paints backdrop-filter surfaces transparent (F59a). */
  .resume-preview { background: var(--perch-glass-bg-solid); border: 1px solid var(--perch-glass-border); border-radius: var(--perch-radius-lg); padding: var(--perch-sp-3); min-width: 320px; max-width: 480px; color: var(--perch-text); font-family: var(--perch-font-sans); }
  .resume-preview-title { margin: 0 0 var(--perch-sp-2) 0; font-size: var(--perch-fs-body); font-weight: 600; }
  .resume-preview-meta { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; margin: 0 0 var(--perch-sp-2) 0; font-size: var(--perch-fs-caption); }
  .resume-preview-meta dt { color: var(--perch-text-dim); }
  .resume-preview-meta dd { margin: 0; }
  .diff-inline { font-family: var(--perch-font-mono); }
  .resume-preview-actions { display: flex; gap: var(--perch-sp-1); justify-content: flex-end; padding-top: var(--perch-sp-1); border-top: 1px solid var(--perch-border); }
  .btn { display: inline-flex; align-items: center; justify-content: center; padding: 5px 14px; border-radius: var(--perch-radius-sm); font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); cursor: pointer; transition: filter var(--perch-dur) var(--perch-ease); background: var(--perch-surface); color: var(--perch-text); border: 1px solid var(--perch-border-strong); }
  .btn:hover { filter: brightness(1.08); }
  .btn:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: 2px; }
  .btn-primary { background: var(--perch-accent); color: var(--perch-accent-fg); border-color: var(--perch-accent); font-weight: 600; }
  .btn-primary:hover { filter: brightness(1.1); }
</style>
