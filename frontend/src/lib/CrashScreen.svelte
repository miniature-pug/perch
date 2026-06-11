<!-- frontend/src/lib/CrashScreen.svelte -->
<script lang="ts">
  let {
    error,
    onRetry,
    onReload,
  }: {
    error: unknown;
    onRetry: () => void;
    onReload: () => void;
  } = $props();

  const message = $derived(error instanceof Error ? error.message : String(error));
</script>

<div class="crash-overlay" data-testid="crash-screen" role="alert">
  <div class="crash-card">
    <h1 class="crash-heading">perch hit an unexpected error</h1>
    <p class="crash-body">
      The interface crashed, but your sessions and the work on disk are safe.
    </p>
    <pre class="crash-detail">{message}</pre>
    <div class="crash-actions">
      <button class="btn btn-primary" onclick={onRetry}>Try again</button>
      <button class="btn btn-secondary" onclick={onReload}>Reload perch</button>
    </div>
  </div>
</div>

<style>
  .crash-overlay {
    position: fixed;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--perch-bg, #282828);
    z-index: var(--perch-z-modal, 200);
  }

  .crash-card {
    background: var(--perch-surface, #32302f);
    border: 1px solid var(--perch-border, #504945);
    border-radius: var(--perch-radius-lg, 10px);
    padding: var(--perch-sp-4, 32px);
    max-width: 520px;
    width: calc(100% - var(--perch-sp-4, 32px) * 2);
    box-shadow: var(--perch-shadow-float, 0 8px 32px rgba(0, 0, 0, 0.45));
    display: flex;
    flex-direction: column;
    gap: var(--perch-sp-2, 16px);
  }

  .crash-heading {
    font-family: var(--perch-font-sans, system-ui, sans-serif);
    font-size: var(--perch-fs-h2, 20px);
    line-height: var(--perch-lh-h2, 1.35);
    color: var(--perch-text, #ebdbb2);
    margin: 0;
  }

  .crash-body {
    font-family: var(--perch-font-sans, system-ui, sans-serif);
    font-size: var(--perch-fs-body, 13px);
    color: var(--perch-text-dim, #a89984);
    margin: 0;
  }

  .crash-detail {
    font-family: var(--perch-font-mono, ui-monospace, monospace);
    font-size: var(--perch-fs-code, 14px);
    line-height: var(--perch-lh-code, 1.55);
    color: var(--perch-err, #fb4934);
    background: var(--perch-bg-elev, #1d2021);
    border: 1px solid var(--perch-border, #504945);
    border-radius: var(--perch-radius-sm, 4px);
    padding: var(--perch-sp-2, 16px);
    margin: 0;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .crash-actions {
    display: flex;
    gap: var(--perch-sp-1, 8px);
    flex-wrap: wrap;
  }

  .btn {
    font-family: var(--perch-font-sans, system-ui, sans-serif);
    font-size: var(--perch-fs-body, 13px);
    padding: calc(var(--perch-sp-1, 8px) * 0.75) var(--perch-sp-2, 16px);
    border-radius: var(--perch-radius-md, 6px);
    border: 1px solid transparent;
    cursor: pointer;
    transition: opacity var(--perch-dur, 120ms) var(--perch-ease, ease);
  }

  .btn:focus-visible {
    outline: var(--perch-ring-w, 2px) solid var(--perch-ring-color, currentColor);
    outline-offset: 2px;
  }

  .btn-primary {
    background: var(--perch-accent, #d79921);
    color: var(--perch-accent-fg, #1d2021);
    border-color: var(--perch-accent, #d79921);
  }

  .btn-primary:hover {
    opacity: 0.85;
  }

  .btn-secondary {
    background: transparent;
    color: var(--perch-text, #ebdbb2);
    border-color: var(--perch-border-strong, #7c716b);
  }

  .btn-secondary:hover {
    opacity: 0.75;
  }
</style>
