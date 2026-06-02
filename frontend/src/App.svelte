<script lang="ts">
  import Sidebar from "./lib/Sidebar.svelte";
  import Tabs from "./lib/Tabs.svelte";
  import Terminal from "./lib/Terminal.svelte";
  import DiffPanel from "./lib/DiffPanel.svelte";
  import CommandPalette from "./lib/CommandPalette.svelte";
  import NewAgentDialog from "./lib/NewAgentDialog.svelte";
  import ConfirmDialog from "./lib/ConfirmDialog.svelte";
  import { createAgent, killSession, type SessionInfo } from "./lib/wails";

  type OpenTab = { id: string; label: string; sessionId: string; dir: string };
  let tabs = $state<OpenTab[]>([]);
  let activeId = $state("");
  let paletteOpen = $state(false);
  let newAgentOpen = $state(false);
  let pendingKill = $state<SessionInfo | null>(null);

  const commands = [
    { id: "new-agent", label: "New agent", run: () => { paletteOpen = false; newAgentOpen = true; } },
  ];

  async function handleNewAgent(tool: string, projectPath: string, branch: string) {
    try {
      await createAgent(tool, projectPath, branch);
    } catch (err) {
      console.error("createAgent failed:", err);
    }
    newAgentOpen = false;
  }

  function openSession(s: SessionInfo) {
    if (!tabs.find((t) => t.id === s.id)) {
      tabs = [...tabs, { id: s.id, label: s.window, sessionId: s.id, dir: s.dir }];
    }
    activeId = s.id;
  }
  function closeTab(id: string) {
    tabs = tabs.filter((t) => t.id !== id);
    if (activeId === id) activeId = tabs[0]?.id ?? "";
  }
  const active = $derived(tabs.find((t) => t.id === activeId));

  function onKey(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      paletteOpen = !paletteOpen;
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="layout">
  <Sidebar onselect={openSession} onkill={(s) => (pendingKill = s)} />
  <section class="main">
    <Tabs {tabs} {activeId} onselect={(id) => (activeId = id)} onclose={closeTab} />
    {#if active}
      {#key active.id}
        <Terminal tabId={active.id} sessionId={active.sessionId} />
      {/key}
      <DiffPanel worktreePath={active.dir} />
    {/if}
  </section>
</div>

<CommandPalette open={paletteOpen} {commands} />

<NewAgentDialog
  open={newAgentOpen}
  onsubmit={handleNewAgent}
  oncancel={() => (newAgentOpen = false)}
/>

<ConfirmDialog
  open={pendingKill !== null}
  message={pendingKill ? `Kill agent "${pendingKill.window}"? The session and its agent will be terminated.` : ""}
  confirmLabel="Kill"
  onconfirm={async () => {
    if (pendingKill) {
      await killSession(pendingKill.id);
      pendingKill = null;
    }
  }}
  oncancel={() => (pendingKill = null)}
/>
