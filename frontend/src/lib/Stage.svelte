<script lang="ts">
  import type { View } from "./stores/layout.svelte";

  let {
    view, split, onView, onSplit,
  }: { view: View; split: boolean; onView: (v: View) => void; onSplit: () => void } = $props();

  const segs: { id: View; label: string }[] = [
    { id: "agent", label: "Agent" },
    { id: "code",  label: "Code"  },
    { id: "diff",  label: "Diff"  },
  ];
</script>

<div class="stage">
  <header class="stage-bar">
    <nav aria-label="View">
      {#each segs as s}
        <button aria-selected={view === s.id} onclick={() => onView(s.id)}>{s.label}</button>
      {/each}
    </nav>
    <button onclick={onSplit} aria-label="Split pane">Split</button>
  </header>

  <div class="stage-content" class:split>
    <div data-pane="primary"   class="pane"><slot name="primary"   /></div>
    {#if split}
    <div data-pane="secondary" class="pane"><slot name="secondary" /></div>
    {/if}
  </div>
</div>

<style>
  .stage         { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .stage-bar     { display: flex; align-items: center; gap: 4px; padding: 0 8px;
                   background: var(--perch-surface); border-bottom: 1px solid var(--perch-border); }
  .stage-content { display: flex; flex: 1; min-height: 0; }
  .pane          { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
</style>
