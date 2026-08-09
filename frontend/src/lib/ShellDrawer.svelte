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
  let {
    paneId,
    cwd,
    collapsed = false,
    onToggleCollapse,
  }: {
    paneId: string;
    cwd: string;
    collapsed?: boolean;
    onToggleCollapse?: () => void;
  } = $props();

  onMount(() => { openShell(paneId, cwd); });
</script>

<div class="shell-drawer" class:collapsed>
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
  <!-- Keep the Terminal mounted across collapse/expand (hide, do not unmount) so the
       xterm buffer and its pty subscription survive; unmounting rebuilt a blank xterm
       that stayed empty until the next pty output. -->
  <section aria-label="shell" class="shell-body" style:display={collapsed ? "none" : undefined}>
    <!-- visible drives the Terminal's re-fit on un-collapse: the drawer hides it via
         an ancestor display:none, which never fires the terminal's own ResizeObserver. -->
    <Terminal {paneId} {cwd} visible={!collapsed} />
  </section>
</div>

<style>
  /* ── Shell drawer outer ───────────────────────────────────────── */
  .shell-drawer {
    display: flex;
    flex-direction: column;
    background: var(--perch-bg);
    border-top: 1px solid var(--perch-border);
    /* Height is JS-driven via parent; we fill what we're given */
    overflow: hidden;
  }

  .shell-drawer.collapsed {
    flex: none;
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
