<!-- frontend/src/lib/Sidebar.svelte -->
<script lang="ts">
  import type { WorkspaceVM } from "./wails";
  import { MIME_SESSION, worktreeColor, formatRelativeAge } from "./constants";
  import { countUp, focusOnMount } from "./actions";

  let {
    workspaces, activeId, onSelect, onNew, onReorder,
    diffStats = {}, openIds, ackedInputIds, ackedDoneIds, onRename, onEditStart, requestRemove,
  }: {
    workspaces: WorkspaceVM[];
    activeId: string | null;
    onSelect: (id: string) => void;
    onNew: () => void;
    onReorder?: (draggedId: string, targetId: string) => void;
    diffStats?: Record<string, { added: number; removed: number; files?: number }>;
    // Ids of sessions that have a live pty now, in this app run. Rows not in
    // this set are "closed": the record stays, but the pty is gone. These
    // rows get a subtle dim cue.
    openIds?: Set<string>;
    // Ids whose current awaiting-input question the user has already seen: the
    // session was active with the agent pane visible. Their "asking you a
    // question" attention signal is suppressed, so a seen question does not
    // keep signalling. A fresh question in App removes the id, which raises the
    // signal again.
    ackedInputIds?: Set<string>;
    // Ids whose current done or errored state the user has already seen: the
    // user opened the session while it was finished or failed. This suppresses
    // only the row-level attention signal for those rows; the persistent ✓/✗
    // status icon and word still show. So a finished session stops signalling
    // once the user looks at it, while its status stays visible. A fresh done
    // or errored event in App removes the id, so a new finish signals again.
    ackedDoneIds?: Set<string>;
    // Inline rename: onRename commits a new title. onEditStart lets the parent
    // dismiss any transient overlay, for example a resume preview, when
    // editing begins.
    onRename?: (id: string, title: string) => void;
    onEditStart?: () => void;
    // Optional: request removal of a session by id, through a hover × or a
    // right-click Remove. The parent (App) wires this in a later phase. The
    // remove controls render only when the callback is provided, so nothing
    // stays unwired.
    requestRemove?: (id: string) => void;
  } = $props();

  // The state to render for a row. When a session is awaiting-input, but the
  // user has already acknowledged that question (id in ackedInputIds), the
  // attention signal has done its job. The row then renders as neutral "idle":
  // no question signal, no pulse, no "asking you" label. Every other state
  // renders as-is. awaiting-approval is deliberately not suppressible here; it
  // must persist until the user decides.
  function displayState(ws: WorkspaceVM): WorkspaceVM["state"] {
    if (ws.state === "awaiting-input" && ackedInputIds?.has(ws.id)) return "idle";
    return ws.state;
  }

  // Inline-rename edit state. editingId is the row now in edit mode, or null.
  // editValue seeds and holds the in-progress text.
  let editingId = $state<string | null>(null);
  let editValue = $state("");

  // The bold primary label: the user-chosen title, or the repo name when the
  // title is empty.
  function primaryLabel(ws: WorkspaceVM): string {
    return ws.title || repoName(ws.repoPath);
  }

  // Enter inline-rename mode for ws. This core function does not depend on the
  // triggering event; the double-click gesture and the right-click menu's
  // Rename item both call it.
  function beginEdit(ws: WorkspaceVM) {
    onEditStart?.();
    editingId = ws.id;
    editValue = primaryLabel(ws);
  }

  // Enter rename from a double-click on the title span.
  function startEdit(e: Event, ws: WorkspaceVM) {
    e.stopPropagation();
    beginEdit(ws);
  }

  // Commit the in-progress rename on Enter or blur, only when the trimmed
  // value is non-empty and actually changed. This always leaves edit mode.
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
    exited:              { icon: "⏻", label: "exited" },
  } as const;

  // Right-click row menu (Rename and Remove). Mirrors the FileTree
  // context-menu pattern: a solid, fixed-position card anchored at the
  // cursor. It closes on an outside click (svelte:window), on Escape, or
  // after an action.
  let rowMenu = $state<{ ws: WorkspaceVM; x: number; y: number } | null>(null);

  const ROW_MENU_APPROX_W = 160;
  const ROW_MENU_APPROX_H = 88;
  function openRowMenu(e: MouseEvent, ws: WorkspaceVM) {
    e.preventDefault();
    e.stopPropagation();
    const x = Math.min(e.clientX, window.innerWidth  - ROW_MENU_APPROX_W);
    const y = Math.min(e.clientY, window.innerHeight - ROW_MENU_APPROX_H);
    rowMenu = { ws, x, y };
  }
  function closeRowMenu() { rowMenu = null; }
  function menuRename() { if (!rowMenu) return; const ws = rowMenu.ws; closeRowMenu(); beginEdit(ws); }
  function menuRemove() { if (!rowMenu) return; const id = rowMenu.ws.id; closeRowMenu(); requestRemove?.(id); }

  function handleRowMenuKey(e: KeyboardEvent, action: () => void) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      action();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const menuEl = (e.currentTarget as HTMLElement).closest('[role="menu"]') as HTMLElement | null;
      if (!menuEl) return;
      const items = Array.from(menuEl.querySelectorAll<HTMLElement>('[role="menuitem"]'));
      const idx = items.findIndex((el) => el === e.currentTarget);
      const next = e.key === "ArrowDown" ? items[idx + 1] : items[idx - 1];
      if (next) next.focus();
    } else if (e.key === "Escape") {
      e.preventDefault();
      closeRowMenu();
    }
  }

  // The compact status word beside the colored icon shows on every row,
  // always. Persistent, glanceable per-session status is the point of the
  // sidebar. The user reads "running", "done", or "idle" across all sessions
  // without switching. A background session's status stays put. It is not a
  // transient flash. Calm states (idle, exited) render dim, so active
  // states (running, done, awaiting, errored) still stand out. The word also
  // serves as the accessible state label.

  // Row-level attention signal: the strong urgency treatment, distinct from
  // the always-on status word above. A background, non-active session that
  // needs the user, or has just finished, signals for a glanceable look. The
  // whole row gets a color-coded left bar plus a slow, pulsing tint (see the
  // .attn CSS), far stronger than the tiny icon. The signal is suppressed in
  // three cases:
  //   - the row is active, because opening the session is itself the
  //     acknowledgement
  //   - the user already saw the awaiting-input question (displayState
  //     returns "idle" through ackedInputIds)
  //   - the user already opened a done or errored state (tracked in
  //     ackedDoneIds)
  // A finished or failed turn signals until the user looks at it, then goes
  // quiet, but its ✓/✗ status word stays. awaiting-approval is never
  // suppressible here: it is a pending action, so it signals until the user
  // decides. Returns the urgency state to color by, or null for no treatment.
  const ATTENTION_STATES = new Set<WorkspaceVM["state"]>([
    "awaiting-approval", "awaiting-input", "errored", "done",
  ]);
  function attentionState(ws: WorkspaceVM, rowState: WorkspaceVM["state"]): WorkspaceVM["state"] | null {
    if (ws.id === activeId) return null;
    if ((rowState === "done" || rowState === "errored") && ackedDoneIds?.has(ws.id)) return null;
    return ATTENTION_STATES.has(rowState) ? rowState : null;
  }

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

  // Basename of a repo path. Falls back to the raw path if it has no
  // segments.
  function repoName(p: string): string {
    const s = (p ?? "").split("/").filter(Boolean);
    return s.length ? s[s.length - 1] : (p || "");
  }

</script>

<svelte:window onclick={closeRowMenu} />

<nav aria-label="sessions" class="sidebar">
  <ul class="workspace-list">
    {#each workspaces as ws, i (ws.id)}
      {@const rowState = displayState(ws)}
      {@const st = STATUS[rowState as keyof typeof STATUS] ?? { icon: "·", label: rowState }}
      {@const ds = diffStats[ws.id]}
      {@const closed = openIds ? !openIds.has(ws.id) : false}
      {@const attn = attentionState(ws, rowState)}
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
        <button class="workspace-row{attn ? ` attn attn-${attn}` : ''}"
          class:closed={closed}
          class:has-remove={requestRemove != null}
          aria-current={ws.id === activeId ? "page" : undefined}
          onclick={() => onSelect(ws.id)}
          oncontextmenu={(e) => openRowMenu(e, ws)}
          aria-label={`${primaryLabel(ws)} ${repoName(ws.repoPath)} ${ws.branch}`}
          title={closed ? "Click to open" : undefined}
          style:--row-color={worktreeColor(ws.id, i)}
        >
          <!-- Line 1: status icon plus bold primary name, with room to read it
               before the ellipsis -->
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
              <!-- A double-click on the title is a mouse-gesture shortcut for
                   inline rename. A right-click anywhere on the row opens the
                   Rename/Remove menu, handled on the row button. The row
                   button remains the accessible primary control, so this
                   span needs no ARIA role. -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <span
                class="workspace-title"
                title={primaryLabel(ws)}
                ondblclick={(e) => startEdit(e, ws)}
              >{primaryLabel(ws)}</span>
            {/if}
            <span class="status-text st-{rowState}">{st.label}</span>
          </span>

          <!-- Line 2: dim meta: repo, branch, agent, age, diffstat -->
          <span class="workspace-row-meta">
            <span class="workspace-repo dim" title={repoName(ws.repoPath)}>{repoName(ws.repoPath)}</span>
            <span class="workspace-branch dim" title={ws.branch}>{ws.branch}</span>
            <span class="workspace-agent dim" title={ws.agent}>{ws.agent}</span>
            <span class="workspace-age dim">{formatRelativeAge(ws.lastActive)}</span>
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
        </button>
        {#if requestRemove}
          <button
            class="row-remove"
            type="button"
            draggable="false"
            data-testid="row-remove-{ws.id}"
            aria-label={`Remove ${primaryLabel(ws)}`}
            title="Remove session"
            onclick={(e) => { e.stopPropagation(); requestRemove?.(ws.id); }}
            onpointerdown={(e) => e.stopPropagation()}
          >✕</button>
        {/if}
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

{#if rowMenu}
  <ul role="menu" class="context-menu" aria-label="Session actions"
      style="position:fixed;left:{rowMenu.x}px;top:{rowMenu.y}px">
    <li role="menuitem" tabindex="0" use:focusOnMount
      onclick={(e) => { e.stopPropagation(); menuRename(); }}
      onkeydown={(e) => handleRowMenuKey(e, menuRename)}>Rename</li>
    {#if requestRemove}
      <li role="menuitem" tabindex="0"
        onclick={(e) => { e.stopPropagation(); menuRemove(); }}
        onkeydown={(e) => handleRowMenuKey(e, menuRemove)}>Remove</li>
    {/if}
  </ul>
{/if}

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
    position: relative; /* anchor for the absolutely-positioned remove (×) control */
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
  /* Two lines: bold name on top, dim meta below. The column layout gives the
     name the full row width, so it stays readable before the ellipsis. A
     single flex row previously truncated it until it was unreadable. */
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

  /* Reserves a right gutter for the hover-revealed remove (×) control, so the
     status word never sits under it. This gutter is present only once
     requestRemove is wired. */
  .workspace-row.has-remove {
    padding-right: calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5 + 20px);
  }

  .workspace-row:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
    transform: translateY(var(--perch-hover-lift));
    box-shadow: var(--perch-shadow-toast);
  }

  .workspace-row[aria-current="page"] {
    background: color-mix(in srgb, var(--perch-accent) 16%, transparent);
  }

  /* Closed session: no live pty. Dim the row to signal it is dormant. A click
     reopens it, through the resume-preview flow. The active row is never
     dimmed, even if it is momentarily flagged closed. */
  .workspace-row.closed:not([aria-current="page"]) {
    color: var(--perch-text-dim);
    opacity: 0.7;
  }

  .workspace-row:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* ── Row-level attention signal ───────────────────────────────── */
  /* A background, non-active session that needs the user, or has just
     finished, signals for a glanceable look: a color-coded left bar plus a
     soft, slow pulsing tint over the whole row, not just the tiny status
     icon. --attn-color drives the bar, tint, and glow; each urgency level
     sets it below. This is painted as a non-interactive ::before, so it
     never disturbs layout, click targets, or the row's own hover shadow. The
     active row never gets .attn, because attentionState() returns null for
     it, so viewing a session clears its signal. */
  .workspace-row.attn                   { position: relative; isolation: isolate; --attn-color: var(--perch-accent); }
  .workspace-row.attn-awaiting-approval { --attn-color: var(--perch-warn); }  /* blocking: amber */
  .workspace-row.attn-awaiting-input    { --attn-color: var(--perch-info); }  /* a question: info/accent */
  .workspace-row.attn-errored           { --attn-color: var(--perch-err); }   /* failed: danger red */
  .workspace-row.attn-done              { --attn-color: var(--perch-ok); }    /* finished: calm green */

  .workspace-row.attn::before {
    content: "";
    position: absolute;
    /* Fills only the padding box (inset:0), not the 3px border, so the
       per-worktree --row-color left edge stays visible. The urgency bar sits
       just inside it. */
    inset: 0;
    /* Paints behind the row's text and icon, but above the row's own
       background and border. The row is isolated (isolation:isolate above),
       so a negative z-index puts the tint and inset glow under the in-flow
       content. It can no longer wash over the text and icon and lower
       contrast; the cockpit holds WCAG AA across all 9 themes. */
    z-index: -1;
    pointer-events: none;
    border-left: 3px solid var(--attn-color);
    background: color-mix(in srgb, var(--attn-color) 10%, transparent);
    box-shadow: inset 0 0 10px -2px color-mix(in srgb, var(--attn-color) 45%, transparent);
    animation: perch-attn-row var(--perch-dur-attn-row) ease-in-out infinite;
  }

  /* "done" is informational, not blocking, so it gets a calmer treatment: a
     low, steady tint with a single gentle breath on appear. This animation
     is finite, not the continuous pulse of the awaiting or errored rows, and
     then it settles. */
  .workspace-row.attn-done::before {
    background: color-mix(in srgb, var(--attn-color) 8%, transparent);
    box-shadow: inset 0 0 8px -3px color-mix(in srgb, var(--attn-color) 28%, transparent);
    animation: perch-attn-row var(--perch-dur-attn-row) ease-in-out 2;
  }

  @keyframes perch-attn-row {
    0%, 100% {
      background: color-mix(in srgb, var(--attn-color) 7%, transparent);
      box-shadow: inset 0 0 8px -3px color-mix(in srgb, var(--attn-color) 28%, transparent);
    }
    50% {
      background: color-mix(in srgb, var(--attn-color) 16%, transparent);
      box-shadow: inset 0 0 12px -1px color-mix(in srgb, var(--attn-color) 58%, transparent);
    }
  }

  /* ── Row line 1: status icon plus bold name ───────────────────── */
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

  /* ── Status icon: colored per state ───────────────────────────── */
  .status-icon {
    font-size: var(--perch-fs-caption);
    flex-shrink: 0;
    /* inline-block, so the running spinner's rotate transform applies;
       transforms are ignored on non-replaced inline elements. The width
       keeps the glyph boxed. */
    display: inline-block;
    width: 16px;
    text-align: center;
    color: var(--perch-text-dim); /* default, idle */
  }

  /* "running" spins its ◐ slowly and forever while the agent works, so a live
     session reads as alive at a glance; a static icon cannot be told apart
     from a frozen one. This is the only motion on a running row, since
     running is not an attention state, so it never competes with the
     attention signal's pulse. */
  .status-running {
    color: var(--perch-ok);
    animation: perch-spin var(--perch-dur-spin) linear infinite;
  }
  @keyframes perch-spin { to { transform: rotate(360deg); } }

  .status-idle {
    color: var(--perch-text-dim);
  }

  /* An approval cannot be dismissed until the user decides, so its pulse runs
     a few cycles to catch the eye, then settles to a steady, full-opacity
     colored dot instead of signalling forever. The warn color, the ⚠ glyph,
     and the "needs you" word persist. */
  .status-awaiting-approval {
    color: var(--perch-warn);
    animation: perch-attn-pulse var(--perch-dur-attn-approval) ease-in-out 6;
  }

  .status-awaiting-input {
    color: var(--perch-info);
    animation: perch-attn-pulse var(--perch-dur-attn-input) ease-in-out infinite;
  }

  /* "done" gets the accent color, distinct from running's ok-green, so a
     finished session that wants review reads apart from one still working.
     It also ties to the accent-colored review pill. */
  .status-done {
    color: var(--perch-accent);
    animation: perch-settle-pop var(--perch-dur-pop) var(--perch-ease);
  }

  .status-errored {
    color: var(--perch-err);
  }

  /* "exited" is a neutral terminal state, either a graceful /exit or a crash
     the user must reopen, not a red error. It reads dim, like idle, so a
     clean exit never looks alarming. The distinct ⏻ glyph and "exited" word
     carry the meaning. */
  .status-exited {
    color: var(--perch-text-dim);
  }

  @keyframes perch-attn-pulse { 0%, 100% { opacity: 1; } 50% { opacity: var(--perch-attn-opacity-min); } }
  @media (prefers-reduced-motion: reduce) {
    .status-awaiting-approval, .status-awaiting-input { animation: none; }
    .status-icon.status-done { animation: none; }
    /* No spin: a running row falls back to a static ◐, still colored ok-green. */
    .status-icon.status-running { animation: none; }
    .workspace-row { transition: none; }
    .workspace-row:hover { transform: none; }
    .row-remove { transition: none; }
    /* No pulsing: the row-level signal falls back to a static colored bar,
       steady tint, and glow, the ::before's non-animated declarations. */
    .workspace-row.attn::before,
    .workspace-row.attn-done::before { animation: none; }
  }

  /* ── Compact visible status word ─────────────────────────────── */
  /* Shown on every row, so each session's status is readable at a glance
     without switching. It is colored to match the state, so the word
     reinforces the icon, and it is the accessible state label. It sits at
     the right of line 1, left of the reserved remove-(×) gutter. */
  .status-text {
    flex-shrink: 0;
    font-size: var(--perch-fs-caption);
    font-weight: 500;
    white-space: nowrap;
  }
  .st-running           { color: var(--perch-ok); }
  .st-idle              { color: var(--perch-text-dim); }
  .st-awaiting-approval { color: var(--perch-warn); }
  .st-awaiting-input    { color: var(--perch-info); }
  .st-done              { color: var(--perch-accent); }
  .st-errored           { color: var(--perch-err); }
  .st-exited            { color: var(--perch-text-dim); }

  /* ── Session title ────────────────────────────────────────────── */
  .workspace-title {
    flex: 1;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  /* ── Inline rename input: replaces the title span in edit mode ───── */
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

  /* ── Repo name: dim secondary context ─────────────────────────── */
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

  /* ── Branch: mono dim caption ──────────────────────────────────── */
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

  /* ── Row remove (×): revealed on hover or focus ───────────────── */
  /* A sibling of the row button inside the <li>. It is never nested, because
     a button inside a button is invalid. It is revealed on row hover, or
     when anything in the row is focused, so mouse and keyboard both reach
     it. */
  .row-remove {
    position: absolute;
    top: 50%;
    right: calc(var(--perch-sp-1) * var(--perch-density-scale));
    transform: translateY(-50%);
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    background: transparent;
    border: none;
    border-radius: var(--perch-radius-sm);
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    line-height: 1;
    cursor: pointer;
    opacity: 0;
    transition: opacity var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease);
  }
  .workspace-list > li:hover .row-remove,
  .workspace-list > li:focus-within .row-remove {
    opacity: 1;
  }
  .row-remove:hover {
    background: color-mix(in srgb, var(--perch-err) 14%, transparent);
    color: var(--perch-err);
  }
  .row-remove:focus-visible {
    opacity: 1;
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* ── Row context menu (Rename, Remove) ─────────────────────────── */
  /* Solid, never glass. This menu can overlap the agent pane's terminal,
     where WebKitGTK paints backdrop-filter surfaces transparent over the
     composited terminal subtree (mirrors the fix in FileTree and
     ApprovalCard). */
  .context-menu {
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    min-width: 160px;
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-float);
    z-index: var(--perch-z-context-menu);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }
  .context-menu [role="menuitem"] {
    display: flex;
    align-items: center;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) var(--perch-sp-2);
    color: var(--perch-text);
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
  }
  .context-menu [role="menuitem"]:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }
  .context-menu [role="menuitem"]:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
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
