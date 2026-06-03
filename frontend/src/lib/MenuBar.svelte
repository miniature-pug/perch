<!-- frontend/src/lib/MenuBar.svelte -->
<script lang="ts">
  let { onCommand, unreadCount = 0 }: { onCommand: (id: string) => void; unreadCount?: number } = $props();

  type MenuItem = { id: string; label: string };
  const menus: { label: string; items: MenuItem[] }[] = [
    { label: "Session", items: [
      { id: "session:new",    label: "New session" },
      { id: "session:close",  label: "Close session" },
      { id: "session:remove", label: "Remove session" },
    ]},
    { label: "Worktree", items: [
      { id: "worktree:open",   label: "Open worktree" },
      { id: "worktree:reveal", label: "Reveal in Files" },
    ]},
    { label: "View", items: [
      { id: "view:agent", label: "Agent view" },
      { id: "view:code",  label: "Code view" },
      { id: "view:diff",  label: "Diff view" },
      { id: "view:split", label: "Split" },
      { id: "view:theme", label: "Theme…" },
    ]},
    { label: "Agent", items: [
      { id: "agent:approve-all", label: "Approve all pending" },
      { id: "agent:deny-all",    label: "Deny all pending" },
    ]},
    { label: "Help", items: [
      { id: "help:shortcuts", label: "Keyboard shortcuts" },
      { id: "help:about",     label: "About perch" },
    ]},
  ];

  let openMenu = $state<string | null>(null);
  function toggleMenu(label: string) { openMenu = openMenu === label ? null : label; }
  function runItem(id: string) { onCommand(id); openMenu = null; }
</script>

<svelte:window onclick={() => (openMenu = null)} />

<header class="menubar" role="menubar">
  {#each menus as m}
    <div class="menu-root">
      <button role="menuitem" aria-haspopup="menu" aria-expanded={openMenu === m.label}
        onclick={(e) => { e.stopPropagation(); toggleMenu(m.label); }}>{m.label}</button>
      {#if openMenu === m.label}
        <ul role="menu" class="dropdown" onclick={(e) => e.stopPropagation()}>
          {#each m.items as item}
            <li role="menuitem" tabindex="0"
              onclick={() => runItem(item.id)}
              onkeydown={(e) => e.key === "Enter" && runItem(item.id)}
            >{item.label}</li>
          {/each}
        </ul>
      {/if}
    </div>
  {/each}
  <button class="bell" aria-label="notifications"
    onclick={(e) => { e.stopPropagation(); onCommand("notifications:open"); }}>
    🔔{#if unreadCount > 0}<span class="badge">{unreadCount}</span>{/if}
  </button>
</header>
