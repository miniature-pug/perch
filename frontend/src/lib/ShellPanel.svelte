<!-- frontend/src/lib/ShellPanel.svelte
     The per-session multi-terminal drawer: a tab strip over N shell terminals, with
     tab switching, a + to add one, a per-tab × (closing the last spawns a fresh one
     upstream, so the drawer is never empty), a side-by-side split toggle, an
     env→agent reload for the active shell, and collapse. Every shell stays MOUNTED
     (hidden via display) so its xterm buffer and pty survive a tab switch; `visible`
     drives the shown cell's re-fit. All list/active/split state lives in App
     (shellPanes.ts); this component is presentational and calls back for every action.
     The home shell is a single terminal and keeps using ShellDrawer directly. -->
<script lang="ts">
  import ShellDrawer from "./ShellDrawer.svelte";
  import { shellTabTitle, type ShellPane } from "./shellPanes";
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

  function reloadActive() {
    if (activeId) reloadAgentEnv(activeId).catch(() => {});
  }
</script>

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
      >⊟</button>
      <button
        class="tab-action"
        aria-label="Reload agent with this terminal's environment"
        title={RELOAD_HINT}
        onclick={reloadActive}
      >↻</button>
      <button
        class="tab-action"
        aria-label={collapsed ? "expand shell" : "collapse shell"}
        title={collapsed ? "Expand" : "Collapse"}
        onclick={onToggleCollapse}
      >{collapsed ? "▲" : "▼"}</button>
    </div>
  </div>

  <!-- Body is hidden (not unmounted) when collapsed so every shell's buffer + pty
       survive. Each cell shows only when it is the active or split shell; the split
       cell is ordered to the right. -->
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
    /* Fill the zone height (JS-driven shellH); flex:1 + min-height:0 is what makes the
       body — and each xterm host — resolve to the zone's real height rather than
       xterm's content default. The parent .shell-drawer-zone is a flex column. */
    flex: 1;
    min-height: 0;
    background: var(--perch-bg);
    border-top: 1px solid var(--perch-border);
    overflow: hidden;
  }

  .shell-panel.collapsed {
    flex: none;
  }

  /* ── Tab strip / header ───────────────────────────────────────── */
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

  /* The active (primary) tab reads as selected; the split (secondary) tab gets an
     accent edge so both shown shells are identifiable at a glance. */
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
    height: 20px;
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
    outline-offset: 1px;
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

  /* ── Body: one row of cells (one shown in tabs mode, two in split) ── */
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
