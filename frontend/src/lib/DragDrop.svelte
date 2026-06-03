<!-- frontend/src/lib/DragDrop.svelte -->
<script lang="ts">
  import { writeToPty } from "./wails";

  let {
    paneId,
    fileDrop,
    children,
  }: { paneId: string; fileDrop: boolean; children?: import("svelte").Snippet } = $props();

  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    if (!fileDrop || !e.dataTransfer) return;
    const files = Array.from(e.dataTransfer.files) as (File & { path?: string })[];
    for (const f of files) {
      const p = (f as any).path ?? f.name;
      const bytes = Array.from(new TextEncoder().encode(`@${p} `));
      await writeToPty(paneId, bytes);
    }
  }

  function prevent(e: DragEvent) { e.preventDefault(); e.stopPropagation(); }
</script>

<div role="region" aria-label="drop zone" class="drop-zone"
  ondragover={prevent} ondragleave={prevent} ondrop={handleDrop}>
  {#if !fileDrop}
    <div class="drop-fallback">
      <button onclick={() => {}}>Open file…</button>
      <button onclick={() => navigator.clipboard?.writeText(paneId).catch(() => {})}>Copy path</button>
    </div>
  {:else if children}
    {@render children()}
  {/if}
</div>
