<!-- frontend/src/lib/DiffView.svelte -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { diffStat, hunks as fetchHunks, stageHunk, discardHunk, unstageHunk, type FileDiff, type Hunk } from "./wails";
  import { MIME_TEXT, UNDO_REMOVE_DELAY_MS } from "./constants";
  import { addBlocking } from "./stores/notifications.svelte";

  // Torn-down guard. A stage or discard promise can resolve after the
  // component is destroyed. The promise must not then write into freed
  // reactive state.
  let mounted = true;
  onDestroy(() => { mounted = false; });

  // Delay before the "Loading…" placeholder appears. A fast diffStat call
  // never flashes the placeholder (F5). This value stays local. It is a
  // visual tuning value, not a cross-boundary limit.
  const DIFF_LOADING_DELAY_MS = 150;

  function handleHunkDragStart(e: DragEvent, h: Hunk) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData(MIME_TEXT, hunkText(h));
    e.dataTransfer.effectAllowed = "copy";
  }

  let {
    worktree,
    refresh = 0,
    visible = true,
    workspaceId = "",
    onSendToAgent,
    onDiffChanged,
  }: {
    worktree: string;
    // A monotonic signal (the session's file-system version). The parent
    // bumps it when files change on disk. The signal re-fetches the file
    // list in place, and the hunks of every expanded file, so a hunk card
    // never shows (or acts on) a stale index (F4, FEX-7). The parent does
    // not remount the whole view.
    refresh?: number;
    // False while the diff view is mounted but hidden (agent or code view).
    // The view then runs no git work on fs changes; it catches up when it
    // is shown again (FEX-20).
    visible?: boolean;
    // The owning session, so a failure notification links back to it.
    workspaceId?: string;
    onSendToAgent?: (text: string) => void;
    onDiffChanged?: () => void;
  } = $props();

  const STATUS_LABELS: Record<string, { icon: string; label: string }> = {
    M: { icon: "✎", label: "modified" },
    A: { icon: "+", label: "added" },
    D: { icon: "−", label: "deleted" },
    R: { icon: "→", label: "renamed" },
    "?": { icon: "?", label: "untracked" },
  };

  let files     = $state<FileDiff[]>([]);
  let expanded  = $state<Record<string, Hunk[]>>({});
  let loading   = $state(false);
  let hasLoaded = $state(false);
  let error     = $state(false);
  let flashFile = $state<string | null>(null);

  // Deferred-discard queue (F1). Discard is the only irreversible
  // working-tree action: git keeps no reflog entry and gives no re-apply
  // binding for it. So the code does not revert the change right away. It
  // holds the discard instead. The hunk disappears from view immediately.
  // An Undo toast shows for UNDO_REMOVE_DELAY_MS. The git revert runs only
  // after that delay passes with no Undo. Undo cancels the discard
  // completely, so the code never loses the change. This matches the
  // session-remove undo pattern in App.svelte.
  //
  // A pending discard remembers the hunk's CONTENT, not just its index. An
  // agent edit during the undo window can shift hunk indices, so the commit
  // re-reads the hunks and discards the hunk with the same content, or
  // nothing (FEX-7).
  type PendingDiscard = {
    id: string; worktree: string; file: string; hunk: Hunk;
    timer: ReturnType<typeof setTimeout>;
  };
  let pendingDiscards = $state<PendingDiscard[]>([]);
  let discardSeq = 0;
  // Files with a discard in flight. Their remaining hunk actions stay
  // frozen, so the user's own stage or unstage cannot race the revert.
  const pendingDiscardFiles = $derived(new Set(pendingDiscards.map((p) => p.file)));

  // Hunk identity by content: the changed lines and the staged flag. The
  // header's line numbers are left out on purpose, because an edit above the
  // hunk shifts them without changing the hunk itself.
  function hunkKey(h: Hunk): string {
    return (h.staged ? "S" : "U") + "\u0000" + h.lines.map((l) => l.kind + ":" + l.text).join("\u0000");
  }

  // Find the hunk in a fresh list that is the same change as `want`. An exact
  // header match wins; otherwise the content must match exactly one hunk.
  // Returns null when the change is gone or ambiguous.
  function findSameHunk(list: Hunk[], want: Hunk): Hunk | null {
    const key = hunkKey(want);
    const same = list.filter((h) => hunkKey(h) === key);
    if (same.length === 1) return same[0];
    return same.find((h) => h.header === want.header) ?? null;
  }

  // Hunks to show for a file: everything except the ones hidden behind a
  // pending discard.
  function withoutPending(wt: string, file: string, list: Hunk[]): Hunk[] {
    const hidden = pendingDiscards.filter((p) => p.worktree === wt && p.file === file);
    if (hidden.length === 0) return list;
    return list.filter((h) => !hidden.some((p) => hunkKey(p.hunk) === hunkKey(h)));
  }

  // A session switch hands this view another worktree. The previous
  // worktree's expanded hunks must not render under (or act on) a same-named
  // file in the new one (FEX-6).
  let shownWorktree: string | undefined;
  $effect(() => {
    const wt = worktree;
    if (shownWorktree !== undefined && shownWorktree !== wt) {
      expanded = {};
      flashFile = null;
    }
    shownWorktree = wt;
  });

  // Cancellation guard. If `worktree` changes before an in-flight diffStat
  // call resolves, the stale result must not overwrite the newer worktree's
  // files or loading flag. The "Loading…" placeholder appears only after a
  // delay (F5). A fast resolve is the common case, and it never flashes the
  // placeholder. The previous file list stays visible until the new list
  // arrives. While hidden, the view does no git work (FEX-20); showing it
  // runs the fetch for the current state.
  $effect(() => {
    const wt = worktree;
    refresh; // track: an fs change re-fetches the file list in place (F4)
    if (!visible) return;
    let cancelled = false;
    error = false;
    const loadingTimer = setTimeout(() => { if (!cancelled) loading = true; }, DIFF_LOADING_DELAY_MS);
    const settle = () => { clearTimeout(loadingTimer); if (!cancelled) { loading = false; hasLoaded = true; } };
    diffStat(wt)
      .then((r) => { if (!cancelled) { files = r; settle(); } })
      .catch(() => { if (!cancelled) { error = true; settle(); } });
    // Expanded hunks go stale on the same change: re-fetch them too, so the
    // Stage and Discard buttons act on current indices (FEX-7).
    for (const file of untrack(() => Object.keys(expanded))) void refreshHunks(file, wt);
    return () => { cancelled = true; clearTimeout(loadingTimer); };
  });

  // Commit any pending discard when the user navigates away from a
  // worktree, or when the component is destroyed. The user asked to
  // discard and did not press Undo, so the code honors that request, by
  // content (see commitDiscard). The cleanup fires on both a worktree
  // change and a component destroy. The code clears timers so the commit
  // never runs twice.
  $effect(() => {
    const wt = worktree;
    return () => {
      for (const p of pendingDiscards) {
        if (p.worktree !== wt) continue;
        clearTimeout(p.timer);
        void discardByContent(p);
      }
      if (mounted) pendingDiscards = pendingDiscards.filter((p) => p.worktree !== wt);
    };
  });

  async function toggleFile(f: FileDiff) {
    if (expanded[f.path]) {
      const next = { ...expanded }; delete next[f.path]; expanded = next;
    } else {
      const wt = worktree;
      const hs = await fetchHunks(wt, f.path);
      if (!mounted || wt !== worktree) return; // the session changed meanwhile (FEX-6)
      expanded = { ...expanded, [f.path]: withoutPending(wt, f.path, hs) };
    }
  }

  async function refreshFiles() {
    const wt = worktree;
    try {
      const r = await diffStat(wt);
      if (mounted && wt === worktree) files = r;
    } catch {
      // A refresh failure must not break staging. The stale counts stay as they are.
    }
  }

  // Re-fetch the hunk list for a file. A `finally` block calls this function
  // to keep the indices current. A result for another worktree, or for a
  // file the user collapsed meanwhile, is dropped.
  async function refreshHunks(file: string, wt: string = worktree) {
    try {
      const hs = await fetchHunks(wt, file);
      if (mounted && wt === worktree && expanded[file]) {
        expanded = { ...expanded, [file]: withoutPending(wt, file, hs) };
      }
    } catch {
      // A hunk refresh failure is not fatal. The file list refresh still runs.
    }
  }

  async function stage(h: Hunk) {
    const wt = worktree;
    try {
      await stageHunk(wt, h.file, h.index);
      if (mounted && wt === worktree) flashFile = h.file;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      addBlocking(workspaceId, "Stage failed", `Could not stage hunk in ${h.file}: ${msg}`);
    } finally {
      // Always re-fetch. This stops stale hunk indices from persisting
      // after a partial operation.
      await refreshHunks(h.file, wt);
      await refreshFiles();
      if (mounted && wt === worktree) onDiffChanged?.();
    }
  }

  async function unstage(h: Hunk) {
    const wt = worktree;
    try {
      await unstageHunk(wt, h.file, h.index);
      if (mounted && wt === worktree) flashFile = h.file;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      addBlocking(workspaceId, "Unstage failed", `Could not unstage hunk in ${h.file}: ${msg}`);
    } finally {
      await refreshHunks(h.file, wt);
      await refreshFiles();
      if (mounted && wt === worktree) onDiffChanged?.();
    }
  }

  // F1: schedule a discard instead of running it now. The hunk hides right
  // away, so it reads as discarded, and an Undo toast appears. The working
  // tree stays untouched until the timer fires.
  function requestDiscard(h: Hunk) {
    if (pendingDiscardFiles.has(h.file)) return; // one deferred discard per file
    const current = expanded[h.file];
    if (current) {
      expanded = { ...expanded, [h.file]: current.filter((x) => x.index !== h.index) };
    }
    const id = `${h.file}#${h.index}#${discardSeq++}`;
    const timer = setTimeout(() => { void commitDiscard(id); }, UNDO_REMOVE_DELAY_MS);
    pendingDiscards = [...pendingDiscards, { id, worktree, file: h.file, hunk: h, timer }];
  }

  function undoDiscard(id: string) {
    const p = pendingDiscards.find((x) => x.id === id);
    if (!p) return;
    clearTimeout(p.timer);
    pendingDiscards = pendingDiscards.filter((x) => x.id !== id);
    // Git reverted nothing. Re-derive the file's hunks to bring the row back.
    if (p.worktree === worktree) void refreshHunks(p.file);
  }

  // Discard the hunk with the pending discard's content, at its CURRENT
  // index. If the change is gone, or no longer matches exactly one hunk,
  // nothing is discarded and the user is told (FEX-7). The backend will
  // also verify the content; this check keeps the window small until then.
  async function discardByContent(p: PendingDiscard): Promise<void> {
    try {
      const fresh = await fetchHunks(p.worktree, p.file);
      const target = findSameHunk(fresh, p.hunk);
      if (!target) {
        addBlocking(workspaceId, "Discard skipped",
          `The change in ${p.file} was modified after you discarded it, so it was kept. Review it and discard again.`);
        return;
      }
      await discardHunk(p.worktree, p.file, target.index);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      addBlocking(workspaceId, "Discard failed", `Could not discard hunk in ${p.file}: ${msg}`);
    }
  }

  async function commitDiscard(id: string) {
    const p = pendingDiscards.find((x) => x.id === id);
    if (!p) return;
    pendingDiscards = pendingDiscards.filter((x) => x.id !== id);
    await discardByContent(p);
    if (mounted && p.worktree === worktree) {
      await refreshHunks(p.file, p.worktree);
      await refreshFiles();
      if (mounted) onDiffChanged?.();
    }
  }

  // The hunk as a unified diff: file headers, the hunk header, and every line
  // with its +, - or space prefix, so the agent can tell what was removed
  // from what was added (FEX-21).
  function hunkText(h: Hunk): string {
    const prefix = { add: "+", del: "-", ctx: " " } as const;
    return [
      `--- a/${h.file}`,
      `+++ b/${h.file}`,
      h.header,
      ...h.lines.map((l) => (prefix[l.kind] ?? " ") + l.text),
    ].join("\n") + "\n";
  }

  function sendHunk(h: Hunk) {
    onSendToAgent?.(hunkText(h));
  }
</script>

<section aria-label="diff view" class="diff-view">
  {#if loading && !hasLoaded}
    <p class="diff-empty">Loading…</p>
  {:else if error}
    <p class="diff-empty">Could not load diff</p>
  {:else if !hasLoaded}
    <!-- The initial fetch is in flight and still under the loading-delay
         threshold. Render nothing, so neither "Loading…" nor "No changes"
         flashes (F5). -->
  {:else if files.length === 0}
    <p class="diff-empty">No changes</p>
  {:else}
    <div class="diff-layout">
      <!-- File list panel -->
      <ul class="file-list scrollable">
        {#each files as f (f.path)}
          {@const st = STATUS_LABELS[f.status] ?? { icon: "?", label: f.status }}
          <li>
            <button
              class="file-row list-row"
              class:flash={flashFile === f.path}
              aria-expanded={!!expanded[f.path]}
              onclick={() => toggleFile(f)}
              onanimationend={() => { if (flashFile === f.path) flashFile = null; }}
              aria-label={f.path}
            >
              <span class="file-status-icon" aria-hidden="true">{st.icon}</span>
              <span class="file-path" title={f.path}>{f.path}</span>
              <span class="stat-label">{st.label}</span>
              <span class="file-stats">
                <span class="stat-add">+{f.added}</span>
                <span class="stat-del">-{f.removed}</span>
              </span>
            </button>
          </li>
        {/each}
      </ul>

      <!-- Hunk display panel -->
      <div class="hunk-panel scrollable">
        {#each files as f (f.path)}
          {#if expanded[f.path]}
            <!-- One heading per expanded file, so the Stage and Discard
                 buttons below it are never ambiguous (FEX-22). -->
            <h3 class="hunk-file-heading" title={f.path}>{f.path}</h3>
            {#each expanded[f.path] as h (h.index)}
              <div class="hunk" role="group" aria-label={h.header} draggable="true" ondragstart={(e) => handleHunkDragStart(e, h)}>
                <div class="hunk-header">
                  <span class="hunk-header-text">{h.header}</span>
                  <div class="hunk-actions">
                    {#if onSendToAgent}
                      <button
                        class="btn btn-send"
                        aria-label="Send hunk to agent"
                        onclick={() => sendHunk(h)}
                      >↗ send</button>
                    {/if}
                    {#if h.staged}
                      <button class="btn" onclick={() => unstage(h)} disabled={pendingDiscardFiles.has(h.file)}>Unstage</button>
                    {:else}
                      <button class="btn" onclick={() => stage(h)} disabled={pendingDiscardFiles.has(h.file)}>Stage</button>
                      <button class="btn btn-danger btn-discard" onclick={() => requestDiscard(h)} disabled={pendingDiscardFiles.has(h.file)}>Discard</button>
                    {/if}
                  </div>
                </div>
                <pre class="hunk-body">{#each h.lines as l}<span class="line line-{l.kind}">{l.text}{"\n"}</span>{/each}</pre>
              </div>
            {/each}
          {/if}
        {/each}
      </div>
    </div>
  {/if}
</section>

{#if pendingDiscards.length > 0}
  <div class="undo-toast-stack" aria-live="polite">
    {#each pendingDiscards as pending (pending.id)}
      <div class="undo-toast" role="status" data-testid="discard-undo-toast">
        <span class="undo-toast-msg">Change discarded</span>
        <button class="undo-toast-btn" onclick={() => undoDiscard(pending.id)}>Undo</button>
      </div>
    {/each}
  </div>
{/if}

<style>
  /* ---------- Layout ---------- */
  .diff-view {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    min-width: 0;
    overflow: hidden;
  }

  .diff-layout {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .diff-empty {
    color: var(--perch-text-dim);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    padding: var(--perch-sp-2);
    margin: 0;
  }

  /* ---------- Scrollable util ---------- */
  .scrollable {
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }
  .scrollable::-webkit-scrollbar { width: var(--perch-scrollbar-w); }
  .scrollable::-webkit-scrollbar-track { background: transparent; }
  .scrollable::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: var(--perch-scrollbar-radius); }
  .scrollable::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  /* ---------- File list panel ---------- */
  .file-list {
    list-style: none;
    margin: 0;
    padding: 0;
    width: 200px;
    flex-shrink: 0;
    border-right: 1px solid var(--perch-border);
    background: var(--perch-bg-elev);
  }

  .file-row {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    background: transparent;
    color: var(--perch-text);
    border: none;
    border-bottom: 1px solid var(--perch-border-strong);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    text-align: left;
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    box-sizing: border-box;
  }
  .file-row:last-child { border-bottom: none; }
  .file-row:hover { background: color-mix(in srgb, var(--perch-accent) 10%, transparent); }
  .file-row[aria-expanded="true"] { background: color-mix(in srgb, var(--perch-accent) 16%, transparent); }
  .file-row:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: -2px; }

  .file-status-icon {
    flex-shrink: 0;
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    width: 14px;
    text-align: center;
  }

  .file-path {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
  }

  .stat-label {
    flex-shrink: 0;
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    font-family: var(--perch-font-sans);
  }

  .file-stats {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-caption);
  }

  .stat-add { color: var(--perch-ok); }
  .stat-del { color: var(--perch-err); }

  /* ---------- Hunk panel ---------- */
  .hunk-panel {
    flex: 1;
    min-width: 0;
    background: var(--perch-bg);
    padding: 0;
  }

  .hunk-file-heading {
    margin: 0;
    padding: 4px var(--perch-sp-2);
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    font-weight: 600;
    color: var(--perch-text);
    background: var(--perch-bg);
    border-bottom: 1px solid var(--perch-border-strong);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hunk {
    border-bottom: 1px solid var(--perch-border);
    transition: opacity var(--perch-dur) var(--perch-ease);
  }
  .hunk[draggable="true"] { cursor: grab; }
  .hunk[draggable="true"]:active { opacity: 0.7; }

  .hunk-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--perch-sp-1);
    padding: 4px var(--perch-sp-2);
    background: var(--perch-bg-elev);
    border-bottom: 1px solid var(--perch-border);
  }

  .hunk-header-text {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    color: var(--perch-info);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hunk-actions {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
  }

  .hunk-body {
    margin: 0;
    padding: 0;
    background: var(--perch-bg);
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    line-height: var(--perch-lh-code);
    overflow-x: auto;
  }

  /* Diff line coloring */
  .line {
    display: block;
    padding: 0 var(--perch-sp-2);
    white-space: pre;
  }
  .line-add {
    background: color-mix(in srgb, var(--perch-ok) 12%, transparent);
    color: var(--perch-ok);
  }
  .line-del {
    background: color-mix(in srgb, var(--perch-err) 12%, transparent);
    color: var(--perch-err);
  }
  .line-ctx {
    color: var(--perch-text);
  }

  /* ---------- Buttons ---------- */
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 2px 8px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    transition:
      border-color var(--perch-dur) var(--perch-ease),
      color        var(--perch-dur) var(--perch-ease),
      background   var(--perch-dur) var(--perch-ease);
  }
  .btn:hover { border-color: var(--perch-accent); color: var(--perch-accent); }
  .btn:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: 2px; }
  .btn:disabled { opacity: 0.45; cursor: default; }
  .btn:disabled:hover { border-color: var(--perch-border-strong); color: var(--perch-text); background: var(--perch-bg); }

  .btn-danger {
    color: var(--perch-err);
    border-color: var(--perch-err);
  }
  .btn-danger:hover { background: color-mix(in srgb, var(--perch-err) 12%, var(--perch-bg)); }
  .btn-danger:focus-visible { outline-color: var(--perch-err); }
  .btn-danger:disabled:hover { border-color: var(--perch-err); color: var(--perch-err); background: var(--perch-bg); }

  /* F6: keep the irreversible Discard button far from the safe Stage
     button, so users do not tap Discard by mistake. A gap and a divider
     line separate the two buttons clearly. */
  .btn-discard {
    margin-left: var(--perch-sp-2);
    position: relative;
  }
  .btn-discard::before {
    content: "";
    position: absolute;
    left: calc(var(--perch-sp-1) * -1);
    top: 50%;
    transform: translateY(-50%);
    width: 1px;
    height: 60%;
    background: var(--perch-border);
  }

  .btn-send {
    color: var(--perch-accent);
    border-color: var(--perch-accent);
    font-family: var(--perch-font-mono);
  }
  .btn-send:hover { background: color-mix(in srgb, var(--perch-accent) 12%, var(--perch-bg)); }
  .btn-send:focus-visible { outline-color: var(--perch-accent); }

  /* ---------- Stage flash ---------- */
  .file-row.flash {
    animation: perch-stage-flash var(--perch-dur-flash) var(--perch-ease);
  }
  @media (prefers-reduced-motion: reduce) {
    .file-row.flash { animation: none; }
  }

  /* ---------- Discard undo toast (F1) ---------- */
  /* The background is solid, not translucent glass. This stops the toast
     from bleeding through onto the content behind it, the same fix used
     for the WebKit glass issue elsewhere. */
  .undo-toast-stack {
    position: fixed;
    bottom: var(--perch-sp-3);
    left: 50%;
    transform: translateX(-50%);
    z-index: var(--perch-z-undo-toast);
    display: flex;
    flex-direction: column;
    gap: var(--perch-sp-1);
  }
  .undo-toast {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-2);
    padding: var(--perch-sp-1) var(--perch-sp-2);
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-toast);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    color: var(--perch-text);
    min-width: 220px;
    animation: discard-toast-in var(--perch-dur) var(--perch-ease);
  }
  @keyframes discard-toast-in {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  @media (prefers-reduced-motion: reduce) {
    .undo-toast { animation: none; }
  }
  .undo-toast-msg { flex: 1; }
  .undo-toast-btn {
    padding: 3px 10px;
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border: none;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: filter var(--perch-dur) var(--perch-ease);
  }
  .undo-toast-btn:hover { filter: brightness(1.1); }
  .undo-toast-btn:focus-visible { outline: var(--perch-ring-w) solid var(--perch-accent); outline-offset: 2px; }
</style>
