<!-- frontend/src/lib/NewSessionDialog.svelte -->
<script lang="ts">
  import { untrack } from "svelte";
  import { DEFAULT_AGENT, AGENT_CLAUDE, AGENT_OPENCODE } from "./constants";
  import { trapFocus } from "./actions";
  import type { RepoInfo } from "./wails";

  // Friendly label for a repo option: "name · branch" when a RepoInfo is known
  // for this path, otherwise the raw path (e.g. a workspace-derived worktree
  // path with no discoverRepos() match). The option VALUE always stays the path.
  function repoLabel(path: string, info: Record<string, RepoInfo> | undefined): string {
    const r = info?.[path];
    return r ? `${r.name} · ${r.branch}` : path;
  }

  const SLUG_RE = /^[A-Za-z0-9._\/-]+$/;

  // Well-known default-branch names, in priority order. Used to pin the repo's
  // likely default branch to the top of the branch lists and to default the
  // base ref to it. The backend already returns the default/current branch
  // first (F30-be); this is a belt-and-suspenders guard on the frontend.
  const DEFAULT_BRANCH_NAMES = ["main", "master"];

  // Slugify a free-text session Name into a branch-safe suffix: lowercased, each
  // run of non-alphanumeric characters collapsed to a single hyphen, ends
  // trimmed. Returns "" when the Name has no usable characters.
  function slugifyName(name: string): string {
    return name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "");
  }

  // Return `base` if it is not already taken, otherwise the first `${base}-N`
  // (N starting at 2) that is free — so a second default session can never
  // collide with the first.
  function uniqueBranch(base: string, taken: string[]): string {
    if (!taken.includes(base)) return base;
    let n = 2;
    while (taken.includes(`${base}-${n}`)) n++;
    return `${base}-${n}`;
  }

  // Suggest a unique branch name. When a Name is given it drives the branch
  // (`${agent}/${slug(name)}`); otherwise it falls back to `${agent}/work`.
  // Either way the result is de-duplicated against the loaded branch list.
  function suggestBranch(agent: string, name: string, taken: string[]): string {
    const s = slugifyName(name);
    const base = s ? `${agent}/${s}` : `${agent}/work`;
    return uniqueBranch(base, taken);
  }

  function slugValid(name: string): boolean {
    return SLUG_RE.test(name);
  }

  // The repo's likely default branch: the first well-known name present in the
  // list, else the first entry (the backend already sorts the default first).
  function pickDefaultBranch(list: string[]): string {
    for (const known of DEFAULT_BRANCH_NAMES) {
      if (list.includes(known)) return known;
    }
    return list[0] ?? "";
  }

  // Move the default branch to the front so both branch dropdowns and the
  // base-ref default surface it first.
  function pinDefaultFirst(list: string[]): string[] {
    const def = pickDefaultBranch(list);
    if (!def || list[0] === def) return list;
    return [def, ...list.filter((b) => b !== def)];
  }

  let {
    open, repos, repoInfo = {}, loadBranches, onCreate, onClose, initialAgent = null, error = null,
  }: {
    open: boolean;
    repos: string[];
    // Path → RepoInfo lookup for friendly repo labels (F#5). Entries are optional —
    // paths with no match (e.g. workspace-derived worktree paths) fall back to
    // showing the raw path.
    repoInfo?: Record<string, RepoInfo>;
    loadBranches: (repo: string) => Promise<string[]>;
    onCreate: (agent: string, repo: string, baseRef: string, branch: string, title: string, worktree: boolean) => void;
    onClose: () => void;
    initialAgent?: string | null;
    // Optional inline error (e.g. a humanized "branch already exists" message),
    // rendered above the action row. App.svelte feeds it after a failed create.
    error?: string | null;
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
  // True once the user edits the branch-name field directly, which stops the
  // auto-suggestion from clobbering their choice (agent/Name/branch-list changes
  // no longer overwrite it). Reset on each open.
  let branchTouched = $state(false);
  // True while an async loadBranches for the current repo is in flight. Blocks
  // Create in new-branch mode so a fast repo switch can't submit a stale baseRef.
  let branchesLoading = $state(false);

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
      branchTouched = false;
      branchName  = suggestBranch(agent, "", branches);
      branchSel   = "";
      baseRef     = "";
    });
  });

  // Keep the suggested new-branch name in sync with the agent, the Name field,
  // and the loaded branch list — until the user edits the branch field, after
  // which `branchTouched` freezes their choice. Reading agent/name/branches only
  // inside the guard means a user edit stops all further auto-suggestion.
  $effect(() => {
    if (worktree && !useExisting && !branchTouched) {
      branchName = suggestBranch(agent, name, branches);
    }
  });

  // Load branches when repo changes; cancellation guard prevents stale resolves.
  // The branch-derived fields (branches/baseRef/branchSel) are cleared
  // SYNCHRONOUSLY here, before the async load, so a fast repo switch can never
  // leave the previous repo's baseRef in place while the new list is pending.
  $effect(() => {
    const currentRepo = repo;
    // Clear immediately so no stale branch data survives the switch.
    untrack(() => {
      branches  = [];
      baseRef   = "";
      branchSel = "";
    });
    if (!currentRepo) { branchesLoading = false; return; }
    branchesLoading = true;
    let cancelled = false;
    loadBranches(currentRepo).then((list) => {
      if (cancelled) return;
      // Pin the default branch first so the base-ref default and both branch
      // dropdowns surface it rather than the alphabetically-first branch.
      const ordered = pinDefaultFirst(list ?? []);
      branches  = ordered;
      baseRef   = ordered[0] ?? "";
      branchSel = ordered[0] ?? "";
      branchesLoading = false;
    }).catch(() => {
      if (cancelled) return;
      branchesLoading = false;
    });
    return () => { cancelled = true; };
  });

  // Derived validity
  const nameValid  = $derived(!worktree || useExisting || slugValid(branchName));
  // In new-branch mode a pending branch load means baseRef is not yet settled, so
  // Create is blocked until the load resolves (prevents submitting a stale baseRef).
  const newBranchReady = $derived(!branchesLoading);
  const canCreate  = $derived(
    !!repo &&
    (!worktree || useExisting
      ? !!branchSel
      : (!!branchName && nameValid && newBranchReady))
  );

  async function handleCreate() {
    if (!canCreate || submitting) return;
    // Trim the optional session name; a whitespace-only value is treated as empty.
    const title = name.trim();
    submitting = true;
    try {
      if (!worktree) {
        // non-worktree: baseRef="" always
        await Promise.resolve(onCreate(agent, repo, "", branchSel, title, false));
      } else if (useExisting) {
        // existing branch: baseRef="" signals no -b
        await Promise.resolve(onCreate(agent, repo, "", branchSel, title, true));
      } else {
        // new branch from baseRef
        await Promise.resolve(onCreate(agent, repo, baseRef, branchName, title, true));
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
       tabindex="-1" onkeydown={handleKey} use:trapFocus={"[aria-label='repo']"}>
    <div class="dialog">
      <h2>New session</h2>

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
        <select class="field-select" aria-label="repo" bind:value={repo}>
          {#each repos as r}<option value={r}>{repoLabel(r, repoInfo)}</option>{/each}
        </select>
      </label>

      <!-- Worktree toggle -->
      <label class="setting-row">
        <span class="setting-label">Worktree</span>
        <div class="branch-new-col">
          <input type="checkbox" aria-label="worktree" bind:checked={worktree} />
          <span class="field-hint">
            {worktree
              ? "Runs in an isolated git worktree."
              : "Operates directly in the repo. Needs a clean working tree."}
          </span>
        </div>
      </label>

      {#if branchesLoading}
        <!-- Branch list in flight: show a placeholder so the empty selects read
             as loading rather than broken, and Create stays blocked (F43). -->
        <div class="setting-row">
          <span class="setting-label"></span>
          <span class="field-hint" aria-live="polite">Loading branches…</span>
        </div>
      {/if}

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
                oninput={() => (branchTouched = true)}
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

      {#if error}
        <p class="field-error dialog-error" role="alert">{error}</p>
      {/if}

      <div class="dialog-actions">
        <button class="btn btn-primary" onclick={handleCreate} disabled={!canCreate || submitting}>
          {submitting ? "Creating…" : "Create"}
        </button>
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

  .dialog-error {
    margin: var(--perch-sp-2) 0 0 0;
    padding: 0;
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
