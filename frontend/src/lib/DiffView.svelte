<!-- frontend/src/lib/DiffView.svelte -->
<script lang="ts">
  import { diffStat, hunks as fetchHunks, stageHunk, discardHunk, type FileDiff, type Hunk } from "./wails";

  let { worktree }: { worktree: string } = $props();

  const STATUS_LABELS: Record<string, { icon: string; label: string }> = {
    M: { icon: "✎", label: "modified" },
    A: { icon: "+", label: "added" },
    D: { icon: "−", label: "deleted" },
    R: { icon: "→", label: "renamed" },
    "?": { icon: "?", label: "untracked" },
  };

  let files    = $state<FileDiff[]>([]);
  let expanded = $state<Record<string, Hunk[]>>({});
  let loading  = $state(false);

  $effect(() => {
    const wt = worktree;
    loading = true;
    diffStat(wt).then((r) => { files = r; loading = false; }).catch(() => { loading = false; });
  });

  async function toggleFile(f: FileDiff) {
    if (expanded[f.path]) {
      const next = { ...expanded }; delete next[f.path]; expanded = next;
    } else {
      expanded = { ...expanded, [f.path]: await fetchHunks(worktree, f.path) };
    }
  }

  async function stage(h: Hunk) {
    await stageHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
  }

  async function discard(h: Hunk) {
    await discardHunk(worktree, h.file, h.index);
    expanded = { ...expanded, [h.file]: await fetchHunks(worktree, h.file) };
  }
</script>

<section aria-label="diff view" class="diff-view">
  {#if loading}
    <p class="dim">Loading…</p>
  {:else if files.length === 0}
    <p class="dim">No changes</p>
  {:else}
    <ul class="file-list">
      {#each files as f (f.path)}
        {@const st = STATUS_LABELS[f.status] ?? { icon: "?", label: f.status }}
        <li class="file-row">
          <button class="file-toggle" aria-expanded={!!expanded[f.path]}
            onclick={() => toggleFile(f)} aria-label={f.path}>
            <span aria-hidden="true">{st.icon}</span>
            <span class="file-path">{f.path}</span>
            <span class="stat-label">{st.label}</span>
            <span class="add">+{f.added}</span>
            <span class="del">-{f.removed}</span>
          </button>
          {#if expanded[f.path]}
            <div class="hunk-list">
              {#each expanded[f.path] as h (h.index)}
                <div class="hunk">
                  <pre class="hunk-header">{h.header}</pre>
                  <pre class="hunk-body">{#each h.lines as l}<span class="line line-{l.kind}">{l.text}{"\n"}</span>{/each}</pre>
                  <div class="hunk-actions">
                    <button onclick={() => stage(h)}>Stage hunk</button>
                    <button onclick={() => discard(h)}>Discard hunk</button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
