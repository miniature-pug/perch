<script lang="ts">
  import { onMount } from "svelte";
  import { terminalExitHandlers, terminalFocusCalls, terminalMountCounts } from "./terminalExit";
  let p = $props();
  // Count this component-instance mount, once. A DOM relocation reuses the
  // same instance, so the count does not advance. This is exactly what the
  // split single-mount test (F10a) asserts stays flat across a split toggle.
  onMount(() => {
    const id = p.paneId as string;
    terminalMountCounts[id] = (terminalMountCounts[id] ?? 0) + 1;
  });
  // Mirrors the real Terminal's `focus` export. jsdom has no xterm textarea
  // to move DOM focus into, so instead of a no-op, this records the call per
  // paneId. A test asserts App focused the active pane on the `i` key or on
  // awaiting-input (F17).
  export function focus() {
    const id = p.paneId as string;
    terminalFocusCalls[id] = (terminalFocusCalls[id] ?? 0) + 1;
  }
  // Register this pane's onExit while mounted, and drop it on unmount. This
  // lets a test fire terminalExitHandlers[paneId](code) to drive
  // App.handleAgentExit.
  $effect(() => {
    const id = p.paneId as string;
    if (p.onExit) terminalExitHandlers[id] = p.onExit as (code: number) => void;
    return () => { if (terminalExitHandlers[id] === p.onExit) delete terminalExitHandlers[id]; };
  });
</script>
<div data-testid="terminal" data-pane-id={p.paneId} data-cwd={p.cwd}></div>
