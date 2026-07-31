<!-- frontend/src/lib/NewSessionDialog.svelte -->
<script lang="ts">
  import { untrack } from "svelte";
  import { DEFAULT_AGENT, AGENT_CLAUDE, AGENT_OPENCODE } from "./constants";
  import { focusOnMount } from "./actions";

  const SLUG_RE = /^[A-Za-z0-9._\/-]+$/;

  function suggestBranch(agent: string): string {
    return `${agent}/work`;
  }

  function slugValid(name: string): boolean {
    return SLUG_RE.test(name);
  }

  let {
    open, repos, loadBranches, onCreate, onClose, initialAgent = null,
  }: {
    open: boolean;
    repos: string[];
    loadBranches: (repo: string) => Promise<string[]>;
    onCreate: (agent: string, repo: string, baseRef: string, branch: string, title: string, worktree: boolean) => void;
    onClose: () => void;
    initialAgent?: string | null;
  } = $props();

  let name        = $state("");
  let agent       = $state(DEFAULT_AGENT);
  let repo        = $state("");
  let worktree    = $state(true);
  let useExisting = $state(false);
  let baseRef     = $state("");
  let branchName  = $state("");
  let branchSel   = $state("");
  let branches    = $state<string[]>([]);
  let submitting  = $state(false);

  // Reset dialog state when opened. Wrapped in untrack so the write to `agent`
  // (and subsequent reads of `agent` inside suggestBranch) do not register
  // this effect as a subscriber to `agent` — otherwise any user change to the
  // agent dropdown would re-fire this effect and snap agent back to DEFAULT_AGENT.
  $effect(() => {
    if (open) untrack(() => {
      name        = "";
      agent       = initialAgent ?? DEFAULT_AGENT;
      repo        = repos[0] ?? "";
      worktree    = true;
      useExisting = false;
      branchName  = suggestBranch(agent);
      branchSel   = "";
      baseRef     = "";
    });
  });

  // Update branch suggestion when agent changes.
  $effect(() => {
    if (worktree && !useExisting) {
      branchName = suggestBranch(agent);
    }
  });

  // Load branches when repo changes; cancellation guard prevents stale resolves.
  $effect(() => {
    const currentRepo = repo;
    if (!currentRepo) { branches = []; baseRef = ""; branchSel = ""; return; }
    let cancelled = false;
    loadBranches(currentRepo).then((list) => {
      if (cancelled) return;
      branches = list ?? [];
      baseRef  = branches[0] ?? "";
      branchSel = branches[0] ?? "";
    });
    return () => { cancelled = true; };
  });

  // Derived validity
  const nameValid  = $derived(!worktree || useExisting || slugValid(branchName));
  const canCreate  = $derived(
    !!repo &&
    (!worktree || useExisting ? !!branchSel : (!!branchName && nameValid))
  );

  async function handleCreate() {
    if (!canCreate || submitting) return;
    submitting = true;
    try {
      if (!worktree) {
        // non-worktree: baseRef="" always
        await Promise.resolve(onCreate(agent, repo, "", branchSel, name, false));
      } else if (useExisting) {
        // existing branch: baseRef="" signals no -b
        await Promise.resolve(onCreate(agent, repo, "", branchSel, name, true));
      } else {
        // new branch from baseRef
        await Promise.resolve(onCreate(agent, repo, baseRef, branchName, name, true));
      }
    } finally {
      submitting = false;
    }
  }

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }
</script>

{#if open}
  <div role="dialog" aria-modal="true" aria-label="new session" class="dialog-overlay"
       tabindex="-1" onkeydown={handleKey}>
    <div class="dialog">
      <h2>New Session</h2>

      <!-- Name (optional user-chosen session label) -->
      <div class="setting-row">
        <span class="setting-label">Name</span>
        <div class="branch-new-col">
          <input
            class="field-input"
            type="text"
            aria-label="name"
            bind:value={name}
          />
          <span class="field-hint">Optional. The repo, branch, and agent are shown next to the name.</span>
        </div>
      </div>

      <!-- Repo -->
      <label class="setting-row">
        <span class="setting-label">Repo</span>
        <select class="field-select" aria-label="repo" bind:value={repo} use:focusOnMount>
          {#each repos as r}<option value={r}>{r}</option>{/each}
        </select>
      </label>

      <!-- Worktree toggle -->
      <label class="setting-row">
        <span class="setting-label">Worktree</span>
        <input type="checkbox" aria-label="worktree" bind:checked={worktree} />
      </label>

      {#if worktree}
        <!-- Starting point (base-ref) — visible only in new-branch mode -->
        {#if !useExisting}
          <label class="setting-row">
            <span class="setting-label">Starting point</span>
            <select class="field-select" aria-label="starting point" bind:value={baseRef}>
              {#each branches as b}<option value={b}>{b}</option>{/each}
            </select>
          </label>
        {/if}

        <!-- Branch — new-name text input or existing dropdown -->
        {#if !useExisting}
          <div class="setting-row">
            <span class="setting-label">Branch</span>
            <div class="branch-new-col">
              <input
                class="field-input"
                type="text"
                aria-label="branch name"
                bind:value={branchName}
              />
              {#if branchName && !nameValid}
                <span class="field-error">Branch name contains invalid characters</span>
              {/if}
            </div>
          </div>
        {:else}
          <label class="setting-row">
            <span class="setting-label">Branch</span>
            <select class="field-select" aria-label="branch" bind:value={branchSel}>
              {#each branches as b}<option value={b}>{b}</option>{/each}
            </select>
          </label>
        {/if}

        <!-- Use existing branch sub-toggle -->
        <div class="setting-row">
          <span class="setting-label"></span>
          <label class="sub-toggle">
            <input type="checkbox" aria-label="use existing branch" bind:checked={useExisting} />
            <span>Use existing branch</span>
          </label>
        </div>
      {:else}
        <!-- Non-worktree: single branch dropdown -->
        <label class="setting-row">
          <span class="setting-label">Branch</span>
          <select class="field-select" aria-label="branch" bind:value={branchSel}>
            {#each branches as b}<option value={b}>{b}</option>{/each}
          </select>
        </label>
      {/if}

      <!-- Agent -->
      <label class="setting-row">
        <span class="setting-label">Agent</span>
        <select class="field-select" aria-label="agent" bind:value={agent}>
          <option value={AGENT_CLAUDE}>Claude</option>
          <option value={AGENT_OPENCODE}>opencode</option>
        </select>
      </label>

      <div class="dialog-actions">
        <button class="btn btn-primary" onclick={handleCreate} disabled={!canCreate || submitting}>Create</button>
        <button class="btn" onclick={onClose}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .dialog-overlay {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-modal);
  }

  .dialog {
    background: var(--perch-surface);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-lg);
    box-shadow: var(--perch-shadow-float);
    padding: var(--perch-sp-3);
    min-width: 480px;
    max-width: 560px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  .dialog h2 {
    font-size: var(--perch-fs-body);
    font-weight: 600;
    margin: 0 0 var(--perch-sp-2) 0;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
    padding-bottom: 4px;
  }

  .setting-row {
    display: flex;
    align-items: flex-start;
    gap: var(--perch-sp-2);
    margin-bottom: var(--perch-sp-1);
  }

  .setting-label {
    min-width: 100px;
    padding-top: 3px;
    color: var(--perch-text);
    font-size: var(--perch-fs-body);
    flex-shrink: 0;
  }

  .field-select {
    flex: 1;
    min-width: 0;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
    padding: 3px 8px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    box-sizing: border-box;
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .field-select option {
    background: var(--perch-bg);
    color: var(--perch-text);
  }

  .field-select:focus {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 0;
    border-color: var(--perch-accent);
  }

  .field-input {
    flex: 1;
    min-width: 0;
    width: 100%;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
    padding: 3px 8px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    box-sizing: border-box;
    transition: border-color var(--perch-dur) var(--perch-ease);
  }

  .field-input:focus {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 0;
    border-color: var(--perch-accent);
  }

  .field-input::placeholder {
    color: var(--perch-text-dim);
  }

  .branch-new-col {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .field-error {
    font-size: var(--perch-fs-body);
    color: var(--perch-err);
  }

  .field-hint {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
  }

  .sub-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--perch-fs-body);
    color: var(--perch-text-dim);
    cursor: pointer;
    user-select: none;
  }

  .dialog-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: var(--perch-sp-1);
    margin-top: var(--perch-sp-2);
    padding-top: var(--perch-sp-1);
    border-top: 1px solid var(--perch-border);
  }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
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
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  .btn:disabled {
    opacity: var(--perch-opacity-disabled);
    cursor: not-allowed;
    pointer-events: none;
  }

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
