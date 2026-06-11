<!-- frontend/src/lib/DiffView.svelte -->
<script lang="ts">
  import { diffStat, hunks as fetchHunks, stageHunk, discardHunk, type FileDiff, type Hunk } from "./wails";
  import { MIME_TEXT } from "./constants";

  function handleHunkDragStart(e: DragEvent, h: Hunk) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData(MIME_TEXT, hunkText(h));
    e.dataTransfer.effectAllowed = "copy";
  }

  let {
    worktree,
    onSendToAgent,
    onDiffChanged,
  }: {
    worktree: string;
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
  let error     = $state(false);
  let flashFile = $state<string | null>(null);

  // Cancellation guard: if `worktree` changes before an in-flight diffStat resolves,
  // the stale resolve must not clobber the newer worktree's files / loading flag.
  $effect(() => {
    const wt = worktree;
    let cancelled = false;
    loading = true;
    error = false;
    diffStat(wt)
      .then((r) => { if (!cancelled) { files = r; loading = false; } })
      .catch(() => { if (!cancelled) { loading = false; error = true; } });
    return () => { cancelled = true; };
  });

  async function toggleFile(f: FileDiff) {
    if (expanded[f.path]) {
      const next = { ...expanded }; delete next[f.path]; expanded = next;
    } else {
      expanded = { ...expanded, [f.path]: await fetchHunks(worktree, f.path) };
    }
  }

  async function refreshFiles() {
    try {
      files = await diffStat(worktree);
    } catch {
      // Refresh failure must not break staging — leave stale counts.
    }
  }

  async function stage(h: Hunk) {
    await stageHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
    flashFile = h.file;
    await refreshFiles();
    onDiffChanged?.();
  }

  async function discard(h: Hunk) {
    await discardHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
    await refreshFiles();
    onDiffChanged?.();
  }

  function hunkText(h: Hunk): string {
    return h.lines.map((l) => l.text).join("\n");
  }

  function sendHunk(h: Hunk) {
    onSendToAgent?.(hunkText(h));
  }
</script>

<section aria-label="diff view" class="diff-view">
  {#if loading}
    <p class="diff-empty">Loading…</p>
  {:else if error}
    <p class="diff-empty">Could not load diff</p>
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
                    <button class="btn" onclick={() => stage(h)} disabled={h.staged} title={h.staged ? "Already staged" : undefined}>Stage</button>
                    <button class="btn btn-danger" onclick={() => discard(h)} disabled={h.staged} title={h.staged ? "Already staged" : undefined}>Discard</button>
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

  .btn-danger {
    color: var(--perch-err);
    border-color: var(--perch-err);
  }
  .btn-danger:hover { background: color-mix(in srgb, var(--perch-err) 12%, var(--perch-bg)); }
  .btn-danger:focus-visible { outline-color: var(--perch-err); }

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
</style>
