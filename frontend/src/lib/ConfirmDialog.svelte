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
  <div role="dialog" aria-label="confirm">
    <p>{message}</p>
    {#if note}<p class="confirm-note dim">{note}</p>{/if}
    <button onclick={() => { onConfirm?.(); onconfirm?.(); }}>{confirmLabel}</button>
    <button onclick={() => { onCancel?.(); oncancel?.(); }}>Cancel</button>
  </div>
{/if}
