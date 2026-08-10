<!-- frontend/src/lib/DragDrop.svelte -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import { writeToPty } from "./wails";
  import { MIME_TEXT } from "./constants";

  let {
    paneId,
    fileDrop,
    children,
  }: { paneId: string; fileDrop: boolean; children?: import("svelte").Snippet } = $props();

  let dragActive = $state(false);

  // Backstop. A drag can end without handleDrop running: a cancelled drag (Escape),
  // a drag that leaves the window, a non-file drag, or a drop that a parent handler
  // swallows (for example, Stage's `if (modalOpen) return`). In these cases the
  // overlay would stay visible forever. While a drag is active, this component
  // listens at the window for `dragend` and `drop`, and turns the overlay off. It
  // also removes the listeners when it is destroyed.
  function resetDragActive() {
    dragActive = false;
    detachBackstop();
  }

  function attachBackstop() {
    if (typeof window === "undefined") return;
    window.addEventListener("dragend", resetDragActive);
    window.addEventListener("drop", resetDragActive);
  }

  function detachBackstop() {
    if (typeof window === "undefined") return;
    window.removeEventListener("dragend", resetDragActive);
    window.removeEventListener("drop", resetDragActive);
  }

  onDestroy(detachBackstop);

  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    resetDragActive();
    if (!fileDrop || !e.dataTransfer) return;
    // This handler does not handle OS file drops. On WebKitGTK, the DOM drop
    // event's File objects carry no real path (the non-standard File.path is
    // undefined). Instead, the absolute paths arrive through Wails' native
    // OnFileDrop. lib/osFileDrop.ts routes them to this pane, matched on this drop
    // zone's data-drop-pane. This handler carries only the in-app text drop, a
    // file-tree or editor @mention drag. This drag is a custom MIME payload, not a
    // file.
    const text = e.dataTransfer.getData(MIME_TEXT);
    if (text) {
      const bytes = Array.from(new TextEncoder().encode(text));
      await writeToPty(paneId, bytes);
    }
    // This handler ignores MIME_SESSION drops on purpose. Stage handles them instead.
  }

  function prevent(e: DragEvent) { e.preventDefault(); e.stopPropagation(); }

  function handleDragEnter(e: DragEvent) {
    prevent(e);
    if (!fileDrop) return;
    dragActive = true;
    attachBackstop();
  }
  function handleDragLeave(e: DragEvent) {
    prevent(e);
    // This handler deactivates the drag only when the pointer leaves the wrapper
    // completely. Entering a child element also fires a dragleave on the parent,
    // but its relatedTarget is still inside the node.
    const rt = e.relatedTarget as Node | null;
    if (!rt || !(e.currentTarget as HTMLElement).contains(rt)) resetDragActive();
  }
</script>

<div
  role="region"
  aria-label="drop zone"
  class="drop-zone"
  class:drag-active={dragActive}
  data-drop-pane={fileDrop ? paneId : null}
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
    <!-- OS file drop is disabled. No file-picker IPC is available.
         This shows a non-interactive "Paste path" hint instead. -->
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
