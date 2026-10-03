<script lang="ts">
  let p = $props();
</script>
<div data-testid="filetree" data-root={p.root}>
  <button onclick={() => p.onOpen("/some/file.ts")}>open file</button>
  <button onclick={() => p.onOpen("/some/file.md")}>open markdown</button>
  <button onclick={() => p.onOpen("/some/diagram.mmd")}>open mermaid</button>
  <button onclick={() => p.onOpen("@mention:/some/file.ts")}>mention file</button>
</div>
