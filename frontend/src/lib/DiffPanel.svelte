<script lang="ts">
  import { diff, type DiffResult } from "./wails";
  let { worktreePath }: { worktreePath: string } = $props();
  let result = $state<DiffResult | null>(null);

  $effect(() => {
    const wt = worktreePath;
    if (!wt) return;
    let cancelled = false;
    diff(wt)
      .then((r) => { if (!cancelled) result = r; })
      .catch(() => { if (!cancelled) result = null; });
    return () => { cancelled = true; }; // stale resolutions are ignored on path change/destroy
  });
</script>

<section aria-label="diff">
  {#if result}
    <header>{result.files} files <span class="add">+{result.added}</span> <span class="del">−{result.removed}</span></header>
    <pre>{result.patch}</pre>
  {/if}
</section>
