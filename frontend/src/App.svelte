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
  import DiffView           from "./lib/DiffView.svelte";
  import MenuBar            from "./lib/MenuBar.svelte";
  import CommandPalette     from "./lib/CommandPalette.svelte";
  import NewSessionDialog   from "./lib/NewSessionDialog.svelte";
  import ConfirmDialog      from "./lib/ConfirmDialog.svelte";
  import HelpDialog         from "./lib/HelpDialog.svelte";
  import TokenMeter         from "./lib/TokenMeter.svelte";
  import DragDrop           from "./lib/DragDrop.svelte";
  import { layout }         from "./lib/stores/layout.svelte";
  import { mode }           from "./lib/stores/mode.svelte";
  import { settings }       from "./lib/stores/settings.svelte";
  import ApprovalCard       from "./lib/ApprovalCard.svelte";
  import NotificationHub    from "./lib/NotificationHub.svelte";
  import { getDnd, setDnd, addBlocking, addAmbient, addRoutine, getItems, markRead, clearRead } from "./lib/stores/notifications.svelte";
  import { listWorkspaces, createWorkspace, removeWorkspace, openWorkspace, closeWorkspace, revealInFiles, onAgentEvent, onNotify, onFsChanged, approve, branches, readFile } from "./lib/wails";
  import type { WorkspaceVM, ApprovalReq } from "./lib/wails";

  let workspaces      = $state<WorkspaceVM[]>([]);
  let activeId        = $state<string | null>(null);
  let codePath        = $state<string | null>(null);
  let previewContent  = $state<string>("");
  let approvals       = $state<Record<string, ApprovalReq>>({});
  let fsVersion  = $state<Record<string, number>>({});
  let usage      = $state<Record<string, { tokens: number; cost: number }>>({});

  // Keymap state machine helpers
  let pendingG     = $state(false);
  let pendingLeave = $state(false);
  let filtering    = $state(false);
  let filterQuery  = $state("");

  // Load file content when codePath changes to a previewable (non-image) file.
  $effect(() => {
    if (codePath && isPreviewable(codePath) && previewKind(codePath) !== "image") {
      readFile(codePath).then((c) => { previewContent = c; }).catch(() => { previewContent = ""; });
    } else {
      previewContent = "";
    }
  });

  // Svelte action: focus the node immediately on mount (avoids a11y warning from autofocus attr).
  function focusOnMount(node: HTMLElement) { node.focus(); }

  // Dialog / overlay state
  let newSessionOpen  = $state(false);
  let confirmRemove   = $state<WorkspaceVM | null>(null);
  let notifOpen       = $state(false);
  let helpOpen        = $state(false);

  const active       = $derived(workspaces.find(w => w.id === activeId) ?? null);
  const unreadCount  = $derived(getItems().filter(n => !n.read).length);

  // Filtered workspace list for Sidebar (j/k also operate on this list when filtering).
  const shownWorkspaces = $derived(
    filtering && filterQuery
      ? workspaces.filter(w => w.title.toLowerCase().includes(filterQuery.toLowerCase()))
      : workspaces
  );

  // Derived repo list for NewSessionDialog — uses distinct worktreePaths from known workspaces.
  // branches() from the wails seam resolves all repo branches from any worktree path.
  const repos = $derived([...new Set(workspaces.map(w => w.worktreePath))]);

  // Off-functions captured from wails event subscriptions (subscribed synchronously in onMount).
  let offAgentEvent: (() => void) | null = null;
  let offNotify:     (() => void) | null = null;
  let offFsChanged:  (() => void) | null = null;

  onMount(async () => {
    // Subscribe synchronously BEFORE any await so off-fns are always captured.
    offAgentEvent = onAgentEvent((ev) => {
      const ws = workspaces.find(w => w.id === ev.workspaceId);
      if (!ws) return;
      if (ev.state) ws.state = ev.state;
      if (ev.approval) approvals[ev.workspaceId] = ev.approval;
      if (ev.kind === "usage") {
        usage = { ...usage, [ev.workspaceId]: { tokens: ev.tokens ?? 0, cost: ev.cost ?? 0 } };
      }
    });

    offNotify = onNotify((n) => {
      if      (n.tier === "blocking") addBlocking(n.workspaceId, n.title, n.body);
      else if (n.tier === "ambient")  addAmbient (n.workspaceId, n.title, n.body);
      else                            addRoutine (n.workspaceId, n.title, n.body);
    });

    offFsChanged = onFsChanged((p) => {
      fsVersion[p.workspaceId] = (fsVersion[p.workspaceId] ?? 0) + 1;
    });

    await Promise.all([settings.load(), layout.restore()]);
    workspaces = await listWorkspaces();
  });

  onDestroy(() => {
    offAgentEvent?.();
    offNotify?.();
    offFsChanged?.();
  });

  async function onSelect(id: string) {
    activeId = id;
    await openWorkspace(id);
  }

  function openNewSession() {
    newSessionOpen = true;
  }

  async function handleCreate(agent: string, repo: string, branch: string, model: string) {
    const vm = await createWorkspace(agent, repo, branch, model);
    workspaces = await listWorkspaces();
    newSessionOpen = false;
    activeId = vm.id;
  }

  function requestRemove(ws: WorkspaceVM) {
    confirmRemove = ws;
  }

  async function handleConfirmRemove() {
    if (!confirmRemove) return;
    const id = confirmRemove.id;
    confirmRemove = null;
    await removeWorkspace(id);
    workspaces = await listWorkspaces();
    if (activeId === id) activeId = workspaces[0]?.id ?? null;
  }

  function handleCancelRemove() {
    confirmRemove = null;
  }

  // ---------------------------------------------------------------------------
  // Command registry — keyed by the ids MenuBar actually emits.
  // ---------------------------------------------------------------------------
  const THEMES = ["gruvbox", "tokyo-night", "catppuccin", "dracula", "nord", "rose-pine", "one-dark", "perch-cyan", "light"];

  // ---------------------------------------------------------------------------
  // Shared bulk-approval helper — mid-flight safe.
  // Snapshots entries pre-await; reads approvals fresh post-await; only removes
  // the entry at wsId if it still holds the SAME reqId we just acted on.
  // ---------------------------------------------------------------------------
  async function decideAll(decision: "allow" | "deny") {
    const entries = Object.entries(approvals);           // pre-await snapshot
    const results = await Promise.allSettled(entries.map(([, req]) => approve(req.reqId, decision)));
    const next = { ...approvals };                        // fresh read post-await
    results.forEach((res, i) => {
      const [wsId, req] = entries[i];
      if (next[wsId]?.reqId !== req.reqId) return;        // replaced mid-flight → leave survivor
      if (res.status === "fulfilled") delete next[wsId];
      else addBlocking(wsId, "Approval failed", String(res.reason));
    });
    approvals = next;
  }

  type Command = { id: string; group: string; label: string; keybinding?: string; run: () => void | Promise<void> };

  const commands: Command[] = [
    // Session
    { id: "session:new",    group: "Session", label: "New session",        run: () => openNewSession() },
    { id: "session:close",  group: "Session", label: "Close session",      run: () => { if (active) closeWorkspace(active.id); } },
    { id: "session:remove", group: "Session", label: "Remove session",     run: () => { if (active) requestRemove(active); } },
    // Worktree
    { id: "worktree:open",   group: "Worktree", label: "Open worktree",    run: () => { if (active) openWorkspace(active.id); } },
    { id: "worktree:reveal", group: "Worktree", label: "Reveal in Files",  run: () => { if (active) revealInFiles(active.worktreePath); } },
    // View
    { id: "view:agent", group: "View", label: "Agent view",  keybinding: "1", run: () => layout.setView("agent") },
    { id: "view:code",  group: "View", label: "Code view",   keybinding: "2", run: () => layout.setView("code")  },
    { id: "view:diff",  group: "View", label: "Diff view",   keybinding: "3", run: () => layout.setView("diff")  },
    { id: "view:split", group: "View", label: "Split",       keybinding: "\\", run: () => layout.toggleSplit()   },
    { id: "view:theme", group: "View", label: "Cycle theme",                  run: () => {
        const idx = THEMES.indexOf(settings.theme);
        settings.setTheme(THEMES[(idx + 1) % THEMES.length]);
      },
    },
    // Agent bulk actions
    { id: "agent:approve-all", group: "Agent", label: "Approve all pending", run: () => decideAll("allow") },
    { id: "agent:deny-all",    group: "Agent", label: "Deny all pending",    run: () => decideAll("deny")  },
    // Notifications
    { id: "notifications:open", group: "Notifications", label: "Open notifications", run: () => { notifOpen = !notifOpen; } },
    { id: "notifications:dnd",  group: "Notifications", label: "Toggle Do Not Disturb", run: () => setDnd(!getDnd()) },
    // Help
    { id: "help:shortcuts", group: "Help", label: "Keyboard shortcuts", run: () => { helpOpen = true; } },
    { id: "help:about",     group: "Help", label: "About perch",        run: () => { helpOpen = true; } },
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

    // g-prefix resolution must come first so gd/ge work correctly.
    if (pendingG) {
      pendingG = false;
      if (e.key === "d") { e.preventDefault(); layout.setView("diff"); }
      else if (e.key === "e") { e.preventDefault(); layout.setView("code"); }
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
      case "/": {
        e.preventDefault();
        filtering    = true;
        filterQuery  = "";
        break;
      }
      case "Enter": {
        e.preventDefault();
        if (activeId) openWorkspace(activeId);
        break;
      }
      case "i": e.preventDefault(); mode.enterTerminal(); break;
      case ":": e.preventDefault(); mode.enterCommand();  break;
    }
  }

  function startResizeSidebar(e: MouseEvent) {
    const startX = e.clientX, startW = layout.sidebarW;
    function onMove(mv: MouseEvent) { layout.setSidebarW(Math.max(160, startW + mv.clientX - startX)); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  function startResizeShell(e: MouseEvent) {
    const startY = e.clientY, startH = layout.shellH;
    function onMove(mv: MouseEvent) { layout.setShellH(Math.max(80, startH - (mv.clientY - startY))); }
    function onUp() { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  // ---------------------------------------------------------------------------
  // Approval decision handler — called by ApprovalCard docked chrome.
  // ---------------------------------------------------------------------------
  async function onDecision(reqId: string, decision: "allow" | "deny" | "always") {
    if (!activeId) return;
    try {
      await approve(reqId, decision);
      const { [activeId]: _, ...rest } = approvals;
      approvals = rest;
    } catch (e) {
      addBlocking(activeId, "Approval failed", String(e));
    }
  }
</script>

<svelte:window onkeydown={onKeyDown} />

<ThemeProvider theme={settings.theme} density={settings.density}>
  <div class="app-root">
    <MenuBar onCommand={(id) => runCommand(id)} {unreadCount} />

    <div class="main-area">
      <aside data-zone="sidebar" class="sidebar-zone" style:width="{layout.sidebarW}px">
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
        <Sidebar workspaces={shownWorkspaces} {activeId} onSelect={onSelect} onNew={openNewSession} />
      </aside>

      <div class="divider divider-v" role="separator" aria-label="Resize sidebar"
           onmousedown={startResizeSidebar}></div>

      <div class="center-column">
        <div data-zone="stage" class="stage-zone">
          <Stage view={layout.view} split={layout.split}
                 onView={(v) => layout.setView(v)}
                 onSplit={() => layout.toggleSplit()}>
            <div slot="primary">
              {#if active}
                {#if layout.view === "agent"}
                  <DragDrop paneId={active.paneId} fileDrop={true}>
                    <Terminal paneId={active.paneId} cwd={active.worktreePath} />
                  </DragDrop>
                {:else if layout.view === "code"}
                  {#key fsVersion[active.id] ?? 0}
                    <FileTree root={active.worktreePath} onOpen={(p) => { codePath = p; }} />
                    {#if isPreviewable(codePath)}
                      <Preview path={codePath ?? ""} kind={previewKind(codePath ?? "")} content={previewContent} />
                    {:else}
                      <Editor path={codePath} worktree={active.worktreePath} />
                    {/if}
                  {/key}
                {:else if layout.view === "diff"}
                  {#key fsVersion[active.id] ?? 0}
                    <DiffView worktree={active.worktreePath} />
                  {/key}
                {/if}
              {:else}
                <div class="empty-state">No session selected</div>
              {/if}
            </div>
            <div slot="secondary">
              {#if layout.split && active}
                {#if layout.view === "agent"}
                  <Terminal paneId={active.paneId} cwd={active.worktreePath} />
                {:else if layout.view === "code"}
                  {#key fsVersion[active.id] ?? 0}
                    <FileTree root={active.worktreePath} onOpen={(p) => { codePath = p; }} />
                    {#if isPreviewable(codePath)}
                      <Preview path={codePath ?? ""} kind={previewKind(codePath ?? "")} content={previewContent} />
                    {:else}
                      <Editor path={codePath} worktree={active.worktreePath} />
                    {/if}
                  {/key}
                {:else if layout.view === "diff"}
                  {#key fsVersion[active.id] ?? 0}
                    <DiffView worktree={active.worktreePath} />
                  {/key}
                {/if}
              {/if}
            </div>
          </Stage>
        </div>

        <div class="divider divider-h" role="separator" aria-label="Resize shell drawer"
             onmousedown={startResizeShell}></div>

        <div data-zone="shell-drawer" class="shell-drawer-zone"
             style:height="{layout.shellH}px"
             style:display={layout.collapsed["shell"] ? "none" : undefined}>
          <ShellDrawer />
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
          queue={[approvals[active.id]]}
          caps={active.caps}
          {onDecision}
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
        />
      </div>
    {/if}

    {#if active}
      <div data-zone="token-meter" class="token-meter-dock">
        <TokenMeter
          tokens={usage[active.id]?.tokens ?? 0}
          cost={usage[active.id]?.cost ?? 0}
          capsTokens={active.caps.tokens}
        />
      </div>
    {/if}

    <NewSessionDialog
      open={newSessionOpen}
      {repos}
      loadBranches={(repo) => branches(repo)}
      onCreate={handleCreate}
      onClose={() => { newSessionOpen = false; }}
    />

    <ConfirmDialog
      open={confirmRemove !== null}
      message={confirmRemove ? `Remove workspace "${confirmRemove.title}"?` : ""}
      confirmLabel="Remove"
      destructive={true}
      note="Removes this session from perch. The worktree and its files remain on disk."
      onConfirm={handleConfirmRemove}
      onCancel={handleCancelRemove}
    />

    <HelpDialog open={helpOpen} onClose={() => { helpOpen = false; }} />
  </div>
</ThemeProvider>

<style>
  .app-root         { display: flex; flex-direction: column; height: 100vh; overflow: hidden;
                      background: var(--perch-bg); color: var(--perch-text);
                      font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .main-area        { display: flex; flex: 1; min-height: 0; }
  .sidebar-zone     { flex-shrink: 0; overflow: hidden; border-right: 1px solid var(--perch-border); }
  .divider-v        { width: 4px; cursor: col-resize; background: var(--perch-border); flex-shrink: 0; }
  .divider-h        { height: 4px; cursor: row-resize; background: var(--perch-border); }
  .center-column    { display: flex; flex-direction: column; flex: 1; min-width: 0; }
  .stage-zone       { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .shell-drawer-zone { flex-shrink: 0; overflow: hidden; border-top: 1px solid var(--perch-border); }
  .filter-input      { display: block; width: 100%; box-sizing: border-box;
                       padding: 0.25rem 0.5rem; border: none; border-bottom: 1px solid var(--perch-border);
                       background: var(--perch-bg); color: var(--perch-text);
                       font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .filter-input:focus { outline: 1px solid var(--perch-accent); }
  .approval-dock     { position: absolute; bottom: 2rem; left: 50%; transform: translateX(-50%);
                       z-index: 100; min-width: 320px; max-width: 560px; }
  .notification-hub-dock { position: absolute; top: 2.5rem; right: 0; z-index: 90;
                            width: 320px; max-height: 60vh; overflow-y: auto;
                            border-left: 1px solid var(--perch-border);
                            background: var(--perch-bg); }
  .token-meter-dock      { position: absolute; bottom: 0; right: 0; z-index: 80;
                            padding: 0.25rem 0.5rem; }
</style>
