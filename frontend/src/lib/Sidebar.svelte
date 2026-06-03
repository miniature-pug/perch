<!-- frontend/src/lib/Sidebar.svelte -->
<script lang="ts">
  import type { WorkspaceVM } from "./wails";

  let {
    workspaces, activeId, onSelect, onNew,
  }: {
    workspaces: WorkspaceVM[];
    activeId: string;
    onSelect: (id: string) => void;
    onNew: () => void;
  } = $props();

  const STATUS = {
    running:             { icon: "◐", label: "running" },
    idle:                { icon: "◯", label: "idle" },
    "awaiting-approval": { icon: "⚠", label: "needs you" },
    done:                { icon: "✓", label: "done" },
    errored:             { icon: "✗", label: "error" },
  } as const;
</script>

<nav aria-label="sessions" class="sidebar">
  <ul class="workspace-list">
    {#each workspaces as ws (ws.id)}
      {@const st = STATUS[ws.state as keyof typeof STATUS] ?? { icon: "?", label: ws.state }}
      <li class:active={ws.id === activeId}>
        <button class="workspace-row"
          aria-current={ws.id === activeId ? "page" : undefined}
          onclick={() => onSelect(ws.id)}
          aria-label={ws.title}
        >
          <span class="status-icon" aria-hidden="true">{st.icon}</span>
          <span class="workspace-title">{ws.title}</span>
          <span class="workspace-branch dim">{ws.branch}</span>
          <span class="status-label">{st.label}</span>
        </button>
      </li>
    {/each}
  </ul>
  <button class="new-session-cta" onclick={onNew} aria-label="New session">+ New session</button>
</nav>
