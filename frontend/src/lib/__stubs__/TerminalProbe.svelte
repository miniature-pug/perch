<script lang="ts">
  import { onMount } from "svelte";
  import { terminalExitHandlers, terminalFocusCalls, terminalMountCounts } from "./terminalExit";
  let p = $props();
  // Count this component-instance mount (once). A DOM relocation reuses the same
  // instance, so the count does NOT advance — that is exactly what the split
  // single-mount test (F10a) asserts stays flat across a split toggle.
  onMount(() => {
    const id = p.paneId as string;
    terminalMountCounts[id] = (terminalMountCounts[id] ?? 0) + 1;
  });
  // Mirror the real Terminal's `focus` export. jsdom has no xterm textarea to move
  // DOM focus into, so instead of a no-op we record the call per paneId — a test
  // asserts App focused the ACTIVE pane on `i` / awaiting-input (F17).
  export function focus() {
    const id = p.paneId as string;
    terminalFocusCalls[id] = (terminalFocusCalls[id] ?? 0) + 1;
  }
  // Register this pane's onExit while mounted; drop it on unmount. Lets a test fire
  // terminalExitHandlers[paneId](code) to drive App.handleAgentExit.
  $effect(() => {
    const id = p.paneId as string;
    if (p.onExit) terminalExitHandlers[id] = p.onExit as (code: number) => void;
    return () => { if (terminalExitHandlers[id] === p.onExit) delete terminalExitHandlers[id]; };
  });
</script>
<div data-testid="terminal" data-pane-id={p.paneId} data-cwd={p.cwd}></div>
