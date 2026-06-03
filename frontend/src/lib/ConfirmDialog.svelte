<script lang="ts">
  let {
    open, message, confirmLabel = "Confirm", destructive = false, note,
    onConfirm, onCancel,
    onconfirm, oncancel,
  }: {
    open: boolean; message: string; confirmLabel?: string; destructive?: boolean; note?: string;
    onConfirm?: () => void; onCancel?: () => void;
    onconfirm?: () => void; oncancel?: () => void;
  } = $props();
</script>

{#if open}
  <div role="dialog" aria-label="confirm" class="confirm-overlay">
    <div class="confirm-dialog">
      <p class="confirm-message">{message}</p>
      {#if note}<p class="confirm-note">{note}</p>{/if}
      <div class="confirm-actions">
        <button
          class="btn {destructive ? 'btn-danger' : 'btn-primary'}"
          onclick={() => { onConfirm?.(); onconfirm?.(); }}
        >{confirmLabel}</button>
        <button class="btn" onclick={() => { onCancel?.(); oncancel?.(); }}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Full-screen scrim that also centres the card */
  .confirm-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
  }

  /* Modal card */
  .confirm-dialog {
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border);
    border-radius: 6px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.45);
    padding: var(--perch-sp-3);
    min-width: 360px;
    max-width: 480px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  /* Main message */
  .confirm-message {
    margin: 0 0 var(--perch-sp-1) 0;
    color: var(--perch-text);
    font-size: var(--perch-fs-body);
    line-height: 1.5;
  }

  /* Optional note — dim caption */
  .confirm-note {
    margin: 0 0 var(--perch-sp-2) 0;
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    line-height: 1.4;
  }

  /* Action row — right-aligned; destructive btn LEFT of Cancel (flex row direction) */
  .confirm-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: var(--perch-sp-1);
    margin-top: var(--perch-sp-2);
    padding-top: var(--perch-sp-1);
    border-top: 1px solid var(--perch-border);
  }

  /* Shared button base */
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border);
    border-radius: 4px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: border-color var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .btn:hover {
    border-color: var(--perch-accent);
    color: var(--perch-accent);
  }

  .btn:active {
    background: color-mix(in srgb, var(--perch-accent) 12%, var(--perch-bg));
  }

  .btn:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    pointer-events: none;
  }

  /* Primary (non-destructive confirm) */
  .btn-primary {
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:hover {
    filter: brightness(1.1);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:active {
    filter: brightness(0.92);
  }

  /* Destructive confirm */
  .btn-danger {
    background: var(--perch-bg);
    color: var(--perch-err);
    border-color: var(--perch-err);
  }

  .btn-danger:hover {
    background: color-mix(in srgb, var(--perch-err) 12%, var(--perch-bg));
  }

  .btn-danger:focus-visible {
    outline: 2px solid var(--perch-err);
    outline-offset: 2px;
  }
</style>
