<!-- frontend/src/lib/Sidebar.svelte -->
<script lang="ts">
  import type { WorkspaceVM } from "./wails";

  const MIME_SESSION = "application/x-perch-session";

  let {
    workspaces, activeId, onSelect, onNew, onReorder,
  }: {
    workspaces: WorkspaceVM[];
    activeId: string | null;
    onSelect: (id: string) => void;
    onNew: () => void;
    onReorder?: (draggedId: string, targetId: string) => void;
  } = $props();

  const STATUS = {
    running:             { icon: "◐", label: "running" },
    idle:                { icon: "◯", label: "idle" },
    "awaiting-approval": { icon: "⚠", label: "needs you" },
    done:                { icon: "✓", label: "done" },
    errored:             { icon: "✗", label: "error" },
  } as const;

  let dragOverId = $state<string | null>(null);

  function handleSessionDragStart(e: DragEvent, id: string) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData(MIME_SESSION, id);
    e.dataTransfer.effectAllowed = "move";
  }

  function handleSessionDragOver(e: DragEvent, id: string) {
    e.preventDefault();
    dragOverId = id;
  }

  function handleSessionDragLeave() {
    dragOverId = null;
  }

  function handleSessionDrop(e: DragEvent, targetId: string) {
    e.preventDefault();
    dragOverId = null;
    if (!e.dataTransfer) return;
    const draggedId = e.dataTransfer.getData(MIME_SESSION);
    if (!draggedId || draggedId === targetId) return;
    onReorder?.(draggedId, targetId);
  }
</script>

<nav aria-label="sessions" class="sidebar">
  <ul class="workspace-list">
    {#each workspaces as ws (ws.id)}
      {@const st = STATUS[ws.state as keyof typeof STATUS] ?? { icon: "?", label: ws.state }}
      <li
        class:active={ws.id === activeId}
        class:drag-over={dragOverId === ws.id}
        draggable="true"
        ondragstart={(e) => handleSessionDragStart(e, ws.id)}
        ondragover={(e) => handleSessionDragOver(e, ws.id)}
        ondragleave={handleSessionDragLeave}
        ondrop={(e) => handleSessionDrop(e, ws.id)}
      >
        <button class="workspace-row"
          aria-current={ws.id === activeId ? "page" : undefined}
          onclick={() => onSelect(ws.id)}
          aria-label={ws.title}
        >
          <span class="status-icon status-{ws.state}" aria-hidden="true">{st.icon}</span>
          <span class="workspace-title">{ws.title}</span>
          <span class="workspace-branch dim">{ws.branch}</span>
          <span class="status-label">{st.label}</span>
        </button>
      </li>
    {/each}
  </ul>
  <button class="new-session-cta" onclick={onNew} aria-label="New session">+ New session</button>
</nav>

<style>
  /* ── Sidebar container ────────────────────────────────────────── */
  .sidebar {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    background: var(--perch-bg-elev);
    border-right: 1px solid var(--perch-border);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    color: var(--perch-text);
    overflow: hidden;
  }

  /* ── Workspace list ───────────────────────────────────────────── */
  .workspace-list {
    list-style: none;
    margin: 0;
    padding: 0;
    flex: 1;
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }

  .workspace-list::-webkit-scrollbar {
    width: 6px;
  }

  .workspace-list::-webkit-scrollbar-track {
    background: transparent;
  }

  .workspace-list::-webkit-scrollbar-thumb {
    background: var(--perch-border);
    border-radius: 3px;
  }

  .workspace-list::-webkit-scrollbar-thumb:hover {
    background: var(--perch-text-dim);
  }

  .workspace-list > li {
    display: block;
    border-bottom: 1px solid var(--perch-border-strong);
    transition: border-color 100ms var(--perch-ease);
  }

  .workspace-list > li:last-child {
    border-bottom: none;
  }

  .workspace-list > li.drag-over {
    border-top: 2px solid var(--perch-accent);
  }

  /* ── Session row button ───────────────────────────────────────── */
  .workspace-row {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    width: 100%;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale))
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    background: transparent;
    border: none;
    color: var(--perch-text);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    text-align: left;
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
  }

  .workspace-row:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }

  .workspace-row[aria-current="page"] {
    background: color-mix(in srgb, var(--perch-accent) 16%, transparent);
  }

  .workspace-row:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* ── Status icon — colored per state ─────────────────────────── */
  .status-icon {
    font-size: 12px;
    flex-shrink: 0;
    width: 16px;
    text-align: center;
    color: var(--perch-text-dim); /* default / idle */
  }

  .status-running {
    color: var(--perch-ok);
  }

  .status-idle {
    color: var(--perch-text-dim);
  }

  .status-awaiting-approval {
    color: var(--perch-accent);
  }

  .status-done {
    color: var(--perch-text-dim);
  }

  .status-errored {
    color: var(--perch-err);
  }

  /* ── Session title ────────────────────────────────────────────── */
  .workspace-title {
    flex: 1;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  /* ── Branch — mono dim caption ────────────────────────────────── */
  .workspace-branch {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex-shrink: 0;
    max-width: 60px;
  }

  /* ── Status label ─────────────────────────────────────────────── */
  .status-label {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    flex-shrink: 0;
    margin-left: auto;
  }

  /* ── New session CTA ──────────────────────────────────────────── */
  .new-session-cta {
    display: flex;
    align-items: center;
    width: 100%;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale))
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    background: transparent;
    border: none;
    border-top: 1px solid var(--perch-border);
    color: var(--perch-accent);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    text-align: left;
    flex-shrink: 0;
    transition: background var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease);
  }

  .new-session-cta:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }

  .new-session-cta:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }
</style>
