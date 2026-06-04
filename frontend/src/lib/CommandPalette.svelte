<!-- frontend/src/lib/CommandPalette.svelte -->
<script lang="ts">
  import { CMD_RECENCY_MAX, STORAGE_CMD_RECENTS } from "./constants";
  import { focusOnMount } from "./actions";
  type Command = { id: string; group: string; label: string; keybinding?: string };
  let {
    open, commands, onRun, onClose,
  }: { open: boolean; commands: Command[]; onRun: (id: string) => void; onClose: () => void } = $props();

  let query = $state("");
  let active = $state(0);

  // Track command invocation recency: id → last-invoked timestamp.
  // Persisted to localStorage so recency survives palette re-opens within a session.
  function loadRecents(): Map<string, number> {
    try {
      const raw = localStorage.getItem(STORAGE_CMD_RECENTS);
      if (raw) return new Map(JSON.parse(raw) as [string, number][]);
    } catch { /* ignore */ }
    return new Map();
  }
  function saveRecent(id: string) {
    const map = loadRecents();
    map.set(id, Date.now());
    // Keep at most CMD_RECENCY_MAX entries
    if (map.size > CMD_RECENCY_MAX) {
      const oldest = [...map.entries()].sort((a, b) => a[1] - b[1])[0][0];
      map.delete(oldest);
    }
    try { localStorage.setItem(STORAGE_CMD_RECENTS, JSON.stringify([...map.entries()])); } catch { /* ignore */ }
  }

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

  // When the query is empty, surface recently-used commands first in a "Recent" group,
  // then show remaining commands in their normal groups below.
  let filtered = $derived((() => {
    if (query) {
      return commands.map((c) => ({ c, score: fuzzyScore(c.label, query) }))
        .filter((x) => x.score > 0).sort((a, b) => b.score - a.score).map((x) => x.c);
    }
    // No query: sort by recency (most-recent first) for the initial list
    const recents = loadRecents();
    return [...commands].sort((a, b) => {
      const ta = recents.get(a.id) ?? 0;
      const tb = recents.get(b.id) ?? 0;
      if (ta !== tb) return tb - ta; // more recent → earlier
      return 0; // preserve insertion order for equal timestamps
    });
  })());

  // Reset active whenever filtered list changes (query change)
  $effect(() => {
    // Access filtered to track it; reset active to 0
    filtered; // eslint-disable-line @typescript-eslint/no-unused-expressions
    active = 0;
  });

  let grouped = $derived((() => {
    if (query) {
      // When filtering, group normally
      return filtered.reduce<{ group: string; items: Command[] }[]>((acc, c) => {
        const last = acc[acc.length - 1];
        if (last && last.group === c.group) last.items.push(c);
        else acc.push({ group: c.group, items: [c] });
        return acc;
      }, []);
    }
    // No query: put recently-used commands first in a "Recent" group, rest below
    const recents = loadRecents();
    const recentIds = new Set(recents.keys());
    const recentCmds = filtered.filter((c) => recentIds.has(c.id));
    const otherCmds  = filtered.filter((c) => !recentIds.has(c.id));
    const result: { group: string; items: Command[] }[] = [];
    if (recentCmds.length > 0) result.push({ group: "Recent", items: recentCmds });
    // Group remaining by their normal group
    for (const c of otherCmds) {
      const last = result[result.length - 1];
      if (last && last.group === c.group) last.items.push(c);
      else result.push({ group: c.group, items: [c] });
    }
    return result;
  })());

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
      const id = filtered[active].id;
      saveRecent(id);
      onRun(id);
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
      <ul id="palette-list" role="listbox" class="palette-list scrollable">
        {#each grouped as g}
          <li class="group-header" aria-hidden="true">{g.group}:</li>
          {#each g.items as c (c.id)}
            {@const flatIndex = filtered.indexOf(c)}
            <li role="option"
              id="palette-option-{flatIndex}"
              aria-selected={flatIndex === active}
              class="palette-item {flatIndex === active ? 'is-active' : ''}"
              tabindex="-1"
              onclick={() => { saveRecent(c.id); onRun(c.id); }}
              onkeydown={(e) => { if (e.key === "Enter") { saveRecent(c.id); onRun(c.id); } }}>
              <span class="item-label">{c.label}</span>
              {#if c.keybinding}<kbd class="item-kbd">{c.keybinding}</kbd>{/if}
            </li>
          {/each}
        {/each}
      </ul>
    </div>
  </div>
{/if}

<style>
  /* Overlay scrim */
  .palette-overlay {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-command-palette);
  }

  /* Floating card — no padding (input/items touch the edges) */
  .palette {
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border);
    border-radius: 6px;
    box-shadow: var(--perch-shadow-float);
    min-width: 520px;
    max-width: 680px;
    width: 100%;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    overflow: hidden;
  }

  /* Search input — full width, border-bottom only, no outer radius */
  .palette input[type="text"] {
    display: block;
    width: 100%;
    box-sizing: border-box;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: none;
    border-bottom: 1px solid var(--perch-border-strong);
    border-radius: 0;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) calc(var(--perch-sp-2) * var(--perch-density-scale));
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    outline: none;
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .palette input[type="text"]:focus {
    border-bottom-color: var(--perch-accent);
  }

  .palette input[type="text"]::placeholder {
    color: var(--perch-text-dim);
  }

  /* Result list */
  .palette-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 320px;
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }

  .palette-list::-webkit-scrollbar { width: 6px; }
  .palette-list::-webkit-scrollbar-track { background: transparent; }
  .palette-list::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: 3px; }
  .palette-list::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  /* Group header row — dim uppercase label, non-interactive */
  .group-header {
    padding: 4px calc(var(--perch-sp-2) * var(--perch-density-scale));
    font-size: var(--perch-fs-label);
    font-weight: 600;
    color: var(--perch-text-dim);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-family: var(--perch-font-sans);
    border-bottom: 1px solid var(--perch-border);
    user-select: none;
    list-style: none;
  }

  /* Command row */
  .palette-item {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) calc(var(--perch-sp-2) * var(--perch-density-scale));
    border-bottom: 1px solid var(--perch-border-strong);
    font-size: var(--perch-fs-body);
    font-family: var(--perch-font-sans);
    color: var(--perch-text);
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
    list-style: none;
  }

  .palette-item:last-child {
    border-bottom: none;
  }

  .palette-item:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }

  .palette-item.is-active,
  .palette-item[aria-selected="true"] {
    background: color-mix(in srgb, var(--perch-accent) 16%, transparent);
  }

  .palette-item:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* Label takes remaining space */
  .item-label {
    flex: 1;
  }

  /* Keybinding badge — mono, right-aligned */
  .item-kbd {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    color: var(--perch-text-dim);
    background: var(--perch-bg-elev);
    border: 1px solid var(--perch-border);
    border-radius: 4px;
    padding: 1px 4px;
    flex-shrink: 0;
    line-height: var(--perch-lh-code);
  }
</style>
