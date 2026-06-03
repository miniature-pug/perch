<script lang="ts">
  let {
    open, message, confirmLabel = "Confirm", destructive = false,
    onConfirm, onCancel,
    onconfirm, oncancel,
  }: {
    open: boolean; message: string; confirmLabel?: string; destructive?: boolean;
    onConfirm?: () => void; onCancel?: () => void;
    onconfirm?: () => void; oncancel?: () => void;
  } = $props();
</script>

{#if open}
  <div role="dialog" aria-label="confirm">
    <p>{message}</p>
    {#if destructive}<p class="undo-notice dim">This action can be undone within 10 seconds.</p>{/if}
    <button onclick={() => { onConfirm?.(); onconfirm?.(); }}>{confirmLabel}</button>
    <button onclick={() => { onCancel?.(); oncancel?.(); }}>Cancel</button>
  </div>
{/if}
