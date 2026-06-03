<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, writeToPty, resizePty } from "./wails";

  let { paneId, cwd }: { paneId: string; cwd: string } = $props();

  let host:    HTMLDivElement;
  let term:    Terminal;
  let fit:     FitAddon;
  let offData: (() => void) | undefined;
  let obs:     ResizeObserver | undefined;

  onMount(() => {
    term = new Terminal({ convertEol: false, scrollback: 10000 });
    fit  = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    offData = onPtyData(paneId, (bytes) => term.write(bytes));
    term.onData((d) => writeToPty(paneId, Array.from(new TextEncoder().encode(d))));

    obs = new ResizeObserver(() => { fit.fit(); resizePty(paneId, term.cols, term.rows); });
    obs.observe(host);
  });

  onDestroy(() => {
    offData?.();
    obs?.disconnect();
    term?.dispose();
  });
</script>

<div class="terminal" bind:this={host}></div>
