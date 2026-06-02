<script lang="ts">
  type Tab = { id: string; label: string };
  let {
    tabs,
    activeId,
    onselect,
    onclose,
  }: { tabs: Tab[]; activeId: string; onselect?: (id: string) => void; onclose?: (id: string) => void } = $props();
</script>

<div role="tablist">
  {#each tabs as t (t.id)}
    <div class="tab" class:active={t.id === activeId}>
      <button role="tab" onclick={() => onselect?.(t.id)}>{t.label}</button>
      <button aria-label={"close " + t.label} onclick={() => onclose?.(t.id)}>×</button>
    </div>
  {/each}
</div>
