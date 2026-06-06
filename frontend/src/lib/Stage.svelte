<script lang="ts">
  import type { Snippet } from "svelte";
  import type { View } from "./stores/layout.svelte";

  let {
    view, split, onView, onSplit,
    primary, secondary,
  }: {
    view: View; split: boolean;
    onView: (v: View) => void; onSplit: () => void;
    primary?: Snippet; secondary?: Snippet;
  } = $props();

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
        <button aria-pressed={view === s.id} onclick={() => onView(s.id)}>{s.label}</button>
      {/each}
    </nav>
    <button onclick={onSplit} aria-label="Split pane">Split</button>
  </header>

  <div class="stage-content" class:split>
    <div data-pane="primary"   class="pane">{@render primary?.()}</div>
    {#if split}
    <div data-pane="secondary" class="pane">{@render secondary?.()}</div>
    {/if}
  </div>
</div>

<style>
  .stage         { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .stage-bar     { display: flex; align-items: center; gap: 4px; padding: 0 8px;
                   background: var(--perch-surface); border-bottom: 1px solid var(--perch-border); }
  .stage-content       { display: flex; flex: 1; min-height: 0; }
  .stage-content.split { flex-direction: row; }
  .pane                { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; }
  .stage-content.split [data-pane="primary"]   { border-right: 1px solid var(--perch-border); }
  .stage-content.split [data-pane="secondary"] { }

  /* Toolbar buttons — match app-wide toolbar style */
  .stage-bar button {
    display: inline-flex;
    align-items: center;
    height: 22px;
    padding: 0 var(--perch-sp-1);
    background: transparent;
    color: var(--perch-text-dim);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    flex-shrink: 0;
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .stage-bar button:hover {
    color: var(--perch-text);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .stage-bar button:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* Active view segment */
  .stage-bar button[aria-pressed="true"] {
    color: var(--perch-accent);
    background: color-mix(in srgb, var(--perch-accent) 14%, transparent);
    border-color: var(--perch-accent);
  }
</style>
