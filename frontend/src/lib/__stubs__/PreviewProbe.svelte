<script lang="ts">
  let p = $props();
</script>
<div data-testid="preview" data-path={p.path ?? ""} data-kind={p.kind ?? ""}></div>
