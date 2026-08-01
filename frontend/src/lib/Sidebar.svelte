<!-- frontend/src/lib/Sidebar.svelte -->
<script lang="ts">
  import type { WorkspaceVM } from "./wails";
  import { MIME_SESSION, worktreeColor } from "./constants";
  import { countUp, focusOnMount } from "./actions";

  let {
    workspaces, activeId, onSelect, onNew, onReorder,
    diffStats = {}, openIds, ackedInputIds, onRename, onEditStart,
  }: {
    workspaces: WorkspaceVM[];
    activeId: string | null;
    onSelect: (id: string) => void;
    onNew: () => void;
    onReorder?: (draggedId: string, targetId: string) => void;
    diffStats?: Record<string, { added: number; removed: number; files?: number }>;
    // Ids of sessions that currently have a live pty this app-run. Rows NOT in
    // this set are "closed" (record kept, pty gone) and get a subtle dim cue.
    openIds?: Set<string>;
    // Ids whose current awaiting-input question the user has already seen (was
    // active + agent pane visible). Their "asking you a question" badge is
    // suppressed so a seen question does not nag forever; a fresh question in App
    // removes the id, re-raising the badge.
    ackedInputIds?: Set<string>;
    // Inline rename: onRename commits a new title; onEditStart lets the parent
    // dismiss any transient overlay (e.g. resume preview) when editing begins.
    onRename?: (id: string, title: string) => void;
    onEditStart?: () => void;
  } = $props();

  // The state to RENDER for a row. When a session is awaiting-input but the user
  // has already acknowledged that question (id in ackedInputIds), the attention
  // signal has done its job, so present it as neutral "idle" — no question badge,
  // no pulse, no "asking you" label. Every other state renders as-is. awaiting-
  // approval is deliberately NOT suppressible here (it must persist until decided).
  function displayState(ws: WorkspaceVM): WorkspaceVM["state"] {
    if (ws.state === "awaiting-input" && ackedInputIds?.has(ws.id)) return "idle";
    return ws.state;
  }

  // Inline-rename edit state. editingId is the row currently in edit mode (or
  // null); editValue seeds/holds the in-progress text.
  let editingId = $state<string | null>(null);
  let editValue = $state("");

  // The bold primary label: the user-chosen title, falling back to the repo name
  // when the title is empty.
  function primaryLabel(ws: WorkspaceVM): string {
    return ws.title || repoName(ws.repoPath);
  }

  // Enter inline-rename mode for ws (from dblclick or contextmenu).
  function startEdit(e: Event, ws: WorkspaceVM) {
    e.stopPropagation();
    onEditStart?.();
    editingId = ws.id;
    editValue = primaryLabel(ws);
  }

  // Commit the in-progress rename (Enter or blur): only when the trimmed value is
  // non-empty and actually changed. Always leaves edit mode.
  function commitEdit(ws: WorkspaceVM) {
    if (editingId !== ws.id) return;
    const trimmed = editValue.trim();
    if (trimmed && trimmed !== primaryLabel(ws)) {
      onRename?.(ws.id, trimmed);
    }
    editingId = null;
  }

  function cancelEdit() {
    editingId = null;
  }

  const STATUS = {
    running:             { icon: "◐", label: "running" },
    idle:                { icon: "◯", label: "idle" },
    "awaiting-approval": { icon: "⚠", label: "needs you" },
    "awaiting-input":    { icon: "?", label: "asking you" },
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

  // Basename of a repo path; falls back to the raw path if it has no segments.
  function repoName(p: string): string {
    const s = (p ?? "").split("/").filter(Boolean);
    return s.length ? s[s.length - 1] : (p || "");
  }

  function formatAge(isoOrEmpty: string): string {
    if (!isoOrEmpty) return "";
    const d = new Date(isoOrEmpty);
    if (isNaN(d.getTime())) return "";
    const days = Math.floor((Date.now() - d.getTime()) / 86400000);
    if (days < 1) return "today";
    if (days === 1) return "1d ago";
    return `${days}d ago`;
  }
</script>

<nav aria-label="sessions" class="sidebar">
  <ul class="workspace-list">
    {#each workspaces as ws (ws.id)}
      {@const rowState = displayState(ws)}
      {@const st = STATUS[rowState as keyof typeof STATUS] ?? { icon: "·", label: rowState }}
      {@const ds = diffStats[ws.id]}
      {@const closed = openIds ? !openIds.has(ws.id) : false}
      <li
        class:active={ws.id === activeId}
        class:drag-over={dragOverId === ws.id}
        draggable="true"
        ondragstart={(e) => handleSessionDragStart(e, ws.id)}
        ondragenter={(e) => handleSessionDragOver(e, ws.id)}
        ondragover={(e) => handleSessionDragOver(e, ws.id)}
        ondragleave={handleSessionDragLeave}
        ondrop={(e) => handleSessionDrop(e, ws.id)}
      >
        <button class="workspace-row"
          class:closed={closed}
          aria-current={ws.id === activeId ? "page" : undefined}
          onclick={() => onSelect(ws.id)}
          aria-label={`${primaryLabel(ws)} ${repoName(ws.repoPath)} ${ws.branch}`}
          title={closed ? "Click to open" : undefined}
          style:--row-color={worktreeColor(ws.id)}
        >
          <!-- Line 1: status icon + bold primary name (room to read it before ellipsis) -->
          <span class="workspace-row-primary">
            <span class="status-icon status-{rowState}" aria-hidden="true" title={st.label}>{st.icon}</span>
            {#if editingId === ws.id}
              <input
                class="workspace-title-edit"
                type="text"
                aria-label="rename session"
                bind:value={editValue}
                use:focusOnMount
                onclick={(e) => e.stopPropagation()}
                onpointerdown={(e) => e.stopPropagation()}
                onkeydown={(e) => {
                  e.stopPropagation();
                  if (e.key === "Enter") { e.preventDefault(); commitEdit(ws); }
                  else if (e.key === "Escape") { e.preventDefault(); cancelEdit(); }
                }}
                onblur={() => commitEdit(ws)}
              />
            {:else}
              <!-- Double-click / right-click on the title are mouse-gesture
                   enhancements for inline rename; the row button remains the
                   accessible primary control, so this span needs no ARIA role. -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <span
                class="workspace-title"
                title={primaryLabel(ws)}
                ondblclick={(e) => startEdit(e, ws)}
                oncontextmenu={(e) => { e.preventDefault(); startEdit(e, ws); }}
              >{primaryLabel(ws)}</span>
            {/if}
          </span>

          <!-- Line 2: dim meta — repo · branch · agent · age · diffstat -->
          <span class="workspace-row-meta">
            <span class="workspace-repo dim" title={repoName(ws.repoPath)}>{repoName(ws.repoPath)}</span>
            <span class="workspace-branch dim" title={ws.branch}>{ws.branch}</span>
            <span class="workspace-agent dim" title={ws.agent}>{ws.agent}</span>
            <span class="workspace-age dim">{formatAge(ws.lastActive)}</span>
            {#if ds && (ds.added > 0 || ds.removed > 0)}
              <span class="sidebar-diffstat" aria-label="+{ds.added} minus {ds.removed}">
                <span class="diff-added">+<span use:countUp={ds.added}></span></span>
                <span class="diff-removed">&minus;<span use:countUp={ds.removed}></span></span>
              </span>
              {#if ds.files != null && ds.files > 0}
                <span class="review-pill" aria-label="{ds.files} files to review"><span use:countUp={ds.files}></span></span>
              {/if}
            {/if}
          </span>
          <span class="status-label">{st.label}</span>
        </button>
      </li>
    {/each}
    {#if workspaces.length === 0}
      <li class="sidebar-empty-hint" data-testid="sidebar-empty-hint">
        No sessions yet
      </li>
    {/if}
  </ul>
  <button class="new-session-cta" onclick={() => onNew()} aria-label="New session">+ New session</button>
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
    width: var(--perch-scrollbar-w);
  }

  .workspace-list::-webkit-scrollbar-track {
    background: transparent;
  }

  .workspace-list::-webkit-scrollbar-thumb {
    background: var(--perch-border);
    border-radius: var(--perch-scrollbar-radius);
  }

  .workspace-list::-webkit-scrollbar-thumb:hover {
    background: var(--perch-text-dim);
  }

  .workspace-list > li {
    display: block;
    border-bottom: 1px solid var(--perch-border-strong);
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .workspace-list > li:last-child {
    border-bottom: none;
  }

  .workspace-list > li.drag-over {
    border-top: 2px solid var(--perch-accent);
  }

  /* ── Session row button ───────────────────────────────────────── */
  /* Two lines: bold name on top, dim meta below. Column layout gives the name
     the full row width so it is readable before ellipsis (single-flex-row
     previously truncated it to uselessness). */
  .workspace-row {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 2px;
    width: 100%;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale))
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5)
             calc(var(--perch-sp-1) * var(--perch-density-scale))
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5 - 3px);
    background: transparent;
    border: none;
    border-left: 3px solid var(--row-color);
    color: var(--perch-text);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    text-align: left;
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease),
                transform var(--perch-dur) var(--perch-ease),
                box-shadow var(--perch-dur) var(--perch-ease);
    user-select: none;
  }

  .workspace-row:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
    transform: translateY(var(--perch-hover-lift));
    box-shadow: var(--perch-shadow-toast);
  }

  .workspace-row[aria-current="page"] {
    background: color-mix(in srgb, var(--perch-accent) 16%, transparent);
  }

  /* Closed session: no live pty. Dim the row to signal it's dormant; clicking
     reopens it (routes through the resume-preview flow). The active row is never
     dimmed even if momentarily flagged closed. */
  .workspace-row.closed:not([aria-current="page"]) {
    color: var(--perch-text-dim);
    opacity: 0.7;
  }

  .workspace-row:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* ── Row line 1: status icon + bold name ─────────────────────── */
  .workspace-row-primary {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    min-width: 0;
    width: 100%;
  }

  /* ── Row line 2: dim meta ─────────────────────────────────────── */
  .workspace-row-meta {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    min-width: 0;
    width: 100%;
    /* Indent under the status icon so meta aligns with the name text. */
    padding-left: calc(16px + var(--perch-sp-1));
    overflow: hidden;
  }

  /* ── Status icon — colored per state ─────────────────────────── */
  .status-icon {
    font-size: var(--perch-fs-caption);
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
    color: var(--perch-warn);
    animation: perch-attn-pulse var(--perch-dur-attn-approval) ease-in-out infinite;
  }

  .status-awaiting-input {
    color: var(--perch-info);
    animation: perch-attn-pulse var(--perch-dur-attn-input) ease-in-out infinite;
  }

  .status-done {
    color: var(--perch-ok);
    animation: perch-settle-pop var(--perch-dur-pop) var(--perch-ease);
  }

  .status-errored {
    color: var(--perch-err);
  }

  @keyframes perch-attn-pulse { 0%, 100% { opacity: 1; } 50% { opacity: var(--perch-attn-opacity-min); } }
  @media (prefers-reduced-motion: reduce) {
    .status-awaiting-approval, .status-awaiting-input { animation: none; }
    .status-icon.status-done { animation: none; }
    .workspace-row { transition: none; }
    .workspace-row:hover { transform: none; }
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

  /* ── Inline rename input — replaces the title span in edit mode ─── */
  .workspace-title-edit {
    flex: 1;
    min-width: 0;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    font-weight: 500;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-accent);
    border-radius: var(--perch-radius-sm);
    padding: 0 4px;
    box-sizing: border-box;
  }

  .workspace-title-edit:focus {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 0;
  }

  /* ── Repo name — dim secondary context ────────────────────────── */
  .workspace-repo {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    flex-shrink: 1;
    min-width: 0;
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* ── Branch — mono dim caption ────────────────────────────────── */
  .workspace-branch {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex-shrink: 1;
    min-width: 0;
    max-width: 120px;
  }

  /* ── Agent name ──────────────────────────────────────────────── */
  .workspace-agent {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    flex-shrink: 0;
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* ── Last-active age ──────────────────────────────────────────── */
  .workspace-age {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    flex-shrink: 0;
  }

  /* ── Empty hint ───────────────────────────────────────────────── */
  .sidebar-empty-hint {
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale) * 2)
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    font-style: italic;
    list-style: none;
  }

  /* ── Diffstat per-row ────────────────────────────────────────── */
  .sidebar-diffstat {
    display: flex;
    gap: var(--perch-sp-1);
    font-size: var(--perch-fs-caption);
    font-family: var(--perch-font-mono);
    flex-shrink: 0;
  }
  .diff-added   { color: var(--perch-ok); }
  .diff-removed { color: var(--perch-err); }

  /* ── Review pill: goal-gradient file count ───────────────────── */
  .review-pill {
    display: inline-flex;
    align-items: center;
    padding: 0 var(--perch-sp-1);
    background: color-mix(in srgb, var(--perch-accent) 18%, transparent);
    border-radius: var(--perch-radius-sm);
    font-size: var(--perch-fs-caption);
    font-family: var(--perch-font-mono);
    color: var(--perch-accent);
    flex-shrink: 0;
  }

  /* ── Status label ─────────────────────────────────────────────── */
  /* Visually hidden, but kept in the DOM + accessibility tree. The colored,
     shaped, pulsing status icon already conveys state to sighted users (hover
     its title for the word), so the text label was redundant chrome that
     clipped on narrow rows. Screen readers still announce it and it stays in
     the row's textContent. Absolute positioning removes it from the flex row
     so it no longer consumes width. */
  .status-label {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
    border: 0;
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
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }
</style>
