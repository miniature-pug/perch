<!-- frontend/src/lib/MenuBar.svelte -->
<script lang="ts">
  let { onCommand, unreadCount = 0 }: { onCommand: (id: string) => void; unreadCount?: number } = $props();

  // `accel` is the global keyboard accelerator for the command, shown right-
  // aligned in the dropdown. It is rendered aria-hidden (announced via
  // aria-keyshortcuts instead) so the menuitem's accessible name stays the
  // plain label.
  type MenuItem = { id: string; label: string; accel?: string };
  const menus: { label: string; items: MenuItem[] }[] = [
    { label: "Session", items: [
      { id: "session:new",    label: "New session" },
      { id: "session:close",  label: "Close session" },
      { id: "session:remove", label: "Remove session" },
      // Folded in from the former "Worktree" menu: the user-facing noun is
      // "session", so "Open worktree" reads as "Open session" (opens the
      // selected session, the Enter accelerator) and the git-jargon top-level
      // menu is gone. "Reveal in Files" opens the session's directory.
      { id: "worktree:open",   label: "Open session", accel: "Enter" },
      { id: "worktree:reveal", label: "Reveal in Files" },
    ]},
    { label: "View", items: [
      { id: "view:agent", label: "Agent view", accel: "1" },
      { id: "view:code",  label: "Code view",  accel: "2" },
      { id: "view:diff",  label: "Diff view",  accel: "3" },
      { id: "view:split", label: "Split",      accel: "\\" },
      { id: "view:theme", label: "Cycle theme" },
    ]},
    { label: "Agent", items: [
      { id: "agent:approve-all", label: "Approve all pending" },
      { id: "agent:deny-all",    label: "Deny all pending" },
    ]},
    { label: "Help", items: [
      { id: "help:shortcuts", label: "Keyboard shortcuts" },
      { id: "help:about",     label: "About perch" },
    ]},
    { label: "Settings", items: [
      { id: "settings:open", label: "Settings…" },
    ]},
  ];

  let openMenu = $state<string | null>(null);
  // Track which top-level button opened the current menu (for focus-return on Escape)
  let triggerButtons = $state<Map<string, HTMLButtonElement>>(new Map());

  function toggleMenu(label: string, btn: HTMLButtonElement) {
    triggerButtons.set(label, btn);
    openMenu = openMenu === label ? null : label;
  }

  function runItem(id: string) { onCommand(id); openMenu = null; }

  function closeAndReturn() {
    const label = openMenu;
    openMenu = null;
    if (label) {
      // Return focus to the button that opened this menu
      const btn = triggerButtons.get(label);
      if (btn) btn.focus();
    }
  }

  // Roving focus across the top-level menu buttons (WAI-ARIA menubar: Left/Right
  // move between menus). `anchor` is any element inside the menubar, used to find
  // the ordered set of top-level trigger buttons. When `keepOpen` is true (a
  // dropdown was already open) the adjacent menu opens and its first item is
  // focused; otherwise focus just moves to the adjacent trigger button.
  function moveTopLevel(currentLabel: string, dir: 1 | -1, anchor: HTMLElement, keepOpen: boolean) {
    const bar = anchor.closest('[role="menubar"]');
    if (!bar) return;
    const btns = Array.from(bar.querySelectorAll<HTMLButtonElement>('.menu-root > button[role="menuitem"]'));
    const labels = menus.map((mn) => mn.label);
    const i = labels.indexOf(currentLabel);
    if (i === -1) return;
    const ni = (i + dir + labels.length) % labels.length;
    const nextBtn = btns[ni];
    const nextLabel = labels[ni];
    if (!nextBtn) return;
    if (keepOpen) {
      triggerButtons.set(nextLabel, nextBtn);
      openMenu = nextLabel;
      requestAnimationFrame(() => {
        const first = nextBtn.parentElement?.querySelector<HTMLElement>('[role="menu"] [role="menuitem"]');
        first?.focus();
      });
    } else {
      nextBtn.focus();
    }
  }

  function handleTriggerKey(e: KeyboardEvent, label: string, btn: HTMLButtonElement) {
    if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
      e.preventDefault();
      triggerButtons.set(label, btn);
      openMenu = label;
      // Focus first item after Svelte updates DOM
      requestAnimationFrame(() => {
        const menu = btn.parentElement?.querySelector<HTMLElement>('[role="menu"] [role="menuitem"]');
        menu?.focus();
      });
    } else if (e.key === "ArrowRight") {
      e.preventDefault();
      moveTopLevel(label, 1, btn, openMenu !== null);
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      moveTopLevel(label, -1, btn, openMenu !== null);
    } else if (e.key === "Escape") {
      openMenu = null;
    }
  }

  function handleItemKey(e: KeyboardEvent, item: MenuItem, items: MenuItem[], menuEl: HTMLElement) {
    const allItems = Array.from(menuEl.querySelectorAll<HTMLElement>('[role="menuitem"]'));
    const currentIndex = allItems.findIndex((el) => el === e.currentTarget);

    if (e.key === "ArrowDown") {
      e.preventDefault();
      const next = allItems[currentIndex + 1];
      if (next) next.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      const prev = allItems[currentIndex - 1];
      if (prev) prev.focus();
    } else if (e.key === "ArrowRight") {
      e.preventDefault();
      if (openMenu) moveTopLevel(openMenu, 1, menuEl, true);
    } else if (e.key === "ArrowLeft") {
      e.preventDefault();
      if (openMenu) moveTopLevel(openMenu, -1, menuEl, true);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      runItem(item.id);
    } else if (e.key === "Escape") {
      closeAndReturn();
    }
  }
</script>

<svelte:window onclick={() => (openMenu = null)} />

<header class="menubar" role="menubar">
  {#each menus as m}
    <div class="menu-root">
      <button role="menuitem" aria-haspopup="menu" aria-expanded={openMenu === m.label}
        onclick={(e) => { e.stopPropagation(); toggleMenu(m.label, e.currentTarget as HTMLButtonElement); }}
        onkeydown={(e) => handleTriggerKey(e, m.label, e.currentTarget as HTMLButtonElement)}
      >{m.label}</button>
      {#if openMenu === m.label}
        <ul role="menu" class="dropdown">
          {#each m.items as item}
            <li role="menuitem" tabindex="0"
              aria-keyshortcuts={item.accel ?? undefined}
              onclick={(e) => { e.stopPropagation(); runItem(item.id); }}
              onkeydown={(e) => {
                const menuEl = (e.currentTarget as HTMLElement).closest('[role="menu"]') as HTMLElement;
                handleItemKey(e, item, m.items, menuEl);
              }}
            ><span class="menu-label">{item.label}</span>{#if item.accel}<kbd class="menu-accel" aria-hidden="true">{item.accel}</kbd>{/if}</li>
          {/each}
        </ul>
      {/if}
    </div>
  {/each}
  <!-- The bell is a direct child of role="menubar", so it must carry a menubar
       role to be a valid menubar item (a bare button here is invalid ARIA). It
       toggles the notification hub, so it stays an action menuitem (no submenu →
       no aria-haspopup). Roving lives over the .menu-root triggers; the bell
       stays reachable via Tab and click. -->
  <button class="bell" role="menuitem" aria-label="notifications"
    onclick={(e) => { e.stopPropagation(); onCommand("notifications:open"); }}>
    🔔{#if unreadCount > 0}<span class="badge">{unreadCount > 99 ? '99+' : unreadCount}</span>{/if}
  </button>
</header>

<style>
  /* ── MenuBar strip ────────────────────────────────────────────── */
  .menubar {
    display: flex;
    align-items: center;
    height: 32px;
    padding: 0 var(--perch-sp-1);
    background: var(--perch-surface);
    border-bottom: 1px solid var(--perch-border);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    color: var(--perch-text);
    flex-shrink: 0;
    gap: 2px;
  }

  /* ── Menu trigger ─────────────────────────────────────────────── */
  .menu-root {
    position: relative;
  }

  .menu-root > button {
    display: inline-flex;
    align-items: center;
    height: 24px;
    padding: 0 var(--perch-sp-1);
    background: transparent;
    color: var(--perch-text);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
    white-space: nowrap;
  }

  .menu-root > button:hover {
    color: var(--perch-accent);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .menu-root > button[aria-expanded="true"] {
    color: var(--perch-accent);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }

  .menu-root > button:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* ── Dropdown popover ─────────────────────────────────────────── */
  .dropdown {
    position: absolute;
    top: calc(100% + 2px);
    left: 0;
    min-width: 180px;
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    /* Solid, never glass: this dropdown can overlap the agent terminal, where
       WebKitGTK paints backdrop-filter surfaces transparent over the composited
       terminal subtree (mirrors the ApprovalCard fix). */
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-float);
    z-index: var(--perch-z-menu-dropdown);
  }

  .dropdown li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--perch-sp-2);
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale))
             calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    font-size: var(--perch-fs-body);
    font-family: var(--perch-font-sans);
    color: var(--perch-text);
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
  }

  /* Accelerator hint — mono, dim, right-aligned (mirrors the palette's kbd). */
  .menu-accel {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    color: var(--perch-text-dim);
    background: var(--perch-bg-elev);
    border: 1px solid var(--perch-border);
    border-radius: var(--perch-radius-sm);
    padding: 1px 4px;
    line-height: var(--perch-lh-code);
    flex-shrink: 0;
  }

  .dropdown li:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }

  .dropdown li:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* ── Bell notification button ─────────────────────────────────── */
  .bell {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    background: transparent;
    color: var(--perch-text-dim);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
    cursor: pointer;
    flex-shrink: 0;
    margin-left: auto;
    font-size: 14px;
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .bell:hover {
    color: var(--perch-text);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .bell:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* ── Unread badge ─────────────────────────────────────────────── */
  .badge {
    position: absolute;
    top: 2px;
    right: 2px;
    min-width: 14px;
    height: 14px;
    padding: 0 3px;
    background: var(--perch-err);
    color: var(--perch-accent-fg);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-label);
    font-weight: 600;
    border-radius: 999px;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
    pointer-events: none;
  }
</style>
