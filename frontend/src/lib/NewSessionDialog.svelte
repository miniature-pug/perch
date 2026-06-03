<!-- frontend/src/lib/NewSessionDialog.svelte -->
<script lang="ts">
  let {
    open, repos, loadBranches, onCreate, onClose,
  }: {
    open: boolean; repos: string[];
    loadBranches: (repo: string) => Promise<string[]>;
    onCreate: (agent: string, repo: string, branch: string, model: string) => void;
    onClose: () => void;
  } = $props();

  let agent    = $state("claude");
  let repo     = $state("");
  let branch   = $state("");
  let model    = $state("claude-sonnet-4-5");
  let branches = $state<string[]>([]);

  // Reset dialog fields when opened; load branches per selected repo.
  $effect(() => { if (open) { agent = "claude"; repo = repos[0] ?? ""; model = "claude-sonnet-4-5"; } });

  // Load branches whenever repo changes (and is non-empty).
  $effect(() => {
    const currentRepo = repo;
    if (!currentRepo) { branches = []; branch = ""; return; }
    loadBranches(currentRepo).then((list) => {
      branches = list;
      branch   = list[0] ?? "";
    });
  });

  function handleCreate() {
    if (!repo || !branch) return;
    onCreate(agent, repo, branch, model);
  }
</script>

{#if open}
  <div role="dialog" aria-label="new session" class="dialog-overlay">
    <div class="dialog">
      <label>Agent<select aria-label="agent" bind:value={agent}>
        <option value="claude">Claude</option>
        <option value="opencode">opencode</option>
      </select></label>
      <label>Repo<select aria-label="repo" bind:value={repo}>
        {#each repos as r}<option value={r}>{r}</option>{/each}
      </select></label>
      <label>Branch<select aria-label="branch" bind:value={branch}>
        {#each branches as b}<option value={b}>{b}</option>{/each}
      </select></label>
      <label>Model<input type="text" aria-label="model" value={model} onchange={(e) => { model = (e.target as HTMLInputElement).value; }} /></label>
      <button onclick={handleCreate}>Create</button>
      <button onclick={onClose}>Cancel</button>
    </div>
  </div>
{/if}
