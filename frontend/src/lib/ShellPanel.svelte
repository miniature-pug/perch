<!-- frontend/src/lib/ShellPanel.svelte
     The shell drawer: a tab strip over N shell terminals for the session.
     It supports tab switching, a + button to add a terminal, and a per-tab ×
     to close one (closing the last tab spawns a fresh terminal upstream, so
     the drawer is never empty). It also supports a side-by-side split toggle,
     an env-to-agent reload for the active shell, and collapse. Every shell
     stays mounted, hidden through display, so its xterm buffer and pty
     survive a tab switch; `visible` drives the shown cell's re-fit. All list,
     active, and split state lives in App (shellPanes.ts); this component is
     presentational and calls back for every action. The home shell is a
     single terminal and keeps using ShellDrawer directly. -->
<script lang="ts">
  import ShellDrawer from "./ShellDrawer.svelte";
  import { shellTabTitle, reloadMenuItems, activeShellTitle, type ShellPane } from "./shellPanes";
  import { reloadAgentEnv } from "./wails";

  const RELOAD_HINT =
    "File-based credentials (e.g. AWS SSO) refresh on the agent's next call with no reload. " +
    "Use this only for a new or changed environment variable.";

  let {
    cwd,
    panes,
    activeId,
    splitId,
    collapsed = false,
    onSelect,
    onNew,
    onClose,
    onToggleSplit,
    onToggleCollapse,
  }: {
    cwd: string;
    panes: ShellPane[];
    activeId: string | null;
    splitId: string | null;
    collapsed?: boolean;
    onSelect: (id: string) => void;
    onNew: () => void;
    onClose: (id: string) => void;
    onToggleSplit: () => void;
    onToggleCollapse: () => void;
  } = $props();

  // With more than one shell open, "reload the active tab" is ambiguous, so the
  // reload control becomes a split button. The labelled main button reloads
  // the active shell and shows its title. A caret opens a menu that reloads
  // any chosen shell. With a single shell, the button stays plain, with no
  // picker.
  const multi = $derived(panes.length > 1);
  const activeTitle = $derived(activeShellTitle(panes, activeId));
  const reloadItems = $derived(reloadMenuItems(panes, activeId));

  let menuOpen = $state(false);
  let menuEl = $state<HTMLDivElement | undefined>();
  let caretEl = $state<HTMLButtonElement | undefined>();

  function reloadActive() {
    if (activeId) reloadAgentEnv(activeId).catch(() => {});
  }

  // reloadPane reloads one chosen shell, any pane id, since the backend
  // resolves it independent of which tab has focus. It then closes the
  // picker. Focus returns to the caret, mirroring the Escape path, so
  // keyboard focus does not fall to <body> when the chosen menu item is
  // removed from the DOM.
  function reloadPane(id: string) {
    reloadAgentEnv(id).catch(() => {});
    menuOpen = false;
    caretEl?.focus();
  }

  // Focus the active item when the picker opens, so keyboard users land on the
  // shell that a plain reload would target. Arrow keys then move focus from
  // there.
  $effect(() => {
    if (menuOpen && menuEl) {
      const active = menuEl.querySelector<HTMLElement>('[data-active="true"]');
      (active ?? menuEl.querySelector<HTMLElement>('[role="menuitem"]'))?.focus();
    }
  });

  function onMenuKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.stopPropagation();
      menuOpen = false;
      caretEl?.focus();
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const items = menuEl ? [...menuEl.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
      if (items.length === 0) return;
      const cur = items.indexOf(document.activeElement as HTMLElement);
      const next = e.key === "ArrowDown"
        ? (cur + 1) % items.length
        : (cur - 1 + items.length) % items.length;
      items[next]?.focus();
    }
  }
</script>

<!-- Closes the reload picker on any outside click. The menu container, caret,
     and menu-item handlers all call stopPropagation, so clicks anywhere
     inside the picker, including its padding chrome, never reach this
     handler. -->
<svelte:window onclick={() => (menuOpen = false)} />

<div class="shell-panel" class:collapsed>
  <div class="shell-tabs">
    <div class="tab-strip" role="tablist" aria-label="terminals">
      {#each panes as p, i (p.id)}
        <div class="shell-tab" class:active={p.id === activeId} class:split={p.id === splitId}>
          <button
            class="tab-label"
            role="tab"
            aria-selected={p.id === activeId}
            title={shellTabTitle(i)}
            onclick={() => onSelect(p.id)}
          >{shellTabTitle(i)}</button>
          <button
            class="tab-close"
            aria-label="close {shellTabTitle(i)}"
            title="Close terminal"
            onclick={(e) => { e.stopPropagation(); onClose(p.id); }}
          >×</button>
        </div>
      {/each}
      <button class="tab-add" aria-label="new terminal" title="New terminal" onclick={onNew}>+</button>
    </div>
    <div class="tab-actions">
      <button
        class="tab-action"
        class:on={splitId != null}
        aria-label="split terminals side by side"
        aria-pressed={splitId != null}
        title="Split side by side"
        onclick={onToggleSplit}
      >⊟ Split</button>

      {#if multi}
        <!-- More than one shell: a split button. The main button reloads the
             active shell and names it so the target is unambiguous. The
             caret opens a picker to reload any shell. -->
        <div class="reload-group">
          <button
            class="tab-action reload-main"
            aria-label="Reload agent with this terminal's environment"
            title={RELOAD_HINT}
            onclick={reloadActive}
          >↻ env → agent · {activeTitle}</button>
          <button
            bind:this={caretEl}
            class="tab-action reload-caret"
            class:on={menuOpen}
            aria-label="choose which shell to reload"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            title="Reload a specific shell's environment"
            onclick={(e) => { e.stopPropagation(); menuOpen = !menuOpen; }}
          >▾</button>
          {#if menuOpen}
            <!-- stopPropagation on the container, so a click on the menu's own
                 padding chrome, between or around the item buttons, does not
                 bubble to the svelte:window handler and close the picker.
                 This onclick is a mouse-only propagation guard, not an
                 interactive surface. Focus and keyboard movement live on the
                 child menuitems (see onMenuKeydown), so the interactive-role
                 focus and keydown a11y rules do not apply here. -->
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_interactive_supports_focus -->
            <div bind:this={menuEl} class="reload-menu" role="menu" aria-label="reload agent from shell"
              onclick={(e) => e.stopPropagation()}>
              {#each reloadItems as it (it.id)}
                <button
                  class="reload-menu-item"
                  class:active={it.active}
                  role="menuitem"
                  data-active={it.active}
                  onclick={(e) => { e.stopPropagation(); reloadPane(it.id); }}
                  onkeydown={onMenuKeydown}
                >{it.title}{it.active ? " (active)" : ""}</button>
              {/each}
            </div>
          {/if}
        </div>
      {:else}
        <button
          class="tab-action"
          aria-label="Reload agent with this terminal's environment"
          title={RELOAD_HINT}
          onclick={reloadActive}
        >↻ env → agent</button>
      {/if}

      <button
        class="tab-action"
        aria-label={collapsed ? "expand shell" : "collapse shell"}
        title={collapsed ? "Expand" : "Collapse"}
        onclick={onToggleCollapse}
      >{collapsed ? "▲ Expand" : "▼ Collapse"}</button>
    </div>
  </div>

  <!-- The body is hidden, not unmounted, when collapsed, so every shell's
       buffer and pty survive. Each cell shows only when it is the active or
       split shell; the split cell is ordered to the right. -->
  <div class="shell-panel-body" class:split={splitId != null} style:display={collapsed ? "none" : undefined}>
    {#each panes as p (p.id)}
      {@const shown = p.id === activeId || p.id === splitId}
      <div
        class="shell-cell"
        class:is-split={p.id === splitId}
        style:display={shown ? undefined : "none"}
        style:order={p.id === splitId ? 1 : 0}
      >
        <ShellDrawer chrome={false} paneId={p.id} {cwd} visible={shown && !collapsed} onExit={() => onClose(p.id)} />
      </div>
    {/each}
  </div>
</div>

<style>
  .shell-panel {
    display: flex;
    flex-direction: column;
    /* Fills the zone height (JS-driven shellH). flex:1 plus min-height:0 makes
       the body, and each xterm host, resolve to the zone's real height
       instead of xterm's content default. The parent .shell-drawer-zone is a
       flex column. */
    flex: 1;
    min-height: 0;
    background: var(--perch-bg);
    border-top: 1px solid var(--perch-border);
    overflow: hidden;
  }

  .shell-panel.collapsed {
    flex: none;
  }

  /* ── Tab strip and header ─────────────────────────────────────── */
  .shell-tabs {
    display: flex;
    align-items: center;
    height: 28px;
    padding: 0 var(--perch-sp-1);
    flex-shrink: 0;
    background: var(--perch-surface);
    border-bottom: 1px solid var(--perch-border);
    gap: var(--perch-sp-1);
  }

  .tab-strip {
    display: flex;
    align-items: center;
    gap: 2px;
    flex: 1;
    min-width: 0;
    overflow-x: auto;
  }

  .shell-tab {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
    height: 22px;
    padding: 0 2px 0 var(--perch-sp-1);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
  }

  /* The active (primary) tab reads as selected. The split (secondary) tab gets
     an accent edge, so both shown shells are identifiable at a glance. */
  .shell-tab.active {
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
    border-color: var(--perch-border);
  }
  .shell-tab.split {
    border-color: color-mix(in srgb, var(--perch-accent) 55%, transparent);
  }

  .shell-tabs button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    /* The 22px height plus 4px icon/label gap matches the old labelled
       ShellDrawer header chrome (the home shell still renders it through
       ShellDrawer chrome=true), so the labelled Split, reload, and Collapse
       actions read the same in both drawers. */
    height: 22px;
    gap: 4px;
    padding: 0 var(--perch-sp-1);
    background: transparent;
    color: var(--perch-text-dim);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .tab-label {
    max-width: 12ch;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tab-close {
    width: 18px;
    padding: 0 !important;
    font-size: var(--perch-fs-body);
    line-height: 1;
  }

  .shell-tabs button:hover {
    color: var(--perch-text);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .shell-tabs button:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  .tab-action.on {
    color: var(--perch-accent);
    border-color: color-mix(in srgb, var(--perch-accent) 45%, transparent);
  }

  .tab-actions {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
    flex-shrink: 0;
  }

  /* ── Reload split button and shell picker ─────────────────────── */
  .reload-group {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  /* Joins the labelled main button and its caret into one control, by
     squaring the corners where they meet. The two-class selectors override
     .shell-tabs button. */
  .reload-group .reload-main {
    border-top-right-radius: 0;
    border-bottom-right-radius: 0;
  }
  .reload-group .reload-caret {
    padding: 0 3px;
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
  }

  .reload-menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    min-width: 100%;
    display: flex;
    flex-direction: column;
    padding: var(--perch-sp-1);
    gap: 2px;
    /* Solid, never glass. This menu overlaps the agent pane's terminal, where
       WebKitGTK paints backdrop-filter surfaces transparent over the
       composited terminal subtree (mirrors the fix in the MenuBar dropdown
       and ApprovalCard). */
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-float);
    z-index: var(--perch-z-menu-dropdown);
  }

  .reload-menu .reload-menu-item {
    justify-content: flex-start;
    width: 100%;
    white-space: nowrap;
  }
  .reload-menu .reload-menu-item.active {
    color: var(--perch-accent);
  }

  /* ── Body: one row of cells (one shown in tabs mode, two in split mode) ── */
  .shell-panel-body {
    display: flex;
    flex-direction: row;
    flex: 1;
    min-height: 0;
  }

  .shell-cell {
    display: flex;
    flex-direction: column;
    flex: 1 1 0;
    min-width: 0;
    min-height: 0;
  }

  .shell-cell.is-split {
    border-left: 1px solid var(--perch-border);
  }
</style>
