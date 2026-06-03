<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, onPtyExit, writeToPty, resizePty } from "./wails";

  let { paneId, cwd, onExit }: { paneId: string; cwd: string; onExit?: (code: number) => void } = $props();

  let host:     HTMLDivElement;
  let term:     Terminal;
  let fit:      FitAddon;
  let offData:  (() => void) | undefined;
  let offExit:  (() => void) | undefined;
  let obs:      ResizeObserver | undefined;
  let disposed = false;

  onMount(() => {
    term = new Terminal({ convertEol: false, scrollback: 10000 });
    fit  = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    offData = onPtyData(paneId, (bytes) => term.write(bytes));
    offExit = onPtyExit(paneId, (code) => {
      if (disposed) return;
      term.write(`\r\n\x1b[2m[process exited: ${code}]\x1b[0m\r\n`);
      onExit?.(code);
    });
    term.onData((d) => writeToPty(paneId, Array.from(new TextEncoder().encode(d))));

    obs = new ResizeObserver(() => { fit.fit(); resizePty(paneId, term.cols, term.rows); });
    obs.observe(host);
  });

  onDestroy(() => {
    disposed = true;
    offData?.();
    offExit?.();
    obs?.disconnect();
    term?.dispose();
  });
</script>

<div class="terminal" bind:this={host}></div>
