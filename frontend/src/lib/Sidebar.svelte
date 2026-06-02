<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { listSessions, onSessionsChanged, type SessionInfo } from "./wails";

  let {
    onselect,
    onkill,
  }: { onselect?: (s: SessionInfo) => void; onkill?: (s: SessionInfo) => void } = $props();
  let sessions = $state<SessionInfo[]>([]);
  let off: (() => void) | undefined;

  async function refresh() {
    sessions = await listSessions();
  }
  onMount(() => {
    refresh();
    off = onSessionsChanged(refresh);
  });
  onDestroy(() => off?.());
</script>

<nav aria-label="sessions">
  <ul>
    {#each sessions as s (s.id)}
      <li>
        <button onclick={() => onselect?.(s)}>
          <span class="branch">{s.window}</span>
          <span class="status status-{s.status}">{s.status}</span>
        </button>
        <button
          aria-label={"kill " + s.window}
          onclick={(e) => { e.stopPropagation(); onkill?.(s); }}
        >✕</button>
      </li>
    {/each}
  </ul>
</nav>
