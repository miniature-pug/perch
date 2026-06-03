<!-- frontend/src/lib/ShellDrawer.svelte -->
<script lang="ts">
  import { onMount } from "svelte";
  import Terminal from "./Terminal.svelte";
  import { openShell } from "./wails";

  let { paneId, cwd }: { paneId: string; cwd: string } = $props();
  let collapsed = $state(false);

  onMount(() => { openShell(paneId, cwd); });
</script>

<div class="shell-drawer" class:collapsed>
  <div class="shell-header">
    <span class="shell-title">Shell — {cwd}</span>
    {#if collapsed}
      <button onclick={() => (collapsed = false)} aria-label="expand shell">▲ Expand</button>
    {:else}
      <button onclick={() => (collapsed = true)} aria-label="collapse shell">▼ Collapse</button>
    {/if}
  </div>
  {#if !collapsed}
    <section aria-label="shell" class="shell-body">
      <Terminal {paneId} {cwd} />
    </section>
  {/if}
</div>
