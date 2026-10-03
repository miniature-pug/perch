<!-- Test host for Editor.svelte. It mirrors App's code view: a previewable
     path (.md) swaps the Editor out for a placeholder, so the Editor is
     destroyed while its `path` prop already names the next file. -->
<script lang="ts">
  import Editor from "../Editor.svelte";
  let { path = $bindable<string | null>(null), reloadToken = $bindable(0), worktree = "/wt" }:
    { path?: string | null; reloadToken?: number; worktree?: string } = $props();
  export function setPath(p: string | null) { path = p; }
  export function bump() { reloadToken++; }
</script>

{#if !(path ?? "").endsWith(".md")}
  <Editor {path} {worktree} {reloadToken} workspaceId="ws-1" />
{:else}
  <div data-testid="preview-placeholder">preview</div>
{/if}
