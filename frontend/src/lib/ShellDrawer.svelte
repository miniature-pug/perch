<!-- frontend/src/lib/ShellDrawer.svelte -->
<script lang="ts">
  import { onMount } from "svelte";
  import Terminal from "./Terminal.svelte";
  import { openShell, reloadAgentEnv } from "./wails";
  import { addBlocking } from "./stores/notifications.svelte";

  const HOME_SHELL_PANE_ID = "shell-home";

  // File-based credentials, for example an AWS SSO token cache, reach a running
  // agent on its own next call. No reload is needed. This button is for the
  // other case: an exported variable that a running process can only pick up
  // through a fresh exec. This text stays in one string, so the button's title
  // and the on-page hint never drift apart.
  const RELOAD_HINT =
    "File-based credentials (e.g. AWS SSO) refresh on the agent's next call with no reload. " +
    "Use this only for a new or changed environment variable.";

  // App.svelte drives `collapsed` through layout.collapsed['shell'].
  // `onToggleCollapse` lets the in-drawer button call back to that store.
  //
  // chrome=false turns this into a bare terminal cell with no header. ShellPanel
  // uses this mode for each per-session shell tab, and owns the tab strip,
  // split, collapse, and reload chrome itself. The home shell keeps chrome=true
  // for its own header. onExit fires when the shell pty exits, so a panel can
  // auto-close that tab.
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

  // When a panel drives visibility (cell mode), it passes `visible` explicitly.
  // Otherwise, for the home shell, visibility follows the collapse state.
  const termVisible = $derived(visible ?? !collapsed);

  let term = $state<{ resync?: () => void; notice?: (text: string) => void } | undefined>();

  // Spawn the shell, then re-send the terminal size: a resize sent before the
  // backend registered the pty is rejected and would otherwise never be
  // retried (FEX-15). A failed spawn writes a line into the terminal instead
  // of leaving a silently blank pane (FEX-33).
  onMount(() => {
    openShell(paneId, cwd)
      .then(() => term?.resync?.())
      .catch((e) => term?.notice?.(`[could not start shell: ${e instanceof Error ? e.message : String(e)}]`));
  });

  function reloadEnv() {
    reloadAgentEnv(paneId).catch((e) => {
      addBlocking("", "Could not reload the agent", String(e), "error");
    });
  }
</script>

<div class="shell-drawer" class:collapsed class:no-chrome={!chrome}>
  {#if chrome}
    <div class="shell-header">
      <span class="shell-title">Shell — {cwd}</span>
      {#if paneId !== HOME_SHELL_PANE_ID}
        <button
          onclick={reloadEnv}
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
  <!-- Keep the Terminal mounted across collapse and expand (hide, do not unmount),
       so the xterm buffer and its pty subscription survive. Unmounting rebuilt a
       blank xterm that stayed empty until the next pty output. In cell mode the
       panel hides this drawer through an ancestor, so the body itself is never
       set to display:none here. -->
  <section aria-label="shell" class="shell-body" style:display={collapsed && chrome ? "none" : undefined}>
    <!-- `visible` drives the Terminal's re-fit when it is shown again. The
         drawer or panel hides it through an ancestor display:none, which never
         fires xterm's ResizeObserver. -->
    <Terminal bind:this={term} {paneId} {cwd} visible={termVisible} {onExit} />
  </section>
</div>

<style>
  /* ── Shell drawer outer ───────────────────────────────────────── */
  .shell-drawer {
    display: flex;
    flex-direction: column;
    /* Fills the height the parent zone gives it: a JS-driven shellH, or the
       home shell's fixed height. flex:1 plus min-height:0 makes .shell-body,
       and the xterm host inside it, resolve to the zone's actual visible
       height, instead of xterm's content-driven default of about 24 rows.
       Without this, FitAddon measures the tall content box, keeps too many
       rows, and the zone's overflow:hidden clips the grid's bottom (the
       cursor) with no way to scroll to it. The parent zone MUST be a flex
       column for this rule to take effect (see .shell-drawer-zone and
       .home-shell-zone in App.svelte). */
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
     cell must not add a second border line. */
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
    overflow: hidden; /* host: hidden is OK, never auto; xterm manages its own viewport */
  }
</style>
