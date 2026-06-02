<script lang="ts">
  let {
    open,
    onsubmit,
    oncancel,
  }: {
    open: boolean;
    onsubmit?: (tool: string, projectPath: string, branch: string) => void;
    oncancel?: () => void;
  } = $props();

  let tool = $state("claude");
  let projectPath = $state("");
  let branch = $state("");

  $effect(() => {
    if (open) {
      tool = "claude";
      projectPath = "";
      branch = "";
    }
  });

  function handleCreate() {
    if (!projectPath || !branch) return;
    onsubmit?.(tool, projectPath, branch);
  }

  function handleCancel() {
    oncancel?.();
  }
</script>

{#if open}
  <div role="dialog" aria-label="new agent">
    <label>
      Tool
      <select aria-label="tool" bind:value={tool}>
        <option value="claude">claude</option>
        <option value="opencode">opencode</option>
      </select>
    </label>
    <label>
      Project path
      <input type="text" aria-label="project path" bind:value={projectPath} />
    </label>
    <label>
      Branch
      <input type="text" aria-label="branch" bind:value={branch} />
    </label>
    <button onclick={handleCreate}>Create</button>
    <button onclick={handleCancel}>Cancel</button>
  </div>
{/if}
