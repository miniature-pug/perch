<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import ThemeProvider   from "./lib/ThemeProvider.svelte";
  import Sidebar         from "./lib/Sidebar.svelte";
  import Stage           from "./lib/Stage.svelte";
  import ShellDrawer     from "./lib/ShellDrawer.svelte";
  import Terminal        from "./lib/Terminal.svelte";
  import Editor          from "./lib/Editor.svelte";
  import FileTree        from "./lib/FileTree.svelte";
  import DiffView        from "./lib/DiffView.svelte";
  import MenuBar         from "./lib/MenuBar.svelte";
  import CommandPalette  from "./lib/CommandPalette.svelte";
  import { layout }      from "./lib/stores/layout.svelte";
  import { mode }        from "./lib/stores/mode.svelte";
  import { settings }    from "./lib/stores/settings.svelte";
  import ApprovalCard    from "./lib/ApprovalCard.svelte";
  import NotificationHub from "./lib/NotificationHub.svelte";
  import { getDnd, setDnd, addBlocking, addAmbient, addRoutine, getItems, markRead, clearRead } from "./lib/stores/notifications.svelte";
  import { listWorkspaces, openWorkspace, revealInFiles, onAgentEvent, onNotify, onFsChanged, approve } from "./lib/wails";
  import type { WorkspaceVM, ApprovalReq } from "./lib/wails";

  let workspaces = $state<WorkspaceVM[]>([]);
  let activeId   = $state<string | null>(null);
  let codePath   = $state<string | null>(null);
  let approvals  = $state<Record<string, ApprovalReq>>({});
  let fsVersion  = $state<Record<string, number>>({});

  const active = $derived(workspaces.find(w => w.id === activeId) ?? null);

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
    // placeholder — NewSessionDialog wired in 4.25.6
  }

  // ---------------------------------------------------------------------------
  // Command registry — keyed by the ids MenuBar actually emits.
  // ---------------------------------------------------------------------------
  const THEMES = ["gruvbox", "tokyo-night", "catppuccin", "dracula", "nord", "rose-pine", "one-dark", "perch-cyan", "light"];

  type Command = { id: string; group: string; label: string; keybinding?: string; run: () => void };

  const commands: Command[] = [
    // Session
    { id: "session:new",    group: "Session", label: "New session",        run: () => openNewSession() },
    // Worktree
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
    // Notifications
    { id: "notifications:dnd", group: "Notifications", label: "Toggle Do Not Disturb", run: () => setDnd(!getDnd()) },
  ];

  function runCommand(id: string) {
    const cmd = commands.find(c => c.id === id);
    if (cmd) cmd.run();
  }

  // ---------------------------------------------------------------------------
  // Keymap
  // ---------------------------------------------------------------------------
  function onKeyDown(e: KeyboardEvent) {
    if (mode.current !== "normal") return;
    switch (e.key) {
      case "1": e.preventDefault(); layout.setView("agent"); break;
      case "2": e.preventDefault(); layout.setView("code");  break;
      case "3": e.preventDefault(); layout.setView("diff");  break;
      case "\\": e.preventDefault(); layout.toggleSplit();   break;
      case "i": e.preventDefault(); mode.enterTerminal();    break;
      case ":": e.preventDefault(); mode.enterCommand();     break;
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
  function onDecision(reqId: string, decision: "allow" | "deny" | "always") {
    approve(reqId, decision);
    if (activeId) {
      const { [activeId]: _, ...rest } = approvals;
      approvals = rest;
    }
  }
</script>

<svelte:window onkeydown={onKeyDown} />

<ThemeProvider theme={settings.theme} density={settings.density}>
  <div class="app-root">
    <MenuBar onCommand={(id) => runCommand(id)} />

    <div class="main-area">
      <aside data-zone="sidebar" class="sidebar-zone" style:width="{layout.sidebarW}px">
        <Sidebar {workspaces} {activeId} onSelect={onSelect} onNew={openNewSession} />
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
                  <Terminal paneId={active.paneId} cwd={active.worktreePath} />
                {:else if layout.view === "code"}
                  {#key fsVersion[active.id] ?? 0}
                    <FileTree root={active.worktreePath} onOpen={(p) => { codePath = p; }} />
                    <Editor path={codePath} worktree={active.worktreePath} />
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
                    <Editor path={codePath} worktree={active.worktreePath} />
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

        <div data-zone="shell-drawer" class="shell-drawer-zone" style:height="{layout.shellH}px">
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

    <div data-zone="notification-hub" class="notification-hub-dock">
      <NotificationHub
        items={getItems()}
        dnd={getDnd()}
        onDismiss={(id) => markRead(id)}
        onToggleDnd={() => setDnd(!getDnd())}
        onClearRead={clearRead}
      />
    </div>
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
  .approval-dock     { position: absolute; bottom: 2rem; left: 50%; transform: translateX(-50%);
                       z-index: 100; min-width: 320px; max-width: 560px; }
  .notification-hub-dock { position: absolute; top: 2.5rem; right: 0; z-index: 90;
                            width: 320px; max-height: 60vh; overflow-y: auto;
                            border-left: 1px solid var(--perch-border);
                            background: var(--perch-bg); }
</style>
