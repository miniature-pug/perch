<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { onPtyData, openTerminal, writeToPty, resizePty, closeTerminal } from "./wails";

  let { tabId, sessionId }: { tabId: string; sessionId: string } = $props();
  let host: HTMLDivElement;
  let term: Terminal;
  let fit: FitAddon;
  let offData: (() => void) | undefined;
  let destroyed = false;

  onMount(async () => {
    term = new Terminal({ convertEol: false, scrollback: 10000 });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    offData = onPtyData(tabId, (bytes) => term.write(bytes));
    term.onData((d) => {
      const bytes = Array.from(new TextEncoder().encode(d));
      writeToPty(tabId, bytes);
    });
    await openTerminal(tabId, sessionId);
    if (destroyed) {
      // Destroyed during the async open (e.g. fast tab switch): the bridge is now
      // registered on the backend, so reap it here — onDestroy's earlier
      // closeTerminal was a no-op (bridge did not exist yet).
      closeTerminal(tabId);
      return;
    }
    await resizePty(tabId, term.cols, term.rows);
  });

  onDestroy(() => {
    destroyed = true;
    offData?.();
    closeTerminal(tabId);
    term?.dispose();
  });
</script>

<div class="terminal" bind:this={host}></div>
