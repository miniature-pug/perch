<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import ThemeProvider      from "./lib/ThemeProvider.svelte";
  import Sidebar            from "./lib/Sidebar.svelte";
  import Stage              from "./lib/Stage.svelte";
  import ShellDrawer        from "./lib/ShellDrawer.svelte";
  import Terminal           from "./lib/Terminal.svelte";
  import Editor             from "./lib/Editor.svelte";
  import Preview            from "./lib/Preview.svelte";
  import FileTree           from "./lib/FileTree.svelte";
  import { isPreviewable, previewKind } from "./lib/preview";
  import { focusOnMount, countUp } from "./lib/actions";
  import DiffView           from "./lib/DiffView.svelte";
  import { shouldFocusAwaitingInput } from "./lib/engagement";
  import MenuBar            from "./lib/MenuBar.svelte";
  import CommandPalette     from "./lib/CommandPalette.svelte";
  import NewSessionDialog   from "./lib/NewSessionDialog.svelte";
  import ConfirmDialog      from "./lib/ConfirmDialog.svelte";
  import HelpDialog         from "./lib/HelpDialog.svelte";
  import SettingsPanel      from "./lib/SettingsPanel.svelte";
  import DragDrop           from "./lib/DragDrop.svelte";
  import { SvelteSet }      from "svelte/reactivity";
  import { layout }         from "./lib/stores/layout.svelte";
  import { mode }           from "./lib/stores/mode.svelte";
  import { settings }       from "./lib/stores/settings.svelte";
  import ApprovalCard       from "./lib/ApprovalCard.svelte";
  import NotificationHub    from "./lib/NotificationHub.svelte";
  import { getDnd, setDnd, addBlocking, addAmbient, addRoutine, getItems, markRead, clearRead, markAllRead, dropForWorkspace } from "./lib/stores/notifications.svelte";
  import CleanupPanel from "./lib/CleanupPanel.svelte";
  import { listWorkspaces, createWorkspace, workspaceForBranch, removeWorkspace, openWorkspace, closeWorkspace, revealInFiles, onAgentEvent, onNotify, onFsChanged, onWorkspaceAttach, approve, branches, readFile, setWindowFocus, writeToPty, discoverRepos, diffStat, listStaleSessions, forceRemoveWorkspace, homeShellCwd as fetchHomeShellCwd } from "./lib/wails";
  import type { WorkspaceVM, ApprovalReq, StaleSessionVM } from "./lib/wails";
  import { UNDO_REMOVE_DELAY_MS, SIDEBAR_MIN_W, SIDEBAR_MAX_W, SHELL_MIN_H, SHELL_MAX_H, RESIZE_STEP_PX, THEMES, MIME_SESSION, MENTION_PREFIX, AGENT_CLAUDE, AGENT_OPENCODE } from "./lib/constants";

  let workspaces      = $state<WorkspaceVM[]>([]);
  let activeId        = $state<string | null>(null);
  // Sessions with a live pty this app-run. A session is "open" once openWorkspace
  // has succeeded and until it is closed/removed. Drives: focus-vs-reopen routing
  // in onSelect, the sidebar "closed" dim cue, and close/remove cleanup.
  // SvelteSet so .add()/.delete()/.has() are genuinely reactive — the in-pane
  // reopen overlay and the Sidebar "closed" cue both read openIds.has() directly.
  let openIds         = new SvelteSet<string>();
  // Per-session terminal epoch. Bumped on every genuine (re)open so the agent
  // terminal-zone {#key} remounts a fresh xterm (a respawned pty must not
  // interleave over a stale buffer). A view switch or focus does NOT bump it,
  // so the scroll buffer survives those.
  let termEpoch       = $state<Record<string, number>>({});
  let codePath        = $state<string | null>(null);
  let previewContent  = $state<string>("");
  let approvals       = $state<Record<string, ApprovalReq>>({});
  let fsVersion  = $state<Record<string, number>>({});
  let wsDiffStats = $state<Record<string, { added: number; removed: number; files: number }>>({});

  // Awaiting-input auto-focus: a ref to the primary agent terminal so we can
  // route the keyboard to it without a click, plus a transient emphasis flag
  // pulsed when the ACTIVE agent asks for input.
  let primaryTerm    = $state<{ focus: () => void } | undefined>(undefined);
  let emphasizeInput = $state(false);

  // Repo discovery — populated lazily when the New Session dialog opens.
  let discoveredRepoPaths = $state<string[]>([]);

  // Pending removals — each entry is an optimistically-hidden workspace with a
  // scheduled real removeWorkspace call.  Using an array lets us handle multiple
  // concurrent removals without any special-case logic.
  interface PendingRemoval { ws: WorkspaceVM; timer: ReturnType<typeof setTimeout>; }
  let pendingRemovals = $state<PendingRemoval[]>([]);

  // Keymap state machine helpers
  let pendingG     = $state(false);
  let pendingLeave = $state(false);
  let filtering    = $state(false);
  let filterQuery  = $state("");

  // Load file content when codePath changes to a previewable (non-image) file.
  // Cancellation guard prevents a stale readFile resolve from clobbering newer content.
  $effect(() => {
    const p = codePath;
    if (!p || !isPreviewable(p) || previewKind(p) === "image") { previewContent = ""; return; }
    let cancelled = false;
    readFile(p)
      .then((c) => { if (!cancelled) previewContent = c; })
      .catch(() => { if (!cancelled) previewContent = ""; });
    return () => { cancelled = true; };
  });

  // Dialog / overlay state
  let newSessionOpen        = $state(false);
  let newSessionInitialAgent = $state<string | null>(null);
  let confirmRemove         = $state<WorkspaceVM | null>(null);
  let notifOpen             = $state(false);
  let helpOpen              = $state(false);
  let settingsOpen          = $state(false);
  let staleSessions         = $state<StaleSessionVM[]>([]);
  let homeShellCwdValue     = $state<string>("");
  let staleBannerDismissed  = $state(false);
  let cleanupOpen           = $state(false);
  let confirmDirty          = $state<WorkspaceVM | null>(null);

  const active          = $derived(workspaces.find(w => w.id === activeId) ?? null);
  const unreadCount     = $derived(getItems().filter(n => !n.read).length);
  const approvalQueue   = $derived(Object.values(approvals).filter(Boolean) as import("./lib/wails").ApprovalReq[]);

  // Apply user-defined order: ids in layout.order come first (in that order),
  // remaining workspaces (not yet in order) follow in backend order.
  const orderedWorkspaces = $derived((() => {
    const order = layout.order;
    if (!order.length) return visibleWorkspaces;
    const indexed = new Map(visibleWorkspaces.map((w, i) => [w.id, { w, i }]));
    const head = order.map(id => indexed.get(id)?.w).filter(Boolean) as typeof visibleWorkspaces;
    const headSet = new Set(order);
    const tail = visibleWorkspaces.filter(w => !headSet.has(w.id));
    return [...head, ...tail];
  })());

  // Filtered workspace list for Sidebar (j/k also operate on this list when filtering).
  // Uses orderedWorkspaces so optimistically-removed items are excluded immediately.
  const shownWorkspaces = $derived(
    filtering && filterQuery
      ? orderedWorkspaces.filter(w => w.title.toLowerCase().includes(filterQuery.toLowerCase()))
      : orderedWorkspaces
  );

  // Reorder callback from Sidebar: move draggedId to the position of targetId.
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

  // Derived repo list for NewSessionDialog — union of workspace-derived paths and
  // any paths returned by discoverRepos() (populated lazily on dialog open).
  const repos = $derived([...new Set([
    ...workspaces.map(w => w.worktreePath),
    ...discoveredRepoPaths,
  ])]);

  // Visible workspaces — excludes any that are pending an optimistic removal.
  const pendingRemovalIds = $derived(new Set(pendingRemovals.map(p => p.ws.id)));
  const visibleWorkspaces = $derived(workspaces.filter(w => !pendingRemovalIds.has(w.id)));

  // Off-functions captured from wails event subscriptions (subscribed synchronously in onMount).
  let offAgentEvent:        (() => void) | null = null;
  let offNotify:            (() => void) | null = null;
  let offFsChanged:         (() => void) | null = null;
  let offWorkspaceAttach:   (() => void) | null = null;

  // Window focus/blur handlers — report focus state to the backend so it can gate
  // OS desktop notifications (only fire when the window is unfocused).
  function onWindowFocus() { setWindowFocus(true).catch(() => {}); }
  function onWindowBlur()  { setWindowFocus(false).catch(() => {}); }

  // Aggregate +N −N diffstat per workspace.
  // A missing or non-git worktree must not throw; catch suppresses errors silently.
  async function refreshDiffStat(ws: WorkspaceVM) {
    try {
      const files = await diffStat(ws.worktreePath);
      let added = 0, removed = 0;
      for (const f of files) { added += f.added; removed += f.removed; }
      wsDiffStats = { ...wsDiffStats, [ws.id]: { added, removed, files: files.length } };
    } catch {
      // non-git or missing worktree — leave any existing entry untouched
    }
  }

  // The active workspace just asked for input: route the user to its pane so
  // they can answer immediately (the question is answered in the agent's own TUI).
  // Only ever called for the active workspace on the agent view (see shouldFocusAwaitingInput).
  function focusAwaitingInput() {
    if (mode.current === "normal") mode.enterTerminal();
    emphasizeInput = false; // reset so the pulse restarts even on a rapid re-ask
    requestAnimationFrame(() => {
      emphasizeInput = true;
      primaryTerm?.focus();
    });
  }

  // Capture-phase pointerdown on the app root: when in terminal mode and the
  // click target is NOT inside a .terminal / [data-terminal-zone] element, leave
  // terminal mode and return to NORMAL.  We use capture so this fires before any
  // child handler, but we NEVER preventDefault/stopPropagation so other handlers
  // (menus, buttons, xterm) still receive the event.
  function onAppPointerDown(e: PointerEvent) {
    if (mode.current !== "terminal") return;
    const target = e.target as Element | null;
    if (!target) return;
    // Stay in terminal mode if the click is inside the terminal zone
    if (target.closest("[data-terminal-zone]")) return;
    mode.leaveTerminal();
  }

  onMount(async () => {
    // Report initial focus state and register focus/blur listeners.
    setWindowFocus(document.hasFocus()).catch(() => {});
    window.addEventListener("focus", onWindowFocus);
    window.addEventListener("blur",  onWindowBlur);

    // Subscribe synchronously BEFORE any await so off-fns are always captured.
    offAgentEvent = onAgentEvent((ev) => {
      const ws = workspaces.find(w => w.id === ev.workspaceId);
      if (!ws) return;
      const prev = ws.state;
      if (ev.state) ws.state = ev.state;
      if (ev.approval) approvals[ev.workspaceId] = ev.approval;
      // Only the ACTIVE workspace, only the agent view, only on the edge.
      if (ev.state && shouldFocusAwaitingInput(prev, ev.state, ev.workspaceId, activeId, layout.view)) {
        focusAwaitingInput();
      }
    });

    offNotify = onNotify((n) => {
      if      (n.tier === "blocking") addBlocking(n.workspaceId, n.title, n.body);
      else if (n.tier === "ambient")  addAmbient (n.workspaceId, n.title, n.body);
      else                            addRoutine (n.workspaceId, n.title, n.body);
    });

    offFsChanged = onFsChanged((p) => {
      fsVersion[p.workspaceId] = (fsVersion[p.workspaceId] ?? 0) + 1;
      const ws = workspaces.find(w => w.id === p.workspaceId);
      if (ws) refreshDiffStat(ws);
    });

    offWorkspaceAttach = onWorkspaceAttach((p) => {
      // Find by exact worktreePath first, then fuzzy match on title/branch/path.
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

    await Promise.all([settings.load(), layout.restore()]);
    workspaces = await listWorkspaces();
    // Refresh diffstats for all loaded workspaces (fire-and-forget, event-driven updates thereafter).
    for (const ws of workspaces) refreshDiffStat(ws);
    try {
      staleSessions = (await listStaleSessions()) ?? [];
    } catch {
      // non-fatal — never block startup
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
    window.removeEventListener("focus", onWindowFocus);
    window.removeEventListener("blur",  onWindowBlur);
    // Cancel any pending deferred removals to avoid use-after-unmount calls.
    for (const p of pendingRemovals) clearTimeout(p.timer);
  });

  // Resume preview state: the workspace pending confirmation before opening.
  let previewWs = $state<WorkspaceVM | null>(null);

  function onSelect(id: string) {
    // Already showing this session → no-op (never reopen the live pane).
    if (id === activeId) return;
    const ws = workspaces.find(w => w.id === id) ?? null;
    if (!ws) return;
    // Open but not active → just FOCUS it. No preview, no reopen: the pty is
    // live and re-running openWorkspace would respawn it and re-type the launch
    // command over the running xterm.
    if (openIds.has(id)) { activeId = id; return; }
    // Not open → show the resume-preview before spawning the pty.
    previewWs = ws;
  }

  // The single open path: bump the terminal epoch (fresh xterm for the respawned
  // pty), mark active + open, then spawn the pty. On failure, roll the open flag
  // back so the row does not falsely read as live.
  async function openSession(id: string) {
    termEpoch[id] = (termEpoch[id] ?? 0) + 1;
    activeId = id;
    openIds.add(id);
    try {
      await openWorkspace(id);
    } catch {
      openIds.delete(id);
    }
  }

  // The primary agent pty exited: leave keyboard mode, drop the session from the
  // open set (so the reopen overlay shows and the row dims), and reset its state
  // so no stale attention lingers. activeId is KEPT so the overlay is reachable.
  function handleAgentExit(id: string) {
    mode.leaveTerminal();
    openIds.delete(id);
    const ws = workspaces.find(w => w.id === id);
    if (ws) ws.state = "idle";
  }

  async function confirmPreview() {
    if (!previewWs) return;
    const id = previewWs.id;
    previewWs = null;
    await openSession(id);
  }

  function cancelPreview() { previewWs = null; }

  // Clicking a notification focuses its session. Focus if already open; open
  // directly (no preview — the user's intent is unambiguous) if it is closed.
  function onNotificationSelect(wsId: string) {
    if (!wsId) return;
    const ws = workspaces.find(w => w.id === wsId);
    if (!ws) return;
    notifOpen = false;
    if (openIds.has(wsId)) {
      activeId = wsId;
    } else {
      openSession(wsId);
    }
  }

  // Route all hub open/close through here so opening always marks the backlog
  // read (seeing the hub is the catch-up → the unread badge clears).
  function openNotif(open: boolean) {
    notifOpen = open;
    if (open) markAllRead();
  }

  function openNewSession(initialAgent?: string) {
    newSessionOpen = true;
    // Lazily discover repos each time the dialog opens — runs in background,
    // merges with workspace-derived paths (deduped in the repos $derived).
    discoverRepos()
      .then((list) => { discoveredRepoPaths = list.map(r => r.path); })
      .catch(() => {}); // non-fatal — fresh-install still sees workspace paths
    // Guard: only accept a genuine string (Sidebar passes this as onclick which
    // injects a MouseEvent; we must not treat that as an agent name).
    newSessionInitialAgent = typeof initialAgent === "string" ? initialAgent : null;
  }

  async function handleCreate(agent: string, repo: string, baseRef: string, branch: string, worktree: boolean) {
    // Guard: if the branch is already owned by a perch session, offer resume instead.
    const existing = await workspaceForBranch(repo, branch);
    if (existing.found) {
      // Branch already in use — resume that session rather than creating a duplicate.
      newSessionOpen = false;
      newSessionInitialAgent = null;
      await onSelect(existing.id);
      return;
    }
    try {
      const vm = await createWorkspace(agent, repo, baseRef, branch, worktree);
      workspaces = await listWorkspaces();
      newSessionOpen = false;
      // Creating a session spawns its pty immediately. onSelect sets activeId
      // and opens the workspace in one step, so the new session is live rather
      // than a selected-but-dead row.
      await onSelect(vm.id);
    } catch (e) {
      const msg = String(e);
      if (msg.includes("uncommitted changes")) {
        // ErrWorktreeDirty: non-worktree session can't switch to a different branch
        // while the working tree has uncommitted changes.
        addBlocking("", "Cannot switch branch",
          "Your working tree has uncommitted changes. Commit or stash them before switching to a different branch.");
      } else {
        addBlocking("", "Failed to create session", msg);
      }
      // Keep the dialog open so the user can correct their choice.
    }
  }

  function requestRemove(ws: WorkspaceVM) {
    confirmRemove = ws;
  }

  function handleConfirmRemove() {
    if (!confirmRemove) return;
    const wsToRemove = confirmRemove;
    confirmRemove = null;

    // Finalize any existing pending removal for the same id (edge-case guard).
    finalizePendingRemoval(wsToRemove.id);

    // Optimistically hide the workspace immediately — visibleWorkspaces $derived
    // filters by pendingRemovalIds so no listWorkspaces() refresh is needed yet.
    if (activeId === wsToRemove.id) {
      const remaining = visibleWorkspaces.filter(w => w.id !== wsToRemove.id);
      activeId = remaining[0]?.id ?? null;
    }
    if (layout.splitId === wsToRemove.id) layout.setSplitId(null);

    const timer = setTimeout(async () => {
      // Time's up — commit the removal for real.
      pendingRemovals = pendingRemovals.filter(p => p.ws.id !== wsToRemove.id);
      try {
        await removeWorkspace(wsToRemove.id);
        openIds.delete(wsToRemove.id);
        dropForWorkspace(wsToRemove.id);
        workspaces = await listWorkspaces();
        if (activeId === wsToRemove.id) activeId = workspaces[0]?.id ?? null;
      } catch (err) {
        // If the backend call fails, put the workspace back.
        workspaces = await listWorkspaces();
        if (String(err).includes("uncommitted changes")) {
          confirmDirty = wsToRemove;
        }
      }
    }, UNDO_REMOVE_DELAY_MS);

    pendingRemovals = [...pendingRemovals, { ws: wsToRemove, timer }];
  }

  /** Cancel a pending deferred removal and return the workspace to the visible list. */
  function handleUndoRemove(id: string) {
    const entry = pendingRemovals.find(p => p.ws.id === id);
    if (!entry) return;
    clearTimeout(entry.timer);
    pendingRemovals = pendingRemovals.filter(p => p.ws.id !== id);
    // workspace is already in `workspaces`; visibleWorkspaces $derived will restore it.
  }

  /** Force-commit a pending removal without waiting for the timer. */
  function finalizePendingRemoval(id: string) {
    const entry = pendingRemovals.find(p => p.ws.id === id);
    if (!entry) return;
    clearTimeout(entry.timer);
    pendingRemovals = pendingRemovals.filter(p => p.ws.id !== id);
    if (layout.splitId === id) layout.setSplitId(null);
    openIds.delete(id);
    dropForWorkspace(id);
    // Fire-and-forget — do not await so we don't block the caller.
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
      dropForWorkspace(ws.id);
      workspaces = await listWorkspaces();
      if (activeId === ws.id) activeId = workspaces[0]?.id ?? null;
    } catch {
      workspaces = await listWorkspaces();
    }
  }

  // ---------------------------------------------------------------------------
  // Command registry — keyed by the ids MenuBar actually emits.
  // ---------------------------------------------------------------------------
  // ---------------------------------------------------------------------------
  // Bulk-approval helper — SAFETY-SCOPED to the ACTIVE workspace only.
  //
  // A batch ("Approve all" / "Deny all") action MUST only affect the approval the
  // user is actually looking at — the active workspace's pending request. It must
  // NEVER silently green-light a tool waiting in a DIFFERENT, unseen workspace.
  // The cross-workspace queue still DRIVES the batch-button render condition (the
  // "N pending" indicator), but the ACTION resolves active.id alone.
  //
  // The data model is one-approval-per-workspace (approvals[wsId] = req), so the
  // active workspace has at most one pending request. Mid-flight safe: re-reads
  // approvals after the await and only clears the active key if it still holds the
  // SAME reqId we acted on (a newer event may have replaced it).
  // ---------------------------------------------------------------------------
  async function decideAll(decision: "allow" | "deny") {
    const wsId = active?.id;
    if (!wsId) return;
    const req = approvals[wsId];                          // active's pending request (if any)
    if (!req) return;
    try {
      await approve(req.reqId, decision);
      if (approvals[wsId]?.reqId === req.reqId) {         // not replaced mid-flight → clear it
        const { [wsId]: _, ...rest } = approvals;
        approvals = rest;
      }
    } catch (e) {
      addBlocking(wsId, "Approval failed", String(e));
    }
  }

  type Command = { id: string; group: string; label: string; keybinding?: string; run: () => void | Promise<void> };

  const commands: Command[] = [
    // Session
    { id: "session:new",    group: "Session", label: "New session",        run: () => openNewSession() },
    { id: "session:close",  group: "Session", label: "Close session",      run: () => {
        if (!active) return;
        const id = active.id;
        closeWorkspace(id).then(() => {
          // Clean up per-workspace frontend state on close
          const { [id]: _a, ...restA } = approvals; approvals = restA;
          const { [id]: _f, ...restF } = fsVersion;  fsVersion = restF;
          // The pty is gone: drop it from the open set so the row dims and a
          // later click routes through the resume-preview reopen path.
          openIds.delete(id);
          // Clear any stuck attention on the now-dead session.
          const ws = workspaces.find(w => w.id === id);
          if (ws) ws.state = "idle";
          // Keep activeId so the in-pane "This session has ended / Reopen"
          // overlay stays reachable (the session left openIds above).
        }).catch(() => {});
      } },
    { id: "session:remove", group: "Session", label: "Remove session",     run: () => { if (active) requestRemove(active); } },
    // Worktree
    { id: "worktree:open",   group: "Worktree", label: "Open worktree",    keybinding: "Enter",       run: () => { if (active) openSession(active.id); } },
    { id: "worktree:reveal", group: "Worktree", label: "Reveal in Files",  run: () => { if (active) revealInFiles(active.worktreePath); } },
    // View
    { id: "view:agent", group: "View", label: "Agent view",  keybinding: "1",  run: () => layout.setView("agent") },
    { id: "view:code",  group: "View", label: "Code view",   keybinding: "2",  run: () => layout.setView("code")  },
    { id: "view:diff",  group: "View", label: "Diff view",   keybinding: "3",  run: () => layout.setView("diff")  },
    { id: "view:split", group: "View", label: "Split",       keybinding: "\\", run: () => layout.toggleSplit()   },
    { id: "view:theme", group: "View", label: "Cycle theme",                   run: () => {
        const idx = THEMES.indexOf(settings.theme);
        settings.setTheme(THEMES[(idx + 1) % THEMES.length]);
      },
    },
    // Agent bulk actions
    { id: "agent:approve-all", group: "Agent", label: "Approve all pending", run: () => decideAll("allow") },
    { id: "agent:deny-all",    group: "Agent", label: "Deny all pending",    run: () => decideAll("deny")  },
    // Notifications
    { id: "notifications:open", group: "Notifications", label: "Open notifications",    run: () => { openNotif(!notifOpen); } },
    { id: "notifications:dnd",  group: "Notifications", label: "Toggle Do Not Disturb", run: () => setDnd(!getDnd()) },
    // Help
    { id: "help:shortcuts", group: "Help", label: "Keyboard shortcuts", run: () => { helpOpen = true; } },
    { id: "help:about",     group: "Help", label: "About perch",        run: () => { helpOpen = true; } },
    // Settings
    { id: "settings:open", group: "Settings", label: "Settings…", run: () => { settingsOpen = true; } },
  ];

  function runCommand(id: string) {
    const cmd = commands.find(c => c.id === id);
    if (cmd) cmd.run();
  }

  // ---------------------------------------------------------------------------
  // Keymap — full state machine
  // ---------------------------------------------------------------------------
  function onKeyDown(e: KeyboardEvent) {
    // COMMAND mode: let the CommandPalette handle everything.
    if (mode.current === "command") return;

    // TERMINAL mode: only intercept the Ctrl-\ Ctrl-n leave sequence.
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
      // Any other key cancels the pending leave prefix; do NOT prevent default
      // so the key reaches the pty.
      pendingLeave = false;
      return;
    }

    // NORMAL mode ---------------------------------------------------------------

    // Ctrl-K / Cmd-K → command palette. Check before the switch so
    // the plain "k" workspace-nav case does not fire when Ctrl is held.
    // Do NOT intercept when focus is inside an input or textarea (would hijack typing).
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
      if (e.key === "d") { e.preventDefault(); layout.setView("diff"); }
      else if (e.key === "e") { e.preventDefault(); layout.setView("code"); }
      else if (e.key === "t") {
        // gt → cycle to the next view (agent → code → diff → agent)
        e.preventDefault();
        const views: import("./lib/stores/layout.svelte").View[] = ["agent", "code", "diff"];
        const idx = views.indexOf(layout.view);
        layout.setView(views[(idx + 1) % views.length]);
      }
      else if (e.key === "T") {
        // gT → cycle to the previous view (agent → diff → code → agent)
        e.preventDefault();
        const views: import("./lib/stores/layout.svelte").View[] = ["agent", "code", "diff"];
        const idx = views.indexOf(layout.view);
        layout.setView(views[(idx - 1 + views.length) % views.length]);
      }
      // any other key: cancel prefix silently (no action)
      return;
    }

    switch (e.key) {
      case "j": {
        e.preventDefault();
        const list = shownWorkspaces;
        const idx  = list.findIndex(w => w.id === activeId);
        if (idx === -1) {
          // nothing active → select first
          if (list.length > 0) activeId = list[0].id;
        } else {
          // clamp at end
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
        if (activeId) openSession(activeId);
        break;
      }
      case "i": e.preventDefault(); mode.enterTerminal(); break;
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
  // Send text to the active agent pane via writeToPty.
  //
  // @mention convention (matches DragDrop.svelte):
  //   • File/path references arrive as '@'+path+' ' (the leading '@' and trailing
  //     space are already present in the string that callers pass).
  //   • Arbitrary selected text (from Editor.onSendToAgent) is sent as-is.
  //
  // Callers must format the text themselves — sendToAgent is a raw pass-through.
  // ---------------------------------------------------------------------------
  function sendToAgent(text: string) {
    if (!active?.paneId) return;
    const bytes = Array.from(new TextEncoder().encode(text));
    writeToPty(active.paneId, bytes);
  }

  // ---------------------------------------------------------------------------
  // Approval decision handler — called by ApprovalCard docked chrome.
  // Key deletion by the workspace that owns reqId, not necessarily activeId
  // (the approval queue may hold entries from non-active workspaces).
  // ---------------------------------------------------------------------------
  async function onDecision(reqId: string, decision: "allow" | "deny" | "always") {
    // Find which workspace owns this reqId
    const ownerEntry = Object.entries(approvals).find(([, req]) => req.reqId === reqId);
    const ownerWsId = ownerEntry?.[0] ?? activeId;
    if (!ownerWsId) return;
    try {
      await approve(reqId, decision);
      const { [ownerWsId]: _, ...rest } = approvals;
      approvals = rest;
    } catch (e) {
      addBlocking(ownerWsId, "Approval failed", String(e));
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
      <!-- Sidebar toggle rail — always visible, survives collapsed state -->
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
        <Sidebar workspaces={shownWorkspaces} {activeId} onSelect={onSelect} onNew={openNewSession} onReorder={handleReorder} diffStats={wsDiffStats} openIds={openIds} />
      </aside>

      <div class="divider divider-v" role="slider" aria-label="Resize sidebar"
           aria-orientation="vertical" aria-valuenow={layout.sidebarW} aria-valuemin={SIDEBAR_MIN_W} aria-valuemax={SIDEBAR_MAX_W}
           tabindex="0"
           onmousedown={startResizeSidebar}
           onkeydown={keyResizeSidebar}
           style:display={layout.collapsed["sidebar"] ? "none" : undefined}></div>

      <div class="center-column">
        <div data-zone="stage" class="stage-zone" role="region" aria-label="stage"
             ondragover={(e) => {
               if (typeof e.dataTransfer?.types?.includes === "function" &&
                   e.dataTransfer.types.includes(MIME_SESSION)) {
                 e.preventDefault();
               }
             }}
             ondrop={(e) => {
               if (typeof e.dataTransfer?.getData !== "function") return;
               const id = e.dataTransfer.getData(MIME_SESSION);
               if (!id) return;
               e.preventDefault();
               layout.setSplit(true);
               layout.setSplitId(id);
             }}
        >
          <Stage view={layout.view} split={layout.split}
                 onView={(v) => layout.setView(v)}
                 onSplit={() => layout.toggleSplit()}>
            {#snippet primary()}
              {#if active}
                <!-- The agent terminal stays mounted whenever a session is active and is hidden
                     (not unmounted) on the code and diff views, so its xterm scroll buffer survives
                     a view switch. This is the same keep-alive pattern as the home shell below.
                     Keyed by session id: switching sessions gives a fresh pane, switching views never
                     remounts it. Clicking the zone in NORMAL enters TERMINAL mode; onpointerdown fires
                     before xterm sees the event. We do NOT preventDefault, so text selection still works. -->
                {#key active.id + ":" + (termEpoch[active.id] ?? 0)}
                  <div class="terminal-zone" class:input-emphasis={emphasizeInput} data-terminal-zone role="group" aria-label="agent terminal"
                       style:display={layout.view === "agent" ? "" : "none"}
                       onanimationend={(e) => { if (e.animationName === "perch-emphasis") emphasizeInput = false; }}
                       onpointerdown={() => { if (mode.current === "normal") mode.enterTerminal(); }}>
                    {#if active && !openIds.has(active.id)}
                      <div class="pane-ended" data-testid="pane-ended">
                        <p>This session has ended.</p>
                        <button class="btn btn-primary" onclick={() => openSession(active.id)}>Reopen</button>
                      </div>
                    {/if}
                    <DragDrop paneId={active.paneId} fileDrop={true}>
                      <Terminal bind:this={primaryTerm} paneId={active.paneId} cwd={active.worktreePath} onExit={() => handleAgentExit(active.id)} />
                    </DragDrop>
                  </div>
                {/key}
                <!-- The code layout stays mounted while a session is active and is hidden on the agent
                     and diff views, so an in-progress Editor draft survives a view switch. FileTree and
                     Preview are keyed on the fs version (only while this view shows, so a hidden pane does
                     no background work) to refresh when files change. The Editor is NOT keyed on it, so a
                     background file change never discards unsaved edits; it reloads on an external change
                     only when it has none, via its reloadToken prop. Keyed by session id so a session
                     switch starts a fresh layout. -->
                {#key active.id}
                  <div class="code-layout" style:display={layout.view === "code" ? "" : "none"}>
                    {#key layout.view === "code" ? (fsVersion[active.id] ?? 0) : -1}
                      <FileTree root={active.worktreePath} onOpen={(p) => {
                        // FileTree may send '@mention:'+path for "Send to agent".
                        // Route to sendToAgent; otherwise treat as a regular file open.
                        if (p.startsWith(MENTION_PREFIX)) {
                          const path = p.slice(MENTION_PREFIX.length);
                          // Format matches DragDrop: '@'+path+' '
                          sendToAgent("@" + path + " ");
                        } else {
                          codePath = p;
                        }
                      }} />
                    {/key}
                    {#if isPreviewable(codePath)}
                      {#key layout.view === "code" ? `${fsVersion[active.id] ?? 0}:${codePath}` : codePath}
                        <Preview path={codePath ?? ""} kind={previewKind(codePath ?? "")} content={previewContent} />
                      {/key}
                    {:else}
                      <Editor path={codePath} worktree={active.worktreePath}
                              reloadToken={layout.view === "code" ? (fsVersion[active.id] ?? 0) : 0}
                              visible={layout.view === "code"}
                              onSendToAgent={sendToAgent} />
                    {/if}
                  </div>
                {/key}
                {#if layout.view === "diff"}
                  {#key fsVersion[active.id] ?? 0}
                    <DiffView worktree={active.worktreePath} onSendToAgent={sendToAgent}
                              onDiffChanged={() => { if (active) refreshDiffStat(active); }} />
                  {/key}
                {/if}
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
                    </div>
                  </div>
                </div>
              {/if}
              <!-- Home shell: lives OUTSIDE the active/home conditional so the xterm instance
                   (and its pty/scroll buffer) is never unmounted when a session is opened.
                   Hidden via inline display style when a session is active; the inline style
                   is required because jsdom only reflects inline styles in visibility assertions. -->
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
                  <!-- Keyed by session id so re-picking the split session remounts the terminal and
                       re-subscribes its pty; a Terminal subscribes to its paneId only at mount. -->
                  {#key splitWs.id}
                    <DragDrop paneId={splitWs.paneId} fileDrop={true}>
                      <Terminal paneId={splitWs.paneId} cwd={splitWs.worktreePath} />
                    </DragDrop>
                  {/key}
                {:else}
                  <div class="split-picker" data-testid="split-picker">
                    <p class="split-picker-hint">Pick a session for this pane</p>
                    <select
                      class="split-picker-select"
                      aria-label="secondary session"
                      value=""
                      onchange={(e) => {
                        const v = (e.currentTarget as HTMLSelectElement).value;
                        if (v) layout.setSplitId(v);
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
             onkeydown={keyResizeShell}></div>

        <div data-zone="shell-drawer" class="shell-drawer-zone" data-terminal-zone role="group" aria-label="shell drawer"
             onpointerdown={() => { if (mode.current === "normal") mode.enterTerminal(); }}
             style:height="{layout.shellH}px"
             style:display={layout.collapsed["shell"] ? "none" : undefined}>
          {#if active}
            {#key active.id}
              <ShellDrawer paneId="shell-{active.id}" cwd={active.worktreePath}
                collapsed={layout.collapsed["shell"] ?? false}
                onToggleCollapse={() => layout.setCollapsed("shell", !layout.collapsed["shell"])} />
            {/key}
          {/if}
        </div>
        <div data-zone="status-line" class="status-line">
          <span class="status-mode">{mode.current.toUpperCase()}</span>
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

    {#if active && approvals[active.id]}
      <div data-zone="approval-dock" class="approval-dock">
        <ApprovalCard
          req={approvals[active.id]}
          queue={approvalQueue}
          caps={active.caps}
          {onDecision}
          onApproveAll={() => decideAll("allow")}
          onDenyAll={() => decideAll("deny")}
        />
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
        />
      </div>
    {/if}

    <NewSessionDialog
      open={newSessionOpen}
      {repos}
      loadBranches={(repo) => branches(repo)}
      onCreate={handleCreate}
      onClose={() => { newSessionOpen = false; newSessionInitialAgent = null; }}
      initialAgent={newSessionInitialAgent}
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
      <div class="modal-overlay" role="presentation">
        <div class="resume-preview" data-testid="resume-preview" role="dialog" aria-modal="true" aria-label="Resume session">
          <h2 class="resume-preview-title">Resume: {previewWs.title}</h2>
          <dl class="resume-preview-meta">
            <dt>Branch</dt><dd>{previewWs.branch}</dd>
            <dt>Agent</dt><dd>{previewWs.agent}</dd>
            <dt>Last active</dt><dd>{previewWs.lastActive ? new Date(previewWs.lastActive).toLocaleString() : "—"}</dd>
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
              // Cleanup may have removed sessions — prune their open-set entries
              // and notifications so nothing dangles for a gone workspace.
              const freshIds = new Set(fresh.map(w => w.id));
              for (const id of [...openIds]) {
                if (!freshIds.has(id)) { openIds.delete(id); dropForWorkspace(id); }
              }
              workspaces = fresh;
            } catch { /* non-fatal */ }
          }}
          onOpen={(id) => { cleanupOpen = false; onSelect(id); }}
        />
      </div>
    {/if}

    <HelpDialog open={helpOpen} onClose={() => { helpOpen = false; }} />

    <SettingsPanel open={settingsOpen} onClose={() => { settingsOpen = false; }} />

    {#if pendingRemovals.length > 0}
      <div class="undo-toast-stack" aria-live="polite">
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
  .center-column    { display: flex; flex-direction: column; flex: 1; min-width: 0; }
  .stage-zone       { flex: 1; min-height: 0; display: flex; flex-direction: column;
                      transition: outline-color var(--perch-dur) var(--perch-ease); }
  .code-layout      { display: flex; flex-direction: row; flex: 1; min-height: 0; min-width: 0; }
  .terminal-zone    { position: relative; display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  /* In-pane "session ended" overlay — covers the dead xterm on the agent view
     only (it lives inside the terminal-zone, which is hidden on code/diff).
     Semi-transparent backdrop keeps the pane legible in every theme. */
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
  /* Transient ring pulse drawing the eye when the active agent wants input. */
  .terminal-zone.input-emphasis { animation: perch-emphasis var(--perch-dur-pop) var(--perch-ease); }
  @media (prefers-reduced-motion: reduce) {
    .terminal-zone.input-emphasis { animation: none; }
  }
  .shell-drawer-zone { flex-shrink: 0; overflow: hidden; border-top: 1px solid var(--perch-border);
                       transition: outline-color var(--perch-dur) var(--perch-ease); }
  /* Active-zone accent ring (you-are-here cue, not a focus indicator).
     Uses outline (not inset box-shadow) so it paints over opaque child panes and
     is not clipped by the zones' overflow:hidden; negative offset draws it inside. */
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
  /* No background here: the hub owns its own (glass) surface. An opaque dock bg
     would sit behind the hub's backdrop-filter and defeat the frost. */
  /* Status line — spans the full bottom of the center column; always in DOM */
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
  .status-sep        { color: var(--perch-border); }
  .status-session    { color: var(--perch-text); font-weight: 500; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status-branch     { font-family: var(--perch-font-mono); font-size: var(--perch-fs-caption); max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status-state      { color: var(--perch-text-dim); }
  .status-diffstat   { display: flex; gap: var(--perch-sp-1);
                       font-family: var(--perch-font-mono); font-size: var(--perch-fs-caption); }
  .status-diff-added   { color: var(--perch-ok); }
  .status-diff-removed { color: var(--perch-err); }
  /* Goal-gradient "files to review" pill; shrinks as the user stages. */
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
  /* When sidebar is collapsed the rail keeps its border but the aside is width:0 */
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

  /* Home view: vertical split — welcome card above, persistent shell below */
  .home-view { display: flex; flex-direction: column; align-items: stretch; justify-content: flex-start; width: 100%; height: 100%; overflow: hidden; }
  .home-welcome { flex: 1; display: flex; align-items: center; justify-content: center; overflow: hidden; }
  .home-shell-zone { flex: none; height: 220px; border-top: 1px solid var(--perch-border); overflow: hidden; }

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

  /* Undo toast — stacked at bottom-right */
  .undo-toast-stack {
    position: fixed; bottom: var(--perch-sp-3); right: var(--perch-sp-3);
    z-index: var(--perch-z-undo-toast);
    display: flex; flex-direction: column; gap: var(--perch-sp-1);
  }
  .undo-toast {
    display: flex; align-items: center; gap: var(--perch-sp-2);
    padding: var(--perch-sp-1) var(--perch-sp-2);
    background: var(--perch-glass-bg);
    -webkit-backdrop-filter: var(--perch-glass-filter);
    backdrop-filter: var(--perch-glass-filter);
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

  /* Resume preview modal */
  .resume-preview { background: var(--perch-glass-bg); -webkit-backdrop-filter: var(--perch-glass-filter); backdrop-filter: var(--perch-glass-filter); border: 1px solid var(--perch-glass-border); border-radius: var(--perch-radius-lg); padding: var(--perch-sp-3); min-width: 320px; max-width: 480px; color: var(--perch-text); font-family: var(--perch-font-sans); }
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
