<script lang="ts">
  let {
    open,
    message,
    confirmLabel = "Confirm",
    onconfirm,
    oncancel,
  }: {
    open: boolean;
    message: string;
    confirmLabel?: string;
    onconfirm?: () => void;
    oncancel?: () => void;
  } = $props();
</script>

{#if open}
  <div role="dialog" aria-label="confirm">
    <p>{message}</p>
    <button onclick={() => onconfirm?.()}>{confirmLabel}</button>
    <button onclick={() => oncancel?.()}>Cancel</button>
  </div>
{/if}
