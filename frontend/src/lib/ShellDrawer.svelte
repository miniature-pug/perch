<!-- Phase 4a stub — real ShellDrawer implemented in Phase 4b -->
<script lang="ts">
  let { paneId = "shell", cwd = "/" }: { paneId?: string; cwd?: string } = $props();
</script>
<div class="shell-drawer-stub" data-paneid={paneId} aria-label="Shell drawer"></div>
