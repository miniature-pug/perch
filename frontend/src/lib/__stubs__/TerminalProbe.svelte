<script lang="ts">
  let p = $props();
  // Mirror the real Terminal's `focus` export so App's awaiting-input auto-focus
  // (primaryTerm?.focus()) is a no-op in tests instead of throwing.
  export function focus() {}
</script>
<div data-testid="terminal" data-pane-id={p.paneId} data-cwd={p.cwd}></div>
