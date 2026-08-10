<!-- frontend/src/lib/ShellDrawer.svelte -->
<script lang="ts">
  import { onMount } from "svelte";
  import Terminal from "./Terminal.svelte";
  import { openShell, reloadAgentEnv } from "./wails";

  const HOME_SHELL_PANE_ID = "shell-home";

  // File-based credentials (an AWS SSO token cache, for example) reach a running
  // agent on its own next call — no reload needed. This button is for the other
  // case: an exported variable a running process can only pick up via a fresh
  // exec. Kept in one string so the button's title and the on-page hint never drift.
  const RELOAD_HINT =
    "File-based credentials (e.g. AWS SSO) refresh on the agent's next call with no reload. " +
    "Use this only for a new or changed environment variable.";

  // collapsed is driven by layout.collapsed['shell'] via App.svelte;
  // onToggleCollapse lets the in-drawer button call back to the authoritative store.
  //
  // chrome=false turns this into a bare terminal CELL (no header): ShellPanel uses it
  // for each per-session shell tab and owns the tab strip / split / collapse / reload
  // chrome itself. The home shell keeps chrome=true (its own header). onExit fires
  // when the shell pty exits so a panel can auto-close that tab.
  let {
    paneId,
    cwd,
    collapsed = false,
    chrome = true,
    visible,
    onToggleCollapse,
    onExit,
  }: {
    paneId: string;
    cwd: string;
    collapsed?: boolean;
    chrome?: boolean;
    visible?: boolean;
    onToggleCollapse?: () => void;
    onExit?: () => void;
  } = $props();

  // When a panel drives visibility (cell mode) it passes `visible` explicitly;
  // otherwise (home shell) visibility follows the collapse state.
  const termVisible = $derived(visible ?? !collapsed);

  onMount(() => { openShell(paneId, cwd); });
</script>

<div class="shell-drawer" class:collapsed class:no-chrome={!chrome}>
  {#if chrome}
    <div class="shell-header">
      <span class="shell-title">Shell — {cwd}</span>
      {#if paneId !== HOME_SHELL_PANE_ID}
        <button
          onclick={() => { reloadAgentEnv(paneId).catch(() => {}); }}
          aria-label="Reload agent with this terminal's environment"
          title={RELOAD_HINT}
        >↻ env → agent</button>
      {/if}
      {#if collapsed}
        <button onclick={() => onToggleCollapse?.()} aria-label="expand shell">▲ Expand</button>
      {:else}
        <button onclick={() => onToggleCollapse?.()} aria-label="collapse shell">▼ Collapse</button>
      {/if}
    </div>
  {/if}
  <!-- Keep the Terminal mounted across collapse/expand (hide, do not unmount) so the
       xterm buffer and its pty subscription survive; unmounting rebuilt a blank xterm
       that stayed empty until the next pty output. In cell mode the panel hides us via
       an ancestor, so the body itself is never display:none-d here. -->
  <section aria-label="shell" class="shell-body" style:display={collapsed && chrome ? "none" : undefined}>
    <!-- visible drives the Terminal's re-fit when it is un-hidden: the drawer/panel
         hides it via an ancestor display:none, which never fires xterm's ResizeObserver. -->
    <Terminal {paneId} {cwd} visible={termVisible} {onExit} />
  </section>
</div>

<style>
  /* ── Shell drawer outer ───────────────────────────────────────── */
  .shell-drawer {
    display: flex;
    flex-direction: column;
    /* Fill the height the parent zone gives us (JS-driven shellH, or the home
       shell's fixed height). flex:1 + min-height:0 is what makes .shell-body —
       and the xterm host inside it — resolve to the zone's ACTUAL visible height
       instead of xterm's content-driven default (~24 rows). Without it FitAddon
       measures the tall content box, keeps too many rows, and the grid's bottom
       (the cursor) is clipped by the zone's overflow:hidden with no way to scroll
       to it. The parent zone MUST be a flex column for this to take (see
       .shell-drawer-zone / .home-shell-zone in App.svelte). */
    flex: 1;
    min-height: 0;
    background: var(--perch-bg);
    border-top: 1px solid var(--perch-border);
    overflow: hidden;
  }

  .shell-drawer.collapsed {
    flex: none;
  }

  /* Cell mode: the tab strip above already provides the top border, so the bare
     cell must not add a second line. */
  .shell-drawer.no-chrome {
    border-top: none;
  }

  /* ── Header strip ─────────────────────────────────────────────── */
  .shell-header {
    display: flex;
    align-items: center;
    height: 28px;
    padding: 0 var(--perch-sp-1);
    flex-shrink: 0;
    background: var(--perch-surface);
    border-bottom: 1px solid var(--perch-border);
    gap: var(--perch-sp-1);
  }

  /* ── Title: "Shell — {cwd}" ───────────────────────────────────── */
  .shell-title {
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
  }

  /* ── Collapse/expand button ───────────────────────────────────── */
  .shell-header button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 22px;
    padding: 0 var(--perch-sp-1);
    background: transparent;
    color: var(--perch-text-dim);
    border: 1px solid transparent;
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    flex-shrink: 0;
    margin-left: auto;
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .shell-header button:hover {
    color: var(--perch-text);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .shell-header button:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* ── Terminal body ────────────────────────────────────────────── */
  .shell-body {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    overflow: hidden; /* host: hidden OK, never auto — xterm manages its own viewport */
  }
</style>
