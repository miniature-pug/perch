<!-- frontend/src/lib/DragDrop.svelte -->
<script lang="ts">
  import { writeToPty } from "./wails";
  import { MIME_TEXT } from "./constants";

  let {
    paneId,
    fileDrop,
    children,
  }: { paneId: string; fileDrop: boolean; children?: import("svelte").Snippet } = $props();

  let dragActive = $state(false);

  // Shell-quote a path so an @mention survives paths containing spaces (or other
  // shell metacharacters). Wrap in single quotes and escape any embedded single
  // quote via the '\'' idiom, e.g. it's → 'it'\''s'.
  function shellQuote(p: string): string {
    return `'${p.replace(/'/g, "'\\''")}'`;
  }

  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    dragActive = false;
    if (!fileDrop || !e.dataTransfer) return;
    const files = Array.from(e.dataTransfer.files) as (File & { path?: string })[];
    if (files.length > 0) {
      // OS file drop — encode each file path as a shell-quoted @mention so a
      // path with spaces is not split into multiple tokens.
      for (const f of files) {
        const p = (f as any).path ?? f.name;
        const bytes = Array.from(new TextEncoder().encode(`@${shellQuote(p)} `));
        await writeToPty(paneId, bytes);
      }
    } else {
      // In-app text drop (behavior a/b/c) — send raw text to the pty
      const text = e.dataTransfer.getData(MIME_TEXT);
      if (text) {
        const bytes = Array.from(new TextEncoder().encode(text));
        await writeToPty(paneId, bytes);
      }
      // MIME_SESSION drops are intentionally ignored here (handled at Stage level)
    }
  }

  function prevent(e: DragEvent) { e.preventDefault(); e.stopPropagation(); }

  function handleDragEnter(e: DragEvent) { prevent(e); if (fileDrop) dragActive = true; }
  function handleDragLeave(e: DragEvent) {
    prevent(e);
    // Only deactivate when leaving the wrapper entirely
    const rt = e.relatedTarget as Node | null;
    if (!rt || !(e.currentTarget as HTMLElement).contains(rt)) dragActive = false;
  }
</script>

<div
  role="region"
  aria-label="drop zone"
  class="drop-zone"
  class:drag-active={dragActive}
  ondragover={prevent}
  ondragenter={handleDragEnter}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
>
  {#if dragActive}
    <div class="drop-overlay" aria-hidden="true">
      <span class="drop-label">Drop here</span>
    </div>
  {/if}

  {#if !fileDrop}
    <!-- OS file drop is disabled; no file-picker IPC is available.
         Show a non-interactive "Paste path" hint instead. -->
    <p class="drop-hint">Paste path to open a file</p>
  {:else if children}
    {@render children()}
  {/if}
</div>

<style>
  /* ---------- Wrapper ---------- */
  .drop-zone {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  /* ---------- Drag-active overlay ---------- */
  .drop-overlay {
    position: absolute;
    inset: 0;
    z-index: var(--perch-z-drop-overlay);
    display: flex;
    align-items: center;
    justify-content: center;
    background: color-mix(in srgb, var(--perch-accent) 15%, transparent);
    border: 2px dashed var(--perch-accent);
    border-radius: var(--perch-radius-sm);
    pointer-events: none;
  }

  .drop-label {
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    font-weight: 600;
    color: var(--perch-accent);
  }

  /* ---------- Fallback hint (non-interactive) ---------- */
  .drop-hint {
    margin: var(--perch-sp-2);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    text-align: center;
  }
</style>
