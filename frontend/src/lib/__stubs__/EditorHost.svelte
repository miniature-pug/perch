<!-- Test host for Editor.svelte. It mirrors App's code view: a previewable
     path (.md) shows a preview placeholder while the Editor stays mounted
     and hidden, so unsaved edits survive the swap (review #3). -->
<script lang="ts">
  import Editor from "../Editor.svelte";
  let { path = $bindable<string | null>(null), reloadToken = $bindable(0), worktree = "/wt" }:
    { path?: string | null; reloadToken?: number; worktree?: string } = $props();
  export function setPath(p: string | null) { path = p; }
  export function bump() { reloadToken++; }
  const previewing = $derived((path ?? "").endsWith(".md"));
</script>

{#if previewing}
  <div data-testid="preview-placeholder">preview</div>
{/if}
<div style:display={previewing ? "none" : "contents"}>
  <Editor {path} {worktree} {reloadToken} workspaceId="ws-1" visible={!previewing} />
</div>
