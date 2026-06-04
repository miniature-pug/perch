<!-- frontend/src/lib/NewSessionDialog.svelte -->
<script lang="ts">
  let {
    open, repos, loadBranches, onCreate, onClose, initialAgent = null,
  }: {
    open: boolean; repos: string[];
    loadBranches: (repo: string) => Promise<string[]>;
    onCreate: (agent: string, repo: string, branch: string, model: string) => void;
    onClose: () => void;
    initialAgent?: string | null;
  } = $props();

  let agent    = $state("claude");
  let repo     = $state("");
  let branch   = $state("");
  let model    = $state("claude-sonnet-4-5");
  let branches = $state<string[]>([]);

  // Reset dialog fields when opened; load branches per selected repo.
  $effect(() => { if (open) { agent = initialAgent ?? "claude"; repo = repos[0] ?? ""; model = "claude-sonnet-4-5"; } });

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
      <h2>New Session</h2>
      <label class="setting-row">
        <span class="setting-label">Agent</span>
        <select class="field-select" aria-label="agent" bind:value={agent}>
          <option value="claude">Claude</option>
          <option value="opencode">opencode</option>
        </select>
      </label>
      <label class="setting-row">
        <span class="setting-label">Repo</span>
        <select class="field-select" aria-label="repo" bind:value={repo}>
          {#each repos as r}<option value={r}>{r}</option>{/each}
        </select>
      </label>
      <label class="setting-row">
        <span class="setting-label">Branch</span>
        <select class="field-select" aria-label="branch" bind:value={branch}>
          {#each branches as b}<option value={b}>{b}</option>{/each}
        </select>
      </label>
      <label class="setting-row">
        <span class="setting-label">Model</span>
        <input class="field-input" type="text" aria-label="model" value={model} onchange={(e) => { model = (e.target as HTMLInputElement).value; }} />
      </label>
      <div class="dialog-actions">
        <button class="btn btn-primary" onclick={handleCreate}>Create</button>
        <button class="btn" onclick={onClose}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Overlay scrim */
  .dialog-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 200;
  }

  /* Modal card */
  .dialog {
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border);
    border-radius: 6px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.45);
    padding: var(--perch-sp-3);
    min-width: 480px;
    max-width: 560px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  /* Section heading */
  .dialog h2 {
    font-size: var(--perch-fs-body);
    font-weight: 600;
    margin: 0 0 var(--perch-sp-2) 0;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
    padding-bottom: 4px;
  }

  /* Label + field rows */
  .setting-row {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-2);
    margin-bottom: var(--perch-sp-1);
  }

  .setting-label {
    min-width: 60px;
    color: var(--perch-text);
    font-size: var(--perch-fs-body);
    flex-shrink: 0;
  }

  /* Select field */
  .field-select {
    flex: 1;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    padding: 3px 8px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    box-sizing: border-box;
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .field-select:focus {
    outline: 2px solid var(--perch-accent);
    outline-offset: 0;
    border-color: var(--perch-accent);
  }

  /* Text input */
  .field-input {
    flex: 1;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    padding: 3px 8px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    box-sizing: border-box;
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .field-input:focus {
    outline: 2px solid var(--perch-accent);
    outline-offset: 0;
    border-color: var(--perch-accent);
  }

  .field-input::placeholder {
    color: var(--perch-text-dim);
  }

  /* Action row — right-aligned */
  .dialog-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: var(--perch-sp-1);
    margin-top: var(--perch-sp-2);
    padding-top: var(--perch-sp-1);
    border-top: 1px solid var(--perch-border);
  }

  /* Shared button base */
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: border-color var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .btn:hover {
    border-color: var(--perch-accent);
    color: var(--perch-accent);
  }

  .btn:active {
    background: color-mix(in srgb, var(--perch-accent) 12%, var(--perch-bg));
  }

  .btn:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    pointer-events: none;
  }

  /* Create — primary accent fill */
  .btn-primary {
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:hover {
    filter: brightness(1.1);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:active {
    filter: brightness(0.92);
  }
</style>
