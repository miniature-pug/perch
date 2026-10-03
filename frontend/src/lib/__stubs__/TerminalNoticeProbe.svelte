<!-- Terminal stub that records the resync() and notice() exports, so a test
     can assert what a parent asked the terminal to do. -->
<script lang="ts">
  import { terminalCalls } from "./terminalCalls";
  let p = $props();
  export function resync() { terminalCalls.push({ paneId: p.paneId as string, call: "resync" }); }
  export function notice(text: string) { terminalCalls.push({ paneId: p.paneId as string, call: "notice", text }); }
</script>
<div data-testid="terminal" data-pane-id={p.paneId}></div>
