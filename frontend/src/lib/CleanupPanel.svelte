<script lang="ts">
  import type { StaleSessionVM } from "./wails";
  import { cleanupSessions } from "./wails";
  import { formatRelativeAge } from "./constants";
  import ConfirmDialog from "./ConfirmDialog.svelte";
  import { trapFocus } from "./actions";

  let {
    sessions,
    onClose,
    onOpen,
  }: {
    sessions: StaleSessionVM[];
    onClose?: () => void;
    onOpen?: (id: string) => void;
  } = $props();

  // Initial selection: the safe rows. The $effect below keeps the selection
  // in sync as `sessions` changes.
  // svelte-ignore state_referenced_locally
  let checked = $state<Set<string>>(new Set(sessions.filter(s => s.safe).map(s => s.id)));
  // Every id the panel has shown. A row seen for the first time gets the
  // default selection (checked when safe); a row the user already saw keeps
  // whatever the user chose (FEX-34).
  // svelte-ignore state_referenced_locally
  const seen = new Set<string>(sessions.map(s => s.id));
  let confirmOpen = $state(false);
  let forceConfirmOpen = $state(false);
  let removing = $state(false);
  let error = $state<string | null>(null);

  // This effect re-derives the checked set from the current `sessions` prop.
  // It drops any checked id that no longer appears in `sessions`, so a removed
  // session never stays in the selection and is never sent again, and it
  // checks a newly arrived safe row by default, like the initial selection.
  $effect(() => {
    const ids = new Set(sessions.map(s => s.id));
    let mutated = false;
    const next = new Set<string>();
    for (const id of checked) {
      if (ids.has(id)) next.add(id);
      else mutated = true;
    }
    for (const s of sessions) {
      if (seen.has(s.id)) continue;
      seen.add(s.id);
      if (s.safe) { next.add(s.id); mutated = true; }
    }
    if (mutated) checked = next;
  });

  const allChecked = $derived(sessions.length > 0 && sessions.every(s => checked.has(s.id)));

  // This splits the checked selection by safety. The normal Remove control
  // only touches safe rows (force=false). This behavior is unchanged. Unsafe
  // rows need the separate force path below, which needs its own explicit
  // confirm. This design makes sure a checked but unsafe row is never removed
  // non-destructively. The destructive git operations, `worktree --force` and
  // `branch -D`, are gated behind their own control and their own
  // confirmation.
  const checkedSafeIds = $derived(sessions.filter(s => checked.has(s.id) && s.safe).map(s => s.id));
  const checkedUnsafeIds = $derived(sessions.filter(s => checked.has(s.id) && !s.safe).map(s => s.id));
  const hasUnsafeSessions = $derived(sessions.some(s => !s.safe));

  function toggleAll() {
    if (allChecked) checked = new Set();
    else checked = new Set(sessions.map(s => s.id));
  }

  function toggleRow(id: string) {
    const next = new Set(checked);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    checked = next;
  }

  async function handleRemove() {
    if (removing) return;
    confirmOpen = false;
    error = null;
    removing = true;
    try {
      // This path only ever removes the safe subset. force is never true here.
      // An unsafe row that is also checked is left for the force control below.
      // This avoids failing, or force-destroying, the whole batch.
      await cleanupSessions(checkedSafeIds, false);
      onClose?.();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      removing = false;
    }
  }

  // This is a separate destructive path. It force-removes only the checked
  // unsafe rows. It discards uncommitted changes (`git worktree remove
  // --force`) and unmerged commits (`git branch -D`). Its own control gates
  // this path. The control stays disabled unless an unsafe row is checked. Its
  // own explicit confirm dialog also gates this path. The normal Remove
  // button, or a single click, can never reach this path.
  async function handleForceRemove() {
    if (removing) return;
    forceConfirmOpen = false;
    error = null;
    removing = true;
    try {
      await cleanupSessions(checkedUnsafeIds, true);
      onClose?.();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      removing = false;
    }
  }

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onClose?.();
  }

  function handleBackdrop(e: MouseEvent) {
    if (e.target === e.currentTarget) onClose?.();
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div class="cleanup-scrim" tabindex="-1" onkeydown={handleKey} onclick={handleBackdrop} use:trapFocus>
<div class="cleanup-panel" role="dialog" aria-modal="true" aria-label="Stale session cleanup">
  <div class="cleanup-header">
    <h2 class="cleanup-title">Stale sessions</h2>
    <button class="cleanup-close" onclick={() => onClose?.()} aria-label="Close">✕</button>
  </div>

  <div class="cleanup-body">
    <table class="cleanup-table">
      <thead>
        <tr>
          <th>
            <input type="checkbox" aria-label="Select all" checked={allChecked} onchange={toggleAll} />
          </th>
          <th>Session</th><th>Branch</th><th>Agent</th><th>Last active</th><th>Diff</th><th>State</th><th></th>
        </tr>
      </thead>
      <tbody>
        {#each sessions as s (s.id)}
          <tr class:unsafe={!s.safe}>
            <td>
              <input type="checkbox" data-testid="row-check-{s.id}" checked={checked.has(s.id)} onchange={() => toggleRow(s.id)} />
            </td>
            <td class="cleanup-title-cell">{s.title}</td>
            <td class="cleanup-branch">{s.branch}</td>
            <td class="cleanup-agent">{s.agent}</td>
            <td class="cleanup-age">{formatRelativeAge(s.lastActive)}</td>
            <td class="cleanup-diff">
              {#if s.added > 0 || s.removed > 0}
                <span class="diff-add">+{s.added}</span>
                <span class="diff-rm">−{s.removed}</span>
              {:else}
                <span class="dim">—</span>
              {/if}
            </td>
            <td class="cleanup-state">
              {#if !s.safe}
                <span class="warn-badge" title={!s.merged ? "Unmerged commits" : "Uncommitted changes"}>⚠</span>
              {:else}
                <span class="clean-badge" title="Clean and merged">✓</span>
              {/if}
            </td>
            <td>
              <button class="cleanup-open-btn" onclick={() => onOpen?.(s.id)}>Open</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>

    {#if error}
      <p class="cleanup-error" role="alert">{error}</p>
    {/if}
  </div>

  <div class="cleanup-footer">
    {#if hasUnsafeSessions}
      <button
        class="cleanup-force-btn"
        disabled={checkedUnsafeIds.length === 0 || removing}
        onclick={() => { forceConfirmOpen = true; }}
        title="Discards uncommitted changes and unmerged commits"
      >
        ⚠ Force remove unsafe ({checkedUnsafeIds.length})
      </button>
    {/if}
    <button class="cleanup-remove-btn" disabled={checkedSafeIds.length === 0 || removing} onclick={() => { confirmOpen = true; }}>
      Remove selected ({checkedSafeIds.length})
    </button>
  </div>

  <ConfirmDialog
    open={confirmOpen}
    message="Remove {checkedSafeIds.length} session{checkedSafeIds.length !== 1 ? 's' : ''}? This deletes the linked worktrees and branches."
    confirmLabel="Remove"
    destructive={true}
    onConfirm={handleRemove}
    onCancel={() => { confirmOpen = false; }}
  />

  <ConfirmDialog
    open={forceConfirmOpen}
    message="Force remove {checkedUnsafeIds.length} session{checkedUnsafeIds.length !== 1 ? 's' : ''}?"
    confirmLabel="Force remove"
    destructive={true}
    note="Discards uncommitted changes and unmerged commits. This cannot be undone."
    onConfirm={handleForceRemove}
    onCancel={() => { forceConfirmOpen = false; }}
  />
</div>
</div>

<style>
  .cleanup-scrim {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-modal);
  }
  .cleanup-panel {
    display: flex; flex-direction: column;
    background: var(--perch-bg); color: var(--perch-text);
    border: 1px solid var(--perch-border); border-radius: var(--perch-radius-lg);
    min-width: 600px; max-width: 900px; max-height: 80vh; overflow: hidden;
    font-family: var(--perch-font-sans); font-size: var(--perch-fs-body);
  }
  .cleanup-header { display: flex; align-items: center; padding: var(--perch-sp-2) var(--perch-sp-3); border-bottom: 1px solid var(--perch-border); gap: var(--perch-sp-2); }
  .cleanup-title { margin: 0; flex: 1; font-size: var(--perch-fs-body); font-weight: 600; }
  .cleanup-close { background: transparent; border: none; color: var(--perch-text-dim); cursor: pointer; font-size: 16px; padding: 2px 6px; border-radius: var(--perch-radius-sm); }
  .cleanup-close:hover { color: var(--perch-text); background: color-mix(in srgb, var(--perch-text) 8%, transparent); }
  .cleanup-body { flex: 1; overflow-y: auto; padding: var(--perch-sp-2) var(--perch-sp-3); }
  .cleanup-table { width: 100%; border-collapse: collapse; font-size: var(--perch-fs-caption); }
  .cleanup-table th { text-align: left; padding: 4px 8px; border-bottom: 1px solid var(--perch-border); color: var(--perch-text-dim); }
  .cleanup-table td { padding: 4px 8px; border-bottom: 1px solid var(--perch-border-strong); }
  .cleanup-table tr.unsafe td { color: var(--perch-text-dim); }
  .cleanup-branch { font-family: var(--perch-font-mono); }
  .diff-add { color: var(--perch-ok); }
  .diff-rm  { color: var(--perch-err); margin-left: 4px; }
  .warn-badge  { color: var(--perch-warn); }
  .clean-badge { color: var(--perch-ok); }
  .dim { color: var(--perch-text-dim); }
  .cleanup-open-btn { background: transparent; border: 1px solid var(--perch-border); color: var(--perch-text-dim); border-radius: var(--perch-radius-sm); padding: 2px 8px; cursor: pointer; font-size: var(--perch-fs-caption); }
  .cleanup-open-btn:hover { border-color: var(--perch-accent); color: var(--perch-accent); }
  .cleanup-footer { display: flex; justify-content: flex-end; align-items: center; gap: var(--perch-sp-2); padding: var(--perch-sp-2) var(--perch-sp-3); border-top: 1px solid var(--perch-border); }
  .cleanup-remove-btn { background: var(--perch-bg); color: var(--perch-err); border: 1px solid var(--perch-err); border-radius: var(--perch-radius-sm); padding: 4px 16px; cursor: pointer; font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); }
  .cleanup-remove-btn:disabled { opacity: var(--perch-opacity-disabled); cursor: not-allowed; }
  .cleanup-remove-btn:hover:not(:disabled) { background: color-mix(in srgb, var(--perch-err) 10%, var(--perch-bg)); }
  /* The force-remove button is visually distinct from the plain Remove button.
     It uses the warn color, the same token as the row's unsafe badge (⚠), so a
     user cannot confuse the two controls at a glance. The footer gap separates
     the two buttons. The force-remove button stays disabled until the user
     checks an unsafe row, so a stray click never triggers it. */
  .cleanup-force-btn { background: var(--perch-bg); color: var(--perch-warn); border: 1px solid var(--perch-warn); border-radius: var(--perch-radius-sm); padding: 4px 16px; cursor: pointer; font-family: var(--perch-font-sans); font-size: var(--perch-fs-body); margin-right: auto; }
  .cleanup-force-btn:disabled { opacity: var(--perch-opacity-disabled); cursor: not-allowed; }
  .cleanup-force-btn:hover:not(:disabled) { background: color-mix(in srgb, var(--perch-warn) 10%, var(--perch-bg)); }
  .cleanup-error { color: var(--perch-err); font-size: var(--perch-fs-caption); margin-top: var(--perch-sp-1); }
</style>
