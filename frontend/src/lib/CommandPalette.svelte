<!-- frontend/src/lib/CommandPalette.svelte -->
<script lang="ts">
  type Command = { id: string; group: string; label: string; keybinding?: string };
  let {
    open, commands, onRun, onClose,
  }: { open: boolean; commands: Command[]; onRun: (id: string) => void; onClose: () => void } = $props();

  let query = $state("");
  let active = $state(0);

  // Svelte action: focus the node immediately on mount (avoids the a11y autofocus warning).
  function focusOnMount(node: HTMLElement) { node.focus(); }

  function fuzzyScore(label: string, q: string): number {
    if (!q) return 1;
    const lbl = label.toLowerCase(); const ql = q.toLowerCase();
    let score = 0; let si = 0;
    for (const ch of ql) {
      const idx = lbl.indexOf(ch, si);
      if (idx === -1) return 0;
      score += idx === si ? 2 : 1; si = idx + 1;
    }
    return score;
  }

  let filtered = $derived(
    query
      ? commands.map((c) => ({ c, score: fuzzyScore(c.label, query) }))
          .filter((x) => x.score > 0).sort((a, b) => b.score - a.score).map((x) => x.c)
      : commands
  );

  // Reset active whenever filtered list changes (query change)
  $effect(() => {
    // Access filtered to track it; reset active to 0
    filtered; // eslint-disable-line @typescript-eslint/no-unused-expressions
    active = 0;
  });

  let grouped = $derived(
    filtered.reduce<{ group: string; items: Command[] }[]>((acc, c) => {
      const last = acc[acc.length - 1];
      if (last && last.group === c.group) last.items.push(c);
      else acc.push({ group: c.group, items: [c] });
      return acc;
    }, [])
  );

  // Compute active option id for aria-activedescendant
  let activeId = $derived(filtered.length > 0 ? `palette-option-${active}` : undefined);

  function handleKey(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      active = Math.min(active + 1, filtered.length - 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      active = Math.max(active - 1, 0);
    } else if (e.key === "Enter" && filtered.length > 0) {
      onRun(filtered[active].id);
    } else if (e.key === "Escape") {
      onClose();
    }
  }
</script>

{#if open}
  <div role="dialog" aria-label="command palette" class="palette-overlay">
    <div class="palette">
      <input type="text" role="combobox" aria-autocomplete="list" aria-controls="palette-list"
        aria-expanded={open}
        aria-activedescendant={activeId}
        bind:value={query} onkeydown={handleKey} placeholder="Type a command… (⌘K)"
        use:focusOnMount />
      <ul id="palette-list" role="listbox" class="palette-list">
        {#each grouped as g}
          <li class="group-header" aria-hidden="true">{g.group}:</li>
          {#each g.items as c (c.id)}
            {@const flatIndex = filtered.indexOf(c)}
            <li role="option"
              id="palette-option-{flatIndex}"
              aria-selected={flatIndex === active}
              class="palette-item {flatIndex === active ? 'is-active' : ''}"
              tabindex="-1"
              onclick={() => onRun(c.id)}
              onkeydown={(e) => e.key === "Enter" && onRun(c.id)}>
              <span class="item-label">{c.label}</span>
              {#if c.keybinding}<kbd class="item-kbd">{c.keybinding}</kbd>{/if}
            </li>
          {/each}
        {/each}
      </ul>
    </div>
  </div>
{/if}
