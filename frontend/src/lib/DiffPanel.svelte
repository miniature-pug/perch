<script lang="ts">
  import { diff, type DiffResult } from "./wails";
  let { worktreePath }: { worktreePath: string } = $props();
  let result = $state<DiffResult | null>(null);

  $effect(() => {
    if (worktreePath) {
      diff(worktreePath).then((r) => (result = r)).catch(() => (result = null));
    }
  });
</script>

<section aria-label="diff">
  {#if result}
    <header>{result.files} files <span class="add">+{result.added}</span> <span class="del">−{result.removed}</span></header>
    <pre>{result.patch}</pre>
  {/if}
</section>
