<script lang="ts">
  type Command = { id: string; label: string; run: () => void };
  let { open, commands }: { open: boolean; commands: Command[] } = $props();
  let query = $state("");
  let filtered = $derived(
    commands.filter((c) => c.label.toLowerCase().includes(query.toLowerCase()))
  );
</script>

{#if open}
  <div role="dialog" aria-label="command palette">
    <input type="text" bind:value={query} placeholder="Type a command…" />
    <ul>
      {#each filtered as c (c.id)}
        <li><button onclick={() => c.run()}>{c.label}</button></li>
      {/each}
    </ul>
  </div>
{/if}
