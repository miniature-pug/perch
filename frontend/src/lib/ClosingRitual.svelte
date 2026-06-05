<script lang="ts">
  import { focusOnMount } from "./actions";

  let {
    lines,
    files,
    sessions,
    onDismiss,
  }: {
    lines: number;
    files: number;
    sessions: number;
    onDismiss: () => void;
  } = $props();

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onDismiss();
  }

  function handleScrimClick(e: MouseEvent) {
    if (e.target === e.currentTarget) onDismiss();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  role="dialog"
  aria-modal="true"
  aria-labelledby="ritual-heading"
  class="ritual-overlay"
  tabindex="-1"
  onkeydown={handleKey}
  onclick={handleScrimClick}
>
  <div class="ritual-card">
    <h2 id="ritual-heading" class="ritual-heading">Session complete</h2>
    <p class="ritual-subtitle">All workspaces have gone quiet.</p>

    <div class="ritual-stats">
      <div class="stat">
        <span class="stat-value">{lines}</span>
        <span class="stat-label">lines</span>
      </div>
      <div class="stat">
        <span class="stat-value">{files}</span>
        <span class="stat-label">files</span>
      </div>
      <div class="stat">
        <span class="stat-value">{sessions}</span>
        <span class="stat-label">sessions</span>
      </div>
    </div>

    <button class="btn-done" onclick={onDismiss} use:focusOnMount>Done</button>
  </div>
</div>

<style>
  .ritual-overlay {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-modal);
  }

  .ritual-card {
    background: var(--perch-glass-bg);
    -webkit-backdrop-filter: var(--perch-glass-filter);
    backdrop-filter: var(--perch-glass-filter);
    color: var(--perch-text);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-lg);
    box-shadow: var(--perch-glass-shadow);
    padding: var(--perch-sp-4, var(--perch-sp-3));
    min-width: 320px;
    max-width: 420px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--perch-sp-2);
    font-family: var(--perch-font-sans);
    animation: perch-ritual-in var(--perch-dur) var(--perch-ease);
  }

  @media (prefers-reduced-motion: reduce) {
    .ritual-card {
      animation: none;
    }
  }

  .ritual-heading {
    margin: 0;
    font-size: var(--perch-fs-h2);
    font-weight: 600;
    color: var(--perch-text);
    text-align: center;
  }

  .ritual-subtitle {
    margin: 0;
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    text-align: center;
  }

  .ritual-stats {
    display: flex;
    gap: var(--perch-sp-3);
    margin: var(--perch-sp-1) 0;
  }

  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
  }

  .stat-value {
    font-size: var(--perch-fs-h2);
    font-weight: 700;
    color: var(--perch-ok);
    line-height: 1;
  }

  .stat-label {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
  }

  .btn-done {
    margin-top: var(--perch-sp-1);
    padding: 4px 20px;
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border: 1px solid var(--perch-accent);
    border-radius: 4px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: filter var(--perch-dur) var(--perch-ease);
  }

  .btn-done:hover {
    filter: brightness(1.1);
  }

  .btn-done:active {
    filter: brightness(0.92);
  }

  .btn-done:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }
</style>
