<script lang="ts">
  let p = $props();
</script>
<div data-testid="editor" data-path={p.path ?? ""} data-worktree={p.worktree}></div>
