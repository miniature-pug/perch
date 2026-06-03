<script lang="ts">
  let p = $props();
</script>
<div data-testid="filetree" data-root={p.root}>
  <button onclick={() => p.onOpen("/some/file.ts")}>open file</button>
  <button onclick={() => p.onOpen("/some/file.md")}>open markdown</button>
</div>
